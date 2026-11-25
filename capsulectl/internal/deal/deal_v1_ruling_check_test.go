package deal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answerFor has side answer the adjudicator's request, after a tick so its
// book's checkpoint covers its records.
func answerFor(t *testing.T, side v1Side, adj v1Side, request string) string {
	t.Helper()
	side.act(t)
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	file := filepath.Join(t.TempDir(), "answer.json")
	dealRun(t, "respond", "--request", request, "--requester", adj.party.signer, "--output", file)
	return file
}

// The check decides a row only on the disputed side's own latest not_met
// (Ethan, 2026-11-25; Sherlock2, buzz 4ec88700 and 319f98a4); a ruling with
// --decided-by is sealed only on what it decides; and a later statement does
// not change the check of a sealed ruling, which reads only as far as the
// ruling read.
func TestTheRulingCheckDecidesOnTheDisputedSidesOwnWord(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	request := adjudicatorRequest(t, h.dealID)
	check := func(answers []string, extra ...string) map[string]any {
		adj.act(t)
		args := []string{"ruling", "check", "--deal", h.dealID, "--row", "paid_as_agreed", "--dispute", dispute}
		for _, a := range answers {
			args = append(args, "--answer", a)
		}
		return dealRun(t, append(args, extra...)...)
	}

	// The seller, the side the buyer's dispute is against, has said nothing on the row: a person rules.
	silent := []string{answerFor(t, h.buyer, adj, request), answerFor(t, h.seller, adj, request)}
	got := check(silent)
	assert.Equal(t, "seller", got["against"])
	assert.Nil(t, got["decisive"])
	assert.Equal(t, "the seller stated nothing on row paid_as_agreed in a record it signed", got["needs_person"])

	// The seller states not_met itself: decided against it, on that record.
	h.seller.act(t)
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=not_met", "--out", filepath.Join(t.TempDir(), "s.json"))
	admitted := []string{answerFor(t, h.buyer, adj, request), answerFor(t, h.seller, adj, request)}
	got = check(admitted)
	decisive, ok := got["decisive"].(map[string]any)
	require.True(t, ok, "%v", got)
	assert.Equal(t, "seller", decisive["against"])
	assert.Equal(t, xdealv1.ValueNotMet, decisive["value"])
	recordID := decisive["record_id"].(string)
	require.NotEmpty(t, recordID)
	assert.NotEmpty(t, decisive["record"])

	ruling := func(value, against, decidedBy string) []string {
		return []string{"ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", value, "--against", against, "--final",
			"--dispute", dispute, "--answer", admitted[0], "--answer", admitted[1], "--decided-by", decidedBy}
	}
	adj.act(t)
	assert.Contains(t, v1Err(t, ruling("not_met", "seller", "rec-other")...), "the check decides on record "+recordID)
	assert.Contains(t, v1Err(t, ruling("met", "seller", recordID)...), "the check finds not_met, not met")
	assert.Contains(t, v1Err(t, ruling("not_met", "buyer", recordID)...), "--against must be seller")
	assert.Contains(t, v1Err(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller",
		"--dispute", dispute, "--answer", silent[0], "--answer", silent[1], "--decided-by", recordID), "the case needs a person")
	rulingFile := filepath.Join(t.TempDir(), "ruling.json")
	sealed := dealRun(t, append(ruling("not_met", "seller", recordID), "--out", rulingFile)...)
	assert.Equal(t, true, sealed["sealed"], "%v", sealed)

	// The seller says met afterwards: read whole, the case needs a person; checked against the sealed
	// ruling, the answer it was sealed on stands.
	h.seller.act(t)
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s2.json"))
	later := []string{admitted[0], answerFor(t, h.seller, adj, request)}
	got = check(later)
	assert.Nil(t, got["decisive"])
	assert.Equal(t, "the seller's latest statement on row paid_as_agreed, in a record it signed, says met", got["needs_person"])
	got = check(later, "--ruling", rulingFile)
	decisive, ok = got["decisive"].(map[string]any)
	require.True(t, ok, "%v", got)
	assert.Equal(t, recordID, decisive["record_id"])
}

func TestTheRulingCheckRefusals(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	copyFile := adjudicatorCopy(t, h.buyer, h.dealID)
	adj.act(t)
	assert.Contains(t, v1Err(t, "ruling", "check", "--deal", h.dealID, "--row", "paid_as_agreed", "--dispute", dispute, "--copy", copyFile),
		"give one or two --answer files")
	assert.Contains(t, v1Err(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller",
		"--dispute", dispute, "--copy", copyFile, "--decided-by", "rec"), "--decided-by needs the parties' answers")
}
