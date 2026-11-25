package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adjudicatorRequest writes the adjudicator's request for deal dealID's full
// copy (derivation deal_adjudicator_copy/1, subject its correlation).
func adjudicatorRequest(t *testing.T, dealID string) string {
	t.Helper()
	v, err := evidencerequest.Request{
		Subject:    evidencerequest.Subject{Kind: evidencerequest.SubjectCorrelation, Correlation: dealID},
		Coverage:   evidencerequest.Coverage{MinFreshness: &evidencerequest.Freshness{Time: "2020-01-01T00:00:00Z"}},
		Derivation: xdealv1.DerivationAdjudicatorCopy,
		Nonce:      "6e6f6e63652d6f662d7468652d72657175657374",
	}.Value()
	require.NoError(t, err)
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

// journalAnswers are the answer records in the current profile's journal.
func journalAnswers(t *testing.T) []xdealv1.AnswerBody {
	t.Helper()
	var out []xdealv1.AnswerBody
	withSession(t, func(s *dealSession) {
		j, records, err := s.journalRecords(t.Context())
		require.NoError(t, err)
		for _, r := range records {
			for _, c := range r.Header.PayloadCommitments {
				payload, err := j.ResolvePayload(t.Context(), r.RecordID, c)
				require.NoError(t, err)
				rec, err := xdealv1.Decode(payload)
				if err != nil {
					continue
				}
				if b, ok := rec.Body.(*xdealv1.AnswerBody); ok {
					out = append(out, *b)
				}
			}
		}
	})
	return out
}

// Acceptance 5 (docs/design/861-four-books.md §3.3), with two real
// capsulectl-deal parties: the buyer disputes, opens the case with its
// adjudicator copy and answers the adjudicator's request in full; the
// seller does not answer. The ruling is gathered from the buyer's own book
// and the copy of the seller's book the buyer held, names those sources and
// the two not shown, and goes against the seller on records it signed. The
// answer is linked by an answer record in the buyer's journal, and the
// seller reads the sources on receive.
func TestAFullAnswerAndAGatheredRuling(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerCopy := adjudicatorCopy(t, h.buyer, h.dealID)

	request := adjudicatorRequest(t, h.dealID)
	answer := filepath.Join(t.TempDir(), "answer.json")
	h.buyer.act(t)
	out := dealRun(t, "respond", "--request", request, "--requester", adj.party.signer, "--output", answer)
	assert.Equal(t, evidencebook.OutcomeArtifact, out["outcome"], "%v", out)

	// The file is the request, the full copy and the facts.
	raw, err := os.ReadFile(answer)
	require.NoError(t, err)
	var a v1Answer
	require.NoError(t, json.Unmarshal(raw, &a))
	reqRaw, err := os.ReadFile(request)
	require.NoError(t, err)
	assert.JSONEq(t, string(reqRaw), string(a.Request))
	cc, err := caseCopyOfBundle(a.Bundle)
	require.NoError(t, err)
	_, full, err := xdealv1.VerifyFullCopy(cc.Report, cc.Extension)
	require.NoError(t, err, "the answer carries a full adjudicator copy")
	assert.NotEmpty(t, full.Received, "with the copy of the seller's book the buyer holds")
	assert.NotEmpty(t, a.Facts.Facts, "and the buyer's book facts")

	// The journal links it.
	answers := journalAnswers(t)
	require.Len(t, answers, 1)
	assert.Equal(t, answerOutcomeEvidence, answers[0].Outcome)
	assert.Equal(t, cli.BytesDigest(reqRaw), answers[0].Request.Digest)
	assert.Equal(t, cli.BytesDigest(raw), answers[0].Answer.Digest)

	adj.act(t)
	rulingFile := filepath.Join(t.TempDir(), "ruling.json")
	ruled := dealRun(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
		"--dispute", dispute, "--copy", buyerCopy, "--answer", answer, "--out", rulingFile)
	assert.Equal(t, true, ruled["sealed"], "%v", ruled)
	read, err := xdealv1.VerifyBook(readPush(t, rulingFile).Record)
	require.NoError(t, err)
	rb := read.Record.Body.(*xdealv1.RulingBody)
	require.NotNil(t, rb.Sources, "a gathered ruling names its sources")
	assert.NotNil(t, rb.Sources.Buyer.Own.V, "the buyer's own book was read")
	assert.Nil(t, rb.Sources.Buyer.HeldBySeller.V, "the seller held no copy shown")
	assert.Nil(t, rb.Sources.Seller.Own.V, "the seller's own book was not shown")
	assert.NotNil(t, rb.Sources.Seller.HeldByBuyer.V, "the copy of the seller's book the buyer held was read")
	books := ruled["books"].(map[string]any)
	seller := books["seller"].(map[string]any)
	assert.Nil(t, seller["own"])
	records := seller["records"].([]any)
	require.NotEmpty(t, records, "the seller's book read is listed with what timed each record")
	assert.Contains(t, records[0], "timed_by")

	h.seller.act(t)
	got := dealRun(t, "receive", rulingFile)
	sources := got["sources"].(map[string]any)
	assert.Equal(t, notShownToAdjudicator, sources["seller"].(map[string]any)["own"])
	assert.Equal(t, notShownToAdjudicator, sources["buyer"].(map[string]any)["held_by_seller"])
	assert.Equal(t, *rb.Sources.Seller.HeldByBuyer.V, sources["seller"].(map[string]any)["held_by_buyer"])
}

// A request is refused, never held: for a deal the profile does not hold
// (no_such_subject, nothing in the journal), and from a requester who is not
// the deal's adjudicator (not_authorized, linked by an answer record of
// outcome refusal).
func TestAnAdjudicatorRequestRefusals(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])

	out := filepath.Join(t.TempDir(), "unknown.json")
	got := dealRun(t, "respond", "--request", adjudicatorRequest(t, "deal-00000000000000ff"), "--requester", adj.party.signer, "--output", out)
	assert.Equal(t, evidencebook.OutcomeRefusal, got["outcome"], "%v", got)
	assert.Equal(t, evidencebook.ReasonNoSuchSubject, got["reason"])
	assert.Empty(t, journalAnswers(t))

	out = filepath.Join(t.TempDir(), "other.json")
	got = dealRun(t, "respond", "--request", adjudicatorRequest(t, h.dealID), "--requester", h.seller.party.signer, "--output", out)
	assert.Equal(t, evidencebook.OutcomeRefusal, got["outcome"], "%v", got)
	assert.Equal(t, evidencebook.ReasonNotAuthorized, got["reason"])
	answers := journalAnswers(t)
	require.Len(t, answers, 1)
	assert.Equal(t, evidencebook.OutcomeRefusal, answers[0].Outcome)
}

// writeRequest writes a request value to a file.
func writeRequest(t *testing.T, r evidencerequest.Request) string {
	t.Helper()
	v, err := r.Value()
	require.NoError(t, err)
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

// A request that names the interop profile is answered in full as well,
// not held; one with an expected_pin is refused coverage_unsatisfiable; and
// a deal whose agreement names no adjudicator is refused not_authorized.
func TestAnAdjudicatorRequestForms(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	base := evidencerequest.Request{
		Subject:    evidencerequest.Subject{Kind: evidencerequest.SubjectCorrelation, Correlation: h.dealID},
		Coverage:   evidencerequest.Coverage{MinFreshness: &evidencerequest.Freshness{Time: "2020-01-01T00:00:00Z"}},
		Derivation: xdealv1.DerivationAdjudicatorCopy,
		Nonce:      "6e6f6e63652d6f662d7468652d72657175657374",
	}
	withProfile := base
	withProfile.Profile = evidencerequest.ProfileID
	got := dealRun(t, "respond", "--request", writeRequest(t, withProfile), "--requester", adj.party.signer, "--output", filepath.Join(t.TempDir(), "a.json"))
	assert.Equal(t, evidencebook.OutcomeArtifact, got["outcome"], "%v", got)

	pinned := base
	pinned.Coverage = evidencerequest.Coverage{ExpectedPin: "sha-256:" + cli.BytesDigest([]byte("x"))}
	if _, err := pinned.Value(); err != nil {
		pinned.Coverage = evidencerequest.Coverage{ExpectedPin: cli.BytesDigest([]byte("x"))}
	}
	got = dealRun(t, "respond", "--request", writeRequest(t, pinned), "--requester", adj.party.signer, "--output", filepath.Join(t.TempDir(), "b.json"))
	assert.Equal(t, evidencebook.ReasonCoverageUnsatisfiable, got["reason"], "%v", got)

	plain := agreedBoth(t, offerTo)
	plain.buyer.act(t)
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	got = dealRun(t, "respond", "--request", adjudicatorRequest(t, plain.dealID), "--requester", adj.party.signer, "--output", filepath.Join(t.TempDir(), "c.json"))
	assert.Equal(t, evidencebook.ReasonNotAuthorized, got["reason"], "%v", got)
}

// Both parties answer and no case-opening copy is given: the ruling is
// gathered from the four books, each side read from its own book and the
// copy the other side held. An answer for another deal is refused.
func TestARulingFromTwoAnswersOnly(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	request := adjudicatorRequest(t, h.dealID)
	var answers []string
	for _, side := range []v1Side{h.buyer, h.seller} {
		side.act(t)
		clockAt(t, cli.DealClock().Add(72*time.Hour))
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		file := filepath.Join(t.TempDir(), "answer.json")
		dealRun(t, "respond", "--request", request, "--requester", adj.party.signer, "--output", file)
		answers = append(answers, file)
	}
	adj.act(t)
	ruled := dealRun(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "met", "--against", "none",
		"--dispute", dispute, "--answer", answers[0], "--answer", answers[1], "--out", filepath.Join(t.TempDir(), "ruling.json"))
	src := ruled["sources"].(map[string]any)
	for _, role := range []string{"buyer", "seller"} {
		side := src[role].(map[string]any)
		assert.NotNil(t, side["own"], "%s: own book shown", role)
	}
	assert.NotNil(t, src["buyer"].(map[string]any)["held_by_seller"])
	assert.NotNil(t, src["seller"].(map[string]any)["held_by_buyer"])

	// An answer whose request is of another deal.
	other := filepath.Join(t.TempDir(), "other.json")
	raw, err := os.ReadFile(answers[0])
	require.NoError(t, err)
	var a map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &a))
	otherReq, err := os.ReadFile(adjudicatorRequest(t, "deal-00000000000000ff"))
	require.NoError(t, err)
	a["request"] = otherReq
	b, err := json.Marshal(a)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(other, b, 0o600))
	assert.Contains(t, v1Err(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "met", "--against", "none",
		"--dispute", dispute, "--answer", other), "another deal")
}

// A full copy's own records and held copy name their shelf logs too, and
// v1OwnIDs takes them out before the share gate checks.
func TestV1OwnIDsReadsAFullCopy(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	_ = adj
	h.buyer.act(t)
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), h.dealID)
		require.NoError(t, err)
		prov := &xdealv1.StatusProvider{Shelf: s.shelf, OfferPair: s.sentOfferPair}
		snap, err := prov.ReportSnapshot(t.Context(), d.OwnLogID, xdealv1.ReportCopy{Audience: "adjudicator", Referee: "r", Nonce: strings.Repeat("0", 64), AsOf: cli.DealClock().UTC().Format("2006-01-02T15:04:05Z"), Full: true})
		require.NoError(t, err)
		require.NotNil(t, snap.Book.Received)
		ids := v1OwnIDs(h.dealID, xdealv1.ReportBook{Received: snap.Book.Received})
		assert.Len(t, ids, 2, "the deal id and the other side's shelf log, from the held copy alone: %v", ids)
	})
}
