// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstPositive is the positive chain's baseline record, as raw JSON.
func firstPositive(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixturesDir, "positive", "01-baseline.json"))
	require.NoError(t, err)
	var fx struct {
		Record map[string]any `json:"record"`
	}
	require.NoError(t, json.Unmarshal(raw, &fx))
	return fx.Record
}

func stageOf(t *testing.T, err error) string {
	t.Helper()
	var se *StageError
	require.ErrorAs(t, err, &se)
	return se.Stage
}

func TestEncodeRefusals(t *testing.T) {
	rec := firstPositive(t)
	_, digest, err := Encode(rec, nil)
	require.NoError(t, err)
	assert.Len(t, digest, 64)

	t.Run("a local-store value", func(t *testing.T) {
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		// Any string the record carries, taken as a raw local value, is refused.
		var some string
		for _, s := range []string{"deal_type", "channel"} {
			if v, ok := rec["body"].(map[string]any)[s].(string); ok && len(v) > 2 {
				some = v
			}
		}
		require.NotEmpty(t, some, "the fixture names a deal type or channel")
		require.Contains(t, string(raw), some)
		_, _, err = Encode(rec, []string{strings.ToUpper(some)})
		assert.Equal(t, StagePersonalData, stageOf(t, err), "matched case-insensitively")
	})
	t.Run("a float", func(t *testing.T) {
		_, _, err := Encode([]byte(`{"x-deal-v0":{"canonicalization":"jcs","seq":1.5},"body":{}}`), nil)
		assert.Equal(t, StageCanonicalization, stageOf(t, err))
	})
	t.Run("another canonicalization", func(t *testing.T) {
		_, _, err := Encode([]byte(`{"x-deal-v0":{"canonicalization":"jcs-n"},"body":{}}`), nil)
		assert.Equal(t, StageCanonicalization, stageOf(t, err))
	})
	t.Run("no block", func(t *testing.T) {
		assert.Equal(t, StageCanonicalization, stageOf(t, Check([]byte(`[1]`), nil)))
	})
	t.Run("a grading word", func(t *testing.T) {
		_, _, err := Encode([]byte(`{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"a Scam"}}`), nil)
		assert.Equal(t, StageWording, stageOf(t, err))
	})
	t.Run("off the schema", func(t *testing.T) {
		_, _, err := Encode([]byte(`{"x-deal-v0":{"canonicalization":"jcs"},"body":{}}`), nil)
		assert.Equal(t, StageSchema, stageOf(t, err))
	})
	t.Run("an unknown typed record", func(t *testing.T) {
		_, _, err := Encode([]byte(`{"type":"payment/v0","canonicalization":"jcs"}`), nil)
		assert.Equal(t, StageSchema, stageOf(t, err))
	})
	t.Run("not JSON", func(t *testing.T) {
		_, _, err := Encode([]byte(`{`), nil)
		assert.Error(t, err)
	})
	t.Run("a claimed digest", func(t *testing.T) {
		assert.NoError(t, CheckDigest(rec, digest))
		assert.Equal(t, StageDigest, stageOf(t, CheckDigest(rec, strings.Repeat("0", 64))))
	})
}

func TestReportPayloadRefusals(t *testing.T) {
	r := Report{DealID: "deal-0123456789abcdef", Audience: AudienceKeep, Nonce: strings.Repeat("a", 64), Body: map[string]any{"n": 1}}
	payload, err := ReportPayload(r)
	require.NoError(t, err)
	assert.JSONEq(t, `{"audience":"keep","deal_id":"deal-0123456789abcdef","nonce":"`+r.Nonce+`","profile":"x-deal-v0","report":{"n":1},"type":"deal_report"}`, string(payload))

	bad := r
	bad.Audience = "history"
	_, err = ReportPayload(bad)
	assert.ErrorContains(t, err, "not a copy audience")
	bad = r
	bad.Nonce = strings.Repeat("A", 64)
	_, err = ReportPayload(bad)
	assert.ErrorContains(t, err, "64 lowercase hex")
}

func TestReportCapsuleIsToTheSecond(t *testing.T) {
	at := time.Date(2026, 10, 30, 1, 2, 3, 999, time.FixedZone("x", 3600))
	c := ReportCapsule("deal", at)
	assert.Equal(t, time.Date(2026, 10, 30, 0, 2, 3, 0, time.UTC), c.Timestamp)
	assert.Equal(t, ReportActionID, c.ActionID)
	assert.Equal(t, emit.ActionTypeFYI, c.ActionType)
	assert.Nil(t, c.Chain, "a report is not chained")
}

func TestStepCapsuleRefusals(t *testing.T) {
	in := StepInput{DealID: "deal-0123456789abcdef", Seq: 1, Operator: "deal", At: time.Unix(0, 0)}
	c, h, err := StepCapsule(in)
	require.NoError(t, err)
	assert.Equal(t, "deal-0123456789abcdef/1", c.ActionID)
	assert.Equal(t, emit.ActionTypeFYI, c.ActionType)
	assert.Nil(t, c.Chain)
	assert.Empty(t, h.Cites, "the first step cites nothing")
	require.Len(t, c.Extensions, 1)
	assert.Equal(t, DealIDMember, c.Extensions[0].Name)

	bad := in
	bad.Seq = 0
	_, _, err = StepCapsule(bad)
	assert.Error(t, err)
	bad = in
	bad.DealID = ""
	_, _, err = StepCapsule(bad)
	assert.Error(t, err)
	bad = in
	bad.Parent = &emit.Chain{ParentCapsuleID: strings.Repeat("a", 64), Relation: "supersedes"}
	_, _, err = StepCapsule(bad)
	assert.ErrorContains(t, err, "not follows or confirms")
}
