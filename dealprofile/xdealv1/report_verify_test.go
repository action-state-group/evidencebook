// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reportVerifyVectors = "../../testdata/deal-v1/report-verify/vectors.json"

// verifyCase is one VerifyReport vector: a report pair and the first record
// it is read against, and what VerifyReport gives (the key, or the stage and
// a fragment of its error).
type verifyCase struct {
	Name   string `json:"name"`
	Report string `json:"report"`
	First  string `json:"first"`
	Want   struct {
		KeyID string `json:"key_id,omitempty"`
		Stage string `json:"stage,omitempty"`
		Error string `json:"error,omitempty"`
	} `json:"want"`
}

// sampleReport is the report vector name, its deal and the log it rests on
// moved to the handshake seller's book, as a mutable JSON object.
func sampleReport(t *testing.T, h *handshake, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/deal-v1/report/vectors.json")
	require.NoError(t, err)
	var f struct {
		Cases []struct {
			Name   string         `json:"name"`
			Report map[string]any `json:"report"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	for _, c := range f.Cases {
		if c.Name == name {
			r := c.Report
			r["deal_id"] = h.agreement.Block.DealID
			r["deal_checkpoint"].(map[string]any)["log_id"] = h.sellerLog
			r["rests_on"].(map[string]any)["own"].(map[string]any)["log_id"] = h.sellerLog
			return r
		}
	}
	t.Fatalf("no report vector %q", name)
	return nil
}

// sealReport seals payload as a report Capsule under key (actionID
// dealprofile.ReportActionID unless given) and returns its pair.
func sealReport(t *testing.T, key string, payload []byte, actionID string) []byte {
	t.Helper()
	if actionID == "" {
		actionID = dealprofile.ReportActionID
	}
	identity, err := emit.NewEd25519SigningIdentity(seed(key + " record key"))
	require.NoError(t, err)
	in, err := dealprofile.ReportCapsule(key, "xdealv1-test", time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	in.ActionID = actionID
	sealed, err := emit.Seal(emit.SealInput{Capsule: in, Payload: json.RawMessage(payload), Identity: identity})
	require.NoError(t, err)
	pair, err := ReportPair(sealed.Payload, sealed.Envelope, payload)
	require.NoError(t, err)
	return pair
}

func payloadOf(t *testing.T, r map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	doc, err := decodeJSON(raw)
	require.NoError(t, err)
	out, err := jcsJSON(doc)
	require.NoError(t, err)
	return out
}

// rulingRow is the ruling of the sample's first ruled row, as a mutable map.
func rulingRow(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	for _, row := range r["rows"].([]any) {
		if m := row.(map[string]any); m["ruling"] != nil {
			return m["ruling"].(map[string]any)
		}
	}
	t.Fatal("no ruled row")
	return nil
}

// firstSource is the sample's first relied_on record, as a mutable map.
func firstSource(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	for _, row := range r["rows"].([]any) {
		if src := row.(map[string]any)["relied_on"].([]any); len(src) > 0 {
			return src[0].(map[string]any)
		}
	}
	t.Fatal("no relied_on record")
	return nil
}

// verifyCases builds every VerifyReport case from one handshake: the
// seller's report of its deal against the seller's agreement, and each way
// it is refused.
func verifyCases(t *testing.T) []verifyCase {
	t.Helper()
	h := newHandshake(t, nil)
	other := "deal/" + hex.EncodeToString(seed("another seller log")[:16])
	h.seller.tick()
	_, otherFirst := h.seller.seal(other, h.agreement)
	// A book whose first record is not an agreement: a case book opening
	// with a ruling of the same deal (a deal book's record checks already
	// make its seq 1 an agreement).
	notAgreement := "case/" + hex.EncodeToString(seed("a case book")[:16])
	ruling := validRecords(t)["ruling"]
	ruling.Block.DealID, ruling.Block.Seq, ruling.Block.Prev = h.agreement.Block.DealID, 1, nil
	h.seller.tick()
	_, caseFirst := h.seller.seal(notAgreement, ruling)
	var cases []verifyCase
	add := func(name string, report, first []byte, stage, fragment string) {
		c := verifyCase{Name: name, Report: hex.EncodeToString(report), First: hex.EncodeToString(first)}
		if stage == "" {
			c.Want.KeyID = h.seller.key()
		}
		c.Want.Stage, c.Want.Error = stage, fragment
		cases = append(cases, c)
	}
	withRuling := func(against any) []byte {
		r := sampleReport(t, h, "appendix-a-21-nov")
		rulingRow(t, r)["against"] = against
		return payloadOf(t, r)
	}
	seal := func(payload []byte) []byte { return sealReport(t, "seller", payload, "") }

	add("ruled-against-seller", seal(withRuling(RoleSeller)), h.agreementPair, "", "")
	add("ruled-against-buyer", seal(withRuling(RoleBuyer)), h.agreementPair, "", "")
	add("ruling-against-null", seal(withRuling(nil)), h.agreementPair, "", "")
	add("no-ruling", seal(payloadOf(t, sampleReport(t, h, "appendix-a-12-nov"))), h.agreementPair, "", "")
	add("against-the-adjudicator", seal(withRuling(SideAdjudicator)), h.agreementPair, StageReport, "against buyer, seller or null")
	add("against-in-another-case", seal(withRuling("Seller")), h.agreementPair, StageReport, "against buyer, seller or null")
	add("against-empty", seal(withRuling("")), h.agreementPair, StageReport, "against buyer, seller or null")
	{
		// A ruling with no against member, or with it in another case, is not
		// the report's form: a null against is written out.
		r := sampleReport(t, h, "appendix-a-21-nov")
		delete(rulingRow(t, r), "against")
		add("ruling-without-against", seal(payloadOf(t, r)), h.agreementPair, StageReport, "does not read as a report")
		r = sampleReport(t, h, "appendix-a-21-nov")
		ru := rulingRow(t, r)
		ru["Against"] = ru["against"]
		delete(ru, "against")
		add("against-misnamed", seal(payloadOf(t, r)), h.agreementPair, StageReport, "does not read as a report")
	}
	{
		// What the round trip keeps but BuildReport never writes.
		for _, c := range []struct {
			name string
			mut  func(r map[string]any)
		}{
			{"rows-null", func(r map[string]any) { r["rows"] = nil }},
			{"report-text-null", func(r map[string]any) { r["report"] = nil }},
			{"relied-on-null", func(r map[string]any) { r["rows"].([]any)[0].(map[string]any)["relied_on"] = nil }},
			{"late-statements-null", func(r map[string]any) {
				r["rows"].([]any)[0].(map[string]any)["late"].(map[string]any)["statements"] = nil
			}},
			{"late-disputes-null", func(r map[string]any) {
				r["rows"].([]any)[0].(map[string]any)["late"].(map[string]any)["disputes"] = nil
			}},
			{"owed-null", func(r map[string]any) { r["rows"].([]any)[0].(map[string]any)["owed"] = nil }},
			{"owed-a-number", func(r map[string]any) { r["rows"].([]any)[0].(map[string]any)["owed"] = 5 }},
			{"owed-a-list", func(r map[string]any) { r["rows"].([]any)[0].(map[string]any)["owed"] = []any{} }},
			{"witnessed-without-a-time", func(r map[string]any) {
				src := firstSource(t, r)
				src["time"], src["witnessed"] = nil, true
			}},
			{"a-time-not-witnessed", func(r map[string]any) {
				src := firstSource(t, r)
				src["time"], src["witnessed"] = "2026-11-03T10:00:00Z", false
			}},
			{"rulings-unnamed-negative", func(r map[string]any) { r["rulings_unnamed"] = -1 }},
		} {
			r := sampleReport(t, h, "appendix-a-21-nov")
			c.mut(r)
			add(c.name, seal(payloadOf(t, r)), h.agreementPair, StageReport, "does not read as a report")
		}
	}
	add("signed-by-the-counterparty", sealReport(t, "buyer", withRuling(RoleSeller), ""), h.agreementPair, StageReport,
		"signed by another key than its deal's first record")
	{
		r := sampleReport(t, h, "appendix-a-21-nov")
		r["rests_on"].(map[string]any)["own"].(map[string]any)["log_id"] = notAgreement
		add("first-record-not-an-agreement", seal(payloadOf(t, r)), caseFirst, StageReport, "not the agreement at seq 1")
	}
	add("first-record-of-another-book", seal(withRuling(RoleSeller)), otherFirst, StageReport, "not of the book the report rests on")
	{
		r := sampleReport(t, h, "appendix-a-21-nov")
		r["deal_id"] = "deal-9999999999999999"
		add("first-record-of-another-deal", seal(payloadOf(t, r)), h.agreementPair, StageReport, "not of the book the report rests on")
	}
	add("not-a-report-capsule", sealReport(t, "seller", withRuling(RoleSeller), "something-else"), h.agreementPair, StageReport,
		"not a report Capsule")
	{
		r := sampleReport(t, h, "appendix-a-21-nov")
		r["profile"] = dealprofile.Profile
		add("a-v0-profile", seal(payloadOf(t, r)), h.agreementPair, StageReport, "not a deal_report of x-deal-v1")
	}
	{
		r := sampleReport(t, h, "appendix-a-21-nov")
		r["extra"] = true
		add("an-unknown-member", seal(payloadOf(t, r)), h.agreementPair, StageReport, "does not read as a report")
	}
	{
		// The pair's agent_input is another payload than the Capsule commits.
		pair := seal(withRuling(RoleSeller))
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(pair, &m))
		m["agent_input"] = withRuling(RoleBuyer)
		swapped, err := json.Marshal(m)
		require.NoError(t, err)
		add("payload-not-committed", swapped, h.agreementPair, StageReport, "not the one the Capsule commits")
	}
	add("the-first-record-is-the-report", seal(withRuling(RoleSeller)), seal(withRuling(RoleSeller)), StageBook, "")
	add("the-report-is-a-book-pair", h.agreementPair, h.agreementPair, StageReport, "not a report pair")
	return cases
}

func TestVerifyReport(t *testing.T) {
	for _, c := range verifyCases(t) {
		report, err := hex.DecodeString(c.Report)
		require.NoError(t, err)
		first, err := hex.DecodeString(c.First)
		require.NoError(t, err)
		got, err := VerifyReport(report, first)
		if c.Want.Stage == "" {
			require.NoError(t, err, c.Name)
			assert.Equal(t, c.Want.KeyID, got.KeyID, c.Name)
			assert.Equal(t, Profile, got.Report.Profile, c.Name)
			continue
		}
		var se *StageError
		require.ErrorAs(t, err, &se, c.Name)
		assert.Equal(t, c.Want.Stage, se.Stage, c.Name)
		assert.Contains(t, err.Error(), c.Want.Error, c.Name)
	}
}

// TestReportVerifyVectors writes and checks testdata/deal-v1/report-verify
// (EB_WRITE_DEAL_V1_VECTORS=1), which the Rust and Python ports replay.
func TestReportVerifyVectors(t *testing.T) {
	out := map[string]any{
		"description": "VerifyReport (design §9, \"Verifying a report\"): each case a sealed v1 report pair and the first record of the holder's deal book it is read against, as hex, and what Go gives: the signer's key_id, or the stage and a fragment of the error. Written by dealprofile/xdealv1 TestReportVerifyVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       verifyCases(t),
	}
	b, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	b = append(b, '\n')
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(reportVerifyVectors), 0o755))
		require.NoError(t, os.WriteFile(reportVerifyVectors, b, 0o644))
	}
	want, err := os.ReadFile(reportVerifyVectors)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, b), "%s differs; rerun with EB_WRITE_DEAL_V1_VECTORS=1", reportVerifyVectors)
}
