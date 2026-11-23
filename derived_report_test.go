// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Terms Pack served by the digest of its compiled definition (#606): a
// holder opts the digest in, a requester asks for it over the holder's
// records, runs the pack over the declared fields the holder sent, signs the
// Result and builds one Evidence Bundle with the contract-report/v1 view.

const packContract = "ec:terms-pack-test@1"

// packDocument stands for a compiled five-row Terms Pack. This package reads
// no member of it (P-14): the holder states the fields it reads when it
// registers the digest.
const packDocument = `{"pack":"terms-pack-test","version":1,"rows":["has-observation","has-claim","has-ruling","has-exchange","all-committed"]}`

var packRows = []struct{ id, recordType string }{
	{"has-observation", "observation"}, {"has-claim", "claim"}, {"has-ruling", "ruling"}, {"has-exchange", "exchange"},
}

// evaluatePack is the requester's evaluator: five rows over the projected
// records, each claim citing the response record, as ReportFromDerivation
// requires.
func evaluatePack(inputs DerivedInputs) (json.RawMessage, error) {
	has := func(recordType string) bool {
		return slices.ContainsFunc(inputs.Records, func(r DerivedRecord) bool { return r.Fields["record_type"] == recordType })
	}
	committed := true
	for _, r := range inputs.Records {
		if _, ok := r.Fields["committed_at"]; !ok {
			committed = false
		}
	}
	verdicts := map[string]bool{"all-committed": committed}
	for _, row := range packRows {
		verdicts[row.id] = has(row.recordType)
	}
	evidence := []any{map[string]any{"digest": inputs.Response, "digest_alg": "SHA-256"}}
	var claims []any
	buckets := map[string]any{"met": []any{}, "not_met": []any{}, "not_evaluable": []any{}}
	for _, id := range []string{"has-observation", "has-claim", "has-ruling", "has-exchange", "all-committed"} {
		verdict := map[bool]string{true: "met", false: "not_met"}[verdicts[id]]
		claims = append(claims, map[string]any{
			"id": id, "contract_ref": packContract, "requirement_ref": id, "tier": "recomputed", "grade": "self-attested",
			"sufficiency": "SATISFIED", "verdict": verdict, "evidence": evidence, "proofs": []any{},
			"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": evidence},
		})
		buckets[verdict] = append(buckets[verdict].([]any), id)
	}
	return json.Marshal(map[string]any{
		"result_version": "evidence-result-v0", "generated_at": "2026-11-22T00:00:00Z", "claims": claims,
		"aggregate": map[string]any{"buckets": buckets, "coverage": map[string]any{"evaluated_population": 5, "excluded_not_applicable": 0, "unknown_count": 0}},
	})
}

type packFixture struct {
	holder, requester *Book
	id                string
	pin               string
}

// newPackFixture is a holder with the linked-book records and a pack
// registered for two requesters, and a requester book.
func newPackFixture(t *testing.T, proofs bool) packFixture {
	t.Helper()
	holder, _ := linkedBook(t)
	mustAppend(t, holder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "someone"})
	id, err := holder.RegisterDerivation(DerivationDefinition{
		Document: []byte(packDocument), Recipients: []string{"requester-1", "requester-2"}, Proofs: proofs,
		Forms: []SubjectKind{SubjectFullHistory}, Fields: []string{"record_type", "committed_at"},
	})
	require.NoError(t, err)
	return packFixture{holder: holder, requester: newFixture(t, "requester", 30).open(t), id: id, pin: anchorPin(t, holder)}
}

func (f packFixture) ask(t *testing.T, requester string) (SentRequest, Reply) {
	t.Helper()
	sent, err := f.requester.Request(context.Background(), EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: f.id, Coverage: Coverage{ExpectedPin: f.pin}}, "book-a")
	require.NoError(t, err)
	reply, _, err := f.holder.Respond(context.Background(), sent.Message(), RespondOptions{AuthenticatedRequester: requester, Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	return sent, reply
}

func (f packFixture) record(t *testing.T, requester string) (Record, ResponseStatement) {
	t.Helper()
	sent, reply := f.ask(t, requester)
	recorded, err := f.requester.RecordResponse(context.Background(), sent.RecordID, reply.Message, responderKeys(f.holder, 1))
	require.NoError(t, err)
	var statement ResponseStatement
	require.NoError(t, json.Unmarshal(recorded.Header.Statement, &statement))
	return recorded, statement
}

func TestTermsPackReportEndToEnd(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, true)
	// The digest is the one of the compiled definition: SHA-256 over its JCS.
	assert.Equal(t, digestBytes([]byte(`{"pack":"terms-pack-test","rows":["has-observation","has-claim","has-ruling","has-exchange","all-committed"],"version":1}`)), f.id)

	recorded, statement := f.record(t, "requester-1")
	require.Equal(t, OutcomeArtifact, statement.Outcome, statement.Failure)
	assert.Empty(t, statement.NotChecked, "the answer's records are proven under its anchor")

	inputs, err := f.requester.DerivedInputs(ctx, recorded.RecordID)
	require.NoError(t, err)
	assert.True(t, inputs.Proven)
	assert.Equal(t, f.id, inputs.Derivation)
	// Five records of the book and the two index roots the book sealed for them.
	require.Len(t, inputs.Records, 7)
	for _, r := range inputs.Records {
		assert.ElementsMatch(t, []string{"record_type", "committed_at"}, mapKeys(r.Fields), "only the declared fields travel")
	}

	report, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{})
	require.NoError(t, err)
	verified, err := VerifyBundle(report.Bundle.JSON)
	require.NoError(t, err)
	require.NoError(t, verified.FullyVerified())
	assert.JSONEq(t, `{}`, string(verified.Extensions[ExtensionContractReport]))

	// The signed reply is in the bundle, whole: the holder's word travels with
	// the requester's Result.
	reply, err := f.requester.ReplyOctets(ctx, recorded.RecordID)
	require.NoError(t, err)
	assert.True(t, slices.ContainsFunc(mapValues(verified.Payloads), func(p []byte) bool { return string(p) == string(reply) }), "the bundle carries the reply octets")

	// The same Result is not sealed twice.
	again, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{})
	require.NoError(t, err)
	assert.Equal(t, report.Result.RecordID, again.Result.RecordID)

	if dir := os.Getenv("EB_WRITE_DERIVED_REPORT_FIXTURE"); dir != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bundle.json"), report.Bundle.JSON, 0o644))
	}
}

func TestTermsPackWithoutProofsIsHolderWordOnly(t *testing.T) {
	f := newPackFixture(t, false)
	recorded, statement := f.record(t, "requester-1")
	assert.Equal(t, []string{"anchor_inclusion"}, statement.NotChecked)
	inputs, err := f.requester.DerivedInputs(context.Background(), recorded.RecordID)
	require.NoError(t, err)
	assert.False(t, inputs.Proven)
}

func TestTermsPackServingRefusals(t *testing.T) {
	f := newPackFixture(t, true)
	ask := func(derivation, requester string) Reply {
		reply, _, err := f.holder.Respond(context.Background(), jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: derivation, Coverage: Coverage{ExpectedPin: f.pin}}), RespondOptions{AuthenticatedRequester: requester, Policy: DefaultSharePolicy()})
		require.NoError(t, err)
		return reply
	}
	mustRefusal(t, ask(digestBytes([]byte("a pack nobody opted in")), "requester-1"), ReasonDerivationUnsupported)
	mustRefusal(t, ask(f.id, "requester-3"), ReasonNotAuthorized)
	mustRefusal(t, ask(f.id, ""), ReasonNotAuthorized)
	for _, who := range []string{"requester-1", "requester-2"} {
		mustEnvelope(t, ask(f.id, who))
	}
}

func TestDerivedInputsRefusesWhatIsNotAnAnswer(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, true)
	_, err := f.requester.DerivedInputs(ctx, "not-a-record")
	require.Error(t, err)

	// A refusal, recorded as a response, is not inputs.
	refused, err := f.requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: digestBytes([]byte("unregistered")), Coverage: Coverage{ExpectedPin: f.pin}}, "book-a")
	require.NoError(t, err)
	reply, _, err := f.holder.Respond(ctx, refused.Message(), RespondOptions{AuthenticatedRequester: "requester-1", Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	recorded, err := f.requester.RecordResponse(ctx, refused.RecordID, reply.Message, responderKeys(f.holder, 1))
	require.NoError(t, err)
	_, err = f.requester.DerivedInputs(ctx, recorded.RecordID)
	assert.ErrorContains(t, err, OutcomeRefusal)

	// A registered token's answer is not a derivation by digest.
	card, err := f.requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard, Coverage: Coverage{ExpectedPin: f.pin}}, "book-a")
	require.NoError(t, err)
	cardReply, _, err := f.holder.Respond(ctx, card.Message(), RespondOptions{AuthenticatedRequester: "requester-1", Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	cardRecorded, err := f.requester.RecordResponse(ctx, card.RecordID, cardReply.Message, responderKeys(f.holder, 1))
	require.NoError(t, err)
	_, err = f.requester.DerivedInputs(ctx, cardRecorded.RecordID)
	assert.ErrorContains(t, err, "not a derivation named by the digest")

	// A request record is not a response.
	_, err = f.requester.DerivedInputs(ctx, card.RecordID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestReportFromDerivationRefusesAnUnsignableResult(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, true)
	recorded, _ := f.record(t, "requester-1")
	rewrite := func(edit func(claim map[string]any)) func(DerivedInputs) (json.RawMessage, error) {
		return func(inputs DerivedInputs) (json.RawMessage, error) {
			raw, err := evaluatePack(inputs)
			require.NoError(t, err)
			var doc map[string]any
			require.NoError(t, json.Unmarshal(raw, &doc))
			edit(doc["claims"].([]any)[0].(map[string]any))
			return json.Marshal(doc)
		}
	}
	cases := map[string]func(DerivedInputs) (json.RawMessage, error){
		"evidence is a source record, not the response": rewrite(func(c map[string]any) {
			c["evidence"] = []any{map[string]any{"digest": digestBytes([]byte("a holder record")), "digest_alg": "SHA-256"}}
		}),
		"evidence has two entries": rewrite(func(c map[string]any) {
			c["evidence"] = append(c["evidence"].([]any), map[string]any{"digest": digestBytes([]byte("x")), "digest_alg": "SHA-256"})
		}),
		"a claim reports another contract": rewrite(func(c map[string]any) { c["contract_ref"] = "ec:another@1" }),
		"not an Evidence Result":           func(DerivedInputs) (json.RawMessage, error) { return json.RawMessage(`{"claims":[]}`), nil },
	}
	for name, evaluate := range cases {
		_, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluate, ReportOptions{})
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
	_, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, func(DerivedInputs) (json.RawMessage, error) { return nil, os.ErrInvalid }, ReportOptions{})
	assert.ErrorIs(t, err, os.ErrInvalid)
	// Nothing was signed for any of them.
	f.requester.mu.Lock()
	log := f.requester.logLocked()
	f.requester.mu.Unlock()
	for _, r := range log {
		assert.NotEqual(t, RecordTypeEvidenceResult, r.Header.RecordType)
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func mapValues(m map[string][]byte) [][]byte {
	values := make([][]byte, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}
