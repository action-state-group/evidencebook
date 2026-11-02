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
// finished tick.
func (c *Cadence) HistoryCard(ctx context.Context, n uint64) (Staple, error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return Staple{}, ErrReleased
	}
	if n == 0 {
		// An unfinished tick (checkpoint_size 0) has no checkpoint yet.
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0`).Scan(&n); err != nil {
			return Staple{}, err
		}
		if n == 0 {
			return Staple{}, fmt.Errorf("%w: the cadence has no finished tick", ErrNotFound)
		}
	}
	var chain []Checkpoint
	for i := uint64(1); i <= n; i++ {
		tick, err := c.tickLocked(ctx, i)
		if err != nil {
			return Staple{}, err
		}
		if tick.CheckpointSize == 0 {
			if i == n {
				return Staple{}, fmt.Errorf("%w: tick %d is not finished", ErrNotFound, n)
			}
			// Only the newest tick can be unfinished.
			return Staple{}, fmt.Errorf("%w: tick %d is unfinished before tick %d", ErrCorrupt, i, n)
		}
		cp, err := checkpointFromStatement(tick.Checkpoint)
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
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return Staple{}, err
	}
	octets, err := EncodeRequest(EvidenceRequest{
		Subject: subject, Derivation: DerivationHistoryCard,
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
		Profile: ProfileID, RequestDigest: digestBytes(octets), Subject: subject, Derivation: DerivationHistoryCard,
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
