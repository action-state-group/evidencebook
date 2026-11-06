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

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
)

// conductPacksFile is the owner's list of conduct pack ids (scope.kind
// conduct), kept beside the book's log. The kind is local policy, written
// into no record.
const conductPacksFile = "conduct-packs"

// conductPacks reads the profile's private pack ids: one per line, blank
// lines and "#" comments skipped. No file means no conduct packs. Every
// bundle the book builds is handed this list, so a Result that cites one
// never goes into a bundle (evidencebook#412); a file that cannot be read
// refuses the bundle rather than leaving the packs unprotected.
func conductPacks(p cli.Profile) ([]string, error) {
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
		// "pack # note" would be stored as one id that matches no Result and
		// protect nothing, so a line is exactly one id.
		if strings.ContainsAny(line, " \t#") {
			return nil, cli.InputError(fmt.Sprintf("%s: %q is not a pack id; put one pack id per line, with comments on their own line", path, line))
		}
		packs = append(packs, line)
	}
	return packs, scanner.Err()
}

// citesConductPack reports whether a Result's header cites a listed pack in
// its subject_ref or a claim's contract_ref ("<pack_id>@<version>"), the
// test the book applies to a record a bundle discloses.
func citesConductPack(h evidencebook.Header, packs []string) bool {
	if h.RecordType != resultRecordType || len(packs) == 0 {
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
