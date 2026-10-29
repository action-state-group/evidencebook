package deal

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The skill's consent text and its scheduling instruction state the cadence a
// new profile really has: the host package's default, read here, not copied.
func TestSkillCadenceWordsMatchTheProfileDefault(t *testing.T) {
	cfg, err := cli.Profile{}.DealCadence()
	require.NoError(t, err)
	require.Zero(t, cfg.Interval%60e9, "the default interval is whole minutes")
	minutes := int(cfg.Interval.Minutes())

	raw, err := os.ReadFile("../../skills/deal/SKILL.md")
	require.NoError(t, err)
	skill := string(raw)

	_, tail, ok := strings.Cut(skill, "Tell the user, in one message, exactly what this does:")
	require.True(t, ok, "the setup consent sentence is missing")
	consent, _, _ := strings.Cut(tail, "\n\n")
	consent = strings.Join(strings.Fields(consent), " ")

	assert.Contains(t, consent, fmt.Sprintf("about every %d minutes, whether or not anything happened, one checkpoint of hashes goes to the witness", minutes))
	assert.NotContains(t, consent, "an hour")
	assert.Contains(t, skill, fmt.Sprintf("cll checkpoint cadence --wait-up-to %dm", minutes))
}
