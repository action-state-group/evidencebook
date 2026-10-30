// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"fmt"
	"slices"

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
	// requester. An empty Recipient makes the derivation answerable under
	// the share policy like the underived subject, so over range or
	// full_history it is answered to no one.
	Recipient string
	// Forms are the subject forms the derivation is defined over; any other
	// form is answered no_such_subject (ER §3.3).
	Forms []SubjectKind
	// Fields is the declared field set: the record header members the
	// projection reveals, and nothing else (ER §8).
	Fields []string
}

type derivationEntry struct {
	id  string
	def DerivationDefinition
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
// change what it answers: a registration that declares the same recipient
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
	for _, field := range def.Fields {
		if !slices.Contains(headerFields, field) {
			return "", fmt.Errorf("%w: %q is not a record header member", ErrInvalid, field)
		}
	}
	id := digestBytes(canonicalDoc)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.derivations == nil {
		b.derivations = make(map[string]derivationEntry)
	}
	if prev, ok := b.derivations[id]; ok {
		if prev.def.Recipient == def.Recipient && sameSet(prev.def.Forms, def.Forms) && sameSet(prev.def.Fields, def.Fields) {
			return id, nil
		}
		return "", fmt.Errorf("%w: derivation %s is registered with a different recipient, forms or fields", ErrInvalid, id)
	}
	def.Forms, def.Fields = slices.Clone(def.Forms), slices.Clone(def.Fields)
	b.derivations[id] = derivationEntry{id: id, def: def}
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
