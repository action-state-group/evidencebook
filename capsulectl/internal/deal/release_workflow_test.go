package deal

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The release workflow publishes only a tag a shared copy keeps as its
// producer's version: a release's sealed records name the tag, and a shared
// copy refuses a producer outside producerVersion's shape (evidencebook-deal#3).
func TestReleaseTagPolicyIsTheProducerShape(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	require.NoError(t, err)
	m := regexp.MustCompile(`(?s)name: Check the tag is a version a shared copy keeps.*?grep -Eqx '([^']+)'`).FindSubmatch(workflow)
	require.NotNil(t, m, "the release workflow checks the tag")
	policy := regexp.MustCompile(`^(?:` + string(m[1]) + `)$`)
	for _, tag := range []string{
		"v0.1.0", "v1.2.3", "v0.1.0-rc1", "v0.1.0-rc", "v9999.0.0-dev999", "v0.1.0-alpha2", "v0.1.0-beta3",
		"0.1.0", "v0.1", "v0.1.0-rc1000", "v0.1.0-ci", "v0.1.0-rc.1", "v10000.0.0", "v0.1.0+build", "v0.1.0-RC1",
	} {
		keeps := producerVersion.MatchString(tag) && tag[0] == 'v'
		assert.Equal(t, keeps, policy.MatchString(tag), tag)
	}
}
