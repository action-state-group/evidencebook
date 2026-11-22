// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// useV1Check readies a session for `deal check` on a v1 deal: the deal key and
// the local events of an opened deal (xdealv1-write-path §12 items 5-6).
func (s *dealSession) useV1Check(ctx context.Context, dealID string) ([]sealedEvent, error) {
	if _, err := s.requireMigrated(ctx); err != nil {
		return nil, err
	}
	if err := s.ensureV1Tables(ctx); err != nil {
		return nil, err
	}
	s.dkey = dealprofile.DealKey(s.secret, dealID)
	s.v1 = true
	events, err := s.v1Events(ctx, dealID)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, cli.InputError("deal " + dealID + " has no intent yet: seal the user's intent first, with `deal note --deal " + dealID + " --kind intent --input FILE --opening FILE`")
	}
	if err := s.v1JournalMatchesEvents(ctx, dealID, events); err != nil {
		return nil, err
	}
	return events, nil
}

// v1PayCheckRef refuses a v1 pay check without its payment reference.
func v1PayCheckRef(snap dealSnapshot) error {
	if snap.Action == "pay" && snap.PaymentRef == "" {
		return cli.InputError("a pay check on an x-deal-v1 deal records payment_ref, the reference fixed before paying that the payment will be made under (a Lightning payment hash, an ACP order id, the x402 authorization nonce): the payer leg cites the check that recorded it")
	}
	return nil
}

// v1CapsuleWithInput is the record the rules checker reads for a v1 deal's
// checked step: the v0 Capsule a v0 deal would have sealed for it, rebuilt from
// the local event, with its v0 record as agent_input, the form capsuleWithInput
// gives a v0 deal. It is never stored and never leaves the process: only the
// pinned checker reads it (xdealv1-write-path §12 item 6).
func (s *dealSession) v1CapsuleWithInput(capsuleID string, events []sealedEvent) (map[string]interface{}, error) {
	at := -1
	for i, se := range events {
		if se.CapsuleID == capsuleID {
			at = i
		}
	}
	if at < 0 {
		return nil, nil
	}
	request, _, err := s.stepRequest(events[:at], events[at].Event, dealCapsuleCurrent)
	if err != nil {
		return nil, err
	}
	record, err := cli.Seal(request, s.key)
	if err != nil {
		return nil, err
	}
	var capsule map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(record.Capsule))
	dec.UseNumber()
	if err := dec.Decode(&capsule); err != nil {
		return nil, err
	}
	if len(record.ProducerEnvelope) != 0 {
		capsule["signature"] = hex.EncodeToString(record.ProducerEnvelope)
	}
	var input interface{}
	dec = json.NewDecoder(bytes.NewReader(request.Payload))
	dec.UseNumber()
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	capsule["agent_input"] = input
	return capsule, nil
}

// v1DealJournal is a deal's journal records: those carrying its deal id, and
// those bound to it by its local event list (an intent, a disclosure, a
// confirm_limits approval, which carry none), in journal order. `deal check`
// folds the matching local events and B2's acts pass exactly these records to
// xdealv1.CheckAuthorizations, so the card and the authorization read one
// subset (xdealv1-write-path §12 item 4).
func (s *dealSession) v1DealJournal(ctx context.Context, dealID string) ([]xdealv1.Record, error) {
	bound := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT record_digest FROM deal_v1_events WHERE deal_id=? AND kind != 'open'`, dealID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return nil, err
		}
		bound[d] = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	j, err := s.shelf.Journal(ctx)
	if err != nil {
		return nil, err
	}
	read, err := s.shelf.Records(ctx, evidencebook.JournalLogID)
	if err != nil {
		return nil, err
	}
	var out []xdealv1.Record
	for _, r := range read.Records {
		for _, c := range r.Header.PayloadCommitments {
			payload, err := j.ResolvePayload(ctx, r.RecordID, c)
			if err != nil {
				continue
			}
			rec, err := xdealv1.Decode(payload)
			if err != nil {
				continue
			}
			if rec.Block.DealID == dealID || bound[c] {
				out = append(out, rec)
			}
		}
	}
	return out, nil
}

// v1JournalMatchesEvents refuses a check when the deal's journal holds a record
// of a kind this slice's verbs write (the journal overlay and the journey
// overlay) with no local event: such a record reaches the authorization but
// not the card. The deal's other records (the offer, the agreement, the
// admission decision, records sent whole) live in their own tables.
func (s *dealSession) v1JournalMatchesEvents(ctx context.Context, dealID string, events []sealedEvent) error {
	local := map[string]bool{}
	for _, se := range events {
		local[se.Digest] = true
	}
	j, err := s.shelf.Journal(ctx)
	if err != nil {
		return err
	}
	read, err := s.shelf.Records(ctx, evidencebook.JournalLogID)
	if err != nil {
		return err
	}
	for _, r := range read.Records {
		for _, c := range r.Header.PayloadCommitments {
			payload, err := j.ResolvePayload(ctx, r.RecordID, c)
			if err != nil {
				continue
			}
			rec, err := xdealv1.Decode(payload)
			if err != nil || rec.Block.DealID != dealID {
				continue
			}
			if v1JournalTypes[rec.Block.RecordType] && rec.Block.RecordType != "counterparty_profile" && !local[c] {
				return fmt.Errorf("%w: deal %s's journal holds a %s record (%s) this profile's deal list does not: it was sealed outside these verbs, so the card cannot read it", cli.ErrConflict, dealID, rec.Block.RecordType, c)
			}
		}
	}
	return nil
}
