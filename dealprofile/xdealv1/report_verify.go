// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// StageReport is a sealed report read against its deal's first record
// (VerifyReport).
const StageReport = "report"

// ReportPair is a sealed report as it travels: the report Capsule with its
// signature and key_id, and the report payload as its agent_input, the form
// of a sent pair (Pair).
func ReportPair(capsule, envelope, payload []byte) ([]byte, error) {
	return Pair(capsule, envelope, payload)
}

// ReportRead is what a verified report shows: the report, its Capsule id and
// the key that signed it, which is the key that signed its deal's first
// record.
type ReportRead struct {
	Report    Report
	CapsuleID string
	KeyID     string
}

// VerifyReport checks a sealed v1 report (design §9, "Verifying a report")
// against its deal's first record, first, the book pair of the record at seq
// 1 of the holder's deal book. The report Capsule verifies under its key_id,
// is a report Capsule (action_id dealprofile.ReportActionID, fyi) and commits
// its payload as its agent_input_digest; the payload is a deal_report of
// x-deal-v1 that reads as Report and would encode to the same bytes. The
// first record is a verified book pair at seq 1 holding an agreement of the
// report's deal, in the log the report rests on (rests_on.own.log_id). The
// report is signed by the key that signed that record: in x-deal-v1 a deal
// book has one signing key, since the counterparty admits a deal-book record
// only from the key its agreement names. Each row's ruling is against buyer,
// seller or null.
func VerifyReport(report, first []byte) (ReportRead, error) {
	bad := func(format string, args ...any) (ReportRead, error) {
		return ReportRead{}, stageErr(StageReport, format, args...)
	}
	if err := checkText(report); err != nil {
		return ReportRead{}, err
	}
	var p struct {
		Record     map[string]json.RawMessage `json:"record"`
		AgentInput json.RawMessage            `json:"agent_input"`
	}
	if err := strictUnmarshal(report, &p); err != nil || p.Record == nil || p.AgentInput == nil {
		return bad("not a report pair {record, agent_input}: %v", err)
	}
	if err := exactMembers(report, "record", "agent_input"); err != nil {
		return bad("not a report pair {record, agent_input}: %v", err)
	}
	capsule, id, keyID, err := verifyCapsule(StageReport, p.Record)
	if err != nil {
		return ReportRead{}, err
	}
	c, err := readCapsule(capsule)
	if err != nil {
		return ReportRead{}, err
	}
	if c.ActionID != dealprofile.ReportActionID || c.ActionType != string(emit.ActionTypeFYI) {
		return bad("not a report Capsule: action_id %q, action_type %q", c.ActionID, c.ActionType)
	}
	doc, err := decodeJSON(p.AgentInput)
	if err != nil {
		return bad("the payload: %v", err)
	}
	payload, err := canonical.JCS(doc)
	if err != nil {
		return bad("the payload: %v", err)
	}
	if sum := sha256.Sum256(payload); c.AgentInputDigest != hex.EncodeToString(sum[:]) {
		return bad("the payload is not the one the Capsule commits (agent_input_digest)")
	}
	var r Report
	if err := strictUnmarshal(payload, &r); err != nil {
		return bad("the payload does not read as a report: %v", err)
	}
	if err := sameRecord(payload, r); err != nil {
		return bad("the payload does not read as a report: %v", err)
	}
	// Go reads a null list or object as nil and writes nil back as null, so
	// the round trip keeps these; BuildReport never writes them, and a page
	// would read a null rows as no rows.
	if err := reportNulls(r); err != nil {
		return bad("the payload does not read as a report: %v", err)
	}
	if r.Type != dealprofile.ReportType || r.Profile != Profile {
		return bad("not a deal_report of %s", Profile)
	}
	for _, row := range r.Rows {
		if row.Ruling == nil || row.Ruling.Against == nil {
			continue
		}
		if a := *row.Ruling.Against; a != RoleBuyer && a != RoleSeller {
			return bad("row %q: a ruling is against buyer, seller or null, not %q", row.ID, a)
		}
	}
	b, err := VerifyBook(first)
	if err != nil {
		return ReportRead{}, err
	}
	if ag, ok := asAgree(b.Record.Body); !ok || ag.Kind != KindAgreement || b.Seq != 1 {
		return bad("the first record is not the agreement at seq 1 of a deal book")
	}
	if b.Record.Block.DealID != r.DealID || b.LogID != r.RestsOn.Own.LogID {
		return bad("the first record is not of the book the report rests on")
	}
	if keyID != b.KeyID {
		return bad("the report is signed by another key than its deal's first record")
	}
	return ReportRead{Report: r, CapsuleID: id, KeyID: keyID}, nil
}

// reportNulls refuses what the round trip keeps but BuildReport never
// writes: a null rows, report text, relied_on or late list, an owed that is
// not an object, a negative rulings_unnamed, and a relied_on record whose
// witnessed is not whether it has a time. The payload is JCS, so a raw
// member has no leading white space.
func reportNulls(r Report) error {
	switch {
	case r.Rows == nil:
		return fmt.Errorf("rows is null")
	case r.Text == nil:
		return fmt.Errorf("report is null")
	case r.RulingsUnnamed < 0:
		return fmt.Errorf("rulings_unnamed is negative")
	}
	for _, row := range r.Rows {
		switch {
		case row.ReliedOn == nil:
			return fmt.Errorf("row %q: relied_on is null", row.ID)
		case row.Late.Statements == nil || row.Late.Disputes == nil:
			return fmt.Errorf("row %q: a late list is null", row.ID)
		case len(row.Owed) == 0 || row.Owed[0] != '{':
			return fmt.Errorf("row %q: owed is not an object", row.ID)
		}
		for _, src := range row.ReliedOn {
			if src.Witnessed != (src.Time != nil) {
				return fmt.Errorf("row %q: relied_on %q is witnessed exactly when it has a time", row.ID, src.Record)
			}
		}
	}
	return nil
}
