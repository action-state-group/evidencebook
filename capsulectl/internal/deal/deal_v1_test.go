package deal

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const v1OfferInput = `{
  "role": "buyer",
  "to": null,
  "terms": {"item": "lamp", "currency": "USD"},
  "pack_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "agree_by": "2026-11-02T10:00:00Z",
  "terms_rows": {
    "rows": [
      {"id": "paid_as_agreed", "owed_by": "buyer", "owed": {"amount_minor": 6000, "currency": "USD"},
       "due": {"at": "2026-11-01T23:59:59Z"}, "window_end": {"at": "2026-11-04T23:59:59Z"}}
    ],
    "named": {}
  },
  "adjudicator": null,
  "forums": [],
  "witnesses": []
}`

// v1Fixture is a deal profile whose clock is fixed.
func v1Fixture(t *testing.T) (dir string) {
	t.Helper()
	dir = dealFixture(t)
	clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	return dir
}

func v1Err(t *testing.T, args ...string) string {
	t.Helper()
	_, err := invoke(t, "", append([]string{"--profile", "deal", "deal"}, args...)...)
	require.Error(t, err)
	return err.Error()
}

func v1Profile(t *testing.T) cli.Profile {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	return p
}

func v1DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+v1Profile(t).Connection.Database+"?mode=ro")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func readPair(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return []byte(strings.TrimSpace(string(b)))
}

func TestSetVersionMovesTheStoreOnlyOnTheUsersWord(t *testing.T) {
	v1Fixture(t)
	p := v1Profile(t)
	backup, err := backupPath(p)
	require.NoError(t, err)

	// Without --migrate: nothing changes and the text says what it costs.
	msg := v1Err(t, "set-version", "x-deal-v1")
	assert.Contains(t, msg, "Muse or Goose")
	assert.Contains(t, msg, backup)
	assert.Contains(t, msg, "Run again with --migrate")
	assert.NotContains(t, msg, "<db>")
	assert.NotContains(t, msg, "written first")
	assert.NotContains(t, msg, "History card")
	got, err := dealVersionSetting(p)
	require.NoError(t, err)
	assert.Equal(t, dealVersionV0, got)
	_, err = os.Stat(backup)
	assert.True(t, os.IsNotExist(err), "no backup before the user's --migrate")

	// With --migrate: moved, backed up, declared; then idempotent.
	out := dealRun(t, "set-version", "x-deal-v1", "--migrate")
	assert.Equal(t, "Moved. Backup: "+backup+".", out["message"])
	_, err = os.Stat(backup)
	require.NoError(t, err)
	got, err = dealVersionSetting(p)
	require.NoError(t, err)
	assert.Equal(t, dealVersionV1, got)
	again := dealRun(t, "set-version", "x-deal-v1")
	assert.Equal(t, dealVersionV1, again["deal_version"])
	assert.Nil(t, again["moved"], "an already migrated store is not moved again")

	// v0 never moves the store and refuses --migrate.
	assert.Contains(t, v1Err(t, "set-version", "x-deal-v0", "--migrate"), "never moves the store")
	assert.Equal(t, dealVersionV0, dealRun(t, "set-version", "x-deal-v0")["deal_version"])
	assert.Contains(t, v1Err(t, "set-version", "x-deal-v2"), "must be x-deal-v0 or x-deal-v1")
}

func TestAV1VerbRefusesUntilTheProfileAndStoreAreOnV1(t *testing.T) {
	v1Fixture(t)
	in := writeJSON(t, v1OfferInput)
	out := filepath.Join(t.TempDir(), "offer.json")

	// A v0 profile writes v0 deals.
	assert.Contains(t, v1Err(t, "offer", "--input", in, "--out", out), "x-deal-v0")
	// Declared but not moved is impossible through the verb; the setting file alone leaves the store old.
	require.NoError(t, putDealVersionSetting(v1Profile(t), dealVersionV1))
	msg := v1Err(t, "offer", "--input", in, "--out", out)
	assert.Contains(t, msg, "not yet in the layout x-deal-v1 deals need")
	assert.Contains(t, msg, "deal set-version --help")
	_, err := os.Stat(out)
	assert.True(t, os.IsNotExist(err))
}

func TestAnOfferIsSealedAndSentWhole(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	in := writeJSON(t, v1OfferInput)
	out := filepath.Join(t.TempDir(), "offer.json")

	res := dealRun(t, "offer", "--input", in, "--out", out)
	dealID, digest := res["deal_id"].(string), res["offer"].(string)
	require.True(t, cli.DealIDPattern.MatchString(dealID))

	sent, err := xdealv1.VerifySent(readPair(t, out), nil)
	require.NoError(t, err)
	assert.Equal(t, digest, sent.Digest)
	assert.Equal(t, xdealv1.TypePropose, sent.Record.Block.RecordType)
	assert.Equal(t, dealID, sent.Record.Block.DealID)
	assert.Equal(t, "2026-11-01T09:00:00Z", sent.Record.Block.At)
	assert.Equal(t, "2026-11-01T09:00:00Z", sent.Record.Body.(*xdealv1.ProposeBody).StatedAt, "the time is the clock's, not the input's")

	// The local rows: a v1 version row with no book, and one operation, written.
	var version, state string
	require.NoError(t, v1DB(t).QueryRow(`SELECT version, state FROM deal_versions WHERE deal_id=?`, dealID).Scan(&version, &state))
	assert.Equal(t, dealVersionV1, version)
	assert.Equal(t, "offered", state)
	require.NoError(t, v1DB(t).QueryRow(`SELECT state FROM deal_sent WHERE record_digest=?`, digest).Scan(&state))
	assert.Equal(t, sentWritten, state)

	// An offer that fails the profile's checks seals nothing.
	bad := writeJSON(t, strings.Replace(v1OfferInput, `"forums": []`, `"forums": ["auto:twin"]`, 1))
	assert.Contains(t, v1Err(t, "offer", "--input", bad, "--out", filepath.Join(t.TempDir(), "bad.json")), "x-deal-v1 checks")
}

func TestARerunWritesTheSameOfferAndSealsNothing(t *testing.T) {
	now := clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	dealFixture(t)
	*now = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	in := writeJSON(t, v1OfferInput)
	out := filepath.Join(t.TempDir(), "offer.json")
	first := dealRun(t, "offer", "--input", in, "--out", out)
	want := readPair(t, out)

	// The file was lost and the operation stopped after its pair was stored:
	// the same command, later, writes the same bytes under the same deal id.
	require.NoError(t, os.Remove(out))
	_, err := openWritableDB(t).Exec(`UPDATE deal_sent SET state=?, written=0`, sentSealed)
	require.NoError(t, err)
	*now = now.Add(time.Hour)
	again := dealRun(t, "offer", "--input", in, "--out", out)
	assert.Equal(t, first["deal_id"], again["deal_id"])
	assert.Equal(t, first["offer"], again["offer"])
	assert.Equal(t, want, readPair(t, out), "the stored Capsule is written again, never rebuilt")
	var n int
	require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_sent`).Scan(&n))
	assert.Equal(t, 1, n)

	// A sealing row the journal already holds is not sealed again: the file
	// cannot be rebuilt, and the way out is a cancellation.
	require.NoError(t, os.Remove(out))
	_, err = openWritableDB(t).Exec(`UPDATE deal_sent SET state=?, pair=NULL, written=0`, sentSealing)
	require.NoError(t, err)
	msg := v1Err(t, "offer", "--input", in, "--out", out)
	assert.Contains(t, msg, "deal offer cancel")
	_, err = os.Stat(out)
	assert.True(t, os.IsNotExist(err))
}

func openWritableDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+v1Profile(t).Connection.Database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAnOfferIsCancelledOnce(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", offerFile)
	digest := res["offer"].(string)

	cancelFile := filepath.Join(t.TempDir(), "cancel.json")
	out := dealRun(t, "offer", "cancel", digest, "--out", cancelFile)
	assert.Equal(t, res["deal_id"], out["deal_id"])

	offer, err := xdealv1.VerifySent(readPair(t, offerFile), nil)
	require.NoError(t, err)
	cancelled, err := xdealv1.VerifySent(readPair(t, cancelFile), &xdealv1.OfferContext{Offer: offer.Record, Signer: offer.KeyID})
	require.NoError(t, err)
	body := cancelled.Record.Body.(*xdealv1.AgreeBody)
	assert.Equal(t, xdealv1.KindCancellation, body.Kind)
	assert.Equal(t, digest, body.Offer.Digest)

	var state string
	require.NoError(t, v1DB(t).QueryRow(`SELECT state FROM deal_versions WHERE deal_id=?`, res["deal_id"]).Scan(&state))
	assert.Equal(t, "cancelled", state)

	assert.Contains(t, v1Err(t, "offer", "cancel", digest, "--out", filepath.Join(t.TempDir(), "again.json")), "already cancelled")
	assert.Contains(t, v1Err(t, "offer", "cancel", strings.Repeat("0", 64), "--out", filepath.Join(t.TempDir(), "x.json")), "wrote no offer")
}

func TestAV1DealHasNoCloseAndNoV0Verb(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	res := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))
	id := res["deal_id"].(string)

	closeMsg := v1Err(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"received"}`))
	assert.Equal(t, "deal "+id+" is an x-deal-v1 deal and has no close. Each row is decided by statements, dates and disputes, and the deal is settled once every row is agreed, uncontested, or ruled with its remedy done. Record a row with `deal statement`.", closeMsg)
	assert.Contains(t, v1Err(t, "terms-result", "--deal", id, "--response", writeJSON(t, "{}"), "--openings", writeJSON(t, "{}"), "--out", filepath.Join(t.TempDir(), "res.json")), "has no terms result")
	other := v1Err(t, "report", "--deal", id)
	assert.Contains(t, other, "x-deal-v1 deal")
	assert.NotContains(t, other, "unknown deal")
}

// A shelf holding a v0 deal and a v1 offer: the v0 deal and the list are read as before.
func TestAV0DealIsUnchangedBesideAV1Offer(t *testing.T) {
	v1Fixture(t)
	v0 := retailDeal(t)
	list := func() string {
		out, err := invoke(t, "", "--profile", "deal", "deal", "list")
		require.NoError(t, err)
		return out
	}
	beforeList := list()
	before, err := invoke(t, "", "--profile", "deal", "deal", "report", "--deal", v0, "--read-only")
	require.NoError(t, err)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))
	after, err := invoke(t, "", "--profile", "deal", "deal", "report", "--deal", v0, "--read-only")
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, beforeList, list(), "a v1 offer is not a v0 deal on the list")
}

// The move first covers a step a crash left without its cut, at the step's
// own time, since the migration's genesis facts would cut it at the move's.
func TestTheMoveCoversAStepALostCutBeforeItMigrates(t *testing.T) {
	dealFixture(t)
	setDealClock(t, "2026-10-19T09:28:13Z")
	id := retailDeal(t)
	setDealClock(t, "2026-10-19T09:29:07Z")
	crashBeforeTheCut(t, id)
	setDealClock(t, "2026-10-19T10:00:00Z")
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	b, _ := sharedCopy(t, id, cli.DealAudienceCounterparty, "the shop")
	assert.Contains(t, b["checkpoint"].(map[string]any)["timestamp"], "2026-10-19T09:29:07")
	verdict, findings := verdictOf(t, b)
	assert.Equal(t, "VALID", verdict, "%v", findings)
}

// A deal opened before the move keeps working after it: the History card
// counts it, and a tick stamps instead of building a tree.
func TestAV0DealStillCountsOnAMigratedStore(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	id := retailDeal(t)
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"not_received","reason":"not_delivered"}`))
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	*now = now.Add(48 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	own := filepath.Join(t.TempDir(), "own.json")
	dealRun(t, "standard-history", "card", "--out", own)
	deals := shBlock(t, readBundle(t, own))["deals"].([]any)
	require.Len(t, deals, 1)
	// A deal opened after the move is v0 too, and counts.
	id2 := retailDeal(t)
	dealRun(t, "close", "--deal", id2, "--input", writeJSON(t, `{"status":"not_received","reason":"not_delivered"}`))
	*now = now.Add(48 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	own2 := filepath.Join(t.TempDir(), "own2.json")
	dealRun(t, "standard-history", "card", "--out", own2)
	assert.Len(t, shBlock(t, readBundle(t, own2))["deals"].([]any), 2)
}

func TestAV0OpenCannotTakeAV1DealID(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	id := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))["deal_id"].(string)
	msg := v1Err(t, "open", "--deal-id", id, "--input", filepath.Join(retailDemo, "open.json"))
	assert.Contains(t, msg, "x-deal-v1 deal")
	var n int
	require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_keys WHERE deal_id=?`, id).Scan(&n))
	assert.Zero(t, n, "no v0 key was made for a v1 deal's id")
}

func TestACancellationNeedsOnlyTheMigratedStore(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	digest := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))["offer"].(string)
	dealRun(t, "set-version", "x-deal-v0")
	out := dealRun(t, "offer", "cancel", digest, "--out", filepath.Join(t.TempDir(), "c.json"))
	assert.Equal(t, digest, out["cancelled"])
}

func TestAnOfferTakesCounterToAndForumFlags(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	withAdj := strings.Replace(v1OfferInput, `"adjudicator": null,
  "forums": [],`, `"adjudicator": {"key_id": "adjudicator-key", "rules_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "rules_url": "https://rules.test/r"},
  "forums": [],`, 1)
	out := filepath.Join(t.TempDir(), "o.json")
	other := strings.Repeat("c", 64)
	dealRun(t, "offer", "--input", writeJSON(t, withAdj), "--forum", "auto:twin", "--forum", "external:court", "--counter-to", other, "--out", out)
	sent, err := xdealv1.VerifySent(readPair(t, out), nil)
	require.NoError(t, err)
	body := sent.Record.Body.(*xdealv1.ProposeBody)
	assert.Equal(t, []string{"auto:twin", "external:court"}, body.Forums)
	require.NotNil(t, body.CounterTo)
	assert.Equal(t, other, body.CounterTo.Digest)
	// A forum with no adjudicator is the profile's refusal.
	assert.Contains(t, v1Err(t, "offer", "--input", writeJSON(t, v1OfferInput), "--forum", "auto:twin", "--out", filepath.Join(t.TempDir(), "x.json")), "x-deal-v1 checks")
}

// The library's SealSent can fail without sealing (an empty Record: retry),
// or after the record is stored (a Record with the error: its Capsule is
// kept and the file made, never a second seal).
func TestTheSendSurvivesTheLibrarysPartialFailures(t *testing.T) {
	real := sealSentLib
	t.Cleanup(func() { sealSentLib = real })
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	in := writeJSON(t, v1OfferInput)
	out := filepath.Join(t.TempDir(), "o.json")

	// An empty Record: nothing sealed; the row stays sealing; the re-run seals the stored payload once.
	sealSentLib = func(ctx context.Context, shelf *evidencebook.Shelf, e evidencebook.Entry, id string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		return evidencebook.Record{}, evidencebook.SentCapsule{}, errors.New("the journal is busy")
	}
	v1Err(t, "offer", "--input", in, "--out", out)
	var dealID, state string
	require.NoError(t, v1DB(t).QueryRow(`SELECT deal_id, state FROM deal_sent`).Scan(&dealID, &state))
	assert.Equal(t, sentSealing, state)
	sealSentLib = real
	got := dealRun(t, "offer", "--input", in, "--out", out)
	assert.Equal(t, dealID, got["deal_id"], "the stored record is sealed, not a new one")
	_, err := xdealv1.VerifySent(readPair(t, out), nil)
	require.NoError(t, err)
	var n int
	require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_sent`).Scan(&n))
	assert.Equal(t, 1, n)

	// A Record with an error: the Capsule comes with it and the file is made.
	sealSentLib = func(ctx context.Context, shelf *evidencebook.Shelf, e evidencebook.Entry, id string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		rec, sent, err := real(ctx, shelf, e, id)
		require.NoError(t, err)
		return rec, sent, errors.New("the log append is pending")
	}
	in2 := writeJSON(t, strings.Replace(v1OfferInput, `"item": "lamp"`, `"item": "chair"`, 1))
	out2 := filepath.Join(t.TempDir(), "o2.json")
	dealRun(t, "offer", "--input", in2, "--out", out2)
	sealSentLib = real
	_, err = xdealv1.VerifySent(readPair(t, out2), nil)
	require.NoError(t, err)
	require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_sent`).Scan(&n))
	assert.Equal(t, 2, n)
}

// A cancellation sealed whose file was lost is repeated as a new operation.
func TestALostCancellationFileIsRepeatedAsANewOperation(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	digest := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))["offer"].(string)
	first := filepath.Join(t.TempDir(), "c1.json")
	dealRun(t, "offer", "cancel", digest, "--out", first)
	// The process stopped inside the library call: sealed, no pair, not written.
	_, err := openWritableDB(t).Exec(`UPDATE deal_sent SET state=?, pair=NULL, written=0 WHERE kind=?`, sentSealing, xdealv1.KindCancellation)
	require.NoError(t, err)
	second := filepath.Join(t.TempDir(), "c2.json")
	dealRun(t, "offer", "cancel", digest, "--out", second)
	_, err = xdealv1.VerifySent(readPair(t, second), nil)
	require.NoError(t, err)
	var n int
	require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_sent WHERE kind=?`, xdealv1.KindCancellation).Scan(&n))
	assert.Equal(t, 2, n)

	// Interrupted again, within the same second: repeated once more, never stuck.
	_, err = openWritableDB(t).Exec(`UPDATE deal_sent SET state=?, pair=NULL, written=0 WHERE kind=? AND request_digest NOT LIKE '%:superseded:%'`, sentSealing, xdealv1.KindCancellation)
	require.NoError(t, err)
	third := filepath.Join(t.TempDir(), "c3.json")
	dealRun(t, "offer", "cancel", digest, "--out", third)
	_, err = xdealv1.VerifySent(readPair(t, third), nil)
	require.NoError(t, err)
	require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_sent WHERE kind=?`, xdealv1.KindCancellation).Scan(&n))
	assert.Equal(t, 3, n)
}

func TestAnOfferWithNoOutGoesToStandardOutput(t *testing.T) {
	v1Fixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	out, err := invoke(t, "", "--profile", "deal", "deal", "offer", "--input", writeJSON(t, v1OfferInput))
	require.NoError(t, err)
	sent, err := xdealv1.VerifySent([]byte(strings.TrimSpace(out)), nil)
	require.NoError(t, err)
	var state string
	require.NoError(t, v1DB(t).QueryRow(`SELECT state FROM deal_sent WHERE record_digest=?`, sent.Digest).Scan(&state))
	assert.Equal(t, sentWritten, state)
	// A v1 deal's checkpoint status says it is a v1 deal, not that it is unknown.
	assert.Contains(t, v1Err(t, "report", "--deal", sent.Record.Block.DealID, "--read-only"), "x-deal-v1 deal")
}
