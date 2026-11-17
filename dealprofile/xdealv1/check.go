// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/evidencebook/dealprofile"
	xdealv1files "github.com/action-state-group/evidencebook/dealprofile/x-deal-v1"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// The stages a v1 record is checked at, in order.
const (
	StageCanonicalization = dealprofile.StageCanonicalization
	StageSchema           = dealprofile.StageSchema
	// StageRecord is the record checks of design §3: where a record is
	// sealed, deal_id presence, and the rules within one record that the
	// schema does not state.
	StageRecord = "record"
	StageDigest = dealprofile.StageDigest
	// StageAgreement is an agreement read against the offer it cites
	// (CheckAgreement).
	StageAgreement = "agreement"
)

// StageError is a record refused at one stage, as dealprofile's.
type StageError = dealprofile.StageError

func stageErr(stage, format string, args ...any) *StageError {
	return &StageError{Stage: stage, Detail: fmt.Sprintf(format, args...)}
}

// Encode is a record's payload, the JCS bytes a record seals, and its
// record digest, once the record passes Check. record is the record's JSON
// text ([]byte or json.RawMessage, read as written) or any other value,
// which is marshaled with encoding/json first.
func Encode(record any) (payload []byte, digest string, err error) {
	doc, raw, err := decode(record)
	if err != nil {
		return nil, "", err
	}
	if err := check(doc, raw); err != nil {
		return nil, "", err
	}
	if payload, err = canonical.JCS(doc); err != nil {
		return nil, "", err
	}
	digest, err = canonical.JSONDigest(doc)
	return payload, digest, err
}

// Check runs the stages over record (canonicalization, schema, record) and
// returns the first refusal as a *StageError.
func Check(record any) error {
	doc, raw, err := decode(record)
	if err != nil {
		return err
	}
	return check(doc, raw)
}

// Digest is a record's record digest: lowercase hex SHA-256 over its JCS
// bytes, with no normalization pass (§3.1, as v0's).
func Digest(record any) (string, error) {
	doc, _, err := decode(record)
	if err != nil {
		return "", err
	}
	return canonical.JSONDigest(doc)
}

// RowsDigest is the rows digest of an offer's terms_rows: SHA-256 over the
// JCS bytes of rows and named together (§4.2).
func RowsDigest(rows TermsRows) (string, error) {
	doc, _, err := decode(rows)
	if err != nil {
		return "", err
	}
	return canonical.JSONDigest(doc)
}

// RefTo is the digest reference to a record with digest d.
func RefTo(d string) Ref { return Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: d} }

func decode(record any) (any, []byte, error) {
	var raw []byte
	switch r := record.(type) {
	case []byte:
		raw = r
	case json.RawMessage:
		raw = r
	default:
		var err error
		if raw, err = json.Marshal(record); err != nil {
			return nil, nil, err
		}
	}
	doc, err := decodeJSON(raw)
	if err != nil {
		return nil, nil, stageErr(StageCanonicalization, "not one JSON value: %v", err)
	}
	return doc, raw, nil
}

// decodeJSON reads one JSON value with numbers kept as written.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("trailing data after the JSON value")
	}
	return v, nil
}

func check(doc any, raw []byte) error {
	rec, _ := doc.(map[string]any)
	blk, _ := rec[Profile].(map[string]any)
	if blk == nil {
		return stageErr(StageCanonicalization, "no x-deal-v1 block")
	}
	if c := blk["canonicalization"]; c != Canonicalization {
		return stageErr(StageCanonicalization, `canonicalization must be exactly "jcs" (got %v); fail closed`, c)
	}
	if err := noFloats(doc, "$"); err != nil {
		return err
	}
	if _, err := canonical.JCS(doc); err != nil {
		return stageErr(StageCanonicalization, "not JCS-serializable: %v", err)
	}
	if err := stageSchema(doc); err != nil {
		return err
	}
	r, err := Decode(raw)
	if err != nil {
		return stageErr(StageSchema, "%v", err)
	}
	return checkRecord(r)
}

func noFloats(v any, path string) error {
	switch x := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return stageErr(StageCanonicalization, "a float at %s; numbers are integers", path)
		}
	case map[string]any:
		for k, e := range x {
			if err := noFloats(e, path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range x {
			if err := noFloats(e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Where records are sealed (§3.2).
var (
	// dealBookTypes are sealed in a deal book only.
	dealBookTypes = []string{TypePerform, TypeSettle, TypeStatement, TypeDispute, TypeRulesVerdict}
	// journalTypes are sealed in the journal only.
	journalTypes = []string{TypePropose, TypeAdmit, TypeIntent, TypeCheck, TypeVerdict, TypeApproval,
		TypeMessage, TypeClaim, TypeEvidence, TypeDetailChange, TypeAgentMove, TypeDisclosure, TypeSale,
		TypeThread, TypeThreadOpened, TypeSaleUnpost, TypeCounterpartyProfile}
	// noDealID are the journal records that predate or span deals: deal_id
	// absent (§3.1).
	noDealID = []string{TypeIntent, TypeAdmit, TypeSale, TypeThread, TypeThreadOpened, TypeSaleUnpost,
		TypeCounterpartyProfile, TypeDisclosure}
	// eitherDealID are the journal records whose deal_id depends on when
	// they were sealed: a journey record carries it once its offer exists,
	// and an ask or answer when it is between the two parties rather than
	// of an admission. The record alone cannot tell, so the record checks
	// take either.
	eitherDealID = []string{TypeMessage, TypeClaim, TypeEvidence, TypeDetailChange, TypeAgentMove, TypeAsk, TypeAnswer}
)

// Placement is where a record is sealed: in a deal book, a case book, or
// the journal.
type Placement string

const (
	PlacementDealBook Placement = "deal_book"
	PlacementCaseBook Placement = "case_book"
	PlacementJournal  Placement = "journal"
)

// PlacementOf is where a record of this type and block is sealed (§3.2):
// an agreement, perform, settle, statement, dispute and rules_verdict in a
// deal book; a ruling in a case book; an ask or answer in a deal book when
// it has a seq, else in the journal; every other record in the journal.
func PlacementOf(r Record) Placement {
	t := r.Block.RecordType
	switch {
	case t == TypeRuling:
		return PlacementCaseBook
	case slices.Contains(dealBookTypes, t):
		return PlacementDealBook
	case t == TypeAgree:
		if b, ok := asAgree(r.Body); ok && b.Kind == KindAgreement {
			return PlacementDealBook
		}
		return PlacementJournal
	case (t == TypeAsk || t == TypeAnswer) && r.Block.Seq > 0:
		return PlacementDealBook
	}
	return PlacementJournal
}

func asAgree(b Body) (AgreeBody, bool) {
	switch x := b.(type) {
	case AgreeBody:
		return x, true
	case *AgreeBody:
		return *x, x != nil
	}
	return AgreeBody{}, false
}

func checkRecord(r Record) error {
	bad := func(format string, args ...any) error { return stageErr(StageRecord, format, args...) }
	b := r.Block
	t := b.RecordType
	place := PlacementOf(r)
	switch place {
	case PlacementDealBook, PlacementCaseBook:
		if b.Seq < 1 {
			return bad("a %s record in a %s carries its seq", t, place)
		}
		if (b.Prev != nil) != (b.Seq > 1) {
			return bad("a book record carries prev exactly when its seq is past 1")
		}
		if place == PlacementCaseBook {
			if b.Seen.Present || b.AgreementRef != nil {
				return bad("a ruling carries no seen and no agreement_ref")
			}
			break
		}
		if !b.Seen.Present {
			return bad("a deal-book record carries seen (null when nothing is held from the other side)")
		}
		if (b.AgreementRef != nil) != (b.Seq > 1) {
			return bad("a deal-book record carries agreement_ref exactly when its seq is past 1")
		}
		if ag, ok := asAgree(r.Body); ok && ag.Kind == KindAgreement && b.Seq != 1 {
			return bad("an agreement opens its deal book: seq 1")
		}
		if t != TypeAgree && b.Seq == 1 {
			return bad("a deal book opens with its agreement, not a %s", t)
		}
	case PlacementJournal:
		if b.Seq != 0 || b.Prev != nil || b.AgreementRef != nil || b.Seen.Present {
			return bad("a journal record carries no seq, prev, agreement_ref or seen, so a record sent whole shows no place in the journal")
		}
	}
	if err := checkDealID(r, place); err != nil {
		return err
	}
	return checkBody(r)
}

// checkDealID applies §3.1's deal_id presence: required on every book
// record, on propose and every agree, on check and verdict, and on an
// approval that answers a verdict; absent on the journal records that
// predate or span deals and on a confirm_limits approval.
func checkDealID(r Record, place Placement) error {
	t, has := r.Block.RecordType, r.Block.DealID != ""
	required, absent := false, false
	switch {
	case place != PlacementJournal:
		required = true
	case slices.Contains(eitherDealID, t):
	case slices.Contains(noDealID, t):
		absent = true
	case t == TypeApproval:
		a, _ := r.Body.(*dealprofile.ApprovalBody)
		if a != nil && a.Choice == ChoiceConfirm {
			absent = true
		} else {
			required = true
		}
	default:
		// propose, agree (decline, cancellation), check, verdict.
		required = true
	}
	switch {
	case required && !has:
		return stageErr(StageRecord, "a %s record carries deal_id", t)
	case absent && has:
		return stageErr(StageRecord, "a %s record carries no deal_id: it predates or spans deals", t)
	}
	return nil
}

func checkBody(r Record) error {
	bad := func(format string, args ...any) error { return stageErr(StageRecord, format, args...) }
	switch body := r.Body.(type) {
	case *ProposeBody:
		return checkRows(body.TermsRows)
	case *AgreeBody:
		if body.Kind == KindAgreement {
			layer := body.CounterpartyLayer != nil && *body.CounterpartyLayer
			if !layer && (!r.Block.Seen.IsNull() || !body.CounterpartyKey.IsNull()) {
				return bad("an agreement with a counterparty that runs no layer has seen null and counterparty_key null")
			}
			if body.Side == SideAcceptor && !r.Block.Seen.IsNull() {
				return bad("the acceptor's agreement comes first: its seen is null")
			}
		}
	case *CheckBody:
		// v0's body_check is closed, so the body without payment_ref is
		// checked against it (x-deal-v1.schema.json, body_check).
		v0, err := json.Marshal(body.CheckBody)
		if err != nil {
			return err
		}
		doc, err := decodeJSON(v0)
		if err != nil {
			return err
		}
		s, err := dealprofile.ProfileSchemaAt("#/$defs/body_check")
		if err != nil {
			return err
		}
		if err := s.Validate(doc); err != nil {
			return stageErr(StageSchema, "the check body is not v0's body_check: %v", err)
		}
	case *StatementBody:
		if body.Names != nil && (body.Names.Cancellation == nil) == (body.Names.Ruling == nil) {
			return bad("a statement names one arrived record: a cancellation or a ruling")
		}
	}
	return nil
}

// checkRows refuses rows the schema admits that the rule cannot read: a
// row id given twice, and a date that hangs on a record named nowhere.
func checkRows(tr TermsRows) error {
	seen := map[string]bool{}
	for _, row := range tr.Rows {
		if seen[row.ID] {
			return stageErr(StageRecord, "row %s is given twice", row.ID)
		}
		seen[row.ID] = true
		for _, d := range []Date{row.Due, row.WindowEnd} {
			if d.After != "" {
				if _, ok := tr.Named[d.After]; !ok {
					return stageErr(StageRecord, "row %s hangs on %s, which is not named", row.ID, d.After)
				}
			}
		}
	}
	return nil
}

// CheckAgreement reads an agreement against the offer it cites (§3.3,
// §3.4): the offer is a propose of the same deal whose record digest the
// agreement cites; the rows digest is the offer's; the witnesses and the
// adjudicator are the offer's; the role is the offerer's on the offerer's
// side and the other on the acceptor's. heldAcceptorAgreement is the record
// id of the acceptor's agreement the holder holds, which an offerer's
// agreement with a counterparty that runs the layer names in its seen field;
// it is ignored for an acceptor's agreement. Both records must pass Check.
func CheckAgreement(agreement, offer Record, heldAcceptorAgreement string) error {
	bad := func(format string, args ...any) error { return stageErr(StageAgreement, format, args...) }
	ag, ok := asAgree(agreement.Body)
	if !ok || ag.Kind != KindAgreement {
		return bad("not an agreement")
	}
	of, ok := offer.Body.(*ProposeBody)
	if !ok {
		return bad("the agreement cites a %s, not an offer", offer.Block.RecordType)
	}
	d, err := Digest(offer)
	if err != nil {
		return err
	}
	if ag.Offer != RefTo(d) {
		return bad("the agreement does not cite this offer's record digest")
	}
	if agreement.Block.DealID != offer.Block.DealID {
		return bad("the agreement's deal_id is not the offer's")
	}
	rows, err := RowsDigest(of.TermsRows)
	if err != nil {
		return err
	}
	if ag.RowsDigest != rows {
		return bad("rows_digest is not the offer's rows digest")
	}
	if !slices.Equal(ag.Witnesses, of.Witnesses) {
		return bad("the agreement's witnesses are not the offer's")
	}
	if !sameAdjudicator(ag.Adjudicator, of.Adjudicator) {
		return bad("the agreement's adjudicator is not the offer's")
	}
	switch ag.Side {
	case SideOfferer:
		if ag.Role != of.Role {
			return bad("the offerer's role is the offer's")
		}
		layer := ag.CounterpartyLayer != nil && *ag.CounterpartyLayer
		if layer && (agreement.Block.Seen.V == nil || *agreement.Block.Seen.V != heldAcceptorAgreement) {
			return bad("the offerer's agreement names the acceptor's agreement it holds in seen")
		}
	case SideAcceptor:
		if ag.Role == of.Role {
			return bad("the acceptor's role is the other side of the offer's")
		}
	}
	return nil
}

func sameAdjudicator(a, b Null[Adjudicator]) bool {
	switch {
	case !a.Present || !b.Present:
		return a.Present == b.Present
	case a.V == nil || b.V == nil:
		return a.V == nil && b.V == nil
	}
	return *a.V == *b.V
}

var (
	schemaOnce sync.Once
	schemaC    *jsonschema.Schema
	schemaErr  error
)

// ProfileSchema is the raw x-deal-v1 record schema.
func ProfileSchema() []byte { return bytes.Clone(xdealv1files.Schema) }

func profileSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		for _, f := range []struct {
			id  string
			raw []byte
		}{
			{"https://agentactioncapsule.org/profiles/x-deal-v0/record.schema.json", dealprofile.ProfileSchema()},
			{"https://agentactioncapsule.org/profiles/x-deal-v1/record.schema.json", xdealv1files.Schema},
		} {
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(f.raw))
			if err != nil {
				schemaErr = err
				return
			}
			if err := c.AddResource(f.id, doc); err != nil {
				schemaErr = err
				return
			}
		}
		schemaC, schemaErr = c.Compile("https://agentactioncapsule.org/profiles/x-deal-v1/record.schema.json")
	})
	return schemaC, schemaErr
}

func stageSchema(doc any) error {
	s, err := profileSchema()
	if err != nil {
		return err
	}
	doc, err = jsonschema.UnmarshalJSON(bytes.NewReader(mustJCS(doc)))
	if err != nil {
		return err
	}
	if err := s.Validate(doc); err != nil {
		v, ok := err.(*jsonschema.ValidationError)
		if !ok {
			return err
		}
		var leaves []string
		schemaLeaves(v, &leaves)
		return &StageError{Stage: StageSchema, Detail: leaves[0], Causes: leaves}
	}
	return nil
}

// schemaLeaves are a validation error's failed leaves, depth first: where,
// and why (as dealprofile's).
func schemaLeaves(v *jsonschema.ValidationError, out *[]string) {
	if len(v.Causes) == 0 {
		*out = append(*out, "/"+strings.Join(v.InstanceLocation, "/")+": "+v.ErrorKind.LocalizedString(schemaPrinter))
		return
	}
	for _, c := range v.Causes {
		schemaLeaves(c, out)
	}
}

var schemaPrinter = message.NewPrinter(language.English)

func mustJCS(doc any) []byte {
	b, _ := canonical.JCS(doc) // JCS-serializable: the canonicalization stage ran first
	return b
}
