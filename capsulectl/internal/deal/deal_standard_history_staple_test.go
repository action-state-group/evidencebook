package deal

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stapleRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return invoke(t, "", append([]string{"--profile", "deal", "deal", "standard-history", "staple"}, args...)...)
}

// stapleFixture is a person who answers the card, with one deal and a tick.
func stapleFixture(t *testing.T) string {
	t.Helper()
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	id := retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "person", "--answer")
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	return id
}

// The contract's refusals are exit 2 with nothing on stdout.
func TestStapleRefusesBadInputWithExit2(t *testing.T) {
	stapleFixture(t)
	for name, args := range map[string][]string{
		"no derivation":    {},
		"a third":          {"--derivation", "deal_ledger/1"},
		"the empty string": {"--derivation", ""},
		"unknown deal":     {"--derivation", "standard_history/1", "--deal", "deal-00000000000000000000000000000000"},
		"not a deal id":    {"--derivation", "standard_history/1", "--deal", "x"},
	} {
		out, err := stapleRun(t, args...)
		require.Error(t, err, name)
		assert.Equal(t, 2, cli.ExitCode(err), name)
		assert.Empty(t, out, name)
	}
}

func TestStapleNeedsADeclaredHolderThatAnswers(t *testing.T) {
	dealFixture(t)
	clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	out, err := stapleRun(t, "--derivation", "standard_history/1")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "declare")
	assert.Empty(t, out)

	dealRun(t, "standard-history", "declare", "--holder-kind", "person")
	out, err = stapleRun(t, "--derivation", "standard_history/1")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err), "a person who answers no card staples none")
	assert.Empty(t, out)
}

// With no tick there is no checkpoint to be as of: any other failure, exit 1.
func TestStapleNeedsATick(t *testing.T) {
	dealFixture(t)
	clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealRun(t, "standard-history", "declare", "--holder-kind", "person", "--answer")
	out, err := stapleRun(t, "--derivation", "standard_history/1")
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "deal tick")
	assert.Empty(t, out)
}

type stapleDoc struct {
	Derivation string `json:"derivation"`
	DealID     string `json:"deal_id"`
	Staple     struct {
		Request json.RawMessage `json:"request"`
		Reply   json.RawMessage `json:"reply"`
	} `json:"staple"`
}

func readStaple(t *testing.T, out string) (stapleDoc, evidencebook.Staple) {
	t.Helper()
	var doc stapleDoc
	require.NoError(t, json.Unmarshal([]byte(out), &doc), out)
	var reply bytes.Buffer
	require.NoError(t, json.Compact(&reply, doc.Staple.Reply))
	var request bytes.Buffer
	require.NoError(t, json.Compact(&request, doc.Staple.Request))
	s := evidencebook.Staple{
		Request: evidencerequest.Message{Octets: request.Bytes(), MediaType: evidencerequest.MediaTypeJSON},
	}
	parsed, err := evidencerequest.ParseReply(evidencerequest.Message{Octets: reply.Bytes(), MediaType: evidencerequest.MediaTypeJSON})
	require.NoError(t, err)
	s.Reply = parsed
	return doc, s
}

// A standard_history/1 staple is the one CheckStaple accepts, and names the
// deal it was attached to; each call carries a fresh nonce.
func TestStapleStandardHistoryPassesCheckStaple(t *testing.T) {
	id := stapleFixture(t)
	out, err := stapleRun(t, "--derivation", "standard_history/1", "--deal", id)
	require.NoError(t, err, out)
	doc, s := readStaple(t, out)
	assert.Equal(t, "standard_history/1", doc.Derivation)
	assert.Equal(t, id, doc.DealID)
	require.NotNil(t, s.Reply.Envelope)
	check, err := evidencebook.CheckStaple(s, s.Reply.Envelope.Responder)
	require.NoError(t, err)
	assert.Equal(t, "standard_history/1", check.Derivation)

	out2, err := stapleRun(t, "--derivation", "standard_history/1")
	require.NoError(t, err)
	doc2, s2 := readStaple(t, out2)
	assert.Empty(t, doc2.DealID, "deal_id is present only when --deal was given")
	assert.NotContains(t, out2, `"deal_id"`)
	check2, err := evidencebook.CheckStaple(s2, s2.Reply.Envelope.Responder)
	require.NoError(t, err)
	assert.NotEqual(t, check.Nonce, check2.Nonce)
	assert.Equal(t, check.Anchor, check2.Anchor, "both are as of the newest checkpoint")
}

// A history_card/1 staple passes CheckStaple, needs no declaration and seals
// nothing; with no tick it is exit 1.
func TestStapleHistoryCardPassesCheckStaple(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	out, err := stapleRun(t, "--derivation", "history_card/1")
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Empty(t, out)

	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	id := retailDeal(t)
	out, err = stapleRun(t, "--derivation", "history_card/1", "--deal", id)
	require.NoError(t, err, out)
	doc, s := readStaple(t, out)
	assert.Equal(t, "history_card/1", doc.Derivation)
	assert.NotContains(t, out, `"deal_id"`, "--deal is ignored for history_card/1")
	require.NotNil(t, s.Reply.Envelope)
	check, err := evidencebook.CheckStaple(s, s.Reply.Envelope.Responder)
	require.NoError(t, err)
	assert.Equal(t, "history_card/1", check.Derivation)
}

// answeredRows is the deal's sealed exchange rows in the store.
func answeredRows(t *testing.T, dealID string) int {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	db, err := sql.Open("sqlite", "file:"+p.Connection.Database+"?mode=ro")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM deal_requests WHERE deal_id=? AND kind=?`, dealID, dealExchangeAnswered).Scan(&n))
	return n
}

// Only a standard_history/1 staple seals a record, one, naming the deal; a
// history_card/1 staple and a refused one seal none. Both kinds are signed by
// the profile's own key.
func TestStapleSealsOnlyTheStandardHistoryRecord(t *testing.T) {
	id := stapleFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	key, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	signer, err := evidencebook.NewEd25519Signer(key)
	require.NoError(t, err)
	holder := evidencebook.KeyID(signer)

	out, err := stapleRun(t, "--derivation", "history_card/1", "--deal", id)
	require.NoError(t, err, out)
	assert.Equal(t, 0, answeredRows(t, id))
	_, card := readStaple(t, out)
	_, err = evidencebook.CheckStaple(card, holder)
	require.NoError(t, err, "the history card is signed by the profile's key")

	out, err = stapleRun(t, "--derivation", "standard_history/1", "--deal", id)
	require.NoError(t, err, out)
	assert.Equal(t, 1, answeredRows(t, id))
	_, std := readStaple(t, out)
	_, err = evidencebook.CheckStaple(std, holder)
	require.NoError(t, err, "the standard card is signed by the profile's key")

	_, err = stapleRun(t, "--derivation", "standard_history/1", "--deal", "deal-00000000000000000000000000000000")
	require.Error(t, err)
	assert.Equal(t, 1, answeredRows(t, id), "a refused staple seals nothing")
}
