// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"errors"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// DealJournal is a deal's journal records, in journal order: those carrying
// its deal id, and those its local steps bind (deal_v1_events), which carry
// none: an intent, a disclosure, a confirm_limits approval (PROFILE §3). It
// is the subset x-deal-v1's authorization reads for the deal's acts
// (Authorize), and the one a card over the same steps reads, so the two
// never read different records. A withheld or deleted payload is skipped;
// any other read error is returned.
func (s *Store) DealJournal(ctx context.Context, dealID string) ([]xdealv1.Record, error) {
	bound := map[string]bool{}
	rows, err := s.cfg.DB.QueryContext(ctx, `SELECT record_digest FROM deal_v1_events WHERE deal_id=? AND kind != 'open'`, dealID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		bound[d] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	all, err := s.journalV1(ctx)
	if err != nil {
		return nil, err
	}
	var out []xdealv1.Record
	for _, jr := range all {
		if jr.rec.Block.DealID == dealID || bound[jr.digest] {
			out = append(out, jr.rec)
		}
	}
	return out, nil
}

// journalRecordV1 is one x-deal-v1 record of the journal and its digest.
type journalRecordV1 struct {
	digest string
	rec    xdealv1.Record
}

// journalV1 reads the journal's x-deal-v1 records in order. A payload that
// is withheld or deleted, or that is no x-deal-v1 record, is skipped.
func (s *Store) journalV1(ctx context.Context) ([]journalRecordV1, error) {
	j, records, err := s.journalRecords(ctx)
	if err != nil {
		return nil, err
	}
	var out []journalRecordV1
	for _, r := range records {
		for _, c := range r.Header.PayloadCommitments {
			payload, err := j.ResolvePayload(ctx, r.RecordID, c)
			var gone *evidencebook.UnavailableError
			if errors.As(err, &gone) {
				continue
			}
			if err != nil {
				return nil, err
			}
			rec, err := xdealv1.Decode(payload)
			if err != nil {
				continue
			}
			out = append(out, journalRecordV1{digest: c, rec: rec})
		}
	}
	return out, nil
}
