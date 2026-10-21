// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Deal Reconcile Contract's conformance vectors, vendored byte for byte
// from agent-action-capsule vectors/deal-reconcile-v0/ at main 2cf9037. They
// are spec-derived: every expected member was written from
// spec/deal-reconcile-contract-v0.md, never from an implementation.
// testdata/deal-reconcile-v0/SHA256SUMS is the upstream file, so a drifted
// copy fails here.
const drVectors = "testdata/deal-reconcile-v0"

func TestVendoredDealReconcileVectorsMatchUpstreamSums(t *testing.T) {
	file, err := os.Open(filepath.Join(drVectors, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	checked := 0
	for scanner.Scan() {
		sum, name, ok := strings.Cut(scanner.Text(), "  ")
		if !ok {
			t.Fatalf("SHA256SUMS line %q", scanner.Text())
		}
		data, err := os.ReadFile(filepath.Join(drVectors, name))
		if err != nil {
			t.Fatal(err)
		}
		if digestBytes(data) != sum {
			t.Fatalf("%s does not match its upstream SHA-256", name)
		}
		checked++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if checked != 3 {
		t.Fatalf("checked %d files, want 3", checked)
	}
}

type drVectorFile struct {
	Count    int        `json:"count"`
	Cases    []drVector `json:"cases"`
	Contract struct {
		ContractRef string `json:"contract_ref"`
	} `json:"contract"`
}

type drVector struct {
	ID     string `json:"id"`
	Inputs struct {
		ContractRef  string                 `json:"contract_ref"`
		SideA        string                 `json:"side_a"`
		Peer         string                 `json:"peer"`
		Period       ResultPeriod           `json:"period"`
		HalfMappings map[string]HalfMapping `json:"half_mappings"`
		Halves       []struct {
			Book       string `json:"book"`
			OctetsText string `json:"octets_text"`
			Digest     string `json:"digest"`
		} `json:"halves"`
		FieldOpenings map[string]map[string]Opening `json:"field_openings"`
		Coverage      map[string]CoverageGiven      `json:"coverage"`
	} `json:"inputs"`
	Expected map[string]json.RawMessage `json:"expected"`
}

func loadDRVectors(t *testing.T) drVectorFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(drVectors, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file drVectorFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Count != 14 || len(file.Cases) != 14 {
		t.Fatalf("vectors hold %d cases (count %d), want 14", len(file.Cases), file.Count)
	}
	return file
}

func (v drVector) input(t *testing.T) DealReconcileInput {
	t.Helper()
	var generated struct {
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal(v.Expected["result"], &generated); err != nil {
		t.Fatal(err)
	}
	in := DealReconcileInput{
		SideA: v.Inputs.SideA, Peer: v.Inputs.Peer, Period: v.Inputs.Period, HalfMappings: v.Inputs.HalfMappings,
		FieldOpenings: v.Inputs.FieldOpenings, Coverage: v.Inputs.Coverage, GeneratedAt: generated.GeneratedAt,
	}
	for _, h := range v.Inputs.Halves {
		octets := []byte(h.OctetsText)
		if digestBytes(octets) != h.Digest {
			t.Fatalf("%s: a half's digest is not the SHA-256 of its octets", v.ID)
		}
		in.Halves = append(in.Halves, DealHalf{Book: h.Book, Octets: octets})
	}
	return in
}

// sameJSON compares two JSON values after JCS.
func sameJSON(t *testing.T, got any, want json.RawMessage) bool {
	t.Helper()
	gotJCS, err := canonicalJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	wantValue, err := decodeJSON(want)
	if err != nil {
		t.Fatal(err)
	}
	wantJCS, err := canonicalJSON(wantValue)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(gotJCS, wantJCS)
}

func TestDealReconcileVectors(t *testing.T) {
	file := loadDRVectors(t)
	if file.Contract.ContractRef != DealReconcileContractRef {
		t.Fatalf("the vectors' contract_ref is %q", file.Contract.ContractRef)
	}
	for _, v := range file.Cases {
		t.Run(v.ID, func(t *testing.T) {
			if v.Inputs.ContractRef != DealReconcileContractRef {
				t.Fatalf("contract_ref %q", v.Inputs.ContractRef)
			}
			got, err := RecomputeDealReconcile(v.input(t))
			if err != nil {
				t.Fatal(err)
			}
			members := map[string]any{
				"field_openings": got.FieldOpenings, "set_completeness": got.SetCompleteness, "pairings": got.Pairings,
				"tallies": got.Tallies, "sufficiency": got.Sufficiency, "verdict": got.Verdict,
				"deal_state": got.DealState, "result": got.Result,
				// The claim has no member that cites a Close (DR §7.4).
				"cites_close": false,
			}
			for name, want := range v.Expected {
				value, ok := members[name]
				if !ok {
					t.Fatalf("expected member %q is not checked", name)
				}
				if !sameJSON(t, value, want) {
					encoded, _ := json.Marshal(value)
					t.Errorf("%s: got %s, want %s", name, encoded, want)
				}
			}
			if len(v.Expected) != len(members) {
				t.Errorf("the case expects %d members, the recompute checks %d", len(v.Expected), len(members))
			}
		})
	}
}

// pairedInput is the pair-matched vector, for cases the vectors leave out.
func pairedInput(t *testing.T) DealReconcileInput {
	t.Helper()
	for _, v := range loadDRVectors(t).Cases {
		if v.ID == "pair-matched" {
			return v.input(t)
		}
	}
	t.Fatal("no pair-matched vector")
	return DealReconcileInput{}
}

// rewriteHalf replaces one half's octets with a changed copy, moving its
// openings to the new digest.
func rewriteHalf(t *testing.T, in *DealReconcileInput, i int, change func(map[string]any)) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(in.Halves[i].Octets, &doc); err != nil {
		t.Fatal(err)
	}
	change(doc)
	octets, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	old := digestBytes(in.Halves[i].Octets)
	openings := map[string]map[string]Opening{}
	for digest, o := range in.FieldOpenings {
		if digest == old {
			digest = digestBytes(octets)
		}
		openings[digest] = o
	}
	in.FieldOpenings = openings
	in.Halves[i].Octets = octets
}

func TestDealReconcileRule3ReadsIllFormedValuesAsAbsent(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"unsupported commit_alg": func(doc map[string]any) { doc["terms"].(map[string]any)["alg"] = "sha256-jcs" },
		"commit_alg not text":    func(doc map[string]any) { doc["terms"].(map[string]any)["alg"] = 1 },
		"malformed commitment": func(doc map[string]any) {
			terms := doc["terms"].(map[string]any)
			terms["amount"] = strings.ToUpper(terms["amount"].(string))
		},
		"commitment absent": func(doc map[string]any) { delete(doc["terms"].(map[string]any), "amount") },
	} {
		t.Run(name, func(t *testing.T) {
			in := pairedInput(t)
			rewriteHalf(t, &in, 1, change)
			got, err := RecomputeDealReconcile(in)
			if err != nil {
				t.Fatal(err)
			}
			if state := got.Pairings["amount-commitment-matched"]["deal-1"]; state != Insufficient {
				t.Fatalf("amount is %s, want INSUFFICIENT", state)
			}
			if got.Verdict["amount-commitment-matched"] != "not_evaluable" || got.Sufficiency["amount-commitment-matched"] != "GAP" {
				t.Fatalf("amount is %s/%s", got.Sufficiency["amount-commitment-matched"], got.Verdict["amount-commitment-matched"])
			}
			want := Matched
			if strings.Contains(name, "commit_alg") {
				// The algorithm covers both fields.
				want = Insufficient
			}
			if state := got.Pairings["currency-commitment-matched"]["deal-1"]; state != want {
				t.Fatalf("currency is %s, want %s", state, want)
			}
		})
	}
}

func TestDealReconcileMissingOpeningOutranksFailedOne(t *testing.T) {
	in := pairedInput(t)
	a, b := digestBytes(in.Halves[0].Octets), digestBytes(in.Halves[1].Octets)
	wrong := in.FieldOpenings[a]["amount"]
	wrong.Text = "999.00"
	in.FieldOpenings[a] = map[string]Opening{"amount": wrong, "currency": in.FieldOpenings[a]["currency"]}
	delete(in.FieldOpenings[b], "amount")
	got, err := RecomputeDealReconcile(in)
	if err != nil {
		t.Fatal(err)
	}
	if state := got.Pairings["amount-commitment-matched"]["deal-1"]; state != Insufficient {
		t.Fatalf("amount is %s, want INSUFFICIENT: rule 3 comes before rule 4", state)
	}
	if got.FieldOpenings[a]["amount"] != "FAIL" {
		t.Fatalf("side A's amount opening is %q, want FAIL", got.FieldOpenings[a]["amount"])
	}
}

func TestDealReconcileTwoHalvesOnASideAreUnresolved(t *testing.T) {
	in := pairedInput(t)
	second := in.Halves[1]
	in.Halves = append(in.Halves, second)
	rewriteHalf(t, &in, 2, func(doc map[string]any) { doc["recorded_at"] = "2026-10-16T00:00:00Z" })
	got, err := RecomputeDealReconcile(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range dealRequirements {
		if state := got.Pairings[req.id]["deal-1"]; state != Unresolved {
			t.Fatalf("%s is %s, want UNRESOLVED (rule 1)", req.id, state)
		}
	}
	if len(got.Result.Claims[0].Evidence) != 3 {
		t.Fatalf("evidence holds %d digests, want all 3 halves", len(got.Result.Claims[0].Evidence))
	}
}

func TestDealReconcilePeriodIsHalfOpen(t *testing.T) {
	in := pairedInput(t)
	// Side B's half is recorded at 2026-10-15T00:00:01Z.
	in.Period = ResultPeriod{Start: "2026-10-15T00:00:00Z", End: "2026-10-15T00:00:01Z"}
	got, err := RecomputeDealReconcile(in)
	if err != nil {
		t.Fatal(err)
	}
	if state := got.Pairings["amount-commitment-matched"]["deal-1"]; state != Insufficient {
		t.Fatalf("with side B's half at the period's end, amount is %s, want INSUFFICIENT (side B NOT_SHOWN)", state)
	}
	if len(got.Result.Claims[0].Evidence) != 1 {
		t.Fatalf("evidence holds %d digests, want side A's only", len(got.Result.Claims[0].Evidence))
	}
	in.Period.End = "2026-10-15T00:00:02Z"
	if got, err = RecomputeDealReconcile(in); err != nil {
		t.Fatal(err)
	}
	if state := got.Pairings["amount-commitment-matched"]["deal-1"]; state != Matched {
		t.Fatalf("with both halves in the period, amount is %s, want MATCHED", state)
	}
}

func TestDealReconcileLeavesOutAThirdBook(t *testing.T) {
	in := pairedInput(t)
	in.HalfMappings["book-c"] = in.HalfMappings["book-b"]
	in.Halves = append(in.Halves, DealHalf{Book: "book-c", Octets: bytes.Replace(in.Halves[1].Octets, []byte(`"book-b"`), []byte(`"book-c"`), 1)})
	got, err := RecomputeDealReconcile(in)
	if err != nil {
		t.Fatal(err)
	}
	if state := got.Pairings["amount-commitment-matched"]["deal-1"]; state != Matched {
		t.Fatalf("amount is %s, want MATCHED: book-c is neither side", state)
	}
}

func TestDealReconcileRefusesMalformedInput(t *testing.T) {
	for name, change := range map[string]func(*testing.T, *DealReconcileInput){
		"book_id is not the book": func(t *testing.T, in *DealReconcileInput) {
			rewriteHalf(t, in, 1, func(doc map[string]any) { doc["book"] = "book-a" })
		},
		"deal_id absent": func(t *testing.T, in *DealReconcileInput) {
			rewriteHalf(t, in, 1, func(doc map[string]any) { delete(doc, "deal_ref") })
		},
		"deal_id empty": func(t *testing.T, in *DealReconcileInput) {
			rewriteHalf(t, in, 1, func(doc map[string]any) { doc["deal_ref"] = "" })
		},
		"recorded_at not seconds": func(t *testing.T, in *DealReconcileInput) {
			rewriteHalf(t, in, 1, func(doc map[string]any) { doc["recorded_at"] = "2026-10-15T00:00:01.5Z" })
		},
		"no half mapping": func(t *testing.T, in *DealReconcileInput) { delete(in.HalfMappings, "book-b") },
		"half not JSON":   func(t *testing.T, in *DealReconcileInput) { in.Halves[1].Octets = []byte("{") },
		"same book twice": func(t *testing.T, in *DealReconcileInput) { in.Peer = in.SideA },
		"period backwards": func(t *testing.T, in *DealReconcileInput) {
			in.Period.Start, in.Period.End = in.Period.End, in.Period.Start
		},
		"generated_at missing": func(t *testing.T, in *DealReconcileInput) {
			in.GeneratedAt = ""
		},
		"bad pointer": func(t *testing.T, in *DealReconcileInput) {
			m := in.HalfMappings["book-b"]
			members := map[string]string{}
			for k, v := range m.Members {
				members[k] = v
			}
			members[HalfMemberDealID] = "deal_ref"
			in.HalfMappings["book-b"] = HalfMapping{Profile: m.Profile, Members: members}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := pairedInput(t)
			change(t, &in)
			if _, err := RecomputeDealReconcile(in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestResolvePointer(t *testing.T) {
	doc, err := decodeJSON([]byte(`{"a/b":{"c~d":[10,{"e":null}]},"":1}`))
	if err != nil {
		t.Fatal(err)
	}
	for pointer, want := range map[string]string{"/a~1b/c~0d/0": "10", "/a~1b/c~0d/1/e": "<nil>", "/": "1"} {
		got, found, err := resolvePointer(doc, pointer)
		if err != nil || !found {
			t.Fatalf("%s: found %v, err %v", pointer, found, err)
		}
		if s := strings.TrimSpace(jsonText(got)); s != want {
			t.Fatalf("%s: got %s, want %s", pointer, s, want)
		}
	}
	for _, pointer := range []string{"/x", "/a~1b/c~0d/2", "/a~1b/c~0d/01", "/a~1b/c~0d/-", "/a~1b/c~0d/+0", "/a~1b/c~0d/-0", "/a~1b/c~0d/+1", "/a~1b/c~0d/0/x"} {
		if _, found, err := resolvePointer(doc, pointer); err != nil || found {
			t.Fatalf("%s: found %v, err %v; want absent", pointer, found, err)
		}
	}
	for _, pointer := range []string{"a", "/a~2b", "/missing/~2bad", "/a~1b/c~0d/0/~"} {
		if _, _, err := resolvePointer(doc, pointer); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: got %v, want ErrInvalid", pointer, err)
		}
	}
}

func jsonText(v any) string {
	if v == nil {
		return "<nil>"
	}
	encoded, _ := json.Marshal(v)
	return string(encoded)
}

// copyHalf adds a copy of half i, changed, keeping the original and copying
// its openings to the copy's digest.
func copyHalf(t *testing.T, in *DealReconcileInput, i int, change func(map[string]any)) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(in.Halves[i].Octets, &doc); err != nil {
		t.Fatal(err)
	}
	change(doc)
	octets, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestBytes(octets)
	openings := map[string]Opening{}
	for field, o := range in.FieldOpenings[digestBytes(in.Halves[i].Octets)] {
		openings[field] = o
	}
	in.FieldOpenings[digest] = openings
	in.Halves = append(in.Halves, DealHalf{Book: in.Halves[i].Book, Octets: octets})
	return digest
}

// Two deals, one INSUFFICIENT and one UNRESOLVED: GAP wins (DR §7.2), and
// the tallies, verdict, evidence and aggregate count both deals.
func TestDealReconcileGapOutranksUnknownAcrossDeals(t *testing.T) {
	in := pairedInput(t)
	b1 := digestBytes(in.Halves[1].Octets)
	toDeal2 := func(doc map[string]any) { doc["deal_ref"] = "deal-2" }
	copyHalf(t, &in, 0, toDeal2)
	b2 := copyHalf(t, &in, 1, toDeal2)
	delete(in.FieldOpenings[b1], "amount")
	wrong := in.FieldOpenings[b2]["amount"]
	wrong.Text = "1.00"
	in.FieldOpenings[b2]["amount"] = wrong
	got, err := RecomputeDealReconcile(in)
	if err != nil {
		t.Fatal(err)
	}
	req := "amount-commitment-matched"
	if got.Pairings[req]["deal-1"] != Insufficient || got.Pairings[req]["deal-2"] != Unresolved {
		t.Fatalf("pairings %v", got.Pairings[req])
	}
	if want := (ResultTallies{Insufficient: 1, Unresolved: 1}); got.Tallies[req] != want {
		t.Fatalf("tallies %+v, want %+v", got.Tallies[req], want)
	}
	if got.Sufficiency[req] != "GAP" || got.Verdict[req] != "not_evaluable" {
		t.Fatalf("amount is %s/%s, want GAP/not_evaluable", got.Sufficiency[req], got.Verdict[req])
	}
	if want := (ResultTallies{Matched: 2}); got.Tallies["currency-commitment-matched"] != want {
		t.Fatalf("currency tallies %+v", got.Tallies["currency-commitment-matched"])
	}
	claim := got.Result.Claims[0]
	if claim.Presentation.Status != "INSUFFICIENT" || len(claim.Evidence) != 4 || claim.Reconcile.Tallies != got.Tallies[req] {
		t.Fatalf("claim %+v", claim)
	}
	if got.Result.Aggregate.Coverage.UnknownCount != 0 || !slices.Equal(got.Result.Aggregate.Buckets.NotEvaluable, []string{"reconcile-amount"}) || !slices.Equal(got.Result.Aggregate.Buckets.Met, []string{"reconcile-currency"}) {
		t.Fatalf("aggregate %+v", got.Result.Aggregate)
	}
	if got.DealState["deal-1"] != "not_reconciled" || got.DealState["deal-2"] != "not_reconciled" {
		t.Fatalf("deal state %v", got.DealState)
	}
}

// A period holding no half has nothing for a claim to cite (RES §1, DR
// §7.3): the reconcile is refused, with no Result.
func TestDealReconcileEmptyPopulation(t *testing.T) {
	in := pairedInput(t)
	in.Period = ResultPeriod{Start: "2026-09-01T00:00:00Z", End: "2026-10-01T00:00:00Z"}
	got, err := RecomputeDealReconcile(in)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no half") {
		t.Fatalf("err = %v, want ErrInvalid naming no half", err)
	}
	if len(got.Result.Claims) != 0 {
		t.Fatalf("a refused reconcile made claims %+v", got.Result.Claims)
	}
	// Only side B's half in the period still reconciles: one half is cited.
	in.Period = ResultPeriod{Start: "2026-10-15T00:00:01Z", End: "2026-10-16T00:00:00Z"}
	if got, err = RecomputeDealReconcile(in); err != nil {
		t.Fatal(err)
	}
	for _, claim := range got.Result.Claims {
		if len(claim.Evidence) != 1 {
			t.Fatalf("claim %s cites %d halves, want 1", claim.ID, len(claim.Evidence))
		}
	}
}

// A half recorded before the period's start is not in the reconcile.
func TestDealReconcileHalfBeforeTheStart(t *testing.T) {
	in := pairedInput(t)
	in.Period.Start = "2026-10-15T00:00:01Z"
	got, err := RecomputeDealReconcile(in)
	if err != nil {
		t.Fatal(err)
	}
	if state := got.Pairings["amount-commitment-matched"]["deal-1"]; state != Insufficient {
		t.Fatalf("with side A's half before the start, amount is %s, want INSUFFICIENT (side A NOT_SHOWN)", state)
	}
	if len(got.Result.Claims[0].Evidence) != 1 {
		t.Fatalf("evidence %v", got.Result.Claims[0].Evidence)
	}
}

// A carried opening whose nonce is not 64 lowercase hex does not open its
// commitment: FAIL, and the pairing is UNRESOLVED (rule 4).
func TestDealReconcileMalformedNonceFails(t *testing.T) {
	in := pairedInput(t)
	b := digestBytes(in.Halves[1].Octets)
	o := in.FieldOpenings[b]["amount"]
	o.Nonce = strings.ToUpper(o.Nonce)
	in.FieldOpenings[b]["amount"] = o
	got, err := RecomputeDealReconcile(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.FieldOpenings[b]["amount"] != "FAIL" || got.Pairings["amount-commitment-matched"]["deal-1"] != Unresolved {
		t.Fatalf("opening %s, pairing %s", got.FieldOpenings[b]["amount"], got.Pairings["amount-commitment-matched"]["deal-1"])
	}
}

// §6.1 cases the vectors leave out: a stream_interval whose rule is not
// all_records, and INCONCLUSIVE beside a failing property, are NOT_SHOWN;
// INCONCLUSIVE alone is UNDECIDED; and B_ONLY over an UNDECIDED side A is
// UNRESOLVED.
func TestDealReconcileSetStatusCases(t *testing.T) {
	period := ResultPeriod{Start: "2026-10-01T00:00:00Z", End: "2026-11-01T00:00:00Z"}
	stream := func(rule string, properties map[string]string) map[string]CoverageGiven {
		return map[string]CoverageGiven{"book-a": {Period: period, Declaration: map[string]any{"kind": "stream_interval", "rule": rule}, Properties: properties}}
	}
	pass := map[string]string{"coverage_declaration_form": "PASS", "coverage_declaration_anchor": "PASS", "coverage_declaration_population": "PASS", "coverage_declaration_commitment": "INCONCLUSIVE"}
	failing := map[string]string{"coverage_declaration_form": "PASS", "coverage_declaration_anchor": "FAIL", "coverage_declaration_population": "PASS", "coverage_declaration_commitment": "INCONCLUSIVE"}
	for name, c := range map[string]struct {
		coverage map[string]CoverageGiven
		status   string
		state    ExchangeState
	}{
		"rule not all_records":     {stream("holder_choice", pass), SetNotShown, Insufficient},
		"inconclusive with a FAIL": {stream("all_records", failing), SetNotShown, Insufficient},
		"inconclusive alone":       {stream("all_records", pass), SetUndecided, Unresolved},
	} {
		t.Run(name, func(t *testing.T) {
			in := pairedInput(t)
			in.Halves = in.Halves[1:]
			in.Coverage = c.coverage
			got, err := RecomputeDealReconcile(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.SetCompleteness["book-a"].Status != c.status || got.SetCompleteness["book-a"].Kind == nil || *got.SetCompleteness["book-a"].Kind != "stream_interval" {
				t.Fatalf("set %+v", got.SetCompleteness["book-a"])
			}
			if state := got.Pairings["amount-commitment-matched"]["deal-1"]; state != c.state {
				t.Fatalf("side B's lone half is %s, want %s", state, c.state)
			}
		})
	}
}

// A malformed half is refused even when the period or the books would leave
// it out: every half given is read through its book's mapping first.
func TestDealReconcileRefusesAMalformedHalfOutOfScope(t *testing.T) {
	in := pairedInput(t)
	in.HalfMappings["book-c"] = in.HalfMappings["book-b"]
	in.Halves = append(in.Halves, DealHalf{Book: "book-c", Octets: []byte(`{"book":"book-c","recorded_at":"2026-12-01T00:00:00Z"}`)})
	if _, err := RecomputeDealReconcile(in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}
