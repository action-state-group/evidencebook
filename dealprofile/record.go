// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
)

// Body is a step record's body, one type per record type (PROFILE.md §3).
// The set is closed: only this package's body types implement it.
type Body interface {
	// RecordType is the record type the body is the body of.
	RecordType() string
	isBody()
}

func (IntentBody) RecordType() string              { return TypeIntent }
func (BaselineBody) RecordType() string            { return TypeBaseline }
func (MessageBody) RecordType() string             { return TypeMessage }
func (Claim) RecordType() string                   { return TypeClaim }
func (EvidenceBody) RecordType() string            { return TypeEvidence }
func (DetailChangeBody) RecordType() string        { return TypeDetailChange }
func (CheckBody) RecordType() string               { return TypeCheck }
func (VerdictBody) RecordType() string             { return TypeVerdict }
func (ApprovalBody) RecordType() string            { return TypeApproval }
func (ActionBody) RecordType() string              { return TypeAction }
func (OutcomeBody) RecordType() string             { return TypeOutcome }
func (CloseBody) RecordType() string               { return TypeClose }
func (DisclosureBody) RecordType() string          { return TypeDisclosure }
func (SaleBody) RecordType() string                { return TypeSale }
func (CounterpartyProfileBody) RecordType() string { return TypeCounterpartyProfile }
func (ThreadBody) RecordType() string              { return TypeThread }
func (ThreadOpenedBody) RecordType() string        { return TypeThreadOpened }
func (SaleCutBody) RecordType() string             { return TypeSaleCut }
func (SaleCloseBody) RecordType() string           { return TypeSaleClose }
func (AgentMoveBody) RecordType() string           { return TypeAgentMove }

func (IntentBody) isBody()              {}
func (BaselineBody) isBody()            {}
func (MessageBody) isBody()             {}
func (Claim) isBody()                   {}
func (EvidenceBody) isBody()            {}
func (DetailChangeBody) isBody()        {}
func (CheckBody) isBody()               {}
func (VerdictBody) isBody()             {}
func (ApprovalBody) isBody()            {}
func (ActionBody) isBody()              {}
func (OutcomeBody) isBody()             {}
func (CloseBody) isBody()               {}
func (DisclosureBody) isBody()          {}
func (SaleBody) isBody()                {}
func (CounterpartyProfileBody) isBody() {}
func (ThreadBody) isBody()              {}
func (ThreadOpenedBody) isBody()        {}
func (SaleCutBody) isBody()             {}
func (SaleCloseBody) isBody()           {}
func (AgentMoveBody) isBody()           {}

// newBody is the zero body of a record type, nil for no record type.
func newBody(recordType string) Body {
	switch recordType {
	case TypeIntent:
		return &IntentBody{}
	case TypeBaseline:
		return &BaselineBody{}
	case TypeMessage:
		return &MessageBody{}
	case TypeClaim:
		return &Claim{}
	case TypeEvidence:
		return &EvidenceBody{}
	case TypeDetailChange:
		return &DetailChangeBody{}
	case TypeCheck:
		return &CheckBody{}
	case TypeVerdict:
		return &VerdictBody{}
	case TypeApproval:
		return &ApprovalBody{}
	case TypeAction:
		return &ActionBody{}
	case TypeOutcome:
		return &OutcomeBody{}
	case TypeClose:
		return &CloseBody{}
	case TypeDisclosure:
		return &DisclosureBody{}
	case TypeSale:
		return &SaleBody{}
	case TypeCounterpartyProfile:
		return &CounterpartyProfileBody{}
	case TypeThread:
		return &ThreadBody{}
	case TypeThreadOpened:
		return &ThreadOpenedBody{}
	case TypeSaleCut:
		return &SaleCutBody{}
	case TypeSaleClose:
		return &SaleCloseBody{}
	case TypeAgentMove:
		return &AgentMoveBody{}
	}
	return nil
}

// Record is a step record: the x-deal-v0 block and its body. Encode takes a
// Record (or a *Record) as it takes the record's JSON text. Body may hold a
// body type or a pointer to one; its RecordType must be the block's.
type Record struct {
	Block Block
	Body  Body
}

// MarshalJSON is the record's JSON: {"x-deal-v0": block, "body": body}.
func (r Record) MarshalJSON() ([]byte, error) {
	if r.Body == nil || reflect.ValueOf(r.Body).Kind() == reflect.Pointer && reflect.ValueOf(r.Body).IsNil() {
		return nil, fmt.Errorf("dealprofile: a %s record has no body", r.Block.RecordType)
	}
	if t := r.Body.RecordType(); t != r.Block.RecordType {
		return nil, fmt.Errorf("dealprofile: %s body on a %s record", t, r.Block.RecordType)
	}
	return json.Marshal(struct {
		Block Block `json:"x-deal-v0"`
		Body  Body  `json:"body"`
	}{r.Block, r.Body})
}

// UnmarshalJSON reads a step record, as Decode does.
func (r *Record) UnmarshalJSON(data []byte) error {
	var raw struct {
		Block json.RawMessage `json:"x-deal-v0"`
		Body  json.RawMessage `json:"body"`
	}
	if err := strictUnmarshal(data, &raw); err != nil {
		return err
	}
	var blk Block
	if err := strictUnmarshal(raw.Block, &blk); err != nil {
		return fmt.Errorf("dealprofile: the x-deal-v0 block: %w", err)
	}
	body := newBody(blk.RecordType)
	if body == nil {
		return fmt.Errorf("dealprofile: record type %q has no body type", blk.RecordType)
	}
	if err := strictUnmarshal(raw.Body, body); err != nil {
		return fmt.Errorf("dealprofile: a %s body: %w", blk.RecordType, err)
	}
	rec := Record{Block: blk, Body: body}
	if err := sameRecord(data, rec); err != nil {
		return err
	}
	*r = rec
	return nil
}

// Decode reads a record's JSON text into v: a *Record, or a pointer to one
// of the typed record or contract document types. It refuses a member v's
// type does not name, and text that v would not encode back to, such as a
// member name in another case or a null where the type holds a value, which
// encoding/json reads without error. A record's digest is still that of its
// sealed bytes (Digest, CheckDigest); Decode only says v holds the same
// record.
func Decode(data []byte, v any) error {
	if err := strictUnmarshal(data, v); err != nil {
		return err
	}
	if _, ok := v.(*Record); ok {
		return nil // UnmarshalJSON has checked it
	}
	return sameRecord(data, v)
}

// sameRecord refuses v unless it encodes to the JCS bytes of data.
func sameRecord(data []byte, v any) error {
	doc, err := decode(data)
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
	if doc, err = decode(enc); err != nil {
		return err
	}
	got, err := canonical.JCS(doc)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("dealprofile: the record's text does not read as this type: it would encode to other bytes")
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
		return fmt.Errorf("dealprofile: trailing data after the JSON value")
	}
	return nil
}
