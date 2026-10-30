// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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

// handBuilt are valid samples of the types no fixture or vector holds, each
// with its optional and nullable members both present and absent. Each is
// checked against its schema before it is decoded.
var handBuilt = map[string]string{
	"action-report minimal": `{"type":"action-report/v0","canonicalization":"jcs","chain_id":"deal-0123456789abcdef","seq":7,"at":"2026-10-30T01:02:03Z",` +
		`"prev":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"},"chain_root":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64b + `"},` +
		`"body":{"presents":[{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"}]}}`,
	"action-report full": `{"type":"action-report/v0","canonicalization":"jcs","chain_id":"deal-0123456789abcdef","seq":7,"at":"2026-10-30T01:02:03Z",` +
		`"prev":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"},"chain_root":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64b + `"},` +
		`"refs":[{"rel":"observes","type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"}],"producer":{"name":"p","version":"1","commit":"c"},` +
		`"body":{"presents":[{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"}],"authority":[]}}`,
	"check-request minimal": `{"type":"check-request/v0","phase":"proposed","ruleset_digest":"` + hex64a + `","task_authority_ref":null,"proposed_action":{"action":"pay"}}`,
	"check-request full": `{"type":"check-request/v0","phase":"executed","platform":{"id":"a-platform","display_name":""},"ruleset_ref":"",` +
		`"ruleset_digest":"` + hex64a + `","task_authority_ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64b + `"},` +
		`"root_intent_ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64b + `"},"parent_delegation_ref":null,` +
		`"proposed_action":{},"history_refs":[],"agent_assessment":{"n":1},"platform_approval_refs":[]}`,
	"check-response predicate": `{"type":"check-response/v0","disposition":"ASK","valid_until":"2026-10-30T01:02:03Z","proposed_action_digest":"` + hex64a + `",` +
		`"findings":[],"authority_basis":[{"type":"platform_approval","ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"},"scope":"mismatch"}],` +
		`"evaluation_ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"},"ruleset_digest":"` + hex64b + `",` +
		`"task_authority_ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64b + `"},"materiality":"predicate","materiality_digest":"` + hex64a + `"}`,
	"check-response none": `{"type":"check-response/v0","disposition":"DO","valid_until":"2026-10-30T01:02:03Z","proposed_action_digest":"` + hex64a + `",` +
		`"findings":[{"question":"payee","rule":"payee_matches"}],"authority_basis":[{"type":"task_authority","ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"}}],` +
		`"evaluation_ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64a + `"},"ruleset_digest":"` + hex64b + `",` +
		`"task_authority_ref":{"type":"record","digest_alg":"SHA-256","digest":"` + hex64b + `"},"materiality":"none_fail_safe","materiality_digest":null}`,
}

const (
	hex64a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hex64b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

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
	names := make([]string, 0, len(handBuilt))
	for name := range handBuilt {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := []byte(handBuilt[name])
		var head struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &head), name)
		sch, err := RecordSchema(head.Type)
		require.NoError(t, err, name)
		require.NoError(t, sch.Validate(mustDecode(t, raw)), name)
		samples = append(samples, sample{"hand-built " + name, raw, nil, head.Type != TypeActionReport})
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
			require.NoError(t, Decode(s.raw, rec), s.name)
			types[head.Type] = true
		} else {
			var r Record
			require.NoError(t, Decode(s.raw, &r), s.name)
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
	assert.Empty(t, missing, "every record type has a sample")
	t.Logf("%d records round-trip through %d record types", len(samples), len(types))
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
	_, err = json.Marshal(Record{Block: blk, Body: (*CloseBody)(nil)})
	assert.ErrorContains(t, err, "has no body", "a nil pointer body is no body")
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

	// encoding/json reads these without error; they are not this record.
	const block = `{"profile":"x-deal-v0","canonicalization":"jcs","deal_id":"sale-0123456789abcdef","record_type":"sale_close","seq":3,"at":"2026-10-30T01:02:03Z"}`
	require.NoError(t, Decode([]byte(`{"x-deal-v0":`+block+`,"body":{}}`), &r))
	for name, text := range map[string]string{
		"a member name in another case":  `{"x-deal-v0":` + block + `,"Body":{}}`,
		"a null body":                    `{"x-deal-v0":` + block + `,"body":null}`,
		"a block member in another case": `{"x-deal-v0":` + strings.Replace(block, `"seq"`, `"Seq"`, 1) + `,"body":{}}`,
	} {
		assert.ErrorContains(t, Decode([]byte(text), &r), "would encode to other bytes", name)
	}
	var ta TaskAuthority
	assert.Error(t, Decode([]byte(`{"type":"task-authority/v0","Type":"x"}`), &ta), "a typed record goes through the same check")
	var cr CheckRequest
	assert.ErrorContains(t, Decode([]byte(`{"type":"check-request/v0","phase":"proposed","ruleset_digest":"`+hex64a+`","task_authority_ref":null,"proposed_action":{},"Phase":"executed"}`), &cr), "would encode to other bytes")

	// A record built from the types passes or fails the schema like its
	// text: a close needs its outcome reference and differences.
	_, _, err = Encode(Record{Block: blk, Body: CloseBody{}}, nil)
	var se *StageError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, StageSchema, se.Stage)
}
