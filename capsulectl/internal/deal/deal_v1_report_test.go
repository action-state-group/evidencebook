// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/evidencebook/dealprofile"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `deal report` on a v1 deal prints the holder's own copy and seals nothing;
// --out seals the report, whose pair verifies against the deal's first
// record; --read-only prints too.
func TestAV1ReportPrintsAndSealsOnOut(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	db := v1Profile(t).Connection.Database
	files := func() map[string][]byte {
		got := map[string][]byte{}
		for _, f := range []string{db, db + "-wal"} {
			if b, err := os.ReadFile(f); err == nil {
				got[f] = b
			}
		}
		return got
	}
	before := files()
	out := dealRun(t, "report", "--deal", h.dealID)
	assert.Equal(t, before, files(), "printing a report writes nothing to the store or its log")
	report, ok := out["report"].(map[string]any)
	require.True(t, ok, "%v", out)
	assert.Equal(t, "keep", report["audience"])
	assert.Nil(t, out["sealed_report"], "printing seals nothing")
	assert.Contains(t, out["settled_note"], "No act on this deal carries the person's own signature yet")

	file := filepath.Join(t.TempDir(), "report.json")
	sealed := dealRun(t, "report", "--deal", h.dealID, "--read-only=false", "--out", file)
	require.NotEmpty(t, sealed["sealed_report"])
	pair, err := os.ReadFile(file)
	require.NoError(t, err)
	read, err := xdealv1.VerifyReport(pair, readPush(t, h.buyerAgr).Record)
	require.NoError(t, err)
	assert.Equal(t, sealed["sealed_report"], read.CapsuleID)
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--out", file), "exists")

	ro := dealRun(t, "report", "--deal", h.dealID, "--read-only")
	assert.NotNil(t, ro["report"])
}

// A book with records after its last checkpoint has no report yet, and the
// refusal names the cadence; a deal with no book names deal agree; the v0
// flags with no v1 form are refused, each with why.
func TestAV1ReportRefusals(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID), "the next cadence tick covers it")
	for flag, want := range map[string]string{"--html": "has no page yet", "--share": "needs --out FILE and --to", "--bundle": "not served yet",
		"--email": "receipt email", "--from-bundle": "countersignatures", "--directory": "countersignatures"} {
		arg := filepath.Join(t.TempDir(), "x")
		if flag == "--share" {
			arg = "counterparty"
		}
		assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, flag, arg), want, flag)
	}
	for _, flag := range []string{"--" + includeAgentClaimsFlag, "--" + includeUnauthorizedFlag} {
		assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, flag), "does not apply to an x-deal-v1 report", flag)
	}
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--read-only", "--out", filepath.Join(t.TempDir(), "r.json")), "writes nothing")

	r := receivedOffer(t)
	assert.Contains(t, v1Err(t, "report", "--deal", r.dealID), "has no agreement of yours recorded yet")
}

// --out is an x-deal-v1 report's; an x-deal-v0 deal refuses it.
func TestAV0ReportRefusesOut(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, v0BuyerOpen))["deal_id"].(string)
	assert.Contains(t, v1Err(t, "report", "--deal", id, "--out", filepath.Join(t.TempDir(), "r.json")), "an x-deal-v0 deal's report goes to --html or --bundle")
}

// A report is signed by the key that signed the agreement: after the profile
// rotates its signing key (the old one kept trusted), --out is refused and no
// file is written; a profile with no signing key is refused too.
func TestAV1ReportOutNeedsTheAgreementsKey(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	p := dealProfile(t)
	public, private, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	seed := filepath.Join(t.TempDir(), "rotated.seed")
	require.NoError(t, os.WriteFile(seed, []byte(hex.EncodeToString(private.Seed())), 0o600))
	rotated := p
	rotated.Signing = cli.Secret{File: seed}
	rotated.TrustedKeys = append(append([]string{}, p.TrustedKeys...), hex.EncodeToString(public))
	require.NoError(t, cli.SaveProfile(rotated, true))
	file := filepath.Join(t.TempDir(), "report.json")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--out", file), "does not verify against your agreement")
	_, err = os.Lstat(file)
	assert.ErrorIs(t, err, os.ErrNotExist, "no file is written")

	unsigned := p
	unsigned.Signing = cli.Secret{}
	require.NoError(t, cli.SaveProfile(unsigned, true))
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--out", file), "has none")
	_, err = os.Lstat(file)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// The refusal's promise holds: once a cadence tick is due and deal tick runs
// it, the book is checkpointed and the report is computed.
func TestAV1ReportAfterADueTick(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	require.Contains(t, v1Err(t, "report", "--deal", h.dealID), "the next cadence tick covers it (about every 5m;")
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	assert.Equal(t, "ticked", dealRun(t, "tick")["state"])
	out := dealRun(t, "report", "--deal", h.dealID)
	assert.NotNil(t, out["report"], "%v", out)
}

// A shared copy is put on record before it is written: the disclosure record
// names the audience, the recipient and the digest of the exact file; an
// adjudicator's copy names its referee. A failed share gate writes and
// records nothing.
func TestAV1SharedReportIsOnRecordFirst(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	disclosures := func() []map[string]any {
		var out []map[string]any
		rows, err := v1DB(t).Query(`SELECT record FROM deal_disclosures WHERE deal_id=? ORDER BY n`, h.dealID)
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var raw string
			require.NoError(t, rows.Scan(&raw))
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &m))
			out = append(out, m)
		}
		require.NoError(t, rows.Err())
		return out
	}
	file := filepath.Join(t.TempDir(), "cp.json")
	out := dealRun(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--out", file)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	read, err := xdealv1.VerifyReport(data, readPush(t, h.buyerAgr).Record)
	require.NoError(t, err)
	assert.Equal(t, out["sealed_report"], read.CapsuleID)
	recs := disclosures()
	require.Len(t, recs, 1)
	assert.Equal(t, "counterparty", recs[0]["audience"])
	assert.Equal(t, "Kettle Shop", recs[0]["recipient"])
	sum := sha256.Sum256(data)
	assert.Equal(t, hex.EncodeToString(sum[:]), recs[0]["receipt_sha256"], "the digest of the exact copy written")

	adj := filepath.Join(t.TempDir(), "adj.json")
	got := dealRun(t, "report", "--deal", h.dealID, "--share", "adjudicator", "--to", "Lamp Court", "--out", adj)
	report := got["report"].(map[string]any)
	assert.Equal(t, "Lamp Court", report["referee"])
	recs = disclosures()
	require.Len(t, recs, 2)
	assert.Equal(t, "adjudicator", recs[1]["audience"])
	assert.Equal(t, "Lamp Court", recs[1]["recipient"])
	assert.Equal(t, "adjudicator-key", recs[1]["adjudicator_key_id"], "the agreement's adjudicator, by key")
	assert.Nil(t, recs[0]["adjudicator_key_id"])

	taken := filepath.Join(t.TempDir(), "taken.json")
	require.NoError(t, os.WriteFile(taken, []byte("x"), 0o600))
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--out", taken), "exists")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "adjudicator", "--to", "Lamp Court", "--out", ""), "cannot be empty")
	assert.Len(t, disclosures(), 2, "an existing or empty --out records nothing")

	v1ShareGate = func([]byte, []sealedEvent, string) error {
		return cli.InputError("the copy would spell out a private value")
	}
	t.Cleanup(func() { v1ShareGate = dealShareGate })
	refused := filepath.Join(t.TempDir(), "refused.json")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--out", refused), "private value")
	_, err = os.Lstat(refused)
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing written")
	assert.Len(t, disclosures(), 2, "nothing recorded")

	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "counterparty", "--out", filepath.Join(t.TempDir(), "x.json")), "needs --out FILE and --to")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--to", "Kettle Shop"), "use it with --share counterparty or adjudicator")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--read-only", "--share", "counterparty", "--to", "x"), "writes nothing")
}

// The real share gate runs on a v1 shared copy with the deal's own local
// events, the opening and its plain counterparty values among them, and the
// copy's audience, over the exact bytes then written.
func TestTheRealShareGateReadsAV1DealsPlainValues(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	var gotEvents []sealedEvent
	var gotAudience string
	var gotBytes []byte
	v1ShareGate = func(data []byte, events []sealedEvent, audience string) error {
		gotEvents, gotAudience, gotBytes = events, audience, data
		return dealShareGate(data, events, audience)
	}
	t.Cleanup(func() { v1ShareGate = dealShareGate })
	file := filepath.Join(t.TempDir(), "adj.json")
	dealRun(t, "report", "--deal", h.dealID, "--share", "adjudicator", "--to", "Lamp Court", "--out", file)
	assert.Equal(t, "adjudicator", gotAudience)
	require.NotEmpty(t, gotEvents)
	require.NotNil(t, gotEvents[0].Event.Open, "the opening, which holds the plain counterparty values")
	assert.Equal(t, "Kettle Shop", gotEvents[0].Event.Open.Who.Name)
	written, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, written, gotBytes, "the gate read the exact bytes written")
}

// An adjudicator's copy goes only to a deal whose agreement chose one.
func TestAV1AdjudicatorCopyNeedsTheAgreementsAdjudicator(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "adj.json")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "adjudicator", "--to", "Lamp Court", "--out", file), "names no adjudicator")
	_, err := os.Lstat(file)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// The bytes are staged before the record: a failed record leaves no file
// (and no staging file); an unwritable directory or an existing file runs no
// record; a file that appears at the path after the record is never
// replaced and the error says the copy is on record; a failed folder sync
// says the copy is written and recorded.
func TestWriteNewFileAfterStagesBeforeTheRecord(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "c.json")
	assert.Error(t, writeNewFileAfter(out, []byte("x\n"), func() error { return errors.New("no record") }))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no file, no staging file")

	ro := filepath.Join(dir, "ro")
	require.NoError(t, os.Mkdir(ro, 0o500))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	ran := false
	assert.Error(t, writeNewFileAfter(filepath.Join(ro, "c.json"), []byte("x\n"), func() error { ran = true; return nil }))
	assert.False(t, ran, "an unwritable directory fails before the record")

	require.NoError(t, os.WriteFile(out, []byte("theirs"), 0o600))
	assert.ErrorContains(t, writeNewFileAfter(out, []byte("mine\n"), func() error { ran = true; return nil }), "exists")
	assert.False(t, ran, "an existing file fails before the record")
	require.NoError(t, os.Remove(out))

	// Another writer takes the path while the record is made: its file is
	// never replaced.
	err = writeNewFileAfter(out, []byte("mine\n"), func() error { return os.WriteFile(out, []byte("theirs"), 0o600) })
	assert.ErrorContains(t, err, "on record, but")
	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "theirs", string(got), "never replaced")
	require.NoError(t, os.Remove(out))

	v1SyncDir = func(string) error { return errors.New("sync failed") }
	t.Cleanup(func() { v1SyncDir = syncDir })
	err = writeNewFileAfter(out, []byte("mine\n"), func() error { return nil })
	assert.ErrorContains(t, err, "on record and written to")
	got, err = os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "mine\n", string(got))
	v1SyncDir = syncDir

	fresh := filepath.Join(dir, "d.json")
	require.NoError(t, writeNewFileAfter(fresh, []byte("y\n"), func() error { return nil }))
	got, err = os.ReadFile(fresh)
	require.NoError(t, err)
	assert.Equal(t, "y\n", string(got))
	info, err := os.Stat(fresh)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"c.json", "d.json", "ro"}, names, "no staging or probe file left")
}

// v1BundleOf reads a written bundle file and checks it as a reader does: the
// core's standalone-report check with the profile's rule registered, and
// xdealv1.VerifyReportBundle on its report and extension.
func v1BundleOf(t *testing.T, file string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	value, err := cli.DecodeBundleJSON(raw)
	require.NoError(t, err)
	cli.SetDealV1ReportVerifier(verifyV1Report)
	t.Cleanup(func() { cli.SetDealV1ReportVerifier(nil) })
	entry := cli.DealReportEntry(value, aacbundle.VerificationResult{})
	assert.Equal(t, "pass", entry["status"], entry)
	require.NoError(t, cli.CheckDealStandaloneReport(value))
	exts := value["extensions"].(map[string]interface{})
	carried := exts[dealprofile.ReportExtension].(map[string]interface{})
	report, err := json.Marshal(carried)
	require.NoError(t, err)
	ext, err := json.Marshal(exts[xdealv1.Profile])
	require.NoError(t, err)
	_, err = xdealv1.VerifyReportBundle(report, ext)
	require.NoError(t, err)
	return value
}

// --bundle writes the deal book's Evidence Bundle with the report beside it,
// which verifies as a reader checks it, and writes nothing to the store: a
// report after it still computes, on the same book.
func TestAV1ReportBundleVerifiesAndWritesNothing(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	db := v1Profile(t).Connection.Database
	files := func() map[string][]byte {
		got := map[string][]byte{}
		for _, f := range []string{db, db + "-wal"} {
			if b, err := os.ReadFile(f); err == nil {
				got[f] = b
			}
		}
		return got
	}
	before := files()
	file := filepath.Join(t.TempDir(), "bundle.json")
	out := dealRun(t, "report", "--deal", h.dealID, "--bundle", file)
	assert.Equal(t, file, out["bundle"])
	assert.Equal(t, before, files(), "a bundle of your own copy writes nothing to the store or its log")
	value := v1BundleOf(t, file)
	assert.Equal(t, out["sealed_report"], value["extensions"].(map[string]interface{})[xdealv1.Profile].(map[string]interface{})["sealed_report"])
	assert.NotNil(t, dealRun(t, "report", "--deal", h.dealID)["report"], "a later report still computes")

	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--bundle", file), "exists")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--bundle", file+"2", "--out", file+"3"), "choose one")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--read-only", "--bundle", file+"2"), "writes nothing")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--bundle", ""), "cannot be empty")
}

// Every audience's bundle carries the same relied-on records, an
// adjudicator's lists no rows, a shared bundle is on record (kind bundle,
// the records it carries, the digest of the exact file), and none of the
// deal's local plain values (the intent's words, the opening's who) appears
// in any bundle's bytes.
func TestAV1ReportBundleAudiencesAndMarkers(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	dir := t.TempDir()
	paths := map[string]string{"keep": filepath.Join(dir, "keep.json"), "counterparty": filepath.Join(dir, "cp.json"), "adjudicator": filepath.Join(dir, "adj.json")}
	dealRun(t, "report", "--deal", h.dealID, "--bundle", paths["keep"])
	dealRun(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--bundle", paths["counterparty"])
	dealRun(t, "report", "--deal", h.dealID, "--share", "adjudicator", "--to", "Lamp Court", "--bundle", paths["adjudicator"])
	var reliedOn []interface{}
	for audience, path := range paths {
		value := v1BundleOf(t, path)
		book := value["extensions"].(map[string]interface{})[xdealv1.Profile].(map[string]interface{})["book"].(map[string]interface{})
		ids := []interface{}{}
		for id := range book["relied_on"].(map[string]interface{}) {
			ids = append(ids, id)
		}
		if reliedOn == nil {
			reliedOn = ids
		}
		assert.ElementsMatch(t, reliedOn, ids, audience)
		input := value["extensions"].(map[string]interface{})[dealprofile.ReportExtension].(map[string]interface{})["agent_input"].(map[string]interface{})
		assert.Equal(t, audience, input["audience"])
		if audience == "adjudicator" {
			assert.Equal(t, "withheld", input["rows_state"])
			assert.Empty(t, input["rows"])
		}
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, marker := range []string{"buy the lamp", "Kettle Shop", "kettle.example"} {
			assert.NotContains(t, string(raw), marker, "%s carries %q", audience, marker)
		}
	}
	rows, err := v1DB(t).Query(`SELECT record FROM deal_disclosures WHERE deal_id=? ORDER BY n`, h.dealID)
	require.NoError(t, err)
	defer rows.Close()
	var recs []map[string]any
	for rows.Next() {
		var raw string
		require.NoError(t, rows.Scan(&raw))
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &m))
		recs = append(recs, m)
	}
	require.NoError(t, rows.Err())
	require.Len(t, recs, 2, "the shared bundles only")
	for i, audience := range []string{"counterparty", "adjudicator"} {
		assert.Equal(t, "bundle", recs[i]["kind"])
		assert.Equal(t, audience, recs[i]["audience"])
		assert.ElementsMatch(t, reliedOn, recs[i]["relied_on"])
		raw, err := os.ReadFile(paths[audience])
		require.NoError(t, err)
		sum := sha256.Sum256(raw)
		assert.Equal(t, hex.EncodeToString(sum[:]), recs[i]["receipt_sha256"])
	}
}
