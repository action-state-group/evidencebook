// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
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
	sent, err := f.requester.Request(context.Background(), EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: f.id, Coverage: Coverage{ExpectedPin: f.pin}, Nonce: "00000000000000000000000000000000"}, "book-a")
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

	report, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{Derivation: f.id})
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
	again, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{Derivation: f.id})
	require.NoError(t, err)
	assert.Equal(t, report.Result.RecordID, again.Result.RecordID)

	// The report module renders the committed bundle, so the bundle this
	// producer builds must be that file, byte for byte: a producer change that
	// breaks rendering shows here. EB_WRITE_DERIVED_REPORT_FIXTURE=1 rewrites it.
	fixture := filepath.Join("report", "testdata", "derived-report", "bundle.json")
	if os.Getenv("EB_WRITE_DERIVED_REPORT_FIXTURE") != "" {
		require.NoError(t, os.WriteFile(fixture, report.Bundle.JSON, 0o644))
	}
	committed, err := os.ReadFile(fixture)
	require.NoError(t, err)
	assert.Equal(t, string(committed), string(report.Bundle.JSON), "report/testdata/derived-report/bundle.json differs from what the producer builds")

	// The Result's verdicts, one per row of the pack.
	var result struct {
		Claims []struct{ ID, Verdict string } `json:"claims"`
	}
	require.NoError(t, json.Unmarshal(report.Result.Header.Statement, &result))
	verdicts := map[string]string{}
	for _, c := range result.Claims {
		verdicts[c.ID] = c.Verdict
	}
	assert.Equal(t, map[string]string{"has-observation": "met", "has-claim": "met", "has-ruling": "met", "has-exchange": "met", "all-committed": "met"}, verdicts)
}

func TestTermsPackWithoutProofsIsHolderWordOnly(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, false)
	recorded, statement := f.record(t, "requester-1")
	assert.Equal(t, []string{"anchor_inclusion"}, statement.NotChecked)
	inputs, err := f.requester.DerivedInputs(ctx, recorded.RecordID)
	require.NoError(t, err)
	assert.False(t, inputs.Proven)

	// A report over such inputs is refused unless the caller says so, and
	// then its page says so in its title: the two never look the same.
	_, err = f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{Derivation: f.id})
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{Derivation: f.id, AllowUnproven: true})
	assert.ErrorContains(t, err, UnprovenInputsTitle, "a Result without the title is refused")
	titled := func(inputs DerivedInputs) (json.RawMessage, error) {
		raw, err := evaluatePack(inputs)
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		doc["view"] = map[string]any{"title": UnprovenInputsTitle + "Terms pack test", "spec_version": "presentation/v1", "producer_name": "requester"}
		return json.Marshal(doc)
	}
	report, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, titled, ReportOptions{Derivation: f.id, AllowUnproven: true})
	require.NoError(t, err)
	_, err = VerifyBundle(report.Bundle.JSON)
	require.NoError(t, err)
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
		_, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluate, ReportOptions{Derivation: f.id})
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
	_, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, func(DerivedInputs) (json.RawMessage, error) { return nil, os.ErrInvalid }, ReportOptions{Derivation: f.id})
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

func TestReportFromDerivationHoldsTheResultToTheGate(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, true)
	recorded, _ := f.record(t, "requester-1")
	edit := func(change func(doc map[string]any)) func(DerivedInputs) (json.RawMessage, error) {
		return func(inputs DerivedInputs) (json.RawMessage, error) {
			raw, err := evaluatePack(inputs)
			require.NoError(t, err)
			var doc map[string]any
			require.NoError(t, json.Unmarshal(raw, &doc))
			change(doc)
			return json.Marshal(doc)
		}
	}
	claims := func(doc map[string]any) []any { return doc["claims"].([]any) }
	cases := map[string]func(DerivedInputs) (json.RawMessage, error){
		"a claim cites only a contract and its evidence": func(inputs DerivedInputs) (json.RawMessage, error) {
			return json.Marshal(map[string]any{"claims": []any{map[string]any{"contract_ref": packContract, "evidence": []any{map[string]any{"digest": inputs.Response}}}}})
		},
		"no generated_at": edit(func(doc map[string]any) { delete(doc, "generated_at") }),
		"duplicate claim ids": edit(func(doc map[string]any) {
			claims(doc)[1].(map[string]any)["id"] = claims(doc)[0].(map[string]any)["id"]
		}),
		"a claim in the wrong bucket": edit(func(doc map[string]any) {
			doc["aggregate"].(map[string]any)["buckets"] = map[string]any{"met": []any{}, "not_met": []any{"has-observation"}, "not_evaluable": []any{}}
		}),
	}
	for name, evaluate := range cases {
		_, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluate, ReportOptions{Derivation: f.id})
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
	_, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{Derivation: digestBytes([]byte("another pack"))})
	assert.ErrorIs(t, err, ErrInvalid, "a Result for one pack is never signed over another's answer")
	_, err = f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{})
	assert.ErrorIs(t, err, ErrInvalid, "the pack the evaluator runs must be named")
	f.requester.mu.Lock()
	log := f.requester.logLocked()
	f.requester.mu.Unlock()
	for _, r := range log {
		assert.NotEqual(t, RecordTypeEvidenceResult, r.Header.RecordType, "nothing was signed")
	}
}

func TestReportFromDerivationSealsOnceUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, true)
	recorded, _ := f.record(t, "requester-1")
	const callers = 8
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{Derivation: f.id})
			ids[i], errs[i] = report.Result.RecordID, err
		}()
	}
	wg.Wait()
	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, ids[0], ids[i])
	}
	f.requester.mu.Lock()
	log := f.requester.logLocked()
	f.requester.mu.Unlock()
	results := 0
	for _, r := range log {
		if r.Header.RecordType == RecordTypeEvidenceResult {
			results++
		}
	}
	assert.Equal(t, 1, results)
}

// An answer that verified under no key the requester pins is recorded as
// received-and-failed, and is not inputs.
func TestDerivedInputsRefusesARecordedFailedAnswer(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, true)
	sent, reply := f.ask(t, "requester-1")
	keys := responderKeys(f.holder, 1)
	keys.Checkpoint = responderKeys(f.holder, 2).Checkpoint
	recorded, err := f.requester.RecordResponse(ctx, sent.RecordID, reply.Message, keys)
	require.NoError(t, err)
	var statement ResponseStatement
	require.NoError(t, json.Unmarshal(recorded.Header.Statement, &statement))
	require.Equal(t, OutcomeArtifactFailed, statement.Outcome)
	_, err = f.requester.DerivedInputs(ctx, recorded.RecordID)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = f.requester.ReportFromDerivation(ctx, recorded.RecordID, evaluatePack, ReportOptions{Derivation: f.id})
	assert.ErrorIs(t, err, ErrInvalid)
}

// A request that pins the requester's own exchange half is answered with
// that digest as the envelope's anchor and the checkpoint in the artifact;
// the proofs are checked under the checkpoint.
func TestTermsPackOverAnExchangeHalfPin(t *testing.T) {
	ctx := context.Background()
	holder := newFixture(t, "book-h", 3).open(t)
	half := digestBytes([]byte("a half many records cite"))
	for range 2 {
		mustAppend(t, holder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: half}}})
	}
	anchorPin(t, holder)
	id, err := holder.RegisterDerivation(DerivationDefinition{Document: []byte(`{"pack":"half"}`), Recipients: []string{"requester-1"}, Proofs: true, Forms: []SubjectKind{SubjectExchange}, Fields: []string{"record_type"}})
	require.NoError(t, err)
	requester := newFixture(t, "requester", 31).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectExchange, Digest: half}, Derivation: id, Coverage: Coverage{ExpectedPin: half}}, "book-h")
	require.NoError(t, err)
	reply, _, err := holder.Respond(ctx, sent.Message(), RespondOptions{AuthenticatedRequester: "requester-1", Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	require.Nil(t, reply.Refusal, "refused: %+v", reply.Refusal)
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(holder, 3))
	require.NoError(t, err)
	var statement ResponseStatement
	require.NoError(t, json.Unmarshal(recorded.Header.Statement, &statement))
	require.Equal(t, OutcomeArtifact, statement.Outcome, statement.Failure)
	inputs, err := requester.DerivedInputs(ctx, recorded.RecordID)
	require.NoError(t, err)
	assert.True(t, inputs.Proven)
	assert.Len(t, inputs.Records, 2)
	assert.NotEqual(t, half, inputs.Anchor, "the anchor is the checkpoint's, not the exchange half's")
}

// A holder refuses, for a limit, an answer whose proofs would pass the
// requester's item cap: the checkpoint and a proof per record come on top of
// the records.
func TestTermsPackProofsCountAgainstTheItemLimit(t *testing.T) {
	ctx := context.Background()
	serve := func(limit int, proofs bool) (served int, refused bool) {
		f := newFixture(t, "book-l", 5)
		cfg := f.config(t)
		cfg.Responder = ResponderPolicy{RefusalPolicy: RefusalPolicySpecific, MaxAnswerItems: limit}
		book, err := Open(ctx, cfg)
		require.NoError(t, err)
		t.Cleanup(func() { _ = book.Release() })
		for range 3 {
			mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent})
		}
		id, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{"pack":"limit"}`), Recipients: []string{"requester-1"}, Proofs: proofs, Forms: []SubjectKind{SubjectFullHistory}, Fields: []string{"record_type"}})
		require.NoError(t, err)
		reply, _, err := book.Respond(ctx, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: id, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}), RespondOptions{AuthenticatedRequester: "requester-1", Policy: DefaultSharePolicy()})
		require.NoError(t, err)
		if reply.Refusal != nil {
			return 0, true
		}
		return len(artifactRecords(t, mustEnvelope(t, reply))), false
	}
	rows, refused := serve(1000, false)
	require.False(t, refused)
	require.Positive(t, rows)
	_, refused = serve(rows, true)
	assert.True(t, refused, "rows+2 verification entries over a limit of rows")
	_, refused = serve(rows+1, true)
	assert.True(t, refused, "rows+2 over rows+1")
	served, refused := serve(rows+2, true)
	assert.False(t, refused)
	assert.Equal(t, rows, served)
	_, refused = serve(rows, false)
	assert.False(t, refused, "without proofs the limit is the rows alone")
}

// An artifact without a records list is not an empty set: with proofs
// claimed, it is refused.
func TestSetInclusionRefusesAMalformedRecordSet(t *testing.T) {
	req := EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: digestBytes([]byte("pack"))}
	for _, artifact := range []string{`{"anchor":"a"}`, `{"anchor":"a","records":null}`, `{"anchor":"a","records":{}}`} {
		e := Envelope{Anchor: Anchor{Digest: "a"}, Artifact: []byte(artifact), Verification: []VerificationEntry{{Kind: KindCLLCheckpoint, Digest: "d"}}, VerificationContent: [][]byte{[]byte("d")}}
		_, _, err := verifyAnchorInclusion(req, e, hex.EncodeToString(make([]byte, 32)))
		assert.ErrorIs(t, err, ErrInvalid, artifact)
	}
}

// A settlement-shaped report (evidencebook#764): a Result whose claim ids are
// <deal>::<row>, as the inference-deals pack's are, each claim citing the
// holder's response record (here the deal's exchange capsule, the id a node
// serves a receipt by), and the receipt bundle rooted at it.
// ReportFromDerivation makes every claim cite exactly the response record, so
// bundle.json names one deal (d1) and shared-bundle.json two (d1 and d2) that
// cite the same capsule: the case a receipt cannot be mapped to one deal.
// report/receipts_test.go builds pages from these files;
// EB_WRITE_SETTLEMENT_RECEIPTS_FIXTURE=1 rewrites them.
func TestSettlementReceiptsFixture(t *testing.T) {
	ctx := context.Background()
	f := newPackFixture(t, true)
	recorded, _ := f.record(t, "requester-1")
	inputs, err := f.requester.DerivedInputs(ctx, recorded.RecordID)
	require.NoError(t, err)
	dealsOf := func(deals ...string) func(DerivedInputs) (json.RawMessage, error) {
		return func(inputs DerivedInputs) (json.RawMessage, error) {
			evidence := []any{map[string]any{"digest": inputs.Response, "digest_alg": "SHA-256"}}
			var claims []any
			met := []any{}
			for _, deal := range deals {
				id := deal + "::has-observation"
				claims = append(claims, map[string]any{
					"id": id, "contract_ref": packContract, "requirement_ref": "has-observation", "tier": "recomputed", "grade": "self-attested",
					"sufficiency": "SATISFIED", "verdict": "met", "evidence": evidence, "proofs": []any{},
					"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": evidence},
				})
				met = append(met, id)
			}
			return json.Marshal(map[string]any{
				"result_version": "evidence-result-v0", "generated_at": "2026-11-22T00:00:00Z", "claims": claims,
				"aggregate": map[string]any{"buckets": map[string]any{"met": met, "not_met": []any{}, "not_evaluable": []any{}}, "coverage": map[string]any{"evaluated_population": len(deals), "excluded_not_applicable": 0, "unknown_count": 0}},
			})
		}
	}
	built := map[string][]byte{}
	one, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, dealsOf("d1"), ReportOptions{Derivation: f.id})
	require.NoError(t, err)
	built["bundle.json"] = one.Bundle.JSON
	two, err := f.requester.ReportFromDerivation(ctx, recorded.RecordID, dealsOf("d1", "d2"), ReportOptions{Derivation: f.id})
	require.NoError(t, err)
	built["shared-bundle.json"] = two.Bundle.JSON
	receipt, err := f.requester.Bundle(ctx, BundleRequest{Root: inputs.Response})
	require.NoError(t, err)
	built["receipt.json"] = receipt.JSON
	dir := filepath.Join("report", "testdata", "settlement-receipts")
	if os.Getenv("EB_WRITE_SETTLEMENT_RECEIPTS_FIXTURE") != "" {
		require.NoError(t, os.RemoveAll(dir))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for name, data := range built {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o644))
		}
	}
	for name, data := range built {
		committed, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.Equal(t, string(committed), string(data), "%s differs from what the producer builds", filepath.Join(dir, name))
	}
}
