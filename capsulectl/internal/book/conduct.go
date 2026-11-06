package book

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
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
		packs = append(packs, line)
	}
	return packs, scanner.Err()
}
