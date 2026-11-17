// SPDX-License-Identifier: Apache-2.0

package xdealv1

import "github.com/action-state-group/evidencebook/dealprofile"

// The profile's names (design §2.2, §3.1).
const (
	// Profile is the record block's member name and its profile value.
	Profile = "x-deal-v1"
	// Canonicalization is the only canonicalization a record may name.
	Canonicalization = dealprofile.Canonicalization
	// RecordRef is the type of a digest reference to a deal record, as v0's.
	RecordRef = dealprofile.RecordRef
	// DigestAlg is the digest algorithm of every reference.
	DigestAlg = "SHA-256"
	// CommitAlg names the salted commitment dealprofile.CommitText makes.
	CommitAlg = dealprofile.CommitAlg
	// FPAlg names a record's counterparty fingerprints, as v0's.
	FPAlg = dealprofile.FPAlg
)

// The ten record types (design §7; §3.2).
const (
	TypePropose   = "propose"
	TypeAgree     = "agree"
	TypePerform   = "perform"
	TypeSettle    = "settle"
	TypeStatement = "statement"
	TypeDispute   = "dispute"
	TypeRuling    = "ruling"
	TypeAsk       = "ask"
	TypeAnswer    = "answer"
	TypeAdmit     = "admit"
)

// The journal overlay (§3.2): sealed in the journal only and never sent,
// each with v0's body (check adds payment_ref).
const (
	TypeIntent   = dealprofile.TypeIntent
	TypeCheck    = dealprofile.TypeCheck
	TypeVerdict  = dealprofile.TypeVerdict
	TypeApproval = dealprofile.TypeApproval
)

// TypeRulesVerdict is the deal-book overlay type, v0's body (§3.3).
const TypeRulesVerdict = dealprofile.TypeRulesVerdict

// The journey overlay (§3.2): v0's bodies, sealed in the journal always.
const (
	TypeMessage             = dealprofile.TypeMessage
	TypeClaim               = dealprofile.TypeClaim
	TypeEvidence            = dealprofile.TypeEvidence
	TypeDetailChange        = dealprofile.TypeDetailChange
	TypeAgentMove           = dealprofile.TypeAgentMove
	TypeDisclosure          = dealprofile.TypeDisclosure
	TypeSale                = dealprofile.TypeSale
	TypeThread              = dealprofile.TypeThread
	TypeThreadOpened        = dealprofile.TypeThreadOpened
	TypeSaleUnpost          = dealprofile.TypeSaleUnpost
	TypeCounterpartyProfile = dealprofile.TypeCounterpartyProfile
)

// RecordTypes are every x-deal-v1 record type: the ten, the journal
// overlay, rules_verdict and the journey overlay.
func RecordTypes() []string { return append([]string(nil), recordTypes...) }

var recordTypes = []string{
	TypePropose, TypeAgree, TypePerform, TypeSettle, TypeStatement, TypeDispute, TypeRuling, TypeAsk, TypeAnswer, TypeAdmit,
	TypeIntent, TypeCheck, TypeVerdict, TypeApproval, TypeRulesVerdict,
	TypeMessage, TypeClaim, TypeEvidence, TypeDetailChange, TypeAgentMove, TypeDisclosure, TypeSale, TypeThread,
	TypeThreadOpened, TypeSaleUnpost, TypeCounterpartyProfile,
}

// Kinds of an agree body (§3.3, §3.5).
const (
	KindAgreement    = "agreement"
	KindDecline      = "decline"
	KindCancellation = "cancellation"
)

// Kinds of a dispute body (§3.3).
const (
	KindDispute    = "dispute"
	KindWithdrawal = "withdrawal"
	KindChallenge  = "challenge"
)

// Roles and sides.
const (
	RoleBuyer     = "buyer"
	RoleSeller    = "seller"
	SideOfferer   = "offerer"
	SideAcceptor  = "acceptor"
	LegPayer      = "payer"
	LegPayee      = "payee"
	ValueMet      = "met"
	ValueNotMet   = "not_met"
	ValueCannot   = "cannot_tell"
	ReasonNotSel  = "not_selected"
	ReasonOther   = "other"
	ChoiceConfirm = "confirm_limits"
)
