// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// HistoryCard is the profile's history_card/1 (ER §8.1) over the cadence
// log, as a staple the host attaches to its own message (evidencebook#341).
// Its stream is the cadence log: the card lists the cadence checkpoint of
// every finished tick from the first through tick n (the newest finished
// tick when n is 0), each with the consistency proof from the one before,
// and is as of tick n's checkpoint, the one the witness sees. The shelf's
// Signer signs the reply. It is the same staple Book.Staple gives a book,
// so CheckStaple accepts it for holder KeyID(the shelf's Signer), and the
// card holds no record content. ErrNotFound when the cadence has no such
// finished tick. ErrOverLimit when the card would list more than
// DefaultMaxAnswerItems ticks (about 35 days at the default interval), the
// bound a Book's card has: history_card/1 is linear in checkpoints, so a
// longer cadence staples its standard_history/1 (StandardHistoryAnchor)
// instead (evidencebook#354).
//
// Deprecated: offer staples carry history_card/2 (Steven u539); use
// HistoryCard2. HistoryCard stays until every caller has moved (Muse).
func (c *Cadence) HistoryCard(ctx context.Context, n uint64) (Staple, error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return Staple{}, ErrReleased
	}
	n, err := c.anchorTickLocked(ctx, n)
	if err != nil {
		return Staple{}, err
	}
	if n > DefaultMaxAnswerItems {
		return Staple{}, fmt.Errorf("%w: the card would list %d ticks, over %d", ErrOverLimit, n, DefaultMaxAnswerItems)
	}
	var chain []Checkpoint
	for i := uint64(1); i <= n; i++ {
		cp, err := c.tickCheckpointLocked(ctx, i, n)
		if err != nil {
			return Staple{}, err
		}
		chain = append(chain, cp)
	}
	anchor := chain[len(chain)-1]
	history, err := historyEntries(ctx, c.log, chain, anchor)
	if err != nil {
		return Staple{}, err
	}
	subject := Subject{Kind: SubjectCheckpoints}
	artifact, err := evidencerequest.EncodeJCS(map[string]any{
		"anchor": anchorDigest(anchor), "evidence_stream": anchor.LogID,
		"subject": subject.Value(), "derivation": DerivationHistoryCard, "history": history,
	})
	if err != nil {
		return Staple{}, err
	}
	return c.signStapleLocked(ctx, subject, DerivationHistoryCard, anchor, artifact)
}

// anchorTickLocked resolves n, the tick a cadence staple is as of: 0 is the
// newest finished tick, since an unfinished tick (checkpoint_size 0) has no
// checkpoint yet. ErrNotFound when the cadence has no finished tick.
func (c *Cadence) anchorTickLocked(ctx context.Context, n uint64) (uint64, error) {
	if n != 0 {
		return n, nil
	}
	if err := c.shelf.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0`).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: the cadence has no finished tick", ErrNotFound)
	}
	return n, nil
}

// tickCheckpointLocked is the checkpoint of finished tick i, where newest is
// the staple's anchor tick: an unfinished anchor is ErrNotFound, and an
// unfinished tick before it ErrCorrupt (only the newest tick can be
// unfinished).
func (c *Cadence) tickCheckpointLocked(ctx context.Context, i, newest uint64) (Checkpoint, error) {
	tick, err := c.tickLocked(ctx, i)
	if err != nil {
		return Checkpoint{}, err
	}
	if tick.CheckpointSize == 0 {
		if i == newest {
			return Checkpoint{}, fmt.Errorf("%w: tick %d is not finished", ErrNotFound, newest)
		}
		return Checkpoint{}, fmt.Errorf("%w: tick %d is unfinished before tick %d", ErrCorrupt, i, newest)
	}
	return checkpointFromStatement(tick.Checkpoint)
}

// HistoryCard2 is the profile's history_card/2 (ER -03 §8.1.2) over the
// cadence log, as the staple a host attaches to its own message: offer
// staples always carry it (Steven u539, which replaces the history_card/1
// staple and its standard_history/1 fallback, evidencebook#354). It lists the
// cadence checkpoints of the finished ticks at the sampled positions
// P(n-1, window), each with the consistency proof from the one listed
// before, as of tick n's checkpoint (n 0: the newest finished tick). Only the
// sampled ticks are read, so it has no tick cap. window 0 is
// DefaultHistoryWindow; outside MinHistoryWindow..MaxHistoryWindow is
// ErrInvalid. The shelf's Signer signs the reply, and CheckStaple accepts it
// for holder KeyID(the shelf's Signer). It holds no record content and no
// deal counts. ErrNotFound when the cadence has no such finished tick.
func (c *Cadence) HistoryCard2(ctx context.Context, n uint64, window int) (Staple, error) {
	if window == 0 {
		window = DefaultHistoryWindow
	}
	if window < MinHistoryWindow || window > MaxHistoryWindow {
		return Staple{}, fmt.Errorf("%w: a history window is %d to %d, not %d", ErrInvalid, MinHistoryWindow, MaxHistoryWindow, window)
	}
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return Staple{}, ErrReleased
	}
	n, err := c.anchorTickLocked(ctx, n)
	if err != nil {
		return Staple{}, err
	}
	// Tick i (from 1) is position i-1 of the cadence's checkpoint stream.
	positions := evidencerequest.HistoryPositions(n-1, uint64(window))
	sampled := make([]Checkpoint, len(positions))
	for k, p := range positions {
		if sampled[k], err = c.tickCheckpointLocked(ctx, p+1, n); err != nil {
			return Staple{}, err
		}
	}
	anchor := sampled[len(sampled)-1]
	history, err := sampledEntries2(ctx, c.log, positions, sampled, anchor)
	if err != nil {
		return Staple{}, err
	}
	subject := Subject{Kind: SubjectCheckpoints}
	artifact, err := evidencerequest.EncodeJCS(map[string]any{
		"anchor": anchorDigest(anchor), "evidence_stream": anchor.LogID, "subject": subject.Value(),
		"derivation": DerivationHistoryCard2, "history": history, "window": uint64(window),
	})
	if err != nil {
		return Staple{}, err
	}
	return c.signStapleLocked(ctx, subject, DerivationHistoryCard2, anchor, artifact)
}

// signStapleLocked makes the staple of a cadence card: the holder's own
// request for derivation, pinned to anchor, and the reply carrying artifact,
// signed by the shelf's Signer.
func (c *Cadence) signStapleLocked(ctx context.Context, subject Subject, derivation string, anchor Checkpoint, artifact []byte) (Staple, error) {
	s := c.shelf
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return Staple{}, err
	}
	octets, err := EncodeRequest(EvidenceRequest{
		Subject: subject, Derivation: derivation,
		Coverage: Coverage{ExpectedPin: anchorDigest(anchor)},
		Nonce:    hex.EncodeToString(nonce), Profile: ProfileID,
	}, MediaTypeJSON)
	if err != nil {
		return Staple{}, err
	}
	request := Message{Octets: octets, MediaType: MediaTypeJSON}
	now := time.Now()
	if s.cfg.Now != nil {
		now = s.cfg.Now()
	}
	envelope := Envelope{
		Profile: ProfileID, RequestDigest: digestBytes(octets), Subject: subject, Derivation: derivation,
		Anchor: Anchor{Digest: anchorDigest(anchor)}, ArtifactDigest: digestBytes(artifact),
		Responder: KeyID(s.cfg.Signer), IssuedAt: evidencerequest.ProfileTime(now), Artifact: artifact,
	}
	if envelope, err = envelope.Sign(ctx, s.cfg.Signer); err != nil {
		return Staple{}, err
	}
	reply, err := evidencerequest.EncodeReply(MediaTypeJSON, &envelope, nil)
	if err != nil {
		return Staple{}, err
	}
	return Staple{Request: request, Reply: reply}, nil
}
