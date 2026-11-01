// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	emit "github.com/action-state-group/capsule-emit/go"
	"golang.org/x/text/unicode/norm"
)

// A sealed report (PROFILE.md §1, "Sealed reports are not steps") seals one
// copy's readable deal text as one more Capsule: action_id ReportActionID,
// fyi, not chained, with the payload {"type": "deal_report", "profile",
// "deal_id", "audience", "nonce", "report"}. A standalone report, appended to
// no log, adds "deal_checkpoint", the deal checkpoint the copy is made at, and
// an adjudicator's copy adds "referee", the referee the party hands it to.
// What the report's lines say is the producer's.

// Checkpoint is the deal checkpoint a standalone report binds.
type Checkpoint struct {
	LogID   string `json:"log_id"`
	MMRSize uint64 `json:"mmr_size"`
	Root    string `json:"root"`
}

// Report is a sealed report's payload before it is encoded.
type Report struct {
	DealID   string
	Audience string
	// Nonce is a fresh 256-bit random value (64 lowercase hex), so the
	// Capsule's digest confirms no guess at the text.
	Nonce string
	// Body is the copy's deal text, already cut for its audience.
	Body map[string]any
	// Checkpoint is set for a standalone report only.
	Checkpoint *Checkpoint
	// Referee is the referee an adjudicator's copy is handed to, as the
	// party names it; required on that audience and refused on the others.
	// It is sealed NFC and trimmed, so two parties who name the same referee
	// seal the same bytes.
	Referee string
}

var nonceHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ReportPayload is a report's payload: its JCS bytes, numbers read back as a
// verifier reads them.
func ReportPayload(r Report) ([]byte, error) {
	switch r.Audience {
	case AudienceKeep, AudienceCounterparty, AudienceAdjudicator:
	default:
		return nil, fmt.Errorf("dealprofile: report audience %q is not a copy audience", r.Audience)
	}
	if !nonceHex.MatchString(r.Nonce) {
		return nil, fmt.Errorf("dealprofile: report nonce must be 64 lowercase hex")
	}
	m := map[string]any{
		"type": ReportType, "profile": Profile, "deal_id": r.DealID, "audience": r.Audience,
		"nonce": r.Nonce, "report": r.Body,
	}
	if r.Checkpoint != nil {
		m["deal_checkpoint"] = r.Checkpoint
	}
	referee := NormalizeReferee(r.Referee)
	switch {
	case r.Audience == AudienceAdjudicator && referee == "":
		return nil, fmt.Errorf("dealprofile: an adjudicator's report names the referee it is handed to")
	case r.Audience == AudienceAdjudicator:
		m["referee"] = referee
	case r.Referee != "":
		return nil, fmt.Errorf("dealprofile: only an adjudicator's report names a referee, not a %s copy", r.Audience)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	doc, err := decode(raw)
	if err != nil {
		return nil, err
	}
	return canonical.JCS(doc)
}

// ReportCapsule is a report's Capsule members, sealed at at (to the second)
// by operator, naming the producer's own developer (the capsulectl deal
// plugin names "capsulectl-deal"). The producer seals it with the payload as
// JSON (json.RawMessage) and its own key; on a book, the record header it
// carries is ReportRecordType, with no subject_ref, link or field
// commitment. An empty developer is refused.
func ReportCapsule(operator, developer string, at time.Time) (emit.Input, error) {
	if developer == "" {
		return emit.Input{}, fmt.Errorf("dealprofile: a report needs the producer's developer name")
	}
	return emit.Input{
		ActionID: ReportActionID, ActionType: emit.ActionTypeFYI, Operator: operator, Developer: developer,
		Timestamp: at.UTC().Truncate(time.Second),
	}, nil
}

// NormalizeReferee is a referee's name as a report seals it: NFC, with
// leading and trailing white space removed.
func NormalizeReferee(v string) string { return strings.TrimSpace(norm.NFC.String(v)) }

// A referee both sides chose (PROFILE.md §10, "A referee both sides chose"):
// each party names the referee in its own adjudicator's copy, and a reader
// says "a referee you both chose" only when both parties' copies name the
// same one. One copy names the referee that side chose, never both sides'.

// RefereeError is why two copies name no referee both sides chose. Reason is
// one of the Referee* reasons; for RefereeDifferent, Named holds each copy's
// referee in the order given, so a reader can show the mismatch.
type RefereeError struct {
	Reason string
	Detail string
	Named  []string
}

func (e *RefereeError) Error() string {
	return "dealprofile: no referee both sides chose: " + e.Detail
}

// The reasons a RefereeError gives.
const (
	// RefereeOneCopy: not exactly two copies; one side's choice is not both.
	RefereeOneCopy = "one_copy"
	// RefereeMalformed: a copy's payload is not a deal_report of this profile.
	RefereeMalformed = "malformed"
	// RefereeNotAdjudicator: a copy is cut for another audience.
	RefereeNotAdjudicator = "not_adjudicator"
	// RefereeNotNamed: a copy names no referee, as one sealed before the
	// profile named it.
	RefereeNotNamed = "not_named"
	// RefereeOneParty: both copies are under one party's key.
	RefereeOneParty = "one_party"
	// RefereeTwoDeals: the copies are of different deals.
	RefereeTwoDeals = "two_deals"
	// RefereeDifferent: the copies name different referees.
	RefereeDifferent = "different"
)

// ReportCopy is one party's copy as its reader admitted it: the sealed
// report's disclosed payload, and the party's signing key it verified under
// (PROFILE.md §10, "An adjudicator's check"). SharedReferee checks neither
// signature nor admission; it reads what the caller admitted.
type ReportCopy struct {
	Payload []byte
	Party   string
}

type reportReferee struct {
	DealID, Audience string
	Referee          *string
}

// ReportReferee reads the referee a sealed report's payload names: "" when it
// names none. A payload that is not a deal_report of this profile is refused
// with RefereeMalformed.
func ReportReferee(payload []byte) (string, error) {
	r, err := readReportReferee(payload)
	if err != nil || r.Referee == nil {
		return "", err
	}
	return *r.Referee, nil
}

// readReportReferee reads members by their exact names (encoding/json would
// match a struct field case-insensitively), each a string where present.
func readReportReferee(payload []byte) (reportReferee, error) {
	var r reportReferee
	var m map[string]json.RawMessage
	// encoding/json would read invalid UTF-8 as U+FFFD; a sealed payload is
	// JCS, which is valid UTF-8. Unmarshal refuses any bytes after the value.
	if !utf8.Valid(payload) {
		return r, &RefereeError{Reason: RefereeMalformed, Detail: "a copy's report is not UTF-8"}
	}
	if err := json.Unmarshal(payload, &m); err != nil || m == nil {
		return r, &RefereeError{Reason: RefereeMalformed, Detail: "a copy's report is not a JSON object"}
	}
	str := func(k string) (*string, bool) {
		raw, ok := m[k]
		if !ok {
			return nil, true
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, false
		}
		return &v, true
	}
	typ, ok1 := str("type")
	profile, ok2 := str("profile")
	dealID, ok3 := str("deal_id")
	audience, ok4 := str("audience")
	referee, ok5 := str("referee")
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || typ == nil || *typ != ReportType || profile == nil || *profile != Profile ||
		dealID == nil || *dealID == "" || audience == nil {
		return r, &RefereeError{Reason: RefereeMalformed, Detail: "a copy's report is not a deal_report of " + Profile}
	}
	r.DealID, r.Audience, r.Referee = *dealID, *audience, referee
	return r, nil
}

// SharedReferee returns the referee both sides chose: exactly two copies,
// under two parties' keys, of one deal, each an adjudicator's copy naming the
// same referee byte for byte. Anything else is a *RefereeError.
func SharedReferee(copies ...ReportCopy) (string, error) {
	if len(copies) != 2 {
		return "", &RefereeError{Reason: RefereeOneCopy, Detail: fmt.Sprintf("%d copies: both sides' copies are needed, one each", len(copies))}
	}
	var got [2]reportReferee
	for i, c := range copies {
		r, err := readReportReferee(c.Payload)
		if err != nil {
			return "", err
		}
		if r.Audience != AudienceAdjudicator {
			return "", &RefereeError{Reason: RefereeNotAdjudicator, Detail: fmt.Sprintf("a copy is cut for %q, not an adjudicator", r.Audience)}
		}
		if r.Referee == nil || *r.Referee == "" {
			return "", &RefereeError{Reason: RefereeNotNamed, Detail: "a copy names no referee"}
		}
		got[i] = r
	}
	switch {
	case copies[0].Party == "" || copies[1].Party == "" || copies[0].Party == copies[1].Party:
		return "", &RefereeError{Reason: RefereeOneParty, Detail: "both copies are one party's"}
	case got[0].DealID != got[1].DealID:
		return "", &RefereeError{Reason: RefereeTwoDeals, Detail: "the copies are of different deals"}
	case *got[0].Referee != *got[1].Referee:
		return "", &RefereeError{Reason: RefereeDifferent, Detail: fmt.Sprintf("one side named %q, the other %q", *got[0].Referee, *got[1].Referee), Named: []string{*got[0].Referee, *got[1].Referee}}
	}
	return *got[0].Referee, nil
}
