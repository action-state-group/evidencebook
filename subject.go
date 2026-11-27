// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// The subject convention v0 (evidencebook#116, design note section 4;
// ruling D-28: "A new record kind roots the subject; the profile's shared
// exchange identifier is canonical; sessions and conversations are
// members").
//
// A subject_root record declares a subject: its kind, its shared id and how
// its members are found. Members are the records the root's membership
// names. A subject_close record, when the subject kind has one, ends it:
// it counts the members and names the last, and the book refuses a member
// sealed after it. A subject may stay open for good (a deal, whose
// agreement is its root; a Mesh exchange in v0).
const (
	RecordTypeSubjectRoot  = "subject_root"
	RecordTypeSubjectClose = "subject_close"
)

// Membership forms of a subject_root.
const (
	// MembershipLog makes every record of the book after the root a member
	// (a per-subject book), the holder's own bookkeeping aside
	// (subjectBookkeeping). The root is then the book's first record.
	MembershipLog = "log"
	// MembershipFieldPrefix starts a field membership, "field:<pointer>":
	// members are the records that cite the root and carry the subject id
	// at the RFC 6901 pointer into their header.
	MembershipFieldPrefix = "field:"
	// MembershipExchangeID is the library's field membership: the
	// correlation.exchange_id of the record header.
	MembershipExchangeID = MembershipFieldPrefix + "/correlation/exchange_id"
)

// SubjectRootStatement is the statement of a subject_root record.
type SubjectRootStatement struct {
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Membership  string `json:"membership"`
}

// SubjectCloseStatement is the statement of a subject_close record: how
// many members the subject has (the root and the close are not members)
// and the record id of the last, "" when it has none.
type SubjectCloseStatement struct {
	Members uint64 `json:"members"`
	Last    string `json:"last"`
}

// subjectControl reports the record types that are never a subject's
// members: a subject_root, a subject_close and the holder's bookkeeping.
// This one rule decides membership for admission, the close's count, the
// subject_id index and the verifier.
func subjectControl(recordType string) bool {
	return recordType == RecordTypeSubjectRoot || recordType == RecordTypeSubjectClose || subjectBookkeeping(recordType)
}

// subjectBookkeeping are the record types of the holder's own bookkeeping,
// which are never a subject's members: an index root, a disclosure, an
// answered request, the disclosure policy, a lifecycle record, and a
// window's or a book's own open and close. A book writes them on its own (a checkpoint, a bundle), so a
// closed per-subject book can still be checkpointed and disclosed.
func subjectBookkeeping(recordType string) bool {
	switch recordType {
	case RecordTypeIndexRoot, RecordTypeDisclosure, RecordTypeRequestAnswered, RecordTypePolicy, RecordTypeLifecycle, RecordTypeBookClose:
		return true
	}
	return isWindowRecord(recordType)
}

// pointer returns the membership's JSON pointer, split into its reference
// tokens, or false for log membership or a malformed one.
func (s SubjectRootStatement) pointer() ([]string, bool) {
	raw, ok := strings.CutPrefix(s.Membership, MembershipFieldPrefix)
	if !ok || !strings.HasPrefix(raw, "/") {
		return nil, false
	}
	tokens := strings.Split(raw[1:], "/")
	for i, token := range tokens {
		// RFC 6901 §3: "~" is escaped as "~0" and "/" as "~1"; any other
		// "~" is malformed. An empty token names the member "".
		for j := 0; j < len(token); j++ {
			if token[j] == '~' && (j+1 == len(token) || (token[j+1] != '0' && token[j+1] != '1')) {
				return nil, false
			}
		}
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return tokens, true
}

func (s SubjectRootStatement) validate() error {
	if s.SubjectKind == "" || s.SubjectID == "" {
		return fmt.Errorf("%w: a subject_root names its subject_kind and subject_id", ErrInvalid)
	}
	if s.Membership == MembershipLog {
		return nil
	}
	if _, ok := s.pointer(); !ok {
		return fmt.Errorf("%w: subject_root membership %q is neither %q nor %q followed by a JSON pointer", ErrInvalid, s.Membership, MembershipLog, MembershipFieldPrefix)
	}
	return nil
}

// readSubjectRoot decodes a subject_root statement strictly.
func readSubjectRoot(raw json.RawMessage) (SubjectRootStatement, error) {
	var s SubjectRootStatement
	if err := decodeStrict(raw, &s); err != nil {
		return SubjectRootStatement{}, fmt.Errorf("%w: subject_root statement: %v", ErrInvalid, err)
	}
	return s, s.validate()
}

// refuseLegacySubjectRoot refuses a stored subject_root that an earlier
// release admitted under encoding/json v1's case-folded member names (a
// statement such as {"SUBJECT_ID":"a",...}) and that no longer reads as
// one. Declaring no subject for it would silently drop its subject on
// reopen, and with it the close that bounds its members, so the book does
// not open (evidencebook#1072). A record that never read as a subject_root
// (a caller's own record_type of that name) still opens as before.
func refuseLegacySubjectRoot(record Record) error {
	if record.Header.RecordType != RecordTypeSubjectRoot {
		return nil
	}
	if _, err := readSubjectRoot(record.Header.Statement); err == nil {
		return nil
	}
	var legacy SubjectRootStatement
	dec := json.NewDecoder(bytes.NewReader(record.Header.Statement))
	dec.DisallowUnknownFields()
	if dec.Decode(&legacy) != nil || legacy.validate() != nil {
		return nil
	}
	return fmt.Errorf("%w: subject_root %s at seq %d names a member in another case; it was admitted as subject %q, which this release reads only from exact member names (evidencebook#1072)", ErrCorrupt, record.RecordID, record.Seq, legacy.SubjectID)
}

// headerValueAt reads the string at a pointer into a header's JSON form;
// false when a step is missing or the value is not a string.
func headerValueAt(h Header, tokens []string) (string, bool) {
	raw, err := json.Marshal(h)
	if err != nil {
		return "", false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	for _, token := range tokens {
		switch v := value.(type) {
		case map[string]any:
			var present bool
			if value, present = v[token]; !present {
				return "", false
			}
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(v) || strconv.Itoa(i) != token {
				return "", false
			}
			value = v[i]
		default:
			return "", false
		}
	}
	text, ok := value.(string)
	return text, ok
}

// subjectOf is one subject a book holds: its root and, once it has one,
// its close.
type subjectOf struct {
	root      Record
	statement SubjectRootStatement
	close     *Record
}

// isMember reports whether h, a record after the root, is a member of s.
func (s subjectOf) isMember(h Header) bool {
	if subjectControl(h.RecordType) {
		return false
	}
	if s.statement.Membership == MembershipLog {
		return true
	}
	if !slices.Contains(h.LinksTo(Cites), s.root.RecordID) {
		return false
	}
	tokens, _ := s.statement.pointer()
	id, ok := headerValueAt(h, tokens)
	return ok && id == s.statement.SubjectID
}

// SubjectsOfRecord returns the subject ids of which the record id of a
// verified bundle is a member, read from the bundle's verified headers
// alone (PeerRecord.HeaderVerified): the readers of a claim's subject (the
// subject convention v0, evidencebook#116 design note section 4, Readers).
// The root is a record of the member's own book with a lower seq (seq 1
// for a log root), as the book admits them, and the member is one by
// isMember: a field member cites the root and carries its id; a log member
// need not cite it. The result is sorted and has no duplicates; it is
// empty for a record that is no verified member, such as one whose root
// the bundle does not carry. An x-deal-v1 deal declares no subject_root
// (its agreement is its root, #116 Q1), so a deal-book record returns
// empty here; its deal is its deal_id. A book admits a record to one
// subject only, so two ids mean the book that sealed it broke that rule;
// the caller decides.
func SubjectsOfRecord(records []PeerRecord, id string) []string {
	var member *Header
	for _, r := range records {
		if r.RecordID == id && r.HeaderVerified && r.Header != nil {
			member = r.Header
		}
	}
	if member == nil {
		return nil
	}
	var out []string
	for _, r := range records {
		if !r.HeaderVerified || r.Header == nil || r.Header.RecordType != RecordTypeSubjectRoot || r.RecordID == id {
			continue
		}
		statement, err := readSubjectRoot(r.Header.Statement)
		if err != nil {
			continue
		}
		s := subjectOf{root: Record{RecordID: r.RecordID, Seq: r.Header.Seq, Header: *r.Header}, statement: statement}
		// A book admits members to its own subjects only, after their root,
		// and a log root only as its first record.
		if member.BookID != r.Header.BookID || member.Seq <= r.Header.Seq || (statement.Membership == MembershipLog && r.Header.Seq != 1) {
			continue
		}
		if s.isMember(*member) && !slices.Contains(out, statement.SubjectID) {
			out = append(out, statement.SubjectID)
		}
	}
	slices.Sort(out)
	return out
}

// trackSubjectLocked keeps b.subjects, the subjects the book holds in the
// order of their roots, as record joins the log. A subject_root whose
// statement does not read as one (a caller's own record_type of that name
// sealed before the convention) declares no subject, so an older book
// opens as before; the book refuses a new one (checkSubjectEntryLocked).
func (b *Book) trackSubjectLocked(record Record) {
	switch record.Header.RecordType {
	case RecordTypeSubjectRoot:
		if statement, err := readSubjectRoot(record.Header.Statement); err == nil {
			b.subjects = append(b.subjects, subjectOf{root: record, statement: statement})
		}
	case RecordTypeSubjectClose:
		for i := range b.subjects {
			if b.subjects[i].close == nil && slices.Contains(record.Header.LinksTo(Closes), b.subjects[i].root.RecordID) {
				closed := record
				b.subjects[i].close = &closed
			}
		}
	}
}

// subjectByRootLocked returns the subject whose root is id.
func (b *Book) subjectByRootLocked(id string) (subjectOf, bool) {
	for _, s := range b.subjects {
		if s.root.RecordID == id {
			return s, true
		}
	}
	return subjectOf{}, false
}

// membersLocked returns the records of s's members, in log order.
func (b *Book) membersLocked(s subjectOf) []Record {
	var out []Record
	for _, r := range b.log[s.root.Seq:] {
		if s.isMember(r.Header) {
			out = append(out, r)
		}
	}
	return out
}

// closeStatementLocked is the statement a subject_close of s would carry now.
func (b *Book) closeStatementLocked(s subjectOf) SubjectCloseStatement {
	members := b.membersLocked(s)
	statement := SubjectCloseStatement{Members: uint64(len(members))}
	if len(members) > 0 {
		statement.Last = members[len(members)-1].RecordID
	}
	return statement
}

// checkSubjectEntryLocked refuses an entry that breaks the subject
// convention: a malformed or second subject_root, a log root that is not
// the book's first record, a subject_close that is not the one the book
// computes, a record citing a field root without carrying its id, and a
// member sealed after its subject's close. It reads the header as the book
// is about to commit it.
func (b *Book) checkSubjectEntryLocked(h Header) error {
	if b.forgeSubjects {
		return nil
	}
	subjects := b.subjects
	switch h.RecordType {
	case RecordTypeSubjectRoot:
		statement, err := readSubjectRoot(h.Statement)
		if err != nil {
			return err
		}
		for _, s := range subjects {
			if s.statement.SubjectID == statement.SubjectID {
				return fmt.Errorf("%w: the book already holds subject_root %s for subject %q", ErrInvalid, s.root.RecordID, statement.SubjectID)
			}
			if s.statement.Membership == MembershipLog {
				return fmt.Errorf("%w: book %s is the per-subject book of %q (its first record is a log subject_root); it roots no other subject", ErrInvalid, b.id, s.statement.SubjectID)
			}
		}
		if statement.Membership == MembershipLog && h.Seq != 1 {
			return fmt.Errorf("%w: a log subject_root is the book's first record, not seq %d", ErrInvalid, h.Seq)
		}
		return nil
	case RecordTypeSubjectClose:
		targets := h.LinksTo(Closes)
		if len(targets) != 1 {
			return fmt.Errorf("%w: a subject_close closes exactly one subject_root", ErrInvalid)
		}
		var s *subjectOf
		for i := range subjects {
			if subjects[i].root.RecordID == targets[0] {
				s = &subjects[i]
			}
		}
		if s == nil {
			return fmt.Errorf("%w: subject_close names %s, which is not a subject_root of this book", ErrInvalid, targets[0])
		}
		if s.close != nil {
			return fmt.Errorf("%w: subject %q is already closed by %s", ErrInvalid, s.statement.SubjectID, s.close.RecordID)
		}
		var stated SubjectCloseStatement
		if err := decodeStrict(h.Statement, &stated); err != nil {
			return fmt.Errorf("%w: subject_close statement: %v", ErrInvalid, err)
		}
		if want := b.closeStatementLocked(*s); stated != want {
			return fmt.Errorf("%w: subject_close states %d members ending %q; the book holds %d ending %q", ErrInvalid, stated.Members, stated.Last, want.Members, want.Last)
		}
		return nil
	}
	if subjectControl(h.RecordType) {
		return nil
	}
	var memberOf []string
	for _, s := range subjects {
		if s.statement.Membership != MembershipLog && slices.Contains(h.LinksTo(Cites), s.root.RecordID) {
			tokens, _ := s.statement.pointer()
			if id, ok := headerValueAt(h, tokens); !ok || id != s.statement.SubjectID {
				return fmt.Errorf("%w: a record citing subject_root %s carries the subject id %q at %s", ErrInvalid, s.root.RecordID, s.statement.SubjectID, strings.TrimPrefix(s.statement.Membership, MembershipFieldPrefix))
			}
		}
		if !s.isMember(h) {
			continue
		}
		if s.close != nil {
			return fmt.Errorf("%w: subject %q was closed by %s; a member sealed after its close is refused", ErrInvalid, s.statement.SubjectID, s.close.RecordID)
		}
		memberOf = append(memberOf, s.statement.SubjectID)
	}
	if len(memberOf) > 1 {
		return fmt.Errorf("%w: a record is a member of one subject, not of %q", ErrInvalid, memberOf)
	}
	return nil
}

// SealSubjectRoot seals a subject_root record declaring a subject, with
// the further header members entry gives (its RecordType, EpistemicType and
// Statement are the book's). It is refused when the book already holds a
// root for the subject id, and a log root anywhere but the book's first
// record.
func (b *Book) SealSubjectRoot(ctx context.Context, statement SubjectRootStatement, entry Entry) (Record, error) {
	if err := statement.validate(); err != nil {
		return Record{}, err
	}
	raw, err := json.Marshal(statement)
	if err != nil {
		return Record{}, err
	}
	entry.RecordType, entry.EpistemicType, entry.Statement = RecordTypeSubjectRoot, ProducerClaim, raw
	if entry.Links == nil {
		entry.Links = []Link{}
	}
	return b.Append(ctx, entry)
}

// CloseSubject seals the subject_close of the subject rooted at root: it
// counts the subject's members and names the last. A member sealed after
// it is refused, and a subject closes once.
func (b *Book) CloseSubject(ctx context.Context, root string) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// A record left pending joins the log first, so the count is the one
	// the close's admission computes.
	if err := b.flushPendingLocked(ctx); err != nil {
		return Record{}, err
	}
	s, ok := b.subjectByRootLocked(root)
	if !ok {
		return Record{}, fmt.Errorf("%w: subject_root %s", ErrNotFound, root)
	}
	raw, err := json.Marshal(b.closeStatementLocked(s))
	if err != nil {
		return Record{}, err
	}
	return b.appendLocked(ctx, Entry{RecordType: RecordTypeSubjectClose, EpistemicType: ProducerClaim, Links: []Link{{Type: Closes, Target: root}}, Statement: raw})
}

// SubjectIDIndexName is the name of the subject_id authenticated index.
const SubjectIDIndexName = "subject_id"

// NewSubjectIDIndex indexes every subject's root, members and close under
// the subject's declared id (design note section 4), so a key range proof
// over one id lists the subject's records: the book's statement of its
// members, checkable against its commitment (AuthenticatedIndex).
//
// It learns the roots from the records it is given, which the book gives
// it in log order (rebuildIndexes, admitLocked); Reset forgets them. Its
// root is committed only once it holds a leaf (lazyIndex), so a book that
// declares no subject commits exactly the index roots it did before the
// index existed.
func NewSubjectIDIndex() *SortedMerkleIndex {
	var mu sync.Mutex
	var subjects []subjectOf
	m := &SortedMerkleIndex{name: SubjectIDIndexName, lazy: true}
	m.keyOf = func(r Record) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Header.RecordType {
		case RecordTypeSubjectRoot:
			statement, err := readSubjectRoot(r.Header.Statement)
			if err != nil {
				return "", false
			}
			subjects = append(subjects, subjectOf{root: r, statement: statement})
			return statement.SubjectID, true
		case RecordTypeSubjectClose:
			for _, s := range subjects {
				if slices.Contains(r.Header.LinksTo(Closes), s.root.RecordID) {
					return s.statement.SubjectID, true
				}
			}
			return "", false
		}
		for _, s := range subjects {
			if s.isMember(r.Header) {
				return s.statement.SubjectID, true
			}
		}
		return "", false
	}
	m.reset = func() {
		mu.Lock()
		defer mu.Unlock()
		subjects = nil
	}
	return m
}
