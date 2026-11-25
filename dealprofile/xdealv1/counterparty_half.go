// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/evidencebook"
)

// The other party's half in a holder's own copy (evidencebook#850; PROFILE
// §10, "The counterparty's half"). It is exactly what the holder's History
// card reads for "stamped by both" (StatusBooks.ProofHeld, proofCovers): the
// counterparty's agreement, the first record of its deal book, signed with
// its key, and the stamp proof it sent that covers it. A copy carries it so
// that a reader checks it from the copy alone, not from the holder's word.
// It shows their signed, stamped agreement, not that they confirmed any
// later record of the deal (Sherlock2, evidencebook#850).

// StageCounterpartyHalf is the counterparty's half read on its own
// (VerifyCounterpartyHalf).
const StageCounterpartyHalf = "counterparty_half"

// CounterpartyHalfExtension is the bundle extension that carries the half,
// on a keep copy only. It is a private x- kind (AAC REGISTRY §14): a neutral
// bundle verifier leaves it uninterpreted, so the rule that a copy whose
// half fails is INVALID binds an x-deal-v1 reader, not the neutral verifier.
const CounterpartyHalfExtension = "x-deal-v1-counterparty-half"

// CounterpartyHalf is the extension: an Evidence Bundle of the
// counterparty's deal book, rooted at its agreement (seq 1) and anchored on
// the book checkpoint its stamp proof carries, every record up to that
// checkpoint carried and only the agreement disclosed; and the stamp proof
// as the counterparty sent it.
type CounterpartyHalf struct {
	Bundle     json.RawMessage         `json:"bundle"`
	StampProof evidencebook.StampProof `json:"stamp_proof"`
}

// HalfRead is what a verified half shows: the counterparty's key, its
// agreement's record id, its deal book's log id, and how many of its records
// the checkpoint covers.
type HalfRead struct {
	Key      string
	RecordID string
	LogID    string
	Size     uint64
}

// MakeCounterpartyHalf builds the extension from the counterparty's deal
// book pairs, seq 1 to the proof's book checkpoint, in order, as the holder
// received them, and the stamp proof the holder holds for them. It builds
// the bundle only; the caller carries it only once VerifyCounterpartyHalf
// passes against its own agreement.
func MakeCounterpartyHalf(ctx context.Context, pairs [][]byte, proof evidencebook.StampProof) ([]byte, error) {
	bad := func(format string, args ...any) ([]byte, error) {
		return nil, stageErr(StageCounterpartyHalf, format, args...)
	}
	if len(pairs) == 0 {
		return bad("no record of theirs")
	}
	records := make([]evidencebook.ForeignRecord, len(pairs))
	var root string
	for i, pair := range pairs {
		read, err := VerifyBook(pair)
		if err != nil {
			return nil, err
		}
		if read.Seq != uint64(i+1) {
			return bad("the record at %d is at seq %d of its book", i+1, read.Seq)
		}
		if records[i], err = foreignRecord(pair, read); err != nil {
			return nil, err
		}
		if i == 0 {
			root = read.RecordID
		}
	}
	bundle, err := evidencebook.ForeignBundle(ctx, proof.BookCheckpoint, records, root)
	if err != nil {
		return nil, err
	}
	return json.Marshal(CounterpartyHalf{Bundle: bundle, StampProof: proof})
}

// foreignRecord is a verified book pair as the bundle producer takes it.
func foreignRecord(pair []byte, read BookRead) (evidencebook.ForeignRecord, error) {
	var p struct {
		Record  map[string]json.RawMessage `json:"record"`
		Header  json.RawMessage            `json:"header"`
		Payload json.RawMessage            `json:"payload"`
	}
	if err := json.Unmarshal(pair, &p); err != nil {
		return evidencebook.ForeignRecord{}, err
	}
	capsule, _, _, err := verifyCapsule(StageCounterpartyHalf, p.Record)
	if err != nil {
		return evidencebook.ForeignRecord{}, err
	}
	var sig string
	if err := json.Unmarshal(p.Record["signature"], &sig); err != nil {
		return evidencebook.ForeignRecord{}, err
	}
	envelope, err := hex.DecodeString(sig)
	if err != nil {
		return evidencebook.ForeignRecord{}, err
	}
	header, err := canonicalOf(p.Header)
	if err != nil {
		return evidencebook.ForeignRecord{}, err
	}
	return evidencebook.ForeignRecord{Capsule: capsule, Envelope: envelope, Header: header,
		Payloads: map[string][]byte{read.Digest: []byte(p.Payload)}}, nil
}

func canonicalOf(raw []byte) ([]byte, error) {
	doc, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	return canonical.JCS(doc)
}

// VerifyCounterpartyHalf checks the extension ext against the holder's own
// agreement (its book pair). It is refused unless:
//
//   - the bundle verifies in full (every claim passes) and its anchor is
//     signed by the checkpoint key the counterparty's agreement names;
//   - its root is the record at seq 1 of the bundle's log, disclosed with
//     its header and payload, and that record, read as a book pair, is an
//     agreement of the holder's deal and offer, of the other role, signed
//     by the counterparty key the holder's agreement names, which runs the
//     counterparty layer;
//   - the stamp proof verifies, is signed by that same checkpoint key on
//     the shelf log the counterparty's agreement names, its fact is of that
//     agreement, and its book checkpoint is byte for byte the bundle's
//     anchor.
//
// A half that passes shows that the counterparty signed this agreement and
// stamped it in its own book. It does not show that the counterparty's log
// has not forked since that checkpoint: the copy speaks as of it.
func VerifyCounterpartyHalf(holderAgreement, ext []byte) (HalfRead, error) {
	bad := func(format string, args ...any) (HalfRead, error) {
		return HalfRead{}, stageErr(StageCounterpartyHalf, format, args...)
	}
	if err := checkText(ext); err != nil {
		return HalfRead{}, err
	}
	var half CounterpartyHalf
	if err := strictUnmarshal(ext, &half); err != nil || half.Bundle == nil {
		return bad("not {bundle, stamp_proof}: %v", err)
	}
	if err := exactObject(ext, reflect.TypeOf(half), false, "the half"); err != nil {
		return bad("%v", err)
	}
	holder, err := VerifyBook(holderAgreement)
	if err != nil {
		return HalfRead{}, err
	}
	hAg, ok := asAgree(holder.Record.Body)
	switch {
	case !ok || hAg.Kind != KindAgreement || holder.Seq != 1:
		return bad("the holder's record is not its agreement")
	case hAg.CounterpartyLayer == nil || !*hAg.CounterpartyLayer || hAg.CounterpartyKey.V == nil:
		return bad("the holder's agreement names no counterparty that keeps its own book")
	}
	vb, err := evidencebook.VerifyBundle(half.Bundle)
	if err != nil {
		return bad("the bundle: %v", err)
	}
	if err := vb.FullyVerified(); err != nil {
		return bad("the bundle: %v", err)
	}
	pair, rootID, err := rootPair(half.Bundle, vb)
	if err != nil {
		return bad("%v", err)
	}
	theirs, err := VerifyBook(pair)
	if err != nil {
		return bad("their record: %v", err)
	}
	tAg, ok := asAgree(theirs.Record.Body)
	switch {
	case theirs.RecordID != rootID || theirs.Seq != 1 || theirs.LogID != vb.LogID:
		return bad("the bundle's root is not the first record of its log")
	case !slices.ContainsFunc(vb.Records, func(r evidencebook.PeerRecord) bool { return r.RecordID == rootID && r.Seq == 1 && r.HeaderVerified }):
		return bad("the bundle does not prove its root at seq 1")
	case !ok || tAg.Kind != KindAgreement:
		return bad("their record is not an agreement")
	case theirs.KeyID != *hAg.CounterpartyKey.V:
		return bad("their agreement is not signed by the counterparty key the holder's agreement names")
	case theirs.Record.Block.DealID != holder.Record.Block.DealID:
		return bad("their agreement is of another deal")
	case tAg.Offer.Digest != hAg.Offer.Digest:
		return bad("their agreement is of another offer")
	case tAg.Role == hAg.Role || (tAg.Role != RoleBuyer && tAg.Role != RoleSeller):
		return bad("their agreement is not of the other role")
	case !vb.AnchorAuthenticated || vb.AnchorKeyID != tAg.CheckpointKey:
		return bad("the bundle is not anchored on a checkpoint signed by their checkpoint key")
	}
	v, err := evidencebook.VerifyStampProof(half.StampProof)
	switch {
	case err != nil:
		return bad("the stamp proof: %v", err)
	case v.KeyID != tAg.CheckpointKey:
		return bad("the stamp proof is not signed by their checkpoint key")
	case v.ShelfCheckpoint.LogID != tAg.ShelfLogID:
		return bad("the stamp proof is not of the shelf log their agreement names")
	case v.Fact.BookID != theirs.RecordID:
		return bad("the stamp proof is not of their agreement")
	case !bytes.Equal(half.StampProof.BookCheckpoint, vb.AnchorStatement):
		return bad("the stamp proof's book checkpoint is not the bundle's anchor")
	}
	return HalfRead{Key: theirs.KeyID, RecordID: theirs.RecordID, LogID: theirs.LogID, Size: vb.IntervalLast}, nil
}

// rootPair rebuilds the bundle root's book pair from what the bundle
// verified: the root's record as the bundle carries it (a signed Capsule),
// its disclosed header and its one payload.
func rootPair(bundle []byte, vb evidencebook.VerifiedBundle) ([]byte, string, error) {
	var b struct {
		Root        string                                `json:"root"`
		Records     []map[string]json.RawMessage          `json:"records"`
		Disclosures map[string]map[string]json.RawMessage `json:"disclosures"`
	}
	if err := json.Unmarshal(bundle, &b); err != nil {
		return nil, "", fmt.Errorf("the bundle: %v", err)
	}
	var record map[string]json.RawMessage
	for _, r := range b.Records {
		var id string
		if json.Unmarshal(r["capsule_id"], &id) == nil && id == b.Root {
			record = r
		}
	}
	header := b.Disclosures[b.Root][evidencebook.HeaderMember]
	if record == nil || header == nil {
		return nil, "", fmt.Errorf("the bundle does not disclose its root's record and header")
	}
	h, err := canonicalOf(header)
	if err != nil {
		return nil, "", fmt.Errorf("the root's header: %v", err)
	}
	var commits struct {
		PayloadCommitments []string `json:"payload_commitments"`
	}
	if err := json.Unmarshal(h, &commits); err != nil || len(commits.PayloadCommitments) != 1 {
		return nil, "", fmt.Errorf("the root's header commits no single payload")
	}
	payload, ok := vb.Payloads[commits.PayloadCommitments[0]]
	if !ok {
		return nil, "", fmt.Errorf("the bundle withholds its root's payload")
	}
	signed, err := json.Marshal(record)
	if err != nil {
		return nil, "", err
	}
	pair, err := json.Marshal(map[string]json.RawMessage{"record": signed, "header": h, "payload": payload})
	return pair, b.Root, err
}

// HeldCounterpartyHalf is the half a holder's copy carries, from what its
// shelf holds for the deal of its agreement (holderAgreement, its book
// pair): the counterparty's counted records from its agreement up to the
// largest held stamp proof that covers that agreement (as proofCovers reads
// it, so the copy and the History card read one rule), with that proof. It
// is nil when the counterparty keeps no book, nothing of theirs is held, no
// held proof covers their agreement, or a record the proof covers is no
// longer held (forgotten under retention): the copy then carries no half,
// which reads absent. A half it builds that does not verify is an error,
// never carried.
func HeldCounterpartyHalf(ctx context.Context, shelf *evidencebook.Shelf, holderAgreement []byte) ([]byte, error) {
	holder, err := VerifyBook(holderAgreement)
	if err != nil {
		return nil, err
	}
	hAg, ok := asAgree(holder.Record.Body)
	if !ok || hAg.Kind != KindAgreement || hAg.CounterpartyLayer == nil || !*hAg.CounterpartyLayer || hAg.CounterpartyKey.V == nil {
		return nil, nil
	}
	key := *hAg.CounterpartyKey.V
	held, err := shelf.HeldByDeal(ctx, holder.Record.Block.DealID)
	if err != nil {
		return nil, err
	}
	logs := logsOf(held, key, func(r Record) bool {
		a, ok := asAgree(r.Body)
		return ok && a.Kind == KindAgreement
	})
	if len(logs) == 0 {
		return nil, nil
	}
	theirs, _, err := countedWith(ctx, shelf, held, key, logs[0])
	if err != nil || len(theirs) == 0 {
		return nil, err
	}
	tAg, _ := asAgree(theirs[0].rec.Record.Body)
	proofs, err := shelf.HeldProofs(ctx, key, logs[0])
	if err != nil {
		return nil, err
	}
	var best *evidencebook.HeldProof
	for i, hp := range proofs {
		v := hp.Verified
		if v.Fact.BookID != theirs[0].rec.RecordID || v.KeyID != tAg.CheckpointKey || v.ShelfCheckpoint.LogID != tAg.ShelfLogID || hp.Covers < 1 {
			continue
		}
		if best == nil || v.BookCheckpoint.Entries > best.Verified.BookCheckpoint.Entries {
			best = &proofs[i]
		}
	}
	if best == nil || best.Verified.BookCheckpoint.Entries > uint64(len(theirs)) {
		return nil, nil
	}
	pairs := make([][]byte, best.Verified.BookCheckpoint.Entries)
	for i := range pairs {
		pairs[i] = theirs[i].pair
	}
	ext, err := MakeCounterpartyHalf(ctx, pairs, best.Proof)
	if err != nil {
		return nil, err
	}
	if _, err := VerifyCounterpartyHalf(holderAgreement, ext); err != nil {
		return nil, err
	}
	return ext, nil
}
