// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-emit/go/artifact"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The construction is capsule-cli's commitText, byte for byte: SHA-256 over
// JCS({"nonce", "text"}). The expected digest is capsule-cli's
// canonical.JSONDigest of the same object, which JCS orders "nonce" before
// "text" and escapes no non-ASCII character.
func TestCommitmentIsSHA256OfJCSNonceText(t *testing.T) {
	opening := Opening{Nonce: strings.Repeat("ab", 32), Text: "Total < 100 €, \"quoted\"\n"}
	got, err := opening.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	want := digestBytes([]byte(`{"nonce":"` + strings.Repeat("ab", 32) + `","text":"Total < 100 €, \"quoted\"\n"}`))
	if got != want {
		t.Fatalf("commitment %s, want %s", got, want)
	}
	if !VerifyOpening(want, opening) {
		t.Fatal("an opening must open its own commitment")
	}
}

func TestAnOpeningOnlyOpensItsOwnCommitment(t *testing.T) {
	opening, err := NewOpening("terms v1")
	if err != nil {
		t.Fatal(err)
	}
	commitment, err := opening.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewOpening("terms v1")
	for name, bad := range map[string]Opening{
		"another nonce":      other,
		"another text":       {Nonce: opening.Nonce, Text: "terms v2"},
		"uppercase nonce":    {Nonce: strings.ToUpper(opening.Nonce), Text: opening.Text},
		"short nonce":        {Nonce: opening.Nonce[:62], Text: opening.Text},
		"invalid UTF-8 text": {Nonce: opening.Nonce, Text: "\xff"},
	} {
		if VerifyOpening(commitment, bad) {
			t.Fatalf("%s must not open the commitment", name)
		}
	}
	if VerifyOpening(strings.ToUpper(commitment), opening) {
		t.Fatal("commitments compare as lowercase hex strings")
	}
	if other.Nonce == opening.Nonce {
		t.Fatal("every opening draws a fresh nonce")
	}
}

func TestFieldsAreCommittedSaltedAndOpenedFromTheArtifactStore(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-fields", 1).open(t)
	entry := observation("deal-1", "payload")
	entry.Fields = map[string]string{"terms": "net 30", "amount": "100"}
	record := mustAppend(t, book, entry)

	h := record.Header
	if h.CommitAlg != CommitAlgNonce256 || len(h.FieldCommitments) != 2 || h.RetentionState != Available {
		t.Fatalf("header: %+v", h)
	}
	if len(h.PayloadCommitments) != 1 {
		t.Fatalf("openings are not payloads: %v", h.PayloadCommitments)
	}
	if h.FieldCommitments["amount"] == digestBytes([]byte("100")) {
		t.Fatal("a field commitment is salted, never the bare text's digest")
	}
	opening, err := book.Opening(ctx, record.RecordID, "terms")
	if err != nil {
		t.Fatal(err)
	}
	if opening.Text != "net 30" || !VerifyOpening(h.FieldCommitments["terms"], opening) {
		t.Fatalf("opening %+v does not open %s", opening, h.FieldCommitments["terms"])
	}
	if _, err := book.Opening(ctx, record.RecordID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an uncommitted field: %v", err)
	}
	if _, err := book.ResolvePayload(ctx, record.RecordID, h.FieldCommitments["terms"]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an opening is never served as a payload: %v", err)
	}
	// The committed header survives a reopen and still validates.
	got, err := book.Get(ctx, record.RecordID)
	if err != nil || got.Header.FieldCommitments["terms"] != h.FieldCommitments["terms"] {
		t.Fatalf("get: %v %+v", err, got.Header)
	}
}

func TestFieldNamesAndCommitAlgAreChecked(t *testing.T) {
	book := newFixture(t, "book-field-names", 1).open(t)
	for _, name := range []string{"", "Upper", "_lead", "has space", strings.Repeat("a", 57)} {
		entry := observation("deal-1", "p")
		entry.Fields = map[string]string{name: "x"}
		if _, err := book.Append(context.Background(), entry); !errors.Is(err, ErrInvalid) {
			t.Fatalf("field name %q: %v", name, err)
		}
	}
	h := Header{Version: HeaderVersion, BookID: "b", Seq: 1, RecordType: "t", EpistemicType: ObservedEvent,
		CommittedAt: "2026-10-19T00:00:00Z", Links: []Link{}, RetentionState: Available,
		FieldCommitments: map[string]string{"terms": strings.Repeat("a", 64)}}
	if err := validateHeader(h); !errors.Is(err, ErrInvalid) {
		t.Fatal("field commitments without commit_alg are refused")
	}
	h.CommitAlg = "sha256"
	if err := validateHeader(h); !errors.Is(err, ErrInvalid) {
		t.Fatal("an unknown commit_alg is refused")
	}
	h.CommitAlg = CommitAlgNonce256
	if err := validateHeader(h); err != nil {
		t.Fatal(err)
	}
	h.FieldCommitments = nil
	if err := validateHeader(h); !errors.Is(err, ErrInvalid) {
		t.Fatal("commit_alg without field commitments is refused")
	}
}

// halfWith is a covered, trusted half committing text under field.
func halfWith(t *testing.T, id, field, text string) (Half, Opening) {
	t.Helper()
	opening, err := NewOpening(text)
	if err != nil {
		t.Fatal(err)
	}
	commitment, _ := opening.Commitment()
	return Half{RecordID: id, Correlation: Correlation{ExchangeID: "x-" + id[:1]},
		FieldCommitments: map[string]string{field: commitment}, Covered: true, Trusted: true}, opening
}

func TestOpenedFieldsDecideTheContentThreeWays(t *testing.T) {
	check := OpenedFieldsAgree("terms")
	run := func(a, b Half) ExchangeState {
		b.Correlation = a.Correlation
		results, _ := ReconcileHalvesWith(HalfSet{Halves: []Half{a}, Complete: true}, HalfSet{Halves: []Half{b}, Complete: true}, check)
		if len(results) != 1 {
			t.Fatalf("results %+v", results)
		}
		return results[0].State
	}
	a, oa := halfWith(t, "a1", "terms", "net 30")
	b, ob := halfWith(t, "b1", "terms", "net 30")
	c, oc := halfWith(t, "b2", "terms", "net 60")
	if a.FieldCommitments["terms"] == b.FieldCommitments["terms"] {
		t.Fatal("two halves of one text carry different commitments")
	}
	open := func(h Half, o Opening) Half { return OpenHalf(h, map[string]Opening{"terms": o}) }

	if got := run(open(a, oa), open(b, ob)); got != Matched {
		t.Fatalf("both opened, equal: %s", got)
	}
	if got := run(open(a, oa), open(c, oc)); got != Conflicting {
		t.Fatalf("both opened, different: %s", got)
	}
	for name, pair := range map[string][2]Half{
		"opened on one side only": {open(a, oa), b},
		"opened on neither side":  {a, b},
	} {
		if got := run(pair[0], pair[1]); got != Insufficient {
			t.Fatalf("%s: %s, want INSUFFICIENT", name, got)
		}
	}
	// An opening that does not open its own half makes that half untrusted:
	// UNRESOLVED, ahead of the missing opening on the other side.
	if got := run(open(a, ob), b); got != Unresolved {
		t.Fatalf("an invalid opening and a missing one: %s, want UNRESOLVED", got)
	}
	// An uncovered half stays INSUFFICIENT even with an invalid opening, as
	// the precedence puts coverage first.
	uncovered := open(a, ob)
	uncovered.Covered = false
	if got := run(uncovered, open(b, ob)); got != Insufficient {
		t.Fatalf("uncovered: %s", got)
	}
}

func TestComparatorKeepsItsTwoWayMeaning(t *testing.T) {
	a := Half{RecordID: "a", Correlation: Correlation{ExchangeID: "x"}, PayloadCommitments: []string{"d"}, Covered: true, Trusted: true}
	b := a
	b.RecordID = "b"
	for compare, want := range map[bool]ExchangeState{true: Matched, false: Conflicting} {
		results, _ := ReconcileHalves(HalfSet{Halves: []Half{a}}, HalfSet{Halves: []Half{b}}, func(Half, Half) bool { return compare })
		if results[0].State != want {
			t.Fatalf("comparator %v: %s", compare, results[0].State)
		}
	}
}

// The #36 acceptance: two books, a deal's halves committed salted on each
// side, opened from each side's artifact store, reach MATCHED.
func TestTwoBooksReconcileOpenedSaltedHalves(t *testing.T) {
	ctx := context.Background()
	a := newFixture(t, "book-a", 1).open(t)
	b := newFixture(t, "book-b", 2).open(t)
	add := func(book *Book, terms string) Record {
		entry := Entry{RecordType: "deal_step", EpistemicType: ProducerClaim, Links: []Link{},
			Correlation: Correlation{ExchangeID: "deal-7"}, Fields: map[string]string{"terms": terms}}
		return mustAppend(t, book, entry)
	}
	ra, rb := add(a, "net 30"), add(b, "net 30")
	if _, err := a.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	oa, err := a.Opening(ctx, ra.RecordID, "terms")
	if err != nil {
		t.Fatal(err)
	}
	ob, err := b.Opening(ctx, rb.RecordID, "terms")
	if err != nil {
		t.Fatal(err)
	}
	input := ReconcileInput{Peer: peerBundle(t, b), RecordType: "deal_step", Check: OpenedFieldsAgree("terms"),
		OwnOpenings:  map[string]map[string]Opening{ra.RecordID: {"terms": oa}},
		PeerOpenings: map[string]map[string]Opening{rb.RecordID: {"terms": ob}}}
	recon, err := a.Reconcile(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if recon.Tallies != (Tallies{Matched: 1}) {
		t.Fatalf("tallies %+v (pairs %+v)", recon.Tallies, recon.Pairs)
	}
	input.PeerOpenings = nil
	if recon, err = a.Reconcile(ctx, input); err != nil || recon.Tallies != (Tallies{Insufficient: 1}) {
		t.Fatalf("without the peer's opening: %v %+v", err, recon.Tallies)
	}
	input.Compare = SamePayloadCommitments
	if _, err := a.Reconcile(ctx, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("Compare and Check together are refused")
	}
}

// Openings are originals: a record's retention reaches them, and a deleted
// record's openings are destroyed with its payloads.
func TestRetentionReachesOpenings(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-opening-retention", 1).open(t)
	entry := Entry{RecordType: "deal_step", EpistemicType: ProducerClaim, Links: []Link{}, Fields: map[string]string{"terms": "net 30"}}
	record := mustAppend(t, book, entry)
	if _, err := book.SetRetention(ctx, record.RecordID, Withheld, "not shared"); err != nil {
		t.Fatal(err)
	}
	var unavailable *UnavailableError
	if _, err := book.Opening(ctx, record.RecordID, "terms"); !errors.As(err, &unavailable) || unavailable.State != Withheld {
		t.Fatalf("withheld: %v", err)
	}
	if _, err := book.SetRetention(ctx, record.RecordID, Deleted, "expiry"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Opening(ctx, record.RecordID, "terms"); !errors.As(err, &unavailable) || unavailable.State != Deleted {
		t.Fatalf("deleted: %v", err)
	}
	held, err := book.originals.Get(ctx, record.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range held.Artifacts {
		if original.Content != nil {
			t.Fatalf("the opening's bytes must be purged: %+v", original)
		}
	}
}

// The published header schema accepts the headers the book seals, with and
// without field commitments, and refuses commit_alg without them.
func TestTheHeaderSchemaAcceptsFieldCommitments(t *testing.T) {
	raw, err := os.ReadFile("schemas/record-header.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("record-header.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("record-header.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	validate := func(header []byte) error {
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(header))
		if err != nil {
			t.Fatal(err)
		}
		return schema.Validate(value)
	}
	book := newFixture(t, "book-schema", 1).open(t)
	plain := mustAppend(t, book, observation("s", "p"))
	entry := observation("s", "p")
	entry.Fields = map[string]string{"terms": "net 30"}
	salted := mustAppend(t, book, entry)
	for _, record := range []Record{plain, salted} {
		header, err := canonicalJSON(record.Header)
		if err != nil {
			t.Fatal(err)
		}
		if err := validate(header); err != nil {
			t.Fatalf("a sealed header fails the schema: %v\n%s", err, header)
		}
	}
	bad := salted.Header
	bad.FieldCommitments = nil
	header, _ := canonicalJSON(bad)
	if validate(header) == nil {
		t.Fatal("commit_alg without field_commitments must fail the schema")
	}
}

// A record a caller holds is its own copy: changing its maps cannot change
// what the book commits or trusts.
func TestARecordsMapsAreTheCallersOwn(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-alias", 1).open(t)
	entry := observation("s", "p")
	entry.Fields = map[string]string{"terms": "net 30"}
	appended := mustAppend(t, book, entry)
	want := appended.Header.FieldCommitments["terms"]
	appended.Header.FieldCommitments["terms"] = strings.Repeat("0", 64)
	got, err := book.Get(ctx, appended.RecordID)
	if err != nil || got.Header.FieldCommitments["terms"] != want {
		t.Fatalf("Append's result aliases the book's record: %v", err)
	}
	got.Header.FieldCommitments["terms"] = strings.Repeat("1", 64)
	got.Header.PayloadCommitments[0] = strings.Repeat("2", 64)
	all, err := book.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	all[0].Header.FieldCommitments["terms"] = strings.Repeat("3", 64)
	again, _ := book.Get(ctx, appended.RecordID)
	if again.Header.FieldCommitments["terms"] != want || again.Header.PayloadCommitments[0] == strings.Repeat("2", 64) {
		t.Fatal("Get or Query results alias the book's record")
	}
}

// A caller-sealed record commits its fields the same way.
func TestACallerSealedRecordCommitsItsFields(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-caller-fields", 1).open(t)
	entry := publishedEntry()
	entry.Fields = map[string]string{"terms": "net 30"}
	payload := `{"order":18}`
	record, err := book.AppendCapsule(ctx, entry, boundPayload(payload), ownSealer(t, 1, payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	if record.Header.CommitAlg != CommitAlgNonce256 || len(record.Header.FieldCommitments) != 1 {
		t.Fatalf("header %+v", record.Header)
	}
	opening, err := book.Opening(ctx, record.RecordID, "terms")
	if err != nil || opening.Text != "net 30" {
		t.Fatalf("opening %+v %v", opening, err)
	}
}

// Openings survive a reopen: they are read from the artifact store.
func TestAnOpeningSurvivesAReopen(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-reopen-opening", 1)
	book := f.open(t)
	entry := observation("s", "p")
	entry.Fields = map[string]string{"terms": "net 30"}
	record := mustAppend(t, book, entry)
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	opening, err := reopened.Opening(ctx, record.RecordID, "terms")
	if err != nil || opening.Text != "net 30" || !VerifyOpening(record.Header.FieldCommitments["terms"], opening) {
		t.Fatalf("opening after reopen: %+v %v", opening, err)
	}
}

// Bytes in the artifact store that do not open the header's commitment are
// corruption, never served as the opening.
func TestATamperedOpeningIsCorrupt(t *testing.T) {
	opening, _ := NewOpening("net 30")
	commitment, _ := opening.Commitment()
	tampered, _ := canonicalJSON(Opening{Nonce: opening.Nonce, Text: "net 60"})
	if _, err := openingFrom(artifact.Artifact{Name: "opening.terms", Content: tampered}, commitment); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := openingFrom(artifact.Artifact{Name: "opening.terms", Content: []byte("not json")}, commitment); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("garbage: %v", err)
	}
	good, _ := canonicalJSON(opening)
	if got, err := openingFrom(artifact.Artifact{Name: "opening.terms", Content: good}, commitment); err != nil || got != opening {
		t.Fatalf("good: %v", err)
	}
}
