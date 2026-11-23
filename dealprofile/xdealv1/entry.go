// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"slices"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// The entry check (evidencebook#773; Ethan, 2026-11-23: option one): what a
// party's copy shows about whether it entered a ruling in its deal book. The
// proof of entry is the party's own records: its statement naming the
// ruling (PROFILE.md §5), in its book under a checkpoint signed by the
// checkpoint key its agreement names, and, where the agreement names
// witnesses, a receipt from one of them over the cadence checkpoint that
// carries that book checkpoint. A copy is a disclose act, so the party's
// book reaches the adjudicator only in one (Sherlock2, #773).

// The entry states. A reader words them; none is a refusal.
const (
	// EntryEntered: the statement is in the book under the copy's
	// checkpoint, and a named witness timed that checkpoint.
	EntryEntered = "entered"
	// EntryUnwitnessed: the statement is in the book under the copy's
	// checkpoint, and no receipt from a named witness verified.
	EntryUnwitnessed = "entered_unwitnessed"
	// EntryNotEntered: the copy's checkpoint is after the ruling and its
	// book holds no statement naming it.
	EntryNotEntered = "not_entered"
	// EntryNotShown: the copy's report predates the ruling, so it shows
	// nothing about it.
	EntryNotShown = "not_shown"
)

// EntryInput is what the entry check reads: one party's copy (a bundle
// `deal report --share ... --bundle` writes), the ruling's record digest and
// time, the adjudicator's key id, and, for the witness, the party's stamp
// proof of the copy's checkpoint with the receipts it sent and the witness
// directory to verify them under.
type EntryInput struct {
	Bundle         []byte
	Ruling         string
	RulingAt       string
	AdjudicatorKey string
	StampProof     *evidencebook.StampProof
	Receipts       []evidencebook.WitnessReceipt
	Witnesses      aacwitness.Directory
}

// Entry is one party's entry state: the state, the party's role and key,
// the naming statement's record id ("" when none), and the checkpoint the
// copy rests on with its time.
type Entry struct {
	State        string
	Role         string
	Key          string
	Statement    string
	Checkpoint   dealprofile.Checkpoint
	CheckpointAt string
}

// EntryOf reads a party's copy for the entry check. The bundle must verify
// in full, rest on a checkpoint signed by the checkpoint key of the
// agreement it carries, of the same book its report rests on, and that
// agreement must name the adjudicator. It never reports a refusal: a party
// that sent no later copy is not shown, and one whose later checkpoint holds
// no naming statement has not entered the ruling as of that checkpoint.
func EntryOf(in EntryInput) (Entry, error) {
	vb, err := evidencebook.VerifyBundle(in.Bundle)
	if err != nil {
		return Entry{}, err
	}
	if err := vb.FullyVerified(); err != nil {
		return Entry{}, err
	}
	report, ext := vb.Extensions[dealprofile.ReportExtension], vb.Extensions[Profile]
	if report == nil || ext == nil {
		return Entry{}, caseErr("the bundle is not an x-deal-v1 report copy")
	}
	read, err := VerifyReportBundle(report, ext)
	if err != nil {
		return Entry{}, err
	}
	var x ReportBundle
	if err := json.Unmarshal(ext, &x); err != nil {
		return Entry{}, caseErr("the x-deal-v1 extension: %v", err)
	}
	first, err := VerifyBook(x.Book.Agreement)
	if err != nil {
		return Entry{}, err
	}
	ag, ok := asAgree(first.Record.Body)
	switch {
	case !ok || ag.Kind != KindAgreement:
		return Entry{}, caseErr("the copy carries no agreement")
	case ag.Adjudicator.V == nil || ag.Adjudicator.V.KeyID != in.AdjudicatorKey:
		return Entry{}, caseErr("the copy's agreement does not name this adjudicator")
	case !vb.AnchorAuthenticated || vb.AnchorKeyID != ag.CheckpointKey:
		return Entry{}, caseErr("the copy's checkpoint is not signed by the checkpoint key its agreement names")
	case vb.Anchor.Root != read.Report.DealCheckpoint.Root || vb.Anchor.MMRSize != read.Report.DealCheckpoint.MMRSize:
		return Entry{}, caseErr("the copy's report does not rest on the bundle's checkpoint")
	}
	signed, err := aacwitness.ParseSignedCheckpoint(vb.AnchorStatement)
	if err != nil {
		return Entry{}, caseErr("the copy's checkpoint: %v", err)
	}
	e := Entry{Role: ag.Role, Key: first.KeyID, Checkpoint: read.Report.DealCheckpoint, CheckpointAt: signed.Timestamp}
	for _, r := range vb.Records {
		if r.Header == nil || len(r.Header.PayloadCommitments) != 1 {
			continue
		}
		payload, held := vb.Payloads[r.Header.PayloadCommitments[0]]
		if !held {
			continue
		}
		rec, err := Decode(payload)
		if err != nil {
			continue
		}
		st, ok := bodyOf[StatementBody](rec.Body)
		if ok && rec.Block.DealID == first.Record.Block.DealID && st.Names != nil && st.Names.Ruling != nil && st.Names.Ruling.Digest == in.Ruling {
			e.Statement = r.RecordID
			break
		}
	}
	switch {
	case e.Statement != "":
		e.State = EntryUnwitnessed
		if witnessedAt(in, vb, ag) {
			e.State = EntryEntered
		}
	case read.Report.AsOf >= in.RulingAt:
		e.State = EntryNotEntered
	default:
		e.State = EntryNotShown
	}
	return e, nil
}

// witnessedAt reports whether a receipt from a witness the agreement names
// verifies over the cadence checkpoint of the party's stamp proof, and that
// proof carries the copy's own book checkpoint, signed by the agreement's
// checkpoint key on its shelf log.
func witnessedAt(in EntryInput, vb evidencebook.VerifiedBundle, ag AgreeBody) bool {
	if in.StampProof == nil || len(in.Receipts) == 0 {
		return false
	}
	v, err := evidencebook.VerifyStampProof(*in.StampProof)
	if err != nil || v.KeyID != ag.CheckpointKey || v.ShelfCheckpoint.LogID != ag.ShelfLogID ||
		v.BookCheckpoint.Root != vb.Anchor.Root || v.BookCheckpoint.TreeSize != vb.Anchor.MMRSize {
		return false
	}
	named := witnessKeys(ag.Witnesses)
	for _, rc := range in.Receipts {
		check, keyID, err := evidencebook.VerifyWitnessReceipt(in.StampProof.ShelfCheckpoint, rc, in.Witnesses)
		if err == nil && check.State == evidencebook.WitnessVerified && slices.Contains(named, keyID) {
			return true
		}
	}
	return false
}
