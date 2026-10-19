// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/action-state-group/capsule-emit/go/artifact"
)

// CommitAlgNonce256 names the salted per-field commitment a header's
// field_commitments use: agent-action-capsule spec/presentation-contract-v0.md
// §7.2, the construction capsule-cli's deal records already declare under the
// same identifier, and the one evidencebook#44 rules a History's deal-level
// commitments use:
//
//	commitment = lowercase_hex(SHA-256(JCS({"nonce": N, "text": T})))
//
// N is 64 lowercase hex characters encoding 32 fresh CSPRNG bytes per
// committed text; T is the exact text, unnormalized. That is the Disclosure
// Envelope's DE-3 computation (JSON-DIGEST) applied to the opening object
// {nonce, text}, never to the bare text: without N a guess at T cannot be
// tested.
const CommitAlgNonce256 = "sha256-jcs-nonce256"

// openingPrefix names a record's openings among its originals in the
// artifact store: the original "opening.<field>" holds JCS({nonce, text}),
// whose SHA-256 is that field's commitment. Artifact names are at most 64
// characters of [A-Za-z0-9._-] (capsule-emit artifact.Record), so a field
// name is at most 56.
const openingPrefix = "opening."

var fieldName = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,55}$`)

// Opening is what opens one field commitment: the nonce and the text. It
// stays with the record's originals and leaves only when disclosed.
type Opening struct {
	Nonce string `json:"nonce"`
	Text  string `json:"text"`
}

// NewOpening draws a fresh 32-byte nonce for text.
func NewOpening(text string) (Opening, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return Opening{}, err
	}
	return Opening{Nonce: hex.EncodeToString(nonce), Text: text}, nil
}

// canonical is UTF8(JCS({"nonce", "text"})), the bytes the commitment hashes.
func (o Opening) canonical() ([]byte, error) {
	if !hex64.MatchString(o.Nonce) {
		return nil, fmt.Errorf("%w: an opening's nonce is 64 lowercase hex characters", ErrInvalid)
	}
	// JSON text is UTF-8; encoding/json would replace invalid bytes, which
	// would commit a text other than the one given.
	if !utf8.ValidString(o.Text) {
		return nil, fmt.Errorf("%w: an opening's text is not valid UTF-8", ErrInvalid)
	}
	return canonicalJSON(o)
}

// Commitment is the field commitment o opens.
func (o Opening) Commitment() (string, error) {
	bytes, err := o.canonical()
	if err != nil {
		return "", err
	}
	return digestBytes(bytes), nil
}

// VerifyOpening reports whether o opens commitment: its nonce is well formed
// and SHA-256(JCS({nonce, text})) equals commitment, compared as lowercase
// hex strings. It never errors; a malformed opening does not open anything.
func VerifyOpening(commitment string, o Opening) bool {
	got, err := o.Commitment()
	return err == nil && hex64.MatchString(commitment) && got == commitment
}

// fieldOriginals turns an entry's committed texts into their openings, held
// as originals, and the header's field_commitments.
func fieldOriginals(fields map[string]string) ([]artifact.Artifact, map[string]string, error) {
	if len(fields) == 0 {
		return nil, nil, nil
	}
	originals := make([]artifact.Artifact, 0, len(fields))
	commitments := make(map[string]string, len(fields))
	for name, text := range fields {
		if !fieldName.MatchString(name) {
			return nil, nil, fmt.Errorf("%w: field name %q is not 1-56 of [a-z0-9_], starting with a letter or digit", ErrInvalid, name)
		}
		opening, err := NewOpening(text)
		if err != nil {
			return nil, nil, err
		}
		content, err := opening.canonical()
		if err != nil {
			return nil, nil, err
		}
		originals = append(originals, artifact.Artifact{Name: openingPrefix + name, State: artifact.Present, Content: content})
		commitments[name] = digestBytes(content)
	}
	return originals, commitments, nil
}

// isOpening reports whether an original is a field opening rather than a
// payload: openings are committed through field_commitments, not
// payload_commitments.
func isOpening(original artifact.Artifact) bool {
	return strings.HasPrefix(original.Name, openingPrefix)
}

// openingFrom reads an opening held as an original, checking it opens the
// commitment its record declares for that field.
func openingFrom(original artifact.Artifact, commitment string) (Opening, error) {
	var opening Opening
	if err := json.Unmarshal(original.Content, &opening); err != nil {
		return Opening{}, fmt.Errorf("%w: opening %s: %v", ErrCorrupt, original.Name, err)
	}
	if !VerifyOpening(commitment, opening) {
		return Opening{}, fmt.Errorf("%w: opening %s does not open its field commitment", ErrCorrupt, original.Name)
	}
	return opening, nil
}

// Opening returns the opening of one field commitment of a record, read from
// the artifact store, when the record's retention state allows it. It is
// checked against the commitment the record's header carries.
func (b *Book) Opening(ctx context.Context, recordID, field string) (Opening, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	record, ok := b.records[recordID]
	commitment, committed := "", false
	if ok {
		commitment, committed = record.Header.FieldCommitments[field]
	}
	if !committed {
		return Opening{}, fmt.Errorf("%w: record %s commits no field %q", ErrNotFound, recordID, field)
	}
	state, _, _, err := b.retentionLocked(recordID)
	if err != nil {
		return Opening{}, err
	}
	if state == Withheld || state == Deleted {
		return Opening{}, &UnavailableError{RecordID: recordID, Digest: commitment, State: state}
	}
	held, err := b.originals.Get(ctx, recordID)
	if errors.Is(err, artifact.ErrNotFound) {
		return Opening{}, fmt.Errorf("%w: record %s is %s but the artifact store holds none of its originals", ErrCorrupt, recordID, state)
	}
	if err != nil {
		return Opening{}, err
	}
	for _, original := range held.Artifacts {
		if original.Name != openingPrefix+field {
			continue
		}
		if original.State != artifact.Present {
			return Opening{}, fmt.Errorf("%w: the opening of %s field %q is %s but the artifact store holds it purged", ErrCorrupt, recordID, field, state)
		}
		return openingFrom(original, commitment)
	}
	return Opening{}, fmt.Errorf("%w: record %s is %s but the artifact store holds no opening of field %q", ErrCorrupt, recordID, state, field)
}
