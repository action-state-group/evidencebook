// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSubject = "exchange-1"

// subjectRoot seals a field subject_root for id under correlation.exchange_id.
func subjectRoot(t *testing.T, book *Book, id string) Record {
	t.Helper()
	root, err := book.SealSubjectRoot(context.Background(), SubjectRootStatement{SubjectKind: "exchange", SubjectID: id, Membership: MembershipExchangeID}, Entry{})
	require.NoError(t, err)
	return root
}

// member is a member entry of the subject id rooted at root.
func member(root Record, id, payload string) Entry {
	return Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Correlation: Correlation{ExchangeID: id},
		Links: []Link{{Type: Cites, Target: root.RecordID}}, Payloads: [][]byte{[]byte(payload)}}
}

// fullBundle is the holder's own full subject bundle, verified.
func fullBundle(t *testing.T, book *Book, req SubjectBundleRequest) (SubjectBundle, VerifiedSubject) {
	t.Helper()
	req.OwnerCopy = true
	if req.SubjectID == "" {
		req.SubjectID = testSubject
	}
	bundle, err := book.SubjectBundle(context.Background(), req)
	require.NoError(t, err)
	verified, err := VerifySubjectBundle(bundle.JSON)
	require.NoError(t, err)
	return bundle, verified
}

// editExtension rewrites a full bundle's subject extension, as a dishonest
// holder or a man in the middle would.
func editExtension(t *testing.T, data []byte, edit func(*SubjectExtension)) []byte {
	t.Helper()
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &wire))
	var extensions map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire["extensions"], &extensions))
	var ext SubjectExtension
	require.NoError(t, json.Unmarshal(extensions[ExtensionSubject], &ext))
	edit(&ext)
	var err error
	extensions[ExtensionSubject], err = json.Marshal(ext)
	require.NoError(t, err)
	wire["extensions"], err = json.Marshal(extensions)
	require.NoError(t, err)
	out, err := canonicalJSON(wire)
	require.NoError(t, err)
	return out
}

func TestSubjectRootRefusals(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)

	_, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "exchange", SubjectID: testSubject, Membership: MembershipExchangeID}, Entry{})
	assert.ErrorIs(t, err, ErrInvalid, "a second root for one subject id is refused")
	_, err = book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "deal", SubjectID: "deal-1", Membership: MembershipLog}, Entry{})
	assert.ErrorIs(t, err, ErrInvalid, "a log root is the book's first record")
	for _, membership := range []string{"", "field:", "field:x", "field:/a~2", "field:/a~", "logs"} {
		_, err = book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "exchange", SubjectID: "x-" + membership, Membership: membership}, Entry{})
		assert.ErrorIs(t, err, ErrInvalid, "membership %q", membership)
	}
	_, err = book.Append(ctx, Entry{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Links: []Link{}, Statement: json.RawMessage(`{"subject_kind":"exchange","subject_id":"y","membership":"log","extra":1}`)})
	assert.ErrorIs(t, err, ErrInvalid, "a root appended directly is checked strictly")

	wrong := member(root, "another-exchange", "p")
	_, err = book.Append(ctx, wrong)
	assert.ErrorIs(t, err, ErrInvalid, "a record citing a root carries its subject id")

	other := subjectRoot(t, book, "exchange-2")
	both := member(root, testSubject, "p")
	both.Links = append(both.Links, Link{Type: Cites, Target: other.RecordID})
	both.SubjectRef = "exchange-2"
	_, err = book.Append(ctx, both)
	assert.ErrorIs(t, err, ErrInvalid, "a record cites a second root only carrying that subject's id too")

	m1 := mustAppend(t, book, member(root, testSubject, "one"))
	_, err = book.Append(ctx, Entry{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: root.RecordID}}, Statement: json.RawMessage(`{"members":2,"last":"` + m1.RecordID + `"}`)})
	assert.ErrorIs(t, err, ErrInvalid, "a close states the count the book holds")
	closed, err := book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)
	var statement SubjectCloseStatement
	require.NoError(t, json.Unmarshal(closed.Header.Statement, &statement))
	assert.Equal(t, SubjectCloseStatement{Members: 1, Last: m1.RecordID}, statement)
	_, err = book.CloseSubject(ctx, root.RecordID)
	assert.ErrorIs(t, err, ErrInvalid, "a subject closes once")
	_, err = book.CloseSubject(ctx, dg("no-such-root"))
	assert.ErrorIs(t, err, ErrNotFound)

	// Worked case (iv): a member sealed after the close is refused.
	_, err = book.Append(ctx, member(root, testSubject, "late"))
	assert.ErrorIs(t, err, ErrInvalid)
	// The holder's own bookkeeping still runs on a closed subject.
	_, _ = fullBundle(t, book, SubjectBundleRequest{})
	_, _ = fullBundle(t, book, SubjectBundleRequest{})
}

// A record in two field subjects would need two keys in the subject_id
// index; it is refused, whichever order it cites the roots in.
func TestARecordIsAMemberOfOneSubject(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	a := subjectRoot(t, book, "a")
	b, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "session", SubjectID: "b", Membership: "field:/subject_ref"}, Entry{})
	require.NoError(t, err)
	for _, links := range [][]Link{{{Type: Cites, Target: a.RecordID}, {Type: Cites, Target: b.RecordID}}, {{Type: Cites, Target: b.RecordID}, {Type: Cites, Target: a.RecordID}}} {
		_, err := book.Append(ctx, Entry{RecordType: "turn", EpistemicType: ObservedEvent, Correlation: Correlation{ExchangeID: "a"}, SubjectRef: "b", Links: links})
		assert.ErrorIs(t, err, ErrInvalid)
	}
	mustAppend(t, book, Entry{RecordType: "turn", EpistemicType: ObservedEvent, SubjectRef: "b", Links: []Link{{Type: Cites, Target: b.RecordID}}})
}

// Worked case (i): root R, members M1 and M2 citing R, close C with
// members 2. The bundle proves them; one omitting M2 fails.
func TestSubjectBundleWorkedCaseOne(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	mustAppend(t, book, observation("unrelated", "x"))
	m1 := mustAppend(t, book, member(root, testSubject, "one"))
	m2 := mustAppend(t, book, member(root, testSubject, "two"))
	closed, err := book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)

	bundle, verified := fullBundle(t, book, SubjectBundleRequest{})
	assert.Equal(t, SubjectLevelHolderCommitment, verified.Level)
	assert.Equal(t, []string{m1.RecordID, m2.RecordID}, verified.Members)
	assert.Equal(t, closed.RecordID, verified.Close)
	assert.True(t, verified.Closed)
	assert.Equal(t, RecordTypeDisclosure, bundle.Disclosure.Header.RecordType)

	omitted := editExtension(t, bundle.JSON, func(ext *SubjectExtension) { ext.Members = ext.Members[:1] })
	_, err = VerifySubjectBundle(omitted)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "counting its 1 members", "the close counts 2")
	unclosed := editExtension(t, bundle.JSON, func(ext *SubjectExtension) { ext.Members, ext.Close, ext.Closed = ext.Members[:1], "", false })
	_, err = VerifySubjectBundle(unclosed)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "key range proves 4 records", "M2 is in the key range proof")
}

// dishonestBook is a holder whose subject_id index leaves M2 out, so it
// can prove a subject without M2 against its own commitment.
func dishonestBook(t *testing.T, leaveOut *string) *Book {
	t.Helper()
	f := newFixture(t, "holder", 1)
	cfg := f.config(t)
	honest := NewSubjectIDIndex()
	keyOf := honest.keyOf
	honest.keyOf = func(r Record) (string, bool) {
		key, ok := keyOf(r)
		return key, ok && r.RecordID != *leaveOut
	}
	cfg.Authenticated = []AuthenticatedIndex{NewRecordIDIndex(), NewSubjectIndex(), honest}
	book, err := Open(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Release() })
	return book
}

// dishonestBundle is the full form a holder builds when it leaves m2 out.
func dishonestBundle(t *testing.T, book *Book, m2 string, discloseLog bool) []byte {
	t.Helper()
	book.mu.Lock()
	defer book.mu.Unlock()
	s := book.subjects[0]
	members := book.membersLocked(s)
	members = members[:len(members)-1]
	require.NotEqual(t, m2, members[len(members)-1].RecordID)
	bundle, err := book.subjectFullLocked(context.Background(), s, members, SubjectBundleRequest{OwnerCopy: true, DiscloseLog: discloseLog})
	require.NoError(t, err)
	return bundle.JSON
}

// Worked cases (ii) and (iii): a holder that leaves M2 out of its index,
// with the subject open or under a close claiming one member, passes the
// first level, as the index's limit allows, and fails the second wherever
// the log prefix is disclosed.
func TestSubjectBundleDishonestHolder(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ii open", true: "iii close claims one"}[closed], func(t *testing.T) {
			ctx := context.Background()
			var leaveOut string
			book := dishonestBook(t, &leaveOut)
			root := subjectRoot(t, book, testSubject)
			mustAppend(t, book, member(root, testSubject, "one"))
			m1 := book.log[len(book.log)-1]
			m2 := mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Correlation: Correlation{ExchangeID: testSubject}, Links: []Link{{Type: Cites, Target: root.RecordID}}})
			leaveOut = m2.RecordID
			require.NoError(t, book.rebuildIndexes(ctx))
			if closed {
				book.forgeSubjects = true
				_, err := book.Append(ctx, Entry{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: root.RecordID}}, Statement: json.RawMessage(`{"last":"` + m1.RecordID + `","members":1}`)})
				require.NoError(t, err)
				book.forgeSubjects = false
			}

			first, err := VerifySubjectBundle(dishonestBundle(t, book, m2.RecordID, false))
			require.NoError(t, err, "the first level is the holder's commitment, which a dishonest index passes")
			assert.Equal(t, SubjectLevelHolderCommitment, first.Level, "a shared-log bundle says it is first-level only")
			assert.Equal(t, []string{m1.RecordID}, first.Members)

			_, err = VerifySubjectBundle(dishonestBundle(t, book, m2.RecordID, true))
			require.ErrorIs(t, err, ErrInvalid, "the disclosed log prefix shows M2")
			assert.Contains(t, err.Error(), "the log prefix holds 2 members; the extension lists 1")
		})
	}
}

// Worked case (iv), in the verifier: a member after the close (a forged
// book) fails.
func TestSubjectBundleMemberAfterCloseFails(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	mustAppend(t, book, member(root, testSubject, "one"))
	_, err := book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)
	book.forgeSubjects = true
	mustAppend(t, book, member(root, testSubject, "late"))
	book.forgeSubjects = false
	bundle, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, OwnerCopy: true})
	require.NoError(t, err)
	_, err = VerifySubjectBundle(bundle.JSON)
	assert.ErrorIs(t, err, ErrInvalid)
}

// Worked case (v): M1 citing a record whose payload is withheld is carried
// in full, and that record is listed as withheld; a foreign citation is
// listed by digest, for the held store.
func TestSubjectBundleWithheldPayloadAndForeignCitation(t *testing.T) {
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	evidence := mustAppend(t, book, observation("evidence", "secret"))
	cited := member(root, testSubject, "one")
	cited.Links = append(cited.Links, Link{Type: Cites, Target: evidence.RecordID})
	m1 := mustAppend(t, book, cited)
	foreign := member(root, testSubject, "two")
	foreign.Links = append(foreign.Links, Link{Type: Cites, Target: dg("a counterparty's half")})
	mustAppend(t, book, foreign)

	_, verified := fullBundle(t, book, SubjectBundleRequest{Withhold: evidence.Header.PayloadCommitments})
	assert.Contains(t, verified.Members, m1.RecordID)
	assert.Equal(t, []WithheldItem{{RecordID: evidence.RecordID, PayloadCommitment: evidence.Header.PayloadCommitments[0], Digest: evidence.Header.PayloadCommitments[0]}}, verified.Withheld)
	assert.Equal(t, []string{dg("a counterparty's half")}, verified.HeldByDigest)
	carried := false
	for _, r := range verified.Bundle.Records {
		carried = carried || (r.RecordID == evidence.RecordID && r.HeaderVerified)
	}
	assert.True(t, carried, "the cited record's header is carried; only its payload is withheld")
}

// projectionBook writes, at fixed times, a subject with visible members V1
// and V2 and, when at is not -1, a protected member (one citing a conduct
// pack) before (0), between (1) or after (2) them.
func projectionBook(t *testing.T, at int) *Book {
	t.Helper()
	f := newFixture(t, "holder", 1)
	book := f.open(t)
	set := func(minute int) {
		f.clock.mu.Lock()
		f.clock.t = time.Date(2026, 11, 26, 12, minute, 0, 0, time.UTC)
		f.clock.mu.Unlock()
	}
	set(0)
	root := subjectRoot(t, book, testSubject)
	protected := func() {
		if at >= 0 {
			set(30 + at)
			p := member(root, testSubject, "ask")
			p.SubjectRef = "conduct-pack@1"
			mustAppend(t, book, p)
		}
	}
	if at == 0 {
		protected()
	}
	set(10)
	mustAppend(t, book, member(root, testSubject, "v1"))
	if at == 1 {
		protected()
	}
	set(20)
	mustAppend(t, book, member(root, testSubject, "v2"))
	if at == 2 {
		protected()
	}
	return book
}

// Worked case (vi): the projection a peer receives is byte-identical with
// and without a protected member, wherever it sits; the full form differs.
func TestSubjectBundleProjectionHidesAProtectedMember(t *testing.T) {
	ctx := context.Background()
	def, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer"}, []string{"record_type", "committed_at", "correlation", "payload_commitments", "statement"})
	require.NoError(t, err)
	project := func(book *Book) []byte {
		id, err := book.RegisterDerivation(def)
		require.NoError(t, err)
		req := SubjectBundleRequest{SubjectID: testSubject, Derivation: id, Requester: "peer", ConductPacks: []string{"conduct-pack"}}
		bundle, err := book.SubjectBundle(ctx, req)
		require.NoError(t, err)
		again, err := book.SubjectBundle(ctx, req)
		require.NoError(t, err)
		require.Equal(t, bundle.JSON, again.JSON, "a repeated request gets the same bytes")
		verified, err := VerifySubjectBundle(bundle.JSON)
		require.NoError(t, err)
		assert.Equal(t, SubjectLevelHolderAssertion, verified.Level)
		assert.Equal(t, 2, verified.MemberCount)
		_, err = book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, Derivation: id, Requester: "stranger"})
		assert.ErrorIs(t, err, ErrRefused, "the refusal is the same whatever the subject holds")
		return bundle.JSON
	}
	without := project(projectionBook(t, -1))
	assert.NotContains(t, string(without), `"seq"`)
	for at := range 3 {
		assert.Equal(t, string(without), string(project(projectionBook(t, at))), "protected member at %d", at)
	}
	full := func(book *Book) VerifiedSubject {
		_, verified := fullBundle(t, book, SubjectBundleRequest{})
		return verified
	}
	assert.Len(t, full(projectionBook(t, -1)).Members, 2)
	assert.Len(t, full(projectionBook(t, 1)).Members, 3, "the full form lists the protected member")
}

func TestSubjectBundleDefinitions(t *testing.T) {
	ctx := context.Background()
	_, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer"}, []string{"seq"})
	assert.ErrorIs(t, err, ErrInvalid, "a projection carries no position")
	_, err = SubjectBundleDefinition(SubjectBundleFull, []string{"adjudicator"}, []string{"record_type"})
	assert.ErrorIs(t, err, ErrInvalid, "a full form discloses whole headers")
	_, err = SubjectBundleDefinition("partial", nil, nil)
	assert.ErrorIs(t, err, ErrInvalid)

	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	m1 := mustAppend(t, book, member(root, testSubject, "one"))
	full, err := SubjectBundleDefinition(SubjectBundleFull, []string{"adjudicator"}, nil)
	require.NoError(t, err)
	fullID, err := book.RegisterDerivation(full)
	require.NoError(t, err)
	a, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer-a"}, []string{"record_type"})
	require.NoError(t, err)
	b, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer-b"}, []string{"record_type", "statement"})
	require.NoError(t, err)
	aID, err := book.RegisterDerivation(a)
	require.NoError(t, err)
	bID, err := book.RegisterDerivation(b)
	require.NoError(t, err, "two grants are two definitions")
	assert.NotEqual(t, aID, bID)

	bundle, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, Derivation: fullID, Requester: "adjudicator"})
	require.NoError(t, err)
	verified, err := VerifySubjectBundle(bundle.JSON)
	require.NoError(t, err)
	assert.Equal(t, []string{m1.RecordID}, verified.Members)
	_, err = book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, Derivation: aID, Requester: "peer-b"})
	assert.ErrorIs(t, err, ErrRefused)
	_, err = book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: "no-such-subject", Derivation: aID, Requester: "peer-a"})
	assert.ErrorIs(t, err, ErrNotFound)

	// Respond does not serve a subject bundle definition: an evidence
	// request answer carries an anchor a projection must not.
	g := gate{derivations: book.derivations}
	_, ok := g.responderPolicy(false).Derivation(aID)
	assert.False(t, ok)
}

// A per-subject book (a log root at seq 1) proves its members by the log
// prefix: the second level.
func TestSubjectBundlePerSubjectBookAtTheSecondLevel(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "deal-book", 1).open(t)
	root, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "deal", SubjectID: "deal-1", Membership: MembershipLog}, Entry{})
	require.NoError(t, err)
	require.Equal(t, uint64(1), root.Seq)
	_, err = book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "exchange", SubjectID: "other", Membership: MembershipExchangeID}, Entry{})
	assert.ErrorIs(t, err, ErrInvalid, "a per-subject book roots no other subject")
	m1 := mustAppend(t, book, observation("deal-1", "offer"))
	_, err = book.Checkpoint(ctx)
	require.NoError(t, err)
	m2 := mustAppend(t, book, observation("deal-1", "accept"))
	_, err = book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)
	_, err = book.Append(ctx, observation("deal-1", "late"))
	assert.ErrorIs(t, err, ErrInvalid, "nothing but bookkeeping follows a per-subject book's close")

	bundle, verified := fullBundle(t, book, SubjectBundleRequest{SubjectID: "deal-1"})
	assert.Equal(t, SubjectLevelLogPrefix, verified.Level)
	assert.Equal(t, []string{m1.RecordID, m2.RecordID}, verified.Members, "the index_root record is bookkeeping, not a member")
	assert.True(t, verified.Closed)
	omitted := editExtension(t, bundle.JSON, func(ext *SubjectExtension) { ext.Members = ext.Members[1:] })
	_, err = VerifySubjectBundle(omitted)
	assert.ErrorIs(t, err, ErrInvalid)
}

// A book that declares no subject writes byte for byte what it wrote
// before the subject_id index existed.
func TestABookWithNoSubjectIsUnchanged(t *testing.T) {
	ctx := context.Background()
	write := func(indexes []AuthenticatedIndex) ([]string, Checkpoint) {
		f := newFixture(t, "holder", 1)
		cfg := f.config(t)
		cfg.Authenticated = indexes
		book, err := Open(ctx, cfg)
		require.NoError(t, err)
		defer book.Release()
		mustAppend(t, book, observation("s", "one"))
		mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Correlation: Correlation{ExchangeID: "e"}, Links: []Link{}})
		_, err = book.Checkpoint(ctx)
		require.NoError(t, err)
		mustAppend(t, book, observation("s", "two"))
		cp, err := book.Checkpoint(ctx)
		require.NoError(t, err)
		var ids []string
		for _, r := range book.log {
			ids = append(ids, r.RecordID)
		}
		return ids, cp
	}
	beforeIDs, beforeCP := write([]AuthenticatedIndex{NewRecordIDIndex(), NewSubjectIndex()})
	afterIDs, afterCP := write(nil)
	assert.Equal(t, beforeIDs, afterIDs)
	assert.Equal(t, beforeCP.Root, afterCP.Root)
	assert.Equal(t, beforeCP.Entries, afterCP.Entries)

	// An explicit proof over the empty index commits its root.
	book := newFixture(t, "holder", 1).open(t)
	answer, err := book.ProveKeyRange(ctx, SubjectIDIndexName, "x", "x")
	require.NoError(t, err)
	assert.Empty(t, answer.Proof.Matches)
	assert.Equal(t, uint64(0), answer.Root.Size)
}

// correlationOf reads a cited root's subject id first, so a subject whose
// id sits at another pointer is answered under it.
func TestCorrelationReadsACitedRootFirst(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	root, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "session", SubjectID: "s-1", Membership: "field:/subject_ref"}, Entry{})
	require.NoError(t, err)
	m := mustAppend(t, book, Entry{RecordType: "turn", EpistemicType: ObservedEvent, SubjectRef: "s-1", Correlation: Correlation{ExchangeID: "e-9"}, Links: []Link{{Type: Cites, Target: root.RecordID}}})
	old := mustAppend(t, book, Entry{RecordType: "turn", EpistemicType: ObservedEvent, Correlation: Correlation{ExchangeID: "e-9"}, Links: []Link{}})
	view, err := book.view(ctx, book.logLocked(), nil)
	require.NoError(t, err)
	assert.Equal(t, "s-1", view.correlationOf(m.Header))
	assert.Equal(t, "e-9", view.correlationOf(old.Header), "a record citing no root reads as before")
	assert.Equal(t, []uint64{m.Seq - 1}, view.Correlated("s-1"))
}

// The subject_id index is rebuilt from the log on reopen to the same root.
func TestSubjectIndexSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "holder", 1)
	book := f.open(t)
	root := subjectRoot(t, book, testSubject)
	mustAppend(t, book, member(root, testSubject, "one"))
	_, err := book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)
	before, err := book.ProveKeyRange(ctx, SubjectIDIndexName, testSubject, testSubject)
	require.NoError(t, err)
	require.NoError(t, book.Release())
	again := f.open(t)
	after, err := again.ProveKeyRange(ctx, SubjectIDIndexName, testSubject, testSubject)
	require.NoError(t, err)
	assert.Equal(t, before.Root, after.Root)
	_, err = again.Append(ctx, member(root, testSubject, "late"))
	assert.ErrorIs(t, err, ErrInvalid, "the close is read back on open")
}

// Members and closes racing a subject bundle never leave it inconsistent.
func TestSubjectBundleUnderConcurrentAppends(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 8 {
			_, _ = book.Append(ctx, member(root, testSubject, "m"))
		}
		_, _ = book.CloseSubject(ctx, root.RecordID)
	}()
	for range 4 {
		bundle, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, OwnerCopy: true})
		require.NoError(t, err)
		_, err = VerifySubjectBundle(bundle.JSON)
		require.NoError(t, err)
	}
	wg.Wait()
	_, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, OwnerCopy: true})
	require.False(t, errors.Is(err, ErrCorrupt))
}

// A projection definition declares neither seq nor links: a visible
// member citing another visible member would otherwise carry an id that a
// protected record before it changes.
func TestSubjectProjectionCarriesNoRecordIDs(t *testing.T) {
	ctx := context.Background()
	for _, fields := range [][]string{{"links"}, {"seq"}, {"record_type", "links"}} {
		_, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer"}, fields)
		assert.ErrorIs(t, err, ErrInvalid, "%v", fields)
	}
	def, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer"}, []string{"record_type", "correlation", "payload_commitments"})
	require.NoError(t, err)
	write := func(protectedFirst bool) []byte {
		f := newFixture(t, "holder", 1)
		book := f.open(t)
		root := subjectRoot(t, book, testSubject)
		if protectedFirst {
			p := member(root, testSubject, "ask")
			p.SubjectRef = "conduct-pack@1"
			mustAppend(t, book, p)
		}
		v1 := mustAppend(t, book, member(root, testSubject, "v1"))
		v2 := member(root, testSubject, "v2")
		v2.Links = append(v2.Links, Link{Type: Cites, Target: v1.RecordID})
		mustAppend(t, book, v2)
		id, err := book.RegisterDerivation(def)
		require.NoError(t, err)
		bundle, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, Derivation: id, Requester: "peer", ConductPacks: []string{"conduct-pack"}})
		require.NoError(t, err)
		return bundle.JSON
	}
	assert.Equal(t, string(write(false)), string(write(true)))
}

// A protected root makes the subject a protected one, answered as an
// unknown subject is; a member that cannot be classified is left out, as
// a protected one is, so it changes neither the bytes nor the refusal.
func TestSubjectProjectionFailsClosed(t *testing.T) {
	ctx := context.Background()
	def, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer"}, []string{"record_type", "correlation"})
	require.NoError(t, err)
	ask := func(book *Book, subject string) ([]byte, error) {
		id, err := book.RegisterDerivation(def)
		require.NoError(t, err)
		bundle, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: subject, Derivation: id, Requester: "peer", ConductPacks: []string{"conduct-pack"}})
		return bundle.JSON, err
	}

	book := newFixture(t, "holder", 1).open(t)
	_, err = book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "exchange", SubjectID: "private", Membership: MembershipExchangeID}, Entry{SubjectRef: "conduct-pack@1"})
	require.NoError(t, err)
	_, protectedErr := ask(book, "private")
	_, unknownErr := ask(book, "no-such-subject")
	require.ErrorIs(t, protectedErr, ErrNotFound)
	require.ErrorIs(t, unknownErr, ErrNotFound)
	assert.Equal(t, strings.ReplaceAll(unknownErr.Error(), "no-such-subject", "private"), protectedErr.Error())

	write := func(malformed bool) []byte {
		book := newFixture(t, "holder", 1).open(t)
		root := subjectRoot(t, book, testSubject)
		if malformed {
			m := member(root, testSubject, "odd")
			m.Statement = json.RawMessage(`{"contract_ref":7}`)
			mustAppend(t, book, m)
		}
		mustAppend(t, book, member(root, testSubject, "v1"))
		data, err := ask(book, testSubject)
		require.NoError(t, err)
		return data
	}
	without, with := write(false), write(true)
	assert.Equal(t, string(without), string(with))
}

// A forged per-subject book, its log root after seq 1 or a second root in
// it, does not pass at the log-prefix level.
func TestSubjectBundleForgedLogRoot(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"root after seq 1", "second root"} {
		t.Run(name, func(t *testing.T) {
			book := newFixture(t, "deal-book", 1).open(t)
			book.forgeSubjects = true
			if name == "root after seq 1" {
				mustAppend(t, book, observation("hidden", "before the root"))
			}
			_, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "deal", SubjectID: "deal-1", Membership: MembershipLog}, Entry{})
			require.NoError(t, err)
			if name == "second root" {
				_, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "deal", SubjectID: "deal-2", Membership: MembershipLog}, Entry{})
				require.NoError(t, err)
			}
			mustAppend(t, book, observation("deal-1", "offer"))
			book.forgeSubjects = false
			bundle, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: "deal-1", OwnerCopy: true})
			require.NoError(t, err)
			_, err = VerifySubjectBundle(bundle.JSON)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

// An older, honest key range proof cannot stand in for the newer state the
// bundle's anchor covers.
func TestSubjectBundleRefusesAStaleProof(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	m1 := mustAppend(t, book, member(root, testSubject, "one"))
	old, _ := fullBundle(t, book, SubjectBundleRequest{})
	var oldExt SubjectExtension
	{
		verified, err := VerifyBundle(old.JSON)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(verified.Extensions[ExtensionSubject], &oldExt))
	}
	mustAppend(t, book, member(root, testSubject, "two"))
	_, err := book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)
	current, _ := fullBundle(t, book, SubjectBundleRequest{})
	stale := editExtension(t, current.JSON, func(ext *SubjectExtension) {
		ext.Members, ext.Close, ext.Closed, ext.KeyRange = []string{m1.RecordID}, "", false, oldExt.KeyRange
	})
	_, err = VerifySubjectBundle(stale)
	assert.ErrorIs(t, err, ErrInvalid)
}

// held_by_digest is checked against the members' closure: neither a
// dropped foreign citation nor an added one passes.
func TestSubjectBundleHeldByDigestIsChecked(t *testing.T) {
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	foreign := member(root, testSubject, "one")
	foreign.Links = append(foreign.Links, Link{Type: Cites, Target: dg("a counterparty's half")})
	mustAppend(t, book, foreign)
	bundle, verified := fullBundle(t, book, SubjectBundleRequest{})
	require.Equal(t, []string{dg("a counterparty's half")}, verified.HeldByDigest)
	for name, held := range map[string][]string{"dropped": {}, "added": {dg("a counterparty's half"), dg("unrelated")}} {
		_, err := VerifySubjectBundle(editExtension(t, bundle.JSON, func(ext *SubjectExtension) { ext.HeldByDigest = held }))
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
}

// A close after a member left pending by a substrate failure counts it.
func TestCloseSubjectCountsAPendingMember(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "holder", 1)
	cfg := f.config(t)
	flaky := &flakySubstrate{Substrate: cfg.Substrate}
	cfg.Substrate = flaky
	book, err := Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Release() })
	root := subjectRoot(t, book, testSubject)
	flaky.failures = 1
	_, err = book.Append(ctx, member(root, testSubject, "pending"))
	require.Error(t, err)
	closed, err := book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)
	var statement SubjectCloseStatement
	require.NoError(t, json.Unmarshal(closed.Header.Statement, &statement))
	assert.Equal(t, uint64(1), statement.Members)

	flaky.failures = 1
	_, err = book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "exchange", SubjectID: "late-root", Membership: MembershipExchangeID}, Entry{})
	require.Error(t, err)
	pendingRoot := book.pending.RecordID
	_, err = book.CloseSubject(ctx, pendingRoot)
	assert.NoError(t, err, "a pending root joins the log before the close looks it up")
}

// Membership pointers follow RFC 6901: an empty member name, the ~0 and ~1
// escapes, and array indices.
func TestSubjectMembershipPointers(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "holder", 1).open(t)
	for _, c := range []struct{ pointer, statement string }{
		{"/statement/", `{"":"s-empty"}`},
		{"/statement/a~1b", `{"a/b":"s-slash"}`},
		{"/statement/a~0b", `{"a~b":"s-tilde"}`},
		{"/statement/list/1", `{"list":["x","s-index"]}`},
	} {
		var id string
		var statement map[string]any
		require.NoError(t, json.Unmarshal([]byte(c.statement), &statement))
		for _, v := range statement {
			if s, ok := v.(string); ok {
				id = s
			} else {
				id = v.([]any)[1].(string)
			}
		}
		root, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "session", SubjectID: id, Membership: MembershipFieldPrefix + c.pointer}, Entry{})
		require.NoError(t, err, c.pointer)
		mustAppend(t, book, Entry{RecordType: "turn", EpistemicType: ObservedEvent, Statement: json.RawMessage(c.statement), Links: []Link{{Type: Cites, Target: root.RecordID}}})
		_, err = book.Append(ctx, Entry{RecordType: "turn", EpistemicType: ObservedEvent, Statement: json.RawMessage(`{"other":1}`), Links: []Link{{Type: Cites, Target: root.RecordID}}})
		assert.ErrorIs(t, err, ErrInvalid, c.pointer)
	}
}

// A full form discloses the whole header, field commitments and all; its
// definition declares no field list.
func TestSubjectBundleFullFormWholeHeader(t *testing.T) {
	ctx := context.Background()
	_, err := SubjectBundleDefinition(SubjectBundleFull, []string{"adjudicator"}, headerFields)
	assert.ErrorIs(t, err, ErrInvalid)
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	m := member(root, testSubject, "one")
	m.Fields = map[string]string{"note": "a committed field"}
	sealed := mustAppend(t, book, m)
	require.NotEmpty(t, sealed.Header.FieldCommitments)
	def, err := SubjectBundleDefinition(SubjectBundleFull, []string{"adjudicator"}, nil)
	require.NoError(t, err)
	assert.Contains(t, string(def.Document), `"fields":"whole_header"`)
	id, err := book.RegisterDerivation(def)
	require.NoError(t, err)
	bundle, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, Derivation: id, Requester: "adjudicator"})
	require.NoError(t, err)
	verified, err := VerifySubjectBundle(bundle.JSON)
	require.NoError(t, err)
	for _, r := range verified.Bundle.Records {
		if r.RecordID == sealed.RecordID {
			assert.Equal(t, sealed.Header.FieldCommitments, r.Header.FieldCommitments)
		}
	}
}
