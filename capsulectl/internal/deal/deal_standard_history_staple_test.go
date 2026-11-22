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

// Offer staples carry history_card/2 only: --derivation accepts nothing
// else, with the reason for the two a staple used to carry, and --deal is
// gone. Refusals are exit 2 with nothing on stdout.
func TestStapleRefusesWhatIsNoLongerStapled(t *testing.T) {
	dealFixture(t)
	for _, d := range []string{"history_card/1", "standard_history/1"} {
		out, err := stapleRun(t, "--derivation", d)
		require.Error(t, err, d)
		assert.Equal(t, 2, cli.ExitCode(err), d)
		assert.Contains(t, err.Error(), "a staple carries history_card/2 only: "+d+" is no longer stapled", d)
		assert.Contains(t, err.Error(), "deal respond", d)
		assert.Empty(t, out, d)
	}
	for _, d := range []string{"history_card/3", ""} {
		out, err := stapleRun(t, "--derivation", d)
		require.Error(t, err, d)
		assert.Equal(t, 2, cli.ExitCode(err), d)
		assert.Contains(t, err.Error(), "--derivation \""+d+"\" is not a derivation a staple carries", d)
		assert.NotContains(t, err.Error(), "no longer stapled", "the reason only for the two it used to carry")
		assert.Empty(t, out, d)
	}
	out, err := stapleRun(t, "--deal", "deal-0123456789abcdef")
	require.Error(t, err, "--deal is gone")
	assert.Empty(t, out)
}

func TestStapleNeedsATick(t *testing.T) {
	dealFixture(t)
	clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	out, err := stapleRun(t)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "deal tick")
	assert.Empty(t, out)
}

type stapleDoc struct {
	Derivation string `json:"derivation"`
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

// The staple is history_card/2 with or without --derivation: it passes
// CheckStaple for the profile's key, needs no declaration, seals nothing,
// and its output keeps the shape {derivation, staple: {request, reply}}.
func TestStapleIsHistoryCard2(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	id := retailDeal(t)
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	key, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	signer, err := evidencebook.NewEd25519Signer(key)
	require.NoError(t, err)

	for _, args := range [][]string{nil, {"--derivation", "history_card/2"}} {
		out, err := stapleRun(t, args...)
		require.NoError(t, err, out)
		var shape map[string]map[string]json.RawMessage
		var top map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(out), &top))
		assert.Subset(t, rawKeys(top), []string{"derivation", "staple"}, "the output keeps its members")
		assert.NotContains(t, rawKeys(top), "deal_id", "--deal is gone")
		require.NoError(t, json.Unmarshal([]byte(`{"s":`+string(top["staple"])+`}`), &shape))
		assert.ElementsMatch(t, []string{"request", "reply"}, rawKeys(shape["s"]), "the staple's members")

		doc, s := readStaple(t, out)
		assert.Equal(t, evidencebook.DerivationHistoryCard2, doc.Derivation)
		check, err := evidencebook.CheckStaple(s, evidencebook.KeyID(signer))
		require.NoError(t, err, "signed by the profile's own key")
		assert.Equal(t, evidencebook.DerivationHistoryCard2, check.Derivation)
	}
	assert.Equal(t, 0, answeredRows(t, id), "a staple seals nothing for the deal")
	assert.Equal(t, 0, allAnsweredRows(t), "nor any answered record at all")
}

// A store moved to the new layout staples the same card: ticks after the
// move are stamps on the same cadence log.
func TestStapleOnAMigratedStore(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	*now = now.Add(10 * time.Minute)
	dealRun(t, "tick")
	out, err := stapleRun(t)
	require.NoError(t, err, out)
	_, s := readStaple(t, out)
	require.NotNil(t, s.Reply.Envelope)
	check, err := evidencebook.CheckStaple(s, s.Reply.Envelope.Responder)
	require.NoError(t, err)
	assert.Equal(t, evidencebook.DerivationHistoryCard2, check.Derivation)
}

// rawKeys is a JSON object's member names.
func rawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// allAnsweredRows is every sealed answered row in the store, whatever its deal.
func allAnsweredRows(t *testing.T) int {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	db, err := sql.Open("sqlite", "file:"+p.Connection.Database+"?mode=ro")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM deal_requests WHERE kind=?`, dealExchangeAnswered).Scan(&n))
	return n
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
