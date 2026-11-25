package deal

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The release workflow publishes only a tag a shared copy keeps as its
// producer's version: a release's sealed records name the tag, and a shared
// copy refuses a producer outside producerVersion's shape (evidencebook-deal#3).
func TestReleaseTagPolicyIsTheProducerShape(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "release.yml"))
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

// The one capsulectl release carries the core's published binaries unchanged
// (evidencebook#895 item 4). They keep capsule-cli's attestation, so this
// workflow's attestation never names them, and they are added after it and
// before the release is published, with SHA256SUMS checked over every file.
func TestReleaseAddsTheCoreAfterThePluginsAttestation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "release.yml"))
	require.NoError(t, err)
	workflow := string(raw)
	step := func(name string) int {
		i := strings.Index(workflow, "- name: "+name+"\n")
		require.GreaterOrEqual(t, i, 0, name)
		return i
	}
	attest := step("Attest build provenance of the assets")
	bundle := step("Attach the attestation bundle")
	core := step("Add the core's published binaries")
	sums := step("Check SHA256SUMS lists every asset")
	publish := step("Publish GitHub release")
	assert.Less(t, attest, bundle)
	assert.Less(t, bundle, core)
	assert.Less(t, core, sums)
	assert.Less(t, sums, publish)
	assert.Contains(t, workflow[core:sums], `scripts/release-core-assets.sh "$VERSION" dist`)

	subjects := regexp.MustCompile(`(?s)subject-path: \|\n(.*)$`).FindStringSubmatch(workflow[attest:bundle])
	require.NotNil(t, subjects, "the attestation step names its subjects")
	for _, line := range strings.Split(strings.TrimSpace(subjects[1]), "\n") {
		glob := strings.TrimPrefix(strings.TrimSpace(line), "capsulectl/dist/")
		for _, core := range []string{"capsulectl-v0.1.0-rc55-linux-amd64", "capsulectl-v0.1.0-rc55.sigstore.json"} {
			matched, err := filepath.Match(glob, core)
			require.NoError(t, err)
			assert.False(t, matched, "%s would attest the core's %s", glob, core)
		}
	}

	// A recovered draft gets the same title and notes as a new release.
	assert.Contains(t, workflow[publish:], `--title "$title" --notes "$notes" --draft=false`)
	assert.Contains(t, workflow[publish:], `--title "$title" --notes "$notes"`+"\n")
}
