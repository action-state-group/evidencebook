// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func builtPage(t *testing.T) *Page {
	t.Helper()
	page, err := Build(mustRead(t, bookFormBundle), View{Card: "outcome"})
	require.NoError(t, err)
	return page
}

func TestSealPageMatchesTheUntouchedPage(t *testing.T) {
	book := newBook(t)
	page := builtPage(t)
	sealed, err := SealPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	assert.NotEmpty(t, sealed.RecordID)
	assert.False(t, sealed.AlreadySealed)
	assert.NotZero(t, sealed.Checkpoint, "the seal is covered by a checkpoint at once")
	assert.Equal(t, 1, sealed.Revision, "the first page over a Result is its revision 1")

	// The record carries the page digest, the bundle digest and the Result
	// root, and nothing else, about the bundle digest.
	record, err := book.Get(t.Context(), sealed.RecordID)
	require.NoError(t, err)
	assert.Equal(t, RecordTypePageSeal, record.Header.RecordType)
	assert.Equal(t, page.BundleDigest, record.Header.SubjectRef)
	assert.Equal(t, evidencebook.ProducerClaim, record.Header.EpistemicType)
	var statement map[string]string
	require.NoError(t, json.Unmarshal(record.Header.Statement, &statement))
	assert.Equal(t, map[string]string{"page_digest": PageDigest(page.HTML), "bundle_digest": page.BundleDigest, "result_root": page.Root}, statement)
	assert.True(t, strings.HasPrefix(PageDigest(page.HTML), "sha256:"))

	check, err := CheckPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	assert.Equal(t, PageMatch, check.Outcome)
	assert.Equal(t, sealed.RecordID, check.RecordID)
	assert.Equal(t, PageDigest(page.HTML), check.PageDigest)
	assert.Equal(t, page.BundleDigest, check.BundleDigest)
	assert.Equal(t, "VALID", check.Verdict)
	assert.NoError(t, check.Refusal)

	// A repeat of the same page seals no second record and cuts no
	// checkpoint.
	again, err := SealPage(t.Context(), book, builtPage(t).HTML)
	require.NoError(t, err)
	assert.Equal(t, Seal{RecordID: sealed.RecordID, Seq: sealed.Seq, AlreadySealed: true, Revision: 1}, again)
}

// A page that changes only its presentation embeds the same bundle, so the
// same Result: sealing it bumps the revision and the Result root stays. A
// repeat of an earlier page keeps that page's revision. Seals over another
// Result, and seals whose statement does not decode, are not revisions of
// this Result and do not stop its seal; a seal over the same Result from
// another bundle is one.
func TestSealPageNumbersRevisionsOverOneResult(t *testing.T) {
	book := newBook(t)
	first := builtPage(t)
	retitled, err := Build(mustRead(t, bookFormBundle), View{Card: "outcome", Presentation: &Presentation{Title: "Week 38"}})
	require.NoError(t, err)
	require.NotEqual(t, PageDigest(first.HTML), PageDigest(retitled.HTML))
	require.Equal(t, first.Root, retitled.Root, "a presentation-only change keeps the Result")
	seal := func(st any) {
		t.Helper()
		body, err := json.Marshal(st)
		require.NoError(t, err)
		_, err = book.Append(t.Context(), evidencebook.Entry{RecordType: RecordTypePageSeal, EpistemicType: evidencebook.ProducerClaim, SubjectRef: "sha256:" + strings.Repeat("1", 64), Statement: body})
		require.NoError(t, err)
	}

	one, err := SealPage(t.Context(), book, first.HTML)
	require.NoError(t, err)
	assert.Equal(t, 1, one.Revision)
	// Between the two: a seal over another Result, and one that states
	// nothing a seal states.
	seal(pageSealStatement{PageDigest: "sha256:" + strings.Repeat("0", 64), BundleDigest: "sha256:" + strings.Repeat("1", 64), ResultRoot: strings.Repeat("2", 64)})
	seal([]string{"not", "a", "seal", "statement"})
	two, err := SealPage(t.Context(), book, retitled.HTML)
	require.NoError(t, err)
	assert.Equal(t, 2, two.Revision)
	assert.NotEqual(t, one.RecordID, two.RecordID)
	again, err := SealPage(t.Context(), book, first.HTML)
	require.NoError(t, err)
	assert.True(t, again.AlreadySealed)
	assert.Equal(t, 1, again.Revision, "a repeat keeps its revision")

	// A page of the same Result embedded in another bundle is its next
	// revision: the revision counts per Result, not per bundle.
	seal(pageSealStatement{PageDigest: "sha256:" + strings.Repeat("3", 64), BundleDigest: "sha256:" + strings.Repeat("4", 64), ResultRoot: first.Root})
	records, err := book.Query(t.Context(), evidencebook.Filter{RecordType: RecordTypePageSeal})
	require.NoError(t, err)
	n, err := revision(t.Context(), book, records[len(records)-1].RecordID, first.Root)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	_, err = revision(t.Context(), book, one.RecordID, strings.Repeat("2", 64))
	assert.ErrorContains(t, err, "is not among the book's page seals over Result")
}

func TestCheckPageMismatchesAChangedPageWhileTheEvidenceStillVerifies(t *testing.T) {
	book := newBook(t)
	page := builtPage(t)
	_, err := SealPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	for name, edit := range map[string][2]string{
		"shell text": {"<!doctype html>", "<!DOCTYPE html>"},
		"page code":  {"\"use strict\";", "\"use strict\":"},
	} {
		html := string(page.HTML)
		require.Contains(t, html, edit[0], name)
		check, err := CheckPage(t.Context(), book, []byte(strings.Replace(html, edit[0], edit[1], 1)))
		require.NoError(t, err, name)
		assert.Equal(t, PageMismatch, check.Outcome, name)
		assert.Empty(t, check.RecordID, name)
		assert.Equal(t, "VALID", check.Verdict, name+": the embedded evidence is untouched")
		assert.NoError(t, check.Refusal, name)
	}
}

// A page whose embedded record signature was damaged embeds a different,
// INVALID bundle: no seal for it, and the evidence is what fails.
func TestCheckPageJudgesTheEmbeddedEvidenceApartFromTheSeal(t *testing.T) {
	book := newBook(t)
	page := builtPage(t)
	_, err := SealPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	embedded, err := embeddedBundle(page.HTML)
	require.NoError(t, err)
	sig := embedded["records"].([]interface{})[0].(map[string]interface{})["signature"].(string)
	flipped := sig[:len(sig)-1] + map[bool]string{true: "1", false: "0"}[sig[len(sig)-1] == '0']
	check, err := CheckPage(t.Context(), book, []byte(strings.Replace(string(page.HTML), sig, flipped, 1)))
	require.NoError(t, err)
	assert.Equal(t, "INVALID", check.Verdict)
	require.ErrorIs(t, check.Refusal, cli.ErrBundleInvalid)
	assert.Equal(t, PageNoSeal, check.Outcome)

	// Nor is such a page sealed.
	before, err := book.Query(t.Context(), evidencebook.Filter{RecordType: RecordTypePageSeal})
	require.NoError(t, err)
	_, err = SealPage(t.Context(), book, []byte(strings.Replace(string(page.HTML), sig, flipped, 1)))
	require.ErrorIs(t, err, ErrRefused)
	require.ErrorIs(t, err, cli.ErrBundleInvalid)
	after, err := book.Query(t.Context(), evidencebook.Filter{RecordType: RecordTypePageSeal})
	require.NoError(t, err)
	assert.Len(t, after, len(before))
}

// What a page seal states is read from the page's bytes, never from a Page
// a caller may have changed.
func TestSealPageStatesWhatThePageEmbeds(t *testing.T) {
	book := newBook(t)
	page := builtPage(t)
	page.BundleDigest, page.Root = "sha256:not-this-bundle", "not-this-root"
	sealed, err := SealPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	record, err := book.Get(t.Context(), sealed.RecordID)
	require.NoError(t, err)
	fresh := builtPage(t)
	assert.Equal(t, fresh.BundleDigest, record.Header.SubjectRef)
	assert.Contains(t, string(record.Header.Statement), fresh.Root)
	check, err := CheckPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	assert.Equal(t, PageMatch, check.Outcome)
	_, err = SealPage(t.Context(), book, mustRead(t, bookFormBundle))
	require.ErrorIs(t, err, ErrRefused, "a file that is not a page is refused")
}

// InspectPage gives every answer that needs no book.
func TestInspectPageNeedsNoBook(t *testing.T) {
	page := builtPage(t)
	inspected, err := InspectPage(page.HTML)
	require.NoError(t, err)
	assert.Equal(t, PageDigest(page.HTML), inspected.PageDigest)
	assert.Equal(t, page.BundleDigest, inspected.BundleDigest)
	assert.Equal(t, "VALID", inspected.Verdict)
	assert.NoError(t, inspected.Refusal)
	assert.Empty(t, inspected.Outcome)
	_, err = InspectPage([]byte("<!doctype html><p>hello</p>"))
	require.ErrorIs(t, err, ErrRefused)
}

func TestCheckPageWithNoSealedRecord(t *testing.T) {
	check, err := CheckPage(t.Context(), newBook(t), builtPage(t).HTML)
	require.NoError(t, err)
	assert.Equal(t, PageNoSeal, check.Outcome)
	assert.Equal(t, "VALID", check.Verdict)
}

// The seal as an interrupted SealPage leaves it: appended, not yet
// checkpointed. Sealing again cuts the checkpoint.
func TestCheckPageWithASealNoCheckpointCoversYet(t *testing.T) {
	book := newBook(t)
	page := builtPage(t)
	body, err := json.Marshal(pageSealStatement{PageDigest: PageDigest(page.HTML), BundleDigest: page.BundleDigest, ResultRoot: page.Root})
	require.NoError(t, err)
	record, err := book.Append(t.Context(), evidencebook.Entry{RecordType: RecordTypePageSeal, EpistemicType: evidencebook.ProducerClaim, SubjectRef: page.BundleDigest, Statement: body})
	require.NoError(t, err)
	check, err := CheckPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	assert.Equal(t, PageNotCovered, check.Outcome)
	assert.Equal(t, record.RecordID, check.RecordID)

	sealed, err := SealPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	assert.True(t, sealed.AlreadySealed)
	assert.NotZero(t, sealed.Checkpoint, "the repeat cuts the checkpoint the first run did not")
	check, err = CheckPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	assert.Equal(t, PageMatch, check.Outcome)
}

// A seal record whose statement does not decode is reported, not skipped.
func TestCheckPageRefusesAnUndecodableSeal(t *testing.T) {
	book := newBook(t)
	page := builtPage(t)
	_, err := book.Append(t.Context(), evidencebook.Entry{RecordType: RecordTypePageSeal, EpistemicType: evidencebook.ProducerClaim, SubjectRef: page.BundleDigest, Statement: json.RawMessage(`["not a statement"]`)})
	require.NoError(t, err)
	_, err = CheckPage(t.Context(), book, page.HTML)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whose statement does not decode")
	_, err = SealPage(t.Context(), book, page.HTML)
	require.Error(t, err)
}

func TestCheckPageRefusesAFileThatIsNotAReportPage(t *testing.T) {
	book := newBook(t)
	html := string(builtPage(t).HTML)
	for name, file := range map[string]string{
		"a bundle":               string(mustRead(t, bookFormBundle)),
		"html with no bundle":    "<!doctype html><p>hello</p>",
		"an unterminated bundle": html[:jsonElementStart(t, html)+1],
		"an undecodable bundle":  html[:jsonElementStart(t, html)] + "{" + html[jsonElementStart(t, html):],
	} {
		_, err := CheckPage(t.Context(), book, []byte(file))
		require.ErrorIs(t, err, ErrRefused, name)
	}
}

// jsonElementStart is the index of the "{" that opens the bundle in a page's
// JSON data element.
func jsonElementStart(t *testing.T, html string) int {
	t.Helper()
	m := bundleElement.FindStringIndex(html)
	require.NotNil(t, m, "the page carries its bundle in a JSON element")
	return m[1] + strings.Index(html[m[1]:], "{")
}

// A page sealed before the bundle moved into a JSON element carried it after
// `window.__BUNDLE__ = `; its seal must still check.
func TestInspectPageReadsAPageSealedBeforeTheJSONElement(t *testing.T) {
	html := string(builtPage(t).HTML)
	m := bundleElement.FindStringIndex(html)
	require.NotNil(t, m)
	end := m[1] + strings.Index(html[m[1]:], "</script>")
	old := html[:m[0]] + "<script>window.__BUNDLE__ = " + strings.TrimSpace(html[m[1]:end]) + ";</script>" + html[end+len("</script>"):]
	require.NotContains(t, old, "application/json")
	want, err := InspectPage([]byte(html))
	require.NoError(t, err)
	got, err := InspectPage([]byte(old))
	require.NoError(t, err)
	assert.Equal(t, want.BundleDigest, got.BundleDigest)
}

// The bundle is data in a JSON element, never code: a member named
// "__proto__" stays a member of the decoded bundle.
func TestEmbeddedBundleKeepsAProtoMember(t *testing.T) {
	page := []byte(`<!doctype html><script type="application/json">{"__proto__":{"x":1},"root":"r"}</script>`)
	value, err := embeddedBundle(page)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"x": json.Number("1")}, value["__proto__"])
	assert.Equal(t, "r", value["root"])
}
