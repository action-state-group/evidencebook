package evidencebook

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/cll-go/checkpoint"
	"github.com/fxamacker/cbor/v2"
	"github.com/veraison/go-cose"
)

const testWitnessURL = "https://witness.test"

// testWitness is the local test witness's key, and a requester directory
// that lists it.
func testWitness() (ed25519.PrivateKey, []WitnessRow) {
	key := ed25519.NewKeyFromSeed(seededKey(77)[:32])
	return key, []WitnessRow{{Name: "test witness", Endpoint: testWitnessURL, KeyIDs: []string{hex.EncodeToString(key.Public().(ed25519.PublicKey))}}}
}

// mintReceipt returns a checkpoint.witnesses entry: a genuine RFC 9162 COSE
// receipt over the checkpoint statement, signed by a local test witness key
// (a single-entry log, so the inclusion path is empty). Adapted from
// cll-go's witness/verify_test.go signedReceipt.
func mintReceipt(t *testing.T, statement []byte, witnessKey ed25519.PrivateKey) map[string]any {
	t.Helper()
	record, err := checkpoint.ParseRecord(statement)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := record.EntryHash()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := cbor.Marshal([]any{int64(1), int64(0), [][]byte{}})
	if err != nil {
		t.Fatal(err)
	}
	leaf := sha256.Sum256(append([]byte{0}, entry...))
	message := cose.NewSign1Message()
	message.Headers.Protected.SetAlgorithm(cose.AlgorithmEdDSA)
	message.Headers.Protected[int64(395)] = int64(1)
	message.Headers.Unprotected[int64(396)] = map[any]any{int64(-1): []any{proof}}
	message.Payload = leaf[:]
	signer, err := cose.NewSigner(cose.AlgorithmEdDSA, witnessKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := message.Sign(rand.Reader, nil, signer); err != nil {
		t.Fatal(err)
	}
	message.Payload = nil
	raw, err := message.MarshalCBOR()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"ts_url": testWitnessURL, "entry_hash": hex.EncodeToString(entry),
		"receipt_b64": base64.StdEncoding.EncodeToString(raw), "leaf_index": 0, "tree_size": 1,
	}
}

// receiptCase is a responder answering a full-history request, and the
// requester about to record it.
type receiptCase struct {
	ctx       context.Context
	responder *Book
	requester *Book
	keys      ResponderKeys
}

func newReceiptCase(t *testing.T) receiptCase {
	t.Helper()
	responder, _ := linkedBook(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	return receiptCase{ctx: context.Background(), responder: responder, requester: newFixture(t, "requester", 30).open(t), keys: responderKeys(responder, 1)}
}

// answer asks for the full history, lets the responder answer, applies
// change to the bundle's checkpoint member (re-signing the response with the
// responder's own key, so only the bundle can fail), and records it.
func (c receiptCase) answer(t *testing.T, change func(checkpoint map[string]any, statement []byte)) ResponseStatement {
	t.Helper()
	// Fresh at the responder's whole log, so a grown log gets a new checkpoint.
	coverage := Coverage{MinFreshness: &Freshness{Size: c.responder.Size()}}
	sent, err := c.requester.Request(c.ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: coverage}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	resp := respond(t, c.responder, sent.Bytes, "requester")
	if change != nil {
		a := resp.Artifact
		var bundle map[string]any
		if err := json.Unmarshal(a.Artifact, &bundle); err != nil {
			t.Fatal(err)
		}
		member := bundle["checkpoint"].(map[string]any)
		statement, err := base64.RawURLEncoding.DecodeString(member["cose"].(string))
		if err != nil {
			t.Fatal(err)
		}
		change(member, statement)
		if a.Artifact, err = json.Marshal(bundle); err != nil {
			t.Fatal(err)
		}
		a.ArtifactDigest = digestBytes(a.Artifact)
		body, err := a.SigningBody()
		if err != nil {
			t.Fatal(err)
		}
		sig, err := c.responder.signer.Sign(c.ctx, body)
		if err != nil {
			t.Fatal(err)
		}
		a.Sig = hex.EncodeToString(sig)
	}
	recorded, err := c.requester.RecordResponse(c.ctx, sent.RecordID, resp, c.keys)
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	return statement
}

// A history answer carrying a witness receipt whose bytes are bogus is
// recorded as a failed artifact, never as a grant, even when the requester's
// directory lists the witness.
func TestAnswerWithBogusWitnessReceiptIsAFailedArtifact(t *testing.T) {
	c := newReceiptCase(t)
	witnessKey, directory := testWitness()
	c.keys.Witnesses = directory
	got := c.answer(t, func(member map[string]any, statement []byte) {
		entry := mintReceipt(t, statement, witnessKey)
		entry["receipt_b64"] = base64.StdEncoding.EncodeToString([]byte("bogus receipt bytes"))
		member["witnesses"] = []any{entry}
	})
	if got.Outcome != OutcomeArtifactFailed || !strings.Contains(got.Failure, "witness receipt 0") {
		t.Fatalf("bogus witness receipt: recorded %q, want %q (%+v)", got.Outcome, OutcomeArtifactFailed, got)
	}
}

// A genuine receipt, signed by the witness, but for another checkpoint of
// the same log: it does not cover the anchor, so the answer is a failed
// artifact.
func TestAnswerWithReceiptForAnotherCheckpointIsAFailedArtifact(t *testing.T) {
	c := newReceiptCase(t)
	// A first answer issues a checkpoint; the log then grows, so the next
	// answer is anchored at a later one.
	c.answer(t, nil)
	mustAppend(t, c.responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	history, err := c.responder.store.Checkpoints(c.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("the responder has issued no checkpoint yet")
	}
	earlier := history[0]
	witnessKey, directory := testWitness()
	c.keys.Witnesses = directory
	got := c.answer(t, func(member map[string]any, statement []byte) {
		if string(statement) == string(earlier.Statement) {
			t.Fatal("the answer is anchored at the earlier checkpoint; the case needs a later one")
		}
		member["witnesses"] = []any{mintReceipt(t, earlier.Statement, witnessKey)}
	})
	if got.Outcome != OutcomeArtifactFailed || !strings.Contains(got.Failure, "entry hash does not bind signed checkpoint") {
		t.Fatalf("receipt for another checkpoint: recorded %q, want %q (%+v)", got.Outcome, OutcomeArtifactFailed, got)
	}
}

// A genuine receipt for the answer's own checkpoint: a grant when the
// requester's directory lists the witness; a failed artifact when the
// requester gave no directory, or one that does not list it. An answer that
// carries no receipt is a grant either way: how many receipts a requester
// needs is its own policy.
func TestAnswerWithReceiptForItsCheckpoint(t *testing.T) {
	witnessKey, directory := testWitness()
	carry := func(member map[string]any, statement []byte) {
		member["witnesses"] = []any{mintReceipt(t, statement, witnessKey)}
	}
	other := []WitnessRow{{Name: "another witness", Endpoint: "https://other.test", KeyIDs: directory[0].KeyIDs}}
	for name, tc := range map[string]struct {
		directory []WitnessRow
		change    func(map[string]any, []byte)
		want      string
	}{
		"listed witness":     {directory, carry, OutcomeArtifact},
		"no directory":       {nil, carry, OutcomeArtifactFailed},
		"unlisted witness":   {other, carry, OutcomeArtifactFailed},
		"no receipt, listed": {directory, nil, OutcomeArtifact},
		"no receipt, none":   {nil, nil, OutcomeArtifact},
	} {
		t.Run(name, func(t *testing.T) {
			c := newReceiptCase(t)
			c.keys.Witnesses = tc.directory
			if got := c.answer(t, tc.change); got.Outcome != tc.want {
				t.Fatalf("recorded %q, want %q (%+v)", got.Outcome, tc.want, got)
			}
		})
	}
}

// A witnesses member that is not an array (a receipt object on its own, a
// string, or null) is malformed, not absent: the answer is a failed
// artifact, never a grant with its receipts skipped.
func TestAnswerWithNonArrayWitnessesIsAFailedArtifact(t *testing.T) {
	witnessKey, directory := testWitness()
	for name, value := range map[string]func([]byte) any{
		"object": func(statement []byte) any { return mintReceipt(t, statement, witnessKey) },
		"string": func([]byte) any { return "bogus" },
		"null":   func([]byte) any { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := newReceiptCase(t)
			c.keys.Witnesses = directory
			got := c.answer(t, func(member map[string]any, statement []byte) {
				member["witnesses"] = value(statement)
			})
			if got.Outcome != OutcomeArtifactFailed {
				t.Fatalf("witnesses %s: recorded %q, want %q (%+v)", name, got.Outcome, OutcomeArtifactFailed, got)
			}
		})
	}
}
