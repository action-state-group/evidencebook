// Package packfile reads a Terms Pack file, the one format capsulectl-book's
// pack directory and capsulectl-deal's `deal window respond --pack` share
// (evidencebook#606, #797), so the two plugins read it alike without linking
// each other's package.
package packfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/evidencebook"
)

// File is one pack file. `document` is the compiled definition as
// compiled: the digest requesters name is the SHA-256 of its JCS, so it is read
// as the file's own bytes. `forms` and `fields` are what the holder declares:
// neither plugin reads a member of the definition (the interop profile does not
// fix them), and `fields` are what each answer reveals of a record, header
// members or paths into one (`statement.generated_at`).
type File struct {
	Document   json.RawMessage
	Recipients []string
	Forms      []string
	Fields     []string
	// Proofs is on unless the file says false: the answer carries the
	// anchor's checkpoint and a proof per record, which a requester's report
	// needs (a report over answers without proofs is refused unless asked).
	Proofs *bool
}

// Read reads a pack file exactly: one JSON object whose members
// are document, recipients, forms, fields and proofs, spelt so, each once.
// encoding/json would take "Proofs" for "proofs" and keep the last of two
// "proofs", so the members are read one by one.
func Read(raw []byte) (File, error) {
	var file File
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return file, errors.New("is not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return file, err
		}
		name, _ := tok.(string)
		if seen[name] {
			return file, fmt.Errorf("member %q appears twice", name)
		}
		seen[name] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return file, err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			// json.Unmarshal takes null into a bool or a list without
			// complaint and leaves the zero value: "proofs": null would
			// switch proofs off, "recipients": null grant no one.
			return file, fmt.Errorf("member %q is null", name)
		}
		switch name {
		case "document":
			file.Document = value
		case "recipients":
			err = decodeStrings(value, &file.Recipients)
		case "forms":
			err = decodeStrings(value, &file.Forms)
		case "fields":
			err = decodeStrings(value, &file.Fields)
		case "proofs":
			var on bool
			if err = json.Unmarshal(value, &on); err == nil {
				file.Proofs = &on
			}
		default:
			return file, fmt.Errorf("member %q is not one of document, recipients, forms, fields, proofs", name)
		}
		if err != nil {
			return file, fmt.Errorf("member %q: %v", name, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return file, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return file, errors.New("has data after the object")
	}
	for _, required := range []string{"document", "forms", "fields"} {
		if !seen[required] {
			return file, fmt.Errorf("has no %q member", required)
		}
	}
	return file, nil
}

func decodeStrings(raw json.RawMessage, into *[]string) error {
	return json.Unmarshal(raw, into)
}

// Digest is the identifier of a definition: SHA-256 over the JCS of
// its document, the digest a requester names.
func Digest(document []byte) (string, error) {
	decoded, err := evidencerequest.DecodeJSON(document)
	if err != nil {
		return "", err
	}
	if _, ok := decoded.(map[string]any); !ok {
		return "", errors.New("a definition is a JSON object")
	}
	jcs, err := evidencerequest.EncodeJCS(decoded)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(jcs)
	return hex.EncodeToString(sum[:]), nil
}

// Definition is the derivation a pack file declares: proofs on unless the
// file says false.
func (f File) Definition() evidencebook.DerivationDefinition {
	def := evidencebook.DerivationDefinition{Document: f.Document, Recipients: f.Recipients, Fields: f.Fields, Proofs: f.Proofs == nil || *f.Proofs}
	for _, form := range f.Forms {
		def.Forms = append(def.Forms, evidencebook.SubjectKind(form))
	}
	return def
}

// SameDeclaration reports whether two files of one document declare it
// alike: one digest, one meaning.
func SameDeclaration(a, b evidencebook.DerivationDefinition) bool {
	return a.Proofs == b.Proofs && sameStrings(a.Recipients, b.Recipients) && sameStrings(Forms(a), Forms(b)) && sameStrings(a.Fields, b.Fields)
}

// Forms are a definition's forms as strings.
func Forms(def evidencebook.DerivationDefinition) []string {
	out := make([]string, len(def.Forms))
	for i, f := range def.Forms {
		out[i] = string(f)
	}
	return out
}

func sameStrings(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}
