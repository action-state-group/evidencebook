// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const v1SellerCommit = `{"action":"commit","terms":{"item":"lamp","price_minor":6000,"currency":"USD"}}`

// sellerChecked is a deal both sides agreed to, with the seller's intent and
// its passing commit check, whose digest it returns; the seller is acting.
func sellerChecked(t *testing.T) (*handshakeV1, string) {
	t.Helper()
	h := agreedBoth(t, offerTo)
	h.seller.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, v1SellerCommit))
	require.Equal(t, "pass", out["verdict"], "%v", out)
	var check string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check = checkDigest(t, journal)
	})
	return h, check
}

// v1ActEvents are the deal's local act events.
func v1ActEvents(t *testing.T, dealID string) []sealedEvent {
	t.Helper()
	var acts []sealedEvent
	withSession(t, func(s *dealSession) {
		events, err := s.v1Events(t.Context(), dealID)
		require.NoError(t, err)
		for _, se := range events {
			if se.Event.Kind == "act" {
				acts = append(acts, se)
			}
		}
	})
	return acts
}

// A perform rests on the seller's passing commit check: it is sealed in the
// seller's book with its statements, the local act event names the approval
// that authorized it, and the buyer counts the record.
func TestAPerformIsSealedOnItsCommitCheck(t *testing.T) {
	h, check := sellerChecked(t)
	file := filepath.Join(t.TempDir(), "perform.json")
	out := dealRun(t, "perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "carrier_scan",
		"--statement", "paid_as_agreed=met", "--out", file)
	assert.Equal(t, xdealv1.TypePerform, out["record"])
	// A standing_intent approval rests on the person's first intent, whose
	// signature the act waits on (x-deal-v1 §8); the approval itself needs none.
	signs, _ := out["person_signs"].([]any)
	assert.Len(t, signs, 1, "the first intent's digest: %v", out)
	assert.Equal(t, "missing", out["person_signature"])

	read, err := xdealv1.VerifyBook(readPush(t, file).Record)
	require.NoError(t, err)
	body, ok := read.Record.Body.(*xdealv1.PerformBody)
	require.True(t, ok, "%T", read.Record.Body)
	assert.Equal(t, "deliver", body.Act)
	assert.Equal(t, "carrier_scan", body.Observed)
	assert.Equal(t, []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "met"}}, body.Statements)
	require.NotNil(t, body.Check)
	assert.Equal(t, check, body.Check.Digest)
	assert.Equal(t, out["digest"], recDigest(t, read.Record))

	acts := v1ActEvents(t, h.dealID)
	require.Len(t, acts, 1)
	assert.Equal(t, out["digest"], acts[0].Digest, "the act event names the book record")
	require.NotNil(t, acts[0].Event.Act)
	assert.Equal(t, "commit", acts[0].Event.Act.Action)
	assert.Nil(t, acts[0].Event.Act.AmountMinor)
	assert.NotEmpty(t, acts[0].Event.Act.AuthorizedBy, "the approval it used")
	assert.Equal(t, read.Record.Block.At, acts[0].Event.At, "the act's own time")

	h.buyer.act(t)
	assert.Equal(t, "kept, and counted", dealRun(t, "receive", file)["record"])
}

// Run again with the same flags, a perform rewrites the same file and seals
// nothing; another act on the same check is refused, since its approval
// already authorized one.
func TestAPerformRerunSealsOnceAndTheApprovalIsUsed(t *testing.T) {
	h, check := sellerChecked(t)
	file := filepath.Join(t.TempDir(), "perform.json")
	args := []string{"perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "carrier_scan", "--out", file}
	first := dealRun(t, args...)
	again := dealRun(t, args...)
	assert.Equal(t, first["digest"], again["digest"])
	assert.Equal(t, first["seq"], again["seq"])
	assert.Len(t, v1ActEvents(t, h.dealID), 1)

	other := filepath.Join(t.TempDir(), "other.json")
	msg := v1Err(t, "perform", "--deal", h.dealID, "--check", check, "--act", "install", "--observed", "self", "--out", other)
	assert.Contains(t, msg, "already authorized")
	_, err := os.Lstat(other)
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing written")
}

// An act whose check has no approval, or that names no check of the deal, is
// refused before anything is sealed; so are bad flags.
func TestAPerformIsRefusedWithoutItsAuthorization(t *testing.T) {
	h, check := sellerChecked(t)
	bogus := "cd" + check[2:]
	assert.Contains(t, v1Err(t, "perform", "--deal", h.dealID, "--check", bogus, "--act", "deliver", "--observed", "self"), "not authorized")
	assert.Contains(t, v1Err(t, "perform", "--deal", h.dealID, "--act", "deliver", "--observed", "self"), "--check is required")
	assert.Contains(t, v1Err(t, "perform", "--deal", h.dealID, "--check", check, "--observed", "self"), "--act and --observed are required")
	assert.Contains(t, v1Err(t, "perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "self", "--statement", "no_such_row=met"), "has no row no_such_row")
	assert.Contains(t, v1Err(t, "perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "self", "--statement", "paid_as_agreed=fine"), "met, not_met or cannot_tell")
	assert.Contains(t, v1Err(t, "perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "self", "--mark", "delivered"), "names no record delivered")
	assert.Contains(t, v1Err(t, "perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "self", "--remedy-row", "nope"), "has no row nope")
	assert.Empty(t, v1ActEvents(t, h.dealID))
}

// The first act waits on the other side's stamp proof: the offerer's
// agreement received without it leaves the seller's perform refused, and the
// file that carries it opens it.
func TestAFirstPerformWaitsOnTheOffersStampProof(t *testing.T) {
	h := receivedOffer(t)
	h.sellerAgree = filepath.Join(t.TempDir(), "agreement.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", h.sellerAgree)
	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	h.buyerAgr = filepath.Join(t.TempDir(), "buyer-agreement.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", h.buyerAgr)
	push := readPush(t, h.buyerAgr)
	recordOnly := filepath.Join(t.TempDir(), "record-only.json")
	data, err := json.Marshal(v1Push{Record: push.Record})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(recordOnly, data, 0o600))

	h.seller.act(t)
	dealRun(t, "receive", recordOnly)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	require.Equal(t, "pass", dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, v1SellerCommit))["verdict"])
	var check string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check = checkDigest(t, journal)
	})
	args := []string{"perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "self", "--out", filepath.Join(t.TempDir(), "p.json")}
	assert.Contains(t, v1Err(t, args...), "waits on the other side's stamp proof")
	assert.Empty(t, v1ActEvents(t, h.dealID))

	dealRun(t, "receive", h.buyerAgr)
	out := dealRun(t, args...)
	assert.NotEmpty(t, out["digest"])
	assert.NotNil(t, out["first_act_gate"], "the gate's answer is printed: %v", out)
}

// A run that stopped after the append and before the local act event is
// finished by the next command, once: the act is counted, and the approval it
// used stays used.
func TestAnActWithNoLocalEventIsRecordedOnce(t *testing.T) {
	h, check := sellerChecked(t)
	args := []string{"perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "self", "--out", filepath.Join(t.TempDir(), "p.json")}
	first := dealRun(t, args...)
	withSession(t, func(s *dealSession) {
		_, err := s.db.ExecContext(t.Context(), `DELETE FROM deal_v1_events WHERE deal_id=? AND kind='act'`, h.dealID)
		require.NoError(t, err)
	})
	require.Empty(t, v1ActEvents(t, h.dealID))

	// deal check reconciles first; a second check reads the act.
	dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, v1SellerCommit))
	acts := v1ActEvents(t, h.dealID)
	require.Len(t, acts, 1)
	assert.Equal(t, first["digest"], acts[0].Digest)
	assert.NotEmpty(t, acts[0].Event.Act.AuthorizedBy)

	// The perform run again rewrites its record and records nothing more.
	again := dealRun(t, args...)
	assert.Equal(t, first["digest"], again["digest"])
	assert.Len(t, v1ActEvents(t, h.dealID), 1)
}

// A statement is never gated and rests on no check: it is sealed with its
// rows, a re-run seals nothing, and bad statements are refused.
func TestAStatementIsSealedAndNeverGated(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "b.json"))
	file := filepath.Join(t.TempDir(), "statement.json")
	args := []string{"statement", "--deal", h.dealID, "--statement", "paid_as_agreed=not_met", "--out", file}
	out := dealRun(t, args...)
	read, err := xdealv1.VerifyBook(readPush(t, file).Record)
	require.NoError(t, err)
	body, ok := read.Record.Body.(*xdealv1.StatementBody)
	require.True(t, ok, "%T", read.Record.Body)
	assert.Equal(t, []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "not_met"}}, body.Statements)
	again := dealRun(t, args...)
	assert.Equal(t, out["digest"], again["digest"])
	assert.Empty(t, v1ActEvents(t, h.dealID), "a statement is not an act")

	assert.Contains(t, v1Err(t, "statement", "--deal", h.dealID), "--statement row=value is required")
	assert.Contains(t, v1Err(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--statement", "paid_as_agreed=not_met"), "twice")
	assert.Contains(t, v1Err(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met:refund"), "allows no remedy refund")
	assert.Contains(t, v1Err(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed"), "write row=value")
	newFile := filepath.Join(t.TempDir(), "taken.json")
	require.NoError(t, os.WriteFile(newFile, []byte("x"), 0o600))
	assert.Contains(t, v1Err(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", newFile), "exists")
}
