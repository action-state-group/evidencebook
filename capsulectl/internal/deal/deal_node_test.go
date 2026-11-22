package deal

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nodeAppendOut struct {
	Existing bool       `json:"existing"`
	Record   nodeRecord `json:"record"`
}

func nodeAppend(t *testing.T, args ...string) (nodeAppendOut, error) {
	t.Helper()
	out, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "node", "append"}, args...)...)
	var got nodeAppendOut
	if err == nil {
		require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	}
	return got, err
}

func nodeRecords(t *testing.T, args ...string) []nodeRecord {
	t.Helper()
	out, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "node", "records"}, args...)...)
	require.NoError(t, err, out)
	var got struct {
		Records []nodeRecord `json:"records"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	return got.Records
}

func TestDealNodeAppendAndRecords(t *testing.T) {
	dealFixture(t)
	joined, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)
	assert.False(t, joined.Existing)
	assert.Equal(t, "goose_joined", joined.Record.RecordType)
	assert.EqualValues(t, 1, joined.Record.Seq)

	asked, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_asked", "--subject-ref", "req-1",
		"--statement", writeJSON(t, `{"request":"r1"}`), "--cite", joined.Record.RecordID, "--payload", writeJSON(t, `{"p":1}`))
	require.NoError(t, err)
	assert.EqualValues(t, 2, asked.Record.Seq)

	all := nodeRecords(t, "--book", "goose/node")
	require.Len(t, all, 2, "oldest first")
	assert.Equal(t, []string{"goose_joined", "goose_asked"}, []string{all[0].RecordType, all[1].RecordType})
	assert.JSONEq(t, `{"request":"r1"}`, string(all[1].Statement))
	assert.Equal(t, "req-1", all[1].SubjectRef)
	assert.Equal(t, []string{joined.Record.RecordID}, all[1].Cites)

	only := nodeRecords(t, "--book", "goose/node", "--type", "goose_asked")
	require.Len(t, only, 1)
	assert.Equal(t, asked.Record.RecordID, only[0].RecordID)

	assert.Empty(t, nodeRecords(t, "--book", "goose/never-written"), "a book that does not exist lists nothing and is not created")
	out, err := invoke(t, "", "--profile", "deal", "deal", "node", "records", "--book", "goose/never-written")
	require.NoError(t, err, out)
	assert.NotContains(t, nodeRecordsBooks(t), "goose/never-written")
}

func nodeRecordsBooks(t *testing.T) []string {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	books, err := s.shelf.Books(t.Context())
	require.NoError(t, err)
	return books
}

func TestDealNodeAppendOnceRepeatsSafely(t *testing.T) {
	dealFixture(t)
	args := []string{"--book", "goose/node", "--type", "goose_dispute_received", "--once", "result_digest",
		"--statement", writeJSON(t, `{"result_digest":"abc","note":"first"}`)}
	first, err := nodeAppend(t, args...)
	require.NoError(t, err)
	assert.False(t, first.Existing)

	again, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_dispute_received", "--once", "result_digest",
		"--statement", writeJSON(t, `{"note":"second","result_digest":"abc"}`))
	require.NoError(t, err)
	assert.True(t, again.Existing, "the same member value, in any key order, seals nothing")
	assert.Equal(t, first.Record.RecordID, again.Record.RecordID)

	other, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_dispute_received", "--once", "result_digest",
		"--statement", writeJSON(t, `{"result_digest":"def"}`))
	require.NoError(t, err)
	assert.False(t, other.Existing, "another value is another record")

	sameValueOtherType, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_dispute_refused", "--once", "result_digest",
		"--statement", writeJSON(t, `{"result_digest":"abc"}`))
	require.NoError(t, err)
	assert.False(t, sameValueOtherType.Existing, "--once looks at this type only")
	assert.Len(t, nodeRecords(t, "--book", "goose/node"), 3)
}

func TestDealNodeRefusesTheShelfsOwnLogs(t *testing.T) {
	dealFixture(t)
	dealID := retailDeal(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	for _, book := range []string{"deal/" + dealID, "deal/deal-0000000000000000", "deal-cadence/x", p.LogID, "journal", "deal/" + dealID + "/results", ""} {
		_, err := nodeAppend(t, "--book", book, "--type", "t", "--statement", writeJSON(t, `{"a":1}`))
		require.ErrorIs(t, err, cli.ErrInput, "book %q", book)
		_, err = invoke(t, "", "--profile", "deal", "deal", "node", "records", "--book", book)
		require.ErrorIs(t, err, cli.ErrInput, "book %q", book)
	}
	listed, err := invoke(t, "", "--profile", "deal", "deal", "list")
	require.NoError(t, err, listed)
	assert.Contains(t, listed, dealID, "the deal is unharmed")
}

func TestDealNodeAppendInputErrors(t *testing.T) {
	dealFixture(t)
	good := writeJSON(t, `{"a":1}`)
	for name, args := range map[string][]string{
		"no type":                     {"--book", "goose/node", "--statement", good},
		"no statement":                {"--book", "goose/node", "--type", "t"},
		"statement not an object":     {"--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `[1]`)},
		"statement not json":          {"--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `nope`)},
		"statement null":              {"--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `null`)},
		"a library record type":       {"--book", "goose/node", "--type", "index_root", "--statement", good},
		"payload from standard input": {"--book", "goose/node", "--type", "t", "--statement", good, "--payload", "-"},
		"once names a missing member": {"--book", "goose/node", "--type", "t", "--statement", good, "--once", "b"},
		"missing payload file":        {"--book", "goose/node", "--type", "t", "--statement", good, "--payload", "/nonexistent/file"},
	} {
		_, err := nodeAppend(t, args...)
		require.Error(t, err, name)
	}
	assert.Empty(t, nodeRecords(t, "--book", "goose/node"), "a refused append seals nothing")
	assert.NotContains(t, nodeRecordsBooks(t), "goose/node", "no book is created by a refused append")
}

func TestDealNodeAppendReadsStandardInput(t *testing.T) {
	dealFixture(t)
	out, err := invoke(t, `{"from":"stdin"}`, "--profile", "deal", "deal", "node", "append", "--book", "goose/node", "--type", "t", "--statement", "-")
	require.NoError(t, err, out)
	records := nodeRecords(t, "--book", "goose/node")
	require.Len(t, records, 1)
	assert.JSONEq(t, `{"from":"stdin"}`, string(records[0].Statement))
}

// Node records go through the same session as every deal verb, so the store's
// mutation counter stays consistent: the deal verbs run before and after them
// without a repair or a "changed outside capsulectl" refusal.
func TestDealNodeAppendKeepsTheStoreConsistentForDealVerbs(t *testing.T) {
	dealFixture(t)
	first := retailDeal(t)
	_, err := nodeAppend(t, "--book", "goose/node", "--type", "t", "--statement", writeJSON(t, `{"a":1}`))
	require.NoError(t, err)
	second := dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))["deal_id"].(string)
	dealRun(t, "check", "--deal", second, "--input", filepath.Join(retailDemo, "check-pay.json"))
	listed, err := invoke(t, "", "--profile", "deal", "deal", "list")
	require.NoError(t, err, listed)
	assert.Contains(t, listed, first)
	assert.Contains(t, listed, second)
	assert.Len(t, nodeRecords(t, "--book", "goose/node"), 1)
}

func TestDealNodeAppendNamedLibraryTypesAreRefused(t *testing.T) {
	dealFixture(t)
	for typ := range libraryRecordTypes {
		_, err := nodeAppend(t, "--book", "goose/node", "--type", typ, "--statement", writeJSON(t, `{"index":1}`))
		require.ErrorIs(t, err, cli.ErrInput, typ)
	}
	_, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"a":1}`))
	require.NoError(t, err, "the book still opens after the refused types")
}

// --once compares numbers exactly, as values: 1 and 1.0 are one value, and a
// number is never rounded through a float64 (so a statement the library would
// refuse for an unsafe integer is refused, not reported as already sealed).
func TestDealNodeAppendOnceComparesNumbersAsValues(t *testing.T) {
	dealFixture(t)
	seal := func(statement string) (nodeAppendOut, error) {
		return nodeAppend(t, "--book", "goose/node", "--type", "t", "--once", "id", "--statement", writeJSON(t, statement))
	}
	first, err := seal(`{"id":{"n":[1,9007199254740991]}}`)
	require.NoError(t, err)
	assert.False(t, first.Existing)
	again, err := seal(`{"id":{"n":[1.0,9007199254740991]}}`)
	require.NoError(t, err)
	assert.True(t, again.Existing, "1 and 1.0 are the same value")
	other, err := seal(`{"id":{"n":[1,9007199254740990]}}`)
	require.NoError(t, err)
	assert.False(t, other.Existing, "another number is another record")
	_, err = seal(`{"id":{"n":[1,9007199254740992]}}`)
	require.Error(t, err, "an unsafe integer is refused, not matched to 2^53-1")
	assert.Len(t, nodeRecords(t, "--book", "goose/node"), 2)
}
