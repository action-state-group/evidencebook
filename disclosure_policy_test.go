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
		"unknown then step":   {changeOf(t, p, map[string]any{FieldWindow: "12"}, "email_everyone"), ErrInvalid},
	} {
		if _, err := p.Apply(tc.c); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	// One bad field in a block refuses the block.
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
			if f.State != row.Value || f.Detail != row.Sends {
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
	findings, err := book.Doctor(ctx, DoctorOptions{Probe: strangerTransport(book, held.SharePolicy()), As: DoctorAsSigned})
	if err != nil {
		t.Fatal(err)
	}
	if f := findingOf(t, findings, DoctorProbeStandardCard); f.State != DoctorStateOff {
		t.Errorf("probe with the card off = %+v", f)
	}
	if f := findingOf(t, findings, DoctorSettingPrefix+FieldStandardHistory); f.State != "off" || !strings.Contains(f.Detail, "derivation_unsupported") {
		t.Errorf("row with the card off = %+v", f)
	}
	reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}),
		RespondOptions{AuthenticatedRequester: "carol", Policy: held.SharePolicy()})
	if reply.Refusal == nil || reply.Refusal.Reason != ReasonDerivationUnsupported {
		t.Errorf("a signed asker with the card off: %+v", reply)
	}
	// A stranger's probe asks only the public questions.
	findings, err = book.Doctor(ctx, DoctorOptions{Probe: strangerTransport(book, held.SharePolicy()), As: DoctorAsStranger})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(findings, func(f DoctorFinding) bool { return f.Check == DoctorProbeHistoryCard }) || slices.ContainsFunc(findings, func(f DoctorFinding) bool { return f.Check == DoctorProbeStandardCard }) {
		t.Errorf("stranger probe findings = %+v", findings)
	}
	if _, err := book.Doctor(ctx, DoctorOptions{As: "friend"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("doctor --as friend: %v", err)
	}
}

func TestUnsignedAskLimiter(t *testing.T) {
	person, node := mustPolicy(t, HolderPerson), mustPolicy(t, HolderNode)
	if node.UnsignedAskLimiter() != nil {
		t.Error("the node default sets no limit")
	}
	l := person.UnsignedAskLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		if !l.Allow("", now.Add(time.Duration(i)*time.Hour)) {
			t.Fatalf("unsigned ask %d refused", i+1)
		}
	}
	if l.Allow("", now.Add(4*time.Hour)) {
		t.Error("a fourth unsigned ask in the week was answered")
	}
	for range 10 {
		if !l.Allow("carol", now.Add(4*time.Hour)) {
			t.Fatal("a signed asker was limited")
		}
	}
	if !l.Allow("", now.Add(8*24*time.Hour)) {
		t.Error("the week did not roll")
	}
}
