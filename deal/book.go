// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/action-state-group/evidencebook/internal/placeappend"
)

// BookRecord is what a deal-book call gives the writer: the body and the
// marks its type carries. A deal-book record carries no block refs: it cites
// by its body's own fields. The writer fills the block's chain: deal_id, at,
// seq, prev, agreement_ref and seen.
type BookRecord struct {
	Body  xdealv1.Body
	Marks []string
	// Seen, when set, is the record id `seen` names instead of the other
	// side's last counted record: the offerer's agreement names the
	// acceptor's agreement, whatever else it has held since.
	Seen string
	// Members are further top-level members of the record's Capsule (the
	// settlement leg a settle record carries), passed to the entry as they
	// are.
	Members map[string]json.RawMessage
	// Validate, when set, checks the finished record (its chain members
	// included) before anything is stored: what is checked is what is
	// sealed.
	Validate func(xdealv1.Record) error
}

// Sealed is a record the deal-book writer sealed: its place, its record id
// (its Capsule id, what `seen` names), its record digest (what `prev` and
// `agreement_ref` name), the record and the pair that travels.
type Sealed struct {
	Seq      uint64
	RecordID string
	Digest   string
	Record   xdealv1.Record
	Pair     []byte
}

func isAgreement(b xdealv1.Body) bool {
	switch b.(type) {
	case xdealv1.AgreeBody, *xdealv1.AgreeBody:
		return true
	}
	return false
}

// SealInBook appends one record to the holder's deal book of dealID and
// returns it with the pair that is sent. The first record of a book is the
// agreement: it opens the book under a fresh random log id. Every later
// record names the book's agreement and the record before it, and carries
// `seen`: the last record held from the other side's counted prefix, null
// when the other side keeps no records of its own. The record passes the
// profile's checks before anything is stored. Every deal-book record the
// holder seals goes through this writer (an agreement, an act, a settlement,
// a statement, a dispute), so a caller adds no chain member of its own.
func (s *Store) SealInBook(ctx context.Context, dealID string, in BookRecord) (Sealed, error) {
	d, err := s.Deal(ctx, dealID)
	if err != nil {
		return Sealed{}, err
	}
	if d == nil {
		return Sealed{}, inputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
	}
	logID := d.OwnLogID
	if logID == "" {
		if !isAgreement(in.Body) {
			return Sealed{}, inputError("deal " + dealID + " has no deal book yet: it opens at your agreement (`deal agree`)")
		}
		if logID, err = s.newOwnLogID(); err != nil {
			return Sealed{}, err
		}
		// The log id is stored before the book exists: a run that stops after
		// the record is appended finds the book, never makes a second one.
		if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET own_log_id=?, state='agreeing' WHERE deal_id=?`, logID, dealID); err != nil {
			return Sealed{}, err
		}
		d.OwnLogID = logID
	}
	book, err := s.cfg.Shelf.Open(ctx, logID)
	if err != nil {
		return Sealed{}, err
	}
	if d.OwnAgreementID == "" && book.Size() > 0 {
		// A run stopped between appending the agreement and recording it.
		if err = s.repairOpening(ctx, d, book); err != nil {
			return Sealed{}, err
		}
	}
	noLayer, err := s.NoLayer(ctx, d)
	if err != nil {
		return Sealed{}, err
	}
	var out Sealed
	err = placeappend.Append(ctx, book, func(size uint64) (placeappend.Try, error) {
		opening := size == 0
		if opening && !isAgreement(in.Body) {
			return placeappend.Try{}, inputError("deal " + dealID + " has no deal book yet: it opens at your agreement (`deal agree`)")
		}
		// One clock read: the record's at, and on a windowed store the time
		// its book is placed by.
		now := s.now().Truncate(time.Second)
		block := xdealv1.Block{
			Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID,
			RecordType: in.Body.RecordType(), At: now.Format(time.RFC3339), Marks: in.Marks,
			Seq: int64(size) + 1,
		}
		var prevID string
		if size > 0 {
			last, err := book.Query(ctx, evidencebook.Filter{FromSeq: size, ToSeq: size})
			if err != nil {
				return placeappend.Try{}, err
			}
			if len(last) != 1 || len(last[0].Header.PayloadCommitments) != 1 {
				return placeappend.Try{}, conflictf("the deal book's last record has no record digest")
			}
			prevID = last[0].RecordID
			prev := xdealv1.RefTo(last[0].Header.PayloadCommitments[0])
			block.Prev = &prev
			agreement := xdealv1.RefTo(d.OwnAgreementDigest)
			block.AgreementRef = &agreement
		}
		block.Seen = xdealv1.NullOf[string]()
		// A book of a counterparty with no layer has seen null throughout
		// (PROFILE §4.7), whatever else the holder has received (a referee's
		// ruling, say).
		if !noLayer && d.CounterpartyKey != "" && d.CounterpartyLogID != "" {
			cp, err := s.cfg.Shelf.CountedPrefix(ctx, d.CounterpartyKey, d.CounterpartyLogID)
			if err != nil {
				return placeappend.Try{}, err
			}
			if cp.Last != "" {
				block.Seen = xdealv1.Some(cp.Last)
			}
		}
		if in.Seen != "" && !noLayer {
			block.Seen = xdealv1.Some(in.Seen)
		}
		rec := xdealv1.Record{Block: block, Body: in.Body}
		if in.Validate != nil {
			if err := in.Validate(rec); err != nil {
				return placeappend.Try{}, err
			}
		}
		if err := xdealv1.Check(rec); err != nil {
			return placeappend.Try{}, inputError("the record does not pass the x-deal-v1 checks: " + err.Error())
		}
		if xdealv1.PlacementOf(rec) != xdealv1.PlacementDealBook {
			return placeappend.Try{}, inputError("a " + block.RecordType + " record is not sealed in a deal book")
		}
		payload, digest, err := xdealv1.Encode(rec)
		if err != nil {
			return placeappend.Try{}, err
		}
		entry, err := xdealv1.BookEntry(rec, prevID)
		if err != nil {
			return placeappend.Try{}, err
		}
		entry.Members = in.Members
		bookAppendHook()
		try := placeappend.Try{Entry: entry, Done: func(stored evidencebook.Record) error {
			sealed, err := book.Sealed(ctx, stored.RecordID)
			if err != nil {
				return err
			}
			pair, err := xdealv1.BookPair(sealed, payload)
			if err != nil {
				return err
			}
			if opening {
				if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_v1 SET own_agreement_id=?, own_agreement_digest=? WHERE deal_id=?`, stored.RecordID, digest, dealID); err != nil {
					return err
				}
				if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET state='agreed' WHERE deal_id=?`, dealID); err != nil {
					return err
				}
			}
			out = Sealed{Seq: size + 1, RecordID: stored.RecordID, Digest: digest, Record: rec, Pair: pair}
			return nil
		}}
		if opening && s.cfg.Shelf.WindowPolicy() != nil {
			// A windowed store places the deal's book in the window its
			// agreement is sealed in, and appends the agreement in the same
			// step. Place skips the append when the book is no longer empty:
			// another write took the place. A window that closed between the
			// record's time and its placement is a place taken too.
			try.Place = func(appendAt func(*evidencebook.Book) error) error {
				appended := false
				_, _, err := s.cfg.Shelf.Place(ctx, logID, now, func(b *evidencebook.Book) error {
					appended = true
					return appendAt(b)
				})
				if err == nil && !appended || errors.Is(err, evidencebook.ErrWindowClosed) {
					return evidencebook.ErrBusy
				}
				return err
			}
		}
		return try, nil
	}, bookAppendTries)
	if errors.Is(err, placeappend.ErrKeptChanging) {
		return Sealed{}, conflictf("the deal book kept changing while the record was sealed; run the command again")
	}
	return out, err
}

// bookAppendTries is how many times the writer rebuilds a record whose place
// was taken by another write before it gives up; a test lowers it.
var bookAppendTries = placeappend.Tries

// bookAppendHook is a seam for tests: it runs after a record is built and
// before it is appended, where a write from elsewhere can land.
var bookAppendHook = func() {}

// repairOpening records the agreement a stopped run appended but did not
// record: the first record of the deal book, from its header.
func (s *Store) repairOpening(ctx context.Context, d *Deal, book *evidencebook.Book) error {
	first, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: 1})
	if err != nil {
		return err
	}
	if len(first) != 1 || len(first[0].Header.PayloadCommitments) != 1 {
		return conflictf("the deal book's first record has no record digest")
	}
	d.OwnAgreementID, d.OwnAgreementDigest = first[0].RecordID, first[0].Header.PayloadCommitments[0]
	if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_v1 SET own_agreement_id=?, own_agreement_digest=? WHERE deal_id=?`, d.OwnAgreementID, d.OwnAgreementDigest, d.DealID); err != nil {
		return err
	}
	_, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET state='agreed' WHERE deal_id=?`, d.DealID)
	return err
}

// repairIfStopped records an agreement a stopped run appended to d's book.
func (s *Store) repairIfStopped(ctx context.Context, d *Deal) error {
	if d.OwnAgreementID != "" || d.OwnLogID == "" {
		return nil
	}
	book, err := s.cfg.Shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return err
	}
	if book.Size() == 0 {
		return nil
	}
	return s.repairOpening(ctx, d, book)
}

// Repair records an agreement a stopped run appended to the deal's book but
// did not record, and returns the deal as it then stands; nil when the store
// holds no such deal. Every call that reads a deal's agreement runs it first.
func (s *Store) Repair(ctx context.Context, dealID string) (*Deal, error) {
	d, err := s.Deal(ctx, dealID)
	if err != nil || d == nil {
		return d, err
	}
	if d.OwnAgreementID != "" || d.OwnLogID == "" {
		return d, nil
	}
	if err := s.repairIfStopped(ctx, d); err != nil {
		return nil, err
	}
	return s.Deal(ctx, dealID)
}

// OwnBook is the holder's deal book of d as records, in order.
func (s *Store) OwnBook(ctx context.Context, d *Deal) ([]xdealv1.Record, error) {
	if d.OwnLogID == "" {
		return nil, nil
	}
	book, err := s.cfg.Shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return nil, err
	}
	if book.Size() == 0 {
		return nil, nil
	}
	all, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: book.Size()})
	if err != nil {
		return nil, err
	}
	var out []xdealv1.Record
	for _, r := range all {
		if len(r.Header.PayloadCommitments) != 1 {
			return nil, conflictf("a deal book record has no record digest")
		}
		payload, err := book.ResolvePayload(ctx, r.RecordID, r.Header.PayloadCommitments[0])
		if err != nil {
			return nil, err
		}
		rec, err := xdealv1.Decode(payload)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}
