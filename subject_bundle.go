// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// DerivationSubjectBundle is the subject bundle derivation (evidencebook#116,
// design note section 4): given a subject id, the subject's root, its
// members, its close when it has one, and each member's outward citations.
const DerivationSubjectBundle = "subject_bundle/0"

// Forms of a subject bundle.
const (
	// SubjectBundleFull is for a recipient entitled to every member (the
	// holder itself; an adjudicator under full disclosure): an Evidence
	// Bundle v2 with the subject's membership proof at the level it names.
	SubjectBundleFull = "full"
	// SubjectBundleProjection is for any other recipient: the visible
	// members only, with no proof of any kind, its completeness the
	// holder's assertion.
	SubjectBundleProjection = "projection"
)

// Completeness levels a subject bundle names.
const (
	// SubjectLevelHolderCommitment: the members are what the holder's
	// subject_id index commits, checkable against its commitment but not
	// against a dishonest book (AuthenticatedIndex).
	SubjectLevelHolderCommitment = "holder_commitment"
	// SubjectLevelLogPrefix: a per-subject book's members are its log
	// prefix up to the anchor, which the checkpoint covers whole.
	SubjectLevelLogPrefix = "log_prefix"
	// SubjectLevelHolderAssertion: a projection's members are the holder's
	// word, with no proof.
	SubjectLevelHolderAssertion = "holder_assertion"
)

// SubjectBundleDefinition is the derivation definition a holder registers
// (Book.RegisterDerivation) to grant recipients a subject bundle of one
// form. Its document names the form, the recipients and the fields, so
// each grant is its own definition. A full form discloses whole headers:
// its fields are every header member, and nil fields mean that. A
// projection declares the header fields it reveals of each record, never
// seq, since a member's position would show a protected record before it.
func SubjectBundleDefinition(form string, recipients, fields []string) (DerivationDefinition, error) {
	if form == SubjectBundleFull && fields == nil {
		fields = slices.Clone(headerFields)
	}
	recipients, fields = slices.Clone(recipients), slices.Clone(fields)
	slices.Sort(recipients)
	slices.Sort(fields)
	def := DerivationDefinition{Recipients: recipients, Forms: []SubjectKind{SubjectCorrelation}, Fields: fields}
	if err := checkSubjectDefinition(form, def); err != nil {
		return DerivationDefinition{}, err
	}
	doc, err := canonicalJSON(map[string]any{"derivation": DerivationSubjectBundle, "form": form, "recipients": recipients, "fields": fields})
	if err != nil {
		return DerivationDefinition{}, err
	}
	def.Document = doc
	return def, nil
}

// checkSubjectDefinition refuses a subject bundle definition the form
// cannot honour.
func checkSubjectDefinition(form string, def DerivationDefinition) error {
	switch form {
	case SubjectBundleFull:
		if !sameSet(def.Fields, headerFields) {
			return fmt.Errorf("%w: a full subject bundle discloses whole headers, so it declares every header field", ErrInvalid)
		}
	case SubjectBundleProjection:
		if slices.ContainsFunc(def.Fields, func(f string) bool { return f == "seq" || strings.HasPrefix(f, "seq.") }) {
			return fmt.Errorf("%w: a subject bundle projection carries no position, so it cannot declare seq", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: subject bundle form %q is neither %s nor %s", ErrInvalid, form, SubjectBundleFull, SubjectBundleProjection)
	}
	if !slices.Equal(def.Forms, []SubjectKind{SubjectCorrelation}) {
		return fmt.Errorf("%w: a subject bundle is defined over the correlation subject form", ErrInvalid)
	}
	return nil
}

// subjectFormOf returns the subject bundle form a definition document
// declares, or "" when it is not a subject bundle definition.
func subjectFormOf(document map[string]any) string {
	if document["derivation"] != DerivationSubjectBundle {
		return ""
	}
	form, _ := document["form"].(string)
	if form == "" {
		form = "?"
	}
	return form
}

// SubjectBundleRequest asks for a subject bundle.
type SubjectBundleRequest struct {
	// SubjectID is the subject's declared id.
	SubjectID string
	// Derivation is the id RegisterDerivation returned for a
	// SubjectBundleDefinition; its form is the bundle's form.
	Derivation string
	// Requester is the recipient, which must be one the definition grants.
	Requester string
	// OwnerCopy is the holder's own copy: the full form, with no
	// definition, conduct records included.
	OwnerCopy bool
	// ClosureDepth is how many link hops each member's outward closure
	// follows; zero is DefaultClosureDepth.
	ClosureDepth int
	// WithholdPayloads, Withhold and DiscloseHeld are BundleRequest's, for
	// the full form.
	WithholdPayloads bool
	Withhold         []string
	DiscloseHeld     bool
	// DiscloseLog discloses every record of the log up to the anchor in a
	// full form, so a field subject's members are checked against the log
	// prefix (SubjectLevelLogPrefix), not only the holder's index; a log
	// subject's full form always does.
	DiscloseLog bool
	// ConductPacks are the holder's conduct pack ids (BundleRequest): a
	// member citing one is protected, invisible to a projection and refused
	// in a full form for anyone but the owner.
	ConductPacks []string
}

// SubjectBundle is a subject bundle and the disclosure record the book
// sealed about it.
type SubjectBundle struct {
	Form       string
	JSON       []byte
	Digest     string
	Disclosure Record
}

// SubjectExtension is the evidencebook/subject extension of a full subject
// bundle: the subject's membership, which the verifier checks against the
// bundle's records (VerifySubjectBundle).
type SubjectExtension struct {
	Derivation string               `json:"derivation"`
	Form       string               `json:"form"`
	Subject    SubjectRootStatement `json:"subject"`
	Root       string               `json:"root"`
	Members    []string             `json:"members"`
	Close      string               `json:"close,omitempty"`
	Closed     bool                 `json:"closed"`
	Level      string               `json:"level"`
	// KeyRange is the subject_id index's proof of the subject's records,
	// for a field membership.
	KeyRange *KeyRangeAnswer `json:"key_range,omitempty"`
	// HeldByDigest are the members' outward citations the book does not
	// hold as records: foreign evidence, found in the held store by digest
	// (Shelf.Held, Shelf.HeldBy).
	HeldByDigest []string `json:"held_by_digest"`
}

// SubjectProjection is a subject bundle's projection form.
type SubjectProjection struct {
	Derivation   string           `json:"derivation"`
	Form         string           `json:"form"`
	Definition   string           `json:"definition"`
	SubjectKind  string           `json:"subject_kind"`
	SubjectID    string           `json:"subject_id"`
	Completeness string           `json:"completeness"`
	Root         map[string]any   `json:"root"`
	Members      []map[string]any `json:"members"`
}

// SubjectBundle builds the subject bundle of a subject the book holds, in
// the form the request's definition grants. Membership, the proof, the
// bundle and its disclosure record are built under one lock, so no member
// or close joins between them.
func (b *Book) SubjectBundle(ctx context.Context, req SubjectBundleRequest) (SubjectBundle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	form := SubjectBundleFull
	var def derivationEntry
	if !req.OwnerCopy {
		entry, ok := b.derivations[req.Derivation]
		if !ok || entry.subjectForm == "" {
			return SubjectBundle{}, fmt.Errorf("%w: %q is not a registered subject bundle definition", ErrNotFound, req.Derivation)
		}
		if req.Requester == "" || !slices.Contains(entry.grants, req.Requester) {
			return SubjectBundle{}, fmt.Errorf("%w: the subject bundle definition does not grant %q", ErrRefused, req.Requester)
		}
		form, def = entry.subjectForm, entry
	}
	if err := checkConductPacks(req.ConductPacks); err != nil {
		return SubjectBundle{}, err
	}
	if err := b.flushPendingLocked(ctx); err != nil {
		return SubjectBundle{}, err
	}
	var subject *subjectOf
	for i := range b.subjects {
		if b.subjects[i].statement.SubjectID == req.SubjectID {
			subject = &b.subjects[i]
		}
	}
	if subject == nil {
		return SubjectBundle{}, fmt.Errorf("%w: subject %q", ErrNotFound, req.SubjectID)
	}
	if form == SubjectBundleProjection {
		return b.subjectProjectionLocked(ctx, *subject, def, req)
	}
	return b.subjectFullLocked(ctx, *subject, b.membersLocked(*subject), req)
}

// subjectFullLocked builds the full form over members, the subject's
// members as the book reads them from its log (membersLocked); the
// subject_id index must commit exactly the root, those members and the
// close.
func (b *Book) subjectFullLocked(ctx context.Context, s subjectOf, members []Record, req SubjectBundleRequest) (SubjectBundle, error) {
	ext := SubjectExtension{Derivation: DerivationSubjectBundle, Form: SubjectBundleFull, Subject: s.statement, Root: s.root.RecordID, Members: []string{}, Closed: s.close != nil, HeldByDigest: []string{}}
	records := []string{s.root.RecordID}
	for _, m := range members {
		ext.Members = append(ext.Members, m.RecordID)
		records = append(records, m.RecordID)
	}
	if s.close != nil {
		ext.Close = s.close.RecordID
		records = append(records, s.close.RecordID)
	}
	include := slices.Clone(records)
	var anchor Checkpoint
	if s.statement.Membership != MembershipLog {
		answer, err := b.proveKeyRangeLocked(ctx, SubjectIDIndexName, s.statement.SubjectID, s.statement.SubjectID)
		if err != nil {
			return SubjectBundle{}, err
		}
		var proved []string
		for _, match := range answer.Proof.Matches {
			proved = append(proved, match.RecordID)
		}
		if len(proved) != len(records) || !sameSet(proved, records) {
			return SubjectBundle{}, fmt.Errorf("%w: the subject_id index holds %d records for %q; the log holds %d", ErrCorrupt, len(proved), s.statement.SubjectID, len(records))
		}
		anchor, ext.KeyRange, ext.Level = answer.Anchor, &answer, SubjectLevelHolderCommitment
		include = append(include, answer.RootRecordID)
	} else {
		var err error
		if anchor, err = b.checkpointLocked(ctx); err != nil {
			return SubjectBundle{}, err
		}
	}
	if s.statement.Membership == MembershipLog || req.DiscloseLog {
		// Every record of the prefix is disclosed, so a verifier can tell
		// each position's kind and find every member: this form is for a
		// recipient entitled to the whole log up to the anchor.
		ext.Level = SubjectLevelLogPrefix
		for _, r := range b.log[:anchor.Entries] {
			include = append(include, r.RecordID)
		}
	}
	depth := req.ClosureDepth
	if depth == 0 {
		depth = DefaultClosureDepth
	}
	held := map[string]struct{}{}
	for _, m := range members {
		selected, missing, err := b.closureLocked(m.RecordID, depth)
		if err != nil {
			return SubjectBundle{}, err
		}
		for id := range selected {
			include = append(include, id)
		}
		for _, id := range missing {
			held[id] = struct{}{}
		}
	}
	for id := range held {
		ext.HeldByDigest = append(ext.HeldByDigest, id)
	}
	slices.Sort(ext.HeldByDigest)
	slices.Sort(include)
	include = slices.Compact(include)
	raw, err := json.Marshal(ext)
	if err != nil {
		return SubjectBundle{}, err
	}
	bundle, err := b.bundleLocked(ctx, BundleRequest{
		Root: s.root.RecordID, Include: include, ClosureDepth: depth, At: &anchor,
		WithholdPayloads: req.WithholdPayloads, Withhold: req.Withhold, DiscloseHeld: req.DiscloseHeld,
		ConductPacks: req.ConductPacks, OwnerCopy: req.OwnerCopy,
		Extensions: map[string]json.RawMessage{ExtensionSubject: raw},
	})
	if err != nil {
		return SubjectBundle{}, err
	}
	return SubjectBundle{Form: SubjectBundleFull, JSON: bundle.JSON, Digest: bundle.Digest, Disclosure: bundle.Disclosure}, nil
}

// subjectProjectionLocked builds the projection: the root and each visible
// member, each as the definition's declared header fields, in log order.
// It carries no capsule, record id, position, checkpoint, proof or close,
// so its bytes are the same whether or not a protected member exists.
func (b *Book) subjectProjectionLocked(ctx context.Context, s subjectOf, def derivationEntry, req SubjectBundleRequest) (SubjectBundle, error) {
	project := func(r Record) (map[string]any, error) {
		raw, err := canonicalJSON(r.Header)
		if err != nil {
			return nil, err
		}
		var header map[string]any
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, err
		}
		entry := map[string]any{}
		for _, field := range def.def.Fields {
			names, _ := fieldPath(field)
			if value, present := lookupPath(header, names); present {
				setPath(entry, names, value)
			}
		}
		return entry, nil
	}
	out := SubjectProjection{
		Derivation: DerivationSubjectBundle, Form: SubjectBundleProjection, Definition: def.id,
		SubjectKind: s.statement.SubjectKind, SubjectID: s.statement.SubjectID,
		Completeness: SubjectLevelHolderAssertion, Members: []map[string]any{},
	}
	var err error
	if out.Root, err = project(s.root); err != nil {
		return SubjectBundle{}, err
	}
	links := []Link{{Type: Cites, Target: s.root.RecordID}}
	for _, m := range b.membersLocked(s) {
		pack, err := conductPackCited(m.RecordID, m.Header, req.ConductPacks)
		if err != nil {
			return SubjectBundle{}, err
		}
		if pack != "" {
			continue
		}
		projected, err := project(m)
		if err != nil {
			return SubjectBundle{}, err
		}
		out.Members = append(out.Members, projected)
		links = append(links, Link{Type: Cites, Target: m.RecordID})
	}
	data, err := canonicalJSON(out)
	if err != nil {
		return SubjectBundle{}, err
	}
	digest := digestBytes(data)
	suppressed := []string{}
	for _, field := range headerFields {
		if !slices.Contains(def.def.Fields, field) {
			suppressed = append(suppressed, field)
		}
	}
	body, err := json.Marshal(DisclosureStatement{BundleDigest: digest, Payloads: PayloadsSelected, SuppressedFields: suppressed, Withheld: []WithheldItem{}})
	if err != nil {
		return SubjectBundle{}, err
	}
	record, err := b.appendLocked(ctx, Entry{RecordType: RecordTypeDisclosure, EpistemicType: ProducerClaim, Links: links, Statement: body})
	if err != nil {
		return SubjectBundle{}, err
	}
	return SubjectBundle{Form: SubjectBundleProjection, JSON: data, Digest: digest, Disclosure: record}, nil
}

// VerifiedSubject is what a relying party learns from a subject bundle.
type VerifiedSubject struct {
	Form    string
	Level   string
	Subject SubjectRootStatement
	// Root, Members and Close are record ids, in log order; a projection
	// carries none, only MemberCount.
	Root        string
	Members     []string
	MemberCount int
	Close       string
	Closed      bool
	// Withheld are the payloads of the full form's disclosed records (the
	// members and their closure) it does not carry, by digest: payload
	// availability, reported apart from membership.
	Withheld     []WithheldItem
	HeldByDigest []string
	// Bundle is the full form's verified Evidence Bundle.
	Bundle *VerifiedBundle
}

// VerifySubjectBundle checks a subject bundle of either form. A full form
// must verify as an Evidence Bundle with every claim passed under an
// authenticated anchor, and its subject extension must agree with the
// records it carries: the root, each member (cites the root and carries
// the id, or for a log subject is any record after the root but the
// holder's bookkeeping), the close's count and last member, no member
// after the close. At SubjectLevelHolderCommitment the subject_id index's
// key range proof must list exactly the root, the members and the close
// under an index root the bundle's index_root record commits. At
// SubjectLevelLogPrefix the bundle must disclose every record from seq 1
// to its anchor, and the members must be exactly the records of the prefix
// the membership names. A projection is checked for its shape only: its
// completeness is the holder's assertion.
func VerifySubjectBundle(data []byte) (VerifiedSubject, error) {
	var probe struct {
		Form       string `json:"form"`
		Derivation string `json:"derivation"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.Derivation == DerivationSubjectBundle && probe.Form == SubjectBundleProjection {
		return verifySubjectProjection(data)
	}
	verified, err := VerifyBundle(data)
	if err != nil {
		return VerifiedSubject{}, err
	}
	if err := verified.FullyVerified(); err != nil {
		return VerifiedSubject{}, err
	}
	if !verified.AnchorAuthenticated {
		return VerifiedSubject{}, fmt.Errorf("%w: a subject bundle's anchor is not authenticated", ErrInvalid)
	}
	raw, ok := verified.Extensions[ExtensionSubject]
	if !ok {
		return VerifiedSubject{}, fmt.Errorf("%w: the bundle carries no %s extension", ErrInvalid, ExtensionSubject)
	}
	var ext SubjectExtension
	if err := decodeStrict(raw, &ext); err != nil {
		return VerifiedSubject{}, fmt.Errorf("%w: %s extension: %v", ErrInvalid, ExtensionSubject, err)
	}
	if ext.Derivation != DerivationSubjectBundle || ext.Form != SubjectBundleFull {
		return VerifiedSubject{}, fmt.Errorf("%w: the %s extension is not a full %s", ErrInvalid, ExtensionSubject, DerivationSubjectBundle)
	}
	if err := ext.Subject.validate(); err != nil {
		return VerifiedSubject{}, err
	}
	byID := make(map[string]PeerRecord, len(verified.Records))
	for _, r := range verified.Records {
		byID[r.RecordID] = r
	}
	disclosed := func(id, what string) (PeerRecord, error) {
		r, ok := byID[id]
		if !ok || !r.HeaderVerified || r.Header == nil || !verified.Covers(r.Seq) {
			return PeerRecord{}, fmt.Errorf("%w: the subject's %s %s is not a disclosed, covered record of the bundle", ErrInvalid, what, id)
		}
		return r, nil
	}
	root, err := disclosed(ext.Root, "root")
	if err != nil {
		return VerifiedSubject{}, err
	}
	stated, err := readSubjectRoot(root.Header.Statement)
	if err != nil || root.Header.RecordType != RecordTypeSubjectRoot || stated != ext.Subject {
		return VerifiedSubject{}, fmt.Errorf("%w: record %s is not the subject_root the extension names", ErrInvalid, ext.Root)
	}
	subject := subjectOf{root: Record{RecordID: root.RecordID, Seq: root.Seq, Header: *root.Header}, statement: stated}
	out := VerifiedSubject{Form: SubjectBundleFull, Level: ext.Level, Subject: stated, Root: ext.Root, Members: ext.Members, MemberCount: len(ext.Members), Close: ext.Close, Closed: ext.Closed, HeldByDigest: ext.HeldByDigest, Withheld: []WithheldItem{}, Bundle: &verified}
	last := root.Seq
	for _, id := range ext.Members {
		m, err := disclosed(id, "member")
		if err != nil {
			return VerifiedSubject{}, err
		}
		if m.Seq <= last || !subject.isMember(*m.Header) {
			return VerifiedSubject{}, fmt.Errorf("%w: record %s is not the subject's next member", ErrInvalid, id)
		}
		last = m.Seq
	}
	for _, r := range verified.Records {
		if !r.HeaderVerified || r.Header == nil {
			continue
		}
		for _, digest := range r.Header.PayloadCommitments {
			if _, carried := verified.Payloads[digest]; !carried {
				out.Withheld = append(out.Withheld, WithheldItem{RecordID: r.RecordID, PayloadCommitment: digest, Digest: digest})
			}
		}
	}
	listed := append([]string{ext.Root}, ext.Members...)
	if ext.Closed != (ext.Close != "") {
		return VerifiedSubject{}, fmt.Errorf("%w: the extension's closed and close disagree", ErrInvalid)
	}
	if ext.Close != "" {
		c, err := disclosed(ext.Close, "close")
		if err != nil {
			return VerifiedSubject{}, err
		}
		var count SubjectCloseStatement
		want := SubjectCloseStatement{Members: uint64(len(ext.Members))}
		if len(ext.Members) > 0 {
			want.Last = ext.Members[len(ext.Members)-1]
		}
		if c.Header.RecordType != RecordTypeSubjectClose || !slices.Equal(c.Header.LinksTo(Closes), []string{ext.Root}) || decodeStrict(c.Header.Statement, &count) != nil || count != want || c.Seq <= last {
			return VerifiedSubject{}, fmt.Errorf("%w: record %s is not a close of the subject counting its %d members", ErrInvalid, ext.Close, len(ext.Members))
		}
		listed = append(listed, ext.Close)
	}
	switch ext.Level {
	case SubjectLevelHolderCommitment:
		if err := verifySubjectKeyRange(ext, listed, disclosed); err != nil {
			return VerifiedSubject{}, err
		}
	case SubjectLevelLogPrefix:
		if stated.Membership != MembershipLog {
			if err := verifySubjectKeyRange(ext, listed, disclosed); err != nil {
				return VerifiedSubject{}, err
			}
		}
		if verified.IntervalFirst != 1 || uint64(len(verified.Records)) != verified.IntervalLast {
			return VerifiedSubject{}, fmt.Errorf("%w: a log-prefix subject bundle carries every record from seq 1, not %d records of %d to %d", ErrInvalid, len(verified.Records), verified.IntervalFirst, verified.IntervalLast)
		}
		var members []string
		for _, r := range verified.Records {
			if !r.HeaderVerified || r.Header == nil {
				return VerifiedSubject{}, fmt.Errorf("%w: record %s of the log prefix is not disclosed, so its kind is unknown", ErrInvalid, r.RecordID)
			}
			if r.Seq > root.Seq && subject.isMember(*r.Header) {
				members = append(members, r.RecordID)
			}
			if r.Header.RecordType == RecordTypeSubjectClose && slices.Contains(r.Header.LinksTo(Closes), ext.Root) && r.RecordID != ext.Close {
				return VerifiedSubject{}, fmt.Errorf("%w: the log prefix holds close %s the extension does not name", ErrInvalid, r.RecordID)
			}
		}
		if !slices.Equal(members, ext.Members) {
			return VerifiedSubject{}, fmt.Errorf("%w: the log prefix holds %d members; the extension lists %d", ErrInvalid, len(members), len(ext.Members))
		}
	default:
		return VerifiedSubject{}, fmt.Errorf("%w: subject bundle level %q", ErrInvalid, ext.Level)
	}
	return out, nil
}

// verifySubjectKeyRange checks a full form's key range proof: it proves
// exactly the listed records under the subject id, and its index root is
// the one a disclosed, covered index_root record commits.
func verifySubjectKeyRange(ext SubjectExtension, listed []string, disclosed func(id, what string) (PeerRecord, error)) error {
	kr := ext.KeyRange
	if kr == nil || kr.Root.Index != SubjectIDIndexName {
		return fmt.Errorf("%w: a field subject's bundle carries the %s index's key range proof", ErrInvalid, SubjectIDIndexName)
	}
	ids, err := VerifyKeyRange(kr.Root, ext.Subject.SubjectID, ext.Subject.SubjectID, kr.Proof)
	if err != nil {
		return err
	}
	if len(ids) != len(listed) || !sameSet(ids, listed) {
		return fmt.Errorf("%w: the key range proves %d records for %q; the extension lists %d", ErrInvalid, len(ids), ext.Subject.SubjectID, len(listed))
	}
	r, err := disclosed(kr.RootRecordID, "index root")
	if err != nil {
		return err
	}
	var committed struct {
		IndexRoot
		AsOfSeq uint64 `json:"as_of_seq"`
	}
	if r.Header.RecordType != RecordTypeIndexRoot || json.Unmarshal(r.Header.Statement, &committed) != nil || committed.IndexRoot != kr.Root {
		return fmt.Errorf("%w: record %s does not commit the key range proof's index root", ErrInvalid, kr.RootRecordID)
	}
	return nil
}

// verifySubjectProjection checks a projection's shape: what it states of
// its members is the holder's assertion.
func verifySubjectProjection(data []byte) (VerifiedSubject, error) {
	var p SubjectProjection
	if err := decodeStrict(data, &p); err != nil {
		return VerifiedSubject{}, fmt.Errorf("%w: subject bundle projection: %v", ErrInvalid, err)
	}
	if p.Completeness != SubjectLevelHolderAssertion || p.SubjectID == "" || p.Root == nil || p.Members == nil {
		return VerifiedSubject{}, fmt.Errorf("%w: a subject bundle projection names its subject, its root and its members, as the holder's assertion", ErrInvalid)
	}
	for _, m := range append([]map[string]any{p.Root}, p.Members...) {
		if _, ok := m["seq"]; ok {
			return VerifiedSubject{}, fmt.Errorf("%w: a subject bundle projection carries no position", ErrInvalid)
		}
	}
	return VerifiedSubject{Form: SubjectBundleProjection, Level: SubjectLevelHolderAssertion, Subject: SubjectRootStatement{SubjectKind: p.SubjectKind, SubjectID: p.SubjectID}, MemberCount: len(p.Members)}, nil
}
