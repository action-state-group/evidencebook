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
// repeat of an earlier page keeps that page's revision, and a seal over
// another Result counts its own revisions.
func TestSealPageNumbersRevisionsOverOneResult(t *testing.T) {
	book := newBook(t)
	first := builtPage(t)
	retitled, err := Build(mustRead(t, bookFormBundle), View{Card: "outcome", Presentation: &Presentation{Title: "Week 38"}})
	require.NoError(t, err)
	require.NotEqual(t, PageDigest(first.HTML), PageDigest(retitled.HTML))
	require.Equal(t, first.Root, retitled.Root, "a presentation-only change keeps the Result")

	one, err := SealPage(t.Context(), book, first.HTML)
	require.NoError(t, err)
	two, err := SealPage(t.Context(), book, retitled.HTML)
	require.NoError(t, err)
	assert.Equal(t, 1, one.Revision)
	assert.Equal(t, 2, two.Revision)
	assert.NotEqual(t, one.RecordID, two.RecordID)
	again, err := SealPage(t.Context(), book, first.HTML)
	require.NoError(t, err)
	assert.True(t, again.AlreadySealed)
	assert.Equal(t, 1, again.Revision, "a repeat keeps its revision")

	// A seal over another Result root is not one of this Result's
	// revisions.
	other := pageSealStatement{PageDigest: "sha256:" + strings.Repeat("0", 64), BundleDigest: "sha256:" + strings.Repeat("1", 64), ResultRoot: strings.Repeat("2", 64)}
	body, err := json.Marshal(other)
	require.NoError(t, err)
	_, err = book.Append(t.Context(), evidencebook.Entry{RecordType: RecordTypePageSeal, EpistemicType: evidencebook.ProducerClaim, SubjectRef: other.BundleDigest, Statement: body})
	require.NoError(t, err)
	n, err := revision(t.Context(), book, two.RecordID, first.Root)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	_, err = revision(t.Context(), book, one.RecordID, other.ResultRoot)
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
		"an unterminated bundle": html[:strings.Index(html, "window.__BUNDLE__ = ")+len("window.__BUNDLE__ = {")],
		"an undecodable bundle":  strings.Replace(html, "window.__BUNDLE__ = {", "window.__BUNDLE__ = {{", 1),
	} {
		_, err := CheckPage(t.Context(), book, []byte(file))
		require.ErrorIs(t, err, ErrRefused, name)
	}
}
