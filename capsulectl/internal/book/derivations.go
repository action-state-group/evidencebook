package book

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/capsulectl/internal/packfile"
)

// derivationsDirName is where a book profile keeps the Terms Packs its holder
// opted in to being served by the digest of their compiled definition
// (evidencebook#606): <profile database>/book/derivations/*.json, one file per
// pack, beside the conduct-packs list. A pack not listed there is refused
// derivation_unsupported.
const derivationsDirName = "derivations"

// derivationFile is one pack file (packfile.File).
type derivationFile = packfile.File

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
		def := file.Definition()
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
		return packfile.SameDeclaration(l.def, def)
	}
	return false
}

func forms(def evidencebook.DerivationDefinition) []string { return packfile.Forms(def) }

// registerDerivations registers every pack of the profile's directory with
// the book, as the book registers per open: nothing is written into its
// history. It returns the packs for `doctor` to describe.
func registerDerivations(opened openedBook, p cli.Profile) ([]loadedDerivation, error) {
	loaded, err := loadDerivations(p)
	if err != nil {
		return nil, err
	}
	register := opened.book.RegisterDerivation
	if opened.shelf != nil {
		// Every window's book answers the packs (evidencebook#689 §3).
		register = opened.shelf.RegisterDerivation
	}
	for _, l := range loaded {
		id, err := register(l.def)
		if err != nil {
			return nil, cli.InputError(fmt.Sprintf("pack file %s: %v", l.file, err))
		}
		if id != l.digest {
			return nil, cli.InputError(fmt.Sprintf("pack file %s: the book registered %s, not the digest %s of the document", l.file, id, l.digest))
		}
	}
	return loaded, nil
}

// documentDigest is a compiled definition's digest (packfile.Digest).
func documentDigest(document []byte) (string, error) { return packfile.Digest(document) }

// readDerivationFile reads a pack file exactly (packfile.Read).
func readDerivationFile(raw []byte) (derivationFile, error) { return packfile.Read(raw) }

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
