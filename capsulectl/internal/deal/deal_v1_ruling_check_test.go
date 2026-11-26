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

	// #952 review: a recheck from other sources than the ruling read (the buyer's answer, and with it the copy of the
	// seller's book it holds, left out) is refused, as is a file that is not a ruling of this case.
	adj.act(t)
	args := []string{"ruling", "check", "--deal", h.dealID, "--row", "paid_as_agreed", "--dispute", dispute}
	assert.Contains(t, v1Err(t, append(args, "--answer", later[1], "--ruling", rulingFile)...), "from other sources than these answers give")
	assert.Contains(t, v1Err(t, append(args, "--answer", later[0], "--answer", later[1], "--ruling", dispute)...), "not a ruling")
	assert.Contains(t, v1Err(t, append(args, "--answer", later[0], "--answer", later[1], "--ruling", "")...), "--ruling is empty")
	assert.Contains(t, v1Err(t, "ruling", "check", "--deal", h.dealID, "--row", "delivered", "--dispute", dispute,
		"--answer", later[0], "--answer", later[1]), "not delivered")
	assert.Contains(t, v1Err(t, ruling("not_met", "none", recordID)...), "--against must be seller")

	adj.act(t)
	// #952 review: the sealed ruling read against a case on another row, deal or adjudicator is refused, past its type check
	withSession(t, func(s *dealSession) {
		me := adj.party.signer
		rec, _, err := readCaseInputs(dispute, nil)
		require.NoError(t, err)
		g, err := s.gatherAnswered(rec, nil, later, h.dealID, me)
		require.NoError(t, err)
		_, err = sealedRulingOf(rulingFile, g, me)
		require.NoError(t, err, "the ruling of this case is accepted")
		for name, change := range map[string]func(*xdealv1.GatheredCase){
			"row":  func(g *xdealv1.GatheredCase) { g.Row = "delivered" },
			"deal": func(g *xdealv1.GatheredCase) { g.DealID = "deal-00000000000000ff" },
			"book": func(g *xdealv1.GatheredCase) { g.LogID = "case/other" },
		} {
			other := g
			change(&other)
			_, err := sealedRulingOf(rulingFile, other, me)
			assert.Error(t, err, name)
		}
		_, err = sealedRulingOf(rulingFile, g, "another-adjudicator")
		assert.ErrorContains(t, err, "not signed by your key")
	})
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
	// an empty --decided-by (an agent's unset variable) never turns the check off
	for _, empty := range []string{"", "  "} {
		assert.Contains(t, v1Err(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller",
			"--dispute", dispute, "--copy", copyFile, "--decided-by", empty), "--decided-by is empty")
	}
}
