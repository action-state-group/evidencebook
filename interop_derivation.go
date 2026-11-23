// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// DerivationDefinition is a derivation this responder serves by the digest
// of its definition (ER §3.3). A recipient-specific disclosure is exactly
// such a derivation (profile §6.5, ruling 5): a new identity, never a
// variant of the canonical Artifact of the underived subject.
type DerivationDefinition struct {
	// Document is the definition document, a JSON object. The derivation's
	// identifier is SHA-256 over UTF-8 of its JCS. The profile does not fix
	// its member names (P-14); this package reads none of them, and the
	// holder states what it declares in the fields below as well.
	Document []byte
	// Recipient is the authenticated requester the projection is prepared
	// for, as the definition names it. Only that requester is answered, over
	// any form the definition declares: it is the holder's grant to that
	// requester. An empty Recipient and no Recipients make the derivation
	// answerable under the share policy like the underived subject, so over
	// range or full_history it is answered to no one.
	Recipient string
	// Recipients are further requesters the holder grants the same
	// derivation to, so one definition digest (a Terms Pack's) is served to
	// several of them. The grant is Recipient and Recipients together, as a
	// set; an empty name is ErrInvalid. Every one is answered the same bytes.
	Recipients []string
	// Forms are the subject forms the derivation is defined over; any other
	// form is answered no_such_subject (ER §3.3).
	Forms []SubjectKind
	// Fields is the declared field set: what the projection reveals of each
	// record, and nothing else (ER §8). A field is a record header member
	// ("statement") or a path into one ("statement.generated_at", names
	// joined by "."; the Plan IR's field-path grammar over objects). A path
	// reveals that member only, nested as the record holds it, so a pack
	// that reads one member of a statement does not reveal the rest. A path
	// whose step is not an object in a record is left out of that record's
	// projection. A path and a field it extends, or one of its own prefixes,
	// are ErrInvalid together: they would say two things about one member.
	Fields []string
	// Proofs makes the answer carry the anchor's signed checkpoint and one
	// inclusion proof per record (cllSetMaterial), which a requester of this
	// package checks: the rows are the holder's log's records at the anchor,
	// whatever values the holder attests for them. It proves membership of
	// the rows given, never that no row was left out. Off, the answer
	// carries no verification material, as every record-set answer does
	// (P-13).
	Proofs bool
}

type derivationEntry struct {
	id  string
	def DerivationDefinition
	// grants is Recipient and Recipients as one cloned, sorted set; empty
	// when the holder named no one.
	grants []string
}

// grantSet returns the definition's recipients as one sorted set, or
// ErrInvalid for an empty name.
func (def DerivationDefinition) grantSet() ([]string, error) {
	var grants []string
	if def.Recipient != "" {
		grants = append(grants, def.Recipient)
	}
	for _, r := range def.Recipients {
		if r == "" {
			return nil, fmt.Errorf("%w: a derivation recipient has a name", ErrInvalid)
		}
		grants = append(grants, r)
	}
	slices.Sort(grants)
	return slices.Compact(grants), nil
}

// fieldPath splits a declared field into its names, or reports false when
// it is not a header member followed by object member names.
func fieldPath(field string) ([]string, bool) {
	names := strings.Split(field, ".")
	if !slices.Contains(headerFields, names[0]) {
		return nil, false
	}
	for _, name := range names[1:] {
		if name == "" || !memberName.MatchString(name) {
			return nil, false
		}
	}
	return names, true
}

// memberName is a name of the Plan IR's field-path grammar.
var memberName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkFieldSet refuses a declared field that is not a field path, and a
// field whose path extends another's. A field declared twice is one field.
func checkFieldSet(fields []string) error {
	for i, field := range fields {
		if _, ok := fieldPath(field); !ok {
			return fmt.Errorf("%w: %q is not a record header member or a path into one", ErrInvalid, field)
		}
		for _, other := range fields[:i] {
			if strings.HasPrefix(field, other+".") || strings.HasPrefix(other, field+".") {
				return fmt.Errorf("%w: %q and %q declare the same member twice", ErrInvalid, other, field)
			}
		}
	}
	return nil
}

// headerFields are the record header members a projection may declare.
var headerFields = []string{"book_id", "committed_at", "correlation", "counterparty_ref", "epistemic_type", "event_time_claim", "links", "payload_commitments", "principal_ref", "record_type", "retention_state", "seq", "statement", "subject_ref", "v"}

// RegisterDerivation makes a derivation answerable by its identifier, which
// it returns. The registration is held in memory: a host registers its
// definitions each time it opens the book, and they are never written into
// the book's history, because a recipient-specific definition names its
// recipient.
//
// The identifier binds the document only, so registering it again must not
// change what it answers: a registration that declares the same grant
// and the same sets of forms and fields returns the same identifier and
// changes nothing, and one that declares anything else is ErrInvalid.
func (b *Book) RegisterDerivation(def DerivationDefinition) (string, error) {
	decoded, err := evidencerequest.DecodeJSON(def.Document)
	if err != nil {
		return "", fmt.Errorf("%w: derivation definition: %v", ErrInvalid, err)
	}
	if _, ok := decoded.(map[string]any); !ok {
		return "", fmt.Errorf("%w: a derivation definition is a JSON object", ErrInvalid)
	}
	canonicalDoc, err := evidencerequest.EncodeJCS(decoded)
	if err != nil {
		return "", fmt.Errorf("%w: derivation definition: %v", ErrInvalid, err)
	}
	if len(def.Forms) == 0 || len(def.Fields) == 0 {
		return "", fmt.Errorf("%w: a derivation declares its subject forms and its field set", ErrInvalid)
	}
	for _, form := range def.Forms {
		if !slices.Contains(SubjectForms, form) || form == SubjectCheckpoints {
			return "", fmt.Errorf("%w: a derivation over records cannot be defined over %q", ErrInvalid, form)
		}
	}
	if err := checkFieldSet(def.Fields); err != nil {
		return "", err
	}
	grants, err := def.grantSet()
	if err != nil {
		return "", err
	}
	id := digestBytes(canonicalDoc)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.derivations == nil {
		b.derivations = make(map[string]derivationEntry)
	}
	if prev, ok := b.derivations[id]; ok {
		if slices.Equal(prev.grants, grants) && prev.def.Proofs == def.Proofs && sameSet(prev.def.Forms, def.Forms) && sameSet(prev.def.Fields, def.Fields) {
			return id, nil
		}
		return "", fmt.Errorf("%w: derivation %s is registered with a different recipient, forms, fields or proofs setting", ErrInvalid, id)
	}
	def.Forms, def.Fields, def.Recipients = slices.Clone(def.Forms), slices.Clone(def.Fields), slices.Clone(def.Recipients)
	b.derivations[id] = derivationEntry{id: id, def: def, grants: grants}
	return id, nil
}

// sameSet reports whether a and b hold the same members, in any order and
// with any repetition.
func sameSet[T comparable](a, b []T) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	for _, x := range b {
		if !slices.Contains(a, x) {
			return false
		}
	}
	return true
}
