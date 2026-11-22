package deal

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The skill's "Points of no return" table names every action `deal check`
// accepts, per deal type, as dealPointsOfNoReturn and the represent check
// define them. A seller's offer and represent sit in their own row.
func TestSkillPointsOfNoReturnTableMatchesThePlugin(t *testing.T) {
	raw, err := os.ReadFile("../../skills/deal/SKILL.md")
	require.NoError(t, err)
	_, tail, ok := strings.Cut(string(raw), "\n## Points of no return\n")
	require.True(t, ok, "the Points of no return section is missing")
	section, _, _ := strings.Cut(tail, "\n## ")

	cell := regexp.MustCompile("`([a-z_]+)`")
	rows := map[string][]string{}
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cols := strings.Split(strings.Trim(line, "| "), " | ")
		require.Len(t, cols, 2, line)
		var actions []string
		for _, m := range cell.FindAllStringSubmatch(cols[1], -1) {
			actions = append(actions, m[1])
		}
		if len(actions) == 0 {
			continue // the header and separator rows
		}
		slices.Sort(actions)
		rows[strings.Trim(cols[0], "` ")] = actions
	}
	require.NotEmpty(t, rows, "no table row parsed: the table was reformatted")

	sellerRow := "any deal where the user sells"
	assert.Equal(t, []string{offerAction, representAction}, rows[sellerRow])
	delete(rows, sellerRow)

	require.Len(t, rows, len(dealPointsOfNoReturn), "one row per deal type")
	for dealType, actions := range dealPointsOfNoReturn {
		want := slices.DeleteFunc(slices.Clone(actions), func(a string) bool { return a == offerAction })
		slices.Sort(want)
		assert.Equal(t, want, rows[dealType], dealType)
	}
}
