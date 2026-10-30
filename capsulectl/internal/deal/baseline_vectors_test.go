package deal

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The baseline vectors freeze what today's builders make of real deals, so
// the move of the x-deal-v0 profile into evidencebook (evidencebook#93) can
// show it changes no byte: for each step, the record's payload and digest,
// the step's Capsule as sealed before any book header is added, and the
// header fields the book commits; for each sealed report, its payload and
// Capsule. Each vector also carries the inputs today's builders took (the
// deal's local events, its deal key and the profile's signing seed, all
// test-only values), so this test re-derives every output from them.
// The vectors moved with the profile and are EvidenceBook's
// (dealprofile/testdata/baseline-vectors.json), whose own tests check them
// too. DEAL_WRITE_BASELINE_VECTORS=1 writes the flows below to
// testdata/baseline-vectors.json, for a deliberate re-freeze there.
const baselineVectorsOut = "testdata/baseline-vectors.json"

var baselineVectorsPath = filepath.Join(dealprofileDir, "testdata", "baseline-vectors.json")

type baselineVectors struct {
	Note  string         `json:"note"`
	Deals []baselineDeal `json:"deals"`
}

type baselineDeal struct {
	Flow        string           `json:"flow"`
	DealID      string           `json:"deal_id"`
	DealKey     string           `json:"deal_key"`
	SigningSeed string           `json:"signing_seed"`
	Operator    string           `json:"operator"`
	Events      []baselineEvent  `json:"events"`
	Steps       []baselineStep   `json:"steps"`
	Reports     []baselineReport `json:"reports,omitempty"`
}

// baselineEvent is a sealed step as the deal store holds it: its local
// event (the builders' input) and what it was sealed as.
type baselineEvent struct {
	CapsuleID string          `json:"capsule_id"`
	Digest    string          `json:"digest"`
	Sequence  uint64          `json:"sequence"`
	Event     json.RawMessage `json:"event"`
}

type baselineStep struct {
	N         int64          `json:"n"`
	Payload   string         `json:"payload"`
	Digest    string         `json:"digest"`
	CapsuleID string         `json:"capsule_id"`
	Capsule   string         `json:"capsule"`
	Header    baselineHeader `json:"header"`
}

// baselineHeader is the evidence record header a step's book entry gives
// the book: everything but what the book itself assigns (its id, seq and the
// commitments it computes from the fields).
type baselineHeader struct {
	RecordType    string                   `json:"record_type"`
	EpistemicType string                   `json:"epistemic_type"`
	CommittedAt   string                   `json:"committed_at"`
	SubjectRef    string                   `json:"subject_ref"`
	Correlation   evidencebook.Correlation `json:"correlation"`
	Links         []evidencebook.Link      `json:"links"`
	Fields        map[string]string        `json:"fields"`
}

type baselineReport struct {
	Form       string `json:"form"`
	Timestamp  string `json:"timestamp"`
	AgentInput string `json:"agent_input"`
	Payload    string `json:"payload"`
	CapsuleID  string `json:"capsule_id"`
	Capsule    string `json:"capsule"`
}

func TestBaselineVectors(t *testing.T) {
	if os.Getenv("DEAL_WRITE_BASELINE_VECTORS") == "1" {
		writeBaselineVectors(t)
	}
	raw, err := os.ReadFile(baselineVectorsPath)
	require.NoError(t, err)
	var v baselineVectors
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.Deals)
	for _, d := range v.Deals {
		t.Run(d.Flow, func(t *testing.T) {
			s := baselineSession(t, d)
			events := make([]sealedEvent, 0, len(d.Events))
			for _, e := range d.Events {
				var ev dealEvent
				require.NoError(t, cli.DecodeJSON(e.Event, &ev))
				events = append(events, sealedEvent{CapsuleID: e.CapsuleID, Digest: e.Digest, Sequence: e.Sequence, Event: ev})
			}
			require.Len(t, d.Steps, len(events))
			for i, want := range d.Steps {
				got, err := baselineStepOf(s, events[:i], events[i].Event)
				require.NoError(t, err, "step %d", want.N)
				assert.Equal(t, want, got, "step %d", want.N)
			}
			for _, want := range d.Reports {
				at, err := time.Parse(time.RFC3339, want.Timestamp)
				require.NoError(t, err)
				got, err := baselineReportOf(s, want.Form, at, want.AgentInput)
				require.NoError(t, err)
				assert.Equal(t, want, got, "%s report", want.Form)
			}
		})
	}
}

// baselineSession is the part of a deal session the step and report
// builders read: the profile's name and signing key, and the deal's key.
func baselineSession(t *testing.T, d baselineDeal) *dealSession {
	t.Helper()
	seed, err := hex.DecodeString(d.SigningSeed)
	require.NoError(t, err)
	dkey, err := hex.DecodeString(d.DealKey)
	require.NoError(t, err)
	return &dealSession{p: cli.Profile{Name: d.Operator}, key: ed25519.NewKeyFromSeed(seed), dkey: dkey}
}

// baselineStepOf derives a step's vector as sealBookStep builds it, up to
// the book's own header extension.
func baselineStepOf(s *dealSession, events []sealedEvent, ev dealEvent) (baselineStep, error) {
	request, digest, err := s.stepRequest(events, ev, dealCapsuleCurrent)
	if err != nil {
		return baselineStep{}, err
	}
	record, err := cli.Seal(request, s.key)
	if err != nil {
		return baselineStep{}, err
	}
	at, entry, err := s.bookStepEntry(events, ev, request.Payload)
	if err != nil {
		return baselineStep{}, err
	}
	header := baselineHeader{
		RecordType: entry.RecordType, EpistemicType: string(entry.EpistemicType), CommittedAt: at.UTC().Format(time.RFC3339Nano),
		SubjectRef: entry.SubjectRef, Correlation: entry.Correlation, Links: entry.Links, Fields: entry.Fields,
	}
	if header.Links == nil {
		header.Links = []evidencebook.Link{}
	}
	return baselineStep{
		N: ev.N, Payload: string(request.Payload), Digest: digest,
		CapsuleID: record.CapsuleID, Capsule: string(record.Capsule), Header: header,
	}, nil
}

// baselineReportOf derives a report's vector as dealSealReport and
// dealStandaloneReport build it, from the report's input and time.
func baselineReportOf(s *dealSession, form string, at time.Time, input string) (baselineReport, error) {
	var in struct {
		Type       string                  `json:"type"`
		Profile    string                  `json:"profile"`
		DealID     string                  `json:"deal_id"`
		Audience   string                  `json:"audience"`
		Nonce      string                  `json:"nonce"`
		Report     map[string]any          `json:"report"`
		Checkpoint *dealprofile.Checkpoint `json:"deal_checkpoint"`
	}
	if err := cli.DecodeJSON([]byte(input), &in); err != nil {
		return baselineReport{}, err
	}
	payload, err := dealprofile.ReportPayload(dealprofile.Report{
		DealID: in.DealID, Audience: in.Audience, Nonce: in.Nonce, Body: in.Report, Checkpoint: in.Checkpoint,
	})
	if err != nil {
		return baselineReport{}, err
	}
	capsule, err := dealprofile.ReportCapsule(s.p.Name, "capsulectl-deal", at)
	if err != nil {
		return baselineReport{}, err
	}
	record, err := cli.Seal(cli.Request{Version: "capsule-seal-request/v1", Capsule: capsule, Payload: payload}, s.key)
	if err != nil {
		return baselineReport{}, err
	}
	return baselineReport{
		Form: form, Timestamp: at.UTC().Format(time.RFC3339), AgentInput: input,
		Payload: string(payload), CapsuleID: record.CapsuleID, Capsule: string(record.Capsule),
	}, nil
}

// baselineFlows are the deals the vectors are made from: x-deal-v0 and
// typed records, a buyer, a seller and a sale with its threads, fyi and
// decide steps (policy and human approvals, recoverable, consequential and
// two-way acts), a record that confirms a close, settlement legs, and a
// report off the deal's log and one in it. Each returns the deals and sales
// to freeze; the first is the one a report is made of, when report is set.
var baselineFlows = []struct {
	name          string
	inLog, report bool
	run           func(t *testing.T) []string
}{
	{"retail-closed", false, true, func(t *testing.T) []string {
		dealFixture(t)
		id := retailDeal(t)
		dealRun(t, "note", "--deal", id, "--kind", "message", "--input", writeJSON(t, `{"from":"user","text":"thanks, it arrived"}`))
		dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"received"}`))
		dealRun(t, "note", "--deal", id, "--kind", "evidence", "--input", writeJSON(t, `{"about":"the delivery","source":"agent"}`))
		return []string{id}
	}},
	{"retail-report-in-log", true, true, func(t *testing.T) []string {
		dealFixture(t)
		return []string{retailDeal(t)}
	}},
	{"claims", false, true, func(t *testing.T) []string { return []string{claimDeal(t)} }},
	{"disclosure", false, true, func(t *testing.T) []string {
		dealFixture(t)
		id, _, _ := openDisclosureDeal(t)
		return []string{id}
	}},
	{"typed-buyer", false, true, func(t *testing.T) []string {
		id := stickerDeal(t, "card", true)
		dealRun(t, "check", "--deal", id, "--input", writeJSON(t, payCheck("card", 454, 500)))
		return []string{id}
	}},
	{"typed-human-approval", false, true, func(t *testing.T) []string {
		dealFixture(t)
		id := openTypedFlight(t)
		c := dealRun(t, "check", "--deal", id, "--input", writeJSON(t, merchantCheck))
		platformApproval(t, id, c["check_id"].(string), "Approve $558.80 purchase", "55880")
		dealRun(t, "note", "--deal", id, "--kind", "act", "--input", writeJSON(t, merchantPay)) // unchecked: an outcome
		_, err := answerCheck(t, id, c["check_id"].(string), "yes, the new site is fine", c["card"].(string))
		require.NoError(t, err)
		dealRun(t, "note", "--deal", id, "--kind", "act", "--input", writeJSON(t, merchantPay))
		dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"received"}`))
		return []string{id}
	}},
	{"typed-seller-settled", false, true, func(t *testing.T) []string {
		id, dueStep := sellerDealWithDueDelivery(t)
		terms, _ := newBuyerSide(t).terms()
		notePaymentReceived(t, id, terms, x402Settle(true))
		dealRun(t, "note", "--deal", id, "--kind", "delivered", "--terms", terms,
			"--content", writeFile(t, "content.bin", deliveredContent), "--resolves", dueStep)
		return []string{id}
	}},
	{"sale-two-threads", false, false, func(t *testing.T) []string {
		saleID, a, b := twoThreadSale(t)
		return []string{saleID, a, b}
	}},
}

func writeBaselineVectors(t *testing.T) {
	v := baselineVectors{Note: "Generated by TestBaselineVectors (DEAL_WRITE_BASELINE_VECTORS=1) from today's builders; every key and secret here is a test-only value."}
	for _, f := range baselineFlows {
		t.Run("write-"+f.name, func(t *testing.T) {
			old := dealReportsOffLog
			dealReportsOffLog = !f.inLog
			t.Cleanup(func() { dealReportsOffLog = old })
			ids := f.run(t)
			bundle := ""
			if f.report {
				bundle = filepath.Join(t.TempDir(), "bundle.json")
				dealRun(t, "report", "--deal", ids[0], "--bundle", bundle)
			}
			for i, id := range ids {
				name := f.name
				if len(ids) > 1 {
					name = fmt.Sprintf("%s-%d", f.name, i)
				}
				d := baselineDealOf(t, name, id)
				if i == 0 && f.report {
					d.Reports = []baselineReport{baselineReportOfCopy(t, id, f.inLog, bundle)}
				}
				v.Deals = append(v.Deals, d)
			}
		})
	}
	out, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(baselineVectorsOut, append(out, '\n'), 0o644))
}

// baselineSessionOf opens the deal store and selects the deal.
func baselineSessionOf(t *testing.T, id string) *dealSession {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.close() })
	require.NoError(t, s.useDeal(t.Context(), id, false))
	return s
}

// baselineDealOf freezes one deal's steps, and requires each to be what the
// deal store holds: the step's digest, and its book record's header and
// field openings exactly as the vector states them.
func baselineDealOf(t *testing.T, flow, id string) baselineDeal {
	t.Helper()
	s := baselineSessionOf(t, id)
	defer func() { require.NoError(t, s.close()) }()
	events, err := s.load(t.Context(), id)
	require.NoError(t, err)
	require.True(t, s.onBook, "a deal opened today is on a book")
	x, ok := s.index.(shelfDealIndex)
	require.True(t, ok)
	records, err := x.records(t.Context(), id)
	require.NoError(t, err)
	byID := map[string]evidencebook.ShelfRecord{}
	for _, r := range records {
		byID[r.RecordID] = r
	}
	d := baselineDeal{
		Flow: flow, DealID: id, DealKey: hex.EncodeToString(s.dkey),
		SigningSeed: hex.EncodeToString(s.key.Seed()), Operator: s.p.Name,
	}
	for i, se := range events {
		local, err := json.Marshal(se.Event)
		require.NoError(t, err)
		d.Events = append(d.Events, baselineEvent{CapsuleID: se.CapsuleID, Digest: se.Digest, Sequence: se.Sequence, Event: local})
		step, err := baselineStepOf(s, events[:i], se.Event)
		require.NoError(t, err)
		require.Equal(t, se.Digest, step.Digest, "the stored digest re-derives")
		r, ok := byID[se.CapsuleID]
		require.True(t, ok, "step %d is a record of the deal's book", step.N)
		h := r.Header
		correlation := evidencebook.Correlation{}
		if h.Correlation != nil {
			correlation = *h.Correlation
		}
		committed, err := time.Parse(time.RFC3339Nano, h.CommittedAt)
		require.NoError(t, err)
		links := h.Links
		if links == nil {
			links = []evidencebook.Link{}
		}
		stored := baselineHeader{
			RecordType: h.RecordType, EpistemicType: string(h.EpistemicType), CommittedAt: committed.UTC().Format(time.RFC3339Nano),
			SubjectRef: h.SubjectRef, Correlation: correlation, Links: links, Fields: map[string]string{},
		}
		for _, field := range []string{dealprofile.LocalField, dealprofile.AmountField, dealprofile.CurrencyField} {
			o, err := x.opening(t.Context(), r, field)
			require.NoError(t, err, "step %d %s", step.N, field)
			stored.Fields[field] = o
		}
		require.Equal(t, step.Header, stored, "step %d: the vector's header is the book's", step.N)
		require.Equal(t, []string{step.Digest}, h.PayloadCommitments, "step %d commits its record", step.N)
		d.Steps = append(d.Steps, step)
	}
	return d
}

// baselineReportOfCopy freezes the report a copy carries, and requires it to
// be what was sealed: off the log, the Capsule rebuilt is the copy's own; in
// it, the payload is the report record's on the book.
func baselineReportOfCopy(t *testing.T, id string, inLog bool, bundlePath string) baselineReport {
	t.Helper()
	raw, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	b, err := cli.DecodeBundleJSON(raw)
	require.NoError(t, err)
	report, capsuleID, form := cli.DealSealedReport(b)
	require.NotNil(t, report, "the copy carries its sealed report")
	want := "standalone"
	if inLog {
		want = "in_log"
	}
	require.Equal(t, want, form)
	input, at := baselineReportInput(t, b, capsuleID)
	s := baselineSessionOf(t, id)
	defer func() { require.NoError(t, s.close()) }()
	r, err := baselineReportOf(s, form, at, input)
	require.NoError(t, err)
	if inLog {
		stored, err := s.verifiedPayload(t.Context(), capsuleID)
		require.NoError(t, err)
		require.Equal(t, string(stored), r.Payload, "the report record's payload")
	} else {
		require.Equal(t, capsuleID, r.CapsuleID, "the copy's own report Capsule")
	}
	return r
}

// baselineReportInput is the sealed report's disclosed input and its
// Capsule's time, read from the copy that carries it.
func baselineReportInput(t *testing.T, b map[string]interface{}, capsuleID string) (string, time.Time) {
	t.Helper()
	exts, _ := b["extensions"].(map[string]interface{})
	var input interface{}
	if section, ok := exts[dealprofile.ReportExtension].(map[string]interface{}); ok {
		input = section["agent_input"]
	} else {
		disclosures, _ := b["disclosures"].(map[string]interface{})
		d, _ := disclosures[capsuleID].(map[string]interface{})
		input = d["agent_input"]
	}
	require.NotNil(t, input, "the report's input is disclosed")
	capsule := findCapsule(b, capsuleID)
	require.NotNil(t, capsule, "the copy carries the report's Capsule")
	at, err := time.Parse(time.RFC3339, capsule["timestamp"].(string))
	require.NoError(t, err)
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	return string(raw), at
}

// findCapsule is the first object under v whose capsule_id is id and that
// carries a timestamp: the Capsule itself, wherever the copy holds it.
func findCapsule(v interface{}, id string) map[string]interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		if x["capsule_id"] == id {
			if _, ok := x["timestamp"].(string); ok {
				return x
			}
		}
		for _, e := range x {
			if c := findCapsule(e, id); c != nil {
				return c
			}
		}
	case []interface{}:
		for _, e := range x {
			if c := findCapsule(e, id); c != nil {
				return c
			}
		}
	}
	return nil
}

// A sealed step is re-derived without the seal checks: a value an earlier
// release's checks admitted (here a run of fullwidth digits, which its ASCII
// scan let through) re-derives to bytes and a digest, so the deal stays
// readable, while a new step that carries it is refused before it is sealed.
func TestRederivingAStepRunsNoSealCheck(t *testing.T) {
	raw, err := os.ReadFile(baselineVectorsPath)
	require.NoError(t, err)
	var v baselineVectors
	require.NoError(t, json.Unmarshal(raw, &v))
	d := v.Deals[0]
	dkey, err := hex.DecodeString(d.DealKey)
	require.NoError(t, err)
	var events []sealedEvent
	for _, e := range d.Events {
		var ev dealEvent
		require.NoError(t, cli.DecodeJSON(e.Event, &ev))
		if ev.Evidence == nil {
			events = append(events, sealedEvent{CapsuleID: e.CapsuleID, Digest: e.Digest, Sequence: e.Sequence, Event: ev})
			continue
		}
		ev.Evidence.Source = "agent_１２３４５６７８"
		payload, digest, err := encodeDealRecord(ev, events, dkey)
		require.NoError(t, err)
		assert.Contains(t, string(payload), "１２３４５６７８")
		assert.Len(t, digest, 64)
		err = checkSealable(payload, events, ev)
		require.ErrorIs(t, err, cli.ErrInput)
		assert.Contains(t, err.Error(), "raw phone number")
		return
	}
	t.Fatal("the first baseline deal has no evidence step")
}

// A refused step says why in capsulectl's words, whatever dealprofile's own
// detail reads: each check's refusal is pinned here.
func TestSealRefusalsKeepTheirWording(t *testing.T) {
	raw, err := os.ReadFile(baselineVectorsPath)
	require.NoError(t, err)
	var v baselineVectors
	require.NoError(t, json.Unmarshal(raw, &v))
	d := v.Deals[0]
	dkey, err := hex.DecodeString(d.DealKey)
	require.NoError(t, err)
	var events []sealedEvent
	var evidence dealEvent
	for _, e := range d.Events {
		var ev dealEvent
		require.NoError(t, cli.DecodeJSON(e.Event, &ev))
		if ev.Evidence != nil {
			evidence = ev
			break
		}
		events = append(events, sealedEvent{CapsuleID: e.CapsuleID, Digest: e.Digest, Sequence: e.Sequence, Event: ev})
	}
	require.NotNil(t, evidence.Evidence)
	for source, want := range map[string]string{
		"agent 555 010 2044":     "refusing to seal: the record would carry a raw phone number; keep it out of claims and tokens",
		"agent bob@shop.example": "refusing to seal: the record would carry a raw email address",
		"agent_scam":             "refusing to seal: a record may not label or grade anyone (scam)",
		"Agent":                  "refusing to seal: the step does not fit the x-deal-v0 profile: /body/source: value \"Agent\" does not match pattern",
	} {
		ev := evidence
		e := *ev.Evidence
		e.Source = source
		ev.Evidence = &e
		payload, _, err := encodeDealRecord(ev, events, dkey)
		require.NoError(t, err, source)
		err = checkSealable(payload, events, ev)
		require.ErrorIs(t, err, cli.ErrInput, source)
		assert.Contains(t, cli.SafeError(err), want, source)
	}
	err = scanRecord(map[string]interface{}{"body": map[string]interface{}{"text": "ask for m. torres"}}, []string{"M. Torres"})
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "refusing to seal: the record would carry a raw counterparty identifier")
}
