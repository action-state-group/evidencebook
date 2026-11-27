// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/require"
)

// holderTranscripts are the receive profile's and act gate's scenarios
// (receive_test.go, gate_test.go) as transcripts (testdata/deal-v1/holder):
// each step a party takes, with what Go gave. Python replays each on its
// own shelves with the same keys and clock and compares every output
// (python/tests/test_deal_v1_holder.py).
const holderTranscripts = "../../testdata/deal-v1/holder/transcripts.json"

// txOp is one step of a transcript.
type txOp struct {
	Op      string                     `json:"op"`
	Party   string                     `json:"party,omitempty"`
	Log     string                     `json:"log,omitempty"`
	First   string                     `json:"first,omitempty"`
	Record  json.RawMessage            `json:"record,omitempty"`
	Members map[string]json.RawMessage `json:"members,omitempty"`
	Pair    string                     `json:"pair,omitempty"` // hex of the bytes received ("" for none)
	Proof   *evidencebook.StampProof   `json:"proof,omitempty"`
	In      any                        `json:"in,omitempty"`
	Out     any                        `json:"out,omitempty"`
}

// holderTx is the transcript being recorded; nil when none is.
var holderTx *[]txOp

func recordStep(op txOp) {
	if holderTx != nil {
		*holderTx = append(*holderTx, op)
	}
}

func rawOf(t *testing.T, r Record) json.RawMessage { return jcsOf(t, r) }

func errText(err error) any {
	if err == nil {
		return nil
	}
	return true
}

func (p *party) receiveRaw(rec []byte, proof *evidencebook.StampProof) (evidencebook.ReceiveResult, error) {
	res, err := p.shelf.Receive(context.Background(), rec, proof)
	// Receive returns its result with an error joining the refusals; an
	// error with no result is the store's own.
	out := map[string]any{"error": err != nil}
	{
		if r := res.Record; r != nil {
			out["record"] = map[string]any{"digest": r.Digest, "deal_id": r.DealID, "stored": r.Stored,
				"duplicate": r.Duplicate, "deleted": r.Deleted, "counted": r.Counted, "ask_for_book": r.AskForBook,
				"refused": r.Refused != nil}
		}
		if q := res.Proof; q != nil {
			out["proof"] = map[string]any{"accepted": q.Accepted, "duplicate": q.Duplicate, "refused": q.Refused != nil}
		}
		out["changes"] = map[string]any{"grew": res.Changes.Grew, "shrank": res.Changes.Shrank,
			"forked": res.Changes.Forked, "covered": res.Changes.Covered}
	}
	op := txOp{Op: "receive", Party: p.name, Proof: proof, Out: out}
	if rec != nil {
		op.Pair = hex.EncodeToString(rec)
	}
	recordStep(op)
	return res, err
}

func (p *party) recCanAct(in CanActInput) (bool, error) {
	ok, err := p.profile.CanAct(context.Background(), in)
	recordStep(txOp{Op: "can_act", Party: p.name, In: in, Out: map[string]any{"ok": ok, "error": errText(err)}})
	return ok, err
}

// recRetention sets the retention state of the holder's journal record at
// seq, recorded with the lifecycle record it seals.
func (p *party) recRetention(seq uint64, state evidencebook.RetentionState) {
	p.recRetentionIn("", seq, state)
}

// recRetentionIn is recRetention in the book logID ("" for the journal).
func (p *party) recRetentionIn(logID string, seq uint64, state evidencebook.RetentionState) {
	ctx := context.Background()
	book, err := p.shelf.Journal(ctx)
	if logID != "" {
		book, err = p.shelf.Open(ctx, logID)
	}
	require.NoError(p.t, err)
	recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: seq, ToSeq: seq})
	require.NoError(p.t, err)
	require.Len(p.t, recs, 1)
	rec, err := book.SetRetention(ctx, recs[0].RecordID, state, "test")
	require.NoError(p.t, err)
	recordStep(txOp{Op: "retention", Party: p.name, Log: logID, In: map[string]any{"seq": seq, "state": state, "reason": "test"},
		Out: rec.RecordID})
}

// recBook records the party's book logID as it is now: each record's id
// and, for a record that reads as x-deal-v1, its book pair (hex).
func (p *party) recBook(logID string) {
	ctx := context.Background()
	book, err := p.shelf.Open(ctx, logID)
	require.NoError(p.t, err)
	recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: book.Size()})
	require.NoError(p.t, err)
	out := []map[string]string{}
	for _, r := range recs {
		entry := map[string]string{"record_id": r.RecordID}
		if rec, kind, err := v1Payload(ctx, book, r); err == nil && kind == payloadV1 {
			sealed, err := book.Sealed(ctx, r.RecordID)
			require.NoError(p.t, err)
			payload, _, err := Encode(rec)
			require.NoError(p.t, err)
			pair, err := BookPair(sealed, payload)
			require.NoError(p.t, err)
			entry["pair"] = hex.EncodeToString(pair)
		}
		out = append(out, entry)
	}
	recordStep(txOp{Op: "book", Party: p.name, Log: logID, Out: out})
}

// recNameRulings is NameRulings over deals (NameAllRulings for nil),
// recorded with the record ids it sealed.
func (p *party) recNameRulings(deals []string) ([]string, error) {
	var ids []string
	var err error
	if deals == nil {
		ids, err = NameAllRulings(context.Background(), p.shelf, p.now)
	} else {
		ids, err = NameRulings(context.Background(), p.shelf, deals, p.now)
	}
	recordStep(txOp{Op: "name_rulings", Party: p.name, In: deals, Out: map[string]any{"sealed": ids, "error": errText(err)}})
	return ids, err
}

// recUseStatus gives the party a StatusProvider, whose OfferPair answers
// from pairs (by offer digest), and reopens its shelf on it.
func (p *party) recUseStatus(pairs map[string][]byte) {
	in := map[string]string{}
	for d, pair := range pairs {
		in[d] = hex.EncodeToString(pair)
	}
	recordStep(txOp{Op: "use_status", Party: p.name, In: in})
	p.status = &StatusProvider{OfferPair: func(_ context.Context, digest string) ([]byte, error) {
		return pairs[digest], nil
	}}
	p.reopen()
}

// recStampTick takes a stamp at the party's next minute, recorded with its
// tick and what its status provider reported.
func (p *party) recStampTick() (evidencebook.TickResult, error) {
	p.tick()
	res, err := p.cadence.Stamp(context.Background(), p.now)
	unstated := res.Unstated
	if unstated == nil {
		unstated = map[string]string{}
	}
	recordStep(txOp{Op: "stamp_tick", Party: p.name, Out: map[string]any{"n": res.Tick.N, "before_stamp": res.BeforeStamp,
		"unstated": unstated, "error": errText(err)}})
	return res, err
}

// recOpenings records every fact of the book bookID with the openings the
// audience may have.
func (p *party) recOpenings(bookID string, audience evidencebook.Audience) []evidencebook.AudienceFact {
	facts, err := p.shelf.Openings(context.Background(), bookID, audience)
	require.NoError(p.t, err)
	out := []map[string]any{}
	for _, f := range facts {
		out = append(out, map[string]any{"entry_seq": f.EntrySeq, "fact": f.Fact, "link": f.LinkOpening, "status": f.StatusOpening})
	}
	recordStep(txOp{Op: "openings", Party: p.name, In: []string{bookID, string(audience)}, Out: out})
	return facts
}

// recReport is the party's StatusProvider's Report, recorded.
func (p *party) recReport(logID string, c ReportCopy) (Report, error) {
	r, err := p.status.Report(context.Background(), logID, c)
	var out any
	if err == nil {
		out = r
	}
	recordStep(txOp{Op: "report", Party: p.name, Log: logID,
		In:  map[string]string{"audience": c.Audience, "nonce": c.Nonce, "referee": c.Referee, "as_of": c.AsOf},
		Out: map[string]any{"report": out, "error": errText(err)}})
	return r, err
}

// recNamingFails makes every naming fail ("the disk is full") until it is
// called with false, recorded for the replay to do the same.
func recNamingFails(t *testing.T, fail bool) {
	nameRulingsBeforeAppend = nil
	if fail {
		nameRulingsBeforeAppend = func() error { return errors.New("the disk is full") }
		t.Cleanup(func() { nameRulingsBeforeAppend = nil })
	}
	recordStep(txOp{Op: "naming_fails", In: fail})
}

func (p *party) recProofSender(checkpointKeyID, logID string) (string, bool, error) {
	sender, ok, err := p.profile.ProofSender(context.Background(), checkpointKeyID, logID)
	recordStep(txOp{Op: "proof_sender", Party: p.name, In: []string{checkpointKeyID, logID},
		Out: map[string]any{"sender": sender, "ok": ok, "error": errText(err)}})
	return sender, ok, err
}

func (p *party) recAnswered(offer evidencebook.ReceivedRecord) (bool, error) {
	ok, err := p.profile.Answered(context.Background(), offer)
	recordStep(txOp{Op: "answered", Party: p.name, In: offer.Digest, Out: map[string]any{"ok": ok, "error": errText(err)}})
	return ok, err
}

func (p *party) recGateAct(in ActGateInput) (ActGate, error) {
	g, err := p.profile.GateAct(context.Background(), in)
	own := []json.RawMessage{}
	for _, r := range in.Own {
		own = append(own, rawOf(p.t, r))
	}
	recordStep(txOp{Op: "gate", Party: p.name, In: map[string]any{"own": own, "act": rawOf(p.t, in.Act),
		"counterpart_agreement": in.CounterpartAgreement, "commitment_shelf_log": in.CommitmentShelfLog},
		Out: map[string]any{"gate": g, "error": errText(err)}})
	return g, err
}

// TestHolderTranscripts writes and checks testdata/deal-v1/holder
// (EB_WRITE_DEAL_V1_VECTORS=1): each scenario run with its steps recorded.
// The scenarios are unexported (testX), so `go test` runs each once, here
// (evidencebook#1056); run one alone with -run 'TestHolderTranscripts/<name>'.
// TestBookPairWithTwoPayloads, which appends a book record by
// hand, and TestOursResultIsACopy, which reads the profile's cache, are Go's
// alone; Python tests both of those on its own. So is
// TestNameRulingsAfterAConcurrentWrite, whose write lands from inside the
// naming (Python ports it on its own).
func TestHolderTranscripts(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{"handshake-from-offer", testHandshakeFromOffer}, {"no-proof-no-act", testNoProofNoAct},
		{"proof-under-another-shelf-log", testProofUnderAnotherShelfLog},
		{"proof-by-another-checkpoint-key", testProofByAnotherCheckpointKey},
		{"genesis-fork-after-proof", testGenesisForkAfterProof}, {"proof-sender-both-sides", testProofSenderBothSides},
		{"answered", testAnswered}, {"admit-refuses-strangers", testAdmitRefusesStrangers}, {"later-records", testLaterRecords},
		{"agreement-on-a-counter-offer", testAgreementOnACounterOffer}, {"cancellation", testCancellation},
		{"ruling-from-the-adjudicator", testRulingFromTheAdjudicator}, {"shared-log-id", testSharedLogID},
		{"proof-the-records-do-not-recompute", testProofTheRecordsDoNotRecompute},
		{"gate-holds-the-first-act", testGateActHoldsTheFirstAct}, {"gate-without-a-layer", testGateActWithoutALayerIsUnguarded},
		{"gate-refuses-a-proof-of-another-shelf", testGateActRefusesAProofOfAnotherShelf},
		{"gate-refuses-another-book", testGateActRefusesAnotherBook}, {"gate-holds-the-acceptor", testGateActHoldsTheAcceptor},
		{"gate-without-witnesses-and-an-agreement", testGateActWithoutWitnessesAndWithoutAnAgreement},
		{"gate-refuses-the-counterpartys-agreement", testGateActRefusesTheCounterpartysAgreement},
		{"withheld-offer-read-again", testWithheldOfferReadAgain}, {"can-act-without-a-layer", testCanActWithoutALayerIsFalse},
		{"name-rulings", testNameRulings}, {"name-rulings-block", testNameRulingsBlock},
		{"name-rulings-leaves-a-part-book", testNameRulingsLeavesAPartBook},
		{"name-rulings-only-the-deals-rulings", testNameRulingsOnlyTheDealsRulings},
		{"name-rulings-retried-on-a-duplicate", testNameRulingsRetriedOnADuplicate}, {"name-all-rulings", testNameAllRulings},
		{"status-provider-at-a-stamp", testStatusProviderAtAStamp}, {"status-provider-report", testStatusProviderReport},
		{"name-rulings-at-the-next-stamp", testNameRulingsAtTheNextStamp},
		{"name-rulings-unreadable-book-is-counted", testNameRulingsUnreadableBookIsCounted},
	}
	type transcript struct {
		Name  string `json:"name"`
		Steps []txOp `json:"steps"`
	}
	var out []transcript
	ran := map[string]bool{}
	for _, sc := range scenarios {
		var steps []txOp
		holderTx = &steps
		t.Run(sc.name, func(t *testing.T) { ran[sc.name] = true; sc.run(t) })
		holderTx = nil
		out = append(out, transcript{Name: sc.name, Steps: steps})
	}
	if len(ran) < len(scenarios) {
		// A -run filter ran only some scenarios: check each against its
		// transcript in the file, which only a full run writes.
		require.NotEqual(t, "1", os.Getenv("EB_WRITE_DEAL_V1_VECTORS"), "writing the transcripts needs every scenario")
		raw, err := os.ReadFile(holderTranscripts)
		require.NoError(t, err)
		var file struct {
			Transcripts []struct {
				Name  string `json:"name"`
				Steps any    `json:"steps"`
			} `json:"transcripts"`
		}
		require.NoError(t, json.Unmarshal(raw, &file))
		want := map[string]any{}
		for _, tr := range file.Transcripts {
			want[tr.Name] = tr.Steps
		}
		for _, tr := range out {
			if !ran[tr.Name] {
				continue
			}
			b, err := json.Marshal(tr.Steps)
			require.NoError(t, err)
			var got any
			require.NoError(t, json.Unmarshal(b, &got))
			require.Contains(t, want, tr.Name)
			require.Equal(t, want[tr.Name], got, "%s: transcript %s differs", holderTranscripts, tr.Name)
		}
		return
	}
	b, err := json.MarshalIndent(map[string]any{
		"description": "The x-deal-v1 receive profile and act gate as transcripts (evidencebook#461): each scenario of dealprofile/xdealv1's receive_test.go and gate_test.go, step by step, with what Go gave. A party's shelf has the record key, checkpoint key and operator of its name, the clock at 2026-11-01T09:00:00Z advancing a minute at each tick, and a migrated cadence on deal-cadence/0123456789abcdef; reopen_as reopens it on a new file with another party's record key. Written by TestHolderTranscripts (EB_WRITE_DEAL_V1_VECTORS=1).",
		"transcripts": out,
	}, "", " ")
	require.NoError(t, err)
	b = append(b, '\n')
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll("../../testdata/deal-v1/holder", 0o755))
		require.NoError(t, os.WriteFile(holderTranscripts, b, 0o644))
	}
	want, err := os.ReadFile(holderTranscripts)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, b), "%s differs; rerun with EB_WRITE_DEAL_V1_VECTORS=1", holderTranscripts)
}
