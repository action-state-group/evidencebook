// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// The disclosure policy document extends loop's disclosure-policy/0 (the
// signed file whose digest sits in a holder's commitment). The `policy`
// member of that file is a DisclosurePolicy. Every change to it is a sealed
// policy record (RecordTypePolicy); a record already sent is never edited,
// and a reply already given stays what it was.
const (
	// PolicyProfile is the document's profile token, loop's.
	PolicyProfile = "disclosure-policy/0"
	// PolicyChangeProfile is the executor's input: a base policy, the
	// fields to change and the steps to run afterwards.
	PolicyChangeProfile = "policy-change/v1"
	// DefaultPolicyName is the shipped policy's name.
	DefaultPolicyName = "Action State default v1"
	// RecordTypePolicy is the record type of a policy record: the policy
	// adopted, or one field's change. It is the holder's own rules and is
	// never served to anyone.
	RecordTypePolicy = "disclosure_policy"
)

// Holder kinds a policy is shipped for.
const (
	HolderPerson = "person"
	HolderNode   = "node"
)

// Settings fields a policy-change/v1 may name, in page order.
const (
	FieldStandardHistory = "standard_history"
	FieldWindow          = "window"
	FieldStaple          = "staple"
	FieldMoreThanStd     = "more_than_standard"
	FieldReceipt         = "receipt"
	FieldUnsignedAsks    = "unsigned_asks_per_week"
	FieldUnderContract   = "under_contract"
	FieldAskedLogDays    = "asked_log_retention_days"
)

// SettingsFields lists every changeable field, in page order.
var SettingsFields = []string{FieldStandardHistory, FieldWindow, FieldStaple, FieldMoreThanStd, FieldReceipt, FieldUnsignedAsks, FieldUnderContract, FieldAskedLogDays}

// NeverList is the fixed text of what no setting and no asker can reach
// (Steven, 9b brief, primitive 3). It is in every policy document and is
// not a setting: a document that carries any other list is refused, and a
// change that names one of these is refused.
var NeverList = []string{
	"name", "contact", "items", "amounts_beyond_bands", "counterparties_except_to_themselves",
	"messages", "rules", "asked_of_you_log", "shop_purchases", "pre_book_history",
	"forum_exception_holders_own_copy_only",
}

// neverFields are the names a change may not carry; they are the
// never-list's items as a person or an agent would spell them.
var neverFields = []string{"never", "name", "contact", "items", "amounts", "counterparties", "messages", "rules", "asked_of_you_log", "shop_purchases", "pre_book_history", "proof_of_records", "proof"}

// Steps a policy-change/v1 may list in then[].
var policyThenSteps = []string{"rebuild_card", "rebuild_receipts", "probe_as_stranger", "probe_as_signed", "make_report"}

var (
	// ErrStaleRound reports a change composed against a policy round that
	// is no longer in force, as stale_check reports a stale approval.
	ErrStaleRound = errors.New("evidencebook: stale settings round")
	// ErrNeverList reports a change that names a never-list item.
	ErrNeverList = errors.New("evidencebook: never-list item")
	// ErrNoPolicy reports a book that has adopted no disclosure policy.
	ErrNoPolicy = errors.New("evidencebook: no disclosure policy adopted")
)

// DisclosurePolicy is the `policy` member of a disclosure-policy/0 file.
type DisclosurePolicy struct {
	Name   string `json:"name"`
	Holder string `json:"holder"`
	// Round counts the settings rounds applied; a change names the round
	// it was composed against.
	Round uint64 `json:"round"`
	// StandardHistory is "on" or "off": whether standard_history/1 is
	// answered to a requester that signs.
	StandardHistory string `json:"standard_history"`
	// Window is how far back the card counts: "1", "3", "12" (months) or "all".
	Window string `json:"window"`
	// Staple is "on" or "off": attach the card to offers and orders.
	Staple string `json:"staple"`
	// MoreThanStandard is who decides a signed ask past the policy:
	// "ask_me" or "operator_decides".
	MoreThanStandard string `json:"more_than_standard"`
	// Receipt is "always" or "off": record-at-completion, both sides.
	Receipt string `json:"receipt"`
	// UnsignedAsksPerWeek limits askers that do not sign; 0 sets no limit.
	// It is never applied to signed askers or to the staple.
	UnsignedAsksPerWeek int `json:"unsigned_asks_per_week"`
	// UnderContract is "none" or "named_by_contract" (the under_contract
	// tier scope, applied only when a contract names it).
	UnderContract string `json:"under_contract"`
	// AskedLogRetentionDays is how long the holder-only asked-of-you log
	// is kept; 0 keeps it until the holder deletes it.
	AskedLogRetentionDays int      `json:"asked_log_retention_days"`
	Never                 []string `json:"never"`
}

// DefaultDisclosurePolicy is "Action State default v1" for a holder kind:
// Muse's person defaults, or Mesh's node defaults.
func DefaultDisclosurePolicy(holder string) (DisclosurePolicy, error) {
	p := DisclosurePolicy{Name: DefaultPolicyName, Holder: holder, StandardHistory: "on", Window: "3", Staple: "off", MoreThanStandard: "ask_me",
		Receipt: "always", UnsignedAsksPerWeek: 3, UnderContract: "none", Never: slices.Clone(NeverList)}
	switch holder {
	case HolderPerson:
	case HolderNode:
		p.Window, p.Staple, p.MoreThanStandard, p.UnsignedAsksPerWeek = "12", "on", "operator_decides", 0
	default:
		return DisclosurePolicy{}, fmt.Errorf("%w: policy holder %q is neither person nor node", ErrInvalid, holder)
	}
	return p, nil
}

// Validate rejects any value outside its enumeration and any never-list
// that is not the fixed one.
func (p DisclosurePolicy) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("%w: a disclosure policy has a name", ErrInvalid)
	}
	if !slices.Contains([]string{HolderPerson, HolderNode}, p.Holder) {
		return fmt.Errorf("%w: policy holder %q is neither person nor node", ErrInvalid, p.Holder)
	}
	for _, c := range []struct {
		field, value string
		allowed      []string
	}{
		{FieldStandardHistory, p.StandardHistory, []string{"on", "off"}},
		{FieldWindow, p.Window, []string{"1", "3", "12", "all"}},
		{FieldStaple, p.Staple, []string{"on", "off"}},
		{FieldMoreThanStd, p.MoreThanStandard, []string{"ask_me", "operator_decides"}},
		{FieldReceipt, p.Receipt, []string{"always", "off"}},
		{FieldUnderContract, p.UnderContract, []string{"none", "named_by_contract"}},
	} {
		if !slices.Contains(c.allowed, c.value) {
			return fmt.Errorf("%w: policy %s=%q is not one of %v", ErrInvalid, c.field, c.value, c.allowed)
		}
	}
	if p.UnsignedAsksPerWeek < 0 || p.AskedLogRetentionDays < 0 {
		return fmt.Errorf("%w: policy limits must not be negative", ErrInvalid)
	}
	if !slices.Equal(p.Never, NeverList) {
		return fmt.Errorf("%w: the never-list is fixed text and a policy carries it unchanged", ErrInvalid)
	}
	return nil
}

// Digest is the sha256 of the policy's JCS octets: the digest a standard
// card names (SH §5.1) and a commitment carries.
func (p DisclosurePolicy) Digest() (string, error) {
	body, err := canonicalJSON(p)
	if err != nil {
		return "", err
	}
	return digestBytes(body), nil
}

// SharePolicy is the switches Respond reads under this policy, the rest
// at their defaults. A book that holds the policy applies it over whatever
// SharePolicy its caller passes (Book.Respond, Book.Doctor).
func (p DisclosurePolicy) SharePolicy() SharePolicy { return p.overlay(DefaultSharePolicy()) }

// StandardPolicy is the two members of the policy a standard_history/1
// card states: its digest and its window (SH §5.1).
func (p DisclosurePolicy) StandardPolicy() (standardhistory.Policy, error) {
	digest, err := p.Digest()
	if err != nil {
		return standardhistory.Policy{}, err
	}
	w := standardhistory.Window{All: true}
	if p.Window != "all" {
		months, _ := strconv.Atoi(p.Window)
		w = standardhistory.Window{Months: months}
	}
	return standardhistory.Policy{Digest: digest, Window: w}, nil
}

// overlay is base with the switches this policy governs: standard history
// and record-at-completion. The others (history_segments, adjudications,
// witness) stay the caller's.
func (p DisclosurePolicy) overlay(base SharePolicy) SharePolicy {
	base.StandardHistory = p.StandardHistory
	base.RecordAtCompletion = "counterparty"
	if p.Receipt == "off" {
		base.RecordAtCompletion = "off"
	}
	return base
}

// unsignedAllowLocked records an ask of an asker that does not sign and
// reports whether it is within n a week. All such askers share one bucket,
// since none has an identity.
func (b *Book) unsignedAllowLocked(n int, now time.Time) bool {
	if b.unsignedAsks.limiter == nil || b.unsignedAsks.n != n {
		b.unsignedAsks.n, b.unsignedAsks.limiter = n, NewWindowLimiter(n, 7*24*time.Hour)
	}
	return b.unsignedAsks.limiter.Allow("", now)
}

// SettingRow is one line of the settings page, and of doctor's report:
// what a setting is and what it sends. The page and doctor render the same
// rows, so they cannot disagree on what a setting sends.
type SettingRow struct {
	Setting string `json:"setting"`
	Value   string `json:"value"`
	Sends   string `json:"sends"`
}

// Rows lists the settings page in order. The proof-of-records row is fixed.
func (p DisclosurePolicy) Rows() []SettingRow {
	window := "the last " + p.Window + " months"
	switch p.Window {
	case "1":
		window = "the last 1 month"
	case "all":
		window = "all time"
	}
	std := "a signed asker is refused derivation_unsupported; no standard card is served"
	if p.StandardHistory == "on" {
		std = "a signed asker receives the standard card counting " + window + "; an unsigned asker receives history_card/1 only"
	}
	staple := "no card is attached to offers or orders"
	if p.Staple == "on" {
		staple = "the standard card's digest is attached to offers and orders"
	}
	more := "an ask past the policy goes to the holder, who answers with a wider card, a selection, or a signed policy_declined"
	if p.MoreThanStandard == "operator_decides" {
		more = "an ask past the policy goes to the node operator, who answers with a wider card, a selection, or a signed policy_declined"
	}
	receipt := "record-at-completion is sealed on both sides"
	if p.Receipt == "off" {
		receipt = "no record-at-completion is sealed; the counterparty's receipt is not offered"
	}
	unsigned := "no limit on askers that do not sign"
	if p.UnsignedAsksPerWeek > 0 {
		unsigned = fmt.Sprintf("askers that do not sign share %d asks a week beyond the public checkpoints, then are refused policy_declined; signed askers and the staple are never limited", p.UnsignedAsksPerWeek)
	}
	contract := "no under_contract scope is granted"
	if p.UnderContract == "named_by_contract" {
		contract = "the under_contract scope is granted where a contract names it"
	}
	logs := "the asked-of-you log is kept for the holder only until the holder deletes it"
	if p.AskedLogRetentionDays > 0 {
		logs = fmt.Sprintf("the asked-of-you log is kept for the holder only, %d days", p.AskedLogRetentionDays)
	}
	return []SettingRow{
		{"proof_of_records", "always", "checkpoints and history_card/1 are answered to anyone, and are not counted against the unsigned-ask limit"},
		{FieldStandardHistory, p.StandardHistory, std},
		{FieldWindow, p.Window, "the standard card counts " + window},
		{FieldStaple, p.Staple, staple},
		{FieldMoreThanStd, p.MoreThanStandard, more},
		{FieldReceipt, p.Receipt, receipt},
		{FieldUnsignedAsks, strconv.Itoa(p.UnsignedAsksPerWeek), unsigned},
		{FieldUnderContract, p.UnderContract, contract},
		{FieldAskedLogDays, strconv.Itoa(p.AskedLogRetentionDays), logs},
		{"never", "fixed", "no matter who asks: " + joinList(p.Never)},
	}
}

func joinList(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

// PolicyChange is policy-change/v1: the one block the Muse laptop form,
// the Muse chat buttons (after agent-side batching) and the Mesh dashboard
// all produce. The executor runs the block, never the prose around it.
type PolicyChange struct {
	Profile    string                     `json:"profile"`
	BasePolicy string                     `json:"base_policy"`
	Round      uint64                     `json:"round"`
	Changes    map[string]json.RawMessage `json:"changes"`
	Then       []string                   `json:"then"`
}

// ParsePolicyChange reads a policy-change/v1 block.
func ParsePolicyChange(data []byte) (PolicyChange, error) {
	var c PolicyChange
	if err := decodeStrict(data, &c); err != nil {
		return PolicyChange{}, fmt.Errorf("%w: policy-change/v1: %v", ErrInvalid, err)
	}
	if c.Profile != PolicyChangeProfile {
		return PolicyChange{}, fmt.Errorf("%w: profile %q is not %s", ErrInvalid, c.Profile, PolicyChangeProfile)
	}
	return c, nil
}

func decodeStrict(data []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// PolicyStep is one field's change as it was sealed.
type PolicyStep struct {
	Field string
	From  string
	To    string
	// Policy is the policy in force after this step.
	Policy DisclosurePolicy
}

// Apply returns the policy after c, as one step per field in page order.
// It is pure: nothing is sealed. A change composed against another round or
// another policy is ErrStaleRound; a change naming a never-list item is
// ErrNeverList; an unknown field or value is ErrInvalid.
func (p DisclosurePolicy) Apply(c PolicyChange) ([]PolicyStep, error) {
	if c.Profile != PolicyChangeProfile {
		return nil, fmt.Errorf("%w: profile %q is not %s", ErrInvalid, c.Profile, PolicyChangeProfile)
	}
	digest, err := p.Digest()
	if err != nil {
		return nil, err
	}
	if c.BasePolicy != digest || c.Round != p.Round {
		return nil, fmt.Errorf("%w: the change was composed against round %d, policy %.12s; round %d is in force", ErrStaleRound, c.Round, c.BasePolicy, p.Round)
	}
	for field := range c.Changes {
		if slices.Contains(neverFields, field) {
			return nil, fmt.Errorf("%w: %q is on the never-list and is not a setting", ErrNeverList, field)
		}
		if !slices.Contains(SettingsFields, field) {
			return nil, fmt.Errorf("%w: %q is not a setting", ErrInvalid, field)
		}
	}
	if len(c.Changes) == 0 {
		return nil, fmt.Errorf("%w: a change names at least one field", ErrInvalid)
	}
	for _, step := range c.Then {
		if !slices.Contains(policyThenSteps, step) {
			return nil, fmt.Errorf("%w: then step %q is not one of %v", ErrInvalid, step, policyThenSteps)
		}
	}
	next := p
	next.Never = slices.Clone(p.Never)
	next.Round = p.Round + 1
	var steps []PolicyStep
	for _, field := range SettingsFields {
		raw, ok := c.Changes[field]
		if !ok {
			continue
		}
		if string(bytes.TrimSpace(raw)) == "null" {
			return nil, fmt.Errorf("%w: %s must have a value", ErrInvalid, field)
		}
		from, to, err := next.set(field, raw)
		if err != nil {
			return nil, err
		}
		if from == to {
			return nil, fmt.Errorf("%w: %s is already %s", ErrInvalid, field, to)
		}
		if err := next.Validate(); err != nil {
			return nil, err
		}
		step := PolicyStep{Field: field, From: from, To: to, Policy: next}
		step.Policy.Never = slices.Clone(next.Never)
		steps = append(steps, step)
	}
	return steps, nil
}

func (p *DisclosurePolicy) set(field string, raw json.RawMessage) (from, to string, err error) {
	var text *string
	var number *int
	switch field {
	case FieldStandardHistory:
		text = &p.StandardHistory
	case FieldWindow:
		text = &p.Window
	case FieldStaple:
		text = &p.Staple
	case FieldMoreThanStd:
		text = &p.MoreThanStandard
	case FieldReceipt:
		text = &p.Receipt
	case FieldUnderContract:
		text = &p.UnderContract
	case FieldUnsignedAsks:
		number = &p.UnsignedAsksPerWeek
	case FieldAskedLogDays:
		number = &p.AskedLogRetentionDays
	}
	if text != nil {
		from = *text
		if err := json.Unmarshal(raw, text); err != nil {
			return "", "", fmt.Errorf("%w: %s must be a string: %v", ErrInvalid, field, err)
		}
		return from, *text, nil
	}
	from = strconv.Itoa(*number)
	if err := json.Unmarshal(raw, number); err != nil {
		return "", "", fmt.Errorf("%w: %s must be a whole number: %v", ErrInvalid, field, err)
	}
	return from, strconv.Itoa(*number), nil
}

// policyStatement is the body of a policy record. Policy is the policy in
// force once the record is sealed; the latest record is the policy in force.
type policyStatement struct {
	Kind   string           `json:"kind"` // "adopted" or "change"
	Field  string           `json:"field,omitempty"`
	From   string           `json:"from,omitempty"`
	To     string           `json:"to,omitempty"`
	Change string           `json:"change,omitempty"` // digest of the policy-change/v1 block
	Policy DisclosurePolicy `json:"policy"`
	Digest string           `json:"policy_digest"`
}

// policyInForce reads the latest policy record of log.
func policyInForce(log []Record) (DisclosurePolicy, bool, error) {
	for i := len(log) - 1; i >= 0; i-- {
		h := log[i].Header
		if h.RecordType != RecordTypePolicy {
			continue
		}
		var st policyStatement
		if err := decodeStrict(h.Statement, &st); err != nil {
			return DisclosurePolicy{}, false, fmt.Errorf("%w: policy record %s: %v", ErrCorrupt, log[i].RecordID, err)
		}
		digest, err := st.Policy.Digest()
		if err != nil || st.Policy.Validate() != nil || digest != st.Digest {
			return DisclosurePolicy{}, false, fmt.Errorf("%w: policy record %s does not carry the policy it names", ErrCorrupt, log[i].RecordID)
		}
		return st.Policy, true, nil
	}
	return DisclosurePolicy{}, false, nil
}

// DisclosurePolicy returns the policy in force: the one the latest policy
// record carries. ErrNoPolicy when the book has adopted none.
func (b *Book) DisclosurePolicy() (DisclosurePolicy, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok, err := policyInForce(b.logLocked())
	if err != nil {
		return DisclosurePolicy{}, err
	}
	if !ok {
		return DisclosurePolicy{}, ErrNoPolicy
	}
	return p, nil
}

// AdoptPolicy seals the first policy record: the shipped default for a
// holder kind. A book that has a policy keeps it.
func (b *Book) AdoptPolicy(ctx context.Context, holder string) (DisclosurePolicy, Record, error) {
	p, err := DefaultDisclosurePolicy(holder)
	if err != nil {
		return DisclosurePolicy{}, Record{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if held, ok, err := policyInForce(b.logLocked()); err != nil || ok {
		if err == nil {
			err = fmt.Errorf("%w: this book already holds the policy %q at round %d", ErrInvalid, held.Name, held.Round)
		}
		return DisclosurePolicy{}, Record{}, err
	}
	digest, err := p.Digest()
	if err != nil {
		return DisclosurePolicy{}, Record{}, err
	}
	record, err := b.appendPolicyLocked(ctx, policyStatement{Kind: "adopted", Policy: p, Digest: digest})
	return p, record, err
}

// ApplyPolicyChange seals one policy record per changed field, in page
// order and returns the policy in force with the records. The then[] steps
// are checked here and run by the executor, which reads them from the block.
// The whole block is checked before anything is sealed, so a
// stale round or a refused field seals nothing.
func (b *Book) ApplyPolicyChange(ctx context.Context, c PolicyChange) (DisclosurePolicy, []Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok, err := policyInForce(b.logLocked())
	if err != nil {
		return DisclosurePolicy{}, nil, err
	}
	if !ok {
		return DisclosurePolicy{}, nil, ErrNoPolicy
	}
	steps, err := p.Apply(c)
	if err != nil {
		return DisclosurePolicy{}, nil, err
	}
	block, err := canonicalJSON(c)
	if err != nil {
		return DisclosurePolicy{}, nil, err
	}
	var records []Record
	for _, s := range steps {
		digest, err := s.Policy.Digest()
		if err != nil {
			return DisclosurePolicy{}, records, err
		}
		r, err := b.appendPolicyLocked(ctx, policyStatement{Kind: "change", Field: s.Field, From: s.From, To: s.To, Change: digestBytes(block), Policy: s.Policy, Digest: digest})
		if err != nil {
			return DisclosurePolicy{}, records, err
		}
		records = append(records, r)
	}
	return steps[len(steps)-1].Policy, records, nil
}

func (b *Book) appendPolicyLocked(ctx context.Context, st policyStatement) (Record, error) {
	body, err := canonicalJSON(st)
	if err != nil {
		return Record{}, err
	}
	return b.appendLocked(ctx, Entry{RecordType: RecordTypePolicy, EpistemicType: ProducerClaim, Links: []Link{}, Statement: json.RawMessage(body)})
}
