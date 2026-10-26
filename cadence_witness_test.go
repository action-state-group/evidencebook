// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
)

// otherWitnessURL is a second witness the test directory lists.
const otherWitnessURL = "https://other-witness.test"

func otherWitnessKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(seededKey(78)[:32])
}

// testWitnesses is the cadence's witness directory: the local test witness
// (testWitness) and otherWitnessURL.
func testWitnesses(t *testing.T) aacwitness.Directory {
	t.Helper()
	key, _ := testWitness()
	raw := fmt.Sprintf(`{"witnesses": [{"name": "test witness", "endpoint": %q, "key_ids": [%q]}, {"name": "other witness", "endpoint": %q, "key_ids": [%q]}]}`,
		testWitnessURL, hex.EncodeToString(key.Public().(ed25519.PublicKey)),
		otherWitnessURL, hex.EncodeToString(otherWitnessKey().Public().(ed25519.PublicKey)))
	directory, err := aacwitness.ParseDirectory([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

// receiptFor is a genuine receipt over the cadence checkpoint statement from
// the witness at url: the test witness's key for testWitnessURL, the other
// witness's key for any other URL.
func receiptFor(t *testing.T, statement []byte, url string) WitnessReceipt {
	t.Helper()
	key, _ := testWitness()
	if url != testWitnessURL {
		key = otherWitnessKey()
	}
	minted := mintReceipt(t, statement, key)
	raw, err := base64.StdEncoding.DecodeString(minted["receipt_b64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return WitnessReceipt{TSURL: url, EntryHash: minted["entry_hash"].(string), Receipt: raw, LeafIndex: 0, TreeSize: 1}
}

// mustWitness reports a genuine receipt for tick n, which must verify.
func mustWitness(t *testing.T, c *Cadence, n uint64) {
	t.Helper()
	tick, err := c.Get(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	check, err := c.Witnessed(context.Background(), n, receiptFor(t, tick.Checkpoint, testWitnessURL))
	if err != nil || check.State != WitnessVerified || strings.HasPrefix(check.Reason, ":") || !strings.HasPrefix(check.Reason, "ok") {
		t.Fatalf("witnessing tick %d: %+v %v", n, check, err)
	}
}

// witnessLegacy marks a pre-shelf tick witnessed with a genuine receipt.
func witnessLegacy(t *testing.T, tick *CadenceTick) {
	t.Helper()
	receipt := receiptFor(t, tick.Checkpoint, testWitnessURL)
	tick.Witnessed, tick.Receipt = true, &receipt
}

// unwitnessed requires that tick n is not witnessed and nothing is indexed.
func unwitnessed(t *testing.T, c *Cadence, n uint64) {
	t.Helper()
	tick, err := c.Get(context.Background(), n)
	if err != nil || tick.Witnessed || tick.Receipt != nil {
		t.Fatalf("tick %d: %+v %v", n, tick, err)
	}
	if rows := countRows(t, c.shelf, `SELECT COUNT(*) FROM cadence_witnessed`); rows != 0 {
		t.Fatalf("%d index rows", rows)
	}
}

// A receipt from a witness the directory does not list, or under a
// directory that lists none, is NOT_CHECKED with AAC's reason: no error,
// the tick stays unwitnessed and its logs stay live. The same tick is
// witnessed once a receipt verifies.
func TestAReceiptNoDirectoryKeyCoversIsNotChecked(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, e := newCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("s", "1"))
	e.set("deal/a")
	tick := mustTick(t, c, tickTime)
	stranger := receiptFor(t, tick.Checkpoint, "https://stranger.test")
	check, err := c.Witnessed(ctx, tick.N, stranger)
	if err != nil || check.State != WitnessNotChecked || !strings.Contains(check.Reason, "witness_unverified:https://stranger.test") || !strings.Contains(check.Reason, "no directory row") {
		t.Fatalf("an unlisted witness: %+v %v", check, err)
	}
	unwitnessed(t, c, tick.N)
	if rows := countRows(t, shelf, `SELECT COUNT(*) FROM cadence_live WHERE log_id = 'deal/a'`); rows != 1 {
		t.Fatalf("deal/a left the live set on an unchecked receipt")
	}
	if _, err := c.Chain(ctx, tick.Checkpoint); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a chain from an unchecked receipt: %v", err)
	}

	// A cadence configured with no witness directory checks no receipt.
	bareShelf := newShelfFixture(t).open(t)
	bare, err := bareShelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef", Ended: e.check})
	if err != nil {
		t.Fatal(err)
	}
	bareTick := mustTick(t, bare, tickTime)
	if check, err := bare.Witnessed(ctx, bareTick.N, receiptFor(t, bareTick.Checkpoint, testWitnessURL)); err != nil || check.State != WitnessNotChecked {
		t.Fatalf("no directory: %+v %v", check, err)
	}
	unwitnessed(t, bare, bareTick.N)

	mustWitness(t, c, tick.N)
	if rows := countRows(t, shelf, `SELECT COUNT(*) FROM cadence_live WHERE log_id = 'deal/a'`); rows != 0 {
		t.Fatalf("deal/a still live after a verified receipt")
	}
}

// A receipt that is checked and fails is INVALID: an ErrInvalid naming
// AAC's reason, and nothing is stored.
func TestAReceiptThatFailsIsRejectedAndNamed(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("s", "1"))
	first := mustTick(t, c, tickTime)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("s", "2"))
	second := mustTick(t, c, first.DueNext)
	forged := receiptFor(t, first.Checkpoint, testWitnessURL)
	forged.Receipt = append([]byte(nil), forged.Receipt...)
	forged.Receipt[len(forged.Receipt)-1] ^= 1
	for name, receipt := range map[string]WitnessReceipt{
		"another tick's receipt": receiptFor(t, second.Checkpoint, testWitnessURL),
		"a forged signature":     forged,
		"another key's receipt":  {TSURL: testWitnessURL, EntryHash: forged.EntryHash, Receipt: receiptFor(t, first.Checkpoint, otherWitnessURL).Receipt},
		"not a receipt":          {TSURL: testWitnessURL, EntryHash: forged.EntryHash, Receipt: []byte("receipt")},
	} {
		check, err := c.Witnessed(ctx, first.N, receipt)
		if !errors.Is(err, ErrInvalid) || check.State != WitnessInvalid || !strings.Contains(check.Reason, "witness_receipt_invalid:"+testWitnessURL) || !strings.Contains(err.Error(), check.Reason) {
			t.Fatalf("%s: %+v %v", name, check, err)
		}
		unwitnessed(t, c, first.N)
	}
}

// ImportLegacy verifies a witnessed pre-shelf tick's receipt as Witnessed
// does, and refuses the import, naming the tick, when it is not VERIFIED.
func TestImportLegacyRefusesAReceiptThatDoesNotVerify(t *testing.T) {
	ctx := context.Background()
	for name, receipt := range map[string]func(l *legacyFixture) WitnessReceipt{
		"NOT_CHECKED": func(l *legacyFixture) WitnessReceipt {
			return receiptFor(t, l.ticks[0].Checkpoint, "https://stranger.test")
		},
		"INVALID": func(l *legacyFixture) WitnessReceipt { return receiptFor(t, l.ticks[1].Checkpoint, testWitnessURL) },
	} {
		l := newLegacyFixture(t, "deal/a")
		bad := receipt(l)
		l.ticks[0].Receipt = &bad
		err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "its receipt is "+name) || !strings.Contains(err.Error(), fmt.Sprintf("entry %d", l.ticks[0].EntrySeq)) {
			t.Fatalf("%s: %v", name, err)
		}
		if rows := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_ticks`); rows != 0 {
			t.Fatalf("%s: %d ticks imported", name, rows)
		}
	}
}

// A re-run does not check a tick it already holds witnessed again: the held
// tick's receipt no longer verifying (a witness rotated its key) does not
// stop the import of what is new.
func TestImportLegacyDoesNotRecheckAHeldWitnessedTick(t *testing.T) {
	ctx := context.Background()
	l := newLegacyFixture(t, "deal/a")
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks[:1]}); err != nil {
		t.Fatal(err)
	}
	stale := receiptFor(t, l.ticks[1].Checkpoint, testWitnessURL)
	l.ticks[0].Receipt = &stale
	witnessLegacy(t, &l.ticks[1])
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	for n := uint64(1); n <= 2; n++ {
		tick, err := l.c.Get(ctx, n)
		if err != nil || !tick.Witnessed {
			t.Fatalf("tick %d: %+v %v", n, tick, err)
		}
	}
	// The held tick keeps the receipt that verified when it was imported.
	first, err := l.c.Get(ctx, 1)
	if err != nil || first.Receipt == nil || bytes.Equal(first.Receipt.Receipt, stale.Receipt) {
		t.Fatalf("tick 1's receipt: %+v %v", first.Receipt, err)
	}
}

// A held tick promoted to witnessed on a re-run has its receipt checked.
func TestImportLegacyChecksAPromotedTicksReceipt(t *testing.T) {
	ctx := context.Background()
	l := newLegacyFixture(t, "deal/a")
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	wrong := receiptFor(t, l.ticks[0].Checkpoint, testWitnessURL)
	l.ticks[1].Witnessed, l.ticks[1].Receipt = true, &wrong
	err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "its receipt is INVALID") {
		t.Fatalf("promoting with another tick's receipt: %v", err)
	}
	if second, err := l.c.Get(ctx, 2); err != nil || second.Witnessed {
		t.Fatalf("tick 2: %+v %v", second, err)
	}
}
