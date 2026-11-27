// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

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
		return &IntentBody{}
	case TypeVerdict:
		return &VerdictBody{}
	case TypeApproval:
		return &ApprovalBody{}
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

// exactObject is the one rule for every object a verify path decodes into a
// Go type (evidencebook#559): a pair, a signed Capsule, a settlement
// member, a record header and the links in it. json.Unmarshal matches a
// member name in another case (U+017F for s and U+212A for k included), and
// a later spelling replaces an earlier one, so without this a signed object
// could carry a second spelling of a member that Go reads in place of the
// one an exact or schema-checking reader takes. Each object in raw, down
// t's struct, list and map types, must name its members exactly as t's
// fields do: a name that differs from a field's only by case folding is
// refused, and so is any other name unless open, for an object that carries
// members the profile does not read (a Capsule, a settlement member). A
// member given twice is the text check's (checkText) to refuse, over the
// pair that holds the object.
func exactObject(raw []byte, t reflect.Type, open bool, at string) error {
	doc, err := decodeJSON(raw)
	if err != nil {
		return err
	}
	return exactValue(doc, t, open, at)
}

func exactValue(v any, t reflect.Type, open bool, at string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return nil // a type error, which json.Unmarshal reports
		}
		fields := map[string]reflect.Type{}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.IsExported() && name != "-" && name != "" {
				fields[name] = f.Type
			}
		}
		for k, e := range m {
			if ft, ok := fields[k]; ok {
				if err := exactValue(e, ft, open, at+"."+k); err != nil {
					return err
				}
				continue
			}
			for name := range fields {
				if strings.EqualFold(k, name) {
					return fmt.Errorf("%s member %q is %q spelled otherwise", at, k, name)
				}
			}
			if !open {
				return fmt.Errorf("%s member %q is not one it has", at, k)
			}
		}
	case reflect.Slice:
		if l, ok := v.([]any); ok {
			for i, e := range l {
				if err := exactValue(e, t.Elem(), open, fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		if m, ok := v.(map[string]any); ok {
			for k, e := range m {
				if err := exactValue(e, t.Elem(), open, at+"."+k); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// capsuleWire is what the profile reads of a verified Capsule, which
// carries other members too (exactObject, open).
type capsuleWire struct {
	ActionID   string `json:"action_id"`
	ActionType string `json:"action_type"`
	References []any  `json:"references"`
	Model      struct {
		Compute struct {
			AgentInputDigest string `json:"agent_input_digest"`
		} `json:"compute_attestation"`
	} `json:"model_attestation"`
}

// capsuleRead is capsuleWire's members.
type capsuleRead struct {
	ActionID, ActionType, AgentInputDigest string
	References                             []any
}

func readCapsule(capsule []byte) (capsuleRead, error) {
	var w capsuleWire
	if err := exactObject(capsule, reflect.TypeFor[capsuleWire](), true, "the Capsule"); err != nil {
		return capsuleRead{}, err
	}
	if err := json.Unmarshal(capsule, &w); err != nil {
		return capsuleRead{}, err
	}
	return capsuleRead{ActionID: w.ActionID, ActionType: w.ActionType, AgentInputDigest: w.Model.Compute.AgentInputDigest,
		References: w.References}, nil
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
