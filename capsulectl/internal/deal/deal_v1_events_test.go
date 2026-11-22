// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"errors"
	"testing"

	"github.com/action-state-group/evidencebook"
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
		_, held, err = s.journalRecord(t.Context(), events[0].Digest)
		require.NoError(t, err)
		assert.False(t, held, "the opening has no record")
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
			if se.Event.Kind == "open" {
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

// An append that fails while the command runs withdraws the step, so the
// user's retry seals it once.
func TestAV1StepWhoseAppendFailedIsWithdrawn(t *testing.T) {
	h := openedV1(t)
	appendV1JournalHook = func() error { return errors.New("stopped") }
	_, err := invoke(t, "", "--profile", "deal", "deal", "note", "--deal", h.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"It ships Monday."}`))
	appendV1JournalHook = nil
	require.Error(t, err)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"It ships Monday."}`))
	types, _ := journalTypes(t, h.dealID)
	assert.Equal(t, []string{"intent", "message"}, types, "one message, not two")
}

// A process that dies after storing its step and before appending its record
// leaves the payload stored: the next command that loads the deal appends it.
func TestAV1StepADeadRunLeftIsSealedByTheNextCommand(t *testing.T) {
	h := openedV1(t)
	var payload []byte
	var digest string
	withSession(t, func(s *dealSession) {
		events, err := s.v1Events(t.Context(), h.dealID)
		require.NoError(t, err)
		ev := dealEvent{Kind: "message", Message: &dealMessage{From: "counterparty", Channel: "web", Text: "It ships Monday."}}
		p, err := s.prepareV1(h.dealID, events, ev, false)
		require.NoError(t, err)
		payload, digest = p.payload, p.se.Digest
		_, err = s.db.ExecContext(t.Context(), `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest, payload) VALUES (?, ?, ?, ?, ?, ?)`,
			h.dealID, p.se.Event.N, p.se.Event.Kind, string(p.local), digest, payload)
		require.NoError(t, err)
		held, err := s.journalHolds(t.Context(), digest)
		require.NoError(t, err)
		require.False(t, held)
	})
	note(t, h.dealID, "searched", `{"where":"ebay","results":1}`)
	types, _ := journalTypes(t, h.dealID)
	assert.Equal(t, []string{"intent", "message", "agent_move"}, types)
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

// Evidence about no recorded claim refers to the deal's first journal record,
// its intent (the opening has no record); a seller agent's claim carries its
// authority in its own body (x-deal-v1 has no claim_authority record).
func TestAV1NoteSealsEvidenceClaimsAndDisclosures(t *testing.T) {
	h := openedV1(t)
	note(t, h.dealID, "evidence", `{"about":"the delivery","source":"agent"}`)
	note(t, h.dealID, "claim", `{"text":"the kettle is new","source_kind":"agent","class":"condition"}`)
	types, withDeal := journalTypes(t, h.dealID)
	assert.Equal(t, []string{"intent", "evidence", "claim"}, types)
	assert.Equal(t, []bool{false, true, true}, withDeal)
	withSession(t, func(s *dealSession) {
		events, err := s.v1Events(t.Context(), h.dealID)
		require.NoError(t, err)
		rec, _, err := s.journalRecord(t.Context(), events[2].Digest)
		require.NoError(t, err)
		require.Len(t, rec.Block.Refs, 1)
		assert.Equal(t, events[1].Digest, rec.Block.Refs[0].Digest, "about: the intent, not the opening")
	})
}

// A v1 record is checked as a v0 step is before anything is stored: the
// opening's own contact never goes into a journal record.
func TestAV1NoteRefusesALocalIdentifier(t *testing.T) {
	h := receivedOffer(t)
	opening := `{"type": "purchase", "who": {"name": "Kettle Shop", "email": "shop@kettle.example"}, "recourse": {"rail": "card", "refundable": true}}`
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, opening))
	_, err := invoke(t, "", "--profile", "deal", "deal", "note", "--deal", h.dealID, "--kind", "change", "--input", writeJSON(t, `{"source":"counterparty","terms":{"place":"write to shop@kettle.example"}}`))
	require.Error(t, err)
	types, _ := journalTypes(t, h.dealID)
	assert.Equal(t, []string{"intent"}, types, "nothing was stored")
}

// An opening whose intent cannot be sealed stores nothing: the opening and its
// intent are stored together or not at all.
func TestAV1OpeningIsStoredWithItsIntentOrNotAtAll(t *testing.T) {
	h := receivedOffer(t)
	appendV1JournalHook = func() error { return errors.New("stopped") }
	_, err := invoke(t, "", "--profile", "deal", "deal", "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	appendV1JournalHook = nil
	require.Error(t, err)
	withSession(t, func(s *dealSession) {
		var n int
		require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deal_v1_events WHERE deal_id=? AND kind='intent'`, h.dealID).Scan(&n))
		assert.Equal(t, 0, n, "the failed intent was withdrawn")
	})
}

func TestOpeningGoesOnlyOnAV1DealsFirstIntent(t *testing.T) {
	h := openedV1(t)
	assert.Contains(t, v1Err(t, "note", "--deal", h.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"user","text":"x"}`), "--opening", writeJSON(t, v1Opening)), "--opening goes only on")
	assert.Contains(t, v1Err(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, `{"verbatim":"at least 42 now","min_total_minor":4200}`), "--opening", writeJSON(t, v1Opening)), "already open")
}

// The local list never leaves the store (§12): the person's verbatim words
// are in no journal record, and a v1 deal has no export or report route that
// could carry them yet.
func TestTheV1LocalListStaysInTheStore(t *testing.T) {
	h := receivedOffer(t)
	const marker = "MARKER-ab12cd34-verbatim"
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, `{"verbatim":"`+marker+` sell the kettle"}`), "--opening", writeJSON(t, v1Opening))
	withSession(t, func(s *dealSession) {
		j, err := s.shelf.Journal(t.Context())
		require.NoError(t, err)
		read, err := s.shelf.Records(t.Context(), evidencebook.JournalLogID)
		require.NoError(t, err)
		for _, r := range read.Records {
			for _, c := range r.Header.PayloadCommitments {
				payload, err := j.ResolvePayload(t.Context(), r.RecordID, c)
				require.NoError(t, err)
				assert.NotContains(t, string(payload), marker, "a journal record carries the verbatim words")
			}
		}
	})
	for _, verb := range [][]string{{"export", "--deal", h.dealID}, {"report", "--deal", h.dealID}} {
		out, err := invoke(t, "", append([]string{"--profile", "deal", "deal"}, verb...)...)
		require.Error(t, err, verb[0])
		assert.NotContains(t, out, marker, verb[0])
	}
}
