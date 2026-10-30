// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTyped is the zero value of a typed record's type, nil for no type.
func newTyped(typ string) any {
	switch typ {
	case TypeTaskAuthority:
		return &TaskAuthority{}
	case TypeProposedAction:
		return &ProposedAction{}
	case TypeActionEvaluation:
		return &ActionEvaluation{}
	case TypeActionApproval:
		return &ActionApproval{}
	case TypeActionRecord:
		return &ActionRecord{}
	case TypeActionOutcome:
		return &ActionOutcome{}
	case TypeActionReport:
		return &ActionReport{}
	case TypeCheckRequest:
		return &CheckRequest{}
	case TypeCheckResponse:
		return &CheckResponse{}
	case TypeCommercialBounds:
		return &CommercialBounds{}
	}
	return nil
}

var typedTypes = []string{
	TypeTaskAuthority, TypeProposedAction, TypeActionEvaluation, TypeActionApproval, TypeActionRecord,
	TypeActionOutcome, TypeActionReport, TypeCheckRequest, TypeCheckResponse, TypeCommercialBounds,
}

// TestRecordRoundTrip checks that the record types hold every record the
// profile's positive fixtures and the baseline vectors carry: each decodes
// into its type with no member left over, and encodes to the same bytes.
func TestRecordRoundTrip(t *testing.T) {
	store := localStoreValues(loadFingerprints(t))
	type sample struct {
		name  string
		raw   []byte
		local []string
		// contract is a contract document, not a record: it is compared
		// as JCS, not through Encode, which only takes records.
		contract bool
	}
	var samples []sample
	for _, dir := range positiveChains {
		files, err := filepath.Glob(filepath.Join(fixturesDir, dir, "*.json"))
		require.NoError(t, err)
		sort.Strings(files)
		for _, f := range files {
			var fx struct {
				Record json.RawMessage `json:"record"`
			}
			readJSON(t, f, &fx)
			samples = append(samples, sample{dir + "/" + filepath.Base(f), fx.Record, store, false})
		}
	}
	for _, d := range loadBaseline(t).Deals {
		for _, s := range d.Steps {
			samples = append(samples, sample{fmt.Sprintf("baseline %s step %d", d.Flow, s.N), []byte(s.Payload), nil, false})
		}
	}
	var bounds struct {
		Vectors []struct {
			Document json.RawMessage `json:"document"`
		} `json:"vectors"`
	}
	readJSON(t, filepath.Join(fixturesDir, "commercial-bounds-vectors.json"), &bounds)
	for i, v := range bounds.Vectors {
		samples = append(samples, sample{fmt.Sprintf("commercial-bounds vector %d", i), v.Document, nil, true})
	}
	require.NotEmpty(t, samples)
	types := map[string]bool{}
	for _, s := range samples {
		var head struct {
			Type  string          `json:"type"`
			Block json.RawMessage `json:"x-deal-v0"`
		}
		require.NoError(t, json.Unmarshal(s.raw, &head), s.name)
		var rec any
		if head.Block == nil {
			rec = newTyped(head.Type)
			require.NotNil(t, rec, "%s: typed record %q", s.name, head.Type)
			require.NoError(t, strictUnmarshal(s.raw, rec), s.name)
			types[head.Type] = true
		} else {
			var r Record
			require.NoError(t, strictUnmarshal(s.raw, &r), s.name)
			rec = r
			types[r.Block.RecordType] = true
		}
		want, err := canonical.JCS(mustDecode(t, s.raw))
		require.NoError(t, err)
		var payload []byte
		if s.contract {
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			payload, err = canonical.JCS(mustDecode(t, raw))
			require.NoError(t, err)
		} else {
			payload, _, err = Encode(rec, s.local)
			require.NoError(t, err, s.name)
		}
		assert.Equal(t, string(want), string(payload), s.name)
	}
	var missing []string
	for _, typ := range append(RecordTypes(), typedTypes...) {
		if !types[typ] {
			missing = append(missing, typ)
		}
	}
	t.Logf("%d records round-trip through %d record types; none of the samples is a %v", len(samples), len(types), missing)
}

func mustDecode(t *testing.T, raw []byte) any {
	t.Helper()
	doc, err := decode(raw)
	require.NoError(t, err)
	return doc
}

func TestRecordBodyTypes(t *testing.T) {
	for _, rt := range RecordTypes() {
		b := newBody(rt)
		if assert.NotNil(t, b, rt) {
			assert.Equal(t, rt, b.RecordType())
		}
	}
	assert.Nil(t, newBody("deal_note"))
}

func TestRecordRefusals(t *testing.T) {
	blk := Block{Profile: Profile, Canonicalization: Canonicalization, DealID: "deal-0123456789abcdef", RecordType: TypeClose, Seq: 3, At: "2026-10-30T01:02:03Z"}

	_, err := json.Marshal(Record{Block: blk})
	assert.ErrorContains(t, err, "has no body")
	_, err = json.Marshal(Record{Block: blk, Body: &OutcomeBody{}})
	assert.ErrorContains(t, err, "outcome body on a close record")

	var r Record
	err = json.Unmarshal([]byte(`{"x-deal-v0":{"profile":"x-deal-v0","record_type":"deal_note"},"body":{}}`), &r)
	assert.ErrorContains(t, err, `record type "deal_note" has no body type`)
	err = json.Unmarshal([]byte(`{"x-deal-v0":{"record_type":"sale_close","colour":"red"},"body":{}}`), &r)
	assert.ErrorContains(t, err, "the x-deal-v0 block")
	err = json.Unmarshal([]byte(`{"x-deal-v0":{"record_type":"sale_close"},"body":{"note":"x"}}`), &r)
	assert.ErrorContains(t, err, "a sale_close body")
	err = json.Unmarshal([]byte(`{"x-deal-v0":{"record_type":"sale_close"},"body":{},"extra":1}`), &r)
	assert.Error(t, err)

	// A record built from the types passes or fails the schema like its
	// text: a close needs its outcome reference and differences.
	_, _, err = Encode(Record{Block: blk, Body: CloseBody{}}, nil)
	var se *StageError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, StageSchema, se.Stage)
}
