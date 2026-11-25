// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnAdmissionThatDoesNotAdmitIsCoded(t *testing.T) {
	_, err := ParseAdmission(engineOutput(t, "ASK", false, nil), time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	assertCoded(t, err, CodeAdmitNotAdmitted, "ASK")
	assert.Contains(t, inputReason(t, err), "pass the new output to ParseAdmission and Store.Admit")
}

// The package's texts name its own calls: never a command of a program
// built on this package (capsulectl), a request to run a command, or a
// company or demo name. A refusal a program words in its own terms is coded
// (InputError.Code). capsule-engine's command, which produces ParseAdmission's
// input, is named where the input comes from.
func TestTheTextsNameOnlyThisPackagesCalls(t *testing.T) {
	refused := regexp.MustCompile("`deal [a-z]|capsulectl|run the command|(?i)\\b(goose|mesh|actionstate|action state group)\\b")
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			assert.False(t, refused.MatchString(line), "%s:%d names a command or product: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}
