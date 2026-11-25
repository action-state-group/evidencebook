// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"

	"github.com/action-state-group/evidencebook/dealprofile"
)

// The record types below follow x-deal-v1.schema.json member for member: a
// required member is a plain field, an optional one is omitted when zero (a
// pointer where zero is a value, a nil slice or map where an empty one is),
// a member that may be null is a *Null (with no omitzero where it is
// REQUIRED), and a member the schema leaves open is json.RawMessage.
// They carry no rule: Check reads a record against the schema and the
// record checks.

// Ref is a digest reference to a deal record (v0's digest_ref): type
// "deal-record", digest_alg "SHA-256", the cited record's record digest.
type Ref = dealprofile.Ref

// RelRef is a typed citation of an earlier record, as v0's rel_ref.
type RelRef = dealprofile.RelRef

// Producer names the software that sealed a record, as v0's.
type Producer = dealprofile.Producer

// Counterparty is v0's fingerprint block, hmac-sha256-deal-key.
type Counterparty = dealprofile.Counterparty

// Terms is v0's terms.
type Terms = dealprofile.Terms

// Block is the x-deal-v1 block every v1 record carries (§3.1). Seq, Prev and
// AgreementRef are a book's (a deal book's or a case book's), Seen a deal
// book's; journal records carry none of them, so a record sent whole shows
// no place in the journal.
type Block struct {
	Profile          string        `json:"profile"`
	Canonicalization string        `json:"canonicalization"`
	DealID           string        `json:"deal_id,omitzero"`
	RecordType       string        `json:"record_type"`
	At               string        `json:"at"`
	Seq              int64         `json:"seq,omitzero"`
	Prev             *Ref          `json:"prev,omitzero"`
	AgreementRef     *Ref          `json:"agreement_ref,omitzero"`
	Seen             Null[string]  `json:"seen,omitzero"`
	Refs             []RelRef      `json:"refs,omitzero"`
	Marks            []string      `json:"marks,omitzero"`
	Counterparty     *Counterparty `json:"counterparty,omitzero"`
	Producer         *Producer     `json:"producer,omitzero"`
	CommitAlg        string        `json:"commit_alg,omitzero"`
}

// Null is a member that is present and may be null: V is the value, or nil
// for null. Its zero value is the member absent (omitzero leaves it out);
// encoding/json hands UnmarshalJSON a JSON null too, so a null read keeps
// Present.
type Null[T any] struct {
	V       *T
	Present bool
}

// NullOf is a present null.
func NullOf[T any]() Null[T] { return Null[T]{Present: true} }

// Some is a present value.
func Some[T any](v T) Null[T] { return Null[T]{V: &v, Present: true} }

// IsZero says the member is absent.
func (n Null[T]) IsZero() bool { return !n.Present }

// IsNull says the member is present and null.
func (n Null[T]) IsNull() bool { return n.Present && n.V == nil }

// MarshalJSON writes the value or null.
func (n Null[T]) MarshalJSON() ([]byte, error) { return json.Marshal(n.V) }

// UnmarshalJSON reads the value or null.
func (n *Null[T]) UnmarshalJSON(b []byte) error {
	n.Present = true
	return json.Unmarshal(b, &n.V)
}

// StatementItem is one statement on a row (§3.3).
type StatementItem struct {
	Row    string `json:"row"`
	Value  string `json:"value"`
	Remedy string `json:"remedy,omitzero"`
}

// Date is a row date: a fixed time, or a number of seconds after the time
// of a named record (rowstatus.Date's shape).
type Date struct {
	At      string `json:"at,omitzero"`
	After   string `json:"after,omitzero"`
	Seconds *int64 `json:"seconds,omitzero"`
}

// RowRemedy is a remedy a row allows, and the seconds it must be made
// within.
type RowRemedy struct {
	ID            string `json:"id"`
	WithinSeconds int64  `json:"within_seconds"`
}

// Row is one term row of an offer (§4.2).
type Row struct {
	ID         string          `json:"id"`
	OwedBy     string          `json:"owed_by"`
	Owed       json.RawMessage `json:"owed"`
	Due        Date            `json:"due"`
	WindowEnd  Date            `json:"window_end"`
	Recomputed bool            `json:"recomputed,omitzero"`
	Remedies   []RowRemedy     `json:"remedies,omitzero"`
}

// Named is a record a row date hangs on: the side that writes it and its
// latest date.
type Named struct {
	Side   string `json:"side"`
	Latest string `json:"latest"`
}

// TermsRows is an offer's rows and the named records they hang on (§4.2).
// Its rows digest is SHA-256 over its JCS bytes (RowsDigest).
type TermsRows struct {
	Rows  []Row            `json:"rows"`
	Named map[string]Named `json:"named"`
}

// Adjudicator names an adjudicator: its key, and the digest and location of
// its rules (design §8).
type Adjudicator struct {
	KeyID       string `json:"key_id"`
	RulesDigest string `json:"rules_digest"`
	RulesURL    string `json:"rules_url"`
}

// Witness names a witness by its verified key id, with its URL only as a
// hint where to find it (Steven, #482 Q1): a moved witness stays the
// same witness, and two URLs of one witness are one witness.
type Witness struct {
	KeyID string `json:"key_id"`
	URL   string `json:"url,omitzero"`
}

// AskAddress is how to ask the offerer for evidence: its evidence
// commitment's address and digest.
type AskAddress struct {
	URL    string `json:"url"`
	Digest string `json:"digest"`
}

// ProposeBody is a propose record's body: an offer or a counter-offer
// (§3.3). To and Adjudicator are REQUIRED and may be null.
type ProposeBody struct {
	Role        string            `json:"role"`
	To          Null[string]      `json:"to"`
	CounterTo   *Ref              `json:"counter_to,omitzero"`
	Terms       Terms             `json:"terms"`
	PackDigest  string            `json:"pack_digest"`
	PackURL     string            `json:"pack_url,omitzero"`
	StatedAt    string            `json:"stated_at"`
	AgreeBy     string            `json:"agree_by"`
	TermsRows   TermsRows         `json:"terms_rows"`
	Adjudicator Null[Adjudicator] `json:"adjudicator"`
	// Forums are the forums both sides accept for a dispute: the recourse
	// clause (#404). Empty exactly when Adjudicator is null.
	Forums        []string    `json:"forums"`
	Witnesses     []Witness   `json:"witnesses"`
	Ask           *AskAddress `json:"ask,omitzero"`
	Profiles      []string    `json:"profiles,omitzero"`
	ListingDigest string      `json:"listing_digest,omitzero"`
}

// AgreeBody is an agree record's body: an agreement, a decline or a
// cancellation, told apart by Kind (§3.3, §3.5). The agreement's members
// are set only on an agreement, Reason and ReasonCommitment only on a
// decline or cancellation; the schema refuses any other mix.
type AgreeBody struct {
	Kind  string `json:"kind"`
	Offer Ref    `json:"offer"`
	// The agreement's members.
	RowsDigest  string            `json:"rows_digest,omitzero"`
	Side        string            `json:"side,omitzero"`
	Role        string            `json:"role,omitzero"`
	Admit       *Ref              `json:"admit,omitzero"`
	Intent      *Ref              `json:"intent,omitzero"`
	Witnesses   []Witness         `json:"witnesses,omitzero"`
	Adjudicator Null[Adjudicator] `json:"adjudicator,omitzero"`
	// Forums are the agreement's recourse clause, the offer's. An agreement
	// always carries the member: with a null adjudicator set it to an empty,
	// non-nil slice, since a nil one is left out (omitzero, as decline and
	// cancellation carry no forums).
	Forums            []string     `json:"forums,omitzero"`
	CounterpartyLayer *bool        `json:"counterparty_layer,omitzero"`
	CounterpartyKey   Null[string] `json:"counterparty_key,omitzero"`
	CheckpointKey     string       `json:"checkpoint_key,omitzero"`
	ShelfLogID        string       `json:"shelf_log_id,omitzero"`
	// A decline's or a cancellation's.
	Reason           string `json:"reason,omitzero"`
	ReasonCommitment string `json:"reason_commitment,omitzero"`
}

// RemedyFor names the row a remedy is for and, optionally, the ruling it
// points at (§3.3).
type RemedyFor struct {
	Row    string `json:"row"`
	Ruling *Ref   `json:"ruling,omitzero"`
}

// PerformBody is a perform record's body: delivery or another act, or a
// remedy that is not a payment (§3.3).
type PerformBody struct {
	Act              string          `json:"act"`
	Observed         string          `json:"observed"`
	DetailCommitment string          `json:"detail_commitment,omitzero"`
	Statements       []StatementItem `json:"statements"`
	Check            *Ref            `json:"check,omitzero"`
	RemedyFor        *RemedyFor      `json:"remedy_for,omitzero"`
}

// SettleBody is a settle record's body: a payment leg, a refund or another
// remedy that is a payment (§3.3). The leg itself is the Capsule's
// top-level settlement member, as v0 carries it.
type SettleBody struct {
	Leg        string          `json:"leg"`
	Statements []StatementItem `json:"statements"`
	Check      *Ref            `json:"check,omitzero"`
	Supersedes *Ref            `json:"supersedes,omitzero"`
	Reverses   *Ref            `json:"reverses,omitzero"`
	RemedyFor  *RemedyFor      `json:"remedy_for,omitzero"`
}

// StatementNames is the arrived record a statement names: a cancellation or
// a ruling, exactly one.
type StatementNames struct {
	Cancellation *Ref `json:"cancellation,omitzero"`
	Ruling       *Ref `json:"ruling,omitzero"`
}

// StatementBody is a statement record's body (§3.3).
type StatementBody struct {
	Statements []StatementItem `json:"statements"`
	Names      *StatementNames `json:"names,omitzero"`
}

// DisputeBody is a dispute record's body: a dispute, a withdrawal or a
// challenge, told apart by Kind (§3.3). A dispute names its Forum, the
// process that resolves it (Steven's AAC#626 ruling, #482 Q4).
type DisputeBody struct {
	Kind string `json:"kind"`
	// A dispute's and a withdrawal's.
	Row string `json:"row,omitzero"`
	// A dispute's.
	Statements  []Ref  `json:"statements,omitzero"`
	Adjudicator string `json:"adjudicator,omitzero"`
	Forum       string `json:"forum,omitzero"`
	// A challenge's.
	Ruling            *Ref   `json:"ruling,omitzero"`
	GroundsCommitment string `json:"grounds_commitment,omitzero"`
}

// RulingKeys are the record keys of a deal's two sides.
type RulingKeys struct {
	Buyer  string `json:"buyer"`
	Seller string `json:"seller"`
}

// RulingRemedy is the remedy a ruling orders.
type RulingRemedy struct {
	Kind        string `json:"kind"`
	AmountMinor *int64 `json:"amount_minor,omitzero"`
	Currency    string `json:"currency,omitzero"`
	Due         string `json:"due"`
}

// RulingRead is the last record id of each deal book the adjudicator read,
// null for a book it did not read.
type RulingRead struct {
	Buyer  Null[string] `json:"buyer"`
	Seller Null[string] `json:"seller"`
}

// RulingSources is a ruling's OPTIONAL sources member (evidencebook#861,
// docs/design/861-four-books.md §3.2): per side, the last record id the
// adjudicator read from that side's own book and from the copy of it the
// other side held, null for a source not shown, and the position at which
// the two differ (0 for none).
type RulingSources struct {
	Buyer  BuyerSources  `json:"buyer"`
	Seller SellerSources `json:"seller"`
}

// BuyerSources is the buyer's book's sources in a ruling (RulingSources).
type BuyerSources struct {
	Own          Null[string] `json:"own"`
	HeldBySeller Null[string] `json:"held_by_seller"`
	ForkAt       int64        `json:"fork_at"`
}

// SellerSources is the seller's book's sources in a ruling (RulingSources).
type SellerSources struct {
	Own         Null[string] `json:"own"`
	HeldByBuyer Null[string] `json:"held_by_buyer"`
	ForkAt      int64        `json:"fork_at"`
}

// RulingBody is a ruling record's body (§3.3; design §8). Against is
// REQUIRED and is buyer, seller or null. Forum is the process that ruled
// (#482 Q4).
type RulingBody struct {
	Row         string         `json:"row"`
	Keys        RulingKeys     `json:"keys"`
	Against     Null[string]   `json:"against"`
	Value       string         `json:"value"`
	Remedy      *RulingRemedy  `json:"remedy,omitzero"`
	Read        RulingRead     `json:"read"`
	Sources     *RulingSources `json:"sources,omitzero"`
	Forum       string         `json:"forum"`
	RulesDigest string         `json:"rules_digest"`
	Final       bool           `json:"final"`
}

// AskBody is an ask record's body: an evidence request the library's
// request document holds (§3.3).
type AskBody struct {
	Request Ref    `json:"request"`
	To      string `json:"to"`
}

// AnswerBody is an answer record's body (§3.3).
type AnswerBody struct {
	Request Ref    `json:"request"`
	Outcome string `json:"outcome"`
	Answer  Ref    `json:"answer"`
}

// AdmitBody is an admit record's body: an admission decision (§3.3). A
// decision without a tier (a NO) is not sealed as one.
type AdmitBody struct {
	Outcome string `json:"outcome"`
	// Tier is the id of the pack tier the decision admitted at.
	Tier string `json:"tier"`
	// Rows are the pack's row ids the decision read, as the pack names
	// them (^[a-z][a-z0-9_-]*$, hyphens included).
	Rows []string `json:"rows"`
	// Asks and Answers cite the holder's own ask and answer records of the
	// admission.
	Asks []Ref `json:"asks"`
	// PackDigest is the pack's definition_digest.
	PackDigest string `json:"pack_digest"`
	// CardDigest is the SHA-256 of the JCS bytes of the counterparty's card
	// document the decision read.
	CardDigest string `json:"card_digest"`
	Answers    []Ref  `json:"answers"`
	// Nonce is a fresh random 256-bit value (64 lowercase hex): an
	// agreement cites the admit by digest, and without it the counterparty
	// could test guesses at the outcome against that digest (PROFILE §5).
	Nonce string `json:"nonce"`
}

// VerdictBody is the journal overlay's verdict: v0's body and a nonce. An
// act's person's signature carries its approval's digest (§8), which cites
// the verdict, whose v0 members are guessable from the check, so each
// carries its own secret (PROFILE §5).
type VerdictBody struct {
	dealprofile.VerdictBody
	// ReceivedHistory binds, by digest only, the counterparty's verified
	// History the rules checker read for this verdict (capsule-cli#327
	// design §5.4; absent when it read none). It is v1's own member, beside
	// nonce, since v0's body_verdict is closed and frozen (PROFILE §5).
	ReceivedHistory *ReceivedHistoryDigests `json:"received_history,omitzero"`
	Nonce           string                  `json:"nonce"`
}

// ReceivedHistoryDigests is a verdict's received_history: each History
// document's SHA-256 over its bytes as received, in the order the checker
// was given them, and input_sha256, the SHA-256 of the JCS form of the whole
// received_history member of the rules checker's input (keys, statuses and
// documents), so the verdict is bound to what it read without disclosing it.
//
// AloneDeny is set exactly when the checker denied given the History and did
// not deny without it: History alone never makes the verdict NO (capsule-cli#327 design
// §3, condition 1), so the verdict asks (result pause) over rules that keep the
// checker's deny.
type ReceivedHistoryDigests struct {
	HistorySHA256 []string `json:"history_sha256"`
	InputSHA256   string   `json:"input_sha256"`
	AloneDeny     bool     `json:"alone_deny,omitzero"`
}

// ApprovalBody is the journal overlay's approval: v0's body and a nonce,
// for the reason VerdictBody carries one.
type ApprovalBody struct {
	dealprofile.ApprovalBody
	Nonce string `json:"nonce"`
}

// CheckBody is the journal overlay's check: v0's body and payment_ref, the
// reference the payment will be made under (§3.2).
type CheckBody struct {
	dealprofile.CheckBody
	PaymentRef string `json:"payment_ref,omitzero"`
	// Nonce is a fresh random 256-bit value (64 lowercase hex): an act
	// cites the check by digest, and the check's members are otherwise
	// guessable from the terms (PROFILE §5).
	Nonce string `json:"nonce"`
}
