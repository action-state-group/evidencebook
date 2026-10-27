// SPDX-License-Identifier: Apache-2.0

package evidencebook_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/evidencebook"
)

// The Examples below are the paths of docs/GUIDE.md. Each reads a corpus
// file by its pinned path under testdata/ (VECTORS.md lists where each set
// comes from), so the guide, the CI run and the vector index are the same
// files.

// node is one party's book with the keys a counterparty pins for it.
type node struct {
	book *evidencebook.Book
	keys evidencebook.ResponderKeys
}

// openNode opens a book in dir with keys derived from seed. Real deployments
// load their keys from storage; fixed seeds keep the output stable.
func openNode(ctx context.Context, dir, id string, seed byte) node {
	key := func(b byte) ed25519.PrivateKey {
		s := make([]byte, ed25519.SeedSize)
		for i := range s {
			s[i] = b + byte(i)
		}
		return ed25519.NewKeyFromSeed(s)
	}
	recordKey, checkpointKey := key(seed), key(seed+100)
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		log.Fatal(err)
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), id+"-log", checkpointKey)
	if err != nil {
		log.Fatal(err)
	}
	signer, err := evidencebook.NewEd25519Signer(recordKey)
	if err != nil {
		log.Fatal(err)
	}
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), id, signer.PublicKey())
	if err != nil {
		log.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	book, err := evidencebook.Open(ctx, evidencebook.Config{
		BookID: id, Operator: id + "-operator",
		Store: store, Substrate: substrate, Originals: originals, Signer: signer,
		Now: func() time.Time { return now },
	})
	if err != nil {
		log.Fatal(err)
	}
	return node{book: book, keys: evidencebook.ResponderKeys{
		Signer:     evidencebook.KeyID(signer),
		Checkpoint: hex.EncodeToString(checkpointKey.Public().(ed25519.PublicKey)),
	}}
}

// requestCase is one case of the request draft's request.json corpus.
type requestCase struct {
	ID          string `json:"id"`
	RequestJSON string `json:"request_json"`
	Expect      struct {
		WellFormed bool   `json:"well_formed"`
		Reason     string `json:"reason"`
	} `json:"expect"`
}

func requestCases() []requestCase {
	data, err := os.ReadFile("testdata/evidence-request/request.json")
	if err != nil {
		log.Fatal(err)
	}
	var corpus struct {
		Cases []requestCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		log.Fatal(err)
	}
	return corpus.Cases
}

func requestCaseByID(id string) requestCase {
	for _, c := range requestCases() {
		if c.ID == id {
			return c
		}
	}
	log.Fatalf("no request case %s", id)
	return requestCase{}
}

// Path 0, join the loop: record an action, ask another book for evidence,
// verify the answer, then check your own node as a stranger would see it.
//
// The request is the request draft's vector pos-derivation-history-card
// (testdata/evidence-request/request.json): the public history card of a
// book's full history. Its expected_pin names the vector's own book, so the
// requester asks instead for any checkpoint of at least one entry.
func Example_joinTheLoop() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "evidencebook-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	alice := openNode(ctx, filepath.Join(dir, "alice"), "alice", 1)
	bob := openNode(ctx, filepath.Join(dir, "bob"), "bob", 2)
	defer alice.book.Release()
	defer bob.book.Release()

	// record: seal one action into alice's book and checkpoint it.
	if _, err := alice.book.Append(ctx, evidencebook.Entry{
		RecordType:    "observation",
		EpistemicType: evidencebook.ObservedEvent,
		SubjectRef:    "order-17",
		Payloads:      [][]byte{[]byte(`{"status":"approved"}`)},
	}); err != nil {
		log.Fatal(err)
	}
	if _, err := alice.book.Checkpoint(ctx); err != nil {
		log.Fatal(err)
	}

	// request: bob reads the vector's request map and records that he asks.
	vector := requestCaseByID("pos-derivation-history-card")
	parsed := evidencerequest.ParseRequest(evidencebook.Message{Octets: []byte(vector.RequestJSON), MediaType: evidencebook.MediaTypeJSON})
	if parsed.Reason != "" {
		log.Fatal(parsed.Reason)
	}
	req := parsed.Request
	one := uint64(1)
	req.Coverage = evidencebook.Coverage{MinFreshness: &evidencebook.Freshness{Size: &one}}
	sent, err := bob.book.Request(ctx, req, "alice")
	if err != nil {
		log.Fatal(err)
	}

	// alice answers bob as a stranger: no transport authentication.
	policy := evidencebook.DefaultSharePolicy()
	reply, _, err := alice.book.Respond(ctx, sent.Message(), evidencebook.RespondOptions{Policy: policy})
	if err != nil {
		log.Fatal(err)
	}

	// verify: bob checks the reply under alice's pinned keys and records it.
	received, err := bob.book.RecordResponse(ctx, sent.RecordID, reply.Message, alice.keys)
	if err != nil {
		log.Fatal(err)
	}
	var outcome evidencebook.ResponseStatement
	if err := json.Unmarshal(received.Header.Statement, &outcome); err != nil {
		log.Fatal(err)
	}
	fmt.Println("subject:", req.Subject.Kind, "derivation:", req.Derivation)
	fmt.Println("outcome:", outcome.Outcome)

	// doctor --probe: alice asks her own responder through the public path.
	findings, err := alice.book.Doctor(ctx, evidencebook.DoctorOptions{
		Policy: policy,
		Probe: func(ctx context.Context, request evidencebook.Message) (evidencebook.Message, error) {
			reply, _, err := alice.book.Respond(ctx, request, evidencebook.RespondOptions{Policy: policy})
			return reply.Message, err
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range findings {
		fmt.Printf("%s: %s\n", f.Check, f.State)
	}
	// Output:
	// subject: full_history derivation: history_card/1
	// outcome: artifact
	// self_attested: self_attested
	// controller_binding: unbound
	// checkpoint: present
	// witness: unwitnessed
	// not_checked: none
	// unsupported: unsupported
	// sharing: on
	// policy_in_commitment: NOT_CHECKED
	// stranger_view: answered
	// identified_view: answered
	// probe_checkpoints: match
	// probe_history_card: match
}

// Path 1, seal a Result: Terms Packs and their evaluation are outside this
// module (docs/GUIDE.md links where they live). A book seals the Result an
// evaluator produced and checkpoints it, so a bundle can be built on it.
//
// The Result is the expected Evidence Result of the Deal Reconcile
// Contract's vector pair-matched (testdata/deal-reconcile-v0/vectors.json),
// written from the specification and never from an implementation.
func ExampleBook_SealResult() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "evidencebook-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	alice := openNode(ctx, dir, "alice", 1)
	defer alice.book.Release()

	data, err := os.ReadFile("testdata/deal-reconcile-v0/vectors.json")
	if err != nil {
		log.Fatal(err)
	}
	var corpus struct {
		Contract struct {
			Ref string `json:"contract_ref"`
		} `json:"contract"`
		Cases []struct {
			ID       string `json:"id"`
			Expected struct {
				Result json.RawMessage `json:"result"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		log.Fatal(err)
	}
	var result json.RawMessage
	for _, c := range corpus.Cases {
		if c.ID == "pair-matched" {
			result = c.Expected.Result
		}
	}

	// A first run finds no earlier seal; a caller re-running the same
	// evaluation looks its earlier record up here instead.
	none := func() (evidencebook.Record, bool, error) { return evidencebook.Record{}, false, nil }
	add := func(e evidencebook.Entry) (evidencebook.Record, error) { return alice.book.Append(ctx, e) }
	sealed, already, checkpoint, err := alice.book.SealResult(ctx, result, corpus.Contract.Ref, nil, none, add)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("record type:", sealed.Header.RecordType)
	fmt.Println("subject:", sealed.Header.SubjectRef)
	fmt.Println("already sealed:", already, "checkpoint cut:", checkpoint > 0)
	// Output:
	// record type: evidence_result
	// subject: ec:deal-reconcile@1
	// already sealed: false checkpoint cut: true
}

// Path 3, serve a History: Respond answers request octets as received, and
// a History is served as a derivation the holder registers for one
// recipient.
//
// Every case of the request draft's corpus
// (testdata/evidence-request/request.json) that is not well formed gets a
// signed refusal with exactly the reason the vector states. The History is
// the definition for-alice from testdata/derivation/vectors.json: its
// identifier binds the document alone, so this book derives the vector's
// identifier, and only its recipient is answered.
func ExampleBook_Respond() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "evidencebook-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	holder := openNode(ctx, dir, "holder", 1)
	defer holder.book.Release()
	if _, err := holder.book.Append(ctx, evidencebook.Entry{
		RecordType: "observation", EpistemicType: evidencebook.ObservedEvent,
		SubjectRef: "order-17", Payloads: [][]byte{[]byte(`{"status":"approved"}`)},
	}); err != nil {
		log.Fatal(err)
	}
	if _, err := holder.book.Checkpoint(ctx); err != nil {
		log.Fatal(err)
	}
	policy := evidencebook.DefaultSharePolicy()
	respond := func(octets []byte, authenticated string) evidencebook.Reply {
		reply, _, err := holder.book.Respond(ctx, evidencebook.Message{Octets: octets, MediaType: evidencebook.MediaTypeJSON},
			evidencebook.RespondOptions{AuthenticatedRequester: authenticated, Policy: policy})
		if err != nil {
			log.Fatal(err)
		}
		return reply
	}

	matched, malformed := 0, 0
	for _, c := range requestCases() {
		if c.Expect.WellFormed {
			continue
		}
		malformed++
		octets := []byte(c.RequestJSON)
		reply := respond(octets, "")
		if reply.Refusal == nil {
			log.Fatalf("%s: answered, want refusal %s", c.ID, c.Expect.Reason)
		}
		// The refusal is signed over the digest of the octets as received.
		digest := sha256.Sum256(octets)
		if err := reply.Refusal.VerifyFor(holder.keys.Signer, hex.EncodeToString(digest[:])); err != nil {
			log.Fatalf("%s: %v", c.ID, err)
		}
		if reply.Refusal.Reason == c.Expect.Reason {
			matched++
		}
	}
	fmt.Printf("%d of %d malformed cases refused with the vector's reason\n", matched, malformed)

	data, err := os.ReadFile("testdata/derivation/vectors.json")
	if err != nil {
		log.Fatal(err)
	}
	var corpus struct {
		Definitions []struct {
			Name      string                     `json:"name"`
			Document  string                     `json:"document"`
			ID        string                     `json:"id"`
			Recipient string                     `json:"recipient"`
			Forms     []evidencebook.SubjectKind `json:"forms"`
			Fields    []string                   `json:"fields"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		log.Fatal(err)
	}
	for _, d := range corpus.Definitions {
		if d.Name != "for-alice" {
			continue
		}
		id, err := holder.book.RegisterDerivation(evidencebook.DerivationDefinition{
			Document: []byte(d.Document), Recipient: d.Recipient, Forms: d.Forms, Fields: d.Fields,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("derivation id matches the vector:", id == d.ID)

		one := uint64(1)
		history, err := evidencebook.EncodeRequest(evidencebook.EvidenceRequest{
			Subject:    evidencebook.Subject{Kind: evidencebook.SubjectCorrelation, Correlation: "order-17"},
			Coverage:   evidencebook.Coverage{MinFreshness: &evidencebook.Freshness{Size: &one}},
			Derivation: id,
			Profile:    evidencebook.ProfileID,
		}, evidencebook.MediaTypeJSON)
		if err != nil {
			log.Fatal(err)
		}
		for _, requester := range []string{d.Recipient, "bob", ""} {
			reply := respond(history, requester)
			outcome := "artifact"
			if reply.Refusal != nil {
				outcome = reply.Refusal.Reason
			}
			fmt.Printf("history for %q: %s\n", requester, outcome)
		}
	}
	// Output:
	// 22 of 22 malformed cases refused with the vector's reason
	// derivation id matches the vector: true
	// history for "alice": artifact
	// history for "bob": not_authorized
	// history for "": not_authorized
}
