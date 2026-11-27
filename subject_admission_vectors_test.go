// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The subject convention's admission vectors (testdata/subject/admission.json,
// evidencebook#1012 and #1013): sequences of record headers a book is asked
// to admit, each with the outcome the book gives (checkSubjectEntryLocked),
// the subject_id index's root after it (NewSubjectIDIndex), and, per case,
// the correlation identifier each admitted record is held under
// (bookView.correlationOf). They are header-level: record ids are fixed
// digests, not sealed capsules, so another implementation replays them
// through its own admission rule and index without sealing; byte parity of
// sealed records is the shelf interlock's (testdata/shelf/after-subject.json).

// Refusal classes: a stable name for each way a header breaks the
// convention, and a fragment of Go's message for it, which the test checks.
var subjectRefusals = map[string]string{
	"malformed_root":        "subject_root",
	"second_root":           "already holds subject_root",
	"per_subject_book":      "roots no other subject",
	"log_root_not_first":    "a log subject_root is the book's first record",
	"close_not_one":         "closes exactly one subject_root",
	"close_unknown_root":    "which is not a subject_root of this book",
	"close_again":           "is already closed by",
	"close_statement":       "subject_close statement",
	"close_count":           "the book holds",
	"cites_root_without_id": "carries the subject id",
	"member_after_close":    "a member sealed after its close is refused",
	"two_subjects":          "a record is a member of one subject",
}

type admissionStep struct {
	Note           string          `json:"note"`
	Header         json.RawMessage `json:"header"`
	RecordID       string          `json:"record_id"`
	Expect         string          `json:"expect"`
	SubjectIDIndex IndexRoot       `json:"subject_id_index"`
	// CheckpointCommits is whether a checkpoint after this step, in a book
	// that has never committed the subject_id index, commits its root: only
	// once the index holds a leaf (the lazy index, book.go
	// checkpointAtLocked).
	CheckpointCommits bool `json:"checkpoint_commits"`
}

type admissionHeld struct {
	RecordID  string `json:"record_id"`
	HeldUnder string `json:"held_under"`
}

type admissionCase struct {
	Name        string          `json:"name"`
	BookID      string          `json:"book_id"`
	Steps       []admissionStep `json:"steps"`
	Correlation []admissionHeld `json:"correlation"`
}

type admissionVectors struct {
	Description string          `json:"description"`
	Classes     []string        `json:"classes"`
	Cases       []admissionCase `json:"cases"`
}

// admissionRun replays one case against an in-package book, its subject_id
// index and its correlation reader.
type admissionRun struct {
	t     *testing.T
	c     *admissionCase
	book  *Book
	index *SortedMerkleIndex
	n     int
}

func newAdmissionRun(t *testing.T, name, bookID string) *admissionRun {
	return &admissionRun{t: t, c: &admissionCase{Name: name, BookID: bookID, Correlation: []admissionHeld{}}, book: &Book{id: bookID}, index: NewSubjectIDIndex()}
}

// id is the fixed record id of the run's next step.
func (r *admissionRun) id() string {
	r.n++
	return dg(r.c.Name + "/" + itoa(uint64(r.n)))
}

// step asks the book to admit a header built from h (its book, seq and
// fixed members filled in) and records the outcome; expect is "ok" or a
// refusal class.
func (r *admissionRun) step(note, expect string, h Header) Record {
	r.t.Helper()
	h.Version, h.BookID, h.Seq, h.CommittedAt = HeaderVersion, r.c.BookID, uint64(len(r.book.log))+1, "2026-11-27T00:00:00Z"
	if h.EpistemicType == "" {
		h.EpistemicType = ObservedEvent
	}
	if h.Links == nil {
		h.Links = []Link{}
	}
	raw, err := canonicalJSON(h)
	require.NoError(r.t, err)
	record := Record{RecordID: r.id(), Seq: h.Seq, Header: h}
	err = r.book.checkSubjectEntryLocked(h)
	if expect == "ok" {
		require.NoError(r.t, err, "%s: %s", r.c.Name, note)
		r.book.log = append(r.book.log, record)
		r.book.trackSubjectLocked(record)
		if h.RecordType != RecordTypeIndexRoot && !isWindowRecord(h.RecordType) && h.RecordType != RecordTypeBookClose {
			if key, ok := r.index.KeyOf(record); ok {
				require.NoError(r.t, r.index.Add(key, record.RecordID))
			}
		}
	} else {
		fragment, known := subjectRefusals[expect]
		require.True(r.t, known, expect)
		require.ErrorIs(r.t, err, ErrInvalid, "%s: %s", r.c.Name, note)
		require.Contains(r.t, err.Error(), fragment, "%s: %s", r.c.Name, note)
	}
	root := r.index.Root()
	r.c.Steps = append(r.c.Steps, admissionStep{Note: note, Header: raw, RecordID: record.RecordID, Expect: expect, SubjectIDIndex: root, CheckpointCommits: root.Size > 0})
	return record
}

// done fills the case's correlation: the identifier each admitted record
// is held under, a cited subject_root's subject_id first.
func (r *admissionRun) done() admissionCase {
	view := &bookView{}
	for _, rec := range r.book.log {
		if rec.Header.RecordType == RecordTypeSubjectRoot {
			if statement, err := readSubjectRoot(rec.Header.Statement); err == nil {
				if view.subjectRoots == nil {
					view.subjectRoots = map[string]string{}
				}
				view.subjectRoots[rec.RecordID] = statement.SubjectID
			}
		}
	}
	for _, rec := range r.book.log {
		r.c.Correlation = append(r.c.Correlation, admissionHeld{RecordID: rec.RecordID, HeldUnder: view.correlationOf(rec.Header)})
	}
	return *r.c
}

func rootStatement(kind, id, membership string) json.RawMessage {
	raw, _ := json.Marshal(SubjectRootStatement{SubjectKind: kind, SubjectID: id, Membership: membership})
	return raw
}

func rootHeader(kind, id, membership string) Header {
	return Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: rootStatement(kind, id, membership)}
}

func closeHeader(root string, members int, last string) Header {
	raw, _ := json.Marshal(SubjectCloseStatement{Members: uint64(members), Last: last})
	return Header{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: root}}, Statement: raw}
}

func citing(recordType, exchangeID string, roots ...string) Header {
	h := Header{RecordType: recordType}
	if exchangeID != "" {
		h.Correlation = &Correlation{ExchangeID: exchangeID}
	}
	for _, root := range roots {
		h.Links = append(h.Links, Link{Type: Cites, Target: root})
	}
	return h
}

func buildAdmissionVectors(t *testing.T) admissionVectors {
	v := admissionVectors{Description: "The subject convention v0's admission rule, header by header (evidencebook#116 design note section 4; Go checkSubjectEntryLocked, NewSubjectIDIndex, bookView.correlationOf), written by subject_admission_vectors_test.go (EB_WRITE_SUBJECT_VECTORS=1). Each case starts from an empty book. Each step is a record header the book is asked to admit, with a fixed record id: expect is ok (the record joins the log at that seq) or a refusal class (it does not, and the next step takes the same seq). subject_id_index is the index's root after the step, over the admitted records in log order (an index_root, window_open, window_close or book_close record is never indexed); checkpoint_commits is whether a checkpoint then, in a book that has never committed that index, commits it (once it holds a leaf). correlation lists, for each admitted record in log order, the identifier a correlation subject holds it under: the subject_id of the first subject_root it cites, else its correlation.exchange_id, else its subject_ref."}
	for class := range subjectRefusals {
		v.Classes = append(v.Classes, class)
	}
	sortStrings(v.Classes)

	// A field subject under correlation.exchange_id: worked cases (i) and
	// (iv), and every way a header can break it.
	r := newAdmissionRun(t, "field subject", "subject-vectors/field")
	r.step("a record before any subject", "ok", Header{RecordType: "observation", SubjectRef: "s-1"})
	root := r.step("the root of exchange-1", "ok", rootHeader("exchange", "exchange-1", MembershipExchangeID)).RecordID
	r.step("a second root for exchange-1", "second_root", rootHeader("exchange", "exchange-1", MembershipExchangeID))
	r.step("a log root after seq 1", "log_root_not_first", rootHeader("deal", "deal-1", MembershipLog))
	for _, bad := range []string{"", "field:", "field:x", "field:/a~2", "field:/a~", "logs"} {
		r.step("a root with membership "+strconvQuote(bad), "malformed_root", rootHeader("exchange", "x", bad))
	}
	r.step("a root missing its subject_id", "malformed_root", rootHeader("exchange", "", MembershipExchangeID))
	r.step("a root with an extra statement member", "malformed_root", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"extra":1,"membership":"log","subject_id":"y","subject_kind":"exchange"}`)})
	r.step("a root with no statement", "malformed_root", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim})
	r.step("a root whose member names differ in case only, as Go's decoder reads them", "ok", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"Membership":"field:/subject_ref","SUBJECT_ID":"session-9","subject_kind":"session"}`)})
	r.step("a record citing the root with another exchange id", "cites_root_without_id", citing("exchange", "exchange-2", root))
	r.step("a record citing the root with no correlation", "cites_root_without_id", citing("exchange", "", root))
	m1 := r.step("member M1", "ok", citing("exchange", "exchange-1", root)).RecordID
	r.step("a record carrying the id without citing the root: not a member", "ok", citing("exchange", "exchange-1"))
	r.step("an index_root citing the root: bookkeeping, never a member", "ok", Header{RecordType: RecordTypeIndexRoot, EpistemicType: DerivedMetric, Links: []Link{{Type: Cites, Target: root}}, Statement: json.RawMessage(`{"as_of_seq":3,"index":"record_id","root":"00","size":0}`)})
	r.step("a disclosure citing the root: bookkeeping", "ok", Header{RecordType: RecordTypeDisclosure, EpistemicType: ProducerClaim, Links: []Link{{Type: Cites, Target: root}}})
	m2 := r.step("member M2", "ok", citing("exchange", "exchange-1", root)).RecordID
	r.step("a close counting one member", "close_count", closeHeader(root, 1, m1))
	r.step("a close naming M1 as the last", "close_count", closeHeader(root, 2, m1))
	r.step("a close naming no root it holds", "close_unknown_root", closeHeader(dg("no such root"), 2, m2))
	twice := closeHeader(root, 2, m2)
	twice.Links = append(twice.Links, Link{Type: Closes, Target: root})
	r.step("a close with two closes links", "close_not_one", twice)
	r.step("a close with no closes link", "close_not_one", Header{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"last":"","members":0}`)})
	r.step("a close whose statement has an extra member", "close_statement", Header{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: root}}, Statement: json.RawMessage(`{"extra":true,"last":"` + m2 + `","members":2}`)})
	r.step("a close whose count is negative", "close_statement", Header{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: root}}, Statement: json.RawMessage(`{"last":"` + m2 + `","members":-2}`)})
	r.step("the close: two members ending M2", "ok", closeHeader(root, 2, m2))
	r.step("a second close", "close_again", closeHeader(root, 2, m2))
	r.step("a member after the close", "member_after_close", citing("exchange", "exchange-1", root))
	r.step("a record citing the root with another id after the close", "cites_root_without_id", citing("exchange", "exchange-2", root))
	r.step("a disclosure after the close: bookkeeping still runs", "ok", Header{RecordType: RecordTypeDisclosure, EpistemicType: ProducerClaim, Links: []Link{{Type: Cites, Target: root}, {Type: Cites, Target: m2}}})
	r.step("another root after the close", "ok", rootHeader("exchange", "exchange-3", MembershipExchangeID))
	v.Cases = append(v.Cases, r.done())

	// Go's decoder reads a statement leniently in some ways and not others.
	r = newAdmissionRun(t, "statement decoding", "subject-vectors/decoding")
	r.step("a root whose subject_kind is null", "malformed_root", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"membership":"log","subject_id":"d","subject_kind":null}`)})
	r.step("a root whose subject_id is a number", "malformed_root", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"membership":"log","subject_id":7,"subject_kind":"deal"}`)})
	r.step("a root whose statement is an array", "malformed_root", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: json.RawMessage(`["log"]`)})
	r.step("a root whose statement is null", "malformed_root", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: json.RawMessage(`null`)})
	droot := r.step("the log root, its members named in other cases", "ok", Header{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"MEMBERSHIP":"log","Subject_Id":"d","subject_kind":"deal"}`)}).RecordID
	for _, bad := range []string{`{"last":"","members":-1}`, `{"last":"","members":"0"}`, `{"last":0,"members":0}`, `{"last":"","members":0,"more":1}`} {
		r.step("an empty subject's close stating "+bad, "close_statement", Header{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: droot}}, Statement: json.RawMessage(bad)})
	}
	r.step("an empty subject's close stating {} (both members left out): admitted", "ok", Header{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: droot}}, Statement: json.RawMessage(`{}`)})
	v.Cases = append(v.Cases, r.done())
	r = newAdmissionRun(t, "close decoding", "subject-vectors/close-decoding")
	croot := r.step("a log root", "ok", rootHeader("deal", "c", MembershipLog)).RecordID
	r.step("an empty subject's close stating null members: admitted", "ok", Header{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: croot}}, Statement: json.RawMessage(`{"LAST":null,"Members":null}`)})
	v.Cases = append(v.Cases, r.done())

	// Two field subjects: a record is a member of one.
	r = newAdmissionRun(t, "two subjects", "subject-vectors/two")
	a := r.step("root a under exchange_id", "ok", rootHeader("exchange", "a", MembershipExchangeID)).RecordID
	b := r.step("root b under subject_ref", "ok", rootHeader("session", "b", "field:/subject_ref")).RecordID
	both := citing("turn", "a", a, b)
	both.SubjectRef = "b"
	r.step("a record citing both and carrying both ids", "two_subjects", both)
	both = citing("turn", "a", b, a)
	both.SubjectRef = "b"
	r.step("the same, citing them in the other order", "two_subjects", both)
	r.step("a record citing both carrying only a's id", "cites_root_without_id", citing("turn", "a", a, b))
	onlyB := citing("turn", "other-exchange", b)
	onlyB.SubjectRef = "b"
	r.step("a member of b whose exchange id names another exchange: held under b", "ok", onlyB)
	aWithRef := citing("turn", "a", a)
	aWithRef.SubjectRef = "b"
	r.step("a member of a carrying b's id without citing b", "ok", aWithRef)
	r.step("a close of b", "ok", closeHeader(b, 1, r.c.Steps[len(r.c.Steps)-2].RecordID))
	r.step("a member of a after b's close", "ok", citing("turn", "a", a))
	v.Cases = append(v.Cases, r.done())

	// A per-subject book: a log root at seq 1, every later record but the
	// holder's bookkeeping a member.
	r = newAdmissionRun(t, "per-subject book", "subject-vectors/log")
	lroot := r.step("the log root of deal-1", "ok", rootHeader("deal", "deal-1", MembershipLog)).RecordID
	r.step("a second root, another id", "per_subject_book", rootHeader("exchange", "e", MembershipExchangeID))
	r.step("a second root, the same id", "second_root", rootHeader("deal", "deal-1", MembershipLog))
	offer := r.step("a member citing nothing", "ok", Header{RecordType: "offer", SubjectRef: "deal-1"}).RecordID
	r.step("a member carrying another exchange id", "ok", citing("accept", "x-9"))
	third := r.step("a member citing the root", "ok", citing("accept", "x-9", lroot)).RecordID
	r.step("a lifecycle record: bookkeeping", "ok", Header{RecordType: RecordTypeLifecycle, Links: []Link{{Type: Cites, Target: offer}}})
	r.step("a disclosure_policy record: bookkeeping", "ok", Header{RecordType: RecordTypePolicy, EpistemicType: ProducerClaim})
	r.step("an evidence_request_answered record: bookkeeping", "ok", Header{RecordType: RecordTypeRequestAnswered, EpistemicType: ProducerClaim})
	r.step("a window_open record: bookkeeping, not indexed", "ok", Header{RecordType: RecordTypeWindowOpen, EpistemicType: ProducerClaim})
	r.step("a close counting two members", "close_count", closeHeader(lroot, 2, offer))
	r.step("the close: three members", "ok", closeHeader(lroot, 3, third))
	r.step("a member after the close", "member_after_close", Header{RecordType: "late"})
	r.step("a window_close after the close: bookkeeping", "ok", Header{RecordType: RecordTypeWindowClose, EpistemicType: ProducerClaim})
	r.step("a book_close after the close: bookkeeping, not indexed", "ok", Header{RecordType: RecordTypeBookClose, EpistemicType: ProducerClaim})
	v.Cases = append(v.Cases, r.done())

	// Membership pointers: escaped tokens into the statement, an array
	// index into the links, and what a token that is no index reads.
	r = newAdmissionRun(t, "pointers", "subject-vectors/pointers")
	sroot := r.step("a root whose id sits at /statement/a~1b/~0c", "ok", rootHeader("custom", "id-1", "field:/statement/a~1b/~0c")).RecordID
	r.step("a member carrying it there", "ok", Header{RecordType: "custom", Links: []Link{{Type: Cites, Target: sroot}}, Statement: json.RawMessage(`{"a/b":{"~c":"id-1"}}`)})
	r.step("a record carrying it unescaped", "cites_root_without_id", Header{RecordType: "custom", Links: []Link{{Type: Cites, Target: sroot}}, Statement: json.RawMessage(`{"a~1b":{"~0c":"id-1"}}`)})
	r.step("a record carrying a number there", "cites_root_without_id", Header{RecordType: "custom", Links: []Link{{Type: Cites, Target: sroot}}, Statement: json.RawMessage(`{"a/b":{"~c":1}}`)})
	target := dg("pointer target")
	lroot2 := r.step("a root whose id is the second link's target", "ok", rootHeader("linked", target, "field:/links/1/target")).RecordID
	r.step("a member whose second link targets the id", "ok", Header{RecordType: "linked", Links: []Link{{Type: Cites, Target: lroot2}, {Type: Supersedes, Target: target}}})
	r.step("a record with one link: no second link", "cites_root_without_id", Header{RecordType: "linked", Links: []Link{{Type: Cites, Target: lroot2}}})
	zid := dg("pointer zero")
	zero := r.step("a root reading /links/01/target", "ok", rootHeader("linked", zid, "field:/links/01/target")).RecordID
	r.step("a record whose second link targets the id: 01 is no array index", "cites_root_without_id", Header{RecordType: "linked", Links: []Link{{Type: Cites, Target: zero}, {Type: Supersedes, Target: zid}}})
	empty := r.step("a root reading the member named \"\" of the statement", "ok", rootHeader("custom", "e-1", "field:/statement/")).RecordID
	r.step("a member carrying it", "ok", Header{RecordType: "custom", Links: []Link{{Type: Cites, Target: empty}}, Statement: json.RawMessage(`{"":"e-1"}`)})
	v.Cases = append(v.Cases, r.done())
	return v
}

// sortStrings sorts in place (kept local so the vector file's order does not
// depend on map iteration).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func strconvQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func TestSubjectAdmissionVectors(t *testing.T) {
	v := buildAdmissionVectors(t)
	data, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	path := filepath.Join("testdata", "subject", "admission.json")
	if os.Getenv("EB_WRITE_SUBJECT_VECTORS") == "1" {
		require.NoError(t, os.WriteFile(path, data, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	if !bytes.Equal(data, want) {
		t.Fatalf("%s differs; regenerate only for an intended change (EB_WRITE_SUBJECT_VECTORS=1)", path)
	}
	// Every class is exercised, and every case's book is rebuilt from the
	// file's headers alone to the same outcomes.
	seen := map[string]bool{}
	for _, c := range v.Cases {
		for _, s := range c.Steps {
			seen[s.Expect] = true
		}
	}
	for class := range subjectRefusals {
		require.True(t, seen[class], "class %s has no step", class)
	}
	var read admissionVectors
	require.NoError(t, json.Unmarshal(want, &read))
	for _, c := range read.Cases {
		book, index := &Book{id: c.BookID}, NewSubjectIDIndex()
		for _, s := range c.Steps {
			var h Header
			require.NoError(t, json.Unmarshal(s.Header, &h))
			err := book.checkSubjectEntryLocked(h)
			if s.Expect != "ok" {
				require.ErrorIs(t, err, ErrInvalid, "%s: %s", c.Name, s.Note)
				require.True(t, strings.Contains(err.Error(), subjectRefusals[s.Expect]), "%s: %s: %v", c.Name, s.Note, err)
			} else {
				require.NoError(t, err, "%s: %s", c.Name, s.Note)
				record := Record{RecordID: s.RecordID, Seq: h.Seq, Header: h}
				book.log = append(book.log, record)
				book.trackSubjectLocked(record)
				if h.RecordType != RecordTypeIndexRoot && !isWindowRecord(h.RecordType) && h.RecordType != RecordTypeBookClose {
					if key, ok := index.KeyOf(record); ok {
						require.NoError(t, index.Add(key, record.RecordID))
					}
				}
			}
			require.Equal(t, s.SubjectIDIndex, index.Root(), "%s: %s", c.Name, s.Note)
		}
	}
}
