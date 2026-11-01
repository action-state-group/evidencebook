// SPDX-License-Identifier: Apache-2.0

package evidencebook_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/capsule-emit/go/artifact"
	artifactsqlite "github.com/action-state-group/capsule-emit/go/artifact/sqlite"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	_ "modernc.org/sqlite"
)

// party is one key's shelf: every log the key answers for, in one file
// (docs/design/254-shelf.md §2.1).
type party struct {
	shelf     *evidencebook.Shelf
	db        *sql.DB
	originals *artifactsqlite.Store
	key       ed25519.PrivateKey
	keys      evidencebook.ResponderKeys
	now       time.Time
}

// openParty opens a shelf in dir under keys derived from seed. Real
// deployments load their keys from storage; fixed seeds keep the output
// stable.
func openParty(ctx context.Context, dir, operator string, seed byte) *party {
	key := func(b byte) ed25519.PrivateKey {
		s := make([]byte, ed25519.SeedSize)
		for i := range s {
			s[i] = b + byte(i)
		}
		return ed25519.NewKeyFromSeed(s)
	}
	p := &party{key: key(seed), now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	checkpointKey := key(seed + 100)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(dir, "shelf.db")
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	p.db = db
	signer, err := evidencebook.NewEd25519Signer(p.key)
	if err != nil {
		log.Fatal(err)
	}
	if p.originals, err = artifactsqlite.New(db, operator, []ed25519.PublicKey{signer.PublicKey()}); err != nil {
		log.Fatal(err)
	}
	if err := p.originals.Init(ctx); err != nil {
		log.Fatal(err)
	}
	if p.shelf, err = evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{
		Path: path, Operator: operator, Signer: signer, CheckpointKey: checkpointKey, Originals: p.originals,
		Now: func() time.Time { return p.now },
		// No authenticated index records: every record on a log is a step
		// or an observation the party sealed.
		AuthenticatedIndexes: func(string) []evidencebook.AuthenticatedIndex { return []evidencebook.AuthenticatedIndex{} },
	}); err != nil {
		log.Fatal(err)
	}
	p.keys = evidencebook.ResponderKeys{
		Signer:     evidencebook.KeyID(signer),
		Checkpoint: hex.EncodeToString(checkpointKey.Public().(ed25519.PublicKey)),
	}
	return p
}

func (p *party) release() {
	if err := p.shelf.Release(); err != nil {
		log.Fatal(err)
	}
	if err := p.db.Close(); err != nil {
		log.Fatal(err)
	}
}

func (p *party) book(ctx context.Context, id string) *evidencebook.Book {
	book, err := p.shelf.Open(ctx, id)
	if err != nil {
		log.Fatal(err)
	}
	return book
}

// keyHex is the party's key as a controller binding names it.
func (p *party) keyHex() string { return hex.EncodeToString(p.key.Public().(ed25519.PublicKey)) }

// stepRef names an earlier step of the same deal by its seq (1-based).
type stepRef struct {
	rel string
	seq int
}

// dealStep is one x-deal-v0 record before its chain members are filled.
type dealStep struct {
	recordType string
	body       map[string]any
	refs       []stepRef
	block      map[string]any
	decide     *dealprofile.Decide
}

// sealDeal seals steps, in order, as the records of book: each an x-deal-v0
// record the profile's checks accept, sealed by the party as a Capsule
// carrying the deal id (dealprofile.StepCapsule), and committed as the
// book's record (Book.AppendCapsule). It returns the records.
func (p *party) sealDeal(ctx context.Context, book *evidencebook.Book, dealID, peer string, steps []dealStep) []evidencebook.Record {
	identity, err := emit.NewEd25519SigningIdentity(p.key)
	if err != nil {
		log.Fatal(err)
	}
	ref := func(digest string) map[string]any {
		return map[string]any{"type": dealprofile.RecordRef, "digest_alg": "SHA-256", "digest": digest}
	}
	var digests []string
	var sealed []evidencebook.Record
	for i, s := range steps {
		block := map[string]any{
			"profile": dealprofile.Profile, "canonicalization": dealprofile.Canonicalization,
			"deal_id": dealID, "record_type": s.recordType, "seq": i + 1, "at": p.now.Format(time.RFC3339),
		}
		if i > 0 {
			block["prev"] = ref(digests[i-1])
			block["baseline_ref"] = ref(digests[0])
		}
		var refs []any
		for _, r := range s.refs {
			named := ref(digests[r.seq-1])
			named["rel"] = r.rel
			refs = append(refs, named)
		}
		if refs != nil {
			block["refs"] = refs
		}
		for k, v := range s.block {
			block[k] = v
		}
		payload, digest, err := dealprofile.Encode(map[string]any{"x-deal-v0": block, "body": s.body}, []string{peer})
		if err != nil {
			log.Fatal(err)
		}
		digests = append(digests, digest)
		in := dealprofile.StepInput{DealID: dealID, Seq: int64(i + 1), Operator: "staple-example", Developer: "staple-example",
			At: p.now, Decide: s.decide, Local: s.recordType}
		if i > 0 {
			in.Parent = &emit.Chain{ParentCapsuleID: sealed[i-1].RecordID, Relation: dealprofile.RelationFollows}
		}
		capsule, header, err := dealprofile.StepCapsule(in)
		if err != nil {
			log.Fatal(err)
		}
		entry := evidencebook.Entry{RecordType: header.RecordType, EpistemicType: evidencebook.EpistemicType(header.EpistemicType),
			SubjectRef: header.SubjectRef, Fields: header.Fields}
		for _, cited := range header.Cites {
			entry.Links = append(entry.Links, evidencebook.Link{Type: evidencebook.Cites, Target: cited})
		}
		seal := func(_ evidencebook.Header, headerJSON []byte) (emit.Result, error) {
			c := capsule
			c.Extensions = append(append([]emit.Extension(nil), capsule.Extensions...),
				emit.Extension{Name: evidencebook.RecordHeaderMember, Value: headerJSON})
			return emit.Seal(emit.SealInput{Capsule: c, Identity: identity, Payload: json.RawMessage(payload)})
		}
		originals := []artifact.Artifact{{Name: "payload", Binding: artifact.PayloadDigest, State: artifact.Present, Content: payload}}
		record, err := book.AppendCapsule(ctx, entry, originals, seal)
		if err != nil {
			log.Fatal(err)
		}
		sealed = append(sealed, record)
		p.now = p.now.Add(time.Minute)
	}
	if _, err := book.Checkpoint(ctx); err != nil {
		log.Fatal(err)
	}
	return sealed
}

// historyCard has relying asks book's holder for history_card/1 over the
// book's full history, and returns what the relying party recorded.
func historyCard(ctx context.Context, relying *evidencebook.Book, holder *party, book *evidencebook.Book) string {
	parsed := evidencerequest.ParseRequest(evidencebook.Message{
		Octets: []byte(requestCaseByID("pos-derivation-history-card").RequestJSON), MediaType: evidencebook.MediaTypeJSON})
	if parsed.Reason != "" {
		log.Fatal(parsed.Reason)
	}
	req := parsed.Request
	req.Nonce = "" // the book draws a fresh one per request
	one := uint64(1)
	req.Coverage = evidencebook.Coverage{MinFreshness: &evidencebook.Freshness{Size: &one}}
	sent, err := relying.Request(ctx, req, holder.keys.Signer)
	if err != nil {
		log.Fatal(err)
	}
	reply, _, err := book.Respond(ctx, sent.Message(), evidencebook.RespondOptions{Policy: evidencebook.DefaultSharePolicy()})
	if err != nil {
		log.Fatal(err)
	}
	received, err := relying.RecordResponse(ctx, sent.RecordID, reply.Message, holder.keys)
	if err != nil {
		log.Fatal(err)
	}
	var outcome evidencebook.ResponseStatement
	if err := json.Unmarshal(received.Header.Statement, &outcome); err != nil {
		log.Fatal(err)
	}
	return outcome.Outcome
}

// member is one party's half of the deal as a composed/v1 member: an
// Evidence Bundle rooted at the record, as the answer to the request whose
// digest the relying party holds.
func member(ctx context.Context, id, observer string, book *evidencebook.Book, root evidencebook.Record) map[string]any {
	built, err := book.Bundle(ctx, evidencebook.BundleRequest{Root: root.RecordID})
	if err != nil {
		log.Fatal(err)
	}
	body, err := aacbundle.Decode(built.JSON)
	if err != nil {
		log.Fatal(err)
	}
	digest, err := aacbundle.BundleDigest(body)
	if err != nil {
		log.Fatal(err)
	}
	request := sha256.Sum256([]byte("the relying party's request for " + id))
	return map[string]any{"id": id, "observer": observer, "request_digest": hex.EncodeToString(request[:]),
		"outcome": "artifact", "digest": digest, "bundle": body}
}

// bindingRecord is a same_controller_asserted controller binding record
// (draft-mih-zhang-agent-disclosure-bundle-02, "Controller Binding
// Records"), signed by both keys.
func bindingRecord(a, b *party, issued time.Time) map[string]any {
	first, second := a, b
	if first.keyHex() > second.keyHex() {
		first, second = second, first
	}
	at := func(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
	record := map[string]any{
		"binding_version": "controller-binding/0", "claim": "same_controller_asserted", "method": "dual_signature/0",
		"keys": []any{first.keyHex(), second.keyHex()}, "issued_at": at(issued), "not_before": at(issued),
		"not_after": at(issued.AddDate(1, 0, 0)), "revocation": "revocation_statement/0", "nonce": "staple-example-binding",
	}
	input, err := canonical.JCS(record)
	if err != nil {
		log.Fatal(err)
	}
	record["sigs"] = []any{
		map[string]any{"key_id": first.keyHex(), "sig": hex.EncodeToString(ed25519.Sign(first.key, input))},
		map[string]any{"key_id": second.keyHex(), "sig": hex.EncodeToString(ed25519.Sign(second.key, input))},
	}
	return record
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Path 4, History cards that staple (docs/design/254-shelf.md §3): a Goose
// agent buys inference from a Mesh node. Each party keeps its own shelf and
// answers its own History; a relying party joins the two parties' evidence
// of the deal on the deal id, and joins a Mesh node to its operator's own
// Goose agent only through a controller binding.
//
// The records are x-deal-v0 steps (dealprofile), the chain a Goose buyer
// seals (python/examples/goose). The keys are derived from fixed seeds.
func Example_staple() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "evidencebook-staple")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	goose := openParty(ctx, filepath.Join(dir, "goose"), "goose", 11)
	mesh := openParty(ctx, filepath.Join(dir, "mesh"), "mesh", 21)
	owner := openParty(ctx, filepath.Join(dir, "owner"), "owner", 31) // the Mesh operator's own Goose agent
	relying := openNode(ctx, filepath.Join(dir, "relying"), "relying", 41)
	defer goose.release()
	defer mesh.release()
	defer owner.release()
	defer relying.book.Release()

	const dealID = "deal-7a41c09e5b2d8f36"
	// Each party names its counterparty only by a fingerprint under its own
	// deal key (x-deal-v0 §4); the raw key never enters a record.
	counterparty := func(storeSecret, peer string) map[string]any {
		payee, err := dealprofile.Fingerprint(dealprofile.DealKey([]byte(storeSecret), dealID), "payee", peer)
		if err != nil {
			log.Fatal(err)
		}
		return map[string]any{"fp_alg": dealprofile.FPAlg, "ids": map[string]any{"payee": payee}}
	}
	terms := map[string]any{"item": "inference", "price_minor": 1200, "currency": "USD"}
	recourse := map[string]any{"rail": "lightning", "refundable": false}

	// The Goose buyer: one book per deal, a node book, and the cadence that
	// commits every book's checkpoint at each tick.
	dealBook := goose.book(ctx, "deal/"+dealID)
	// A commitment's nonce is drawn fresh for each text; a fixed one keeps
	// this output stable.
	commit, err := dealprofile.CommitText(digestOf("nonce"), "buy one inference call")
	if err != nil {
		log.Fatal(err)
	}
	seller := counterparty("the Goose agent's deal store secret", mesh.keys.Signer)
	gooseHalf := goose.sealDeal(ctx, dealBook, dealID, mesh.keys.Signer, []dealStep{
		{recordType: dealprofile.TypeBaseline, block: map[string]any{"channel": "app_chat", "counterparty": seller},
			body: map[string]any{"deal_type": "purchase", "terms": terms, "recourse": recourse,
				"intent": map[string]any{"verbatim_commitment": commit, "asked": map[string]any{"item": "inference"}, "allowed": []any{"pay"}}}},
		{recordType: dealprofile.TypeCheck, block: map[string]any{"counterparty": seller},
			body: map[string]any{"action": "pay", "action_class": "money.purchase", "amount_minor": 1200, "authorized_max_minor": 1200,
				"currency": "USD", "spend_minor": 1200, "spend_authorized_minor": 1200, "taxonomy_version": "6",
				"terms": terms, "recourse": recourse}},
		{recordType: dealprofile.TypeVerdict, refs: []stepRef{{"checks", 2}},
			body: map[string]any{"result": "pass", "differences": []any{}, "options": []any{}, "judge": map[string]any{"kind": "rules"},
				"rules": map[string]any{"status": "not_configured"}}},
		{recordType: dealprofile.TypeApproval, refs: []stepRef{{"approves", 3}},
			body: map[string]any{"choice": "proceed", "proceed": true, "approver": "standing_intent"}},
		{recordType: dealprofile.TypeAction, refs: []stepRef{{"authorized_by", 4}}, block: map[string]any{"counterparty": seller},
			decide: &dealprofile.Decide{Disposition: emit.Disposition{Decision: "accept", Approver: "policy", VerdictClass: "executed"},
				EffectType: "send_payment", Irreversibility: "one_way_consequential"},
			body: map[string]any{"action": "pay", "action_class": "money.purchase", "amount_minor": 1200, "currency": "USD",
				"direction": "out", "rail": "lightning", "reference_commitment": commit, "spend_minor": 1200, "taxonomy_version": "6"}},
	})
	nodeBook := goose.book(ctx, "goose/node")
	if _, err := nodeBook.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent,
		SubjectRef: "admitted " + mesh.keys.Signer, Payloads: [][]byte{[]byte(`{"admitted":true}`)}}); err != nil {
		log.Fatal(err)
	}
	if _, err := nodeBook.Checkpoint(ctx); err != nil {
		log.Fatal(err)
	}
	cadence, err := goose.shelf.Cadence(ctx, evidencebook.CadenceConfig{LogID: "goose/cadence", NoJitter: true,
		Ended: func(context.Context, string) (bool, error) { return false, nil }})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := cadence.Tick(ctx, goose.now); err != nil {
		log.Fatal(err)
	}

	// The Mesh seller: its whole history is one stream, which answers at its
	// own checkpoint.
	meshLog := mesh.book(ctx, "mesh/node")
	sellerIntent := map[string]any{"verbatim_commitment": commit, "asked": map[string]any{"item": "inference"},
		"allowed": []any{"offer", "commit"}, "party_role": "seller"}
	meshHalf := mesh.sealDeal(ctx, meshLog, dealID, goose.keys.Signer, []dealStep{
		{recordType: dealprofile.TypeBaseline, block: map[string]any{"channel": "app_chat",
			"counterparty": counterparty("the Mesh node's deal store secret", goose.keys.Signer)},
			body: map[string]any{"deal_type": "purchase", "terms": terms, "recourse": recourse, "intent": sellerIntent}},
	})
	// The Mesh operator's own Goose agent recorded the same sale under its
	// own key.
	ownerBook := owner.book(ctx, "deal/"+dealID)
	ownerHalf := owner.sealDeal(ctx, ownerBook, dealID, goose.keys.Signer, []dealStep{
		{recordType: dealprofile.TypeBaseline, block: map[string]any{"channel": "app_chat",
			"counterparty": counterparty("the operator's agent's deal store secret", goose.keys.Signer)},
			body: map[string]any{"deal_type": "purchase", "terms": terms, "recourse": recourse, "intent": sellerIntent}},
	})

	// history_card/1 per log: the relying party asks each log's holder and
	// verifies the answer under the holder's pinned keys.
	fmt.Println("history_card/1 goose deal/" + dealID + ": " + historyCard(ctx, relying.book, goose, dealBook))
	fmt.Println("history_card/1 goose goose/node: " + historyCard(ctx, relying.book, goose, nodeBook))
	fmt.Println("history_card/1 mesh mesh/node: " + historyCard(ctx, relying.book, mesh, meshLog))

	// Goose's History: standard_history/1 as of a cadence tick, each book
	// bounded by the checkpoint the tick carries, one deal entry per deal
	// book. A relying party that holds the same inputs checks every SH
	// property against them.
	anchor, err := cadence.StandardHistoryAnchor(ctx, 0)
	if err != nil {
		log.Fatal(err)
	}
	var in dealprofile.HistoryInput
	held, err := dealBook.Query(ctx, evidencebook.Filter{})
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range held[:anchor.Extents["deal/"+dealID]] {
		originals, err := goose.originals.Get(ctx, r.RecordID)
		if err != nil {
			log.Fatal(err)
		}
		at, err := time.Parse(time.RFC3339Nano, r.Header.CommittedAt)
		if err != nil {
			log.Fatal(err)
		}
		for _, a := range originals.Artifacts {
			if a.Name == "payload" {
				in.Records = append(in.Records, dealprofile.HistoryRecord{Payload: a.Content, CommittedAt: at})
			}
		}
	}
	entry, agreed, err := dealprofile.StandardHistoryDeal(in)
	if err != nil || !agreed {
		log.Fatalf("deal entry: %v %v", agreed, err)
	}
	deals := []standardhistory.Deal{entry}
	policy := standardhistory.Policy{Digest: digestOf("the Goose agent's disclosure policy"), Window: standardhistory.Window{Months: 3}}
	card, err := evidencebook.DeriveStandardHistory("person", anchor.AsOf, policy, deals)
	if err != nil {
		log.Fatal(err)
	}
	properties, err := standardhistory.Check(card, &policy, &anchor.AsOf, deals)
	if err != nil {
		log.Fatal(err)
	}
	passed := 0
	for _, p := range standardhistory.Properties {
		if properties[p] == standardhistory.Pass {
			passed++
		}
	}
	fmt.Printf("standard_history/1 goose at tick %d: %d deal, %d of %d properties PASS\n",
		anchor.Tick.N, len(deals), passed, len(standardhistory.Properties))

	// The staple: the relying party composes each party's half of the deal
	// in composed/v1 and joins them on the deal id every half's Capsule
	// carries. The Mesh node and its operator's agent are joined only by
	// the binding record both keys signed.
	binding := bindingRecord(mesh, owner, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	unsigned := map[string]any{}
	for k, v := range binding {
		if k != "sigs" {
			unsigned[k] = v
		}
	}
	bindingInput, err := canonical.JCS(unsigned)
	if err != nil {
		log.Fatal(err)
	}
	bindingDigest := sha256.Sum256(bindingInput)
	pointer, keyPointer := "/"+dealprofile.DealIDMember, "/key_id"
	identifier := digestOf(dealID)
	block := map[string]any{
		"observers": []any{
			map[string]any{"id": "goose", "role": "buyer", "custody_domain": "goose"},
			map[string]any{"id": "mesh", "role": "seller", "custody_domain": "mesh-operator"},
			map[string]any{"id": "owner", "role": "seller-agent", "custody_domain": "mesh-operator"},
		},
		"members": []any{
			member(ctx, "goose-half", "goose", dealBook, gooseHalf[len(gooseHalf)-1]),
			member(ctx, "mesh-half", "mesh", meshLog, meshHalf[0]),
			member(ctx, "owner-half", "owner", ownerBook, ownerHalf[0]),
		},
		"joins": []any{
			map[string]any{"members": []any{"goose-half", "mesh-half"}, "basis": "pre_agreed_identifier", "state": "agree",
				"pointer": pointer, "identifier_digest": identifier},
			map[string]any{"members": []any{"mesh-half", "owner-half"}, "basis": "controller_binding", "state": "agree",
				"pointer": keyPointer, "identifier_digest": hex.EncodeToString(bindingDigest[:])},
		},
	}
	// The digest covers the declarations only, not the digest member.
	block["composed_digest"] = strings.Repeat("0", 64)
	if block["composed_digest"], err = aacbundle.ComposedDigest(block); err != nil {
		log.Fatal(err)
	}
	// A block travels as JSON; the verifier reads it back as JSON values.
	carried, err := json.Marshal(block)
	if err != nil {
		log.Fatal(err)
	}
	raw, err := aacbundle.Decode(carried)
	if err != nil {
		log.Fatal(err)
	}
	result := aacbundle.VerifyComposed(raw, aacbundle.Options{ControllerBindings: []map[string]interface{}{binding}})
	fmt.Println("composed/v1:", result.Status)
	for _, m := range result.Members {
		fmt.Printf("member %s: digest %s, graph closure %s, membership %s\n", m.ID, m.Digest,
			m.Bundle.GraphClosure.Status, m.Bundle.PerRecordMembership.Status)
	}
	for i, j := range result.Joins {
		c := result.Corroboration[i]
		why := c.Qualifier
		if c.Result == "redundant" {
			why = strings.Join(c.Reasons, ", ")
		}
		fmt.Printf("join %s + %s on %s: %s, %s (%s)\n", j.Members[0], j.Members[1], j.Basis, j.Derived, c.Result, why)
	}

	// Without the binding record, nothing links the two keys: the join is
	// not derivable, and the block does not pass.
	unbound := aacbundle.VerifyComposed(raw, aacbundle.Options{})
	fmt.Println("composed/v1 without the binding record:", unbound.Status, unbound.Joins[1].Result)
	// Output:
	// history_card/1 goose deal/deal-7a41c09e5b2d8f36: artifact
	// history_card/1 goose goose/node: artifact
	// history_card/1 mesh mesh/node: artifact
	// standard_history/1 goose at tick 1: 1 deal, 6 of 6 properties PASS
	// composed/v1: pass
	// member goose-half: digest reproduced, graph closure pass, membership pass
	// member mesh-half: digest reproduced, graph closure pass, membership pass
	// member owner-half: digest reproduced, graph closure pass, membership pass
	// join goose-half + mesh-half on pre_agreed_identifier: agree, corroborating (custody_declared)
	// join mesh-half + owner-half on controller_binding: agree, redundant (same_custody_domain, controller_binding)
	// composed/v1 without the binding record: withheld not_derivable
}
