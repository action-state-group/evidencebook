// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// peers is the book's log as a verified bundle's records.
func peers(book *Book) []PeerRecord {
	book.mu.Lock()
	defer book.mu.Unlock()
	out := make([]PeerRecord, len(book.log))
	for i, r := range book.log {
		header := r.Header
		out[i] = PeerRecord{RecordID: r.RecordID, Seq: r.Seq, Header: &header, HeaderVerified: true, CapsuleOK: true}
	}
	return out
}

// edited is a copy of records with the record id's header changed by edit.
func edited(records []PeerRecord, id string, edit func(*PeerRecord)) []PeerRecord {
	out := make([]PeerRecord, len(records))
	for i, r := range records {
		if r.Header != nil {
			header := *r.Header
			r.Header = &header
		}
		if r.RecordID == id {
			edit(&r)
		}
		out[i] = r
	}
	return out
}

func TestSubjectsOfRecordReadsAFieldMember(t *testing.T) {
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	m := mustAppend(t, book, member(root, testSubject, "half"))
	plain := mustAppend(t, book, observation("other", "unrelated"))
	records := peers(book)

	assert.Equal(t, []string{testSubject}, SubjectsOfRecord(records, m.RecordID))
	assert.Empty(t, SubjectsOfRecord(records, root.RecordID), "the root is no member")
	assert.Empty(t, SubjectsOfRecord(records, plain.RecordID), "a record citing no root is no member")
	assert.Empty(t, SubjectsOfRecord(records, "not-a-record"))

	// Read from the headers alone: only verified ones, and only with the root carried.
	unverified := edited(records, m.RecordID, func(r *PeerRecord) { r.HeaderVerified = false })
	assert.Empty(t, SubjectsOfRecord(unverified, m.RecordID), "an unverified member header decides nothing")
	unverified = edited(records, root.RecordID, func(r *PeerRecord) { r.HeaderVerified = false })
	assert.Empty(t, SubjectsOfRecord(unverified, m.RecordID), "an unverified root decides nothing")
	var rootless []PeerRecord
	for _, r := range records {
		if r.RecordID != root.RecordID {
			rootless = append(rootless, r)
		}
	}
	assert.Empty(t, SubjectsOfRecord(rootless, m.RecordID), "a member whose root the bundle does not carry")

	malformed := edited(records, root.RecordID, func(r *PeerRecord) {
		r.Header.Statement = json.RawMessage(`{"subject_kind":"exchange","subject_id":"exchange-1","membership":"field:x"}`)
	})
	assert.Empty(t, SubjectsOfRecord(malformed, m.RecordID), "a root whose statement does not read as one")

	// The library's membership exactly: control records are never members,
	// and a book admits members to its own subjects only, after their root.
	for _, recordType := range []string{RecordTypeSubjectRoot, RecordTypeSubjectClose, RecordTypeIndexRoot, RecordTypeDisclosure, RecordTypeRequestAnswered, RecordTypePolicy, RecordTypeLifecycle, RecordTypeBookClose, RecordTypeWindowOpen, RecordTypeWindowClose} {
		control := edited(records, m.RecordID, func(r *PeerRecord) { r.Header.RecordType = recordType })
		assert.Empty(t, SubjectsOfRecord(control, m.RecordID), "a %s record that cites the root and carries its id", recordType)
	}
	foreign := edited(records, m.RecordID, func(r *PeerRecord) { r.Header.BookID = "another-book" })
	assert.Empty(t, SubjectsOfRecord(foreign, m.RecordID), "another book's record citing this book's root")
	earlier := edited(records, m.RecordID, func(r *PeerRecord) { r.Header.Seq = root.Seq })
	assert.Empty(t, SubjectsOfRecord(earlier, m.RecordID), "a record not after its root")
	wrongID := edited(records, m.RecordID, func(r *PeerRecord) { r.Header.Correlation = &Correlation{ExchangeID: "another"} })
	assert.Empty(t, SubjectsOfRecord(wrongID, m.RecordID), "a record citing the root without carrying its id")
}

func TestSubjectsOfRecordReadsALogMemberWithNoCitation(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "deal-book", 1).open(t)
	root, err := book.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "deal", SubjectID: "deal-1", Membership: MembershipLog}, Entry{})
	require.NoError(t, err)
	m := mustAppend(t, book, observation("deal-1", "offer"))
	require.NotContains(t, m.Header.LinksTo(Cites), root.RecordID)
	_, err = book.Checkpoint(ctx)
	require.NoError(t, err)
	records := peers(book)

	assert.Equal(t, []string{"deal-1"}, SubjectsOfRecord(records, m.RecordID))
	var bookkeeping int
	for _, r := range records {
		if r.Header.RecordType == RecordTypeIndexRoot {
			bookkeeping++
			assert.Empty(t, SubjectsOfRecord(records, r.RecordID), "the checkpoint's index_root is bookkeeping")
		}
	}
	require.Positive(t, bookkeeping)
	foreign := edited(records, m.RecordID, func(r *PeerRecord) { r.Header.BookID = "another-book" })
	assert.Empty(t, SubjectsOfRecord(foreign, m.RecordID), "a log subject's members are its own book's")
	late := edited(records, root.RecordID, func(r *PeerRecord) { r.Header.Seq = 0 })
	assert.Empty(t, SubjectsOfRecord(late, m.RecordID), "a log root is its book's first record")
	var moved []PeerRecord
	for _, r := range edited(records, root.RecordID, func(r *PeerRecord) { r.Header.Seq = 5 }) {
		if r.RecordID == m.RecordID {
			r.Header.Seq = 6
		}
		moved = append(moved, r)
	}
	assert.Empty(t, SubjectsOfRecord(moved, m.RecordID), "a log root after seq 1 roots nothing")
}

func TestSubjectsOfRecordNamesEverySubjectARecordIsAMemberOf(t *testing.T) {
	// A book refuses a member of two subjects; a bundle may still carry one
	// (forged), and the reader names both so its caller can refuse.
	book := newFixture(t, "holder", 1).open(t)
	first := subjectRoot(t, book, testSubject)
	second, err := book.SealSubjectRoot(context.Background(), SubjectRootStatement{SubjectKind: "case", SubjectID: "case-9", Membership: MembershipFieldPrefix + "/subject_ref"}, Entry{})
	require.NoError(t, err)
	book.forgeSubjects = true
	entry := member(first, testSubject, "both")
	entry.SubjectRef = "case-9"
	entry.Links = append(entry.Links, Link{Type: Cites, Target: second.RecordID})
	both := mustAppend(t, book, entry)
	book.forgeSubjects = false

	assert.Equal(t, []string{"case-9", testSubject}, SubjectsOfRecord(peers(book), both.RecordID))
}

func TestSubjectsOfRecordReadsAVerifiedBundle(t *testing.T) {
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	m := mustAppend(t, book, member(root, testSubject, "half"))
	bundle, _ := fullBundle(t, book, SubjectBundleRequest{})
	verified, err := VerifyBundle(bundle.JSON)
	require.NoError(t, err)
	assert.Equal(t, []string{testSubject}, SubjectsOfRecord(verified.Records, m.RecordID))
}
