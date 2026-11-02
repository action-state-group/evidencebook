// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

func changeOf(t *testing.T, p DisclosurePolicy, changes map[string]any, then ...string) PolicyChange {
	t.Helper()
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]json.RawMessage{}
	for k, v := range changes {
		raw[k], err = json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
	}
	return PolicyChange{Profile: PolicyChangeProfile, BasePolicy: digest, Round: p.Round, Changes: raw, Then: then}
}

func mustPolicy(t *testing.T, holder string) DisclosurePolicy {
	t.Helper()
	p, err := DefaultDisclosurePolicy(holder)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultDisclosurePolicies(t *testing.T) {
	person, node := mustPolicy(t, HolderPerson), mustPolicy(t, HolderNode)
	if person.Name != DefaultPolicyName || person.Window != "3" || person.Staple != "off" || person.MoreThanStandard != "ask_me" || person.UnsignedAsksPerWeek != 3 || person.StandardHistory != "on" || person.Receipt != "always" {
		t.Errorf("person defaults = %+v", person)
	}
	if node.Window != "12" || node.Staple != "on" || node.MoreThanStandard != "operator_decides" || node.UnderContract != "none" {
		t.Errorf("node defaults = %+v", node)
	}
	if _, err := DefaultDisclosurePolicy("robot"); !errors.Is(err, ErrInvalid) {
		t.Errorf("holder robot: %v", err)
	}
	bad := person
	bad.Never = slices.DeleteFunc(slices.Clone(NeverList), func(s string) bool { return s == "rules" })
	if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a shortened never-list: %v", err)
	}
}

func TestPolicyApplyRefusals(t *testing.T) {
	p := mustPolicy(t, HolderPerson)
	other := changeOf(t, p, map[string]any{FieldWindow: "12"})
	other.BasePolicy = digestBytes([]byte("another policy"))
	stale := changeOf(t, p, map[string]any{FieldWindow: "12"})
	stale.Round = 7
	for name, tc := range map[string]struct {
		c    PolicyChange
		want error
	}{
		"another base policy": {other, ErrStaleRound},
		"a stale round":       {stale, ErrStaleRound},
		"never-list contact":  {changeOf(t, p, map[string]any{"contact": "everyone"}), ErrNeverList},
		"never-list rules":    {changeOf(t, p, map[string]any{"rules": "shared"}), ErrNeverList},
		"proof of records":    {changeOf(t, p, map[string]any{"proof_of_records": "off"}), ErrNeverList},
		"unknown field":       {changeOf(t, p, map[string]any{"colour": "red"}), ErrInvalid},
		"value outside enum":  {changeOf(t, p, map[string]any{FieldWindow: "2"}), ErrInvalid},
		"wrong type":          {changeOf(t, p, map[string]any{FieldUnsignedAsks: "many"}), ErrInvalid},
		"negative limit":      {changeOf(t, p, map[string]any{FieldUnsignedAsks: -1}), ErrInvalid},
		"no fields":           {changeOf(t, p, map[string]any{}), ErrInvalid},
		"same value":          {changeOf(t, p, map[string]any{FieldWindow: "3"}), ErrInvalid},
		"unknown then step":   {changeOf(t, p, map[string]any{FieldWindow: "12"}, "email_everyone"), ErrInvalid},
	} {
		if _, err := p.Apply(tc.c); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	// One bad field in a block refuses the block.
	nullChange := changeOf(t, p, map[string]any{})
	nullChange.Changes = map[string]json.RawMessage{FieldWindow: json.RawMessage("null")}
	if _, err := p.Apply(nullChange); !errors.Is(err, ErrInvalid) {
		t.Errorf("a null value: %v", err)
	}
	if _, err := p.Apply(changeOf(t, p, map[string]any{FieldWindow: "12", FieldStaple: "maybe"})); !errors.Is(err, ErrInvalid) {
		t.Errorf("a block with one bad value: %v", err)
	}
	if _, err := ParsePolicyChange([]byte(`{"profile":"policy-change/v2"}`)); !errors.Is(err, ErrInvalid) {
		t.Errorf("another profile: %v", err)
	}
	if _, err := ParsePolicyChange([]byte(`{"profile":"policy-change/v1","base_policy":"x","round":0,"changes":{},"then":[],"extra":1}`)); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unknown member: %v", err)
	}
}

// policyBook is a standard-history book that adopts a person policy and
// states the policy in force on its cards.
func policyBook(t *testing.T) *Book {
	t.Helper()
	var book *Book
	inputs := func(ctx context.Context, anchor Checkpoint, records []Record) (standardhistory.Policy, []standardhistory.Deal, error) {
		policy, deals := standardhistory.Policy{}, []standardhistory.Deal{}
		held, err := book.DisclosurePolicy()
		if err != nil {
			return policy, nil, err
		}
		if policy, err = held.StandardPolicy(); err != nil {
			return policy, nil, err
		}
		_, deals, err = dealsOf(nil)(ctx, anchor, records)
		return policy, deals, err
	}
	book = standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: inputs}, nil)
	if _, _, err := book.AdoptPolicy(context.Background(), HolderPerson); err != nil {
		t.Fatal(err)
	}
	anchorPin(t, book)
	return book
}

func TestPolicyChangeIsOneSealedRecordPerField(t *testing.T) {
	ctx := context.Background()
	book := policyBook(t)
	if _, _, err := book.AdoptPolicy(ctx, HolderPerson); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a second adoption: %v", err)
	}
	p, err := book.DisclosurePolicy()
	if err != nil {
		t.Fatal(err)
	}
	size := book.Size()
	c := changeOf(t, p, map[string]any{FieldWindow: "12", FieldStaple: "on", FieldUnsignedAsks: 5}, "rebuild_card", "probe_as_stranger")
	now, records, err := book.ApplyPolicyChange(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || book.Size() != size+3 {
		t.Fatalf("records = %d, book grew %d", len(records), book.Size()-size)
	}
	if now.Round != 1 || now.Window != "12" || now.Staple != "on" || now.UnsignedAsksPerWeek != 5 {
		t.Errorf("policy after = %+v", now)
	}
	// The record order is page order, each naming what it changed from.
	var fields []string
	for _, r := range records {
		var st policyStatement
		if err := json.Unmarshal(r.Header.Statement, &st); err != nil {
			t.Fatal(err)
		}
		fields = append(fields, fmt.Sprintf("%s:%s>%s", st.Field, st.From, st.To))
	}
	if want := []string{"window:3>12", "staple:off>on", "unsigned_asks_per_week:3>5"}; !slices.Equal(fields, want) {
		t.Errorf("records = %v, want %v", fields, want)
	}
	held, err := book.DisclosurePolicy()
	if err != nil || held.Window != "12" {
		t.Fatalf("policy in force = %+v, %v", held, err)
	}

	// The same block again is stale: nothing more is sealed.
	if _, _, err := book.ApplyPolicyChange(ctx, c); !errors.Is(err, ErrStaleRound) || book.Size() != size+3 {
		t.Fatalf("a stale round: err = %v, grew %d", err, book.Size()-size-3)
	}
	// A refused block seals nothing, even when its first field is fine.
	bad := changeOf(t, held, map[string]any{FieldWindow: "1", "contact": "all"})
	if _, _, err := book.ApplyPolicyChange(ctx, bad); !errors.Is(err, ErrNeverList) || book.Size() != size+3 {
		t.Fatalf("a never-list change: err = %v", err)
	}
	// A book without a policy refuses a change.
	if _, _, err := checkpointedBook(t, "none", 4).ApplyPolicyChange(ctx, c); !errors.Is(err, ErrNoPolicy) {
		t.Errorf("no policy: %v", err)
	}
}

// A policy record is the holder's own rules: no requester can ask for it.
func TestPolicyRecordIsNeverServed(t *testing.T) {
	ctx := context.Background()
	book := policyBook(t)
	p, _ := book.DisclosurePolicy()
	_, records, err := book.ApplyPolicyChange(ctx, changeOf(t, p, map[string]any{FieldWindow: "1"}))
	if err != nil {
		t.Fatal(err)
	}
	pin := anchorPin(t, book)
	reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: pin}}),
		RespondOptions{AuthenticatedRequester: "carol", Policy: policyWith("peers")})
	if reply.Refusal == nil {
		t.Fatalf("a policy record was served: %+v", reply)
	}
}

// The page and doctor --probe agree on what each setting sends: both are
// the policy's rows, and the probe's card states the window the row says.
func TestDoctorProbeAgreesWithTheSettingsRows(t *testing.T) {
	ctx := context.Background()
	book := policyBook(t)
	for _, window := range []string{"1", "12", "all", "3"} {
		p, _ := book.DisclosurePolicy()
		if _, _, err := book.ApplyPolicyChange(ctx, changeOf(t, p, map[string]any{FieldWindow: window})); err != nil {
			t.Fatal(err)
		}
		held, _ := book.DisclosurePolicy()
		findings, err := book.Doctor(ctx, DoctorOptions{Policy: held.SharePolicy(), Probe: strangerTransport(book, held.SharePolicy()), As: DoctorAsSigned})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range held.Rows() {
			f := findingOf(t, findings, DoctorSettingPrefix+row.Setting)
			if f.State != DoctorStateAnswered || f.Detail != row.Value+": "+row.Sends {
				t.Errorf("window %s: %s = %+v, row %+v", window, row.Setting, f, row)
			}
		}
		card := findingOf(t, findings, DoctorProbeStandardCard)
		stated := "window " + window + " months"
		if window == "all" {
			stated = "window all time"
		}
		if card.State != DoctorStateMatch || !strings.Contains(card.Detail, stated) {
			t.Errorf("window %s: probe = %+v", window, card)
		}
	}

	// Standard history off: the row says so, the probe says so, and a
	// signed asker is refused as the probe says.
	p, _ := book.DisclosurePolicy()
	if _, _, err := book.ApplyPolicyChange(ctx, changeOf(t, p, map[string]any{FieldStandardHistory: "off"})); err != nil {
		t.Fatal(err)
	}
	held, _ := book.DisclosurePolicy()
	findings, err := book.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy(), Probe: strangerTransport(book, held.SharePolicy()), As: DoctorAsSigned})
	if err != nil {
		t.Fatal(err)
	}
	if f := findingOf(t, findings, DoctorProbeStandardCard); f.State != DoctorStateOff {
		t.Errorf("probe with the card off = %+v", f)
	}
	if f := findingOf(t, findings, DoctorSettingPrefix+FieldStandardHistory); !strings.HasPrefix(f.Detail, "off: ") || !strings.Contains(f.Detail, "derivation_unsupported") {
		t.Errorf("row with the card off = %+v", f)
	}
	reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}),
		RespondOptions{AuthenticatedRequester: "carol", Policy: held.SharePolicy()})
	if reply.Refusal == nil || reply.Refusal.Reason != ReasonDerivationUnsupported {
		t.Errorf("a signed asker with the card off: %+v", reply)
	}
	// A stranger's probe asks only the public questions.
	findings, err = book.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy(), Probe: strangerTransport(book, held.SharePolicy()), As: DoctorAsStranger})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(findings, func(f DoctorFinding) bool { return f.Check == DoctorProbeHistoryCard }) || slices.ContainsFunc(findings, func(f DoctorFinding) bool { return f.Check == DoctorProbeStandardCard }) {
		t.Errorf("stranger probe findings = %+v", findings)
	}
	if _, err := book.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy(), As: "friend"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("doctor --as friend: %v", err)
	}
}

// A caller that passes the defaults gets the policy the book holds: the
// card off, the receipt off and the unsigned-ask limit apply through
// Respond, as doctor reports them.
func TestRespondAppliesTheBooksPolicy(t *testing.T) {
	ctx := context.Background()
	book := policyBook(t)
	pin := anchorPin(t, book)
	ask := func(requester string, req EvidenceRequest) Reply {
		reply, _ := respondWith(t, book, jsonRequest(t, req), RespondOptions{AuthenticatedRequester: requester, Policy: DefaultSharePolicy()})
		return reply
	}
	private := EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: digestBytes([]byte("x"))}, Coverage: Coverage{ExpectedPin: pin}}
	public := EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{ExpectedPin: pin}}
	card := EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: pin}}

	if r := ask("carol", card); r.Envelope == nil {
		t.Fatalf("the card is on: %+v", r)
	}
	// Three unsigned asks beyond the public ones are answered (refused
	// not_authorized); the fourth is refused policy_declined. Checkpoints
	// and signed askers are never counted.
	for i := range 3 {
		if r := ask("", private); r.Refusal == nil || r.Refusal.Reason != ReasonNotAuthorized {
			t.Fatalf("unsigned ask %d: %+v", i+1, r)
		}
	}
	if r := ask("", private); r.Refusal == nil || r.Refusal.Reason != ReasonPolicyDeclined {
		t.Fatalf("a fourth unsigned ask: %+v", r)
	}
	historyCard := EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationHistoryCard, Coverage: Coverage{ExpectedPin: pin}}
	for range 5 {
		if r := ask("", historyCard); r.Envelope == nil {
			t.Fatalf("an unsigned history_card/1 ask was limited: %+v", r)
		}
		if r := ask("", public); r.Envelope == nil {
			t.Fatalf("a public ask was limited: %+v", r)
		}
		if r := ask("carol", card); r.Envelope == nil {
			t.Fatalf("a signed ask was limited: %+v", r)
		}
	}
	// The limit follows the policy: raised, the next ask is answered.
	p, _ := book.DisclosurePolicy()
	if _, _, err := book.ApplyPolicyChange(ctx, changeOf(t, p, map[string]any{FieldUnsignedAsks: 10, FieldStandardHistory: "off"})); err != nil {
		t.Fatal(err)
	}
	if r := ask("", private); r.Refusal == nil || r.Refusal.Reason != ReasonNotAuthorized {
		t.Fatalf("an unsigned ask after the limit rose: %+v", r)
	}
	if r := ask("carol", card); r.Refusal == nil || r.Refusal.Reason != ReasonDerivationUnsupported {
		t.Fatalf("the card is off: %+v", r)
	}
}

// The probe checks the card against the policy in force, so a host whose
// card states another window is a mismatch, not a match.
func TestDoctorProbeCatchesACardThePolicyDoesNotDescribe(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil) // its own window and digest
	anchorPin(t, book)
	if _, _, err := book.AdoptPolicy(ctx, HolderPerson); err != nil {
		t.Fatal(err)
	}
	findings, err := book.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy(), As: DoctorAsSigned, Probe: strangerTransport(book, DefaultSharePolicy())})
	if err != nil {
		t.Fatal(err)
	}
	if f := findingOf(t, findings, DoctorProbeStandardCard); f.State != DoctorStateMismatch || !strings.Contains(f.Detail, "other than the one in force") {
		t.Errorf("a card of another policy: %+v", f)
	}
	node := standardHistoryBook(t, &StandardHistorySource{HolderKind: "node", Inputs: dealsOf(nil)}, nil)
	anchorPin(t, node)
	if _, _, err := node.AdoptPolicy(ctx, HolderPerson); err != nil {
		t.Fatal(err)
	}
	if findings, err = node.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy(), As: DoctorAsSigned, Probe: strangerTransport(node, DefaultSharePolicy())}); err != nil {
		t.Fatal(err)
	}
	if f := findingOf(t, findings, DoctorProbeStandardCard); f.State != DoctorStateMismatch || !strings.Contains(f.Detail, "for a node") {
		t.Errorf("a node host with a person policy: %+v", f)
	}
}

func TestZeroSettingsReadInWords(t *testing.T) {
	node := mustPolicy(t, HolderNode)
	for _, row := range node.Rows() {
		if (row.Setting == FieldUnsignedAsks && row.Value != "no limit") || (row.Setting == FieldAskedLogDays && row.Value != "until deleted") {
			t.Errorf("row %+v shows a bare 0", row)
		}
	}
}

func TestPolicyRecordsAreNeverServedByAnyPath(t *testing.T) {
	for _, recordType := range []string{RecordTypePolicy, RecordTypeRequestAnswered, RecordTypeDisclosure} {
		if !neverServed(recordType) {
			t.Errorf("%s is served", recordType)
		}
	}
	if neverServed("observation") {
		t.Error("an observation is never served")
	}
}

// Lowering the limit mid-week keeps the asks already counted.
func TestLoweredUnsignedLimitKeepsTheCount(t *testing.T) {
	var b Book
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	b.mu.Lock()
	defer b.mu.Unlock()
	for range 5 {
		if !b.unsignedAllowLocked(5, now) {
			t.Fatal("an ask within the limit was refused")
		}
	}
	if b.unsignedAllowLocked(3, now.Add(time.Hour)) {
		t.Error("an ask after the limit was lowered below the count was answered")
	}
	if !b.unsignedAllowLocked(3, now.Add(8*24*time.Hour)) {
		t.Error("the week did not roll")
	}
}
