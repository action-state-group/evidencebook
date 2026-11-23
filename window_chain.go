// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	emit "github.com/action-state-group/capsule-emit/go"
)

// WindowChainVersion is the v member of a window chain document: 2 when a
// window carries its boundary (a store with a grace, evidencebook#807), 1
// otherwise. A verifier takes both.
const WindowChainVersion = 2

// WindowChain is a run of consecutive closed windows of one windowed shelf,
// handed over with nothing else (evidencebook#689 §5; v 1, or 2 when a
// window carries its boundary, evidencebook#807): each window's open and
// close records with their inclusion proofs under its final checkpoint, that
// checkpoint, and for a window that had a grace its boundary checkpoint, a
// consistency proof to the final one and every record after the boundary.
// VerifyWindowChain checks it.
type WindowChain struct {
	V      int          `json:"v"`
	Base   string       `json:"base"`
	Period WindowPeriod `json:"period"`
	Since  string       `json:"since"`
	From   string       `json:"from"`
	To     string       `json:"to"`
	// Windows are the windows From through To, in order.
	Windows []ChainWindow `json:"windows"`
}

// ChainWindow is one window of a WindowChain.
type ChainWindow struct {
	Label string `json:"label"`
	LogID string `json:"log_id"`
	// Open and Close are the window's first and last records as sealed:
	// canonical header, Capsule and Producer Envelope.
	Open  StoredRecord `json:"open"`
	Close StoredRecord `json:"close"`
	// OpenProof and CloseProof are cll-inclusion-proof octets under
	// FinalCheckpoint.
	OpenProof  []byte `json:"open_proof"`
	CloseProof []byte `json:"close_proof"`
	// FinalCheckpoint is the signed statement of a checkpoint of the
	// window's final tree.
	FinalCheckpoint []byte `json:"final_checkpoint"`
	// Receipts are witness receipts of FinalCheckpoint in the bundle form
	// (checkpoint.witnesses), when the holder has them.
	Receipts []json.RawMessage `json:"receipts,omitempty"`
	// Boundary is the tree the window held when it ended, for a window with
	// a grace: the next window's open record names it, and the final tree
	// must extend it with only late Closes and index roots.
	Boundary *ChainBoundary `json:"boundary,omitempty"`
}

// ChainBoundary is a window's boundary in a chain (evidencebook#807): the
// boundary checkpoint, a CLL consistency proof from it to the final
// checkpoint, and every record after it up to the close record.
type ChainBoundary struct {
	Checkpoint  []byte            `json:"checkpoint"`
	Receipts    []json.RawMessage `json:"receipts,omitempty"`
	Consistency []byte            `json:"consistency"`
	// Tail is every record after the boundary's last leaf up to the one
	// before the close record, in position order, each with its inclusion
	// proof under the final checkpoint.
	Tail []ChainTail `json:"tail"`
}

// ChainTail is one record of a boundary's tail.
type ChainTail struct {
	Record StoredRecord `json:"record"`
	Proof  []byte       `json:"proof"`
}

// WindowChainOptions selects what Shelf.WindowChain carries.
type WindowChainOptions struct {
	// Receipts returns the witness receipts the host holds for a window's
	// final or boundary checkpoint, in the bundle form; nil carries none. It runs under the
	// shelf's lock and must not call back into the Shelf.
	Receipts func(ctx context.Context, final Checkpoint) ([]json.RawMessage, error)
}

// WindowChain rolls the shelf and returns the chain of windows from through
// to (labels, inclusive). Every window in the range must be closed: an open
// window has no final tree yet.
func (s *Shelf) WindowChain(ctx context.Context, from, to string, opts WindowChainOptions) (WindowChain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return WindowChain{}, ErrReleased
	}
	if err := s.windowed(); err != nil {
		return WindowChain{}, err
	}
	policy := s.cfg.Window
	first, err := policy.Period.ParseWindow(from)
	if err != nil {
		return WindowChain{}, err
	}
	last, err := policy.Period.ParseWindow(to)
	if err != nil {
		return WindowChain{}, err
	}
	if last.Start.Before(first.Start) {
		return WindowChain{}, fmt.Errorf("%w: window %s is before %s", ErrInvalid, to, from)
	}
	if _, err := s.rollLocked(ctx); err != nil {
		return WindowChain{}, err
	}
	rows, err := s.windowRowsLocked(ctx)
	if err != nil {
		return WindowChain{}, err
	}
	byLabel := make(map[string]windowRow, len(rows))
	for _, row := range rows {
		byLabel[row.Label] = row
	}
	chain := WindowChain{V: WindowChainVersion, Base: policy.Base, Period: policy.Period, Since: windowTime(policy.Since), From: from, To: to}
	for w := first; !w.Start.After(last.Start); w = policy.Period.Next(w) {
		row, ok := byLabel[w.Label]
		if !ok {
			return WindowChain{}, fmt.Errorf("%w: window %s has no book on this shelf", ErrNotFound, w.Label)
		}
		if row.inGrace() {
			return WindowChain{}, fmt.Errorf("%w: window %s is in grace until %s; no final checkpoint yet", ErrInGrace, w.Label, windowTime(w.End.Add(policy.Grace)))
		}
		if row.state != windowClosed {
			return WindowChain{}, fmt.Errorf("%w: window %s is still open: it has no final tree yet", ErrInvalid, w.Label)
		}
		entry, err := s.chainWindowLocked(ctx, row, opts)
		if err != nil {
			return WindowChain{}, err
		}
		chain.Windows = append(chain.Windows, entry)
	}
	chain.V = 1
	for _, w := range chain.Windows {
		if w.Boundary != nil {
			chain.V = WindowChainVersion
		}
	}
	return chain, nil
}

func (s *Shelf) chainWindowLocked(ctx context.Context, row windowRow, opts WindowChainOptions) (ChainWindow, error) {
	book, err := s.openLocked(ctx, row.logID)
	if err != nil {
		return ChainWindow{}, err
	}
	book.mu.Lock()
	log, final := book.logLocked(), book.window.final
	book.mu.Unlock()
	if len(log) < 2 || uint64(len(log)) != final.Entries {
		return ChainWindow{}, fmt.Errorf("%w: closed window %s holds %d records, its final tree %d", ErrCorrupt, row.Label, len(log), final.Entries)
	}
	cllLog, ok := book.commitments.(*CLLSubstrate)
	if !ok {
		return ChainWindow{}, fmt.Errorf("%w: window %s is not on a CLL log", ErrUnsupported, row.Label)
	}
	prover, err := cllLog.cllProver(ctx, final)
	if err != nil {
		return ChainWindow{}, err
	}
	out := ChainWindow{Label: row.Label, LogID: row.logID, FinalCheckpoint: bytes.Clone(final.Statement)}
	for _, end := range []struct {
		record *StoredRecord
		proof  *[]byte
		seq    uint64
	}{{&out.Open, &out.OpenProof, 1}, {&out.Close, &out.CloseProof, final.Entries}} {
		if *end.record, err = book.store.GetRecord(ctx, log[end.seq-1].RecordID); err != nil {
			return ChainWindow{}, err
		}
		if *end.proof, err = prover.inclusionProof(end.seq); err != nil {
			return ChainWindow{}, err
		}
	}
	if opts.Receipts != nil {
		if out.Receipts, err = opts.Receipts(ctx, final); err != nil {
			return ChainWindow{}, err
		}
	}
	if row.boundaryEntries > 0 {
		if out.Boundary, err = chainBoundary(ctx, book, prover, row, log, final, opts); err != nil {
			return ChainWindow{}, err
		}
	}
	return out, nil
}

// chainBoundary builds a window's boundary entry: its checkpoint, the
// consistency proof to the final checkpoint, and the tail's records with
// their proofs under it.
func chainBoundary(ctx context.Context, book *Book, prover *cllProver, row windowRow, log []Record, final Checkpoint, opts WindowChainOptions) (*ChainBoundary, error) {
	boundary, err := finalCheckpoint(ctx, book, row.boundaryEntries, row.boundaryRoot)
	if err != nil {
		return nil, err
	}
	consistency, err := prover.consistencyProof(boundary.TreeSize, final.TreeSize)
	if err != nil {
		return nil, err
	}
	out := &ChainBoundary{Checkpoint: bytes.Clone(boundary.Statement), Consistency: consistency, Tail: []ChainTail{}}
	if opts.Receipts != nil {
		if out.Receipts, err = opts.Receipts(ctx, boundary); err != nil {
			return nil, err
		}
	}
	for seq := boundary.Entries + 1; seq < final.Entries; seq++ {
		stored, err := book.store.GetRecord(ctx, log[seq-1].RecordID)
		if err != nil {
			return nil, err
		}
		proof, err := prover.inclusionProof(seq)
		if err != nil {
			return nil, err
		}
		out.Tail = append(out.Tail, ChainTail{Record: stored, Proof: proof})
	}
	return out, nil
}

// VerifyChainOptions are what a verifier of a window chain brings: the
// holder's checkpoint key, and, where the agreement names witnesses, their
// directory, under which every window's final checkpoint must carry a
// verifying receipt.
type VerifyChainOptions struct {
	// CheckpointKey is the key id every final checkpoint must be signed by.
	CheckpointKey string
	// Witnesses, when non-empty, makes every window need at least one
	// receipt, and every receipt carried must verify under it.
	Witnesses []WitnessRow
}

// VerifiedWindow is one window a chain proved: its bounds and final tree.
type VerifiedWindow struct {
	Window
	Final WindowTree `json:"final"`
	// Boundary is the tree the window held when it ended and entered its
	// grace, the one the next window's open record names; nil with no grace.
	Boundary *WindowTree `json:"boundary,omitempty"`
	// RecordKey is the hex public key that signed the window's records.
	RecordKey string `json:"record_key"`
}

// ChainFinding is one failure of a window chain or a report's coverage,
// named so a reader sees which window failed and how.
type ChainFinding struct {
	Window string `json:"window,omitempty"`
	Text   string `json:"text"`
}

func (f ChainFinding) String() string { return f.Text }

// VerifyWindowChain checks a window chain with nothing but itself and the
// options: each final checkpoint's signature, key and log; the open record
// at the first leaf and the close record at the last; the labels consecutive
// from the policy's first window; each open record's bounds its label's; each
// previous the prior window's boundary tree when it had one, else its final
// tree; and for a window with a boundary, the one its close record names,
// that the final tree extends it, and that every record after it is a
// close/v1 for a period inside the window or an index root. It returns the windows proved
// and a finding for each failure; a chain with any finding proves nothing
// about its range. What a chain never shows: that an answer holds every row
// of its window, or, without witnesses, that a window was not re-cut and the
// later ones re-signed by the holder's one key, or that a holder did not
// delay a window's boundary past its end to place ordinary records before it.
func VerifyWindowChain(chain WindowChain, opts VerifyChainOptions) ([]VerifiedWindow, []ChainFinding) {
	var findings []ChainFinding
	fail := func(window, format string, args ...any) {
		findings = append(findings, ChainFinding{Window: window, Text: fmt.Sprintf(format, args...)})
	}
	if chain.V != 1 && chain.V != WindowChainVersion {
		fail("", "window chain version %d is not 1 or %d", chain.V, WindowChainVersion)
		return nil, findings
	}
	if !chain.Period.valid() {
		fail("", "window period %q is not one of day, week, month, quarter, year", chain.Period)
		return nil, findings
	}
	since, err := time.Parse(time.RFC3339, chain.Since)
	if err != nil || !since.Equal(chain.Period.start(since)) {
		fail("", "since %q is not the start of a %s", chain.Since, chain.Period)
		return nil, findings
	}
	first, err := chain.Period.ParseWindow(chain.From)
	if err != nil {
		fail("", "from: %v", err)
		return nil, findings
	}
	last, err := chain.Period.ParseWindow(chain.To)
	if err != nil {
		fail("", "to: %v", err)
		return nil, findings
	}
	if first.Start.Before(since) {
		fail(chain.From, "window %s is before the chain began (%s)", chain.From, chain.Since)
	}
	if last.Start.Before(first.Start) {
		fail("", "the chain's range %s..%s is empty", chain.From, chain.To)
		return nil, findings
	}
	var verified []VerifiedWindow
	var prior *VerifiedWindow
	expected := first
	for i, cw := range chain.Windows {
		if expected.Start.After(last.Start) {
			fail(cw.Label, "window %s is past the chain's end %s", cw.Label, chain.To)
			break
		}
		if cw.Label != expected.Label {
			fail(expected.Label, "chain breaks at %s: the next window carried is %s", expected.Label, cw.Label)
			return verified, findings
		}
		v, ok := verifyChainWindow(chain, cw, expected, since, prior, opts, fail)
		if !ok {
			return verified, findings
		}
		if i > 0 && v.RecordKey != verified[0].RecordKey {
			fail(cw.Label, "window %s's records are signed by another key than the chain's", cw.Label)
		}
		verified = append(verified, v)
		prior = &verified[len(verified)-1]
		expected = chain.Period.Next(expected)
	}
	if !expected.Start.After(last.Start) {
		fail(expected.Label, "chain breaks at %s: the chain ends before %s", expected.Label, chain.To)
	}
	return verified, findings
}

// verifyChainWindow checks one window against what precedes it, reporting
// through fail; ok is false when the window is not proved.
func verifyChainWindow(chain WindowChain, cw ChainWindow, w Window, since time.Time, prior *VerifiedWindow, opts VerifyChainOptions, fail func(string, string, ...any)) (VerifiedWindow, bool) {
	label := w.Label
	if want := w.LogID(chain.Base); cw.LogID != want {
		fail(label, "window %s's log is %q, not %q", label, cw.LogID, want)
		return VerifiedWindow{}, false
	}
	cp, err := readSignedCheckpoint(cw.FinalCheckpoint, opts.CheckpointKey)
	if err != nil {
		fail(label, "window %s's final checkpoint: %v", label, err)
		return VerifiedWindow{}, false
	}
	if cp.LogID != cw.LogID {
		fail(label, "window %s's final checkpoint is of log %q", label, cp.LogID)
		return VerifiedWindow{}, false
	}
	if cp.Entries < 2 {
		fail(label, "window %s's final tree holds %d records, fewer than its open and close", label, cp.Entries)
		return VerifiedWindow{}, false
	}
	if len(opts.Witnesses) > 0 {
		if len(cw.Receipts) == 0 {
			fail(label, "window %s not witnessed: its final checkpoint carries no receipt", label)
			return VerifiedWindow{}, false
		}
		if err := verifyCarriedReceipts(cw.FinalCheckpoint, cw.Receipts, opts.Witnesses); err != nil {
			fail(label, "window %s not witnessed: %v", label, err)
			return VerifiedWindow{}, false
		}
	}
	open, openKey, err := windowChainRecord(cw.Open, cp, 1, cw.OpenProof)
	if err != nil {
		fail(label, "window %s's open record: %v", label, err)
		return VerifiedWindow{}, false
	}
	closing, closeKey, err := windowChainRecord(cw.Close, cp, cp.Entries, cw.CloseProof)
	if err != nil {
		fail(label, "window %s's close record: %v", label, err)
		return VerifiedWindow{}, false
	}
	if openKey != closeKey {
		fail(label, "window %s's open and close records are signed by two keys", label)
		return VerifiedWindow{}, false
	}
	if open.RecordType != RecordTypeWindowOpen || closing.RecordType != RecordTypeWindowClose || open.BookID != cw.LogID || closing.BookID != cw.LogID {
		fail(label, "window %s's first and last records are not its open and close", label)
		return VerifiedWindow{}, false
	}
	var os WindowOpenStatement
	var cs WindowCloseStatement
	if decodeStrict(open.Statement, &os) != nil || decodeStrict(closing.Statement, &cs) != nil || os.RecordVersion != WindowOpenVersion || cs.RecordVersion != WindowCloseVersion {
		fail(label, "window %s's open or close record does not carry its body", label)
		return VerifiedWindow{}, false
	}
	start, end := windowTime(w.Start), windowTime(w.End)
	if os.Period != chain.Period || os.Label != label || os.Start != start || os.End != end ||
		cs.Period != chain.Period || cs.Label != label || cs.Start != start || cs.End != end {
		fail(label, "window %s bounds differ from its label's [%s, %s)", label, start, end)
		return VerifiedWindow{}, false
	}
	switch {
	case w.Start.Equal(since) && os.Previous != nil:
		fail(label, "chain breaks at %s: the chain's first window names a previous one", label)
		return VerifiedWindow{}, false
	case !w.Start.Equal(since) && os.Previous == nil:
		fail(label, "chain breaks at %s: a new chain began", label)
		return VerifiedWindow{}, false
	case prior != nil && *os.Previous != prior.next():
		fail(label, "chain breaks at %s: its open record names a previous tree other than window %s's final tree", label, prior.Label)
		return VerifiedWindow{}, false
	}
	verified := VerifiedWindow{Window: w, Final: WindowTree{LogID: cw.LogID, Entries: cp.Entries, Root: cp.Root}, RecordKey: openKey}
	// The close record, in the final tree, names the boundary the window
	// took: a chain carries exactly that one, so it can neither swap nor
	// drop it (evidencebook#807).
	switch {
	case cs.Boundary == nil && cw.Boundary != nil:
		fail(label, "window %s carries a boundary its close record does not name", label)
		return VerifiedWindow{}, false
	case cs.Boundary != nil && cw.Boundary == nil:
		fail(label, "window %s's close record names a boundary the chain does not carry", label)
		return VerifiedWindow{}, false
	case cs.Boundary != nil && chain.V < WindowChainVersion:
		fail(label, "window %s has a boundary, which a version 1 chain cannot carry", label)
		return VerifiedWindow{}, false
	}
	if cw.Boundary != nil {
		boundary, ok := verifyBoundary(cw, cp, w, openKey, opts, fail)
		if !ok {
			return VerifiedWindow{}, false
		}
		if *boundary != *cs.Boundary {
			fail(label, "window %s carries a boundary other than the one its close record names", label)
			return VerifiedWindow{}, false
		}
		verified.Boundary = boundary
	}
	return verified, true
}

// next is the tree the next window's open record must name.
func (v VerifiedWindow) next() WindowTree {
	if v.Boundary != nil {
		return *v.Boundary
	}
	return v.Final
}

// verifyBoundary checks a window's boundary (evidencebook#807): its
// checkpoint, that the final tree extends it, and that every record after
// it up to the close record is in the tail, contiguous, and a close/v1 for
// a period inside the window or an index root. The tail is positional, so a
// record whose commit time was set back cannot be left out of it.
func verifyBoundary(cw ChainWindow, final signedCheckpoint, w Window, recordKey string, opts VerifyChainOptions, fail func(string, string, ...any)) (*WindowTree, bool) {
	label, b := w.Label, cw.Boundary
	bcp, err := readSignedCheckpoint(b.Checkpoint, opts.CheckpointKey)
	if err != nil {
		fail(label, "window %s's boundary checkpoint: %v", label, err)
		return nil, false
	}
	if bcp.LogID != cw.LogID || bcp.Entries == 0 || bcp.Entries >= final.Entries {
		fail(label, "window %s's boundary checkpoint is not of its log before its final tree", label)
		return nil, false
	}
	if len(opts.Witnesses) > 0 {
		if len(b.Receipts) == 0 {
			fail(label, "window %s not witnessed: its boundary checkpoint carries no receipt", label)
			return nil, false
		}
		if err := verifyCarriedReceipts(b.Checkpoint, b.Receipts, opts.Witnesses); err != nil {
			fail(label, "window %s not witnessed: %v", label, err)
			return nil, false
		}
	}
	if !consistencySizes(b.Consistency, bcp.TreeSize, final.TreeSize) || checkCLLConsistency(bcp.commitment, final.commitment, b.Consistency) != nil {
		fail(label, "window %s's final tree does not extend its boundary", label)
		return nil, false
	}
	if want := final.Entries - 1 - bcp.Entries; uint64(len(b.Tail)) != want {
		fail(label, "window %s's tail carries %d of the %d records after its boundary", label, len(b.Tail), want)
		return nil, false
	}
	for i, t := range b.Tail {
		seq := bcp.Entries + 1 + uint64(i)
		h, key, err := windowChainRecord(t.Record, final, seq, t.Proof)
		if err != nil {
			fail(label, "window %s's late record at %d: %v", label, seq, err)
			return nil, false
		}
		if key != recordKey || h.BookID != cw.LogID {
			fail(label, "window %s's late record at %d is not its own", label, seq)
			return nil, false
		}
		switch h.RecordType {
		case RecordTypeIndexRoot:
			var root struct {
				IndexRoot
				AsOfSeq uint64 `json:"as_of_seq"`
			}
			if decodeStrict(h.Statement, &root) != nil || root.AsOfSeq != seq-1 {
				fail(label, "window %s holds a malformed index_root record at %d", label, seq)
				return nil, false
			}
		case RecordTypeClose:
			if err := (&bookWindow{Window: w}).closeInside(h); err != nil {
				fail(label, "window %s holds a late Close at %d that is not a close/v1 for a period inside it", label, seq)
				return nil, false
			}
		default:
			fail(label, "window %s holds a late %s record at %d", label, h.RecordType, seq)
			return nil, false
		}
	}
	return &WindowTree{LogID: cw.LogID, Entries: bcp.Entries, Root: bcp.Root}, true
}

// consistencySizes reports whether cll-consistency-proof octets are from
// MMR size from to size to.
func consistencySizes(proof []byte, from, to uint64) bool {
	decoded, err := evidencerequest.DecodeCBOR(proof)
	parts, ok := decoded.([]any)
	if err != nil || !ok || len(parts) != 4 {
		return false
	}
	a, okA := evidencerequest.UintValue(parts[0])
	b, okB := evidencerequest.UintValue(parts[1])
	return okA && okB && a == from && b == to
}

// windowChainRecord checks a sealed record of a chain: its capsule commits its
// header and verifies as its id, its envelope is signed, its header names
// its position, and its id is the leaf at that position under cp.
func windowChainRecord(sr StoredRecord, cp signedCheckpoint, seq uint64, proof []byte) (Header, string, error) {
	if err := checkCapsuleCommitsHeader(sr.RecordID, sr.Capsule, sr.Header); err != nil {
		return Header{}, "", err
	}
	envelope, err := emit.VerifyEnvelope(sr.RecordID, sr.Envelope)
	if err != nil {
		return Header{}, "", fmt.Errorf("%w: envelope: %v", ErrInvalid, err)
	}
	record, err := decodeStored(StoredRecord{Seq: seq, RecordID: sr.RecordID, Header: sr.Header, Capsule: sr.Capsule})
	if err != nil {
		return Header{}, "", err
	}
	entry, err := decodeRecordID(sr.RecordID)
	if err != nil {
		return Header{}, "", err
	}
	if err := checkProofLeaf(proof, seq-1); err != nil {
		return Header{}, "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := checkCLLInclusion(cp, entry, proof, nil); err != nil {
		return Header{}, "", fmt.Errorf("%w: not the leaf at %d under the final checkpoint: %v", ErrInvalid, seq, err)
	}
	return record.Header, hex.EncodeToString(envelope.PublicKey), nil
}
