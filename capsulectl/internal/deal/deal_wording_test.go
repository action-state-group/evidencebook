package deal

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullPackDoc is a pack holding every key a deal profile speaks with: the
// core's built-in pack and this plugin's, as a pack pinned on a deal profile
// must.
func fullPackDoc(t *testing.T) map[string]any {
	t.Helper()
	var doc, deal map[string]any
	require.NoError(t, json.Unmarshal(cli.BuiltinWordingBytes(), &doc))
	require.NoError(t, json.Unmarshal(dealWordingBytes, &deal))
	maps.Copy(doc["entries"].(map[string]any), deal["entries"].(map[string]any))
	doc["id"] = "org.example.product.wording/v0"
	return doc
}

// writePackDoc writes doc as a pack file and returns its path.
func writePackDoc(t *testing.T, doc map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "pack.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

// plainWordsForbidden is what a user is never read: an id, a digest, a rule
// id, a flag or a UTC time (capsule-cli's AssertPlainWords, which checked
// these words while the core's pack held them).
var plainWordsForbidden = []*regexp.Regexp{
	regexp.MustCompile(`[0-9a-f]{16,}`),
	regexp.MustCompile(`\bdeal-[0-9a-z]+`),
	regexp.MustCompile(`\b[rR][0-9]{2}\b`),
	regexp.MustCompile(`--[a-z]`),
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`),
	regexp.MustCompile(`\bUTC\b|\d{2}:\d{2}Z`),
}

// The plugin's pack is valid, plain, sorted and disjoint from the core's, and
// this process speaks with both.
func TestTheDealWordingPackIsRegisteredBesideTheCores(t *testing.T) {
	var doc struct {
		ID      string          `json:"id"`
		Locale  string          `json:"locale"`
		Entries json.RawMessage `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(dealWordingBytes, &doc))
	assert.Equal(t, "capsulectl.deal-wording/v0", doc.ID)
	assert.Equal(t, cli.BuiltinWording().Locale, doc.Locale)
	var names []string
	for _, k := range regexp.MustCompile(`"([^"]+)":`).FindAllStringSubmatch(string(doc.Entries), -1) {
		names = append(names, k[1])
	}
	assert.True(t, sort.StringsAreSorted(names), "the keys are listed once, sorted")

	var entries map[string]string
	require.NoError(t, json.Unmarshal(doc.Entries, &entries))
	var core map[string]any
	require.NoError(t, json.Unmarshal(cli.BuiltinWordingBytes(), &core))
	for k, text := range entries {
		for _, bad := range plainWordsForbidden {
			assert.NotRegexp(t, bad, text, k)
		}
		assert.NotContains(t, core["entries"], k, "the core's pack holds no deal key")
		assert.Equal(t, text, cli.BuiltinWording().Entries[k], "this process speaks the deal key %s", k)
	}
	assert.Contains(t, entries, "card.terms.label", "the terms line is the plugin's")
}

// The merge gate of capsule-cli#428 step 3 (Steven's door (c) ruling): the
// card's terms line lives in this plugin's pack, and a pinned pack that
// could hide a term from it is still refused across processes. The real
// capsulectl reads this plugin's pack from the handshake of the real
// capsulectl-deal when it pins, and this plugin's process refuses such a pack
// when it speaks (TestAPackThatHidesTheTermsLineIsNotUsed).
func TestCapsulectlRefusesAPackThatHidesADealTerm(t *testing.T) {
	bin := filepath.Join(installedCapsulectl(t), "capsulectl")
	dealFixture(t)
	run := func(args ...string) (string, error) {
		out, err := exec.Command(bin, args...).CombinedOutput()
		return string(out), err
	}

	coreOnly := map[string]any{}
	require.NoError(t, json.Unmarshal(cli.BuiltinWordingBytes(), &coreOnly))
	coreOnly["id"] = "org.example.product.wording/v0"
	out, err := run("profile", "update", "--profile", "deal", "--wording", writePackDoc(t, coreOnly))
	require.Error(t, err, out)
	assert.Contains(t, out, "of the capsulectl-deal plugin's keys, first action.cancel: a pack that plugin speaks with holds the core's keys and its own")

	for key, text := range map[string]string{
		"card.terms.price":          "a new price",
		"card.terms.label":          "Saying yes also agrees to {terms}. {extra}",
		"card.terms.not_refundable": " ",
	} {
		doc := fullPackDoc(t)
		doc["entries"].(map[string]any)[key] = text
		out, err := run("profile", "update", "--profile", "deal", "--wording", writePackDoc(t, doc))
		require.Error(t, err, key)
		assert.Contains(t, out, "could hide a term a yes agrees to: "+key, key)
	}
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	assert.Empty(t, p.Wording.Path, "a refused pack is not pinned")

	doc := fullPackDoc(t)
	doc["entries"].(map[string]any)["card.tag.ask"] = "PAUSED FOR YOU, PLEASE"
	path := writePackDoc(t, doc)
	out, err = run("profile", "update", "--profile", "deal", "--wording", path)
	require.NoError(t, err, out)
	p, err = cli.LoadProfile("deal")
	require.NoError(t, err)
	assert.Equal(t, path, p.Wording.Path)
	out, err = run("--profile", "deal", "doctor")
	require.NoError(t, err, out)
	assert.True(t, strings.Contains(out, `"used":"pinned"`), out)
}
