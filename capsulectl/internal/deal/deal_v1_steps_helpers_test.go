// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"encoding/json"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// preparedV1 is a step built and encoded as package deal's writer would
// store it, for a test that stores it by hand to stand for a run that
// stopped (the writer itself is package deal's since evidencebook#899 phase
// 3).
type preparedV1 struct {
	se      sealedEvent
	local   []byte
	payload []byte
}

// prepareV1 builds ev as the next step after events, with its record
// encoded as Store.SealJournal encodes it (deal_id where PROFILE §3 puts it,
// the nonce, the step's time), storing nothing.
func (s *dealSession) prepareV1(dealID string, events []sealedEvent, ev dealEvent, opening bool) (preparedV1, error) {
	f := dealapi.StepFacts{N: int64(len(events)) + 1, At: cli.DealClock().UTC().Truncate(time.Second).Format(time.RFC3339)}
	if len(events) > 0 {
		f.Prev = events[len(events)-1].Digest
	}
	p := s.v1Step(dealID, events, ev, opening)
	parts, err := p.step.Build(f)
	if err != nil {
		return preparedV1{}, err
	}
	out := preparedV1{local: parts.Local}
	digest := digestBytesHex(parts.Local)
	if r := parts.Record; r != nil {
		b := map[string]any{"profile": xdealv1.Profile, "canonicalization": xdealv1.Canonicalization, "record_type": r.RecordType, "at": f.At}
		for k, v := range r.Block {
			b[k] = v
		}
		switch {
		case r.RecordType == "intent", r.RecordType == "disclosure", r.RecordType == "counterparty_profile":
		case r.RecordType == "approval" && r.Body["choice"] == "confirm_limits":
		default:
			b["deal_id"] = dealID
		}
		body := map[string]any{}
		for k, v := range r.Body {
			body[k] = v
		}
		if r.Nonce != "" {
			body["nonce"] = r.Nonce
		}
		raw, err := json.Marshal(map[string]any{xdealv1.Profile: b, "body": body})
		if err != nil {
			return preparedV1{}, err
		}
		if out.payload, digest, err = xdealv1.Encode(raw); err != nil {
			return preparedV1{}, err
		}
	}
	out.se = sealedEvent{CapsuleID: v1EventID(dealID, p.ev.N), Digest: digest, Sequence: uint64(p.ev.N), Event: *p.ev}
	return out, nil
}

// appendV1Journal appends one journal record, as a run outside the plugin's
// verbs would.
func (s *dealSession) appendV1Journal(ctx context.Context, payload []byte) error {
	j, err := s.shelf.Journal(ctx)
	if err != nil {
		return err
	}
	_, err = j.Append(ctx, evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}})
	return err
}

// checkRecord is the deal's journal check of digest (Store.CheckRecord).
func (s *dealSession) checkRecord(ctx context.Context, dealID, digest string) (*xdealv1.CheckBody, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	cb, err := st.CheckRecord(ctx, dealID, digest)
	return cb, libErr(err)
}
