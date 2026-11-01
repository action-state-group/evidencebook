// SPDX-License-Identifier: Apache-2.0

package dealprofile

import "encoding/json"

// The record types below follow the profile's schemas member for member:
// a required member is a plain field, an optional one is omitted when zero
// (a pointer where zero is a value, a nil slice or map where an empty one
// is), and a member the schemas leave open is json.RawMessage. They carry no
// rule: Encode checks a record against the schemas. TestRecordRoundTrip
// checks that every record the fixtures and baseline vectors hold decodes
// into them and encodes to the same bytes.

// Ref is digest_ref in the schema.
type Ref struct {
	Type      string `json:"type"`
	DigestAlg string `json:"digest_alg"`
	Digest    string `json:"digest"`
}

// Counterparty is counterparty in the schema.
type Counterparty struct {
	FPAlg string            `json:"fp_alg"`
	IDs   map[string]string `json:"ids"`
}

// CounterpartyProfileIDs is the ids member of CounterpartyProfile.
type CounterpartyProfileIDs struct {
	Payee string `json:"payee"`
}

// CounterpartyProfile is counterparty_profile in the schema.
type CounterpartyProfile struct {
	FPAlg string                 `json:"fp_alg"`
	IDs   CounterpartyProfileIDs `json:"ids"`
}

// RelRef is rel_ref in the schema.
type RelRef struct {
	Rel       string `json:"rel"`
	Type      string `json:"type"`
	DigestAlg string `json:"digest_alg"`
	Digest    string `json:"digest"`
}

// Block is block in the schema.
type Block struct {
	Profile             string               `json:"profile"`
	Canonicalization    string               `json:"canonicalization"`
	DealID              string               `json:"deal_id"`
	RecordType          string               `json:"record_type"`
	Seq                 int64                `json:"seq"`
	At                  string               `json:"at"`
	Prev                *Ref                 `json:"prev,omitzero"`
	BaselineRef         *Ref                 `json:"baseline_ref,omitzero"`
	Channel             string               `json:"channel,omitzero"`
	Counterparty        *Counterparty        `json:"counterparty,omitzero"`
	CounterpartyProfile *CounterpartyProfile `json:"counterparty_profile,omitzero"`
	Refs                []RelRef             `json:"refs,omitzero"`
	Producer            *Producer            `json:"producer,omitzero"`
	CommitAlg           string               `json:"commit_alg,omitzero"`
}

// Terms is terms in the schema.
type Terms struct {
	Item         string            `json:"item,omitzero"`
	Quantity     *int64            `json:"quantity,omitzero"`
	PriceMinor   *int64            `json:"price_minor,omitzero"`
	DepositMinor *int64            `json:"deposit_minor,omitzero"`
	Currency     string            `json:"currency,omitzero"`
	When         string            `json:"when,omitzero"`
	Place        string            `json:"place,omitzero"`
	Conditions   map[string]string `json:"conditions,omitzero"`
	Handover     string            `json:"handover,omitzero"`
}

// IntentBody is body_intent in the schema.
type IntentBody struct {
	VerbatimCommitment     string   `json:"verbatim_commitment"`
	PartyRole              string   `json:"party_role,omitzero"`
	Asked                  *Terms   `json:"asked,omitzero"`
	MaxTotalMinor          *int64   `json:"max_total_minor,omitzero"`
	BoundsCommitment       string   `json:"bounds_commitment,omitzero"`
	Allowed                []string `json:"allowed,omitzero"`
	OrderedRepresentations []string `json:"ordered_representations,omitzero"`
	Delegated              []string `json:"delegated,omitzero"`
	DelegatedCommitment    string   `json:"delegated_commitment,omitzero"`
	Move                   string   `json:"move,omitzero"`
	Proposed               *bool    `json:"proposed,omitzero"`
	WhyCommitment          string   `json:"why_commitment,omitzero"`
}

// MaterialityRef is materiality_ref in the schema.
type MaterialityRef struct {
	Digest          string `json:"digest"`
	LabelCommitment string `json:"label_commitment,omitzero"`
	Name            string `json:"name,omitzero"`
	Version         string `json:"version,omitzero"`
}

// TypedRef is record_ref in the schema.
type TypedRef struct {
	Type      string `json:"type"`
	DigestAlg string `json:"digest_alg"`
	Digest    string `json:"digest"`
}

// AuthorityEntry is authority_entry in the schema.
type AuthorityEntry struct {
	Type  string   `json:"type"`
	Ref   TypedRef `json:"ref"`
	Scope string   `json:"scope,omitzero"`
}

// Claim is claim in the schema.
type Claim struct {
	Text                string           `json:"text,omitzero"`
	Source              string           `json:"source,omitzero"`
	TextCommitment      string           `json:"text_commitment,omitzero"`
	SourceKind          string           `json:"source_kind,omitzero"`
	SourceRefCommitment string           `json:"source_ref_commitment,omitzero"`
	Class               string           `json:"class,omitzero"`
	AuthorityBasis      []AuthorityEntry `json:"authority_basis,omitzero"`
	EvaluationRef       *TypedRef        `json:"evaluation_ref,omitzero"`
	Unchecked           *bool            `json:"unchecked,omitzero"`
	Rule                string           `json:"rule,omitzero"`
}

// Recourse is recourse in the schema.
type Recourse struct {
	Rail       string `json:"rail,omitzero"`
	Refundable *bool  `json:"refundable,omitzero"`
}

// CounterpartyFacts is counterparty_facts in the schema.
type CounterpartyFacts struct {
	DomainAgeDays *int64 `json:"domain_age_days,omitzero"`
}

// BaselineBodySkill is the skill member of BaselineBody.
type BaselineBodySkill struct {
	SkillMDDigest string `json:"skill_md_digest"`
	OtherCopies   int64  `json:"other_copies"`
}

// BaselineBody is body_baseline in the schema.
type BaselineBody struct {
	DealType          string             `json:"deal_type"`
	Materiality       *MaterialityRef    `json:"materiality,omitzero"`
	Demo              *bool              `json:"demo,omitzero"`
	Intent            IntentBody         `json:"intent"`
	Terms             Terms              `json:"terms"`
	Claims            []Claim            `json:"claims,omitzero"`
	Recourse          *Recourse          `json:"recourse,omitzero"`
	CounterpartyFacts *CounterpartyFacts `json:"counterparty_facts,omitzero"`
	Skill             *BaselineBodySkill `json:"skill,omitzero"`
	ExpectCloseBy     string             `json:"expect_close_by,omitzero"`
}

// MessageBody is body_message in the schema.
type MessageBody struct {
	From              string `json:"from"`
	ContentCommitment string `json:"content_commitment"`
	SentAt            string `json:"sent_at,omitzero"`
}

// MerchantEmailParsed is the parsed member of MerchantEmail.
type MerchantEmailParsed struct {
	Method            string `json:"method"`
	TotalMinor        *int64 `json:"total_minor,omitzero"`
	Currency          string `json:"currency,omitzero"`
	OrderIDCommitment string `json:"order_id_commitment,omitzero"`
	CancelBy          string `json:"cancel_by,omitzero"`
	SentAt            string `json:"sent_at,omitzero"`
	ItemCount         *int64 `json:"item_count,omitzero"`
	Kind              string `json:"kind,omitzero"`
}

// MerchantEmail is merchant_email in the schema.
type MerchantEmail struct {
	MessageDigest         string              `json:"message_digest"`
	KeyRecordsDigest      string              `json:"key_records_digest"`
	KeySource             string              `json:"key_source"`
	KeyResolver           string              `json:"key_resolver,omitzero"`
	KeyDNSSEC             string              `json:"key_dnssec,omitzero"`
	DKIM                  string              `json:"dkim"`
	MerchantSigned        bool                `json:"merchant_signed"`
	DMARCPolicy           string              `json:"dmarc_policy"`
	DMARCRecordDigest     string              `json:"dmarc_record_digest,omitzero"`
	DMARCSource           string              `json:"dmarc_source,omitzero"`
	SignerMatchesBaseline *bool               `json:"signer_matches_baseline,omitzero"`
	Parsed                MerchantEmailParsed `json:"parsed"`
}

// Obligation is obligation in the schema.
type Obligation struct {
	Kind            string `json:"kind"`
	CancelBy        string `json:"cancel_by,omitzero"`
	DueBy           string `json:"due_by,omitzero"`
	TakesEffect     string `json:"takes_effect,omitzero"`
	AmountMinor     *int64 `json:"amount_minor,omitzero"`
	Currency        string `json:"currency,omitzero"`
	Period          string `json:"period,omitzero"`
	TermsCommitment string `json:"terms_commitment,omitzero"`
}

// EvidenceBodyWrappedItem is an item of the wrapped member of EvidenceBody.
type EvidenceBodyWrappedItem struct {
	Type      string `json:"type"`
	DigestAlg string `json:"digest_alg"`
	Digest    string `json:"digest"`
}

// EvidenceBodyReceivedTo is the received_to member of EvidenceBody.
type EvidenceBodyReceivedTo struct {
	FPAlg string `json:"fp_alg"`
	Payee string `json:"payee"`
}

// EvidenceBody is body_evidence in the schema.
type EvidenceBody struct {
	Source                   string                    `json:"source"`
	Verified                 bool                      `json:"verified"`
	DetailCommitment         string                    `json:"detail_commitment,omitzero"`
	CounterpartyFacts        *CounterpartyFacts        `json:"counterparty_facts,omitzero"`
	MerchantEmail            *MerchantEmail            `json:"merchant_email,omitzero"`
	Obligation               *Obligation               `json:"obligation,omitzero"`
	ResolvesObligation       *Ref                      `json:"resolves_obligation,omitzero"`
	Wrapped                  []EvidenceBodyWrappedItem `json:"wrapped,omitzero"`
	WrappedOctets            string                    `json:"wrapped_octets,omitzero"`
	WrappedHeld              string                    `json:"wrapped_held,omitzero"`
	CarrierHeadersCommitment string                    `json:"carrier_headers_commitment,omitzero"`
	ReceivedTo               *EvidenceBodyReceivedTo   `json:"received_to,omitzero"`
	Preimage                 string                    `json:"preimage,omitzero"`
}

// DetailChangeBody is body_detail_change in the schema.
type DetailChangeBody struct {
	Source   string    `json:"source"`
	Changed  []string  `json:"changed"`
	Terms    *Terms    `json:"terms,omitzero"`
	Recourse *Recourse `json:"recourse,omitzero"`
}

// CheckBodyPosting is the posting member of CheckBody.
type CheckBodyPosting struct {
	TextCommitment string   `json:"text_commitment"`
	Classes        []string `json:"classes"`
	Platform       string   `json:"platform"`
}

// CheckBodyRepresenting is the representing member of CheckBody.
type CheckBodyRepresenting struct {
	Class               string `json:"class"`
	TextCommitment      string `json:"text_commitment"`
	SourceRefCommitment string `json:"source_ref_commitment,omitzero"`
	AttributedTo        string `json:"attributed_to,omitzero"`
}

// CheckBody is body_check in the schema.
type CheckBody struct {
	Action                string                 `json:"action"`
	Posting               *CheckBodyPosting      `json:"posting,omitzero"`
	ListingClasses        []string               `json:"listing_classes,omitzero"`
	Representing          *CheckBodyRepresenting `json:"representing,omitzero"`
	ItemRefCommitment     string                 `json:"item_ref_commitment,omitzero"`
	ActionClass           string                 `json:"action_class,omitzero"`
	TaxonomyVersion       string                 `json:"taxonomy_version,omitzero"`
	SpendMinor            *int64                 `json:"spend_minor,omitzero"`
	AuthorizedMaxMinor    *int64                 `json:"authorized_max_minor,omitzero"`
	SpendAuthorizedMinor  *int64                 `json:"spend_authorized_minor,omitzero"`
	Direction             string                 `json:"direction,omitzero"`
	ReversesRef           *TypedRef              `json:"reverses_ref,omitzero"`
	FeeMinor              *int64                 `json:"fee_minor,omitzero"`
	CancelledAmountMinor  *int64                 `json:"cancelled_amount_minor,omitzero"`
	AmountMinor           *int64                 `json:"amount_minor,omitzero"`
	Currency              string                 `json:"currency,omitzero"`
	SeenItem              *bool                  `json:"seen_item,omitzero"`
	Disclosing            []string               `json:"disclosing,omitzero"`
	DisclosingTo          string                 `json:"disclosing_to,omitzero"`
	Terms                 *Terms                 `json:"terms,omitzero"`
	RecipientRole         string                 `json:"recipient_role,omitzero"`
	RepeatDisclosures     []string               `json:"repeat_disclosures,omitzero"`
	RecipientToldBefore   *bool                  `json:"recipient_told_before,omitzero"`
	Channel               string                 `json:"channel,omitzero"`
	FirstContactChannel   string                 `json:"first_contact_channel,omitzero"`
	MaterialFieldsChanged *int64                 `json:"material_fields_changed,omitzero"`
	MaterialFieldsBasis   string                 `json:"material_fields_basis,omitzero"`
	RuleInputsVersion     *int64                 `json:"rule_inputs_version,omitzero"`
	OfferFieldsChanged    *int64                 `json:"offer_fields_changed,omitzero"`
	OfferFieldsBasis      string                 `json:"offer_fields_basis,omitzero"`
	UpfrontAmountMinor    *int64                 `json:"upfront_amount_minor,omitzero"`
	Recourse              *Recourse              `json:"recourse,omitzero"`
	DescriptionCommitment string                 `json:"description_commitment,omitzero"`
	NoProposal            *bool                  `json:"no_proposal,omitzero"`
	PackID                string                 `json:"pack_id,omitzero"`
	PackDigest            string                 `json:"pack_digest,omitzero"`
}

// VerdictBodyRulesHistory is the history member of VerdictBodyRules.
type VerdictBodyRulesHistory struct {
	Days     int64 `json:"days"`
	Acts     int64 `json:"acts"`
	Complete bool  `json:"complete"`
}

// VerdictBodyRulesFindingsItem is an item of the findings member of VerdictBodyRules.
type VerdictBodyRulesFindingsItem struct {
	ID          string          `json:"id"`
	Check       *string         `json:"check,omitzero"`
	Verdict     string          `json:"verdict"`
	Disposition string          `json:"disposition,omitzero"`
	Limit       json.RawMessage `json:"limit,omitzero"`
	Value       json.RawMessage `json:"value,omitzero"`
}

// VerdictBodyRules is the rules member of VerdictBody.
type VerdictBodyRules struct {
	Status           string                         `json:"status"`
	Cause            string                         `json:"cause,omitzero"`
	RulesetID        string                         `json:"ruleset_id,omitzero"`
	DefinitionDigest string                         `json:"definition_digest,omitzero"`
	CheckerSHA256    string                         `json:"checker_sha256,omitzero"`
	Verdict          string                         `json:"verdict,omitzero"`
	Tier             string                         `json:"tier,omitzero"`
	Grade            string                         `json:"grade,omitzero"`
	History          *VerdictBodyRulesHistory       `json:"history,omitzero"`
	Findings         []VerdictBodyRulesFindingsItem `json:"findings,omitzero"`
}

// Difference is difference in the schema.
type Difference struct {
	Question string `json:"question"`
	Rule     string `json:"rule"`
	Field    string `json:"field,omitzero"`
}

// VerdictBodyUnverifiedClaimsItem is an item of the unverified_claims member of VerdictBody.
type VerdictBodyUnverifiedClaimsItem struct {
	Claim Ref    `json:"claim"`
	Index *int64 `json:"index,omitzero"`
}

// VerdictBodyJudge is the judge member of VerdictBody.
type VerdictBodyJudge struct {
	Kind           string `json:"kind"`
	Status         string `json:"status,omitzero"`
	ResponseDigest string `json:"response_digest,omitzero"`
}

// VerdictBody is body_verdict in the schema.
type VerdictBody struct {
	Result           string                            `json:"result"`
	Materiality      *MaterialityRef                   `json:"materiality,omitzero"`
	Rules            *VerdictBodyRules                 `json:"rules,omitzero"`
	PackID           string                            `json:"pack_id,omitzero"`
	Differences      []Difference                      `json:"differences"`
	Unverified       []string                          `json:"unverified,omitzero"`
	UnverifiedClaims []VerdictBodyUnverifiedClaimsItem `json:"unverified_claims,omitzero"`
	Notes            []string                          `json:"notes,omitzero"`
	Options          []string                          `json:"options"`
	CardCommitment   string                            `json:"card_commitment,omitzero"`
	Judge            *VerdictBodyJudge                 `json:"judge,omitzero"`
}

// LimitSet is limit_set in the schema.
type LimitSet struct {
	MaxTotalMinor          *int64   `json:"max_total_minor,omitzero"`
	BoundsCommitment       string   `json:"bounds_commitment,omitzero"`
	Allowed                []string `json:"allowed,omitzero"`
	OrderedRepresentations []string `json:"ordered_representations,omitzero"`
	Delegated              []string `json:"delegated,omitzero"`
}

// ApprovalBodyLimits is the limits member of ApprovalBody.
type ApprovalBodyLimits struct {
	Previous LimitSet `json:"previous"`
	New      LimitSet `json:"new"`
}

// ApprovalBody is body_approval in the schema.
type ApprovalBody struct {
	Choice         string              `json:"choice"`
	Proceed        bool                `json:"proceed"`
	Approver       string              `json:"approver"`
	SaidCommitment string              `json:"said_commitment,omitzero"`
	CardCommitment string              `json:"card_commitment,omitzero"`
	Limits         *ApprovalBodyLimits `json:"limits,omitzero"`
}

// ActionBody is body_action in the schema.
type ActionBody struct {
	Action                string `json:"action"`
	ActionClass           string `json:"action_class,omitzero"`
	TaxonomyVersion       string `json:"taxonomy_version,omitzero"`
	SpendMinor            *int64 `json:"spend_minor,omitzero"`
	FeeMinor              *int64 `json:"fee_minor,omitzero"`
	CancelledAmountMinor  *int64 `json:"cancelled_amount_minor,omitzero"`
	AmountMinor           *int64 `json:"amount_minor,omitzero"`
	Currency              string `json:"currency,omitzero"`
	Rail                  string `json:"rail,omitzero"`
	Direction             string `json:"direction,omitzero"`
	ReferenceCommitment   string `json:"reference_commitment,omitzero"`
	DescriptionCommitment string `json:"description_commitment,omitzero"`
}

// OutcomeBody is body_outcome in the schema.
type OutcomeBody struct {
	Status         string       `json:"status"`
	Outcome        string       `json:"outcome"`
	Delivered      *Terms       `json:"delivered,omitzero"`
	Differences    []Difference `json:"differences"`
	Unchecked      *ActionBody  `json:"unchecked,omitzero"`
	NoteCommitment string       `json:"note_commitment,omitzero"`
	Actor          string       `json:"actor,omitzero"`
	Reason         string       `json:"reason,omitzero"`
}

// CloseBodyCarriedObligationsItem is an item of the carried_obligations member of CloseBody.
type CloseBodyCarriedObligationsItem struct {
	Obligation Ref    `json:"obligation"`
	CancelBy   string `json:"cancel_by,omitzero"`
	DueBy      string `json:"due_by,omitzero"`
}

// CloseBody is body_close in the schema.
type CloseBody struct {
	Outcome            string                            `json:"outcome"`
	UncheckedActions   int64                             `json:"unchecked_actions"`
	Actor              string                            `json:"actor,omitzero"`
	Reason             string                            `json:"reason,omitzero"`
	Differences        []Difference                      `json:"differences,omitzero"`
	CarriedObligations []CloseBodyCarriedObligationsItem `json:"carried_obligations,omitzero"`
}

// DisclosureBodyFieldsItem is an item of the fields member of DisclosureBody.
type DisclosureBodyFieldsItem struct {
	Class           string `json:"class"`
	ValueCommitment string `json:"value_commitment"`
}

// DisclosureBody is body_disclosure in the schema.
type DisclosureBody struct {
	To              string                     `json:"to"`
	ActionClass     string                     `json:"action_class,omitzero"`
	TaxonomyVersion string                     `json:"taxonomy_version,omitzero"`
	Fields          []DisclosureBodyFieldsItem `json:"fields"`
	Authority       string                     `json:"authority"`
	Rule            string                     `json:"rule,omitzero"`
}

// SaleBody is body_sale in the schema.
type SaleBody struct {
	DealType          string     `json:"deal_type"`
	Demo              *bool      `json:"demo,omitzero"`
	Intent            IntentBody `json:"intent"`
	Terms             Terms      `json:"terms"`
	Recourse          Recourse   `json:"recourse"`
	ItemRefCommitment string     `json:"item_ref_commitment"`
}

// CounterpartyProfileBody is body_counterparty_profile in the schema.
type CounterpartyProfileBody struct {
}

// ThreadBody is body_thread in the schema.
type ThreadBody struct {
	ThreadRefCommitment string `json:"thread_ref_commitment"`
}

// ThreadOpenedBody is body_thread_opened in the schema.
type ThreadOpenedBody struct {
	TaskAuthorityCommitment string `json:"task_authority_commitment"`
}

// SaleCutBodyThreadHeadsItem is an item of the thread_heads member of SaleCutBody.
type SaleCutBodyThreadHeadsItem struct {
	Registration   Ref    `json:"registration"`
	HeadCommitment string `json:"head_commitment"`
}

// SaleCutBody is body_sale_cut in the schema.
type SaleCutBody struct {
	ThreadHeads []SaleCutBodyThreadHeadsItem `json:"thread_heads"`
}

// SaleCloseBody is body_sale_close in the schema.
type SaleCloseBody struct {
}

// AgentMoveBodyOptionsItem is an item of the options member of AgentMoveBody.
type AgentMoveBodyOptionsItem struct {
	ItemCommitment string `json:"item_commitment"`
	PriceMinor     *int64 `json:"price_minor,omitzero"`
}

// AgentMoveBody is body_agent_move in the schema.
type AgentMoveBody struct {
	Move             string                     `json:"move"`
	WhereCommitment  string                     `json:"where_commitment,omitzero"`
	Results          *int64                     `json:"results,omitzero"`
	Options          []AgentMoveBodyOptionsItem `json:"options,omitzero"`
	Found            *int64                     `json:"found,omitzero"`
	Option           *int64                     `json:"option,omitzero"`
	ReasonCommitment string                     `json:"reason_commitment,omitzero"`
	ItemCommitment   string                     `json:"item_commitment,omitzero"`
	PriceMinor       *int64                     `json:"price_minor,omitzero"`
	Reason           string                     `json:"reason,omitzero"`
	DetailCommitment string                     `json:"detail_commitment,omitzero"`
}

// TypedRelRef is rel_ref in record-common-v0.
type TypedRelRef struct {
	Rel       string `json:"rel"`
	Type      string `json:"type"`
	DigestAlg string `json:"digest_alg"`
	Digest    string `json:"digest"`
}

// Producer is producer in record-common-v0.
type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// ActionApprovalBody is the body member of ActionApproval.
type ActionApprovalBody struct {
	Authority              string    `json:"authority"`
	Choice                 string    `json:"choice,omitzero"`
	Proceed                *bool     `json:"proceed,omitzero"`
	SaidCommitment         string    `json:"said_commitment,omitzero"`
	RenderingCommitment    string    `json:"rendering_commitment,omitzero"`
	Kind                   string    `json:"kind,omitzero"`
	Platform               string    `json:"platform,omitzero"`
	Mechanism              string    `json:"mechanism,omitzero"`
	DisplayedTextDigest    string    `json:"displayed_text_digest,omitzero"`
	ReturnedUserTextDigest string    `json:"returned_user_text_digest,omitzero"`
	ProposedActionRef      *TypedRef `json:"proposed_action_ref,omitzero"`
	ObservedAt             string    `json:"observed_at,omitzero"`
	AmountMinor            *int64    `json:"amount_minor,omitzero"`
	Currency               string    `json:"currency,omitzero"`
	EffectivePolicyDigest  string    `json:"effective_policy_digest,omitzero"`
	SemanticDiffDigest     string    `json:"semantic_diff_digest,omitzero"`
	ContentCommitment      string    `json:"content_commitment,omitzero"`
	Channel                string    `json:"channel,omitzero"`
}

// ActionApproval is an action-approval/v0 record.
type ActionApproval struct {
	Type             string             `json:"type"`
	Canonicalization string             `json:"canonicalization"`
	ChainID          string             `json:"chain_id"`
	Seq              int64              `json:"seq"`
	At               string             `json:"at"`
	Prev             TypedRef           `json:"prev"`
	ChainRoot        TypedRef           `json:"chain_root"`
	Refs             []TypedRelRef      `json:"refs,omitzero"`
	Producer         *Producer          `json:"producer,omitzero"`
	CommitAlg        string             `json:"commit_alg,omitzero"`
	Body             ActionApprovalBody `json:"body"`
}

// TypedFinding is finding in record-common-v0.
type TypedFinding struct {
	Question string `json:"question"`
	Rule     string `json:"rule"`
	Field    string `json:"field,omitzero"`
}

// ActionEvaluationBodyUnverifiedClaimsItem is an item of the unverified_claims member of ActionEvaluationBody.
type ActionEvaluationBodyUnverifiedClaimsItem struct {
	Claim TypedRef `json:"claim"`
	Index *int64   `json:"index,omitzero"`
}

// ActionEvaluationBodyRulesChecksItem is an item of the rules_checks member of ActionEvaluationBody.
type ActionEvaluationBodyRulesChecksItem struct {
	RulesetID        string `json:"ruleset_id"`
	DefinitionDigest string `json:"definition_digest"`
	Verdict          string `json:"verdict"`
}

// ActionEvaluationBodyAuthorityBasisItem is an item of the authority_basis member of ActionEvaluationBody.
type ActionEvaluationBodyAuthorityBasisItem struct {
	Type string   `json:"type"`
	Ref  TypedRef `json:"ref"`
}

// ActionEvaluationBody is the body member of ActionEvaluation.
type ActionEvaluationBody struct {
	Disposition          string                                     `json:"disposition"`
	Findings             []TypedFinding                             `json:"findings"`
	Options              []string                                   `json:"options"`
	Unverified           []string                                   `json:"unverified,omitzero"`
	UnverifiedClaims     []ActionEvaluationBodyUnverifiedClaimsItem `json:"unverified_claims,omitzero"`
	Notes                []string                                   `json:"notes,omitzero"`
	RenderingCommitment  string                                     `json:"rendering_commitment,omitzero"`
	Judge                json.RawMessage                            `json:"judge,omitzero"`
	ProposedActionDigest string                                     `json:"proposed_action_digest"`
	TaskAuthorityRef     TypedRef                                   `json:"task_authority_ref"`
	RulesetDigest        string                                     `json:"ruleset_digest"`
	RulesStatus          string                                     `json:"rules_status,omitzero"`
	RulesChecks          []ActionEvaluationBodyRulesChecksItem      `json:"rules_checks,omitzero"`
	Materiality          string                                     `json:"materiality"`
	MaterialityDigest    *string                                    `json:"materiality_digest"`
	ValidUntil           string                                     `json:"valid_until"`
	AuthorityBasis       []ActionEvaluationBodyAuthorityBasisItem   `json:"authority_basis"`
}

// ActionEvaluation is an action-evaluation/v0 record.
type ActionEvaluation struct {
	Type             string               `json:"type"`
	Canonicalization string               `json:"canonicalization"`
	ChainID          string               `json:"chain_id"`
	Seq              int64                `json:"seq"`
	At               string               `json:"at"`
	Prev             TypedRef             `json:"prev"`
	ChainRoot        TypedRef             `json:"chain_root"`
	Refs             []TypedRelRef        `json:"refs"`
	Producer         *Producer            `json:"producer,omitzero"`
	CommitAlg        string               `json:"commit_alg,omitzero"`
	Body             ActionEvaluationBody `json:"body"`
}

// ActionOutcomeBody is the body member of ActionOutcome.
type ActionOutcomeBody struct {
	Status         string          `json:"status"`
	Outcome        string          `json:"outcome"`
	Findings       []TypedFinding  `json:"findings"`
	Attempted      json.RawMessage `json:"attempted,omitzero"`
	Delivered      json.RawMessage `json:"delivered,omitzero"`
	NoteCommitment string          `json:"note_commitment,omitzero"`
	Actor          string          `json:"actor,omitzero"`
	Reason         string          `json:"reason,omitzero"`
}

// ActionOutcome is an action-outcome/v0 record.
type ActionOutcome struct {
	Type             string            `json:"type"`
	Canonicalization string            `json:"canonicalization"`
	ChainID          string            `json:"chain_id"`
	Seq              int64             `json:"seq"`
	At               string            `json:"at"`
	Prev             TypedRef          `json:"prev"`
	ChainRoot        TypedRef          `json:"chain_root"`
	Refs             []TypedRelRef     `json:"refs,omitzero"`
	Producer         *Producer         `json:"producer,omitzero"`
	CommitAlg        string            `json:"commit_alg,omitzero"`
	Body             ActionOutcomeBody `json:"body"`
}

// TypedDisclosedFieldsItem is an item of the fields member of TypedDisclosed.
type TypedDisclosedFieldsItem struct {
	Class           string `json:"class"`
	ValueCommitment string `json:"value_commitment"`
}

// TypedDisclosed is disclosed in record-common-v0.
type TypedDisclosed struct {
	To     string                     `json:"to"`
	Fields []TypedDisclosedFieldsItem `json:"fields"`
}

// TypedCounterparty is counterparty in record-common-v0.
type TypedCounterparty struct {
	FPAlg string            `json:"fp_alg"`
	IDs   map[string]string `json:"ids"`
}

// ActionRecordBody is the body member of ActionRecord.
type ActionRecordBody struct {
	Action                string             `json:"action"`
	ActionClass           string             `json:"action_class,omitzero"`
	TaxonomyVersion       string             `json:"taxonomy_version,omitzero"`
	SpendMinor            *int64             `json:"spend_minor,omitzero"`
	FeeMinor              *int64             `json:"fee_minor,omitzero"`
	CancelledAmountMinor  *int64             `json:"cancelled_amount_minor,omitzero"`
	AmountMinor           *int64             `json:"amount_minor,omitzero"`
	Currency              string             `json:"currency,omitzero"`
	Rail                  string             `json:"rail,omitzero"`
	Direction             string             `json:"direction,omitzero"`
	ReferenceCommitment   string             `json:"reference_commitment,omitzero"`
	DescriptionCommitment string             `json:"description_commitment,omitzero"`
	Disclosed             *TypedDisclosed    `json:"disclosed,omitzero"`
	Channel               *string            `json:"channel,omitzero"`
	Counterparty          *TypedCounterparty `json:"counterparty,omitzero"`
	EvaluationRef         TypedRef           `json:"evaluation_ref"`
	AuthorityBasis        []AuthorityEntry   `json:"authority_basis"`
}

// ActionRecord is an action-record/v0 record.
type ActionRecord struct {
	Type             string           `json:"type"`
	Canonicalization string           `json:"canonicalization"`
	ChainID          string           `json:"chain_id"`
	Seq              int64            `json:"seq"`
	At               string           `json:"at"`
	Prev             TypedRef         `json:"prev"`
	ChainRoot        TypedRef         `json:"chain_root"`
	Refs             []TypedRelRef    `json:"refs"`
	Producer         *Producer        `json:"producer,omitzero"`
	CommitAlg        string           `json:"commit_alg,omitzero"`
	Body             ActionRecordBody `json:"body"`
}

// ActionReportBodyAuthorityItem is an item of the authority member of ActionReportBody.
type ActionReportBodyAuthorityItem struct {
	Layer string   `json:"layer"`
	Ref   TypedRef `json:"ref"`
	At    string   `json:"at"`
}

// ActionReportBody is the body member of ActionReport.
type ActionReportBody struct {
	Presents  []TypedRef                      `json:"presents"`
	Authority []ActionReportBodyAuthorityItem `json:"authority,omitzero"`
}

// ActionReport is an action-report/v0 record.
type ActionReport struct {
	Type             string           `json:"type"`
	Canonicalization string           `json:"canonicalization"`
	ChainID          string           `json:"chain_id"`
	Seq              int64            `json:"seq"`
	At               string           `json:"at"`
	Prev             TypedRef         `json:"prev"`
	ChainRoot        TypedRef         `json:"chain_root"`
	Refs             []TypedRelRef    `json:"refs,omitzero"`
	Producer         *Producer        `json:"producer,omitzero"`
	CommitAlg        string           `json:"commit_alg,omitzero"`
	Body             ActionReportBody `json:"body"`
}

// CheckRequestPlatform is the platform member of CheckRequest.
type CheckRequestPlatform struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"display_name,omitzero"`
}

// CheckRequest is a check-request/v0 contract document.
type CheckRequest struct {
	Type                 string                `json:"type"`
	Phase                string                `json:"phase"`
	Platform             *CheckRequestPlatform `json:"platform,omitzero"`
	RulesetRef           *string               `json:"ruleset_ref,omitzero"`
	RulesetDigest        string                `json:"ruleset_digest"`
	TaskAuthorityRef     *TypedRef             `json:"task_authority_ref"`
	RootIntentRef        *TypedRef             `json:"root_intent_ref,omitzero"`
	ParentDelegationRef  json.RawMessage       `json:"parent_delegation_ref,omitzero"`
	ProposedAction       json.RawMessage       `json:"proposed_action"`
	HistoryRefs          []TypedRef            `json:"history_refs,omitzero"`
	AgentAssessment      json.RawMessage       `json:"agent_assessment,omitzero"`
	PlatformApprovalRefs []TypedRef            `json:"platform_approval_refs,omitzero"`
}

// CheckResponse is a check-response/v0 contract document.
type CheckResponse struct {
	Type                 string           `json:"type"`
	Disposition          string           `json:"disposition"`
	ValidUntil           string           `json:"valid_until"`
	ProposedActionDigest string           `json:"proposed_action_digest"`
	Findings             []TypedFinding   `json:"findings"`
	AuthorityBasis       []AuthorityEntry `json:"authority_basis"`
	EvaluationRef        TypedRef         `json:"evaluation_ref"`
	RulesetDigest        string           `json:"ruleset_digest"`
	TaskAuthorityRef     TypedRef         `json:"task_authority_ref"`
	Materiality          string           `json:"materiality"`
	MaterialityDigest    *string          `json:"materiality_digest"`
}

// CommercialBounds is a commercial-bounds/v0 contract document.
type CommercialBounds struct {
	Type          string `json:"type"`
	MinTotalMinor int64  `json:"min_total_minor"`
}

// ProposedActionBodyRepresenting is the representing member of ProposedActionBody.
type ProposedActionBodyRepresenting struct {
	Class               string `json:"class"`
	TextCommitment      string `json:"text_commitment"`
	SourceRefCommitment string `json:"source_ref_commitment,omitzero"`
	AttributedTo        string `json:"attributed_to,omitzero"`
}

// ProposedActionBodyPosting is the posting member of ProposedActionBody.
type ProposedActionBodyPosting struct {
	TextCommitment string   `json:"text_commitment"`
	Classes        []string `json:"classes"`
	Platform       string   `json:"platform"`
}

// ProposedActionBody is the body member of ProposedAction.
type ProposedActionBody struct {
	Action                string                          `json:"action"`
	ActionClass           string                          `json:"action_class,omitzero"`
	TaxonomyVersion       string                          `json:"taxonomy_version,omitzero"`
	SpendMinor            *int64                          `json:"spend_minor,omitzero"`
	AuthorizedMaxMinor    *int64                          `json:"authorized_max_minor,omitzero"`
	SpendAuthorizedMinor  *int64                          `json:"spend_authorized_minor,omitzero"`
	FeeMinor              *int64                          `json:"fee_minor,omitzero"`
	CancelledAmountMinor  *int64                          `json:"cancelled_amount_minor,omitzero"`
	Direction             string                          `json:"direction,omitzero"`
	AmountMinor           *int64                          `json:"amount_minor,omitzero"`
	Currency              string                          `json:"currency,omitzero"`
	SeenItem              *bool                           `json:"seen_item,omitzero"`
	Disclosing            []string                        `json:"disclosing,omitzero"`
	DisclosingTo          string                          `json:"disclosing_to,omitzero"`
	Terms                 json.RawMessage                 `json:"terms,omitzero"`
	Recourse              json.RawMessage                 `json:"recourse,omitzero"`
	UpfrontAmountMinor    *int64                          `json:"upfront_amount_minor,omitzero"`
	RecipientRole         string                          `json:"recipient_role,omitzero"`
	RepeatDisclosures     []string                        `json:"repeat_disclosures,omitzero"`
	RecipientToldBefore   *bool                           `json:"recipient_told_before,omitzero"`
	Channel               string                          `json:"channel,omitzero"`
	FirstContactChannel   string                          `json:"first_contact_channel,omitzero"`
	MaterialFieldsChanged *int64                          `json:"material_fields_changed,omitzero"`
	MaterialFieldsBasis   string                          `json:"material_fields_basis,omitzero"`
	RuleInputsVersion     *int64                          `json:"rule_inputs_version,omitzero"`
	OfferFieldsChanged    *int64                          `json:"offer_fields_changed,omitzero"`
	OfferFieldsBasis      string                          `json:"offer_fields_basis,omitzero"`
	ReversesRef           *TypedRef                       `json:"reverses_ref,omitzero"`
	TaskAuthorityRef      *TypedRef                       `json:"task_authority_ref,omitzero"`
	DescriptionCommitment string                          `json:"description_commitment,omitzero"`
	Counterparty          *TypedCounterparty              `json:"counterparty,omitzero"`
	ItemRefCommitment     string                          `json:"item_ref_commitment,omitzero"`
	Representing          *ProposedActionBodyRepresenting `json:"representing,omitzero"`
	NoProposal            *bool                           `json:"no_proposal,omitzero"`
	Posting               *ProposedActionBodyPosting      `json:"posting,omitzero"`
	ListingClasses        []string                        `json:"listing_classes,omitzero"`
}

// ProposedAction is a proposed-action/v0 record.
type ProposedAction struct {
	Type             string             `json:"type"`
	Canonicalization string             `json:"canonicalization"`
	ChainID          string             `json:"chain_id"`
	Seq              int64              `json:"seq"`
	At               string             `json:"at"`
	Prev             TypedRef           `json:"prev"`
	ChainRoot        TypedRef           `json:"chain_root"`
	Refs             []TypedRelRef      `json:"refs,omitzero"`
	Producer         *Producer          `json:"producer,omitzero"`
	CommitAlg        string             `json:"commit_alg,omitzero"`
	Body             ProposedActionBody `json:"body"`
}

// TaskAuthorityBodyPreconditionsItem is an item of the preconditions member of TaskAuthorityBody.
type TaskAuthorityBodyPreconditionsItem struct {
	Action string `json:"action"`
	Citing string `json:"citing"`
}

// TaskAuthorityBody is the body member of TaskAuthority.
type TaskAuthorityBody struct {
	VerbatimCommitment      string                               `json:"verbatim_commitment"`
	MaxTotalMinor           *int64                               `json:"max_total_minor,omitzero"`
	BoundsCommitment        string                               `json:"bounds_commitment,omitzero"`
	Allowed                 []string                             `json:"allowed,omitzero"`
	OrderedRepresentations  []string                             `json:"ordered_representations,omitzero"`
	OutcomeID               string                               `json:"outcome_id,omitzero"`
	AllowedActions          []string                             `json:"allowed_actions,omitzero"`
	Preconditions           []TaskAuthorityBodyPreconditionsItem `json:"preconditions,omitzero"`
	Asked                   json.RawMessage                      `json:"asked,omitzero"`
	PreviousRef             *TypedRef                            `json:"previous_ref,omitzero"`
	SaidCommitment          string                               `json:"said_commitment,omitzero"`
	SaleAuthorityCommitment string                               `json:"sale_authority_commitment,omitzero"`
}

// TaskAuthority is a task-authority/v0 record.
type TaskAuthority struct {
	Type             string            `json:"type"`
	Canonicalization string            `json:"canonicalization"`
	ChainID          string            `json:"chain_id"`
	Seq              int64             `json:"seq"`
	At               string            `json:"at"`
	Prev             TypedRef          `json:"prev"`
	ChainRoot        TypedRef          `json:"chain_root"`
	Refs             []TypedRelRef     `json:"refs,omitzero"`
	Producer         *Producer         `json:"producer,omitzero"`
	CommitAlg        string            `json:"commit_alg,omitzero"`
	Body             TaskAuthorityBody `json:"body"`
}
