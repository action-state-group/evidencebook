// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// Body is a v1 record's body: one of this package's body types, or v0's
// body type for an overlay type (§3.2).
type Body interface {
	// RecordType is the record type the body is the body of.
	RecordType() string
}

func (ProposeBody) RecordType() string   { return TypePropose }
func (AgreeBody) RecordType() string     { return TypeAgree }
func (PerformBody) RecordType() string   { return TypePerform }
func (SettleBody) RecordType() string    { return TypeSettle }
func (StatementBody) RecordType() string { return TypeStatement }
func (DisputeBody) RecordType() string   { return TypeDispute }
func (RulingBody) RecordType() string    { return TypeRuling }
func (AskBody) RecordType() string       { return TypeAsk }
func (AnswerBody) RecordType() string    { return TypeAnswer }
func (AdmitBody) RecordType() string     { return TypeAdmit }
func (CheckBody) RecordType() string     { return TypeCheck }

// newBody is the zero body of a v1 record type, nil for no v1 record type.
func newBody(recordType string) Body {
	switch recordType {
	case TypePropose:
		return &ProposeBody{}
	case TypeAgree:
		return &AgreeBody{}
	case TypePerform:
		return &PerformBody{}
	case TypeSettle:
		return &SettleBody{}
	case TypeStatement:
		return &StatementBody{}
	case TypeDispute:
		return &DisputeBody{}
	case TypeRuling:
		return &RulingBody{}
	case TypeAsk:
		return &AskBody{}
	case TypeAnswer:
		return &AnswerBody{}
	case TypeAdmit:
		return &AdmitBody{}
	case TypeCheck:
		return &CheckBody{}
	case TypeIntent:
		return &dealprofile.IntentBody{}
	case TypeVerdict:
		return &dealprofile.VerdictBody{}
	case TypeApproval:
		return &dealprofile.ApprovalBody{}
	case TypeRulesVerdict:
		return &dealprofile.RulesVerdictBody{}
	case TypeMessage:
		return &dealprofile.MessageBody{}
	case TypeClaim:
		return &dealprofile.Claim{}
	case TypeEvidence:
		return &dealprofile.EvidenceBody{}
	case TypeDetailChange:
		return &dealprofile.DetailChangeBody{}
	case TypeAgentMove:
		return &dealprofile.AgentMoveBody{}
	case TypeDisclosure:
		return &dealprofile.DisclosureBody{}
	case TypeSale:
		return &dealprofile.SaleBody{}
	case TypeThread:
		return &dealprofile.ThreadBody{}
	case TypeThreadOpened:
		return &dealprofile.ThreadOpenedBody{}
	case TypeSaleUnpost:
		return &dealprofile.SaleUnpostBody{}
	case TypeCounterpartyProfile:
		return &dealprofile.CounterpartyProfileBody{}
	}
	return nil
}

// Record is a v1 record: the x-deal-v1 block and its body. Body may hold a
// body type or a pointer to one; its RecordType must be the block's.
type Record struct {
	Block Block
	Body  Body
}

// MarshalJSON is the record's JSON: {"x-deal-v1": block, "body": body}.
func (r Record) MarshalJSON() ([]byte, error) {
	if r.Body == nil || reflect.ValueOf(r.Body).Kind() == reflect.Pointer && reflect.ValueOf(r.Body).IsNil() {
		return nil, fmt.Errorf("xdealv1: a %s record has no body", r.Block.RecordType)
	}
	if t := r.Body.RecordType(); t != r.Block.RecordType || !slices.Contains(recordTypes, t) {
		return nil, fmt.Errorf("xdealv1: %s body on a %s record", t, r.Block.RecordType)
	}
	return json.Marshal(struct {
		Block Block `json:"x-deal-v1"`
		Body  Body  `json:"body"`
	}{r.Block, r.Body})
}

// UnmarshalJSON reads a v1 record, as Decode does.
func (r *Record) UnmarshalJSON(data []byte) error {
	if err := checkText(data); err != nil {
		return err
	}
	var raw struct {
		Block json.RawMessage `json:"x-deal-v1"`
		Body  json.RawMessage `json:"body"`
	}
	if err := strictUnmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Block == nil || raw.Body == nil {
		return fmt.Errorf("xdealv1: a record has an x-deal-v1 block and a body")
	}
	var blk Block
	if err := strictUnmarshal(raw.Block, &blk); err != nil {
		return fmt.Errorf("xdealv1: the x-deal-v1 block: %w", err)
	}
	body := newBody(blk.RecordType)
	if body == nil {
		return fmt.Errorf("xdealv1: record type %q is not an x-deal-v1 type", blk.RecordType)
	}
	if err := strictUnmarshal(raw.Body, body); err != nil {
		return fmt.Errorf("xdealv1: a %s body: %w", blk.RecordType, err)
	}
	rec := Record{Block: blk, Body: body}
	if err := sameRecord(data, rec); err != nil {
		return err
	}
	*r = rec
	return nil
}

// Decode reads a v1 record's JSON text. It refuses a member the types do
// not name, and text the types would not encode back to (a member name in
// another case, a null where a value is held), which encoding/json reads
// without error. It runs no schema or record check: Check does.
func Decode(data []byte) (Record, error) {
	var r Record
	if err := r.UnmarshalJSON(data); err != nil {
		return Record{}, err
	}
	return r, nil
}

// sameRecord refuses v unless it encodes to the JCS bytes of data.
func sameRecord(data []byte, v any) error {
	doc, err := decodeJSON(data)
	if err != nil {
		return err
	}
	want, err := canonical.JCS(doc)
	if err != nil {
		return err
	}
	enc, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if doc, err = decodeJSON(enc); err != nil {
		return err
	}
	got, err := canonical.JCS(doc)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("xdealv1: the record's text does not read as its type: it would encode to other bytes")
	}
	return nil
}

func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("xdealv1: trailing data after the JSON value")
	}
	return nil
}
