// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	for _, pointer := range []string{"/x", "/a~1b/c~0d/2", "/a~1b/c~0d/01", "/a~1b/c~0d/-"} {
		if _, found, err := resolvePointer(doc, pointer); err != nil || found {
			t.Fatalf("%s: found %v, err %v; want absent", pointer, found, err)
		}
	}
	for _, pointer := range []string{"a", "/a~2b"} {
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
