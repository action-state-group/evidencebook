// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	emit "github.com/action-state-group/capsule-emit/go"
)

// A sealed report (PROFILE.md §1, "Sealed reports are not steps") seals one
// copy's readable deal text as one more Capsule: action_id ReportActionID,
// fyi, not chained, with the payload {"type": "deal_report", "profile",
// "deal_id", "audience", "nonce", "report"}. A standalone report, appended to
// no log, adds "deal_checkpoint", the deal checkpoint the copy is made at.
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
// commitment.
func ReportCapsule(operator, developer string, at time.Time) emit.Input {
	return emit.Input{
		ActionID: ReportActionID, ActionType: emit.ActionTypeFYI, Operator: operator, Developer: developer,
		Timestamp: at.UTC().Truncate(time.Second),
	}
}
