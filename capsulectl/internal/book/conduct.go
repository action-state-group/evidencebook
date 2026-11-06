package book

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"go.yaml.in/yaml/v3"
)

// conductPacksFile is the owner's list of conduct pack ids (scope.kind
// conduct), kept beside the book's log. The kind is local policy, written
// into no record.
const conductPacksFile = "conduct-packs"

// conductPacks is the owner's private pack ids: the packs of the current
// directory's workspace that say so (workspacePacksDir) and the ids in the
// profile's conduct-packs file, so a pack the owner marked conduct in its
// source is never bundled whether or not anyone also lists it. Every bundle
// the book builds is handed this list, so a record that cites one never goes
// into a bundle (evidencebook#412, #418); a pack source or list that cannot
// be read refuses the bundle rather than leaving the packs unprotected.
func conductPacks(p cli.Profile) ([]string, error) {
	listed, err := listedConductPacks(p)
	if err != nil {
		return nil, err
	}
	workspace, err := workspaceConductPacks()
	if err != nil {
		return nil, err
	}
	for _, id := range workspace {
		if !slices.Contains(listed, id) {
			listed = append(listed, id)
		}
	}
	return listed, nil
}

// workspacePacksDir is where the workspace keeps its Terms Packs, one folder
// per pack with a pack-source.yaml, relative to the current directory.
const workspacePacksDir = "packs"

// workspaceConductPacks reads each packs/*/pack-source.yaml of the current
// directory and returns the pack_id of those whose scope.kind is conduct. No
// packs directory means none; a source that does not parse refuses, since its
// kind cannot be told.
func workspaceConductPacks() ([]string, error) {
	// filepath.Glob would drop a directory it cannot read without an error,
	// leaving its packs unprotected, so the directories are listed here.
	folders, err := os.ReadDir(workspacePacksDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, cli.InputError(fmt.Sprintf("cannot read the packs directory %s, so nothing is bundled: %v", workspacePacksDir, err))
	}
	var packs []string
	for _, folder := range folders {
		path := filepath.Join(workspacePacksDir, folder.Name(), "pack-source.yaml")
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return nil, cli.InputError(fmt.Sprintf("cannot read the pack source %s, so nothing is bundled: %v", path, err))
		}
		var source struct {
			PackID string `yaml:"pack_id"`
			Scope  struct {
				Kind string `yaml:"kind"`
			} `yaml:"scope"`
		}
		if err := yaml.Unmarshal(data, &source); err != nil {
			return nil, cli.InputError(fmt.Sprintf("cannot parse the pack source %s, so its scope.kind is unknown and nothing is bundled: %v", path, err))
		}
		// A source with no scope.kind is an ordinary pack, not a conduct one.
		if source.Scope.Kind != "conduct" {
			continue
		}
		id, _, _ := strings.Cut(strings.TrimSpace(source.PackID), "@")
		if id == "" {
			return nil, cli.InputError(fmt.Sprintf("%s is a conduct pack without a pack_id, so nothing is bundled", path))
		}
		packs = append(packs, id)
	}
	return packs, nil
}

// listedConductPacks reads the profile's conduct-packs file: one pack id per
// line, blank lines and "#" comments skipped. No file means none.
func listedConductPacks(p cli.Profile) ([]string, error) {
	path := filepath.Join(p.Connection.Database, "book", conductPacksFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, cli.InputError(fmt.Sprintf("cannot read the conduct pack list %s, so nothing is bundled: %v", path, err))
	}
	var packs []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// "pack # note" would be stored as one id that matches no record and
		// protect nothing, so a line is exactly one id.
		if strings.ContainsFunc(line, func(r rune) bool { return r == '#' || unicode.IsSpace(r) }) {
			return nil, cli.InputError(fmt.Sprintf("%s: %q is not a pack id; put one pack id per line, with comments on their own line", path, line))
		}
		packs = append(packs, line)
	}
	return packs, scanner.Err()
}

// citesConductPack reports whether a record's header cites a listed pack in
// its subject_ref or a claim's contract_ref ("<pack_id>@<version>"), whatever
// its record type, as the book's own check does once capsulectl pins an evidencebook
// release with #418. The book pinned today (v0.13.7) checks only a Result, so
// another record type is refused only after the pin moves.
func citesConductPack(h evidencebook.Header, packs []string) bool {
	if len(packs) == 0 {
		return false
	}
	refs := []string{h.SubjectRef}
	var doc struct {
		Claims []struct {
			ContractRef string `json:"contract_ref"`
		} `json:"claims"`
	}
	_ = json.Unmarshal(h.Statement, &doc)
	for _, c := range doc.Claims {
		refs = append(refs, c.ContractRef)
	}
	for _, ref := range refs {
		pack, _, _ := strings.Cut(ref, "@")
		if slices.Contains(packs, pack) {
			return true
		}
	}
	return false
}
