package book

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
)

// derivationsDirName is where a book profile keeps the Terms Packs its holder
// opted in to being served by the digest of their compiled definition
// (evidencebook#606): <profile database>/book/derivations/*.json, one file per
// pack, beside the conduct-packs list. A pack not listed there is refused
// derivation_unsupported.
const derivationsDirName = "derivations"

// derivationFile is one pack file. `document` is the compiled definition as
// compiled: the digest requesters name is the SHA-256 of its JCS, so it is read
// as the file's own bytes. `forms` and `fields` are what the holder declares:
// this plugin reads no member of the definition (the interop profile does not
// fix them), and `fields` are what each answer reveals of a record, header
// members or paths into one (`statement.generated_at`).
type derivationFile struct {
	Document   json.RawMessage
	Recipients []string
	Forms      []string
	Fields     []string
	// Proofs is on unless the file says false: the answer carries the
	// anchor's checkpoint and a proof per record, which a requester's report
	// needs (a report over answers without proofs is refused unless asked).
	Proofs *bool
}

type loadedDerivation struct {
	file   string
	digest string
	def    evidencebook.DerivationDefinition
}

// derivationDir is the profile's pack directory.
func derivationDir(p cli.Profile) string {
	return filepath.Join(p.Connection.Database, "book", derivationsDirName)
}

// loadDerivations reads every *.json file of the profile's pack directory, in
// file-name order. No directory means no packs. A file that cannot be read or
// is not exactly the format refuses, naming the file: one bad pack file stops
// every answer, public questions included, rather than leave a pack the
// holder meant to serve silently unserved.
func loadDerivations(p cli.Profile) ([]loadedDerivation, error) {
	dir := derivationDir(p)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, cli.InputError(fmt.Sprintf("cannot read the pack directory %s: %v", dir, err))
	}
	var loaded []loadedDerivation
	byDigest := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, cli.InputError(fmt.Sprintf("cannot read the pack file %s: %v", path, err))
		}
		file, err := readDerivationFile(raw)
		if err != nil {
			return nil, cli.InputError(fmt.Sprintf("pack file %s: %v", path, err))
		}
		digest, err := documentDigest(file.Document)
		if err != nil {
			return nil, cli.InputError(fmt.Sprintf("pack file %s: document: %v", path, err))
		}
		def := evidencebook.DerivationDefinition{Document: file.Document, Recipients: file.Recipients, Fields: file.Fields, Proofs: file.Proofs == nil || *file.Proofs}
		for _, form := range file.Forms {
			def.Forms = append(def.Forms, evidencebook.SubjectKind(form))
		}
		if first, repeated := byDigest[digest]; repeated {
			// One digest, one meaning: a second file may repeat a pack as the
			// first declared it, never declare it differently.
			if !sameDeclaration(loaded, first, def) {
				return nil, cli.InputError(fmt.Sprintf("pack files %s and %s hold the same document (%s) and declare it differently", first, path, digest))
			}
			continue
		}
		byDigest[digest] = path
		loaded = append(loaded, loadedDerivation{file: path, digest: digest, def: def})
	}
	return loaded, nil
}

func sameDeclaration(loaded []loadedDerivation, file string, def evidencebook.DerivationDefinition) bool {
	for _, l := range loaded {
		if l.file != file {
			continue
		}
		return l.def.Proofs == def.Proofs && sameStrings(l.def.Recipients, def.Recipients) && sameStrings(forms(l.def), forms(def)) && sameStrings(l.def.Fields, def.Fields)
	}
	return false
}

func forms(def evidencebook.DerivationDefinition) []string {
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

// registerDerivations registers every pack of the profile's directory with
// the book, as the book registers per open: nothing is written into its
// history. It returns the packs for `doctor` to describe.
func registerDerivations(book *evidencebook.Book, p cli.Profile) ([]loadedDerivation, error) {
	loaded, err := loadDerivations(p)
	if err != nil {
		return nil, err
	}
	for _, l := range loaded {
		id, err := book.RegisterDerivation(l.def)
		if err != nil {
			return nil, cli.InputError(fmt.Sprintf("pack file %s: %v", l.file, err))
		}
		if id != l.digest {
			return nil, cli.InputError(fmt.Sprintf("pack file %s: the book registered %s, not the digest %s of the document", l.file, id, l.digest))
		}
	}
	return loaded, nil
}

// documentDigest is the identifier of a definition: SHA-256 over the JCS of
// its document, the digest a requester names.
func documentDigest(document []byte) (string, error) {
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

// readDerivationFile reads a pack file exactly: one JSON object whose members
// are document, recipients, forms, fields and proofs, spelt so, each once.
// encoding/json would take "Proofs" for "proofs" and keep the last of two
// "proofs", so the members are read one by one.
func readDerivationFile(raw []byte) (derivationFile, error) {
	var file derivationFile
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

// describeDerivations is `doctor`'s line for the packs it serves: each digest,
// whether its answers carry proofs, who is granted it, and what that leaves
// unserved.
func describeDerivations(loaded []loadedDerivation) evidencebook.DoctorFinding {
	if len(loaded) == 0 {
		return evidencebook.DoctorFinding{Check: "terms_packs_served", State: evidencebook.DoctorStateNone, Detail: "no Terms Pack is registered in " + derivationsDirName + ": every derivation named by a digest is refused derivation_unsupported"}
	}
	lines := make([]string, len(loaded))
	for i, l := range loaded {
		line := fmt.Sprintf("%s (forms %s, %d granted", l.digest, strings.Join(forms(l.def), "+"), len(grants(l.def)))
		if len(grants(l.def)) == 0 {
			line += ": no recipient, so over full_history it is answered to no one"
		}
		if !l.def.Proofs {
			line += "; answers carry no proofs, so a requester's report is refused unless it allows unproven inputs"
		}
		lines[i] = line + ")"
	}
	return evidencebook.DoctorFinding{Check: "terms_packs_served", State: evidencebook.DoctorStateAnswered, Detail: strings.Join(lines, "; ")}
}

func grants(def evidencebook.DerivationDefinition) []string {
	out := slices.Clone(def.Recipients)
	if def.Recipient != "" {
		out = append(out, def.Recipient)
	}
	return out
}
