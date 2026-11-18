// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRecord is the test profile's record format: JSON whose sig member
// must be "ok", carrying what the store needs.
type testRecord struct {
	Sig    string `json:"sig"`
	Digest string `json:"digest"`
	Sender string `json:"sender"`
	Log    string `json:"log"`
	Pos    uint64 `json:"pos"`
	Prev   string `json:"prev"`
	Deal   string `json:"deal"`
	Offer  bool   `json:"offer"`
	Pad    string `json:"pad,omitempty"`
}

// testReceiveProfile admits offers from anyone and other records from the
// senders it lists, and maps checkpoint keys to senders.
type testReceiveProfile struct {
	mu        sync.Mutex
	admitted  map[string]bool
	answered  map[string]bool
	proofKeys map[string]string
	reads     int
	// Hooks run outside the profile's mutex, as a profile reading the
	// shelf would.
	onAdmit    func(ReceivedRecord)
	onAnswered func(ReceivedRecord)
	// afterAnswered runs once Answered has decided, before it returns.
	afterAnswered func(ReceivedRecord)
	proofErr      error
}

func newTestReceiveProfile(senders ...string) *testReceiveProfile {
	p := &testReceiveProfile{admitted: map[string]bool{}, answered: map[string]bool{}, proofKeys: map[string]string{}}
	for _, s := range senders {
		p.admitted[s] = true
	}
	return p
}

func (p *testReceiveProfile) Read(_ context.Context, record []byte) (ReceivedRecord, error) {
	p.mu.Lock()
	p.reads++
	p.mu.Unlock()
	var r testRecord
	if err := json.Unmarshal(record, &r); err != nil {
		return ReceivedRecord{}, err
	}
	if r.Sig != "ok" {
		return ReceivedRecord{}, errors.New("bad signature")
	}
	return ReceivedRecord{Digest: r.Digest, SenderKey: r.Sender, LogID: r.Log, Position: r.Pos, Prev: r.Prev, DealID: r.Deal, Offer: r.Offer}, nil
}

func (p *testReceiveProfile) Admit(_ context.Context, r ReceivedRecord) (bool, error) {
	if p.onAdmit != nil {
		p.onAdmit(r)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return r.Offer || p.admitted[r.SenderKey], nil
}

func (p *testReceiveProfile) Answered(_ context.Context, r ReceivedRecord) (bool, error) {
	if p.onAnswered != nil {
		p.onAnswered(r)
	}
	p.mu.Lock()
	answered := p.answered[r.Digest]
	p.mu.Unlock()
	if p.afterAnswered != nil {
		p.afterAnswered(r)
	}
	return answered, nil
}

func (p *testReceiveProfile) ProofSender(_ context.Context, keyID, _ string) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.proofErr != nil {
		return "", false, p.proofErr
	}
	s, ok := p.proofKeys[keyID]
	return s, ok, nil
}

func dg(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func rec(t *testing.T, r testRecord) []byte {
	t.Helper()
	r.Sig = "ok"
	b, err := json.Marshal(r)
	require.NoError(t, err)
	return b
}

// chain is n records of one sender's log, digests named by name.
func chainRecord(t *testing.T, sender, log, deal string, pos uint64, name, prevName string) []byte {
	prev := ""
	if prevName != "" {
		prev = dg(prevName)
	}
	return rec(t, testRecord{Digest: dg(name), Sender: sender, Log: log, Pos: pos, Prev: prev, Deal: deal})
}

type receiveFixture struct {
	*shelfFixture
	profile *testReceiveProfile
}

func newReceiveFixture(t *testing.T, cfg ReceiveConfig) (*receiveFixture, *Shelf) {
	t.Helper()
	f := &receiveFixture{shelfFixture: newShelfFixture(t), profile: newTestReceiveProfile("alice")}
	cfg.Profile = f.profile
	f.cfg.Receive = cfg
	return f, f.open(t)
}

func mustReceive(t *testing.T, s *Shelf, record []byte, proof *StampProof) ReceiveResult {
	t.Helper()
	got, err := s.Receive(context.Background(), record, proof)
	require.NoError(t, err)
	return got
}

func receivedCount(t *testing.T, s *Shelf) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM shelf_received`).Scan(&n))
	return n
}

func prefixOf(t *testing.T, s *Shelf, sender, log string) CountedPrefix {
	t.Helper()
	p, err := s.CountedPrefix(context.Background(), sender, log)
	require.NoError(t, err)
	return p
}

func TestReceiveADuplicateIsAcknowledgedAndNotStoredAgain(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{})
	r1 := chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", "")
	got := mustReceive(t, s, r1, nil)
	assert.True(t, got.Record.Stored)
	assert.True(t, got.Record.Counted)
	assert.Equal(t, []string{"deal-1"}, got.Changes.Grew)

	again := mustReceive(t, s, r1, nil)
	assert.False(t, again.Record.Stored)
	assert.True(t, again.Record.Duplicate)
	assert.True(t, again.Record.Counted)
	assert.Empty(t, again.Changes.Grew)
	assert.Equal(t, 1, receivedCount(t, s))

	// Another record under a held digest is refused.
	other := rec(t, testRecord{Digest: dg("a1"), Sender: "alice", Log: "deal/a", Pos: 1, Deal: "deal-1", Pad: "x"})
	_, err := s.Receive(context.Background(), other, nil)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestReceiveAGapIsStoredNotCountedAndItsFillJoinsThePrefix(t *testing.T) {
	ctx := context.Background()
	f, s := newReceiveFixture(t, ReceiveConfig{})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	got := mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 3, "a3", "a2"), nil)
	assert.True(t, got.Record.Stored)
	assert.False(t, got.Record.Counted)
	assert.True(t, got.Record.AskForBook, "a record past the counted prefix asks for the book")
	assert.Empty(t, got.Changes.Grew)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 5, "a5", "a4"), nil)
	assert.Equal(t, CountedPrefix{Size: 1, Last: dg("a1")}, prefixOf(t, s, "alice", "deal/a"))

	fill := mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
	assert.True(t, fill.Record.Counted)
	assert.Equal(t, []string{"deal-1"}, fill.Changes.Grew)
	assert.Equal(t, CountedPrefix{Size: 3, Last: dg("a3")}, prefixOf(t, s, "alice", "deal/a"), "the fill joins the prefix up to the next gap")

	// A restart recomputes the same prefix from the store.
	require.NoError(t, s.Release())
	reopened := f.open(t)
	assert.Equal(t, CountedPrefix{Size: 3, Last: dg("a3")}, prefixOf(t, reopened, "alice", "deal/a"))
	again, err := reopened.Receive(ctx, chainRecord(t, "alice", "deal/a", "deal-1", 4, "a4", "a3"), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"deal-1"}, again.Changes.Grew)
	assert.Equal(t, CountedPrefix{Size: 5, Last: dg("a5")}, prefixOf(t, reopened, "alice", "deal/a"))
}

func TestReceiveAForkKeepsBothAndEndsThePrefixForGood(t *testing.T) {
	ctx := context.Background()
	_, s := newReceiveFixture(t, ReceiveConfig{})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
	fork := mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2-other", "a1"), nil)
	assert.True(t, fork.Record.Stored, "both records of a fork are kept")
	assert.False(t, fork.Record.Counted)
	assert.Equal(t, []string{"deal-1"}, fork.Changes.Forked)
	assert.Equal(t, []string{"deal-1"}, fork.Changes.Shrank)
	assert.Equal(t, 3, receivedCount(t, s))
	assert.Equal(t, CountedPrefix{Size: 1, Last: dg("a1"), ForkAt: 2}, prefixOf(t, s, "alice", "deal/a"))

	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, EvidenceFork, evidence[0].Kind)
	assert.Contains(t, string(evidence[0].A), dg("a2"))
	assert.Contains(t, string(evidence[0].B), dg("a2-other"))

	// For good: records after the fork never join the prefix.
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 3, "a3", "a2"), nil)
	assert.Equal(t, uint64(1), prefixOf(t, s, "alice", "deal/a").Size)
}

func TestReceiveALateForkShrinksThePrefix(t *testing.T) {
	ctx := context.Background()
	_, s := newReceiveFixture(t, ReceiveConfig{})
	for i, name := range []string{"a1", "a2", "a3"} {
		prev := ""
		if i > 0 {
			prev = []string{"a1", "a2"}[i-1]
		}
		mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", uint64(i+1), name, prev), nil)
	}
	assert.Equal(t, uint64(3), prefixOf(t, s, "alice", "deal/a").Size)
	// A record at 4 naming another predecessor than the one held at 3 is a
	// fork at 3.
	late := mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 4, "a4", "a3-other"), nil)
	assert.Equal(t, []string{"deal-1"}, late.Changes.Forked)
	assert.Equal(t, []string{"deal-1"}, late.Changes.Shrank)
	assert.Equal(t, CountedPrefix{Size: 2, Last: dg("a2"), ForkAt: 3}, prefixOf(t, s, "alice", "deal/a"))
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, EvidenceFork, evidence[0].Kind)
}

func TestReceiveRefusesASenderAndStoresNothing(t *testing.T) {
	ctx := context.Background()
	_, s := newReceiveFixture(t, ReceiveConfig{})
	got, err := s.Receive(ctx, chainRecord(t, "mallory", "deal/m", "deal-m", 1, "m1", ""), nil)
	assert.ErrorIs(t, err, ErrRefused)
	require.NotNil(t, got.Record)
	assert.ErrorIs(t, got.Record.Refused, ErrRefused)
	assert.False(t, got.Record.Stored)
	assert.Zero(t, receivedCount(t, s))
	_, err = s.ReceivedRecordOf(ctx, dg("m1"))
	assert.ErrorIs(t, err, ErrNotFound)

	// A record the profile cannot read (a bad signature) is invalid.
	bad, _ := json.Marshal(testRecord{Sig: "forged", Digest: dg("x"), Sender: "alice", Log: "deal/a", Pos: 1, Deal: "d"})
	_, err = s.Receive(ctx, bad, nil)
	assert.ErrorIs(t, err, ErrInvalid)
	// A record that names no predecessor past position 1 is invalid.
	_, err = s.Receive(ctx, rec(t, testRecord{Digest: dg("y"), Sender: "alice", Log: "deal/a", Pos: 2, Deal: "d"}), nil)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = s.Receive(ctx, nil, nil)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Zero(t, receivedCount(t, s))
}

func TestReceiveWithoutAProfileIsRefused(t *testing.T) {
	s := newShelfFixture(t).open(t)
	_, err := s.Receive(context.Background(), []byte(`{}`), nil)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestReceiveRefusesAnOversizeRecordBeforeReadingIt(t *testing.T) {
	f, s := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxRecordBytes: 64}})
	got, err := s.Receive(context.Background(), rec(t, testRecord{Digest: dg("a1"), Sender: "alice", Log: "deal/a", Pos: 1, Deal: "deal-1", Pad: strings.Repeat("x", 64)}), nil)
	assert.ErrorIs(t, err, ErrOverLimit)
	assert.False(t, got.Record.Stored)
	assert.Zero(t, f.profile.reads)
	assert.Zero(t, receivedCount(t, s))
}

func TestReceiveRefusesARecordPastThePerBookBound(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxRecordsPerLog: 2}})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
	_, err := s.Receive(context.Background(), chainRecord(t, "alice", "deal/a", "deal-1", 3, "a3", "a2"), nil)
	assert.ErrorIs(t, err, ErrOverLimit)
	assert.Equal(t, 2, receivedCount(t, s))
	// Another log of the same sender has its own bound.
	mustReceive(t, s, chainRecord(t, "alice", "deal/b", "deal-2", 1, "b1", ""), nil)
}

func offer(t *testing.T, sender, name string, pad int) []byte {
	return rec(t, testRecord{Digest: dg(name), Sender: sender, Log: "offers/" + name, Pos: 1, Deal: "offer-" + name, Offer: true, Pad: strings.Repeat("x", pad)})
}

func TestReceiveOfferBoundsDropTheOldestUnansweredOffer(t *testing.T) {
	ctx := context.Background()
	f, s := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxOffersPerSender: 2, MaxOffers: 3, MaxOfferBytesPerSender: 1000}})
	held := func(name string) bool {
		_, err := s.ReceivedRecordOf(ctx, dg(name))
		if errors.Is(err, ErrNotFound) {
			return false
		}
		require.NoError(t, err)
		return true
	}
	// Offers come from any key, Admit says.
	for _, name := range []string{"o1", "o2", "o3"} {
		got := mustReceive(t, s, offer(t, "bob", name, 0), nil)
		assert.True(t, got.Record.Stored)
	}
	assert.False(t, held("o1"), "the oldest unanswered offer is dropped at the per-sender bound")
	_, err := s.ReceivedRecordOf(ctx, dg("o1"))
	assert.ErrorIs(t, err, ErrNotFound, "a dropped offer leaves no tombstone")
	assert.True(t, held("o2") && held("o3"))

	// An answered offer leaves the bounds and is never dropped.
	f.profile.answered[dg("o2")] = true
	mustReceive(t, s, offer(t, "bob", "o4", 0), nil)
	assert.True(t, held("o2") && held("o3") && held("o4"))

	// The total bound drops the oldest unanswered offer of any sender.
	mustReceive(t, s, offer(t, "carol", "c1", 0), nil)
	assert.True(t, held("o3"))
	mustReceive(t, s, offer(t, "carol", "c2", 0), nil)
	assert.False(t, held("o3"))
	assert.True(t, held("o2") && held("o4") && held("c1") && held("c2"))

	// An offer over a byte bound alone is refused.
	_, err = s.Receive(ctx, offer(t, "dave", "d1", 1000), nil)
	assert.ErrorIs(t, err, ErrOverLimit)
	assert.False(t, held("d1"))
}

func TestReceiveOfferBytesBoundDropsTheOldest(t *testing.T) {
	ctx := context.Background()
	one := int64(len(offer(t, "bob", "o1", 150)))
	_, s := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxOfferBytesPerSender: 2*one + 10}})
	mustReceive(t, s, offer(t, "bob", "o1", 150), nil)
	mustReceive(t, s, offer(t, "bob", "o2", 150), nil)
	_, err := s.ReceivedRecordOf(ctx, dg("o1"))
	require.NoError(t, err)
	mustReceive(t, s, offer(t, "bob", "o3", 150), nil)
	_, err = s.ReceivedRecordOf(ctx, dg("o1"))
	assert.ErrorIs(t, err, ErrNotFound)
}

// proofHolder is a holder's shelf with one deal book, migrated and stamped:
// the side that pushes records and stamp proofs.
type proofHolder struct {
	shelf   *Shelf
	cadence *Cadence
	values  *statusValues
	book    *Book
	bookID  string
}

func newProofHolder(t *testing.T, payloads ...string) *proofHolder {
	t.Helper()
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, values := newStampCadence(t, shelf)
	values.set("deal/a", `{"row":"open"}`)
	book := mustOpenBook(t, shelf, "deal/a")
	for _, p := range payloads {
		mustAppend(t, book, observation("a", p))
	}
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	h := &proofHolder{shelf: shelf, cadence: c, values: values, book: book}
	p := h.proof(t, AudienceCounterparty)
	var f BookFact
	require.NoError(t, json.Unmarshal(p.Fact, &f))
	h.bookID = f.BookID
	return h
}

func (h *proofHolder) proof(t *testing.T, audience Audience) StampProof {
	t.Helper()
	ctx := context.Background()
	var bookID string
	require.NoError(t, h.shelf.db.QueryRow(`SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' AND kind = ? LIMIT 1`, FactKindBook).Scan(&bookID))
	p, err := h.cadence.StampProof(ctx, bookID, 0, audience)
	require.NoError(t, err)
	return p
}

// records are the holder's book log entries as the test profile's records.
func (h *proofHolder) records(t *testing.T, sender, deal string) [][]byte {
	t.Helper()
	rows, err := h.shelf.db.Query(`SELECT seq, value FROM cll_entries WHERE log_id = 'deal/a' ORDER BY seq`)
	require.NoError(t, err)
	defer rows.Close()
	var out [][]byte
	prev := ""
	for rows.Next() {
		var seq uint64
		var value []byte
		require.NoError(t, rows.Scan(&seq, &value))
		digest := hex.EncodeToString(value)
		out = append(out, rec(t, testRecord{Digest: digest, Sender: sender, Log: "deal/a", Pos: seq, Prev: prev, Deal: deal}))
		prev = digest
	}
	require.NoError(t, rows.Err())
	return out
}

func keyOf(t *testing.T, p StampProof) string {
	t.Helper()
	cp, err := checkpointFromStatement(p.BookCheckpoint)
	require.NoError(t, err)
	return cp.KeyID
}

func proofRows(t *testing.T, s *Shelf) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM shelf_received_proofs`).Scan(&n))
	return n
}

func TestStampProofVerifiesAndHandsOpeningsByAudience(t *testing.T) {
	h := newProofHolder(t, "1", "2")
	p := h.proof(t, AudienceCounterparty)
	v, err := VerifyStampProof(p)
	require.NoError(t, err)
	assert.Equal(t, "deal/a", v.Fact.LogID)
	assert.Equal(t, v.BookCheckpoint.KeyID, v.ShelfCheckpoint.KeyID)
	require.NotNil(t, p.StatusOpening)
	assert.JSONEq(t, `{"row":"open"}`, string(p.StatusOpening.Value))

	assert.NotNil(t, h.proof(t, AudienceChosenReader).StatusOpening)
	assert.Nil(t, h.proof(t, AudienceAdjudicator).StatusOpening)
	_, err = h.cadence.StampProof(context.Background(), h.bookID, 0, Audience("stranger"))
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = h.cadence.StampProof(context.Background(), dg("no such book"), 0, AudienceCounterparty)
	assert.ErrorIs(t, err, ErrNotFound)

	// Each part of a proof is checked.
	for name, tamper := range map[string]func(*StampProof){
		"fact": func(p *StampProof) {
			p.Fact = append([]byte(nil), p.Fact[:len(p.Fact)-1]...)
			p.Fact = append(p.Fact, ' ', '}')
		},
		"opening": func(p *StampProof) {
			p.StatusOpening = &FactOpening{Salt: p.StatusOpening.Salt, Value: json.RawMessage(`{"row":"closed"}`)}
		},
		"inclusion":  func(p *StampProof) { p.Inclusion.Seq++ },
		"book":       func(p *StampProof) { p.BookCheckpoint = p.ShelfCheckpoint },
		"shelf":      func(p *StampProof) { p.ShelfCheckpoint = p.BookCheckpoint },
		"no opening": nil,
	} {
		t.Run(name, func(t *testing.T) {
			q := h.proof(t, AudienceCounterparty)
			if tamper == nil {
				q.StatusOpening = nil
				_, err := VerifyStampProof(q)
				assert.NoError(t, err)
				return
			}
			tamper(&q)
			_, err := VerifyStampProof(q)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestReceiveAValidProofCoversTheRecords(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1", "2", "3")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	for _, r := range h.records(t, "alice", "deal-1") {
		mustReceive(t, s, r, nil)
	}
	got := mustReceive(t, s, nil, &p)
	require.NotNil(t, got.Proof)
	assert.True(t, got.Proof.Accepted)
	assert.Equal(t, []string{"deal-1"}, got.Changes.Covered)
	assert.Equal(t, 1, proofRows(t, s))

	// The same proof again is no contradiction, and is not kept twice.
	again := mustReceive(t, s, nil, &p)
	assert.True(t, again.Proof.Duplicate)
	assert.False(t, again.Proof.Accepted)
	assert.Empty(t, again.Changes.Covered)
	assert.Equal(t, 1, proofRows(t, s))
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	assert.Empty(t, evidence)

	// A proof from a key the profile does not relate to a sender is refused.
	delete(f.profile.proofKeys, keyOf(t, p))
	_, err = s.Receive(ctx, nil, &p)
	assert.ErrorIs(t, err, ErrRefused)
}

func TestReceiveADuplicateRecordStillDeliversTheProofItLacked(t *testing.T) {
	h := newProofHolder(t, "1")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	records := h.records(t, "alice", "deal-1")
	for _, r := range records {
		mustReceive(t, s, r, nil)
	}
	last := records[len(records)-1]
	got := mustReceive(t, s, last, &p)
	assert.True(t, got.Record.Duplicate)
	assert.True(t, got.Proof.Accepted)
	assert.Equal(t, 1, proofRows(t, s))

	// A refused record does not stop its proof either.
	f.profile.admitted["alice"] = false
	q := h.proof(t, AudienceChosenReader)
	got, err := s.Receive(context.Background(), chainRecord(t, "alice", "deal/z", "deal-z", 1, "z1", ""), &q)
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorIs(t, got.Record.Refused, ErrRefused)
	assert.True(t, got.Proof.Duplicate)
}

func TestReceiveAContradictingProofIsRefusedAndKeptAsEvidence(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1", "2")
	other := newProofHolder(t, "x", "y") // the same key, another book at the same size
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p, q := h.proof(t, AudienceCounterparty), other.proof(t, AudienceCounterparty)
	require.Equal(t, keyOf(t, p), keyOf(t, q))
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	mustReceive(t, s, nil, &p)

	got, err := s.Receive(ctx, nil, &q)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.ErrorIs(t, got.Proof.Refused, ErrInvalid)
	assert.False(t, got.Proof.Accepted)
	assert.Equal(t, 1, proofRows(t, s))
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, EvidenceCheckpoint, evidence[0].Kind)
	assert.Equal(t, p.BookCheckpoint, evidence[0].A)
	assert.Equal(t, q.BookCheckpoint, evidence[0].B)

	// The held proof survives a restart, and the contradiction is found
	// against it again, its evidence kept once.
	require.NoError(t, s.Release())
	reopened := f.open(t)
	_, err = reopened.Receive(ctx, nil, &q)
	assert.ErrorIs(t, err, ErrInvalid)
	evidence, err = reopened.Evidence(ctx)
	require.NoError(t, err)
	assert.Len(t, evidence, 1)
}

func TestReceiveAProofTheRecordsHeldDoNotRecomputeIsRefused(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1", "2")
	other := newProofHolder(t, "x", "y")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	for _, r := range other.records(t, "alice", "deal-1") {
		mustReceive(t, s, r, nil)
	}
	got, err := s.Receive(ctx, nil, &p)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, []string{"deal-1"}, got.Changes.Forked)
	assert.Zero(t, proofRows(t, s))
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, EvidenceCheckpointRecords, evidence[0].Kind)
}

func TestReceiveRecordsThatContradictAHeldProofAreEvidence(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1", "2")
	other := newProofHolder(t, "x", "y")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	mustReceive(t, s, nil, &p) // before any record: nothing to check it against
	var last ReceiveResult
	for _, r := range other.records(t, "alice", "deal-1") {
		last = mustReceive(t, s, r, nil)
	}
	assert.Equal(t, []string{"deal-1"}, last.Changes.Forked)
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, EvidenceCheckpointRecords, evidence[0].Kind)
}

func TestReceiveOneBookCheckpointUnderTwoShelfCheckpointsKeepsBoth(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	mustReceive(t, s, nil, &p)

	// A received-only change: a new fact at the same size and checkpoint,
	// under a new shelf checkpoint.
	h.values.set("deal/a", `{"row":"agreed"}`)
	_, err := h.cadence.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	q := h.proof(t, AudienceCounterparty)
	require.Equal(t, p.BookCheckpoint, q.BookCheckpoint)
	require.NotEqual(t, p.ShelfCheckpoint, q.ShelfCheckpoint)
	got := mustReceive(t, s, nil, &q)
	assert.True(t, got.Proof.Accepted)
	assert.Empty(t, got.Changes.Covered, "the same size covers no new record")
	assert.Equal(t, 2, proofRows(t, s))
}

func TestForgetWaitsForTheDealToSettleAndLeavesTombstones(t *testing.T) {
	ctx := context.Background()
	f, s := newReceiveFixture(t, ReceiveConfig{Retention: time.Hour})
	r1 := chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", "")
	mustReceive(t, s, r1, nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/b", "deal-2", 1, "b1", ""), nil)
	later := f.clock.t.Add(2 * time.Hour)
	settled := false
	isSettled := func(_ context.Context, deal string) (bool, error) { return deal == "deal-1" && settled, nil }

	_, err := s.Forget(ctx, "deal-1", later, isSettled)
	assert.ErrorIs(t, err, ErrInvalid, "nothing of a deal is deleted before it is settled")
	assert.Equal(t, 2, receivedCount(t, s))

	settled = true
	// Records younger than the retention stay.
	gone, err := s.Forget(ctx, "deal-1", f.clock.t, isSettled)
	require.NoError(t, err)
	assert.Empty(t, gone)
	gone, err = s.Forget(ctx, "deal-1", later, isSettled)
	require.NoError(t, err)
	assert.Equal(t, []string{dg("a1")}, gone)
	_, err = s.ReceivedRecordOf(ctx, dg("a1"))
	assert.ErrorIs(t, err, ErrDeleted, "a deleted record is reported deleted, not missing")
	_, err = s.ReceivedRecordOf(ctx, dg("b1"))
	assert.NoError(t, err)

	// Sent again, it is acknowledged as deleted, not stored again.
	again := mustReceive(t, s, r1, nil)
	assert.True(t, again.Record.Deleted)
	assert.False(t, again.Record.Stored)
	assert.Equal(t, 1, receivedCount(t, s))
}

func TestForgetIsRefusedWhenTheHolderKeepsRecordsForTheShelfsLife(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	_, err := s.Forget(context.Background(), "deal-1", time.Now().Add(1000*time.Hour), func(context.Context, string) (bool, error) { return true, nil })
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, 1, receivedCount(t, s))
}

func TestReceiveWorksOnAVersionOneShelf(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{})
	var version int
	require.NoError(t, s.db.QueryRow(`SELECT version FROM shelf_meta`).Scan(&version))
	assert.Equal(t, ShelfVersion, version)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
}

func TestReceiveRefusesAfterRelease(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{})
	require.NoError(t, s.Release())
	_, err := s.Receive(context.Background(), chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	assert.ErrorIs(t, err, ErrReleased)
}

// withinTime fails the test when f does not return: a deadlock.
func withinTime(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("deadlock: the call did not return")
	}
}

// readerStatus is a StatusProvider whose link value reads the received
// store, as a deal profile builds last_received.
type readerStatus struct {
	shelf *Shelf
	reads int
}

func (r *readerStatus) Values(ctx context.Context, _, _ string) (string, LinkValue, json.RawMessage, bool, error) {
	p, err := r.shelf.CountedPrefix(ctx, "alice", "deal/a")
	if err != nil {
		return "", LinkValue{}, nil, false, err
	}
	if _, err := r.shelf.Evidence(ctx); err != nil {
		return "", LinkValue{}, nil, false, err
	}
	if _, err := r.shelf.ReceivedRecordOf(ctx, dg("a1")); err != nil {
		return "", LinkValue{}, nil, false, err
	}
	r.reads++
	link := LinkValue{CheckedOver: []string{}}
	if p.Size > 0 {
		link.LastReceived = &ReceivedPosition{LogID: "deal/a", Size: p.Size, RecordDigest: p.Last}
	}
	return "deal", link, json.RawMessage(`{}`), true, nil
}

// P1-1: the store's readers take no lock, so a StatusProvider calls them
// during a stamp, which holds the shelf's mutex.
func TestReceivedStoreReadersRunInsideAStamp(t *testing.T) {
	ctx := context.Background()
	f := &receiveFixture{shelfFixture: newShelfFixture(t), profile: newTestReceiveProfile("alice")}
	f.cfg.Receive = ReceiveConfig{Profile: f.profile}
	s := f.open(t)
	status := &readerStatus{shelf: s}
	c, err := s.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t), Status: status})
	require.NoError(t, err)
	mustAppend(t, mustOpenBook(t, s, "deal/ours"), observation("a", "1"))
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustTick(t, c, tickTime)
	withinTime(t, func() {
		require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
		mustAppend(t, mustOpenBook(t, s, "deal/ours"), observation("a", "2"))
		_, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
		require.NoError(t, err)
	})
	assert.Positive(t, status.reads)
}

// P1-2: an answer committed between a pushed offer's Read and the drop keeps
// the answered offer; Answered runs before the write transaction (a moved
// mark replans the drop), and a profile reading the shelf there does not
// deadlock.
func TestReceiveAnAnswerCommittedBeforeTheDropKeepsTheOffer(t *testing.T) {
	ctx := context.Background()
	f, s := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxOffersPerSender: 1}})
	mustReceive(t, s, offer(t, "bob", "o1", 0), nil)
	f.profile.onAdmit = func(r ReceivedRecord) {
		if r.Digest == dg("o2") {
			f.profile.mu.Lock()
			f.profile.answered[dg("o1")] = true
			f.profile.mu.Unlock()
		}
	}
	asked := 0
	f.profile.onAnswered = func(ReceivedRecord) {
		asked++
		_, err := s.CountedPrefix(ctx, "bob", "offers/o1")
		require.NoError(t, err)
		_, err = s.ReceivedRecordOf(ctx, dg("o1"))
		require.NoError(t, err)
	}
	withinTime(t, func() { mustReceive(t, s, offer(t, "bob", "o2", 0), nil) })
	assert.Equal(t, 1, asked)
	_, err := s.ReceivedRecordOf(ctx, dg("o1"))
	assert.NoError(t, err, "the answered offer is kept")
	_, err = s.ReceivedRecordOf(ctx, dg("o2"))
	assert.NoError(t, err)
}

// P1-3: a fork's position ends the counted prefix for good, whatever is
// deleted later, also after a restart.
func TestReceiveAForkSurvivesForgetAndRestart(t *testing.T) {
	ctx := context.Background()
	f, s := newReceiveFixture(t, ReceiveConfig{Retention: time.Hour})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-x", 2, "a2-other", "a1"), nil)
	want := CountedPrefix{Size: 1, Last: dg("a1"), ForkAt: 2}
	require.Equal(t, want, prefixOf(t, s, "alice", "deal/a"))
	gone, err := s.Forget(ctx, "deal-x", f.clock.t.Add(2*time.Hour), func(context.Context, string) (bool, error) { return true, nil })
	require.NoError(t, err)
	require.Equal(t, []string{dg("a2-other")}, gone)
	assert.Equal(t, want, prefixOf(t, s, "alice", "deal/a"), "deleting one record of a fork does not undo it")
	require.NoError(t, s.Release())
	assert.Equal(t, want, prefixOf(t, f.open(t), "alice", "deal/a"))
}

// P1-4: repeated forks at one position keep one evidence row.
func TestReceiveRepeatedForksAtOnePositionKeepOneEvidenceRow(t *testing.T) {
	ctx := context.Background()
	_, s := newReceiveFixture(t, ReceiveConfig{})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
	first := mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2-b", "a1"), nil)
	assert.Equal(t, []string{"deal-1"}, first.Changes.Forked)
	for _, name := range []string{"a2-c", "a2-d", "a2-e"} {
		got := mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, name, "a1"), nil)
		assert.Empty(t, got.Changes.Forked, "a fork already recorded at the position is not news")
	}
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	assert.Len(t, evidence, 1)
}

// P1-5: a proof past the records held grants no coverage; once the records
// arrive, a smaller proof they contradict is evidence and the larger one
// they recompute covers them.
func TestReceiveAProofPastTheRecordsGrantsNoCoverageUntilTheyRecomputeIt(t *testing.T) {
	ctx := context.Background()
	small := newProofHolder(t, "1", "2")
	large := newProofHolder(t, "x", "y", "z")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	a, b := small.proof(t, AudienceCounterparty), large.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, a)] = "alice"
	got := mustReceive(t, s, nil, &a)
	assert.True(t, got.Proof.Accepted)
	got = mustReceive(t, s, nil, &b)
	assert.True(t, got.Proof.Accepted)
	assert.Empty(t, got.Changes.Covered, "a proof past the records held grants no coverage")

	var covered, forked []string
	for _, r := range large.records(t, "alice", "deal-1") {
		got := mustReceive(t, s, r, nil)
		covered = append(covered, got.Changes.Covered...)
		forked = append(forked, got.Changes.Forked...)
	}
	assert.Equal(t, []string{"deal-1"}, covered)
	assert.Equal(t, []string{"deal-1"}, forked)
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, EvidenceCheckpointRecords, evidence[0].Kind)
	assert.Equal(t, a.BookCheckpoint, evidence[0].A)
}

// P2-6: a store or profile error in the proof part still returns the record
// part's committed outcome and changes.
func TestReceiveAProofErrorKeepsTheRecordsOutcome(t *testing.T) {
	h := newProofHolder(t, "1")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	boom := errors.New("profile store down")
	f.profile.proofErr = boom
	got, err := s.Receive(context.Background(), chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), &p)
	assert.ErrorIs(t, err, boom)
	require.NotNil(t, got.Record)
	assert.True(t, got.Record.Stored)
	assert.Nil(t, got.Proof)
	assert.Equal(t, []string{"deal-1"}, got.Changes.Grew)
}

// P2-7: a proof held first covers the deal when the record that completes
// the counted prefix up to its size arrives last.
func TestReceiveAProofHeldFirstCoversWhenAGapIsFilledLast(t *testing.T) {
	h := newProofHolder(t, "1", "2", "3")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	got := mustReceive(t, s, nil, &p)
	assert.Empty(t, got.Changes.Covered)
	records := h.records(t, "alice", "deal-1")
	require.Greater(t, len(records), 2)
	for i, r := range records {
		if i == 1 {
			continue
		}
		got := mustReceive(t, s, r, nil)
		assert.Empty(t, got.Changes.Covered)
	}
	got = mustReceive(t, s, records[1], nil)
	assert.Equal(t, []string{"deal-1"}, got.Changes.Covered)
	assert.Equal(t, []string{"deal-1"}, got.Changes.Grew)
}

// P2-8: a dropped offer's deal shrank; it never grew, also when dropping it
// leaves one record where two forked.
func TestReceiveADroppedOfferIsReportedShrankOnly(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxOffersPerSender: 1}})
	mustReceive(t, s, offer(t, "bob", "o1", 0), nil)
	got := mustReceive(t, s, offer(t, "bob", "o2", 0), nil)
	assert.Equal(t, []string{"offer-o1"}, got.Changes.Shrank)
	assert.Equal(t, []string{"offer-o2"}, got.Changes.Grew)

	// Two offers at one position fork; dropping the older one does not
	// resolve the fork, and its deal is not reported grown.
	_, s = newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxOffersPerSender: 2}})
	forkOffer := func(name string) []byte {
		return rec(t, testRecord{Digest: dg(name), Sender: "bob", Log: "offers/shared", Pos: 1, Deal: "offer-" + name, Offer: true})
	}
	mustReceive(t, s, forkOffer("f1"), nil)
	mustReceive(t, s, forkOffer("f2"), nil)
	got = mustReceive(t, s, offer(t, "bob", "o3", 0), nil)
	assert.NotContains(t, got.Changes.Grew, "offer-f1")
	assert.NotContains(t, got.Changes.Grew, "offer-f2")
	assert.Equal(t, uint64(1), prefixOf(t, s, "bob", "offers/shared").ForkAt)
}

// P3-9: Grew names the deals whose records entered the counted prefix, not
// every deal held in the log.
func TestReceiveGrewNamesOnlyDealsWhoseRecordsWereCounted(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-2", 3, "a3", "a2"), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-3", 5, "a5", "a4"), nil)
	got := mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
	assert.Equal(t, []string{"deal-1", "deal-2"}, got.Changes.Grew, "deal-3 is past the next gap")
	assert.Empty(t, got.Changes.Shrank)
}

// R2-1: a proof verified against records Forget deleted grants no coverage
// to other records arriving at the deleted positions: they are a fork.
func TestReceiveAProofDoesNotCoverRecordsReplacingDeletedOnes(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1", "2")
	other := newProofHolder(t, "x", "y")
	f, s := newReceiveFixture(t, ReceiveConfig{Retention: time.Hour})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	for _, r := range h.records(t, "alice", "deal-1") {
		mustReceive(t, s, r, nil)
	}
	require.Equal(t, []string{"deal-1"}, mustReceive(t, s, nil, &p).Changes.Covered)
	_, err := s.Forget(ctx, "deal-1", f.clock.t.Add(2*time.Hour), func(context.Context, string) (bool, error) { return true, nil })
	require.NoError(t, err)

	var covered, forked []string
	for _, r := range other.records(t, "alice", "deal-2") {
		got := mustReceive(t, s, r, nil)
		covered = append(covered, got.Changes.Covered...)
		forked = append(forked, got.Changes.Forked...)
	}
	assert.Empty(t, covered, "the proof was checked against records no longer held")
	assert.Contains(t, forked, "deal-2")
	assert.Equal(t, uint64(1), prefixOf(t, s, "alice", "deal/a").ForkAt, "another record at a deleted position is a fork")

	// A dropped offer's position is remembered the same way, within the
	// offer bounds (here the defaults).
	_, s2 := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxOffersPerSender: 1}})
	mustReceive(t, s2, rec(t, testRecord{Digest: dg("o1"), Sender: "bob", Log: "offers/x", Pos: 1, Deal: "offer-o1", Offer: true}), nil)
	mustReceive(t, s2, offer(t, "bob", "o2", 0), nil) // drops o1
	mustReceive(t, s2, rec(t, testRecord{Digest: dg("o1-other"), Sender: "bob", Log: "offers/x", Pos: 1, Deal: "offer-o3", Offer: true}), nil)
	assert.Equal(t, uint64(1), prefixOf(t, s2, "bob", "offers/x").ForkAt)
}

// R2-2: a sender rotating log ids, each with two conflicting offers at
// position 1, keeps no more evidence than the offer evidence bounds allow;
// each fork still ends its log's counted prefix.
func TestReceiveOfferForksAcrossRotatedLogsKeepBoundedEvidence(t *testing.T) {
	ctx := context.Background()
	_, s := newReceiveFixture(t, ReceiveConfig{Bounds: ReceiveBounds{MaxOffersPerSender: 4, MaxOfferEvidencePerSender: 2, MaxOfferEvidence: 3}})
	push := func(sender, log, name string) {
		mustReceive(t, s, rec(t, testRecord{Digest: dg(name), Sender: sender, Log: log, Pos: 1, Deal: "offer-" + name, Offer: true, Pad: strings.Repeat("x", 100)}), nil)
	}
	for i := 0; i < 6; i++ {
		log := "offers/rot-" + string(rune('a'+i))
		push("bob", log, log+"-1")
		push("bob", log, log+"-2")
		assert.Equal(t, uint64(1), prefixOf(t, s, "bob", log).ForkAt)
	}
	for i := 0; i < 3; i++ {
		log := "offers/carol-" + string(rune('a'+i))
		push("carol", log, log+"-1")
		push("carol", log, log+"-2")
	}
	evidence, err := s.Evidence(ctx)
	require.NoError(t, err)
	assert.Len(t, evidence, 3, "two of bob's, one of carol's, then the total bound")
	var bytes int
	for _, e := range evidence {
		bytes += len(e.A) + len(e.B)
	}
	assert.LessOrEqual(t, bytes, 3*2*600)

	// What offers leave behind, the dropped offers' positions and the
	// conflicts in logs of offers alone, stays within the same bounds: two
	// rows for bob, three in all.
	var vacated, conflicts, bobs int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM shelf_received_vacated`).Scan(&vacated))
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM shelf_received_conflicts`).Scan(&conflicts))
	require.NoError(t, s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM shelf_received_vacated WHERE sender_key = 'bob') + (SELECT COUNT(*) FROM shelf_received_conflicts WHERE sender_key = 'bob')`).Scan(&bobs))
	assert.LessOrEqual(t, vacated+conflicts, 3)
	assert.LessOrEqual(t, bobs, 2)
	// A fork kept within the bounds ends its log's prefix for good, though
	// both its offers were dropped; one past them is forgotten once its
	// offers are dropped.
	_, err = s.ReceivedRecordOf(ctx, dg("offers/rot-a-1"))
	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, uint64(1), prefixOf(t, s, "bob", "offers/rot-a").ForkAt)
	_, err = s.ReceivedRecordOf(ctx, dg("offers/rot-c-2"))
	require.ErrorIs(t, err, ErrNotFound)
	assert.Zero(t, prefixOf(t, s, "bob", "offers/rot-c").ForkAt)
}

// R2-3: Answered never runs inside Receive's transaction. A journal reply
// appended after Answered said no, and before the transaction, moves the
// mark: the drop is planned again, keeps the answered offer, and the
// append does not wait on Receive's write lock.
func TestReceiveRetriesTheDropWhenTheJournalChangesUnderIt(t *testing.T) {
	ctx := context.Background()
	f := &receiveFixture{shelfFixture: newShelfFixture(t), profile: newTestReceiveProfile("alice")}
	f.cfg.Receive = ReceiveConfig{Profile: f.profile, Bounds: ReceiveBounds{MaxOffersPerSender: 1}}
	s := f.open(t)
	c, _ := newStampCadence(t, s)
	mustAppend(t, mustOpenBook(t, s, "deal/ours"), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	journal, err := s.Journal(ctx)
	require.NoError(t, err)

	mustReceive(t, s, offer(t, "bob", "o1", 0), nil)
	asked := 0
	var appendErr error
	f.profile.afterAnswered = func(r ReceivedRecord) {
		asked++
		if asked == 1 {
			// Our reply to o1, committed after Answered said no.
			_, appendErr = journal.Append(ctx, observation("reply", r.Digest))
			f.profile.mu.Lock()
			f.profile.answered[r.Digest] = true
			f.profile.mu.Unlock()
		}
	}
	withinTime(t, func() { mustReceive(t, s, offer(t, "bob", "o2", 0), nil) })
	require.NoError(t, appendErr, "the journal append does not wait on Receive")
	assert.Equal(t, 2, asked, "the drop was planned again")
	_, err = s.ReceivedRecordOf(ctx, dg("o1"))
	assert.NoError(t, err, "the answered offer is kept")
}

// Round 4: when our books change under every plan, the drop is refused as
// busy after three attempts, and nothing is stored or dropped.
func TestReceiveRefusesBusyWhenTheMarkKeepsMoving(t *testing.T) {
	ctx := context.Background()
	f := &receiveFixture{shelfFixture: newShelfFixture(t), profile: newTestReceiveProfile("alice")}
	f.cfg.Receive = ReceiveConfig{Profile: f.profile, Bounds: ReceiveBounds{MaxOffersPerSender: 1}}
	s := f.open(t)
	c, _ := newStampCadence(t, s)
	mustAppend(t, mustOpenBook(t, s, "deal/ours"), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	journal, err := s.Journal(ctx)
	require.NoError(t, err)

	mustReceive(t, s, offer(t, "bob", "o1", 0), nil)
	asked := 0
	f.profile.afterAnswered = func(r ReceivedRecord) {
		asked++
		_, err := journal.Append(ctx, observation("busy", fmt.Sprint(asked)))
		require.NoError(t, err)
	}
	var res ReceiveResult
	withinTime(t, func() { res, err = s.Receive(ctx, offer(t, "bob", "o2", 0), nil) })
	assert.ErrorIs(t, err, ErrBusy)
	require.NotNil(t, res.Record)
	assert.ErrorIs(t, res.Record.Refused, ErrBusy)
	assert.Equal(t, 3, asked, "three plans, then the refusal")
	_, err = s.ReceivedRecordOf(ctx, dg("o1"))
	assert.NoError(t, err, "nothing was dropped")
	_, err = s.ReceivedRecordOf(ctx, dg("o2"))
	assert.ErrorIs(t, err, ErrNotFound, "nothing was stored")
}

// Round 4: a position past SQLite's signed 64-bit range is refused as
// invalid, as Python and Rust refuse it.
func TestReceiveRefusesAPositionPastInt64(t *testing.T) {
	r := ReceivedRecord{Digest: dg("x"), SenderKey: "bob", LogID: "l", DealID: "d", Position: math.MaxInt64 + 1, Prev: dg("p")}
	assert.Error(t, r.validate())
	r.Position = math.MaxInt64
	assert.NoError(t, r.validate())
}

// R2-4: a proof part whose transaction does not commit adds no changes;
// the committed record part's changes stay.
func TestReceiveAFailedProofCommitReportsNoProofChanges(t *testing.T) {
	h := newProofHolder(t, "1", "2")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	records := h.records(t, "alice", "deal-1")
	for _, r := range records[:len(records)-1] {
		mustReceive(t, s, r, nil)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	receiveBeforeCommit = func(_ context.Context, part string) {
		if part == "proof" {
			cancel()
		}
	}
	t.Cleanup(func() { receiveBeforeCommit = nil })
	got, err := s.Receive(ctx, records[len(records)-1], &p)
	require.Error(t, err)
	assert.Equal(t, []string{"deal-1"}, got.Changes.Grew, "the record part committed")
	assert.Empty(t, got.Changes.Covered, "the proof part did not")
	assert.Zero(t, proofRows(t, s))
}

// evidencebook#543: CountedPrefix reads the records and the recorded forks
// in one snapshot. Receives that commit between the two reads (the prefix
// grows to a2, then two records at 5 fork it) are seen whole or not at all;
// two snapshots would give the old size with the new fork, Size 1 ForkAt 5,
// a state the store never held.
func TestCountedPrefixReadsOneSnapshot(t *testing.T) {
	_, s := newReceiveFixture(t, ReceiveConfig{})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	fired := false
	receivedPrefixBetween = func(context.Context) {
		if fired {
			return
		}
		fired = true
		mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
		mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 5, "a5", "a4"), nil)
		mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 5, "a5-other", "a4"), nil)
	}
	t.Cleanup(func() { receivedPrefixBetween = nil })
	got := prefixOf(t, s, "alice", "deal/a")
	require.True(t, fired)
	assert.Equal(t, CountedPrefix{Size: 1, Last: dg("a1")}, got, "the state before the receives")
	assert.Equal(t, CountedPrefix{Size: 2, Last: dg("a2"), ForkAt: 5}, prefixOf(t, s, "alice", "deal/a"), "and after them")
}
