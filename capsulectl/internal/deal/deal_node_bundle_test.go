package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nodeCheckpoint(t *testing.T, args ...string) map[string]any {
	t.Helper()
	got, err := nodeVerb(t, "checkpoint", args...)
	require.NoError(t, err)
	return got
}

// A node book's checkpoints: the newest is cut now when the book grew, its
// signed statement goes to a new file for a host to submit to its witness, and
// a checkpoint with nothing new to cover is the same one.
func TestDealNodeCheckpoint(t *testing.T) {
	dealFixture(t)
	first, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "checkpoint.cose")
	one := nodeCheckpoint(t, "--out", file)
	assert.EqualValues(t, 1, one["entries"])
	assert.Equal(t, file, one["statement_file"])
	statement, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotEmpty(t, statement)
	again := nodeCheckpoint(t)
	assert.Equal(t, one["id"], again["id"], "nothing new to cover: the same checkpoint")

	_, err = nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n2"}`), "--cite", first.Record.RecordID)
	require.NoError(t, err)
	two := nodeCheckpoint(t)
	assert.EqualValues(t, 2, two["entries"])
	assert.NotEqual(t, one["id"], two["id"], "the book grew, so a newer checkpoint is cut")

	_, err = nodeVerb(t, "checkpoint", "--out", file)
	require.ErrorIs(t, err, cli.ErrInput, "--out is a new file")
}

func nodeBundle(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	return nodeVerb(t, "bundle", args...)
}

func bundlePayloads(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var bundle struct {
		Extensions map[string]json.RawMessage `json:"extensions"`
	}
	require.NoError(t, json.Unmarshal(raw, &bundle))
	var payloads map[string]string
	if held, ok := bundle.Extensions[evidencebook.ExtensionPayloads]; ok {
		require.NoError(t, json.Unmarshal(held, &payloads))
	}
	return payloads
}

func TestDealNodeBundleProvesARecordAndDisclosesOnlyWhenAsked(t *testing.T) {
	dealFixture(t)
	request, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_asked", "--statement", writeJSON(t, `{"request":"r1"}`), "--payload", writeJSON(t, `{"secret":"the request body"}`))
	require.NoError(t, err)
	answer, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_answered", "--statement", writeJSON(t, `{"outcome":"artifact"}`), "--cite", request.Record.RecordID)
	require.NoError(t, err)
	dir := t.TempDir()

	plain, err := nodeBundle(t, "--record", answer.Record.RecordID, "--out", filepath.Join(dir, "plain.json"))
	require.NoError(t, err)
	assert.Equal(t, false, plain["disclosed"])
	assert.Equal(t, false, plain["witnessed"])
	assert.NotContains(t, plain, "cadence_extent", "no witnessed tick carries it yet")
	raw, err := os.ReadFile(filepath.Join(dir, "plain.json"))
	require.NoError(t, err)
	verified, err := evidencebook.VerifyBundle(raw)
	require.NoError(t, err)
	assert.True(t, verified.AnchorAuthenticated)
	assert.NotContains(t, string(raw), "the request body")
	assert.Empty(t, bundlePayloads(t, raw), "without --disclose no payload travels")

	before := len(nodeRecords(t, "--book", "goose/node", "--type", "disclosure"))
	shown, err := nodeBundle(t, "--record", answer.Record.RecordID, "--disclose", "--out", filepath.Join(dir, "shown.json"))
	require.NoError(t, err)
	assert.Equal(t, true, shown["disclosed"])
	raw, err = os.ReadFile(filepath.Join(dir, "shown.json"))
	require.NoError(t, err)
	_, err = evidencebook.VerifyBundle(raw)
	require.NoError(t, err)
	var carried []string
	for _, encoded := range bundlePayloads(t, raw) {
		octets, err := base64.RawURLEncoding.DecodeString(encoded)
		require.NoError(t, err)
		carried = append(carried, string(octets))
	}
	assert.Contains(t, strings.Join(carried, "|"), "the request body", "--disclose carries the cited record's payload")
	assert.Equal(t, before+1, len(nodeRecords(t, "--book", "goose/node", "--type", "disclosure")), "the book records what it disclosed")
	assert.NotEmpty(t, shown["disclosure_record"])
}

// A bundle under an earlier checkpoint proves the record at that size, not the newest.
func TestDealNodeBundleUnderAnEarlierCheckpoint(t *testing.T) {
	dealFixture(t)
	first, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)
	early := nodeCheckpoint(t)
	_, err = nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n2"}`))
	require.NoError(t, err)
	nodeCheckpoint(t)
	got, err := nodeBundle(t, "--record", first.Record.RecordID, "--at", early["id"].(string), "--out", filepath.Join(t.TempDir(), "b.json"))
	require.NoError(t, err)
	assert.Equal(t, early["id"], got["checkpoint"])
	assert.EqualValues(t, 1, got["entries"])
}

func TestDealNodeBundleRefusals(t *testing.T) {
	dealFixture(t)
	record, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)
	dir := t.TempDir()
	existing := writeJSON(t, `keep`)
	receipt := writeJSON(t, `{"ts_url":"https://witness.example","entry_hash":"00","receipt_b64":"AA==","leaf_index":0,"tree_size":1}`)
	directory := writeJSON(t, `{"witnesses":[{"name":"w","endpoint":"https://witness.example","keys":[]}]}`)
	cp := nodeCheckpoint(t)["id"].(string)
	for name, args := range map[string][]string{
		"no record":                      {"--out", filepath.Join(dir, "a.json")},
		"no out":                         {"--record", record.Record.RecordID},
		"a record the book lacks":        {"--record", "0000", "--out", filepath.Join(dir, "b.json")},
		"an existing out":                {"--record", record.Record.RecordID, "--out", existing},
		"an unknown checkpoint":          {"--record", record.Record.RecordID, "--at", "nope", "--out", filepath.Join(dir, "c.json")},
		"a receipt with no directory":    {"--record", record.Record.RecordID, "--at", cp, "--witness-receipt", receipt, "--out", filepath.Join(dir, "d.json")},
		"a receipt with no --at":         {"--record", record.Record.RecordID, "--witness-receipt", receipt, "--witness-directory", directory, "--out", filepath.Join(dir, "e.json")},
		"a receipt that does not verify": {"--record", record.Record.RecordID, "--at", cp, "--witness-receipt", receipt, "--witness-directory", directory, "--out", filepath.Join(dir, "f.json")},
		"a receipt that is not JSON":     {"--record", record.Record.RecordID, "--at", cp, "--witness-receipt", writeJSON(t, `nope`), "--witness-directory", directory, "--out", filepath.Join(dir, "g.json")},
	} {
		_, err := nodeBundle(t, args...)
		require.ErrorIs(t, err, cli.ErrInput, name)
	}
	_, err = nodeBundle(t, "--record", record.Record.RecordID, "--at", cp, "--witness-receipt", receipt, "--witness-directory", directory, "--out", filepath.Join(dir, "f2.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "receipt", "the refusal is the library's: the carried receipt does not verify under the directory")
	kept, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(kept))
	matches, err := filepath.Glob(filepath.Join(dir, "*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "a refused bundle writes no file and leaves no temp file")
	_, err = invoke(t, "", "--profile", "deal", "deal", "node", "bundle", "--book", "deal/deal-0000000000000000", "--record", "x", "--out", filepath.Join(dir, "h.json"))
	require.ErrorIs(t, err, cli.ErrInput)
}

// A misspelled --book is an input error and makes no new empty log the cadence would commit.
func TestDealNodeCheckpointAndBundleDoNotCreateABook(t *testing.T) {
	dealFixture(t)
	record, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)
	for _, verb := range [][]string{
		{"checkpoint", "--book", "goose/nod"},
		{"checkpoint", "--book", "goose/nod", "--list"},
		{"bundle", "--book", "goose/nod", "--record", record.Record.RecordID, "--out", filepath.Join(t.TempDir(), "b.json")},
	} {
		_, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "node"}, verb...)...)
		require.ErrorIs(t, err, cli.ErrInput, verb[0])
	}
	assert.NotContains(t, nodeRecordsBooks(t), "goose/nod")
}

// --disclose follows one hop, not the library's default two: a record's bundle
// reveals what the record links to, not what those records link to.
func TestDealNodeBundleDiscloseFollowsOneHop(t *testing.T) {
	dealFixture(t)
	a, err := nodeAppend(t, "--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `{"n":"a"}`), "--payload", writeJSON(t, `{"body":"payload of A"}`))
	require.NoError(t, err)
	b, err := nodeAppend(t, "--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `{"n":"b"}`), "--payload", writeJSON(t, `{"body":"payload of B"}`), "--cite", a.Record.RecordID)
	require.NoError(t, err)
	c, err := nodeAppend(t, "--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `{"n":"c"}`), "--cite", b.Record.RecordID)
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "b.json")
	_, err = nodeBundle(t, "--record", c.Record.RecordID, "--disclose", "--out", out)
	require.NoError(t, err)
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var carried []string
	for _, encoded := range bundlePayloads(t, raw) {
		octets, err := base64.RawURLEncoding.DecodeString(encoded)
		require.NoError(t, err)
		carried = append(carried, string(octets))
	}
	joined := strings.Join(carried, "|")
	assert.Contains(t, joined, "payload of B", "the record it links to is disclosed")
	assert.NotContains(t, joined, "payload of A", "what that record links to is not")
}

// With a witnessed cadence tick the chain travels in the bundle; --list says which checkpoints are carried.
func TestDealNodeBundleCarriesTheCadenceChain(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	first, err := nodeAppend(t, "--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `{"n":"1"}`))
	require.NoError(t, err)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)

	listed := func() []map[string]any {
		got, err := nodeVerb(t, "checkpoint", "--list")
		require.NoError(t, err)
		var rows []map[string]any
		for _, r := range got["checkpoints"].([]any) {
			rows = append(rows, r.(map[string]any))
		}
		return rows
	}
	rows := listed()
	require.NotEmpty(t, rows)
	assert.Equal(t, true, rows[len(rows)-1]["cadence_witnessed"], "the tick carries the book's checkpoint")
	assert.Equal(t, "all", rows[len(rows)-1]["cadence_extent"])
	carried := rows[len(rows)-1]["id"].(string)

	out := filepath.Join(t.TempDir(), "all.json")
	got, err := nodeBundle(t, "--record", first.Record.RecordID, "--out", out)
	require.NoError(t, err)
	assert.Equal(t, "all", got["cadence_extent"])
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "x-cadence-witness/v0")
	_, err = evidencebook.VerifyBundle(raw)
	require.NoError(t, err)

	// The book grows: the newest checkpoint is new and carried only as an earlier checkpoint
	// plus a consistency proof ("part"); --at names the one the tick carried ("all").
	_, err = nodeAppend(t, "--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `{"n":"2"}`))
	require.NoError(t, err)
	part, err := nodeBundle(t, "--record", first.Record.RecordID, "--out", filepath.Join(t.TempDir(), "part.json"))
	require.NoError(t, err)
	assert.Equal(t, "part", part["cadence_extent"])
	exact, err := nodeBundle(t, "--record", first.Record.RecordID, "--at", carried, "--out", filepath.Join(t.TempDir(), "exact.json"))
	require.NoError(t, err)
	assert.Equal(t, "all", exact["cadence_extent"])
	assert.Equal(t, carried, exact["checkpoint"])
	assert.Greater(t, len(listed()), len(rows), "the bundles above cut newer checkpoints as the book grew")
}
