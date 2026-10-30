// SPDX-License-Identifier: Apache-2.0

package dealprofile

// The profile's names (PROFILE.md §1-§3, §9).
const (
	// Profile is the record block's member name and its profile value.
	Profile = "x-deal-v0"
	// Canonicalization is the only canonicalization a record may name.
	Canonicalization = "jcs"
	// FPAlg names a record's counterparty fingerprints: fingerprints under
	// the deal key (DealKey).
	FPAlg = "hmac-sha256-deal-key"
	// ProfileFPAlg names the fingerprints a counterparty_profile record
	// seals: under the profile key (ProfileKey).
	ProfileFPAlg = "hmac-sha256-profile-key"
	// RecordRef is the type of a digest reference to a deal record.
	RecordRef = "deal-record"
	// CommitAlg names the salted commitment CommitText makes.
	CommitAlg = "sha256-jcs-nonce256"
	// DealIDMember is the top-level Capsule member a step carries its deal
	// id in, which a composition joins the parties' copies on.
	DealIDMember = "deal_id"
)

// The x-deal-v0 record types (PROFILE.md §3), in the schema's order.
const (
	TypeIntent              = "intent"
	TypeBaseline            = "baseline"
	TypeMessage             = "message"
	TypeClaim               = "claim"
	TypeEvidence            = "evidence"
	TypeDetailChange        = "detail_change"
	TypeCheck               = "check"
	TypeVerdict             = "verdict"
	TypeApproval            = "approval"
	TypeAction              = "action"
	TypeOutcome             = "outcome"
	TypeClose               = "close"
	TypeDisclosure          = "disclosure"
	TypeSale                = "sale"
	TypeCounterpartyProfile = "counterparty_profile"
	TypeThread              = "thread"
	TypeThreadOpened        = "thread_opened"
	TypeSaleCut             = "sale_cut"
	TypeSaleClose           = "sale_close"
	TypeAgentMove           = "agent_move"
)

// RecordTypes are the x-deal-v0 record types, in the schema's order.
func RecordTypes() []string { return append([]string(nil), recordTypes...) }

var recordTypes = []string{
	TypeIntent, TypeBaseline, TypeMessage, TypeClaim, TypeEvidence, TypeDetailChange, TypeCheck,
	TypeVerdict, TypeApproval, TypeAction, TypeOutcome, TypeClose, TypeDisclosure, TypeSale,
	TypeCounterpartyProfile, TypeThread, TypeThreadOpened, TypeSaleCut, TypeSaleClose, TypeAgentMove,
}

// The typed action records and contract types (PROFILE.md §9).
const (
	TypeTaskAuthority    = "task-authority/v0"
	TypeProposedAction   = "proposed-action/v0"
	TypeActionEvaluation = "action-evaluation/v0"
	TypeActionApproval   = "action-approval/v0"
	TypeActionRecord     = "action-record/v0"
	TypeActionOutcome    = "action-outcome/v0"
	TypeActionReport     = "action-report/v0"
	TypeCheckRequest     = "check-request/v0"
	TypeCheckResponse    = "check-response/v0"
	TypeCommercialBounds = "commercial-bounds/v0"
	// TypedRecordRef is the type of a digest reference to a typed record.
	TypedRecordRef = "record"
	// TypedFPAlg names a typed record's counterparty fingerprints.
	TypedFPAlg = "hmac-sha256-chain-key"
)

// Materiality modes an evaluation states, so that no predicate is told apart
// from a missing field.
const (
	MaterialityModePredicate = "predicate"
	MaterialityModeNone      = "none_fail_safe"
)

// The sealed report's names (PROFILE.md §1, "Sealed reports are not steps").
const (
	// ReportActionID is the action_id of every report Capsule.
	ReportActionID = "capsulectl-deal-report"
	// ReportType is the report payload's own type member (PROFILE.md §1),
	// not the book header's record type, which is ReportRecordType.
	ReportType = "deal_report"
	// ReportPointer is the bundle's x-deal-v0 extension member that names
	// the copy's report Capsule.
	ReportPointer = "sealed_report"
	// ReportExtension is the bundle extension carrying a standalone report:
	// {"record": the Capsule with its signature and key_id, "agent_input":
	// its payload}.
	ReportExtension = "x-deal-report/v0"
)

// The audiences a copy, and its report, are cut for.
const (
	AudienceKeep         = "keep"
	AudienceCounterparty = "counterparty"
	AudienceAdjudicator  = "adjudicator"
)

// The evidence record header a step or report carries on a book.
const (
	StepRecordType = "deal_step"
	// ReportRecordType is the record_type of a report's evidence record
	// header on a book, not the payload's type member (ReportType).
	ReportRecordType = "deal_report"
	// LocalField commits a step's local event (its raw values and the
	// nonces of its commitments); its opening never leaves the device.
	LocalField = "deal_local"
	// AmountField and CurrencyField commit the deal's closed money terms.
	AmountField   = "amount"
	CurrencyField = "currency"
)
