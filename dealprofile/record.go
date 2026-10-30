// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	if r.Body == nil {
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

// UnmarshalJSON reads a step record, refusing a member no type here names
// (which the schema refuses too) and a record type with no body type.
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
	r.Block, r.Body = blk, body
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
