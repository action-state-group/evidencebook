// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rotatedWitnessKey is a second key the test witness's directory row lists
// (a key rotation): one witness, one URL, two keys.
func rotatedWitnessKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(seededKey(79)[:32])
}

func keyHexOf(k ed25519.PrivateKey) string { return hex.EncodeToString(k.Public().(ed25519.PublicKey)) }

// twoKeyWitnesses is a directory whose test witness lists two keys.
func twoKeyWitnesses(t *testing.T) aacwitness.Directory {
	t.Helper()
	key, _ := testWitness()
	raw := fmt.Sprintf(`{"witnesses": [{"name": "test witness", "endpoint": %q, "key_ids": [%q, %q]}]}`,
		testWitnessURL, keyHexOf(key), keyHexOf(rotatedWitnessKey()))
	directory, err := aacwitness.ParseDirectory([]byte(raw))
	require.NoError(t, err)
	return directory
}

// receiptUnder is a genuine receipt from the witness at url signed by key.
func receiptUnder(t *testing.T, statement []byte, url string, key ed25519.PrivateKey, iat time.Time) WitnessReceipt {
	t.Helper()
	minted := mintReceiptAt(t, statement, key, iat)
	raw, err := base64.StdEncoding.DecodeString(minted["receipt_b64"].(string))
	require.NoError(t, err)
	return WitnessReceipt{TSURL: url, EntryHash: minted["entry_hash"].(string), Receipt: raw, LeafIndex: 0, TreeSize: 1}
}

// twoKeyShelf is recordTimeShelf's shelf under a cadence whose directory
// lists two keys for the test witness, with the book id of deal/a and its
// two stamps after migration.
func twoKeyShelf(t *testing.T) (*shelfFixture, *Shelf, *Cadence, string, CadenceTick) {
	t.Helper()
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	values := &statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: twoKeyWitnesses(t), Status: values})
	require.NoError(t, err)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	stamp, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	var bookID string
	require.NoError(t, shelf.db.QueryRow(`SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' LIMIT 1`).Scan(&bookID))
	return f, shelf, c, bookID, stamp.Tick
}

// evidencebook#492: two keys of one witness that register the same
// checkpoint both keep their receipts, in either order, so naming either key
// gives its own time, and naming the URL gives the earliest.
func TestTwoKeysOfOneWitnessKeepTheirReceipts(t *testing.T) {
	ctx := context.Background()
	key, _ := testWitness()
	for _, order := range []string{"old key first", "new key first"} {
		t.Run(order, func(t *testing.T) {
			_, shelf, c, bookID, tick := twoKeyShelf(t)
			early, late := tickTime.Add(10*time.Minute), tickTime.Add(20*time.Minute)
			steps := []struct {
				key ed25519.PrivateKey
				at  time.Time
			}{{key, late}, {rotatedWitnessKey(), early}}
			if order == "new key first" {
				steps[0], steps[1] = steps[1], steps[0]
			}
			for _, s := range steps {
				check, err := c.Witnessed(ctx, tick.N, receiptUnder(t, tick.Checkpoint, testWitnessURL, s.key, s.at))
				require.NoError(t, err)
				require.Equal(t, WitnessVerified, check.State, check.Reason)
			}
			assert.Equal(t, 2, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts`))
			byOld, ok, err := shelf.RecordTimeByKey(ctx, bookID, 1, []string{keyHexOf(key)})
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, late, byOld)
			byNew, ok, err := shelf.RecordTimeByKey(ctx, bookID, 1, []string{keyHexOf(rotatedWitnessKey())})
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, early, byNew)
			byURL, ok := recordTime(t, shelf, bookID, 1, testWitnessURL)
			require.True(t, ok)
			assert.Equal(t, early, byURL, "by URL, the earliest across the witness's keys")

			// The handover carries both receipts, and its reader agrees by key.
			_, err = c.Stamp(ctx, tickTime.Add(3*time.Minute))
			require.NoError(t, err)
			h, err := c.Handover(ctx, 0)
			require.NoError(t, err)
			require.Len(t, h.Witnessed, 1)
			assert.Len(t, h.Witnessed[0].Receipts, 2)
			times, err := VerifyHandoverTimes(h, checkpointKeyOf(t, h), twoKeyWitnesses(t))
			require.NoError(t, err)
			got, ok, err := times.RecordTimeByKey(bookID, 1, []string{keyHexOf(key)})
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, late, got)
		})
	}
}

// A shelf written before #492 keyed shelf_receipts by checkpoint and URL
// alone; opening it rebuilds the table, keeps every row, and from then on a
// second key of the same witness keeps its receipt.
func TestAShelfReceiptsTableKeyedByURLIsMigrated(t *testing.T) {
	ctx := context.Background()
	f, shelf, c, bookID, tick := twoKeyShelf(t)
	key, _ := testWitness()
	late := tickTime.Add(20 * time.Minute)
	check, err := c.Witnessed(ctx, tick.N, receiptUnder(t, tick.Checkpoint, testWitnessURL, key, late))
	require.NoError(t, err)
	require.Equal(t, WitnessVerified, check.State)
	// Put the table back in its layout before #492, rows and all.
	for _, statement := range []string{
		`CREATE TABLE old_receipts (checkpoint_sha256 TEXT NOT NULL, witness_id TEXT NOT NULL, key_id TEXT NOT NULL, receipt TEXT NOT NULL, registered_at INTEGER, ` + receiptsKeyedByURL + `)`,
		`INSERT INTO old_receipts SELECT * FROM shelf_receipts`,
		`DROP TABLE shelf_receipts`,
		`ALTER TABLE old_receipts RENAME TO shelf_receipts`,
	} {
		_, err := shelf.db.Exec(statement)
		require.NoError(t, err)
	}
	require.NoError(t, shelf.Release())

	shelf = f.open(t)
	var ddl string
	require.NoError(t, shelf.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'shelf_receipts'`).Scan(&ddl))
	assert.Contains(t, ddl, "PRIMARY KEY (checkpoint_sha256, witness_id, key_id)")
	assert.Equal(t, 1, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts`), "the row is kept")
	got, ok, err := shelf.RecordTimeByKey(ctx, bookID, 1, []string{keyHexOf(key)})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, late, got)

	values := &statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}
	c, err = shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: twoKeyWitnesses(t), Status: values})
	require.NoError(t, err)
	check, err = c.Witnessed(ctx, tick.N, receiptUnder(t, tick.Checkpoint, testWitnessURL, rotatedWitnessKey(), tickTime.Add(10*time.Minute)))
	require.NoError(t, err)
	require.Equal(t, WitnessVerified, check.State)
	assert.Equal(t, 2, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts`), "the other key's receipt is kept beside it")

	// Opening again changes nothing.
	require.NoError(t, shelf.Release())
	shelf = f.open(t)
	assert.Equal(t, 2, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts`))
}
