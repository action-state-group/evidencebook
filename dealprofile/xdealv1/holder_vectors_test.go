// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
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
// (EB_WRITE_DEAL_V1_VECTORS=1): each scenario run again with its steps
// recorded. The scenarios that change a shelf otherwise than through the
// recorded steps (a journal record withheld, a book record with two
// payloads) are Go's alone.
func TestHolderTranscripts(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{"handshake-from-offer", TestHandshakeFromOffer}, {"no-proof-no-act", TestNoProofNoAct},
		{"proof-under-another-shelf-log", TestProofUnderAnotherShelfLog},
		{"proof-by-another-checkpoint-key", TestProofByAnotherCheckpointKey},
		{"genesis-fork-after-proof", TestGenesisForkAfterProof}, {"proof-sender-both-sides", TestProofSenderBothSides},
		{"answered", TestAnswered}, {"admit-refuses-strangers", TestAdmitRefusesStrangers}, {"later-records", TestLaterRecords},
		{"agreement-on-a-counter-offer", TestAgreementOnACounterOffer}, {"cancellation", TestCancellation},
		{"ruling-from-the-adjudicator", TestRulingFromTheAdjudicator}, {"shared-log-id", TestSharedLogID},
		{"proof-the-records-do-not-recompute", TestProofTheRecordsDoNotRecompute},
		{"gate-holds-the-first-act", TestGateActHoldsTheFirstAct}, {"gate-without-a-layer", TestGateActWithoutALayerIsUnguarded},
		{"gate-refuses-a-proof-of-another-shelf", TestGateActRefusesAProofOfAnotherShelf},
		{"gate-refuses-another-book", TestGateActRefusesAnotherBook}, {"gate-holds-the-acceptor", TestGateActHoldsTheAcceptor},
		{"gate-without-witnesses-and-an-agreement", TestGateActWithoutWitnessesAndWithoutAnAgreement},
		{"gate-refuses-the-counterpartys-agreement", TestGateActRefusesTheCounterpartysAgreement},
	}
	type transcript struct {
		Name  string `json:"name"`
		Steps []txOp `json:"steps"`
	}
	var out []transcript
	for _, sc := range scenarios {
		var steps []txOp
		holderTx = &steps
		t.Run(sc.name, sc.run)
		holderTx = nil
		out = append(out, transcript{Name: sc.name, Steps: steps})
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
