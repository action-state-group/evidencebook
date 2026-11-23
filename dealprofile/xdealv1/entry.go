// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

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
	// EntryNotEntered: the copy's checkpoint, by its signed time, is after
	// the ruling's witnessed stamp, and the book under it holds no statement
	// naming the ruling.
	EntryNotEntered = "not_entered"
	// EntryNotShown: the copy's checkpoint is not after the ruling's
	// witnessed stamp, or the ruling has no witnessed stamp, so the copy shows
	// nothing about it.
	EntryNotShown = "not_shown"
)

// EntryInput is what the entry check reads: one party's copy (a bundle
// `deal report --share ... --bundle` writes, for the adjudicator or the
// other side), the ruling's record digest, the ruling's witnessed stamp
// time, the adjudicator's key id, and, for the witness, the party's stamp
// proof of the copy's checkpoint with the receipts it sent and the witness
// directory to verify them under. RulingAt is the time a witness the
// agreement names gave the adjudicator's stamp that covers the ruling (for
// the adjudicator, Shelf.StampTimeByKey), RFC 3339 in UTC; never the
// ruling's own at, which is the adjudicator's word. "" means no such stamp,
// and then a copy without the statement shows nothing (EntryNotShown).
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
	case vb.LogID != read.Report.RestsOn.Own.LogID:
		return Entry{}, caseErr("the bundle is not of the deal book the copy's report rests on")
	case vb.IntervalFirst != 1 || vb.IntervalLast != read.Report.RestsOn.Own.Size:
		return Entry{}, caseErr("the bundle does not carry the whole deal book, from its agreement to the report's checkpoint")
	}
	if !slices.ContainsFunc(vb.Records, func(r evidencebook.PeerRecord) bool { return r.Seq == 1 && r.RecordID == first.RecordID }) {
		return Entry{}, caseErr("the bundle's first record is not the copy's agreement")
	}
	// Every place from 1 to the checkpoint is carried with its header, so a
	// copy that leaves a record out cannot read as "not entered".
	carried := map[uint64]bool{}
	for _, r := range vb.Records {
		if r.Header != nil {
			carried[r.Seq] = true
		}
	}
	for seq := uint64(1); seq <= read.Report.RestsOn.Own.Size; seq++ {
		if !carried[seq] {
			return Entry{}, caseErr("the bundle withholds the record at %d: the copy must carry every record and payload", seq)
		}
	}
	signed, err := aacwitness.ParseSignedCheckpoint(vb.AnchorStatement)
	if err != nil {
		return Entry{}, caseErr("the copy's checkpoint: %v", err)
	}
	e := Entry{Role: ag.Role, Key: first.KeyID, Checkpoint: read.Report.DealCheckpoint, CheckpointAt: signed.Timestamp}
	for _, r := range vb.Records {
		if r.Header == nil || len(r.Header.PayloadCommitments) != 1 {
			return Entry{}, caseErr("the record at %d carries no single payload", r.Seq)
		}
		payload, held := vb.Payloads[r.Header.PayloadCommitments[0]]
		if !held {
			return Entry{}, caseErr("the bundle withholds the record at %d: the copy must carry every payload", r.Seq)
		}
		rec, err := Decode(payload)
		if err != nil {
			return Entry{}, caseErr("the record at %d is not an x-deal-v1 record: %v", r.Seq, err)
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
	case in.RulingAt == "":
		e.State = EntryNotShown
	default:
		ruled, err := utcTime(in.RulingAt)
		if err != nil {
			return Entry{}, caseErr("the ruling's stamp time: %v", err)
		}
		checkpointed, err := time.Parse(time.RFC3339, signed.Timestamp)
		if err != nil {
			return Entry{}, caseErr("the copy's checkpoint time %q: %v", signed.Timestamp, err)
		}
		e.State = EntryNotShown
		if checkpointed.After(ruled) {
			e.State = EntryNotEntered
		}
	}
	return e, nil
}

// utcTime parses an RFC 3339 time written in UTC with a Z.
func utcTime(v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, err
	}
	if !strings.HasSuffix(v, "Z") {
		return time.Time{}, fmt.Errorf("%q is not in UTC (Z)", v)
	}
	return t, nil
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
