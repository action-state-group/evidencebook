// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The agent-action-capsule commit whose vectors/personal-history-basic-v0/
// this command reproduces, and the SHA-256 of the files the guide relies on
// there. testdata/vectors.json is that commit's vectors.json, unchanged.
//
// producerSHA256 is this repository's main.go. It differs from that commit's
// producer/main.go (SHA-256 e9054656...f7c38e) where the book's payload store
// became its artifact store (Config.Originals, capsule-cli#236), which the
// corpus's evidencebook commit predates, and in its -correlated mode (#82),
// which leaves the default run unchanged. producer.diff is the whole difference, and CI
// checks it against the corpus. When the corpus next pins evidencebook, its
// producer/main.go takes this file.
const (
	aacCommit      = "9d653f41419d2d2bac4714137b763315947ec754"
	vectorsSHA256  = "f1f0e1b95c7dd32f2ddc584d4c55bc9a643a58f550a941651bfd5d3e4c59a4b9"
	producerSHA256 = "99e9f3e7e43de6d879c7730155aae8f14f081c0f2e528e5a21cfda1da0f9ace9"
)

var (
	runOnce  sync.Once
	runBytes []byte
	runErr   error
)

// produce runs the command once, as `go run` would, and returns its stdout.
func produce(t *testing.T) []byte {
	t.Helper()
	runOnce.Do(func() { runBytes, runErr = capture(false) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	return runBytes
}

// capture runs the command, with -correlated or without, and returns its stdout.
func capture(correlated bool) ([]byte, error) {
	f, err := os.CreateTemp("", "personal-history-run-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	stdout := os.Stdout
	os.Stdout = f
	err = run(context.Background(), correlated)
	os.Stdout = stdout
	if err != nil {
		return nil, err
	}
	return os.ReadFile(f.Name())
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readFile(t *testing.T, path ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type octetsText struct {
	MediaType  string `json:"media_type"`
	OctetsText string `json:"octets_text"`
}

type row struct {
	Row         string `json:"row"`
	Status      string `json:"status"`
	Sufficiency string `json:"sufficiency"`
	Verdict     string `json:"verdict"`
}

type claim struct {
	Evidence []json.RawMessage `json:"evidence"`
	Proofs   []json.RawMessage `json:"proofs"`
}

type expectedExchange struct {
	MessageType  string            `json:"message_type"`
	Properties   map[string]string `json:"properties"`
	Accepted     bool              `json:"accepted"`
	Requirements map[string]row    `json:"requirements"`
	Claim        claim             `json:"claim"`
}

type vectorCase struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Mode   string `json:"mode"`
	Pins   struct {
		TrustPolicy struct {
			ResponderKeys []string `json:"responder_keys"`
			LogKeys       []logKey `json:"log_keys"`
		} `json:"trust_policy"`
		VerificationPolicy struct {
			Online              bool     `json:"online"`
			ProfilesImplemented []string `json:"profiles_implemented"`
			KindsImplemented    []string `json:"kinds_implemented"`
		} `json:"verification_policy"`
		ContractRef    string `json:"contract_ref"`
		ContractDigest string `json:"contract_digest"`
		EvaluationTime string `json:"evaluation_time"`
	} `json:"pins"`
	Inputs struct {
		Definition json.RawMessage `json:"definition"`
		Exchanges  []struct {
			Request octetsText `json:"request"`
			Reply   octetsText `json:"reply"`
		} `json:"exchanges"`
		Requester string     `json:"requester"`
		Request   octetsText `json:"request"`
		Reply     octetsText `json:"reply"`
	} `json:"inputs"`
	Expected struct {
		// A History case.
		Exchanges         []expectedExchange `json:"exchanges"`
		HistoryProperties map[string]string  `json:"history_properties"`
		Accepted          bool               `json:"accepted"`
		Fails             []string           `json:"fails"`
		DealStates        *struct {
			SelectedDeals int            `json:"selected_deals"`
			Counts        map[string]int `json:"counts"`
		} `json:"deal_states"`
		// The refusal case.
		MessageType  string            `json:"message_type"`
		Reason       string            `json:"reason"`
		Properties   map[string]string `json:"properties"`
		Requirements map[string]row    `json:"requirements"`
	} `json:"expected"`
}

type corpus struct {
	History   string `json:"history"`
	Responder struct {
		Commit    string `json:"commit"`
		RunSHA256 string `json:"run_sha256"`
	} `json:"responder"`
	TestKeys struct {
		Keys map[string]struct {
			Public string `json:"public"`
		} `json:"keys"`
	} `json:"test_keys"`
	Contract struct {
		ContractRef    string `json:"contract_ref"`
		ContractDigest string `json:"contract_digest"`
		Document       struct {
			Requirements []struct {
				ID string `json:"id"`
			} `json:"requirements"`
		} `json:"document"`
	} `json:"contract"`
	HistoryProperties []string     `json:"history_properties"`
	Count             int          `json:"count"`
	Cases             []vectorCase `json:"cases"`
}

func loadCorpus(t *testing.T) (corpus, []byte) {
	t.Helper()
	data := readFile(t, "testdata", "vectors.json")
	if got := sha(data); got != vectorsSHA256 {
		t.Fatalf("testdata/vectors.json has SHA-256 %s, not the pinned corpus's %s", got, vectorsSHA256)
	}
	var c corpus
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c, data
}

func loadRun(t *testing.T) output {
	t.Helper()
	var out output
	if err := json.Unmarshal(produce(t), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRunIsThePublishedRun: this command, against this commit's Respond,
// writes the corpus's responder-run.json byte for byte, on every run.
func TestRunIsThePublishedRun(t *testing.T) {
	c, _ := loadCorpus(t)
	first := produce(t)
	if got := sha(first); got != c.Responder.RunSHA256 {
		t.Fatalf("the run has SHA-256 %s, not the corpus's run_sha256 %s: Respond's output drifted from the published vectors", got, c.Responder.RunSHA256)
	}
	again, err := capture(false)
	if err != nil {
		t.Fatal(err)
	}
	if sha(again) != sha(first) {
		t.Fatal("two runs differ")
	}
}

// TestProducerIsTheCorpusProducer: main.go is the pinned producer (see
// producerSHA256), so any change to it is deliberate. Its header comment
// still describes the corpus README's copy step.
func TestProducerIsTheCorpusProducer(t *testing.T) {
	if got := sha(readFile(t, "main.go")); got != producerSHA256 {
		t.Fatalf("main.go has SHA-256 %s, not the pinned %s: update producerSHA256 and producer.diff together", got, producerSHA256)
	}
}

// TestCorpusIsCutFromTheRun: every case the corpus marks as the responder's
// carries this run's octets unchanged, and its definition is one the run
// registered.
func TestCorpusIsCutFromTheRun(t *testing.T) {
	c, _ := loadCorpus(t)
	out := loadRun(t)
	carried := map[[2]string]bool{}
	for _, e := range out.Exchanges {
		carried[[2]string{e.Request.OctetsText, e.Reply.OctetsText}] = true
	}
	definitions := map[string]bool{}
	for _, d := range out.Definitions {
		definitions[d] = true
	}
	responder := 0
	for _, vc := range c.Cases {
		if vc.Source != "responder" {
			if !strings.HasPrefix(vc.Source, "derived: ") {
				t.Errorf("%s: source %q", vc.ID, vc.Source)
			}
			continue
		}
		responder++
		pairs := [][2]string{}
		for _, x := range vc.Inputs.Exchanges {
			pairs = append(pairs, [2]string{x.Request.OctetsText, x.Reply.OctetsText})
		}
		if vc.Inputs.Reply.OctetsText != "" {
			pairs = append(pairs, [2]string{vc.Inputs.Request.OctetsText, vc.Inputs.Reply.OctetsText})
		}
		for _, p := range pairs {
			if !carried[p] {
				t.Errorf("%s: an exchange is not one of the run's", vc.ID)
			}
		}
		if len(vc.Inputs.Definition) > 0 {
			var d any
			if err := json.Unmarshal(vc.Inputs.Definition, &d); err != nil {
				t.Fatal(err)
			}
			if !definitions[jcsOf(t, d)] {
				t.Errorf("%s: the definition is not one the run registered", vc.ID)
			}
		}
	}
	if c.Count != 14 || len(c.Cases) != 14 || responder != 5 {
		t.Errorf("corpus: %d cases (count %d), %d from the responder", len(c.Cases), c.Count, responder)
	}
}

// jcsOf re-encodes a decoded definition the way the producer does: its
// members are strings and arrays of strings, so sorted keys and Go's
// escaping-free encoding give JCS.
func jcsOf(t *testing.T, v any) string {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// The envelope properties, in the interop profile's §7.2 order, as the
// corpus lists them.
var envelopeProperties = []string{
	"profile_version", "envelope_signature", "responder_trust", "responder_key_current", "issued_at_form",
	"request_binding", "subject_binding", "artifact_digest", "verification_binding", "anchor_match", "anchor_inclusion",
}

func code(s string) string { return "`" + s + "`" }

// listed joins code spans as prose: "`a`", "`a` and `b`", "`a`, `b` and `c`".
func listed(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = code(x)
	}
	if len(q) < 2 {
		return strings.Join(q, "")
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

func rowText(r row) string {
	return fmt.Sprintf("%s: %s, %s, %s", code(r.Row), code(r.Status), code(r.Sufficiency), code(r.Verdict))
}

// guideLine returns the one guide line that starts with prefix.
func guideLine(t *testing.T, guide, prefix string) string {
	t.Helper()
	var found []string
	for _, l := range strings.Split(guide, "\n") {
		if strings.HasPrefix(l, prefix) {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Errorf("the guide has %d lines starting %q, want 1", len(found), prefix)
		return ""
	}
	return found[0]
}

// TestGuideQuotesTheCorpus: every pin, key, digest and specified result that
// §6 of the third-party guide quotes is the pinned corpus's, and the corpus
// itself has the shape the guide describes.
func TestGuideQuotesTheCorpus(t *testing.T) {
	guideBytes := readFile(t, "..", "..", "..", "docs", "THIRD-PARTY-VERIFIER.md")
	guide := string(guideBytes)
	// Quotes are matched across line breaks; table rows line by line.
	flat := strings.Join(strings.Fields(guide), " ")
	c, _ := loadCorpus(t)
	out := loadRun(t)
	quote := func(what, s string) {
		t.Helper()
		if !strings.Contains(flat, s) {
			t.Errorf("the guide does not quote %s: %s", what, s)
		}
	}

	// §6.1 and §6.2: where the inputs come from, and the run.
	quote("the corpus commit", "AAC="+aacCommit)
	quote("the corpus commit", code(aacCommit))
	quote("the vectors' digest", code(vectorsSHA256))
	quote("the responder commit", code(c.Responder.Commit))
	quote("the run digest", code(c.Responder.RunSHA256))
	quote("the case count", fmt.Sprintf("`vectors.json`, %d cases", len(c.Cases)))
	quote("the History name", code(c.History))
	quote("the producer's directory", "go run ./test/interop/personal-history > run/responder-run.json")
	if len(out.Deals) != 4 || len(out.Selection) != 3 || len(out.Records) != 8 || len(out.Checkpoints) != 2 {
		t.Errorf("the run is not four deals, three selected, eight records and two checkpoints")
	}
	quote("the stream's shape", "four deals, three selected, eight records")

	// §6.3: the pins every case carries.
	holder := c.TestKeys.Keys["holder"].Public
	if holder != out.HolderKey {
		t.Errorf("the corpus's holder key %s is not the run's %s", holder, out.HolderKey)
	}
	quote("the holder key", code(holder))
	quote("the second holder key", code(c.TestKeys.Keys["second-holder"].Public))
	quote("the holder log key", code(c.TestKeys.Keys["holder-log"].Public))
	quote("the recipient", code(out.Recipient))
	quote("the contract", code(c.Contract.ContractRef))
	quote("the contract digest", code(c.Contract.ContractDigest))
	if len(c.Contract.Document.Requirements) != 1 {
		t.Errorf("the contract has %d requirements", len(c.Contract.Document.Requirements))
	}
	requirement := c.Contract.Document.Requirements[0].ID
	quote("the requirement", code(requirement))
	for i, cp := range out.Checkpoints {
		if cp.LogKey.Kid != c.TestKeys.Keys["holder-log"].Public {
			t.Errorf("checkpoint %d is signed by %s, not the holder log key", i+1, cp.LogKey.Kid)
		}
		quote("a checkpoint's iss", code(cp.LogKey.Iss))
		quote("a checkpoint's sub", code(cp.LogKey.Sub))
	}
	for _, vc := range c.Cases {
		p := vc.Pins
		keys := []string{holder}
		if vc.ID == "mutant-second-trusted-key" {
			keys = append(keys, c.TestKeys.Keys["second-holder"].Public)
		}
		if !slices.Equal(p.TrustPolicy.ResponderKeys, keys) {
			t.Errorf("%s: responder keys %v, the guide says %v", vc.ID, p.TrustPolicy.ResponderKeys, keys)
		}
		if len(p.TrustPolicy.LogKeys) != len(out.Checkpoints) {
			t.Errorf("%s: %d log keys", vc.ID, len(p.TrustPolicy.LogKeys))
		}
		for i, lk := range p.TrustPolicy.LogKeys {
			if lk != out.Checkpoints[i].LogKey {
				t.Errorf("%s: log key %d is %+v, not the run's %+v", vc.ID, i+1, lk, out.Checkpoints[i].LogKey)
			}
		}
		vp := p.VerificationPolicy
		if vp.Online || !slices.Equal(vp.ProfilesImplemented, []string{profile}) || len(vp.KindsImplemented) != 3 {
			t.Errorf("%s: verification policy %+v", vc.ID, vp)
		}
		if p.ContractRef != c.Contract.ContractRef || p.ContractDigest != c.Contract.ContractDigest {
			t.Errorf("%s: contract %s %s", vc.ID, p.ContractRef, p.ContractDigest)
		}
		quote("an evaluation time", code(p.EvaluationTime))
		if vc.Mode != "asked" {
			t.Errorf("%s: mode %s, the guide says asked", vc.ID, vc.Mode)
		}
	}
	quote("the mode", "`mode` `asked`")

	// §6.3: what every accepted and every rejected envelope has.
	var accepted map[string]string
	var acceptedRow, rejectedRow *row
	for _, vc := range c.Cases {
		for _, x := range vc.Expected.Exchanges {
			r := x.Requirements[requirement]
			if x.Accepted {
				if accepted == nil {
					accepted = x.Properties
					acceptedRow = &r
				}
				if !mapsEqual(x.Properties, accepted) || r != *acceptedRow || len(x.Claim.Proofs) != 0 {
					t.Errorf("%s: an accepted envelope differs from the others", vc.ID)
				}
			} else {
				if rejectedRow == nil {
					rejectedRow = &r
				}
				if r != *rejectedRow || len(differing(x.Properties, accepted)) != 1 {
					t.Errorf("%s: a rejected envelope is not one FAIL with the common row", vc.ID)
				}
			}
			if r.Verdict == "met" || r.Verdict == "not_met" {
				t.Errorf("%s: verdict %s", vc.ID, r.Verdict)
			}
		}
	}
	keys := make([]string, 0, len(accepted))
	for k := range accepted {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	ordered := slices.Clone(envelopeProperties)
	slices.Sort(ordered)
	if !slices.Equal(keys, ordered) {
		t.Fatalf("the corpus's envelope properties %v are not %v", keys, envelopeProperties)
	}
	byStatus := map[string][]string{}
	for _, k := range envelopeProperties {
		byStatus[accepted[k]] = append(byStatus[accepted[k]], k)
	}
	if len(byStatus) != 3 || len(byStatus["NOT_PRESENT"]) != 1 {
		t.Fatalf("accepted envelope statuses %v", byStatus)
	}
	quote("the accepted envelope's statuses", fmt.Sprintf("In every case, an accepted envelope has %s `PASS`; %s `NOT_CHECKED`; and %s `NOT_PRESENT`.",
		listed(byStatus["PASS"]), listed(byStatus["NOT_CHECKED"]), listed(byStatus["NOT_PRESENT"])))
	quote("the accepted envelope's row", fmt.Sprintf("Its row for %s is %s, and its `claim.proofs` is empty.", code(requirement), rowText(*acceptedRow)))
	quote("the rejected envelope's row", fmt.Sprintf("its row is %s.", rowText(*rejectedRow)))

	// §6.3: one table row per case, its result cells generated from the case.
	if len(c.HistoryProperties) != 7 {
		t.Errorf("%d History properties", len(c.HistoryProperties))
	}
	for _, vc := range c.Cases {
		source := "responder"
		if vc.Source != "responder" {
			source = "derived"
		}
		line := guideLine(t, guide, "| "+code(vc.ID)+" | "+source+" | ")
		if line == "" {
			continue
		}
		if vc.Expected.Exchanges == nil {
			e := vc.Expected
			r := e.Requirements[requirement]
			var notPass []string
			for _, k := range []string{"refusal_signature", "responder_trust", "responder_key_current", "request_binding", "reason_registered", "issued_at_form"} {
				if e.Properties[k] != "PASS" {
					notPass = append(notPass, k)
				}
			}
			if len(e.Properties) != 6 || len(notPass) != 1 {
				t.Errorf("%s: refusal properties %v", vc.ID, e.Properties)
				continue
			}
			want := fmt.Sprintf(" | no envelope | refusal %s; every refusal property `PASS` except %s %s; row %s | no History | no History |",
				code(e.Reason), code(notPass[0]), code(e.Properties[notPass[0]]), rowText(r))
			if vc.Inputs.Requester == out.Recipient || !strings.Contains(line, code(vc.Inputs.Requester)) {
				t.Errorf("the guide's row for %s does not name its requester %s, who is not the recipient", vc.ID, vc.Inputs.Requester)
			}
			if e.MessageType != "refusal" || !strings.HasSuffix(line, want) {
				t.Errorf("the guide's row for %s does not end %q:\n%s", vc.ID, want, line)
			}
			continue
		}
		e := vc.Expected
		n := 0
		var rejected []string
		for i, x := range e.Exchanges {
			if x.Accepted {
				n++
				continue
			}
			for _, k := range differing(x.Properties, accepted) {
				rejected = append(rejected, fmt.Sprintf("exchange %d: %s %s", i+1, code(k), code(x.Properties[k])))
			}
		}
		var failing []string
		for _, k := range c.HistoryProperties {
			if e.HistoryProperties[k] != "PASS" {
				failing = append(failing, code(k))
			}
		}
		for _, f := range e.Fails {
			if e.HistoryProperties[f] != "FAIL" && !slices.ContainsFunc(e.Exchanges, func(x expectedExchange) bool { return x.Properties[f] == "FAIL" }) {
				t.Errorf("%s: the property §6.4 names, %s, does not fail", vc.ID, f)
			}
		}
		history := "accepted"
		if !e.Accepted {
			history = "not accepted"
		}
		want := fmt.Sprintf(" | %d of %d | %s | %s | %s |", n, len(e.Exchanges), orNone(rejected), orNone(failing), history)
		if !strings.HasSuffix(line, want) {
			t.Errorf("the guide's row for %s does not end %q:\n%s", vc.ID, want, line)
		}
		if e.Accepted {
			d := e.DealStates
			if d == nil {
				t.Fatalf("%s: no deal states", vc.ID)
			}
			quote("the deal states", fmt.Sprintf("For `%s`, `deal_states` counts %d selected deals: %d with a recorded failed step, %d with recorded completed steps only, %d with no recorded step.",
				vc.ID, d.SelectedDeals, d.Counts["with a recorded failed step"], d.Counts["with recorded completed steps only"], d.Counts["with no recorded step"]))
			quote("the selected deals", fmt.Sprintf("selected deals, %d in `%s`", d.SelectedDeals, vc.ID))
		}
	}
	quote("the personal row of §6.4", fmt.Sprintf("%s of %s: row %s", code(requirement), code(c.Contract.ContractRef), strings.Replace(rowText(*acceptedRow), ":", ",", 1)))
	quote("the holder key in §6.4", "the holder key `"+holder[:8]+"…`")
	quote("the run's epistemic type", "(`producer_claim`)")
	for _, e := range out.Exchanges {
		if e.Name == "history-1" && !strings.Contains(decodedArtifact(t, e.Reply.OctetsText), `"epistemic_type":"producer_claim"`) {
			t.Error("history-1's records are not producer_claim")
		}
	}
}

func decodedArtifact(t *testing.T, reply string) string {
	t.Helper()
	var env struct {
		Artifact string `json:"artifact"`
	}
	if err := json.Unmarshal([]byte(reply), &env); err != nil {
		t.Fatal(err)
	}
	b, err := base64.RawURLEncoding.DecodeString(env.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func differing(got, base map[string]string) []string {
	var out []string
	for _, k := range envelopeProperties {
		if got[k] != base[k] {
			out = append(out, k)
		}
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

// TestCorrelatedRunAnswersTheSameRecords: filed under correlation.exchange_id
// with subject_ref <deal>/<n>, as capsule-cli#264 files steps, every request
// is answered with the same records as the corpus run, which Respond can
// answer only through the subject_ref fallback. The record digests, and so
// the anchors and replies, differ; sequence numbers and the projected fields
// do not.
func TestCorrelatedRunAnswersTheSameRecords(t *testing.T) {
	base := loadRun(t)
	data, err := capture(true)
	if err != nil {
		t.Fatal(err)
	}
	var correlated output
	if err := json.Unmarshal(data, &correlated); err != nil {
		t.Fatal(err)
	}
	for _, r := range base.Records {
		if r.SubjectRef != "" {
			t.Errorf("the corpus run carries subject_ref %q", r.SubjectRef)
		}
	}
	numbered := map[string]int{}
	for i, r := range correlated.Records {
		numbered[r.Deal]++
		if want := fmt.Sprintf("%s/%d", r.Deal, numbered[r.Deal]); r.SubjectRef != want {
			t.Errorf("record %d: subject_ref %q, want %q", i, r.SubjectRef, want)
		}
		if r.Digest == base.Records[i].Digest {
			t.Errorf("record %d: the digest is the corpus run's, so the header did not change", i)
		}
	}
	if len(correlated.Exchanges) != len(base.Exchanges) {
		t.Fatalf("%d exchanges, the corpus run has %d", len(correlated.Exchanges), len(base.Exchanges))
	}
	for i, e := range correlated.Exchanges {
		b := base.Exchanges[i]
		if e.Name != b.Name || e.Reply.MediaType != b.Reply.MediaType {
			t.Fatalf("exchange %d: %s (%s), the corpus run has %s (%s)", i, e.Name, e.Reply.MediaType, b.Name, b.Reply.MediaType)
		}
		got, want := answered(t, e.Reply.OctetsText), answered(t, b.Reply.OctetsText)
		if len(want) == 0 {
			t.Fatalf("%s: the corpus run answers no record", b.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: records\n%s\nwant\n%s", e.Name, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// answered is a reply's records without their digests, as JSON, or its
// refusal reason when it is not an envelope.
func answered(t *testing.T, reply string) []string {
	t.Helper()
	var env struct {
		Artifact string `json:"artifact"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(reply), &env); err != nil {
		t.Fatal(err)
	}
	if env.Artifact == "" {
		return []string{"refused: " + env.Reason}
	}
	var artifact struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal([]byte(decodedArtifact(t, reply)), &artifact); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range artifact.Records {
		delete(r, "digest")
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}
