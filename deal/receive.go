// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"database/sql"
	"errors"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// Received is a record from the other side that the caller verified on its
// own and that evidencebook.Shelf.Receive stored (or already held): one sent
// whole (an offer, a decline or a cancellation; xdealv1.VerifySent) or one
// of the other side's deal-book records (xdealv1.VerifyBook).
type Received struct {
	Sent *xdealv1.Sent
	Book *xdealv1.BookRead
	// Pair is the sent record's pair as received, kept with a received offer
	// for the status provider's payment check.
	Pair []byte
}

// NoteReceived records what the store needs of a record the shelf took, so
// the next call knows the deal: a received offer opens the deal on the
// acceptor's side; a decline or a cancellation ends an offer not yet agreed
// to; a deal-book record names the other side's book and key, and its first
// record, the agreement, is what the offerer's agreement names.
func (s *Store) NoteReceived(ctx context.Context, r Received) error {
	switch {
	case r.Sent != nil && r.Sent.Record.Block.RecordType == xdealv1.TypePropose:
		dealID := r.Sent.Record.Block.DealID
		prop, _ := proposeOf(r.Sent.Record)
		payload, digest, err := xdealv1.Encode(r.Sent.Record)
		if err != nil {
			return err
		}
		tx, err := s.cfg.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err = tx.ExecContext(ctx, `INSERT INTO deal_versions (deal_id, version, own_log_id, counterparty_log_id, state) VALUES (?, ?, '', '', 'offer_received') ON CONFLICT(deal_id) DO NOTHING`, dealID, version); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, ?, '', '', '') ON CONFLICT(deal_id) DO NOTHING`,
			dealID, xdealv1.SideAcceptor, oppositeRole(prop.Role), digest, r.Sent.KeyID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO deal_v1_offers (offer_digest, deal_id, signer_key, payload, pair) VALUES (?, ?, ?, ?, ?) ON CONFLICT(offer_digest) DO UPDATE SET pair=excluded.pair WHERE deal_v1_offers.pair IS NULL`, digest, dealID, r.Sent.KeyID, payload, r.Pair); err != nil {
			return err
		}
		if err := noteStops(); err != nil {
			return err
		}
		return tx.Commit()
	case r.Sent != nil:
		state := "declined"
		if ag, ok := agreeOf(r.Sent.Record); ok && ag.Kind == xdealv1.KindCancellation {
			state = "cancelled"
		}
		_, err := s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET state=? WHERE deal_id=? AND state IN ('offered','offer_received')`, state, r.Sent.Record.Block.DealID)
		return err
	case r.Book != nil:
		dealID := r.Book.Record.Block.DealID
		d, err := s.Deal(ctx, dealID)
		if err != nil || d == nil {
			return err
		}
		if err = s.ensureOfferedRow(ctx, d); err != nil {
			return err
		}
		// A deal with no layer on the other side has no counterparty: a
		// record received for it (a referee's ruling) is kept, and no one is
		// registered as the other side.
		if noLayer, err := s.NoLayer(ctx, d); err != nil || noLayer {
			return err
		}
		if d.CounterpartyLogID == "" {
			if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET counterparty_log_id=? WHERE deal_id=?`, r.Book.LogID, dealID); err != nil {
				return err
			}
		}
		if d.CounterpartyKey == "" || d.CounterpartyKey == r.Book.KeyID {
			if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_v1 SET counterparty_key=? WHERE deal_id=?`, r.Book.KeyID, dealID); err != nil {
				return err
			}
		}
		if ag, ok := agreeOf(r.Book.Record); ok && ag.Kind == xdealv1.KindAgreement && r.Book.Seq == 1 && r.Book.LogID == firstNonEmpty(d.CounterpartyLogID, r.Book.LogID) {
			_, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_v1 SET counterparty_agreement=? WHERE deal_id=? AND counterparty_agreement=''`, r.Book.RecordID, dealID)
		}
		return err
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// noteStops is a seam for tests: it stops the bookkeeping of a received
// offer after its rows were written and before they are committed, as a
// crash there would.
var noteStops = func() error { return nil }

// ensureOfferedRow gives an offer the holder sent before the deal's own row
// existed (an early build wrote only deal_versions) the row Agree and
// NoteReceived update, rebuilt from the stored offer.
func (s *Store) ensureOfferedRow(ctx context.Context, d *Deal) error {
	if d.Side != "" {
		return nil
	}
	var payload []byte
	var digest string
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT payload, record_digest FROM deal_sent WHERE deal_id=? AND kind=? ORDER BY id LIMIT 1`, d.DealID, xdealv1.TypePropose).Scan(&payload, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rec, err := xdealv1.Decode(payload)
	if err != nil {
		return err
	}
	prop, ok := proposeOf(rec)
	if !ok {
		return nil
	}
	if _, err = s.cfg.DB.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, '', '', '', '') ON CONFLICT(deal_id) DO NOTHING`,
		d.DealID, xdealv1.SideOfferer, prop.Role, digest); err != nil {
		return err
	}
	d.Side, d.Role, d.OfferDigest = xdealv1.SideOfferer, prop.Role, digest
	return nil
}
