// SPDX-License-Identifier: Apache-2.0

package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Build writes each committed fixture page byte for byte, so the browser
// checks that read the committed page measure what Build writes. A page from
// an older viewer fails here; EVIDENCEBOOK_UPDATE_PRESENTATION_PAGES=1
// rewrites it.
func TestBuildWritesTheCommittedPages(t *testing.T) {
	entries, err := os.ReadDir(presentationDir)
	require.NoError(t, err)
	seen := 0
	for _, entry := range entries {
		dir := filepath.Join(presentationDir, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if !entry.IsDir() || err != nil {
			continue
		}
		var meta struct {
			Rerender string `json:"rerender"`
			Card     string `json:"card"`
		}
		require.NoError(t, json.Unmarshal(raw, &meta))
		if meta.Rerender != "report-build" {
			continue
		}
		seen++
		t.Run(entry.Name(), func(t *testing.T) {
			page, err := Build(mustRead(t, filepath.Join(dir, "bundle.json")), View{Card: meta.Card})
			require.NoError(t, err, "the page gate must let this fixture's page through")
			assert.Equal(t, "VALID", page.Verdict)
			assert.Empty(t, page.UnmetClaims)
			pageFile := filepath.Join(dir, "page.html")
			if os.Getenv("EVIDENCEBOOK_UPDATE_PRESENTATION_PAGES") == "1" {
				require.NoError(t, os.WriteFile(pageFile, page.HTML, 0o644))
				return
			}
			require.Equal(t, string(mustRead(t, pageFile)), string(page.HTML), "the committed page is not what Build writes now; rewrite it with EVIDENCEBOOK_UPDATE_PRESENTATION_PAGES=1")
		})
	}
	require.Positive(t, seen, "no report-build fixture found")
}

// The book form: the Result is the statement of a disclosed evidence_result
// header, checked by the book's own verifier too.
func TestBuildRendersABookFormResultRoot(t *testing.T) {
	raw := mustRead(t, bookFormBundle)
	before := string(raw)
	page, err := Build(raw, View{Card: "obligation", Presentation: &Presentation{ProducerDisplayName: "Demo Co", Title: "Week 38"}})
	require.NoError(t, err)
	assert.Equal(t, before, string(raw), "Build does not touch its input")
	assert.Equal(t, "VALID", page.Verdict)
	assert.Equal(t, "pass", page.Verification)
	assert.Equal(t, "book", page.Form)
	assert.Equal(t, "agent_input", page.Member)
	assert.Equal(t, decode(t, raw)["root"], page.Root)
	assert.Equal(t, 2, page.Claims)
	assert.Equal(t, 0, page.UnsupportedClaims)
	assert.Equal(t, []string{"ec:airline-week:2026-09-14@1"}, page.ContractRefs)

	html := string(page.HTML)
	assert.Contains(t, html, `renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"))`)
	assert.Contains(t, html, "globalThis.renderEvidenceGraph", "the runtime is inline")
	assert.NotContains(t, html, "http://")
	assert.NotContains(t, html, "https://", "the page loads nothing from the network")
	embedded, err := embeddedBundle(page.HTML)
	require.NoError(t, err)
	digest, err := aacbundle.BundleDigest(embedded)
	require.NoError(t, err)
	assert.Equal(t, digest, page.BundleDigest)
	extensions, _ := embedded["extensions"].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"card": "obligation"}, extensions[cli.CardExtension])
	assert.Equal(t, map[string]interface{}{"producer_display_name": "Demo Co", "title": "Week 38"}, extensions[cli.PresentationExtension])
	_, hasPayloads := extensions["evidencebook/payloads"]
	assert.True(t, hasPayloads, "the book's own extensions are kept")

	// The permalink carries the same bytes the page embeds.
	link, err := page.Permalink("")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(link, cli.DefaultBundleURL+"#"))
	decoded, err := aacbundle.DecodeFragment(strings.TrimPrefix(link, cli.DefaultBundleURL+"#"))
	require.NoError(t, err)
	assert.Equal(t, jcs(t, embedded), jcs(t, decoded))
	other, err := page.Permalink("https://viewer.example/bundle#")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(other, "https://viewer.example/bundle#"), "one # between base and fragment")

	// A bundle may already state the card and header, but not other ones.
	withCard := edited(t, raw, func(value map[string]interface{}) {
		value["extensions"].(map[string]interface{})[cli.CardExtension] = map[string]interface{}{"card": "outcome"}
	})
	_, err = Build(withCard, View{Card: "process"})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "already carries a different report-card/v1 block")
	_, err = Build(withCard, View{Card: "outcome"})
	require.NoError(t, err)
}

func jcs(t *testing.T, value interface{}) string {
	t.Helper()
	encoded, err := canonical.JCS(value)
	require.NoError(t, err)
	return string(encoded)
}

func TestBuildMarksADraft(t *testing.T) {
	raw := mustRead(t, bookFormBundle)
	draft, err := Build(raw, View{Card: "human_role", Draft: true})
	require.NoError(t, err)
	assert.True(t, draft.Draft)
	assert.Equal(t, "VALID", draft.Verdict)
	embedded, err := embeddedBundle(draft.HTML)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"card": "human_role", "draft": true}, embedded["extensions"].(map[string]interface{})[cli.CardExtension])
	_, err = draft.Permalink("")
	require.ErrorIs(t, err, ErrRefused)
	_, err = SealPage(t.Context(), newBook(t), draft.HTML)
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "a draft page is never sealed")

	stamped, err := Build(raw, View{Card: "human_role"})
	require.NoError(t, err)
	assert.NotEqual(t, stamped.BundleDigest, draft.BundleDigest, "a draft page never has a stamped page's digest")
}

// A payload-form bundle with no checkpoint signature and no producer
// signatures verifies but is INCOMPLETE: Build reports what it found, renders
// nothing, and returns the page gate's refusal with capsule-cli's class.
func TestBuildRendersNothingForAnIncompleteBundle(t *testing.T) {
	page, err := Build(mustRead(t, incompleteBundle), View{Card: "outcome"})
	require.ErrorIs(t, err, cli.ErrPartial)
	assert.NotErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "no page written")
	require.NotNil(t, page)
	assert.Nil(t, page.HTML)
	assert.Equal(t, "INCOMPLETE", page.Verdict)
	assert.Equal(t, []string{"checkpoint withheld", "producer_signatures withheld"}, page.UnmetClaims)
	assert.Equal(t, "payload", page.Form)
	assert.Equal(t, "agent_input", page.Member)
	assert.Equal(t, 3, page.Claims)
	assert.Equal(t, []string{"ec:airline-week:2026-09-14@1"}, page.ContractRefs)
	_, err = page.Permalink("")
	require.ErrorIs(t, err, ErrRefused)
	_, err = SealPage(t.Context(), newBook(t), page.HTML)
	require.ErrorIs(t, err, ErrRefused)
}

// A record whose signature does not verify is INVALID: no page either.
func TestBuildRendersNothingForAnInvalidBundle(t *testing.T) {
	tampered := edited(t, mustRead(t, bookFormBundle), func(value map[string]interface{}) {
		record := value["records"].([]interface{})[0].(map[string]interface{})
		sig := []byte(record["signature"].(string))
		if sig[len(sig)-1] == '0' {
			sig[len(sig)-1] = '1'
		} else {
			sig[len(sig)-1] = '0'
		}
		record["signature"] = string(sig)
	})
	page, err := Build(tampered, View{Card: "obligation"})
	require.ErrorIs(t, err, cli.ErrBundleInvalid)
	require.NotNil(t, page)
	assert.Nil(t, page.HTML)
	assert.Equal(t, "INVALID", page.Verdict)
	assert.Equal(t, []string{"producer_signatures fail"}, page.UnmetClaims)
}

func TestBuildRefusesATamperedBundle(t *testing.T) {
	raw := mustRead(t, bookFormBundle)
	root := decode(t, raw)["root"].(string)
	cases := map[string]func(map[string]interface{}){
		"a verdict edited inside the disclosed Result": func(v map[string]interface{}) {
			header := v["disclosures"].(map[string]interface{})[root].(map[string]interface{})["agent_input"].(map[string]interface{})
			header["statement"].(map[string]interface{})["claims"].([]interface{})[0].(map[string]interface{})["verdict"] = "not_met"
		},
		"the checkpoint size edited": func(v map[string]interface{}) {
			v["checkpoint"].(map[string]interface{})["mmr_size"] = json.Number("99")
		},
		"a record dropped": func(v map[string]interface{}) {
			records := v["records"].([]interface{})
			v["records"] = records[1:]
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			page, err := Build(edited(t, raw, mutate), View{Card: "outcome"})
			require.ErrorIs(t, err, ErrRefused)
			assert.Nil(t, page)
			assert.Contains(t, err.Error(), "does not verify")
			assert.Contains(t, err.Error(), "nothing was rendered")
		})
	}
}

func TestBuildRefusesABadView(t *testing.T) {
	raw := mustRead(t, bookFormBundle)
	for _, card := range []string{"vibes", ""} {
		_, err := Build(raw, View{Card: card})
		require.ErrorIs(t, err, ErrRefused)
		assert.Contains(t, err.Error(), `card must be one of attribution, human_role, obligation, outcome, process, quality, settlement (the contract's profile set); got "`+card+`"`)
	}
	_, err := Build(raw, View{Card: "outcome", Presentation: &Presentation{}})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "names none of")
	_, err = Build(raw, View{Card: "outcome", Presentation: &Presentation{LogoDataURL: "https://example.invalid/logo.png"}})
	require.ErrorIs(t, err, ErrRefused)
	_, err = Build(raw, View{Card: "outcome", Presentation: &Presentation{LogoDataURL: "data:image/png;base64,AAAA"}})
	require.NoError(t, err)
	_, err = Build([]byte("not json"), View{Card: "outcome"})
	require.ErrorIs(t, err, ErrRefused, "a bundle that does not decode is refused")
	require.ErrorIs(t, err, cli.ErrInput, "and keeps the decoder's class")
}

func TestDecodePresentation(t *testing.T) {
	block, err := DecodePresentation([]byte(`{"producer_display_name":"Demo Co","logo_data_url":"data:image/png;base64,AAAA"}`))
	require.NoError(t, err)
	assert.Equal(t, Presentation{ProducerDisplayName: "Demo Co", LogoDataURL: "data:image/png;base64,AAAA"}, block)
	cases := map[string]struct{ raw, want string }{
		"an extra member":   {`{"title":"x","badge":"verified"}`, "only producer_display_name, logo_data_url and title"},
		"a remote logo":     {`{"logo_data_url":"https://example.invalid/logo.png"}`, "must be an inline data:image/ URL"},
		"a data: non-image": {`{"logo_data_url":"data:text/html,hi"}`, "must be an inline data:image/ URL"},
		"nothing named":     {`{}`, "names none of"},
		"a non-string":      {`{"title":7}`, "only producer_display_name, logo_data_url and title"},
		"two values":        {`{"title":"x"} {"title":"y"}`, "one JSON value"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodePresentation([]byte(tc.raw))
			require.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// The card set is the contract schema's own profile set, nothing invented.
func TestCardsAreTheContractProfiles(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/action-state-group/capsule-cli").Output()
	require.NoError(t, err)
	raw := mustRead(t, filepath.Join(strings.TrimSpace(string(out)), "cli", "testdata", "contract", "evidence-contract-v0.json"))
	var schema any
	require.NoError(t, json.Unmarshal(raw, &schema))
	assert.Equal(t, cli.DiscriminatorValues(schema, "profile"), Cards())
	got := Cards()
	got[0] = "changed"
	assert.Equal(t, "attribution", Cards()[0], "Cards returns a copy")
}

func TestResultRootOfNamesWhatItFound(t *testing.T) {
	root := strings.Repeat("1", 64)
	bundle := func(members map[string]interface{}) map[string]interface{} {
		value := map[string]interface{}{"root": root, "records": []interface{}{map[string]interface{}{"capsule_id": root}}, "disclosures": map[string]interface{}{}}
		if members != nil {
			value["disclosures"].(map[string]interface{})[root] = members
		}
		return value
	}
	_, err := resultRootOf(bundle(map[string]interface{}{"agent_input": map[string]interface{}{"spec_version": "evaluation-summary/v1"}}))
	assert.Contains(t, err.Error(), "its agent_input is a evaluation-summary/v1 document")
	_, err = resultRootOf(bundle(map[string]interface{}{"agent_output": map[string]interface{}{"spec_version": "report/v1"}}))
	assert.Contains(t, err.Error(), "its agent_output is a report/v1 document")
	_, err = resultRootOf(bundle(map[string]interface{}{"agent_input": map[string]interface{}{"hello": "world"}}))
	assert.Contains(t, err.Error(), "no disclosed member carries an evidence-result-v0 document or an evidence_result record header")
	_, err = resultRootOf(bundle(map[string]interface{}{"agent_input": map[string]interface{}{"record_type": "close"}}))
	assert.Contains(t, err.Error(), `its agent_input is a book record header of record_type "close"`)
	_, err = resultRootOf(bundle(nil))
	assert.Contains(t, err.Error(), "the bundle discloses nothing for it")
	_, err = resultRootOf(bundle(map[string]interface{}{"agent_input": map[string]interface{}{"record_type": evidencebook.RecordTypeEvidenceResult}}))
	assert.Contains(t, err.Error(), "header carries no statement object")
	_, err = resultRootOf(map[string]interface{}{"root": root, "records": []interface{}{}})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "is not among the bundle's records")
	found, err := resultRootOf(bundle(map[string]interface{}{"agent_output": map[string]interface{}{"result_version": cli.ResultVersion}, "agent_input": map[string]interface{}{"record_type": recordTypePublished}}))
	require.NoError(t, err)
	assert.Equal(t, "agent_output", found.member)
	assert.Equal(t, "payload", found.form)

	// Exactly one Result: a root carrying one in both members, or any other
	// disclosed record carrying one, is refused with both named.
	_, err = resultRootOf(bundle(map[string]interface{}{"agent_output": map[string]interface{}{"result_version": cli.ResultVersion}, "agent_input": map[string]interface{}{"record_type": evidencebook.RecordTypeEvidenceResult, "statement": map[string]interface{}{}}}))
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "root "+root+" carries a Result in both its agent_output and its agent_input; a report is rooted on exactly one Result document")
	other := strings.Repeat("2", 64)
	two := bundle(map[string]interface{}{"agent_input": map[string]interface{}{"record_type": evidencebook.RecordTypeEvidenceResult, "statement": map[string]interface{}{}}})
	two["records"] = append(two["records"].([]interface{}), map[string]interface{}{"capsule_id": other})
	two["disclosures"].(map[string]interface{})[other] = map[string]interface{}{"agent_output": map[string]interface{}{"result_version": cli.ResultVersion}}
	_, err = resultRootOf(two)
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "the bundle carries two Result documents: root "+root+" and record "+other+" (its agent_output); a report is rooted on exactly one Result")
	two["disclosures"].(map[string]interface{})[other] = map[string]interface{}{"agent_input": map[string]interface{}{"record_type": recordTypePublished, "statement": map[string]interface{}{"result_version": cli.ResultVersion}}}
	_, err = resultRootOf(two)
	require.NoError(t, err, "a non-Result record whose statement merely mentions a result_version is not a Result")
}

// A book profile's own capsule as root is named for what it is, whether or
// not the bundle discloses its payload; a payload shaped like an
// evidence_result header is not a Result in book form.
func TestResultRootOfNamesAnOwnCapsule(t *testing.T) {
	own := strings.Repeat("b", 64)
	value := map[string]interface{}{
		"root": own,
		"records": []interface{}{map[string]interface{}{
			"capsule_id":                    own,
			evidencebook.RecordHeaderMember: map[string]interface{}{"record_type": "own_capsule"},
		}},
	}
	_, err := resultRootOf(value)
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), `is not a sealed Evidence Result v0: it is a book record of record_type "own_capsule", not "evidence_result"`)
	value["disclosures"] = map[string]interface{}{own: map[string]interface{}{"agent_input": map[string]interface{}{
		"record_type": evidencebook.RecordTypeEvidenceResult, "statement": map[string]interface{}{"result_version": "x"},
	}}}
	_, err = resultRootOf(value)
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), `it is a book record of record_type "own_capsule"`)
}

// graph_closure withheld (the bundle declares records of its closure
// missing) is accepted, and said so; fail is refused, as is anything but
// pass on interval coverage or per-record membership. A sealed fixture
// cannot be withheld and still pass membership (its certificate covers every
// record), so the acceptance rule is pinned on the verdicts.
func TestHeldBundleAcceptanceNamesAWithheldClosure(t *testing.T) {
	verdict := func(graph, interval, membership string) aacbundle.VerificationResult {
		return aacbundle.VerificationResult{GraphClosure: aacbundle.ClaimResult{Status: graph}, IntervalCoverage: aacbundle.ClaimResult{Status: interval}, PerRecordMembership: aacbundle.ClaimResult{Status: membership}}
	}
	label, err := acceptHeldVerification(verdict("pass", "pass", "pass"))
	require.NoError(t, err)
	assert.Equal(t, "pass", label)
	label, err = acceptHeldVerification(verdict("withheld", "pass", "pass"))
	require.NoError(t, err)
	assert.Equal(t, "graph_closure withheld", label)
	for _, v := range []aacbundle.VerificationResult{verdict("fail", "pass", "pass"), verdict("pass", "withheld", "pass"), verdict("pass", "pass", "fail"), verdict("withheld", "pass", "withheld")} {
		_, err = acceptHeldVerification(v)
		require.ErrorIs(t, err, ErrRefused, "%+v", v)
		assert.Contains(t, err.Error(), "nothing was rendered")
	}
}

// A claim may cite the id a request seals to on a plain profile; on a book
// profile the request's own capsule carries that id as its header's
// subject_ref, so a bundle holding that capsule supports the claim.
func TestUnsupportedClaimsReadsAnOwnCapsulesSubject(t *testing.T) {
	plain, own := strings.Repeat("a", 64), strings.Repeat("b", 64)
	claims := []cli.ResultClaim{{Evidence: []string{plain}}}
	bare := map[string]interface{}{"records": []interface{}{map[string]interface{}{"capsule_id": own}}}
	assert.Equal(t, 1, unsupportedClaims(bare, claims), "a capsule that names nothing does not hold another id")
	assert.Equal(t, 1, unsupportedClaims(bare, []cli.ResultClaim{{}}), "a claim citing nothing is unsupported")
	headed := map[string]interface{}{"records": []interface{}{map[string]interface{}{
		"capsule_id":                    own,
		evidencebook.RecordHeaderMember: map[string]interface{}{"record_type": "own_capsule", "subject_ref": plain},
	}}}
	assert.Equal(t, 1, unsupportedClaims(headed, claims), "a claim cites an own capsule by its record id, not the sealed id")
	assert.Equal(t, 0, unsupportedClaims(headed, []cli.ResultClaim{{Evidence: []string{own}}}))
	// A wrapper's disclosed header stands for the published capsule it names.
	wrapped := map[string]interface{}{
		"records": []interface{}{map[string]interface{}{"capsule_id": own}},
		"disclosures": map[string]interface{}{own: map[string]interface{}{"agent_input": map[string]interface{}{
			"record_type": recordTypePublished, "subject_ref": plain, "payload_commitments": []interface{}{plain},
		}}},
	}
	assert.Equal(t, 0, unsupportedClaims(wrapped, claims))
	// Only a published or backfilled wrapper does: another record's header
	// naming the id does not.
	closing := map[string]interface{}{
		"records": wrapped["records"],
		"disclosures": map[string]interface{}{own: map[string]interface{}{"agent_input": map[string]interface{}{
			"record_type": "close", "subject_ref": plain, "payload_commitments": []interface{}{plain},
		}}},
	}
	assert.Equal(t, 1, unsupportedClaims(closing, claims), "only a wrapper stands for the id it names")
	// An own capsule's disclosed agent_input is its payload, which may look
	// like a wrapper's header; it never stands for the id it names.
	posing := map[string]interface{}{
		"records": []interface{}{map[string]interface{}{
			"capsule_id":                    own,
			evidencebook.RecordHeaderMember: map[string]interface{}{"record_type": "own_capsule", "subject_ref": own},
		}},
		"disclosures": wrapped["disclosures"],
	}
	assert.Equal(t, 1, unsupportedClaims(posing, claims), "an own capsule's payload is not read as a header")
}

// hostWording is a wording layer that sets the document title, with its digest.
func hostWording(t *testing.T) *Wording {
	t.Helper()
	pack := `{"wording_pack_version":"aac.wording-pack/v0","id":"example.host/v0","locale":"en","entries":{"page.title":"Monthly outcomes"}}`
	return &Wording{Pack: pack, SHA256: hostWordingDigest(pack)}
}

// A wording pack reaches the runtime, bound to its digest, and its
// page.title is the page title; the contract report is selected by the
// empty contract-report/v1 block the embedded bundle carries.
func TestBuildCarriesAWordingPackAndTheContractReport(t *testing.T) {
	raw := mustRead(t, filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json"))
	wording := hostWording(t)
	page, err := Build(raw, View{Card: "outcome", Wording: wording, ContractReport: true})
	require.NoError(t, err)
	assert.Equal(t, "VALID", page.Verdict)
	html := string(page.HTML)
	assert.Contains(t, html, "<title>Monthly outcomes</title>")
	options, err := canonical.JCS(map[string]interface{}{"audience": "*", "format": "html", "wording": map[string]interface{}{"pack": wording.Pack, "sha256": wording.SHA256}})
	require.NoError(t, err)
	assert.Contains(t, html, `renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"), undefined, `+strings.NewReplacer("<", `\u003c`, ">", `\u003e`, "&", `\u0026`).Replace(string(options))+`);`)
	embedded, err := embeddedBundle(page.HTML)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{}, embedded["extensions"].(map[string]interface{})[contractReportExtension])

	// Neither is required, and each changes the page on its own.
	plain, err := Build(raw, View{Card: "outcome"})
	require.NoError(t, err)
	assert.Equal(t, string(mustRead(t, filepath.Join(presentationDir, "4-monthly-outcome", "page.html"))), string(plain.HTML))
	worded, err := Build(raw, View{Card: "outcome", Wording: wording})
	require.NoError(t, err)
	assert.NotEqual(t, plain.HTML, worded.HTML)
	assert.Equal(t, plain.BundleDigest, worded.BundleDigest, "a wording pack changes the page, not the bundle it embeds")
	contract, err := Build(raw, View{Card: "outcome", ContractReport: true})
	require.NoError(t, err)
	assert.NotEqual(t, plain.BundleDigest, contract.BundleDigest, "the contract report is recorded in the bundle")

	// A permalink would carry the bundle without the layer, so the worded
	// page gets none; the contract-report selection is in the bundle, so
	// that page does.
	_, err = page.Permalink("")
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "wording layer")
	_, err = contract.Permalink("")
	require.NoError(t, err)

	// The page with the wording pack is sealed and checked like any other.
	book := newBook(t)
	_, err = SealPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	check, err := CheckPage(t.Context(), book, page.HTML)
	require.NoError(t, err)
	assert.Equal(t, PageMatch, check.Outcome)
}

func TestBuildRefusesABadWordingPackOrAnotherContractReportBlock(t *testing.T) {
	raw := mustRead(t, bookFormBundle)
	good := hostWording(t)
	for name, wording := range map[string]*Wording{
		"a digest of other bytes": {Pack: good.Pack, SHA256: strings.Repeat("0", 64)},
		"not a digest":            {Pack: good.Pack, SHA256: "sha256:abc"},
		"not a wording pack":      {Pack: `{"entries":{}}`, SHA256: hostWordingDigest(`{"entries":{}}`)},
	} {
		_, err := Build(raw, View{Card: "outcome", Wording: wording})
		require.ErrorIs(t, err, ErrRefused, name)
		assert.Contains(t, err.Error(), "wording: ", name)
	}
	other := edited(t, raw, func(value map[string]interface{}) {
		value["extensions"].(map[string]interface{})[contractReportExtension] = map[string]interface{}{"grouping": "lines"}
	})
	_, err := Build(other, View{Card: "outcome", ContractReport: true})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "already carries a different contract-report/v1 block")
}

func hostWordingDigest(pack string) string {
	sum := sha256.Sum256([]byte(pack))
	return hex.EncodeToString(sum[:])
}
