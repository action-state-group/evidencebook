// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// Send states: a row is stored as sealing before SealSent is called, sealed
// once the pair is stored, and written once the caller has delivered it
// (MarkWritten).
const (
	sentSealing = "sealing"
	sentSealed  = "sealed"
	sentWritten = "written"
)

// Sent is a journal record sent whole: an offer, a cancellation or a
// decline.
type Sent struct {
	DealID string
	// Digest is the record digest the other side and later calls name it by.
	Digest  string
	Payload []byte
	// Pair is the standalone Capsule that carries the record, the bytes to
	// deliver to the other side.
	Pair []byte
	// Request is the caller's request key the record was sealed under, which
	// MarkWritten takes.
	Request string
	// Resumed is set when the record is one an earlier run of the same
	// request sealed or began, returned unchanged.
	Resumed bool
}

// sentOp is a row of deal_sent.
type sentOp struct {
	recordDigest, requestDigest, dealID, kind, state string
	payload, pair                                    []byte
}

// findSentOp is the operation a request left behind, if any: it is looked
// up by the caller's request key, so a re-run needs no digest.
func (s *Store) findSentOp(ctx context.Context, request string) (*sentOp, error) {
	var op sentOp
	err := s.cfg.DB.QueryRowContext(ctx,
		`SELECT record_digest, request_digest, deal_id, kind, payload, state, pair FROM deal_sent WHERE request_digest=? ORDER BY rowid DESC LIMIT 1`,
		request).Scan(&op.recordDigest, &op.requestDigest, &op.dealID, &op.kind, &op.payload, &op.state, &op.pair)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// sealSent is the shelf's SealSent; a test replaces it to make the call fail
// the ways the shelf says it can.
var sealSent = func(ctx context.Context, shelf *evidencebook.Shelf, e evidencebook.Entry, actionID string) (evidencebook.Record, evidencebook.SentCapsule, error) {
	return shelf.SealSent(ctx, e, actionID)
}

// offerTermsLeg is the terms leg an offer's sent Capsule carries as its
// settlement member: the offer's, when its terms have exactly one payment
// row, and none otherwise (the shelf refuses the member on an offer with no
// payment row or several).
func offerTermsLeg(payload []byte) (json.RawMessage, error) {
	rec, err := xdealv1.Decode(payload)
	if err != nil {
		return nil, err
	}
	prop, ok := proposeOf(rec)
	if !ok || len(xdealv1.PaymentRows(prop.TermsRows)) != 1 {
		return nil, nil
	}
	return xdealv1.TermsLeg(rec)
}

// send seals a journal record that leaves the shelf whole and returns its
// pair. A re-run of the same request finds the operation row and never seals
// a record twice: a pair already stored is returned again; a row still
// sealing with no pair is sealed once more only when the journal holds no
// record of that digest.
func (s *Store) send(ctx context.Context, request, dealID, kind string, payload []byte, digest string) (pair []byte, err error) {
	if _, err = s.cfg.Shelf.Journal(ctx); err != nil {
		return nil, err
	}
	op, err := s.findSentOp(ctx, request)
	if err != nil {
		return nil, err
	}
	if op != nil && op.state != sentWritten {
		payload, digest, dealID = op.payload, op.recordDigest, op.dealID
	} else {
		op = nil
	}
	if op != nil && op.pair != nil {
		return op.pair, nil
	}
	if op == nil {
		if _, err = s.cfg.DB.ExecContext(ctx,
			`INSERT INTO deal_sent (record_digest, request_digest, deal_id, kind, payload, state) VALUES (?, ?, ?, ?, ?, ?)`,
			digest, request, dealID, kind, payload, sentSealing); err != nil {
			return nil, err
		}
	} else {
		held, err := s.journalHolds(ctx, digest)
		if err != nil {
			return nil, err
		}
		if held {
			return nil, codedConflict(CodeOfferNotRebuilt, "the record "+digest+" was sealed but its file was not made, and the library cannot rebuild it: for an offer, cancel it with Store.CancelOffer", digest)
		}
	}
	entry := evidencebook.Entry{
		RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload},
	}
	if kind == xdealv1.TypePropose {
		leg, lerr := offerTermsLeg(payload)
		if lerr != nil {
			return nil, lerr
		}
		if leg != nil {
			entry.Members = map[string]json.RawMessage{xdealv1.SettlementMember: leg}
		}
	}
	rec, sent, err := sealSent(ctx, s.cfg.Shelf, entry, dealID+xdealv1.SentSuffix)
	if err != nil && rec.RecordID == "" {
		return nil, err
	}
	// A record with an error is stored and its Capsule comes with it: the
	// pair is made from that Capsule and the record is not sealed again.
	pair, perr := xdealv1.Pair(sent.Capsule, sent.Envelope, sent.Payload)
	if perr != nil {
		return nil, errors.Join(err, perr)
	}
	if _, serr := s.cfg.DB.ExecContext(ctx, `UPDATE deal_sent SET pair=?, state=? WHERE record_digest=? AND request_digest=?`, pair, sentSealed, digest, request); serr != nil {
		return nil, errors.Join(err, serr)
	}
	return pair, nil
}

// MarkWritten records that a sent record was delivered. Until then a call
// with the same request key returns the same record; after it, the same
// request makes a new offer, and a cancellation or a decline is refused as
// already made.
func (s *Store) MarkWritten(ctx context.Context, sent Sent) error {
	_, err := s.cfg.DB.ExecContext(ctx, `UPDATE deal_sent SET state=?, written=1 WHERE record_digest=? AND request_digest=?`, sentWritten, sent.Digest, sent.Request)
	return err
}

// journalRecords reads the whole journal in order, across its windows on a
// windowed store, with the reader that resolves its payloads. Opening the
// journal recovers a record a crash left stored but not yet in its log.
func (s *Store) journalRecords(ctx context.Context) (evidencebook.JournalReader, []evidencebook.Record, error) {
	j, err := s.cfg.Shelf.JournalView(ctx)
	if err != nil {
		return nil, nil, err
	}
	records, err := j.Query(ctx, evidencebook.Filter{})
	return j, records, err
}

// journalHolds reports whether the journal holds a record whose payload
// commitment is the record's digest.
func (s *Store) journalHolds(ctx context.Context, digest string) (bool, error) {
	_, records, err := s.journalRecords(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range records {
		for _, c := range r.Header.PayloadCommitments {
			if c == digest {
				return true, nil
			}
		}
	}
	return false, nil
}

// journalRecord is the x-deal-v1 record the journal holds under payload
// digest digest (nil when the journal's record there is not one), and
// whether the journal holds one.
func (s *Store) journalRecord(ctx context.Context, digest string) (rec *xdealv1.Record, held bool, err error) {
	j, records, err := s.journalRecords(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, r := range records {
		for _, c := range r.Header.PayloadCommitments {
			if c != digest {
				continue
			}
			payload, err := j.ResolvePayload(ctx, r.RecordID, digest)
			if err != nil {
				return nil, false, err
			}
			r, err := xdealv1.Decode(payload)
			if err != nil {
				return nil, true, nil
			}
			return &r, true, nil
		}
	}
	return nil, false, nil
}
