// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// The x-deal-v1 deal store is evidencebook's package deal (evidencebook#899):
// it owns the deal tables (deal_versions, deal_sent, deal_v1, deal_v1_offers,
// deal_v1_admits), the journal send writer and the deal-book writer, and
// seals the deal.open records (offer, cancellation, decline, admit,
// agreement). The plugin keeps those verbs' flags, files and output, and for
// now its own deal_v1_events and deal_v1_sends tables and the bodies of
// perform, settle, statement and dispute. This file opens the store over the
// session's shelf and maps its errors to the CLI's.

type (
	v1Deal       = dealapi.Deal
	bookSealed   = dealapi.Sealed
	bookRecordIn = dealapi.BookRecord
	offerHeld    = dealapi.HeldOffer
)

// dealStore opens the session's deal store once, on its shelf and its store
// file. Only x-deal-v1 code calls it (a verb's ensureV1Tables, a reader of a
// v1 deal, the status provider of a v1 book), so a store that never holds an
// x-deal-v1 deal never gains its tables. A read session (openDealStoreRead)
// writes nothing for it: it takes the profile's log id as it is, never saving
// a new one, since only Agree, a writer's verb, names it.
func (s *dealSession) dealStore(ctx context.Context) (*dealapi.Store, error) {
	if s.store != nil {
		return s.store, nil
	}
	if s.p.Checkpoint.Signing == (cli.Secret{}) {
		return nil, errNoCheckpointKey(s.p)
	}
	checkpoint, err := cli.PrivateKey(s.p.Checkpoint.Signing)
	if err != nil {
		return nil, err
	}
	logID := s.p.LogID
	if !s.shared {
		p, err := s.cadenceProfile()
		if err != nil {
			return nil, err
		}
		logID = p.LogID
	} else if logID == "" {
		logID = readSessionShelfLog
	}
	s.store, err = dealapi.Open(ctx, dealapi.Config{
		Shelf: s.shelf, DB: s.db,
		SigningKey:    s.key.Public().(ed25519.PublicKey),
		CheckpointKey: checkpoint.Public().(ed25519.PublicKey),
		ShelfLogID:    logID,
		// The deal clock, read each time, as every v1 record's time was.
		Now: func() time.Time { return cli.DealClock() },
	})
	return s.store, err
}

// readSessionShelfLog stands in for the cadence log id of a read session on a
// profile that has none yet: the store needs one, and a read session seals no
// agreement, which is the only record that names it.
const readSessionShelfLog = "deal-cadence/unset"

// ensureV1Tables opens the deal store, which creates its tables, and the
// plugin's own v1 tables: the local event list and the payment sends.
func (s *dealSession) ensureV1Tables(ctx context.Context) error {
	if _, err := s.dealStore(ctx); err != nil {
		return err
	}
	for _, stmt := range []string{dealV1EventsTable, dealV1SendsTable} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// libErr is a deal-store error as the CLI reports it: a refusal of the input
// is an input error (its argument named by its flag), stored state that
// disagrees is a conflict, and anything else passes as it is.
func libErr(err error) error {
	var in *dealapi.InputError
	if errors.As(err, &in) {
		if in.Field != "" {
			return cli.InputError("--" + in.Field + in.Reason)
		}
		return cli.InputError(in.Reason)
	}
	var conflict *dealapi.ConflictError
	if errors.As(err, &conflict) {
		return fmt.Errorf("%w: %s", cli.ErrConflict, conflict.Reason)
	}
	return err
}

// v1DealOf reads a deal's rows; nil when the deal is not a v1 deal here.
func (s *dealSession) v1DealOf(ctx context.Context, dealID string) (*v1Deal, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	return st.Deal(ctx, dealID)
}

// offerByDigest finds an offer by its record digest among the offers this
// profile sent and the ones it received.
func (s *dealSession) offerByDigest(ctx context.Context, digest string) (*offerHeld, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	return st.HeldOffer(ctx, digest)
}

// ownBookV1 is the holder's deal book of the deal as records, in order.
func (s *dealSession) ownBookV1(ctx context.Context, d *v1Deal) ([]xdealv1.Record, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	book, err := st.OwnBook(ctx, d)
	return book, libErr(err)
}

// v1NoLayer reports whether a deal is with a counterparty that runs no layer.
func (s *dealSession) v1NoLayer(ctx context.Context, d *v1Deal) (bool, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return false, err
	}
	return st.NoLayer(ctx, d)
}

// sealBookV1 appends one record to the holder's deal book through the deal
// store's one writer.
func (s *dealSession) sealBookV1(ctx context.Context, dealID string, in bookRecordIn) (bookSealed, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return bookSealed{}, err
	}
	sealed, err := st.SealInBook(ctx, dealID, in)
	return sealed, libErr(err)
}

// repairIfStopped records an agreement a stopped run appended, and leaves d
// as the repair left it.
func (s *dealSession) repairIfStopped(ctx context.Context, d *v1Deal) error {
	st, err := s.dealStore(ctx)
	if err != nil {
		return err
	}
	got, err := st.Repair(ctx, d.DealID)
	if err != nil {
		return libErr(err)
	}
	if got != nil {
		*d = *got
	}
	return nil
}

// sentOfferPair is the pair of the offer whose record digest is given, for
// the status provider's payment check (Store.OfferPair).
func (s *dealSession) sentOfferPair(ctx context.Context, offerDigest string) ([]byte, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	return st.OfferPair(ctx, offerDigest)
}

// writeSent writes a sent record's pair to path, or to the command's output
// with no path, and records it delivered. A file already at path holding the
// same pair is the earlier run's: it counts as written.
func (s *dealSession) writeSent(ctx context.Context, c *cobra.Command, path string, sent dealapi.Sent) error {
	data := append(bytes.Clone(sent.Pair), '\n')
	if path == "" {
		if _, err := c.OutOrStdout().Write(data); err != nil {
			return err
		}
	} else if err := cli.AtomicFile(path, data, false); err != nil {
		if prev, rerr := os.ReadFile(path); rerr != nil || !bytes.Equal(prev, data) {
			return err
		}
	}
	st, err := s.dealStore(ctx)
	if err != nil {
		return err
	}
	return st.MarkWritten(ctx, sent)
}

func proposeOf(r xdealv1.Record) (*xdealv1.ProposeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.ProposeBody:
		return b, true
	case xdealv1.ProposeBody:
		return &b, true
	}
	return nil, false
}

func agreeOf(r xdealv1.Record) (*xdealv1.AgreeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.AgreeBody:
		return b, true
	case xdealv1.AgreeBody:
		return &b, true
	}
	return nil, false
}
