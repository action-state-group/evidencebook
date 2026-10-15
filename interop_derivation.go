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
	// for, as the definition names it. Only that requester is answered. An
	// empty Recipient makes the derivation answerable under the share policy
	// like any protected subject.
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
	def.Forms, def.Fields = slices.Clone(def.Forms), slices.Clone(def.Fields)
	b.derivations[id] = derivationEntry{id: id, def: def}
	return id, nil
}
