// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/envelope"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/capsule-emit/go/artifact"
)

// strictVerdict is agent-action-capsule's strict verdict on a bundle.
func strictVerdict(t *testing.T, data []byte) aacbundle.StrictResult {
	t.Helper()
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return aacbundle.VerifyStrict(decoded, aacbundle.StrictOptions{})
}

func bundleTree(t *testing.T, data []byte) map[string]any {
	t.Helper()
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return decoded.(map[string]any)
}

// recordSigners maps each carried record to the public key of the Producer
// Envelope it carries, checking each envelope.
func recordSigners(t *testing.T, data []byte) map[string]string {
	t.Helper()
	signers := map[string]string{}
	for _, raw := range bundleTree(t, data)["records"].([]any) {
		record := raw.(map[string]any)
		id := record["capsule_id"].(string)
		signature, ok := record["signature"].(string)
		if !ok {
			t.Fatalf("record %s carries no producer signature", id)
		}
		envelopeBytes, err := hex.DecodeString(signature)
		if err != nil {
			t.Fatal(err)
		}
		checked := envelope.Verify(id, envelopeBytes)
		if !checked.OK || hex.EncodeToString(checked.PublicKey) != record["key_id"] {
			t.Fatalf("record %s: envelope does not verify under its key_id", id)
		}
		signers[id] = record["key_id"].(string)
	}
	return signers
}

// TestBundleCarriesProducerEnvelopesAndReadsValid: under Steven's ruling on
// evidencebook#84 §4 (option A), every carried record, ride-alongs included,
// carries its Producer Envelope, so the bundle reads VALID under strict
// verification; it used to read INCOMPLETE with every signature unclaimed.
func TestBundleCarriesProducerEnvelopesAndReadsValid(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	bundle, err := book.Bundle(ctx, BundleRequest{Root: records[1].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	if result := strictVerdict(t, bundle.JSON); result.Verdict != aacbundle.VerdictValid {
		t.Fatalf("strict verdict %s: %+v", result.Verdict, result.Claims)
	}
	signers := recordSigners(t, bundle.JSON)
	// The range runs from the claim's closure start to the checkpoint tip,
	// so the ruling and the later observation ride along, signed too.
	for _, record := range records {
		if _, ok := signers[record.RecordID]; !ok {
			t.Fatalf("record %s is not carried with its envelope", record.RecordID)
		}
	}
	if digest, err := aacbundle.BundleDigest(bundleTree(t, bundle.JSON)); err != nil || digest != bundle.Digest {
		t.Fatalf("disclosure record states %s, the bundle digest is %s (%v)", bundle.Digest, digest, err)
	}
	checkpoint := bundleTree(t, bundle.JSON)["checkpoint"].(map[string]any)
	for _, member := range []string{"key_id", "timestamp", "prev_size", "prev_root"} {
		if _, ok := checkpoint[member]; !ok {
			t.Fatalf("checkpoint member has no %s", member)
		}
	}
}

// TestBundleAcrossTwoSignerKeys: a book written under one key and reopened
// under another bundles records of both, each with its own envelope; the
// ruling covers every signer key the book holds.
func TestBundleAcrossTwoSignerKeys(t *testing.T) {
	ctx := context.Background()
	fixture := newFixture(t, "book-a", 1)
	first := fixture.open(t)
	early := mustAppend(t, first, observation("order-17", `{"status":"approved"}`))
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	config := fixture.config(t)
	signer, err := NewEd25519Signer(seededKey(77))
	if err != nil {
		t.Fatal(err)
	}
	config.Signer = signer
	if config.Originals, err = OpenOriginalsFile(ctx, filepath.Join(fixture.dir, "originals.jsonl"), "book", seededKey(1).Public().(ed25519.PublicKey), signer.PublicKey()); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Release() })
	late := mustAppend(t, second, Entry{RecordType: "claim", EpistemicType: ProducerClaim, SubjectRef: "order-17", Links: []Link{{Type: Cites, Target: early.RecordID}}})
	bundle, err := second.Bundle(ctx, BundleRequest{Root: late.RecordID})
	if err != nil {
		t.Fatal(err)
	}
	if result := strictVerdict(t, bundle.JSON); result.Verdict != aacbundle.VerdictValid {
		t.Fatalf("strict verdict %s: %+v", result.Verdict, result.Claims)
	}
	signers := recordSigners(t, bundle.JSON)
	if signers[early.RecordID] == signers[late.RecordID] {
		t.Fatalf("both records name key %s; want one key each", signers[early.RecordID])
	}
	if signers[early.RecordID] != hex.EncodeToString(seededKey(1).Public().(ed25519.PublicKey)) || signers[late.RecordID] != hex.EncodeToString(signer.PublicKey()) {
		t.Fatalf("record signers %v", signers)
	}
}

// TestBundleRestoresTheTreeOnce: the producer asks for a proof per carried
// record; all of them come from one restored MMR.
func TestBundleRestoresTheTreeOnce(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	root := mustAppend(t, book, observation("order", `{"n":0}`))
	for i := 1; i < 12; i++ {
		mustAppend(t, book, observation("order", `{"n":`+strconv.Itoa(i)+`}`))
	}
	substrate := book.commitments.(*CLLSubstrate)
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := substrate.treeLoads
	if _, err := book.Bundle(ctx, BundleRequest{Root: root.RecordID, At: &anchor}); err != nil {
		t.Fatal(err)
	}
	if loads := substrate.treeLoads - before; loads != 1 {
		t.Fatalf("one bundle restored the MMR %d times, want 1", loads)
	}
}

// TestBundleAtAnEarlierAnchor: a pinned anchor is the bundle's checkpoint.
func TestBundleAtAnEarlierAnchor(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("order-19", `{"status":"new"}`))
	if _, err := book.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	bundle, err := book.Bundle(ctx, BundleRequest{Root: records[1].RecordID, At: &anchor})
	if err != nil {
		t.Fatal(err)
	}
	if result := strictVerdict(t, bundle.JSON); result.Verdict != aacbundle.VerdictValid {
		t.Fatalf("strict verdict %s", result.Verdict)
	}
	tree := bundleTree(t, bundle.JSON)
	if size := tree["checkpoint"].(map[string]any)["mmr_size"].(json.Number).String(); size != strconv.FormatUint(anchor.TreeSize, 10) {
		t.Fatalf("checkpoint mmr_size %s, want the anchor's %d", size, anchor.TreeSize)
	}
	if last := tree["completeness_certificate"].(map[string]any)["last_seq"].(json.Number).String(); last != strconv.FormatUint(anchor.Entries, 10) {
		t.Fatalf("range ends at %s, want the anchor's tip %d", last, anchor.Entries)
	}
}

// citingSealer seals a caller capsule whose references cite targets that its
// header links do not name: a writer whose citations and links differ.
func citingSealer(t *testing.T, payload string, targets ...string) CapsuleSealer {
	t.Helper()
	return func(header Header, headerJSON []byte) (emit.Result, error) {
		identity, err := emit.NewEd25519SigningIdentity(seededKey(1))
		if err != nil {
			return emit.Result{}, err
		}
		var references []emit.Reference
		for _, target := range targets {
			references = append(references, emit.Reference{Type: "agent-action-capsule", DigestAlg: "SHA-256", Digest: target})
		}
		input := emit.Input{
			ActionID: "own-action", ActionType: emit.ActionTypeFYI, Operator: "caller", Developer: "caller",
			Timestamp: time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC), References: references,
			Extensions: []emit.Extension{{Name: RecordHeaderMember, Value: headerJSON}},
		}
		return emit.Seal(emit.SealInput{Capsule: input, Payload: json.RawMessage(payload), Identity: identity})
	}
}

// TestBundleWhenCitationsDifferFromLinks: the bundle's closure is the
// verifier's (the capsule's citations); the book's links choose the range
// start and the disclosed set. A citation inside the range rides along, one
// the book does not hold is missing, and one below the range start is
// refused as the book's data.
func TestBundleWhenCitationsDifferFromLinks(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	below := mustAppend(t, book, observation("below", `{"n":0}`))
	inside := mustAppend(t, book, observation("inside", `{"n":1}`))
	absent, absentToo := strings.Repeat("ab", 32), strings.Repeat("cd", 32)

	payload := `{"order":17}`
	ridesAlong, err := book.AppendCapsule(ctx, Entry{RecordType: "published_capsule", EpistemicType: ProducerClaim, SubjectRef: "p", Links: []Link{{Type: Cites, Target: inside.RecordID}}}, boundPayload(payload), citingSealer(t, payload, inside.RecordID, absentToo, absent))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := book.Bundle(ctx, BundleRequest{Root: ridesAlong.RecordID, ClosureDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	tree := bundleTree(t, bundle.JSON)
	completeness := tree["completeness"].(map[string]any)
	missing := anySlice(completeness["missing"])
	slices.Sort(missing)
	if completeness["records_mode"] != "declared_incomplete" || !slices.Equal(missing, []string{absent, absentToo}) {
		t.Fatalf("completeness %v, want both citations the book does not hold missing", completeness)
	}
	if result := strictVerdict(t, bundle.JSON); result.Verdict == aacbundle.VerdictInvalid {
		t.Fatalf("strict verdict INVALID: %+v", result.Claims)
	}

	// A record whose links pick a later range start than its citations.
	payload = `{"order":18}`
	refused, err := book.AppendCapsule(ctx, Entry{RecordType: "published_capsule", EpistemicType: ProducerClaim, SubjectRef: "q"}, boundPayload(payload), citingSealer(t, payload, below.RecordID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.Bundle(ctx, BundleRequest{Root: refused.RecordID}); !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), below.RecordID) {
		t.Fatalf("a citation below the range start: %v, want ErrCorrupt naming %s", err, below.RecordID)
	}

	// A citation inside the range that the book's links do not select rides
	// along: carried and closed over, but its header is not disclosed and the
	// disclosure record lists it withheld.
	early := mustAppend(t, book, observation("early", `{"n":2}`))
	cited := mustAppend(t, book, observation("cited", `{"n":3}`))
	payload = `{"order":19}`
	citing, err := book.AppendCapsule(ctx, Entry{RecordType: "published_capsule", EpistemicType: ProducerClaim, SubjectRef: "s"}, boundPayload(payload), citingSealer(t, payload, cited.RecordID))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err = book.Bundle(ctx, BundleRequest{Root: citing.RecordID, Include: []string{early.RecordID}})
	if err != nil {
		t.Fatal(err)
	}
	if result := strictVerdict(t, bundle.JSON); result.Verdict != aacbundle.VerdictValid {
		t.Fatalf("strict verdict %s: %+v", result.Verdict, result.Claims)
	}
	if _, carried := recordSigners(t, bundle.JSON)[cited.RecordID]; !carried {
		t.Fatal("the cited record is not carried")
	}
	if _, disclosed := bundleTree(t, bundle.JSON)["disclosures"].(map[string]any)[cited.RecordID]; disclosed {
		t.Fatal("a record the book did not select was disclosed")
	}
	var statement DisclosureStatement
	if err := json.Unmarshal(bundle.Disclosure.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(statement.Withheld, func(w WithheldItem) bool { return w.RecordID == cited.RecordID && w.Member == HeaderMember }) {
		t.Fatalf("the disclosure record does not list the ride-along header withheld: %+v", statement.Withheld)
	}
}

func anySlice(value any) []string {
	var out []string
	for _, item := range value.([]any) {
		out = append(out, item.(string))
	}
	return out
}

// outputSealer seals a caller capsule committing both agent_input (the
// payload) and agent_output.
func outputSealer(t *testing.T, payload, output string) CapsuleSealer {
	t.Helper()
	return func(header Header, headerJSON []byte) (emit.Result, error) {
		identity, err := emit.NewEd25519SigningIdentity(seededKey(1))
		if err != nil {
			return emit.Result{}, err
		}
		input := emit.Input{
			ActionID: "own-action", ActionType: emit.ActionTypeFYI, Operator: "caller", Developer: "caller",
			Timestamp:  time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC),
			Extensions: []emit.Extension{{Name: RecordHeaderMember, Value: headerJSON}},
		}
		return emit.Seal(emit.SealInput{Capsule: input, Payload: json.RawMessage(payload), AgentOutput: json.RawMessage(output), Identity: identity})
	}
}

// TestPayloadsAllRevealsEveryCommittedMember: payloads=all needs every
// eligible, unsuppressed member a disclosed capsule commits revealed. An
// output with no original, or one declared never retained, is refused with
// advice that works: suppressing it builds.
func TestPayloadsAllRevealsEveryCommittedMember(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	payload, output := `{"order":17}`, `{"ok":true}`
	for name, originals := range map[string][]artifact.Artifact{
		"no original":    boundPayload(payload),
		"never retained": append(boundPayload(payload), artifact.Artifact{Name: "agent_output", Binding: artifact.AgentOutputDigest, State: artifact.NeverRetained}),
	} {
		record, err := book.AppendCapsule(ctx, publishedEntry(), originals, outputSealer(t, payload, output))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = book.Bundle(ctx, BundleRequest{Root: record.RecordID, Payloads: PayloadsAll})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "agent_output") || !strings.Contains(err.Error(), "suppress agent_output") {
			t.Fatalf("%s: %v, want ErrInvalid naming agent_output and offering to suppress it", name, err)
		}
		bundle, err := book.Bundle(ctx, BundleRequest{Root: record.RecordID, Payloads: PayloadsAll, Suppress: []string{"agent_output"}})
		if err != nil {
			t.Fatalf("%s: suppressing agent_output: %v", name, err)
		}
		completeness := bundleTree(t, bundle.JSON)["completeness"].(map[string]any)
		if completeness["payloads_mode"] != "all" {
			t.Fatalf("%s: payloads_mode %v", name, completeness["payloads_mode"])
		}
	}
	// A bound original that is not JSON cannot be revealed as its member, so
	// suppressing would withhold it: only selected helps. emit commits JSON
	// digests, so a capsule like this comes only from another sealer.
	capsule := json.RawMessage(`{"model_attestation":{"compute_attestation":{"agent_output_digest":"` + strings.Repeat("ef", 32) + `"}}}`)
	err := revealsEveryMember([]string{"r"}, map[string]json.RawMessage{"r": capsule}, nil, map[string]map[string][]string{"r": {strings.Repeat("ef", 32): {"agent_output"}}}, nil)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not JSON") || strings.Contains(err.Error(), "suppress") {
		t.Fatalf("a non-JSON bound original: %v", err)
	}
}

func nested(levels int) json.RawMessage {
	return json.RawMessage(strings.Repeat("[", levels) + strings.Repeat("]", levels))
}

// TestBundleRequestChecks: every input agent-action-capsule's producer would
// refuse is refused first as ErrInvalid, and Suppress is stated once,
// sorted, in both the bundle and the disclosure record.
func TestBundleRequestChecks(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	root := records[1].RecordID
	for name, req := range map[string]BundleRequest{
		"ineligible suppress":     {Root: root, Suppress: []string{"agent_input", "header"}},
		"negative closure depth":  {Root: root, ClosureDepth: -1},
		"short producer key":      {Root: root, ProducerKey: make([]byte, 31)},
		"producer-key extension":  {Root: root, Extensions: map[string]json.RawMessage{"producer-key/v1": json.RawMessage(`{"public_key":"00"}`)}},
		"composed extension":      {Root: root, Extensions: map[string]json.RawMessage{"composed/v1": json.RawMessage(`{}`)}},
		"float extension":         {Root: root, Extensions: map[string]json.RawMessage{"x/float": json.RawMessage(`{"v":1.5}`)}},
		"unsafe integer":          {Root: root, Extensions: map[string]json.RawMessage{"x/big": json.RawMessage(`{"v":9007199254740993}`)}},
		"extension too deep":      {Root: root, Extensions: map[string]json.RawMessage{"x/deep": nested(9999)}},
		"extension past decoding": {Root: root, Extensions: map[string]json.RawMessage{"x/deeper": nested(10001)}},
	} {
		if _, err := book.Bundle(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v, want ErrInvalid", name, err)
		}
	}
	key := seededKey(5).Public().(ed25519.PublicKey)
	bundle, err := book.Bundle(ctx, BundleRequest{Root: root, Suppress: []string{"agent_output", "agent_input", "agent_output"}, ProducerKey: key, Extensions: map[string]json.RawMessage{"x/deep": nested(9998)}})
	if err != nil {
		t.Fatal(err)
	}
	tree := bundleTree(t, bundle.JSON)
	if got := anySlice(tree["completeness"].(map[string]any)["suppressed_fields"]); !slices.Equal(got, []string{"agent_input", "agent_output"}) {
		t.Fatalf("bundle suppressed_fields %v", got)
	}
	var statement DisclosureStatement
	if err := json.Unmarshal(bundle.Disclosure.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(statement.SuppressedFields, []string{"agent_input", "agent_output"}) {
		t.Fatalf("disclosure record suppressed_fields %v", statement.SuppressedFields)
	}
	declared := tree["extensions"].(map[string]any)["producer-key/v1"].(map[string]any)["public_key"]
	if declared != hex.EncodeToString(key) {
		t.Fatalf("producer-key/v1 declares %v", declared)
	}
}
