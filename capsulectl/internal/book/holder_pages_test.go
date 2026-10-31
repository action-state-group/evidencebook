package book

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const holderGoldenDir = "testdata/holder-pages"

// The values Steven's canvas draws as examples. None is data: a page that
// shows one has invented it.
var canvasExamples = []string{"Dana R.", "Default v1", "3 months", "3 a week", "12 months", "bike buyer", "answer within 3 days", "Action State default"}

func holderPageOut(t *testing.T, extra ...string) (string, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "page.html")
	stdout, err := invoke(t, "", append([]string{"holder-page", "--out", out}, extra...)...)
	require.NoError(t, err, stdout)
	return string(mustRead(t, out)), stdout
}

func holderGolden(t *testing.T, name, page string) {
	t.Helper()
	path := filepath.Join(holderGoldenDir, name+".html")
	if os.Getenv("CAPSULECTL_UPDATE_PRESENTATION_PAGES") == "1" {
		require.NoError(t, os.MkdirAll(holderGoldenDir, 0o755))
		require.NoError(t, os.WriteFile(path, []byte(page), 0o644))
	}
	assert.Equal(t, string(mustRead(t, path)), page, "the committed page is not what `book holder-page` writes now; rewrite it with CAPSULECTL_UPDATE_PRESENTATION_PAGES=1")
}

func TestHolderShareGoldensAndNoInventedValues(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	own, stdout := holderPageOut(t, "--view", "share")
	holderGolden(t, "share", own)
	assert.Contains(t, stdout, `"wording":"evidencebook.holder-pages.en/v0"`)
	shared, _ := holderPageOut(t, "--view", "share", "--shared")
	holderGolden(t, "share-shared", shared)

	for _, page := range []string{own, shared} {
		for _, example := range canvasExamples {
			assert.NotContains(t, page, example)
		}
		// Only the checkpoint answer is in force: every other row says so.
		assert.Equal(t, 8, strings.Count(page, `<span class="pill ">Not available yet</span>`), "every row without a producer says so")
		assert.Equal(t, 1, strings.Count(page, `<span class="pill on">Always</span>`))
		assert.Contains(t, page, "Never, no matter who asks")
	}
	// Voice: you to the holder, the holder when shared; the tell-your-agent
	// block is the holder's alone.
	assert.Contains(t, own, "Your limit on anonymous askers")
	assert.Contains(t, own, "Tell your agent")
	assert.Contains(t, shared, "The holder&#39;s limit on anonymous askers")
	assert.NotContains(t, shared, "Tell your agent")
	assert.NotContains(t, shared, "Your ")
	assert.NotContains(t, shared, " you ")
}

func TestHolderPromptGolden(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	page, _ := holderPageOut(t, "--view", "prompt")
	holderGolden(t, "prompt", page)
	for _, example := range canvasExamples {
		assert.NotContains(t, page, example)
	}
	// Every control the canvas draws is there; all but the report are
	// disabled, and no policy-change block is written.
	assert.Equal(t, 6, strings.Count(page, "<select disabled>"))
	assert.Contains(t, page, `<input type="checkbox" id="rebuild" disabled>`)
	assert.Contains(t, page, `<input type="checkbox" id="report">`)
	assert.NotContains(t, page, "policy-change")
	// html/template reads a data-on* attribute as a script, so the script's
	// strings sit in attributes it leaves as text.
	assert.Contains(t, page, `data-single="1 change"`)
}

func TestHolderAskedListsTheAnsweredRecordsAndNamesNobody(t *testing.T) {
	x := newEvidenceExchange(t)
	empty := filepath.Join(t.TempDir(), "empty.html")
	_, err := invoke(t, "", "holder-page", "--view", "asked", "--profile", "b", "--out", empty)
	require.NoError(t, err)
	holderGolden(t, "asked-empty", string(mustRead(t, empty)))
	assert.Contains(t, string(mustRead(t, empty)), "Nobody has asked about you")
	assert.Contains(t, string(mustRead(t, empty)), "Nothing is waiting. This book cannot yet record")

	x.ask("history", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	x.respond("history")
	x.ask("records", `{"subject":{"full_history":null},"coverage":{"min_freshness":1}}`)
	x.respond("records")

	// What the page may say is what the sealed records say.
	var statements []evidencebook.AnsweredStatement
	withBook(t, x.b, func(book *evidencebook.Book) {
		records, err := book.Query(t.Context(), evidencebook.Filter{RecordType: evidencebook.RecordTypeRequestAnswered})
		require.NoError(t, err)
		for _, r := range records {
			var st evidencebook.AnsweredStatement
			require.NoError(t, json.Unmarshal(r.Header.Statement, &st))
			statements = append(statements, st)
		}
	})
	require.Len(t, statements, 2)

	out := filepath.Join(t.TempDir(), "asked.html")
	stdout, err := invoke(t, "", "holder-page", "--view", "asked", "--profile", "b", "--out", out)
	require.NoError(t, err, stdout)
	assert.Contains(t, stdout, `"answered":2`)
	page := string(mustRead(t, out))
	holderGolden(t, "asked", page)
	assert.Contains(t, page, `<span class="pill">2</span>`)
	assert.Contains(t, page, "<b>proof only</b>")
	assert.Contains(t, page, "Reason: your settings do not allow it")
	// Sentences that promised what nothing produces are not on the page.
	for _, promised := range []string{"small deals only", "shows you here", "witness"} {
		assert.NotContains(t, page, promised)
	}
	assert.Contains(t, page, "Snapshots. No counts.")
	for _, example := range canvasExamples {
		assert.NotContains(t, page, example)
	}
	// The book keeps no requester name and no ask waiting for an answer, so
	// the page draws none.
	assert.Contains(t, page, "Nothing is waiting. This book cannot yet record")
}

func TestHolderAskedAndPromptAreNeverShared(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, view := range []string{"asked", "prompt"} {
		out := filepath.Join(t.TempDir(), "p.html")
		_, err := invoke(t, "", "holder-page", "--view", view, "--shared", "--out", out)
		assert.ErrorIs(t, err, cli.ErrInput, view)
		assert.NoFileExists(t, out)
	}
}

func TestHolderWordingPackIsPinnedAndComplete(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out := func() string { return filepath.Join(t.TempDir(), "p.html") }
	pack, digest := wordingFixture(t, `{"wording_pack_version":"aac.wording-pack/v0","id":"example.host/v0","locale":"en","entries":{"share.title":"Mine"}}`)

	_, err := invoke(t, "", "holder-page", "--out", out(), "--wording", pack)
	assert.ErrorIs(t, err, cli.ErrInput, "a pack without its digest is unpinned")
	_, err = invoke(t, "", "holder-page", "--out", out(), "--wording", pack, "--wording-digest", strings.Repeat("0", 64))
	assert.ErrorIs(t, err, cli.ErrInput, "a digest that is not the pack's")
	o := out()
	_, err = invoke(t, "", "holder-page", "--out", o, "--wording", pack, "--wording-digest", digest)
	assert.ErrorIs(t, err, cli.ErrInput, "a pack that lacks entries the page draws")
	assert.ErrorContains(t, err, "share.anyone.title")
	assert.NoFileExists(t, o)

	// A complete pack changes the words and is escaped: only <b> is markup.
	full := string(holderWordingJSON)
	full = strings.Replace(full, `"id": "evidencebook.holder-pages.en/v0"`, `"id": "example.host/v0"`, 1)
	full = strings.Replace(full, `"What I share"`, `"<script>x</script>Mine <b>bold</b>"`, 1)
	fullPath, fullDigest := wordingFixture(t, full)
	page, stdout := holderPageOut(t, "--wording", fullPath, "--wording-digest", fullDigest)
	assert.Contains(t, stdout, `"wording":"example.host/v0"`)
	assert.Contains(t, page, "&lt;script&gt;x&lt;/script&gt;Mine <b>bold</b>")
	assert.NotContains(t, page, "<script>x")
}

func TestHolderPageRefusesAnExistingOutAndAnUnknownView(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out := filepath.Join(t.TempDir(), "p.html")
	require.NoError(t, os.WriteFile(out, []byte("keep"), 0o600))
	_, err := invoke(t, "", "holder-page", "--out", out)
	assert.ErrorIs(t, err, cli.ErrInput)
	assert.Equal(t, "keep", string(mustRead(t, out)))
	_, err = invoke(t, "", "holder-page", "--view", "other", "--out", filepath.Join(t.TempDir(), "q.html"))
	assert.ErrorIs(t, err, cli.ErrInput)
	_, err = invoke(t, "", "holder-page")
	assert.ErrorIs(t, err, cli.ErrInput)
}

// A record's reason and subject kind are the book's own text, and a page
// built from them must not let them carry markup, nor reach another entry.
func TestHolderAskedEscapesRecordFieldsAndReadsTheCounterparty(t *testing.T) {
	entries, err := decodeHolderWording(holderWordingJSON, false)
	require.NoError(t, err)
	w := holderWording{entries: entries}
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	page, err := buildHolderPage("asked", w, []answeredEntry{
		{At: at, Statement: evidencebook.AnsweredStatement{Outcome: evidencebook.OutcomeRefusal, Reason: "<b>x{kind}<script>", Relationship: evidencebook.RelationshipCounterparty}},
		{At: at.Add(-time.Hour), Statement: evidencebook.AnsweredStatement{Outcome: evidencebook.OutcomeArtifact, SubjectKind: "<i>kind</i>", Relationship: evidencebook.RelationshipIdentified}},
	})
	require.NoError(t, err)
	got := string(page)
	assert.NotContains(t, got, "<script>x")
	assert.NotContains(t, got, "<i>kind")
	assert.Contains(t, got, "Reason: &lt;b&gt;x{kind}&lt;script&gt;")
	assert.Contains(t, got, "They asked for: &lt;i&gt;kind&lt;/i&gt;")
	assert.Equal(t, 1, strings.Count(got, "someone you dealt with"), "a counterparty is named as one")
	assert.Equal(t, 1, strings.Count(got, "an agent that said who it is"))
	assert.Less(t, strings.Index(got, "Oct 29 · someone"), strings.Index(got, "Oct 29 · an agent"), "newest first")
}

// A pack used with --shared must carry the shared strings, or the page given
// to a third party would speak to "you".
func TestHolderSharedNeedsTheSharedEntriesAndNoEmptyValues(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var pack struct {
		Entries map[string]string `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(holderWordingJSON, &pack))
	for key := range pack.Entries {
		if strings.HasSuffix(key, ".shared") {
			delete(pack.Entries, key)
		}
	}
	raw, err := json.Marshal(map[string]any{"wording_pack_version": "aac.wording-pack/v0", "id": "example.host/v0", "locale": "en", "entries": pack.Entries})
	require.NoError(t, err)
	path, digest := wordingFixture(t, string(raw))
	out := filepath.Join(t.TempDir(), "p.html")
	_, err = invoke(t, "", "holder-page", "--out", out, "--wording", path, "--wording-digest", digest)
	require.NoError(t, err, "the holder's own page needs no shared entries")
	_, err = invoke(t, "", "holder-page", "--shared", "--out", filepath.Join(t.TempDir(), "q.html"), "--wording", path, "--wording-digest", digest)
	assert.ErrorIs(t, err, cli.ErrInput)
	assert.ErrorContains(t, err, ".shared")

	pack.Entries["share.title"] = ""
	raw, err = json.Marshal(map[string]any{"wording_pack_version": "aac.wording-pack/v0", "id": "example.host/v0", "locale": "en", "entries": pack.Entries})
	require.NoError(t, err)
	path, digest = wordingFixture(t, string(raw))
	_, err = invoke(t, "", "holder-page", "--out", filepath.Join(t.TempDir(), "r.html"), "--wording", path, "--wording-digest", digest)
	assert.ErrorContains(t, err, "share.title")
}
