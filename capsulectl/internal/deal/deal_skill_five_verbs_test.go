package deal

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// The expert commands, re-derived from the two skills' text before the merge
// (and the x-deal-v0 deal verbs): SKILL.md names none of them. Words may be
// split across a line break, so any run of white space separates them.
var expertCommands = map[string]*regexp.Regexp{}

func init() {
	for _, name := range []string{
		"deal init", "deal set-version", "deal tick", "deal audit-host", "deal settlement",
		"deal window", "deal sale", "deal open", "deal close", "plugin install",
		"cll checkpoint cadence", "book close", "book result derived-inputs", "book window",
	} {
		expertCommands[name] = regexp.MustCompile(`\b` + strings.Join(strings.Fields(name), `\s+`) + `\b`)
	}
}

func namesExpertCommand(text string) []string {
	var found []string
	for name, re := range expertCommands {
		if re.MatchString(text) {
			found = append(found, name)
		}
	}
	return found
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
	assert.Contains(t, skillDescription(t, skill), "`capsulectl book`")
}

// skillDescription is the frontmatter's description line.
func skillDescription(t *testing.T, skill string) string {
	t.Helper()
	body, ok := strings.CutPrefix(skill, "---\n")
	require.True(t, ok, "SKILL.md starts with frontmatter")
	front, _, ok := strings.Cut(body, "\n---\n")
	require.True(t, ok, "the frontmatter ends")
	for _, line := range strings.Split(front, "\n") {
		if d, ok := strings.CutPrefix(line, "description: "); ok {
			return d
		}
	}
	require.Fail(t, "the frontmatter has no description")
	return ""
}

func TestSkillDescriptionReadsOnlyTheFrontmatter(t *testing.T) {
	skill := "---\nname: deal\ndescription: deals only\n---\n\n# deal\n\nrun `capsulectl book` here\n"
	assert.Equal(t, "deals only", skillDescription(t, skill))
}

func TestDealSkillNamesNoExpertCommand(t *testing.T) {
	assert.Empty(t, namesExpertCommand(readSkillText(t, "SKILL.md")), "SKILL.md names an expert command")
	// Nor do the procedure files it points to: they moved out of SKILL.md
	// (evidencebook#1025) and teach the same five verbs.
	for _, file := range dealProcedureFiles() {
		assert.Empty(t, namesExpertCommand(readSkillText(t, file)), "%s names an expert command", file)
	}
}

func TestExpertCommandPatternsCatchWrappedAndLegacyCommands(t *testing.T) {
	assert.Equal(t, []string{"deal tick"}, namesExpertCommand("run `deal\n  tick` first"))
	assert.Equal(t, []string{"deal open"}, namesExpertCommand("`deal open --input x`"))
	assert.Empty(t, namesExpertCommand("`deal offer`, `deal settle`, `deal opener`"))
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

// The setup rules stay in SKILL.md without the commands: the setup reference
// is read before the first deal, and the three steps the user's alone stay
// never the agent's.
func TestDealSkillKeepsTheSetupRules(t *testing.T) {
	body := strings.Join(strings.Fields(section(t, readSkillText(t, "SKILL.md"), "Setup (once)")), " ")
	for _, rule := range []string{
		"Before the first deal on a profile, read [references/setup.md](references/setup.md) and follow it",
		"Three setup steps are the user's, never yours: installing a plugin, moving a store to a new version, and setting or changing the materiality predicate",
		"stop and show the user its message as it is; never run the command it names yourself.",
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

// The x-deal-v1 procedures live in reference files, and SKILL.md keeps one
// pointer line per procedure saying when to open which (evidencebook#1025).
// Each value is the link the pointer gives, relative to the skill folder.
var dealProcedures = map[string]string{
	"Purchase":             "references/purchase.md#purchase",
	"One errand, one deal": "references/purchase.md#one-errand-one-deal",
	"Booking":              "references/purchase.md#booking",
	"Signature":            "references/purchase.md#signature",
	`What "settled" means, and the person's signature`: "references/purchase.md#what-settled-means-and-the-persons-signature",
	"Disclosure": "references/disclosure.md",
	"Sale":       "references/sale.md#sale",
	"Where the user sells: payment received, and what was delivered": "references/sale.md#where-the-user-sells-payment-received-and-what-was-delivered",
	"Cancellation and refund":     "references/cancellation.md",
	"A dispute":                   "references/dispute.md",
	"Evidence that arrives later": "references/evidence.md",
}

// pointerLabel is a procedure's short name in its pointer line, where the
// heading is longer.
var pointerLabel = map[string]string{
	`What "settled" means, and the person's signature`:               "What \"settled\" means",
	"Where the user sells: payment received, and what was delivered": "Where the user sells",
}

// dealProcedureFiles are the reference files that hold the procedures.
func dealProcedureFiles() []string {
	seen := map[string]bool{}
	var files []string
	for _, target := range dealProcedures {
		file, _, _ := strings.Cut(target, "#")
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}
	slices.Sort(files)
	return files
}

// Rules and Points of no return carry the payment safety floor, and an agent
// cannot be relied on to go and read another file for them (Ethan,
// evidencebook#1025): they stay in SKILL.md in full, and no reference file
// carries a second copy.
func TestDealSkillKeepsRulesAndPointsOfNoReturnInSkillMD(t *testing.T) {
	skill := readSkillText(t, "SKILL.md")
	rules := strings.Join(strings.Fields(section(t, skill, "Rules (never break these)")), " ")
	for _, rule := range []string{
		"**Fixed order:** snapshot → seal → diff → show → approval → seal approval → act.",
		"`deal settle --sent`, and the payment goes to the rail only once that succeeded.",
		"**Never act on an unsealed approval.** Act only when `deal check` returned `\"proceed\": true`, or `deal note --kind approval` returned `\"proceed\": true`.",
		"**On an error, hold.** If any `capsulectl` command exits non-zero, do not act.",
		"**Note every telling, mechanically.**",
		"a first telling no approved check named is held there.",
		"**Never hold silently.** A `pause` always goes to the user with the check's text.",
		"**Show the check's own card, once.**",
		"**A pass the user acts on shows its text.** When `deal check` passes and the final button (\"Place order\", \"Cancel order\" or any other) is the user's to press",
		"**Options are buttons, never text.**",
		"**The text is evidence.**",
		"**\"Next\" is a promise, and it is kept.**",
		"**Never say the rules ran when they did not.**",
		"**This upgrades your host's own confirmation; it never replaces it or bypasses it.**",
	} {
		assert.Contains(t, rules, rule)
	}
	points := strings.Join(strings.Fields(section(t, skill, "Points of no return")), " ")
	for _, rule := range []string{
		"| `purchase` | `pay`, `commit`, `cancel`, `share_contact`, `share_credentials` |",
		"| any deal where the user sells | `offer`, `represent` |",
		"`pay` is the payment the user makes, `commit` an act such as a delivery, a booking or a signature (`deal perform` rests on it), and `cancel` a cancellation after agreeing (a refund rests on it).",
		"`deal check` refuses any other action name.",
	} {
		assert.Contains(t, points, rule)
	}
	// The sections come before the setup and the reference material, as they
	// did before the procedures moved.
	assert.Less(t, strings.Index(skill, "\n## Rules (never break these)\n"), strings.Index(skill, "\n## Setup (once)\n"))
	assert.Less(t, strings.Index(skill, "\n## Points of no return\n"), strings.Index(skill, "\n## Setup (once)\n"))

	err := filepath.WalkDir(dealSkillDir, func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() || filepath.Ext(path) != ".md" || filepath.Base(path) == "SKILL.md" && filepath.Dir(path) == filepath.Clean(dealSkillDir) {
			return nil
		}
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		text := strings.Join(strings.Fields(string(raw)), " ")
		for _, copied := range []string{"Rules (never break these)", "Never act on an unsealed approval.", "snapshot → seal → diff → show → approval"} {
			assert.NotContains(t, text, copied, "%s carries a copy of SKILL.md's Rules", path)
		}
		assert.NotContains(t, string(raw), "\n## Points of no return\n", "%s carries a copy of SKILL.md's Points of no return", path)
		return nil
	})
	require.NoError(t, err)
}

// SKILL.md has one pointer line per procedure, naming the procedure and
// linking the file that holds it; each procedure's heading is in exactly one
// of SKILL.md and the procedure files, never in two (the x-deal-v0 forms in
// LEGACY-X-DEAL-V0.md are a separate version and not counted).
func TestDealSkillPointsToEachProcedureOnce(t *testing.T) {
	procs := section(t, readSkillText(t, "SKILL.md"), "Procedures")
	assert.Contains(t, strings.Join(strings.Fields(procs), " "), "**Before the first step of a scenario, open its file and follow it step by step**")
	var pointers []string
	for _, line := range strings.Split(procs, "\n") {
		if strings.HasPrefix(line, "- **") {
			pointers = append(pointers, line)
		}
	}
	// One line for each procedure, and one for the older deals.
	assert.Len(t, pointers, len(dealProcedures)+1)
	flat := strings.Join(strings.Fields(procs), " ")
	for name, target := range dealProcedures {
		label := pointerLabel[name]
		if label == "" {
			label = name
		}
		re := regexp.MustCompile(`- \*\*` + regexp.QuoteMeta(label) + `\*\*: [^*]*?\]\(` + regexp.QuoteMeta(target) + `\)`)
		assert.Regexp(t, re, flat, "SKILL.md points %q to %s", name, target)
	}
	assert.Contains(t, flat, "- **Older deals**: an x-deal-v0 deal already open keeps its version: finish it with the procedures in [LEGACY-X-DEAL-V0.md](LEGACY-X-DEAL-V0.md).")

	headings := map[string][]string{}
	for _, file := range append([]string{"SKILL.md"}, dealProcedureFiles()...) {
		for _, line := range strings.Split(readSkillText(t, file), "\n") {
			if h, ok := strings.CutPrefix(strings.TrimLeft(line, "#"), " "); ok && strings.HasPrefix(line, "#") {
				headings[h] = append(headings[h], file)
			}
		}
	}
	for name := range dealProcedures {
		assert.Len(t, headings[name], 1, "the heading %q is in %v", name, headings[name])
		if len(headings[name]) == 1 {
			assert.NotEqual(t, "SKILL.md", headings[name][0], "%q stays in SKILL.md", name)
		}
	}
	// Each procedure file says the SKILL.md Rules and Points of no return
	// bind its steps.
	for _, file := range dealProcedureFiles() {
		head := strings.Join(strings.Fields(strings.SplitN(readSkillText(t, file), "\n\n", 3)[1]), " ")
		assert.Contains(t, head, "[Rules](../SKILL.md#rules-never-break-these) and [Points of no return](../SKILL.md#points-of-no-return) bind every step here", file)
	}
}

// The skill's text as the agent may read it: SKILL.md, then the procedure
// files it points to, flattened.
func dealSkillProcedureText(t *testing.T) string {
	t.Helper()
	text := readSkillText(t, "SKILL.md")
	for _, file := range dealProcedureFiles() {
		text += "\n" + readSkillText(t, file)
	}
	return text
}
