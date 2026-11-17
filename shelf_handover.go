// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// The whole-shelf handover and its replay (evidencebook#461,
// docs/design/461-shelf-facts.md §2.7): a reader the holder chooses is handed
// every entry of the shelf log up to an anchor, with the documents after the
// format switch, each book fact's status opening, the book checkpoints the
// facts carry and the consistency proofs between a book's facts, and checks
// that nothing was held back, left out, changed or replaced. Counting a card
// from the latest facts is standard_history/3's (AAC). A genesis fact's tie
// to the old witnessed tree (§2.7) is not handed over yet: every genesis fact
// is reported untied, which claims nothing about a log before the coverage
// start.

// Handover is the material a whole-shelf reader is handed (§2.7).
type Handover struct {
	V int `json:"v"`
	// LogID is the shelf log (the cadence log).
	LogID string `json:"log_id"`
	// Entries is every entry of the shelf log, in order, up to the anchor:
	// 32-byte ids in lowercase hex.
	Entries []string `json:"entries"`
	// Documents are the entry documents after the format switch, with the
	// status opening of each book fact.
	Documents []HandoverDocument `json:"documents"`
	// BookCheckpoints are the COSE statements of the book checkpoints the
	// book facts carry.
	BookCheckpoints [][]byte `json:"book_checkpoints"`
	// Consistency is, for each pair of consecutive facts of a book whose
	// size grew, the proof between their checkpoints.
	Consistency []HandoverConsistency `json:"consistency"`
	// Anchor is the shelf checkpoint the handover runs to, and Receipts the
	// witness receipts the holder holds for it.
	Anchor   []byte           `json:"anchor"`
	Receipts []WitnessReceipt `json:"receipts,omitempty"`
}

// HandoverDocument is one entry document and, for a book fact, its status
// opening.
type HandoverDocument struct {
	Seq           uint64       `json:"seq"`
	Doc           []byte       `json:"doc"`
	StatusOpening *FactOpening `json:"status_opening,omitempty"`
}

// HandoverConsistency is a consistency proof between two checkpoints of one
// book.
type HandoverConsistency struct {
	LogID    string              `json:"log_id"`
	FromSize uint64              `json:"from_size"`
	ToSize   uint64              `json:"to_size"`
	Proof    ConsistencyEvidence `json:"proof"`
}

// HandoverResult is what a verified handover shows.
type HandoverResult struct {
	// Recomputed is true: VerifyHandover fails rather than return a result
	// it could not recompute.
	Recomputed bool `json:"recomputed"`
	// Witnessed says a receipt from a witness in the directory verifies for
	// the anchor; a shelf with no witness is a supported default and is
	// unwitnessed.
	Witnessed bool `json:"witnessed"`
	// CoverageStart is the shelf log size, in entries, at which the facts
	// begin to be complete: the end of the migration's stamp.
	CoverageStart uint64 `json:"coverage_start"`
	// Latest is each book's latest fact and its status opening.
	Latest []HandoverLatest `json:"latest"`
	// Untied lists the books whose genesis fact carries no tie to the old
	// witnessed tree.
	Untied []string `json:"untied"`
}

// HandoverLatest is a book's latest fact up to the anchor.
type HandoverLatest struct {
	Fact          BookFact    `json:"fact"`
	StatusOpening FactOpening `json:"status_opening"`
}

// Handover hands over the shelf up to tick n's checkpoint (the newest
// finished tick when n is 0), which must be a stamp's: the material a reader
// the holder chooses replays (§2.7). Link openings and journal openings are
// never handed over.
func (c *Cadence) Handover(ctx context.Context, n uint64) (Handover, error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return Handover{}, ErrReleased
	}
	if n == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0`).Scan(&n); err != nil {
			return Handover{}, err
		}
	}
	tick, err := c.tickLocked(ctx, n)
	if err != nil {
		return Handover{}, err
	}
	if tick.Leaves.Stamp == nil || tick.CheckpointSize == 0 {
		return Handover{}, fmt.Errorf("%w: tick %d is not a finished stamp", ErrInvalid, n)
	}
	anchor, err := checkpointFromStatement(tick.Checkpoint)
	if err != nil {
		return Handover{}, err
	}
	out := Handover{V: 1, LogID: c.cfg.LogID, Anchor: tick.Checkpoint, Entries: []string{}, Documents: []HandoverDocument{}, BookCheckpoints: [][]byte{}, Consistency: []HandoverConsistency{}}
	if tick.Receipt != nil {
		out.Receipts = []WitnessReceipt{*tick.Receipt}
	}
	for after := uint64(0); after < anchor.Entries; {
		entries, err := c.log.Entries(ctx, after, int(min(1000, anchor.Entries-after)))
		if err != nil {
			return Handover{}, err
		}
		if len(entries) == 0 {
			return Handover{}, fmt.Errorf("%w: the shelf log holds %d entries, its checkpoint %d", ErrCorrupt, after, anchor.Entries)
		}
		for _, e := range entries {
			// A gap, or an entry past the anchor, is a log that breaks its
			// own contract: stop rather than loop.
			if e.Seq != after+1 {
				return Handover{}, fmt.Errorf("%w: the shelf log's entry after %d is %d", ErrCorrupt, after, e.Seq)
			}
			out.Entries = append(out.Entries, e.RecordID)
			after = e.Seq
		}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_seq, kind, log_id, doc, status_opening FROM shelf_entries WHERE entry_seq <= ? ORDER BY entry_seq`, anchor.Entries)
	if err != nil {
		return Handover{}, err
	}
	type fact struct {
		log  string
		size uint64
		sum  string
	}
	var facts []fact
	for rows.Next() {
		var d HandoverDocument
		var kind, log string
		var so []byte
		if err := rows.Scan(&d.Seq, &kind, &log, &d.Doc, &so); err != nil {
			_ = rows.Close()
			return Handover{}, err
		}
		if kind == FactKindBook {
			d.StatusOpening = &FactOpening{}
			var f BookFact
			if err := firstErr(json.Unmarshal(so, d.StatusOpening), json.Unmarshal(d.Doc, &f)); err != nil {
				_ = rows.Close()
				return Handover{}, fmt.Errorf("%w: stored fact %d: %v", ErrCorrupt, d.Seq, err)
			}
			facts = append(facts, fact{log: f.LogID, size: f.Size, sum: f.CheckpointSHA256})
		}
		out.Documents = append(out.Documents, d)
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		return Handover{}, err
	}
	// The book checkpoints the facts carry are the statements the stamps'
	// leaves kept, one per distinct checkpoint.
	statements := map[string][]byte{}
	tickRows, err := s.db.QueryContext(ctx, `SELECT tick FROM cadence_ticks WHERE tick <= ? ORDER BY tick`, n)
	if err != nil {
		return Handover{}, err
	}
	var ticks []uint64
	for tickRows.Next() {
		var t uint64
		if err := tickRows.Scan(&t); err != nil {
			_ = tickRows.Close()
			return Handover{}, err
		}
		ticks = append(ticks, t)
	}
	if err := firstErr(tickRows.Err(), tickRows.Close()); err != nil {
		return Handover{}, err
	}
	for _, t := range ticks {
		got, err := c.tickLocked(ctx, t)
		if err != nil {
			return Handover{}, err
		}
		if got.Leaves.Stamp == nil {
			continue
		}
		for _, leaf := range got.Leaves.Deals {
			if leaf.Statement == "" {
				continue
			}
			b, err := base64.RawURLEncoding.DecodeString(leaf.Statement)
			if err != nil {
				return Handover{}, fmt.Errorf("%w: tick %d's statement of %s: %v", ErrCorrupt, t, leaf.LogID, err)
			}
			statements[leaf.CheckpointSHA256] = b
		}
	}
	seen := map[string]bool{}
	last := map[string]fact{}
	for _, f := range facts {
		if !seen[f.sum] {
			b, ok := statements[f.sum]
			if !ok {
				return Handover{}, fmt.Errorf("%w: no stored statement for %s's checkpoint %s", ErrCorrupt, f.log, f.sum)
			}
			out.BookCheckpoints = append(out.BookCheckpoints, b)
			seen[f.sum] = true
		}
		if prev, ok := last[f.log]; ok && f.size > prev.size {
			older, err := checkpointFromStatement(statements[prev.sum])
			if err != nil {
				return Handover{}, err
			}
			newer, err := checkpointFromStatement(statements[f.sum])
			if err != nil {
				return Handover{}, err
			}
			log, err := s.cadenceLogLocked(ctx, f.log)
			if err != nil {
				return Handover{}, err
			}
			proof, err := log.substrate().ProveConsistency(ctx, older, newer)
			if err != nil {
				return Handover{}, err
			}
			out.Consistency = append(out.Consistency, HandoverConsistency{LogID: f.log, FromSize: prev.size, ToSize: f.size, Proof: proof})
		}
		last[f.log] = f
	}
	return out, nil
}

// VerifyHandover replays a handover (§2.7) under the holder's checkpoint key
// and the reader's witness directory. It fails when an entry is held back, a
// document is left out or does not hash to its entry, a status opening does
// not recompute its commitment, a book checkpoint is not the fact's or not
// the holder's, or a book's facts break their chain (a replaced book). The
// journal's chain is not checked and its openings are not asked for.
func VerifyHandover(h Handover, pinnedKey string, witnesses aacwitness.Directory) (HandoverResult, error) {
	bad := func(format string, args ...any) (HandoverResult, error) {
		return HandoverResult{}, fmt.Errorf("%w: handover: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	anchor, err := checkpointFromStatement(h.Anchor)
	if err != nil {
		return bad("the anchor does not verify: %v", err)
	}
	if h.V != 1 {
		return bad("a handover of version %d", h.V)
	}
	if anchor.KeyID != pinnedKey || anchor.LogID != h.LogID {
		return bad("the anchor is not the holder's shelf log")
	}
	// Every entry up to the anchor, exactly: an entry held back changes the
	// count or the root.
	if uint64(len(h.Entries)) != anchor.Entries {
		return bad("%d entries for an anchor of %d", len(h.Entries), anchor.Entries)
	}
	tree, err := mmr.New(nil)
	if err != nil {
		return HandoverResult{}, err
	}
	for i, id := range h.Entries {
		value, err := decodeRecordID(id)
		if err != nil {
			return bad("entry %d: %v", i+1, err)
		}
		if _, err := tree.Append(value); err != nil {
			return HandoverResult{}, err
		}
	}
	peaks, err := tree.PeakHashesAt(anchor.TreeSize)
	if err != nil {
		return bad("the entries do not reach the anchor's size: %v", err)
	}
	if hex.EncodeToString(mmr.RootFromPeaks(peaks)) != anchor.Root {
		return bad("the entries do not recompute the anchor's root")
	}
	// Every entry after the marker has its document.
	docs := map[uint64]HandoverDocument{}
	for _, d := range h.Documents {
		if d.Seq < 1 || d.Seq > anchor.Entries {
			return bad("a document at %d, outside the handover", d.Seq)
		}
		sum := sha256.Sum256(d.Doc)
		if hex.EncodeToString(sum[:]) != h.Entries[d.Seq-1] {
			return bad("the document at %d does not hash to its entry", d.Seq)
		}
		docs[d.Seq] = d
	}
	var marker uint64
	for seq := uint64(1); seq <= anchor.Entries; seq++ {
		d, ok := docs[seq]
		if !ok {
			if marker != 0 {
				return bad("the entry at %d after the format switch has no document", seq)
			}
			continue
		}
		var head struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(d.Doc, &head); err != nil {
			return bad("the document at %d: %v", seq, err)
		}
		if marker == 0 {
			if head.Kind != EntryKindFormatSwitch {
				return bad("a document at %d before the format switch", seq)
			}
			marker = seq
		}
	}
	if marker == 0 {
		return bad("no format switch up to the anchor")
	}
	statements := map[string]Checkpoint{}
	for _, b := range h.BookCheckpoints {
		cp, err := checkpointFromStatement(b)
		if err != nil {
			return bad("a book checkpoint does not verify: %v", err)
		}
		if cp.KeyID != pinnedKey {
			return bad("a book checkpoint of %s is not the holder's", cp.LogID)
		}
		sum := sha256.Sum256(b)
		statements[hex.EncodeToString(sum[:])] = cp
	}
	proofs := map[string]ConsistencyEvidence{}
	for _, p := range h.Consistency {
		proofs[fmt.Sprintf("%s %d %d", p.LogID, p.FromSize, p.ToSize)] = p.Proof
	}
	result := HandoverResult{Recomputed: true, Latest: []HandoverLatest{}, Untied: []string{}}
	latest := map[string]HandoverLatest{}
	var coverage uint64
	for seq := marker; seq <= anchor.Entries; seq++ {
		d := docs[seq]
		var head struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(d.Doc, &head); err != nil {
			return bad("the document at %d: %v", seq, err)
		}
		var f BookFact
		var err error
		switch head.Kind {
		case EntryKindStamp:
			err = canonicalDocument(d.Doc, &StampEntry{})
			if coverage == 0 {
				coverage = seq
			}
		case EntryKindFormatSwitch:
			err = canonicalDocument(d.Doc, &FormatSwitch{})
		case FactKindJournal, FactKindBook:
			err = canonicalDocument(d.Doc, &f)
		default:
			return bad("the document at %d is a %q", seq, head.Kind)
		}
		if err != nil {
			return bad("the document at %d: %v", seq, err)
		}
		if head.Kind != FactKindBook {
			continue
		}
		if d.StatusOpening == nil {
			return bad("the fact at %d has no status opening", seq)
		}
		if err := d.StatusOpening.Verify(f.StatusCommitment); err != nil {
			return bad("the fact at %d: %v", seq, err)
		}
		cp, ok := statements[f.CheckpointSHA256]
		if !ok {
			return bad("the fact at %d carries a checkpoint the handover does not hold", seq)
		}
		if cp.LogID != f.LogID || cp.TreeSize != f.Size {
			return bad("the fact at %d names %s at %d, its checkpoint %s at %d", seq, f.LogID, f.Size, cp.LogID, cp.TreeSize)
		}
		prev, ok := latest[f.BookID]
		if !ok {
			if f.PrevFactSize != 0 {
				return bad("the first fact of %s at %d names a previous size", f.BookID, seq)
			}
			result.Untied = append(result.Untied, f.BookID)
		} else if err := checkFactChain(prev.Fact, f, statements, proofs); err != nil {
			return bad("the fact of %s at %d: %v", f.BookID, seq, err)
		}
		latest[f.BookID] = HandoverLatest{Fact: f, StatusOpening: *d.StatusOpening}
	}
	if coverage == 0 {
		return bad("the migration's stamp is not in the handover")
	}
	result.CoverageStart = coverage
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		result.Latest = append(result.Latest, latest[id])
	}
	sort.Strings(result.Untied)
	for _, r := range h.Receipts {
		if check, err := checkWitnessReceipt(h.Anchor, r, witnesses); err == nil && check.State == WitnessVerified {
			result.Witnessed = true
			break
		}
	}
	return result, nil
}

// checkFactChain checks two consecutive facts of one book (§2.7): one log,
// prev_fact_size the previous size, no shrinking, the same checkpoint at the
// same size, and a consistency proof that verifies when the size grew. A
// break is a replaced book. statements maps a checkpoint statement's SHA-256
// to the checkpoint, and proofs "<log> <from> <to>" to its proof.
func checkFactChain(p, f BookFact, statements map[string]Checkpoint, proofs map[string]ConsistencyEvidence) error {
	replaced := func(format string, args ...any) error {
		return fmt.Errorf("%s (a replaced book)", fmt.Sprintf(format, args...))
	}
	switch {
	case f.LogID != p.LogID:
		return replaced("names %s, its previous fact %s", f.LogID, p.LogID)
	case f.PrevFactSize != p.Size:
		return replaced("names previous size %d, its previous fact %d", f.PrevFactSize, p.Size)
	case f.Size < p.Size:
		return replaced("shrinks the book from %d to %d", p.Size, f.Size)
	case f.Size == p.Size && f.CheckpointSHA256 != p.CheckpointSHA256:
		return replaced("keeps size %d with another checkpoint", f.Size)
	case f.Size > p.Size:
		proof, ok := proofs[fmt.Sprintf("%s %d %d", f.LogID, p.Size, f.Size)]
		if !ok {
			return replaced("no consistency proof for %s from %d to %d", f.LogID, p.Size, f.Size)
		}
		older, ok1 := statements[p.CheckpointSHA256]
		newer, ok2 := statements[f.CheckpointSHA256]
		if !ok1 || !ok2 {
			return replaced("a checkpoint of %s the handover does not hold", f.LogID)
		}
		if err := verifyCLLConsistency(older, newer, proof); err != nil {
			return replaced("%s from %d to %d: %v", f.LogID, p.Size, f.Size, err)
		}
	}
	return nil
}

// canonicalDocument decodes an entry document into v and requires the
// document to be v's canonical JSON, so every language reads the same
// members: a duplicate, an unknown member or another spelling of a value
// changes the bytes.
func canonicalDocument(doc []byte, v any) error {
	if err := json.Unmarshal(doc, v); err != nil {
		return err
	}
	_, canonical, err := EntryID(v)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, doc) {
		return fmt.Errorf("not its canonical JSON")
	}
	return nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
