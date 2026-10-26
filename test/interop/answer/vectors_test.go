// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The agent-action-capsule commit the whole third-party guide pins, and its
// vectors/interop-profile-v0/vectors.json, vendored unchanged in testdata.
const (
	aacCommit         = "9d653f41419d2d2bac4714137b763315947ec754"
	regeneratedCommit = "e706487d37561a7e7aae5c169d7c45e76305973b"
	vectorsSHA256     = "5dc5078642df32969dd91ab57371fd4a8f1900b1505ec55e77b44dabc509b10b"
)

type ipRow struct {
	Row         string `json:"row"`
	Status      string `json:"status"`
	Sufficiency string `json:"sufficiency"`
	Verdict     string `json:"verdict"`
}

type ipReply struct {
	MessageType              string            `json:"message_type"`
	Properties               map[string]string `json:"properties"`
	Requirements             map[string]ipRow  `json:"requirements"`
	PresentationKindsAllowed []string          `json:"presentation_kinds_allowed"`
}

type ipCase struct {
	ID       string `json:"id"`
	Expected struct {
		Replies       []ipReply         `json:"replies"`
		SetProperties map[string]string `json:"set_properties"`
		Absence       *struct {
			Form                        string           `json:"form"`
			Requirements                map[string]ipRow `json:"requirements"`
			Claim                       json.RawMessage  `json:"claim"`
			PresentationKindRecommended string           `json:"presentation_kind_recommended"`
		} `json:"absence"`
	} `json:"expected"`
}

func loadIPVectors(t *testing.T) map[string]ipCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "interop-profile-v0-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != vectorsSHA256 {
		t.Fatalf("testdata/interop-profile-v0-vectors.json is not the pinned vectors.json (%x)", sum)
	}
	var v struct {
		Cases []ipCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) != 53 {
		t.Fatalf("%d cases", len(v.Cases))
	}
	out := map[string]ipCase{}
	for _, c := range v.Cases {
		out[c.ID] = c
	}
	return out
}

func tick(s string) string { return "`" + s + "`" }

func rowOf(r ipRow) string {
	return fmt.Sprintf("row %s: %s, %s, %s", tick(r.Row), tick(r.Status), tick(r.Sufficiency), tick(r.Verdict))
}

// TestGuideQuotesTheVectors: the guide pins one agent-action-capsule commit,
// and every case and specified result its §5 table quotes is that commit's
// vectors': every case of the seven classes is routed somewhere, and the
// rows, statuses and forms the table states are the cases' expected ones.
func TestGuideQuotesTheVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "THIRD-PARTY-VERIFIER.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(data)
	cases := loadIPVectors(t)

	// One pin for the whole guide.
	pins := regexp.MustCompile(`(?m)^AAC=(\S+)$`).FindAllStringSubmatch(guide, -1)
	if len(pins) != 2 {
		t.Errorf("the guide sets AAC %d times, want 2 (§1 and §6.1)", len(pins))
	}
	for _, p := range pins {
		if p[1] != aacCommit {
			t.Errorf("the guide pins AAC=%s, not %s", p[1], aacCommit)
		}
	}
	for _, q := range []string{tick(aacCommit), tick(regeneratedCommit), fmt.Sprintf("The vectors at this commit hold %d cases.", len(cases))} {
		if !strings.Contains(guide, q) {
			t.Errorf("the guide does not quote %s", q)
		}
	}
	for _, sha := range regexp.MustCompile("`([0-9a-f]{40})`").FindAllStringSubmatch(guide, -1) {
		if sha[1] != aacCommit && sha[1] != regeneratedCommit && sha[1] != "43f3cb1b955d647bc92cb4da0d823973d1981cad" {
			t.Errorf("the guide names another commit: %s", sha[1])
		}
	}

	// The §5 table: its lines, and the case ids it routes.
	section := guide[strings.Index(guide, "## 5. "):strings.Index(guide, "## 6. ")]
	var table []string
	for _, l := range strings.Split(section, "\n") {
		if strings.HasPrefix(l, "| ") && !strings.HasPrefix(l, "| Class |") {
			table = append(table, l)
		}
	}
	idPattern := regexp.MustCompile("`([a-z][A-Za-z0-9_-]*)`")
	quoted := map[string]bool{}
	for _, l := range table {
		for _, m := range idPattern.FindAllStringSubmatch(l, -1) {
			if _, ok := cases[m[1]]; ok {
				quoted[m[1]] = true
			}
		}
	}
	// The table names the backdated-acceptance run as a range; every other
	// case of the seven classes is named by id.
	for id := range cases {
		outside := strings.HasPrefix(id, "foreign-native-check-") || strings.HasPrefix(id, "lifecycle-") || strings.HasPrefix(id, "carriage-")
		if !outside && !strings.HasPrefix(id, "acceptance-backdated-") && !quoted[id] {
			t.Errorf("the §5 table does not route vector case %s", id)
		}
		if outside && quoted[id] {
			t.Errorf("the §5 table routes %s, a case outside the seven classes", id)
		}
	}
	line := func(id string) string {
		t.Helper()
		var found []string
		for _, l := range table {
			if strings.Contains(l, tick(id)) {
				found = append(found, l)
			}
		}
		if len(found) != 1 {
			t.Fatalf("%d §5 table lines name %s", len(found), id)
		}
		return found[0]
	}
	has := func(id, fragment string) {
		t.Helper()
		if l := line(id); !strings.Contains(l, fragment) {
			t.Errorf("the §5 row for %s does not say %q:\n%s", id, fragment, l)
		}
	}
	reply := func(id string) ipReply {
		t.Helper()
		r := cases[id].Expected.Replies
		if len(r) != 1 {
			t.Fatalf("%s: %d replies", id, len(r))
		}
		return r[0]
	}

	// Positive and invariance.
	has("valid-exchange", rowOf(reply("valid-exchange").Requirements["r-record"]))
	inv := cases["invariance-two-nonces-two-transports"].Expected
	for _, r := range inv.Replies {
		if r.Requirements["r-record"] != reply("valid-exchange").Requirements["r-record"] {
			t.Error("an invariance reply is not as for the positive class")
		}
	}
	has("invariance-two-nonces-two-transports", "`caller_invariance` "+tick(inv.SetProperties["caller_invariance"]))

	// Tampered: the eight envelope cases and the two forged refusals.
	tampered := line("tampered-artifact")
	envelopeRow, refusalRow := "", ""
	for _, m := range idPattern.FindAllStringSubmatch(strings.Split(tampered, " | ")[1], -1) {
		r := reply(m[1])
		fails := 0
		for _, s := range r.Properties {
			if s == "FAIL" {
				fails++
			}
		}
		if fails != 1 {
			t.Errorf("%s: %d FAIL properties", m[1], fails)
		}
		rr := r.Requirements["r-record"]
		got := fmt.Sprintf("%s (%s)", tick(rr.Row), tick(rr.Status))
		want := &envelopeRow
		if r.MessageType == "refusal" {
			want = &refusalRow
		}
		if *want == "" {
			*want = got
		} else if *want != got {
			t.Errorf("%s: row %s, not %s", m[1], got, *want)
		}
	}
	has("tampered-artifact", "row "+envelopeRow+" for the eight envelope cases and "+refusalRow+" for the two forged refusals")

	// Declined, missing evidence, unsupported version.
	declined := reply("declined")
	has("declined", rowOf(declined.Requirements["r-record"])+", with presentation "+tick(declined.PresentationKindsAllowed[0])+" or "+tick(declined.PresentationKindsAllowed[1]))
	has("missing-evidence-no-such-subject", rowOf(reply("missing-evidence-no-such-subject").Requirements["r-record"]))
	has("unsupported-version-refused", rowOf(reply("unsupported-version-refused").Requirements["r-record"]))
	var versions []string
	for _, id := range []string{"unsupported-version-mismatch", "unsupported-version-absent", "unsupported-version-unimplemented"} {
		versions = append(versions, tick(reply(id).Properties["profile_version"]))
	}
	has("unsupported-version-mismatch", fmt.Sprintf("`profile_version` %s, %s or %s respectively", versions[0], versions[1], versions[2]))

	absence := cases["missing-evidence-recorded-absence"].Expected.Absence
	has("missing-evidence-recorded-absence", fmt.Sprintf("absence form %s; %s; presentation %s",
		tick(absence.Form), rowOf(absence.Requirements["r-record"]), tick(absence.PresentationKindRecommended)))
	signed := cases["missing-evidence-recorded-absence-signed"].Expected.Absence
	if signed.Form != absence.Form || signed.Requirements["r-record"] != absence.Requirements["r-record"] {
		t.Error("the signed absence record is not specified as the unsigned one")
	}
	has("missing-evidence-recorded-absence-signed", "the signed record: form "+tick(signed.Form)+", and the same row `absence` as the unsigned one")
	for _, id := range []string{"missing-evidence-recorded-absence-key-id-unsigned", "missing-evidence-recorded-absence-key-id-without-sig", "missing-evidence-recorded-absence-sig-without-key-id"} {
		a := cases[id].Expected.Absence
		if a == nil || len(a.Requirements) != 0 || len(a.Claim) != 0 {
			t.Errorf("%s: specified with a requirement row or a claim", id)
			continue
		}
		has(id, "form "+tick(a.Form)+", with no requirement row and no claim")
	}

	// Backdated acceptance: the ends of the range the table names are cases.
	for _, id := range []string{"acceptance-backdated-A-no-ordering-evidence", "acceptance-backdated-G-policy-requires-other-party", "acceptance-after-action-D"} {
		if _, ok := cases[id]; !ok || !strings.Contains(line("acceptance-absent"), tick(id)) {
			t.Errorf("the backdated-acceptance row does not name vector case %s", id)
		}
	}
}
