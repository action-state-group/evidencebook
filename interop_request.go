// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// The request draft's identifiers, subject forms, refusal reasons and
// request types are defined in go/evidencerequest
// (draft-mih-agent-evidence-request-00, profile evidence-interop/0); these
// names keep evidencebook's API.
const (
	ProfileID             = evidencerequest.ProfileID
	RequestDraft          = evidencerequest.RequestDraft
	DerivationHistoryCard = evidencerequest.DerivationHistoryCard
)

// SubjectKind names one of the six subject forms (ER §3.1).
type SubjectKind = evidencerequest.SubjectKind

const (
	SubjectFullHistory = evidencerequest.SubjectFullHistory
	SubjectCheckpoints = evidencerequest.SubjectCheckpoints
	SubjectRecord      = evidencerequest.SubjectRecord
	SubjectRange       = evidencerequest.SubjectRange
	SubjectCorrelation = evidencerequest.SubjectCorrelation
	SubjectExchange    = evidencerequest.SubjectExchange
)

// SubjectForms lists the six forms in code-point order.
var SubjectForms = evidencerequest.SubjectForms

// The registered refusal reasons (ER §13).
const (
	ReasonNotAuthorized         = evidencerequest.ReasonNotAuthorized
	ReasonNoSuchSubject         = evidencerequest.ReasonNoSuchSubject
	ReasonCoverageUnsatisfiable = evidencerequest.ReasonCoverageUnsatisfiable
	ReasonDerivationUnsupported = evidencerequest.ReasonDerivationUnsupported
	ReasonPolicyDeclined        = evidencerequest.ReasonPolicyDeclined
	ReasonDeadlineUnmet         = evidencerequest.ReasonDeadlineUnmet
	ReasonRequestMalformed      = evidencerequest.ReasonRequestMalformed
	ReasonRetentionExpired      = evidencerequest.ReasonRetentionExpired
)

// RefusalReasons is the registry, in the draft's order.
var RefusalReasons = evidencerequest.RefusalReasons

type (
	Subject         = evidencerequest.Subject
	Freshness       = evidencerequest.Freshness
	Coverage        = evidencerequest.Coverage
	EvidenceRequest = evidencerequest.Request
)

// EncodeRequest encodes r in the given media type: JCS for JSON, core
// deterministic CBOR for CBOR.
func EncodeRequest(r EvidenceRequest, mediaType string) ([]byte, error) {
	return evidencerequest.EncodeRequest(r, mediaType)
}

// requestCheck is evidencebook's view of evidencerequest.ParseRequest:
// the parsed request, the refusal reason when it is not well formed, and
// min_freshness as a time when the request gave one.
type requestCheck struct {
	request   EvidenceRequest
	floorTime time.Time
	reason    string
	why       string
	detail    string
}

// readRequest validates received request octets (profile §6.1 steps 1 to 5).
func readRequest(m Message) requestCheck {
	check := evidencerequest.ParseRequest(m)
	out := requestCheck{request: check.Request, reason: check.Reason, why: check.Why, detail: check.Detail}
	if check.Reason == "" {
		if f := check.Request.Coverage.MinFreshness; f != nil && f.Time != "" {
			out.floorTime, _ = evidencerequest.ParseRequestTime(f.Time)
		}
	}
	return out
}
