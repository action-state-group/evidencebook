// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v0WitnessFixture is an x-deal-v0 deal whose holder's book a witness
// covered at its checkpoint, with the witness's key and directory.
type v0WitnessFixture struct {
	p         cli.Profile
	dealID    string
	endpoint  string
	public    ed25519.PublicKey
	key       ed25519.PrivateKey
	directory string
	dir       string
	now       *time.Time
}

const v0WitnessAt = "2026-10-04T09:05:00Z"

func newV0WitnessFixture(t *testing.T, iat time.Time) v0WitnessFixture {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliverAt(t, p, cadenceSize(t, p), key, iat)
	return v0WitnessFixture{p: p, dealID: dealID, endpoint: endpoint, public: public, key: key,
		directory: writeDirectory(t, rawKeyRow(endpoint, public)), dir: t.TempDir(), now: now}
}

// copyAndProof writes the holder's copy and `deal checkpoint proof` for it.
func (f v0WitnessFixture) copyAndProof(t *testing.T, name string) (string, string, map[string]any) {
	t.Helper()
	copyPath := filepath.Join(f.dir, name+".json")
	dealRun(t, "report", "--deal", f.dealID, "--bundle", copyPath)
	proofPath := filepath.Join(f.dir, name+".proof.json")
	out := dealRun(t, "checkpoint", "proof", "--deal", f.dealID, "--copy", copyPath, "--out", proofPath)
	return copyPath, proofPath, out
}

func v0WitnessedOf(t *testing.T, args ...string) (map[string]any, string) {
	t.Helper()
	report := verifyReport(t, append([]string{"--bundle"}, args...)...)
	w, _ := v0Entry(t, report)["witnessed"].(map[string]any)
	return w, report["verdict"].(string)
}

// The holder's chain beside an x-deal-v0 copy gives its witness time
// (u219): verified with the directory's name for the witness and the
// receipt's own time; never a change of verdict; withheld with no
// directory; and the copy itself still carries no chain.
func TestV0WitnessedFromTheHoldersChain(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	copyPath, proofPath, out := f.copyAndProof(t, "all")
	assert.Equal(t, "all", out["extent"])
	assert.NotContains(t, out, "note")
	assert.NotContains(t, string(mustRead(t, copyPath)), DealParentWitnessExtension, "the copy carries no chain (u225)")

	_, plain := v0WitnessedOf(t, copyPath)
	w, verdict := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", f.directory)
	assert.Equal(t, plain, verdict, "the witness time never changes the verdict")
	assert.Equal(t, map[string]any{"status": "verified", "witness": "test witness", "at": v0WitnessAt, "independent_of": []any{}}, w,
		"the row states no independent_of: [] (evidencebook#1073)")
	listedRow := rawKeyRow(f.endpoint, f.public)
	listedRow["independent_of"] = []string{strings.Repeat("a1", 32)}
	listed, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", writeDirectory(t, listedRow))
	assert.Equal(t, []any{strings.Repeat("a1", 32)}, listed["independent_of"], "the row's list as W states it")
	badRow := rawKeyRow(f.endpoint, f.public)
	badRow["independent_of"] = "everyone"
	malformedList, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", writeDirectory(t, badRow))
	assert.Equal(t, "test witness", malformedList["witness"])
	assert.NotContains(t, malformedList, "independent_of", "a list that is not a list of strings is never guessed")

	// The member has the viewer contract's shape (capsule-viewer
	// tests/testdata/deal-v0-report, main-standalone-keep.verify.json).
	raw, err := os.ReadFile("testdata/deal-v0-report/countersigned-keep.verify.json")
	require.NoError(t, err)
	var fixture map[string]any
	require.NoError(t, json.Unmarshal(raw, &fixture))
	want := v0Entry(t, fixture)["witnessed"].(map[string]any)
	for k := range want {
		assert.Contains(t, w, k)
	}

	none, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath)
	assert.Equal(t, "withheld", none["status"], "no directory")
	unknown, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", writeDirectory(t, rawKeyRow("https://other.example", f.public)))
	assert.Equal(t, "withheld", unknown["status"], "a witness the directory does not list")
	assert.NotContains(t, unknown, "witness", "no row is known")
	assert.NotContains(t, unknown, "independent_of", "no row is known")
	assert.NotContains(t, none, "witness", "no directory, no row")
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	wrongKey, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", writeDirectory(t, rawKeyRow(f.endpoint, other)))
	assert.Equal(t, "failed", wrongKey["status"], "a receipt that does not verify, and none that does")
	assert.Equal(t, "test witness", wrongKey["witness"], "the row of the receipt that does not verify")
	assert.Equal(t, []any{}, wrongKey["independent_of"])

	// A failing receipt beside one that verifies: the verified one counts.
	var proof v0ChainProof
	require.NoError(t, json.Unmarshal(mustRead(t, proofPath), &proof))
	inner := proof.Chain["parent"].(map[string]any)
	good := inner["witnesses"].([]any)[0].(map[string]any)
	bad := map[string]any{}
	for k, v := range good {
		bad[k] = v
	}
	bad["receipt_b64"] = strings.Repeat("A", 40) // decodes, and does not verify
	malformedEntry := map[string]any{}
	for k, v := range good {
		malformedEntry[k] = v
	}
	malformedEntry["receipt_b64"] = "not base64!" // no receipt at all
	unknownEntry := map[string]any{}
	for k, v := range good {
		unknownEntry[k] = v
	}
	unknownEntry["ts_url"] = "https://unlisted.example"
	write := func(name string, entries ...any) string {
		inner["witnesses"] = entries
		path := filepath.Join(f.dir, name+".proof.json")
		data, err := json.Marshal(proof)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o600))
		return path
	}
	// statusOf is the member's status, and the row it names ("" for none).
	statusOf := func(path string) string {
		w, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", path, "--witness-directory", f.directory)
		if name, named := w["witness"].(string); named {
			return w["status"].(string) + " " + name
		}
		return w["status"].(string)
	}
	assert.Equal(t, "verified test witness", statusOf(write("bad-good", bad, good)), "a receipt that does not verify beside one that does")
	assert.Equal(t, "failed test witness", statusOf(write("bad", bad)), "a receipt that does not verify, alone")
	assert.Equal(t, "failed test witness", statusOf(write("unknown-bad", unknownEntry, bad)), "the row of the receipt that does not verify, not an unlisted one")
	assert.Equal(t, "verified test witness", statusOf(write("malformed-good", malformedEntry, good)), "a malformed receipt beside a verified one")
	assert.Equal(t, "failed", statusOf(write("malformed", malformedEntry)), "a malformed receipt alone fails, never reads as no receipt; it gives no result, so no row")
	assert.Equal(t, "withheld", statusOf(write("unknown", unknownEntry)), "a witness the directory does not list")
	assert.Equal(t, "failed", statusOf(write("malformed-unknown", malformedEntry, unknownEntry)), "nothing verified, and a receipt is malformed")
}

// A receipt that verifies but states no time gives no time.
func TestV0WitnessedWithNoTimeIsWithheld(t *testing.T) {
	f := newV0WitnessFixture(t, time.Time{})
	copyPath, proofPath, _ := f.copyAndProof(t, "all")
	w, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", f.directory)
	assert.Equal(t, "withheld", w["status"])
	assert.Equal(t, "test witness", w["witness"], "the row of the receipt that verified without a time")
	assert.Equal(t, []any{}, w["independent_of"])
	assert.NotContains(t, w, "at")
}

// A chain witnessed for an earlier checkpoint, after a later step: its time
// is not the copy's, so none is given; another deal's chain fails.
func TestV0WitnessedOnlyForTheCopysOwnCheckpoint(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	allCopy, allProof, _ := f.copyAndProof(t, "all")
	*f.now = f.now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", f.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	partCopy, partProof, out := f.copyAndProof(t, "part")
	assert.Equal(t, "part", out["extent"])
	assert.Contains(t, out["note"], "earlier checkpoint")
	w, _ := v0WitnessedOf(t, partCopy, "--stamp-proof", partProof, "--witness-directory", f.directory)
	assert.Equal(t, "withheld", w["status"])
	assert.Contains(t, w["reason"], "earlier checkpoint")
	assert.NotContains(t, w, "witness", "an earlier checkpoint's chain names no row")
	// The chain of the earlier copy beside the later one names another
	// checkpoint.
	stale, _ := v0WitnessedOf(t, partCopy, "--stamp-proof", allProof, "--witness-directory", f.directory)
	assert.Equal(t, "failed", stale["status"])
	still, _ := v0WitnessedOf(t, allCopy, "--stamp-proof", allProof, "--witness-directory", f.directory)
	assert.Equal(t, "verified", still["status"])
}

// checkpoint proof refuses a copy of another deal.
func TestV0CheckpointProofRefusesAnotherDealsCopy(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	copyPath, _, _ := f.copyAndProof(t, "all")
	other := retailDeal(t)
	_, err := invoke(t, "", "--profile", "deal", "deal", "checkpoint", "proof", "--deal", other, "--copy", copyPath, "--out", filepath.Join(t.TempDir(), "p.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a copy of deal "+other)
}

// The page: the holder's own page with --witness-directory says the witness
// time in capsulectl's voice (u219); without it, no time; a shared page
// takes no --witness-directory (CAPSULECTL_CHROME).
func TestV0PageDrawsTheHoldersWitnessTime(t *testing.T) {
	chrome := chromeOrSkip(t)
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	page := filepath.Join(t.TempDir(), "keep.html")
	dealRun(t, "report", "--deal", f.dealID, "--html", page, "--witness-directory", f.directory)
	assert.Equal(t, map[string]any{"status": "verified", "witness": "test witness", "at": v0WitnessAt, "independent_of": []any{}},
		v0Entry(t, pageVerifyReport(t, mustRead(t, page)))["witnessed"], "capsulectl's report on the page, to the second")
	// The chain itself never reaches the page: neither its bundle nor its
	// bootstrap data carries it (u219).
	bundleData, err := json.Marshal(embeddedBundle(t, string(mustRead(t, page))))
	require.NoError(t, err)
	assert.NotContains(t, string(bundleData), DealParentWitnessExtension)
	assert.NotContains(t, string(pageBootstrapData(t, mustRead(t, page))), DealParentWitnessExtension)
	assert.NotContains(t, string(pageBootstrapData(t, mustRead(t, page))), "inclusion_proof")
	with := readV0Page(t, chrome, page)
	require.Empty(t, with.Errors)
	require.Len(t, with.Rungs["witness"], 1)
	assert.Contains(t, with.Rungs["witness"][0], "test witness")
	assert.NotEqual(t, "No witness result was reported to this page.", with.Rungs["witness"][0])
	without := reportV0Page(t, chrome, f.dealID, "")
	assert.Equal(t, []string{"No witness result was reported to this page."}, without.Rungs["witness"])

	_, err = invoke(t, "", "--profile", "deal", "deal", "report", "--deal", f.dealID, "--html", filepath.Join(t.TempDir(), "s.html"),
		"--share", cli.DealAudienceCounterparty, "--to", "the shop", "--witness-directory", f.directory)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a shared copy carries none")
	// Without --html there is no page to put it on.
	_, err = invoke(t, "", "--profile", "deal", "deal", "report", "--deal", f.dealID, "--witness-directory", f.directory)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--witness-directory needs --html on your own copy")

	// A shared copy given with --from-bundle is refused too, so its page's
	// report never holds the time.
	for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		sharedPage := filepath.Join(t.TempDir(), audience+".html")
		dealRun(t, "report", "--deal", f.dealID, "--share", audience, "--to", "the shop", "--html", sharedPage)
		// The shared copy's bundle, as the other side holds it.
		data, err := json.Marshal(embeddedBundle(t, string(mustRead(t, sharedPage))))
		require.NoError(t, err)
		shared := filepath.Join(t.TempDir(), audience+".json")
		require.NoError(t, os.WriteFile(shared, data, 0o600))
		out := filepath.Join(t.TempDir(), audience+".html")
		_, err = invoke(t, "", "--profile", "deal", "deal", "report", "--deal", f.dealID, "--from-bundle", shared, "--html", out, "--witness-directory", f.directory)
		require.Error(t, err, audience)
		assert.Contains(t, err.Error(), "a shared copy carries none", audience)
		assert.NoFileExists(t, out)
	}

	// After a later step, the page of the new copy draws no time: the
	// held chain is witnessed for an earlier checkpoint.
	*f.now = f.now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", f.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	later := filepath.Join(t.TempDir(), "later.html")
	dealRun(t, "report", "--deal", f.dealID, "--html", later, "--witness-directory", f.directory)
	w := v0Entry(t, pageVerifyReport(t, mustRead(t, later)))["witnessed"]
	if w != nil {
		assert.NotEqual(t, "verified", w.(map[string]any)["status"])
	}
	part := readV0Page(t, chrome, later)
	assert.NotContains(t, strings.Join(part.Rungs["witness"], " "), "test witness", "no earlier witness time")
}

// A copy whose report is sealed in the deal's log reads its witness time the
// same way (the viewer's contract reads every report form).
func TestV0WitnessedForAnInLogKeepCopy(t *testing.T) {
	inLogReports(t)
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	// Sealing the report in the log adds a record after the first tick, so
	// the copy's checkpoint is witnessed only by a later tick.
	copyPath := filepath.Join(f.dir, "inlog.json")
	dealRun(t, "report", "--deal", f.dealID, "--bundle", copyPath)
	*f.now = f.now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliverAt(t, f.p, cadenceSize(t, f.p), f.key, time.Date(2026, 10, 4, 11, 5, 0, 0, time.UTC))
	proofPath := filepath.Join(f.dir, "inlog.proof.json")
	assert.Equal(t, "all", dealRun(t, "checkpoint", "proof", "--deal", f.dealID, "--copy", copyPath, "--out", proofPath)["extent"])
	report := verifyReport(t, "--bundle", copyPath, "--stamp-proof", proofPath, "--witness-directory", f.directory)
	entry := v0Entry(t, report)
	require.NotEqual(t, "standalone", entry["report"], "the report is sealed in the log")
	assert.Equal(t, "verified", entry["witnessed"].(map[string]any)["status"], "%v", entry["witnessed"])
}

// The member names the row of the receipt it reports on, among distinct
// witnesses with distinct lists (evidencebook#1073): the earliest verified
// time's witness wherever its receipt stands; on failed the first receipt
// that does not verify; on withheld the first listed receipt; and never a
// row with no endpoint for a receipt W does not list.
func TestV0WitnessedNamesTheChosenReceiptsRow(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	copyPath, proofPath, _ := f.copyAndProof(t, "all")
	var proof v0ChainProof
	require.NoError(t, json.Unmarshal(mustRead(t, proofPath), &proof))
	inner := proof.Chain["parent"].(map[string]any)
	first := inner["witnesses"].([]any)[0].(map[string]any)
	entry, err := hex.DecodeString(first["entry_hash"].(string))
	require.NoError(t, err)
	secondPublic, secondKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	const secondURL = "https://second.example"
	receiptOf := func(base map[string]any, url string, receipt []byte) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		out["ts_url"], out["receipt_b64"] = url, base64.StdEncoding.EncodeToString(receipt)
		return out
	}
	firstKey := f.key
	at := func(key ed25519.PrivateKey, url string, iat time.Time) map[string]any {
		return receiptOf(first, url, mintReceiptAt(t, entry, key, iat))
	}
	bad := func(url string) map[string]any { return receiptOf(first, url, []byte(strings.Repeat("x", 30))) }
	a, b := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	row1 := rawKeyRow(f.endpoint, f.public)
	row1["independent_of"] = []string{a}
	row2 := rawKeyRow(secondURL, secondPublic)
	row2["name"], row2["independent_of"] = "second witness", []string{b}
	directory := writeDirectory(t, row1, row2)
	n := 0
	member := func(dir string, entries ...any) map[string]any {
		inner["witnesses"] = entries
		n++
		path := filepath.Join(f.dir, fmt.Sprintf("chosen-%d.proof.json", n))
		data, err := json.Marshal(proof)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o600))
		w, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", path, "--witness-directory", dir)
		return w
	}
	nine05 := time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC)
	nine01 := time.Date(2026, 10, 4, 9, 1, 0, 0, time.UTC)
	earliestSecond := map[string]any{"status": "verified", "witness": "second witness", "at": "2026-10-04T09:01:00Z", "independent_of": []any{b}}
	assert.Equal(t, earliestSecond, member(directory, at(firstKey, f.endpoint, nine05), at(secondKey, secondURL, nine01)), "the earlier receipt second")
	assert.Equal(t, earliestSecond, member(directory, at(secondKey, secondURL, nine01), at(firstKey, f.endpoint, nine05)), "the earlier receipt first")

	failed := member(directory, bad(f.endpoint), bad(secondURL))
	assert.Equal(t, []any{"failed", "test witness", []any{a}}, []any{failed["status"], failed["witness"], failed["independent_of"]})
	failed = member(directory, bad(secondURL), bad(f.endpoint))
	assert.Equal(t, []any{"failed", "second witness", []any{b}}, []any{failed["status"], failed["witness"], failed["independent_of"]})

	withheld := member(directory, at(secondKey, secondURL, time.Time{}), at(firstKey, f.endpoint, time.Time{}))
	assert.Equal(t, []any{"withheld", "second witness", []any{b}}, []any{withheld["status"], withheld["witness"], withheld["independent_of"]}, "no time: the first listed receipt")

	// A row with no endpoint is no receipt's row here.
	noEndpoint := map[string]interface{}{"name": "unrelated", "key_ids": []string{hex.EncodeToString(f.public)}, "independent_of": []string{a}}
	unrelated := writeDirectory(t, noEndpoint)
	for name, entries := range map[string][]any{
		"an unlisted receipt":                 {at(firstKey, "https://unlisted.example", nine05)},
		"a malformed and an unlisted receipt": {receiptOf(first, f.endpoint, nil), at(firstKey, "https://unlisted.example", nine05)},
	} {
		w := member(unrelated, entries...)
		assert.NotEqual(t, "verified", w["status"], name)
		assert.NotContains(t, w, "witness", name)
		assert.NotContains(t, w, "independent_of", name)
	}
}
