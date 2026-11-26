package deal

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deal skill is the one agent skill for the deal and book plugins, and it
// teaches five verbs (Steven's ruling on simplification step 2, ops u632,
// 2026-11-25; evidencebook#995). Every other command it taught is an expert
// command, kept with its instructions in a reference file.

const dealSkillDir = "../../skills/deal"

var fiveVerbs = []string{"check", "record", "ask", "verify", "show"}

// The expert commands, re-derived from the two skills' text before the merge:
// each pattern names one, and SKILL.md names none of them.
var expertCommands = map[string]*regexp.Regexp{
	"deal init":                  regexp.MustCompile("deal init"),
	"deal set-version":           regexp.MustCompile("deal set-version"),
	"deal tick":                  regexp.MustCompile("deal tick"),
	"deal audit-host":            regexp.MustCompile("deal audit-host"),
	"deal settlement":            regexp.MustCompile(`deal settlement\b`),
	"deal window":                regexp.MustCompile("deal window"),
	"deal sale":                  regexp.MustCompile("deal sale"),
	"plugin install":             regexp.MustCompile("plugin install"),
	"cll checkpoint cadence":     regexp.MustCompile("cll checkpoint cadence"),
	"book close":                 regexp.MustCompile("book close"),
	"book result derived-inputs": regexp.MustCompile("book result derived-inputs"),
	"book window":                regexp.MustCompile("book window"),
}

func readSkillText(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dealSkillDir, rel))
	require.NoError(t, err)
	return string(raw)
}

// section returns the text under a "## name" heading, up to the next "## ".
func section(t *testing.T, text, name string) string {
	t.Helper()
	_, tail, ok := strings.Cut(text, "\n## "+name+"\n")
	require.True(t, ok, "the %q section is missing", name)
	body, _, _ := strings.Cut(tail, "\n## ")
	return body
}

func TestDealSkillTeachesOnlyTheFiveVerbs(t *testing.T) {
	skill := readSkillText(t, "SKILL.md")
	table := section(t, skill, "The five verbs")
	seen := map[string]bool{}
	rows := 0
	for _, line := range strings.Split(table, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| Verb ") {
			continue
		}
		cols := strings.Split(strings.Trim(line, "| "), " | ")
		require.Len(t, cols, 3, line)
		assert.Contains(t, fiveVerbs, cols[0], "a row maps a verb outside the five: %s", line)
		assert.Contains(t, cols[2], "`", "a row maps its verb to a command: %s", line)
		seen[cols[0]] = true
		rows++
	}
	require.NotZero(t, rows, "no table row parsed: the table was reformatted")
	for _, v := range fiveVerbs {
		assert.True(t, seen[v], "the table maps %s", v)
	}
	// Book commands run on a book profile, never the deal profile.
	flat := strings.Join(strings.Fields(table), " ")
	assert.Contains(t, flat, "the `book` commands run on a jsonl book profile, never the deal profile")
	assert.Contains(t, flat, "stop and ask a human")
	// The one commerce card, and Mesh's service card as its exception (Q4).
	assert.Contains(t, flat, "not a second commerce card")
	// The description covers the book work too.
	front, _, _ := strings.Cut(skill, "\n---\n# ")
	assert.Contains(t, front, "`capsulectl book`")
}

func TestDealSkillNamesNoExpertCommand(t *testing.T) {
	skill := readSkillText(t, "SKILL.md")
	for name, re := range expertCommands {
		assert.False(t, re.MatchString(skill), "SKILL.md names the expert command %q", name)
	}
}

// Each expert command is kept in a reference file, with its instructions.
func TestDealSkillKeepsEachExpertCommandInAReference(t *testing.T) {
	var refs strings.Builder
	for _, rel := range []string{"references/setup.md", "references/expert.md", "references/book.md", "LEGACY-X-DEAL-V0.md"} {
		refs.WriteString(readSkillText(t, rel))
	}
	text := refs.String()
	for name, re := range expertCommands {
		assert.True(t, re.MatchString(text), "no reference keeps %q", name)
	}
	expert := strings.Join(strings.Fields(readSkillText(t, "references/expert.md")), " ")
	// Reading at the last checkpoint keeps its order: tick, wait until due
	// when not due, tick again, then the report.
	order := []string{"Run `deal tick`", "`state: not_due`", "wait until the `due` time it prints", "run it again", "Then run the report again."}
	at := -1
	for _, part := range order {
		i := strings.Index(expert, part)
		require.Greater(t, i, at, "%q is out of order or missing", part)
		at = i
	}
	setup := strings.Join(strings.Fields(readSkillText(t, "references/setup.md")), " ")
	for _, rule := range []string{
		"Installing it is the user's step: never run `plugin install` yourself.",
		"That is the user's step: show them the message and never run it yourself.",
		"**Setting or changing the predicate is the user's policy: never do it yourself.**",
		`choose **"Always allow this site"**, not "allow once"`,
		"Tell the user, in one message, exactly what this does:",
	} {
		assert.Contains(t, setup, rule)
	}
}

// The setup rules stay in SKILL.md without the commands: the user's steps are
// never the agent's, and the setup reference is read before the first deal.
func TestDealSkillKeepsTheSetupRules(t *testing.T) {
	body := strings.Join(strings.Fields(section(t, readSkillText(t, "SKILL.md"), "Setup is the user's")), " ")
	for _, rule := range []string{
		"Setting up is the user's step, never yours: installing a plugin, creating a profile or its store, moving a store to a new version, and setting or changing the materiality predicate",
		"stop and show the user its message as it is; never run the command it names yourself.",
		"Before the first deal on a profile, read [references/setup.md](references/setup.md)",
	} {
		assert.Contains(t, body, rule)
	}
}

var mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// githubAnchor is the id GitHub gives a Markdown heading.
func githubAnchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func anchorsOf(text string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimLeft(line, "#"), " "); ok && strings.HasPrefix(line, "#") {
			out[githubAnchor(h)] = true
		}
	}
	return out
}

// Every relative link in the skill's Markdown resolves inside the tree as
// released (deal-skill-$V.tar.gz is the skills/deal tree), anchors included.
func TestDealSkillRelativeLinksResolve(t *testing.T) {
	err := filepath.WalkDir(dealSkillDir, func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, m := range mdLink.FindAllStringSubmatch(string(raw), -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			file, anchor, _ := strings.Cut(target, "#")
			dest := path
			if file != "" {
				dest = filepath.Join(filepath.Dir(path), file)
			}
			info, err := os.Stat(dest)
			if !assert.NoError(t, err, "%s links to %s", path, target) {
				continue
			}
			if anchor == "" || info.IsDir() || filepath.Ext(dest) != ".md" {
				continue
			}
			text, err := os.ReadFile(dest)
			require.NoError(t, err)
			assert.True(t, anchorsOf(string(text))[anchor], "%s links to %s: no such heading", path, target)
		}
		return nil
	})
	require.NoError(t, err)
}
