// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"errors"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1Opening is the first intent note's input on a v1 deal: v0's opening
// without terms, which come from the offer.
const (
	v1Opening = `{"type": "purchase", "who": {"name": "Kettle Shop"}, "recourse": {"rail": "card", "refundable": true}}`
	v1Intent  = `{"verbatim": "sell the kettle, at least 40 dollars"}`
)

// The first intent note on a v1 deal opens it: the opening stays local, and
// the intent goes to the journal as an x-deal-v1 intent record, with no deal
// id (PROFILE §3).
func TestTheFirstIntentNoteOpensAV1Deal(t *testing.T) {
	h := receivedOffer(t)
	assert.Contains(t, v1Err(t, "note", "--deal", h.dealID, "--kind", "message", "--input", writeJSON(t, `{"from": "agent", "text": "hello"}`)), "has no intent yet")

	res := dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	assert.Equal(t, true, res["opened"])
	intent := res["intent"].(string)
	withSession(t, func(s *dealSession) {
		rec, held, err := s.journalRecord(t.Context(), intent)
		require.NoError(t, err)
		require.True(t, held)
		require.NotNil(t, rec)
		assert.Equal(t, xdealv1.TypeIntent, rec.Block.RecordType)
		assert.Empty(t, rec.Block.DealID, "an intent spans deals")
		events, err := s.v1Events(t.Context(), h.dealID)
		require.NoError(t, err)
		require.Len(t, events, 2)
		assert.Equal(t, "open", events[0].Event.Kind)
		assert.Equal(t, "seller", events[0].Event.Open.Intent.PartyRole, "the deal's own side")
		assert.Equal(t, "", events[0].Digest, "the opening has no record")
	})
}

func TestAV1OpeningRefusesAnotherPartyRole(t *testing.T) {
	h := receivedOffer(t)
	in := `{"verbatim": "sell it", "party_role": "buyer"}`
	assert.Contains(t, v1Err(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, in), "--opening", writeJSON(t, v1Opening)), "this profile is the seller")
	assert.Contains(t, v1Err(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent)), "--opening FILE")
}

func TestAV1NoteKindWithNoFormNamesItsVerb(t *testing.T) {
	h := receivedOffer(t)
	assert.Contains(t, v1Err(t, "note", "--deal", h.dealID, "--kind", "act", "--input", writeJSON(t, `{}`)), "`deal perform` or `deal settle`")
}

// openedV1 is a received offer the seller has opened with its intent.
func openedV1(t *testing.T) *handshakeV1 {
	t.Helper()
	h := receivedOffer(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	return h
}

// journalTypes is each x-deal-v1 journal record's type and whether it carries
// the deal id, for the deal's local events with a record.
func journalTypes(t *testing.T, dealID string) (types []string, withDeal []bool) {
	t.Helper()
	withSession(t, func(s *dealSession) {
		events, err := s.v1Events(t.Context(), dealID)
		require.NoError(t, err)
		for _, se := range events {
			if se.Digest == "" {
				continue
			}
			rec, held, err := s.journalRecord(t.Context(), se.Digest)
			require.NoError(t, err)
			require.True(t, held, se.Event.Kind)
			require.NotNil(t, rec, se.Event.Kind)
			types = append(types, rec.Block.RecordType)
			withDeal = append(withDeal, rec.Block.DealID == dealID)
		}
	})
	return types, withDeal
}

// Later notes run the v0 note code over the local list and seal the v1
// journal record of the same body: a message and an evidence record carry the
// deal id (their offer exists).
func TestAV1NoteSealsItsJournalRecord(t *testing.T) {
	h := openedV1(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"It ships Monday."}`))
	types, withDeal := journalTypes(t, h.dealID)
	assert.Equal(t, []string{"intent", "message"}, types)
	assert.Equal(t, []bool{false, true}, withDeal)
}

// A run that stops after its local event is stored but before its record is
// appended is finished by the next read, with the same record.
func TestAV1StepAStoppedRunLeftIsSealedOnTheNextRead(t *testing.T) {
	h := openedV1(t)
	appendV1JournalHook = func() error { return errors.New("stopped") }
	_, err := invoke(t, "", "--profile", "deal", "deal", "note", "--deal", h.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"It ships Monday."}`))
	appendV1JournalHook = nil
	require.Error(t, err)
	var digest string
	withSession(t, func(s *dealSession) {
		require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT record_digest FROM deal_v1_events WHERE deal_id=? AND kind='message'`, h.dealID).Scan(&digest))
		held, err := s.journalHolds(t.Context(), digest)
		require.NoError(t, err)
		require.False(t, held, "the run stopped before the append")
		_, err = s.v1Events(t.Context(), h.dealID)
		require.NoError(t, err)
		held, err = s.journalHolds(t.Context(), digest)
		require.NoError(t, err)
		assert.True(t, held, "the next read appended the stored record")
	})
}

// The agent moves and a detail change seal as agent_move and detail_change
// with the deal id, and a later intent that narrows the bounds seals as an
// intent with none (PROFILE §3).
func TestAV1NoteSealsAgentMovesChangesAndLaterIntents(t *testing.T) {
	h := openedV1(t)
	note(t, h.dealID, "searched", `{"where":"ebay, etsy","results":4}`)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "change", "--input", writeJSON(t, `{"source":"counterparty","terms":{"when":"2026-12-01"}}`))
	note(t, h.dealID, "intent", `{"verbatim":"at least 45 dollars now","min_total_minor":4500}`)
	types, withDeal := journalTypes(t, h.dealID)
	assert.Equal(t, []string{"intent", "agent_move", "detail_change", "intent"}, types)
	assert.Equal(t, []bool{false, true, true, false}, withDeal)
	withSession(t, func(s *dealSession) {
		events, err := s.v1Events(t.Context(), h.dealID)
		require.NoError(t, err)
		state, err := foldDeal(events)
		require.NoError(t, err)
		require.NotNil(t, state.intent.MinTotalMinor, "the later intent's floor is folded")
		assert.Equal(t, int64(4500), *state.intent.MinTotalMinor)
		assert.Equal(t, "2026-12-01", state.terms.When, "the change overlays the offer's terms")
	})
}
