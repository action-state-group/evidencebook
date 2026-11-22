package deal

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every wording key this plugin's code names is in the built-in packs this
// process speaks with (the core's and this plugin's own), so no line is left
// out for a missing key (Line drops one). capsule-cli's
// TestEveryWordingKeyTheCodeNamesIsInThePack checks the core's code; this is
// the same check over this plugin's code (capsule-cli#326, design note
// section 3.1; capsule-cli#428).
func TestEveryWordingKeyTheDealCodeNamesIsInThePack(t *testing.T) {
	w := cli.BuiltinWording()
	key := regexp.MustCompile(`"((?:say|card|report|action)\.[a-z_.]*[a-z_])"`)
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	checked := 0
	var missing []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range key.FindAllStringSubmatch(string(src), -1) {
			if _, ok := w.Entries[m[1]]; !ok {
				missing = append(missing, f+": "+m[1])
			}
		}
	}
	require.Positive(t, checked, "the glob matched no source file")
	assert.Empty(t, missing, "wording keys the built-in pack lacks")
}

// deal-view.js speaks only with the pack's page.* words (evidencebook-deal#97):
// every key it names is in the plugin's pack, and no page.* entry sits unused,
// so a word cannot be left out of the page for a missing key (W words nothing
// for one) and the pack holds no word the page never says. A key built at run
// time is named by its prefix, which ends in a dot.
func TestTheDealViewSpeaksWithThePackPageWords(t *testing.T) {
	entries := cli.BuiltinWording().Entries
	// The built-in table names every key; only the code's own mentions count.
	code := regexp.MustCompile(`(?s)// BEGIN DEAL_VIEW_WORDS\n.*?// END DEAL_VIEW_WORDS\n`).ReplaceAllString(DealViewJS, "")
	named := regexp.MustCompile(`"(page\.[a-z_0-9.]*)"`).FindAllStringSubmatch(code, -1)
	require.NotEmpty(t, named)
	var prefixes []string
	exact := map[string]bool{}
	for _, m := range named {
		if strings.HasSuffix(m[1], ".") {
			prefixes = append(prefixes, m[1])
			continue
		}
		exact[m[1]] = true
		assert.Contains(t, entries, m[1], "deal-view.js names a page word the pack lacks")
	}
	// A line with a placeholder the call gives no value for is left out
	// (W), so each call of a key passes every placeholder its entry holds.
	holder := regexp.MustCompile(`\{([a-z_]+)\}`)
	for k := range exact {
		names := holder.FindAllStringSubmatch(entries[k], -1)
		if len(names) == 0 {
			continue
		}
		calls := regexp.MustCompile(`"`+regexp.QuoteMeta(k)+`"`).FindAllStringIndex(code, -1)
		for _, c := range calls {
			args := code[c[1]:min(len(code), c[1]+400)]
			for _, n := range names {
				assert.Regexp(t, `\b`+n[1]+`\b`, args, "a call of %s gives no value for {%s}", k, n[1])
			}
		}
	}
	for k := range entries {
		if !strings.HasPrefix(k, pagePrefix) {
			continue
		}
		used := exact[k]
		for _, p := range prefixes {
			used = used || strings.HasPrefix(k, p)
		}
		assert.True(t, used, "the pack's page word %s is never said by deal-view.js", k)
	}
}

// dealViewWords are the pack entries deal-view.js holds built in
// (DEAL_VIEW_WORDS): its own page.* words and the core's size.* words the
// receipt names a settlement's band with.
func dealViewWords() map[string]string {
	words := map[string]string{}
	for k, v := range cli.BuiltinWording().Entries {
		if strings.HasPrefix(k, pagePrefix) || strings.HasPrefix(k, "size.") {
			words[k] = v
		}
	}
	return words
}

// The module's built-in words are the pack's (evidencebook-deal#97): a viewer
// that opens a deal bundle runs deal-view.js with no capsulectlDealPage, so
// the words ride in the module, and the pack stays their source.
// CAPSULECTL_UPDATE_DEAL_VIEW_WORDS=1 rewrites the table (and the digests that
// pin the script).
func TestDealViewDefaultWordsAreThePack(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(dealViewWords()))
	raw := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	block := regexp.MustCompile(`(?s)// BEGIN DEAL_VIEW_WORDS\n.*?// END DEAL_VIEW_WORDS\n`)
	want := "// BEGIN DEAL_VIEW_WORDS\nconst DEAL_VIEW_WORDS = " + string(raw) + ";\n// END DEAL_VIEW_WORDS\n"
	if os.Getenv("CAPSULECTL_UPDATE_DEAL_VIEW_WORDS") == "1" {
		old := DealViewJS
		next := block.ReplaceAllLiteralString(old, want)
		require.NoError(t, os.WriteFile("assets/deal-view.js", []byte(next), 0o644))
		from, to := strings.TrimSpace(DealViewJSSHA256), hexSHA256([]byte(next))
		for _, f := range []string{"assets/deal-view.js.sha256", "assets/deal-view.manifest.json", "assets/deal-view-composed.manifest.json"} {
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(f, []byte(strings.ReplaceAll(string(b), from, to)), 0o644))
		}
		t.Skip("deal-view.js words rewritten; run again")
	}
	assert.Equal(t, want, block.FindString(DealViewJS), "DEAL_VIEW_WORDS is not the pack's words")
}

// A deal page carries only the words that differ from the built-in ones: none
// for the built-in pack, so no pack text reaches the page data the share gate
// reads, and a pinned pack's changes.
func TestADealPageCarriesOnlyTheWordsThatDifferFromTheBuiltInOnes(t *testing.T) {
	w := cli.BuiltinWording()
	for k := range dealPageWords(w) {
		assert.NotContains(t, k, pagePrefix, "built-in page word %s rides on the page", k)
	}
	w.Entries = maps.Clone(w.Entries)
	w.Entries["page.card.done"] = "Finished: {text}"
	got := dealPageWords(w)
	assert.Equal(t, "Finished: {text}", got["page.card.done"])
	assert.NotContains(t, got, "page.card.so_far")
}
