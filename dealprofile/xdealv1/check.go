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
	"time"
	"unicode/utf16"
	"unicode/utf8"

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
	if err := checkText(raw); err != nil {
		return nil, nil, err
	}
	doc, err := decodeJSON(raw)
	if err != nil {
		return nil, nil, stageErr(StageCanonicalization, "not one JSON value: %v", err)
	}
	return doc, raw, nil
}

// checkText refuses JSON text a decoder would read other than as written:
// invalid UTF-8 or an escaped surrogate outside a pair (which encoding/json
// replaces with U+FFFD unseen), and an object member given twice (of which
// it keeps the last). A record's digest is over the bytes as read, so both
// would let two texts seal as one.
func checkText(raw []byte) error {
	if !utf8.Valid(raw) {
		return stageErr(StageCanonicalization, "invalid UTF-8")
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		switch {
		case !inString:
			inString = raw[i] == '"'
		case raw[i] == '"':
			inString = false
		case raw[i] == '\\' && i+1 < len(raw):
			i++
			if raw[i] != 'u' || i+4 >= len(raw) {
				continue
			}
			r := hex4(raw[i+1 : i+5])
			i += 4
			if !utf16.IsSurrogate(r) {
				continue
			}
			if r >= 0xdc00 || i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return stageErr(StageCanonicalization, "an escaped surrogate outside a pair")
			}
			if lo := hex4(raw[i+3 : i+7]); lo < 0xdc00 || lo > 0xdfff {
				return stageErr(StageCanonicalization, "an escaped surrogate outside a pair")
			}
			i += 6
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := noRepeats(dec); err != nil {
		return stageErr(StageCanonicalization, "%v", err)
	}
	return nil
}

// noRepeats walks one JSON value and refuses an object member given twice.
func noRepeats(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '[':
		for dec.More() {
			if err := noRepeats(dec); err != nil {
				return err
			}
		}
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return err
			}
			name, _ := k.(string)
			if seen[name] {
				return fmt.Errorf("member %q given twice", name)
			}
			seen[name] = true
			if err := noRepeats(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // the closing delimiter
	return err
}

func hex4(b []byte) rune {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		}
	}
	return r
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
	// noDealID are the journal records that predate or span deals: deal_id
	// absent (§3.1).
	noDealID = []string{TypeIntent, TypeAdmit, TypeSale, TypeThread, TypeThreadOpened, TypeSaleUnpost,
		TypeCounterpartyProfile, TypeDisclosure}
	// journeyTypes carry deal_id once their offer exists, and askTypes when
	// they are between the two parties rather than of an admission. The
	// record alone cannot tell which, so Check takes either and CheckSealed,
	// given what the holder knows, decides.
	journeyTypes = []string{TypeMessage, TypeClaim, TypeEvidence, TypeDetailChange, TypeAgentMove}
	askTypes     = []string{TypeAsk, TypeAnswer}
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

func asPropose(b Body) (ProposeBody, bool) {
	switch x := b.(type) {
	case ProposeBody:
		return x, true
	case *ProposeBody:
		if x != nil {
			return *x, true
		}
	}
	return ProposeBody{}, false
}

func asAgree(b Body) (AgreeBody, bool) {
	if x, ok := bodyOf[AgreeBody](b); ok {
		return *x, true
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
		// A deal-book record leaves the holder, and refs could cite a journal
		// record whose digest must not (PROFILE §5): it cites only through
		// the members its type names.
		if b.Refs != nil {
			return bad("a deal-book record carries no refs: it cites only through its type's own members")
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
		if t == TypePropose && b.Refs != nil {
			return bad("an offer, sent whole, carries no refs: it cites only through counter_to")
		}
		if ag, ok := asAgree(r.Body); ok && (b.Refs != nil || b.Marks != nil || b.Counterparty != nil) {
			return bad("a %s carries no refs, marks or counterparty fingerprint: it names nothing beyond the offer it answers", ag.Kind)
		}
	}
	if err := checkDates(r); err != nil {
		return err
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
	case slices.Contains(journeyTypes, t) || slices.Contains(askTypes, t):
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
		if err := checkWitnesses(body.Witnesses); err != nil {
			return err
		}
		if err := checkForums(body.Adjudicator, body.Forums); err != nil {
			return err
		}
		return checkRows(body.TermsRows)
	case *AgreeBody:
		if err := checkWitnesses(body.Witnesses); err != nil {
			return err
		}
		if body.Kind == KindAgreement {
			if err := checkForums(body.Adjudicator, body.Forums); err != nil {
				return err
			}
			layer := body.CounterpartyLayer != nil && *body.CounterpartyLayer
			if !layer && (!r.Block.Seen.IsNull() || !body.CounterpartyKey.IsNull()) {
				return bad("an agreement with a counterparty that runs no layer has seen null and counterparty_key null")
			}
			if layer && body.CounterpartyKey.IsNull() {
				return bad("an agreement with a counterparty that runs the layer names its record key in counterparty_key")
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
		s, err := v0BodyCheck()
		if err != nil {
			return err
		}
		if err := s.Validate(doc); err != nil {
			return stageErr(StageSchema, "the check body is not v0's body_check: %v", err)
		}
	}
	return nil
}

// checkDates refuses a time the schema's pattern admits that names no
// instant: a calendar date that does not exist (2026-02-30), an hour past
// 23. Every time a record carries is read by rowstatus or compared, so each
// must parse as RFC 3339 UTC to the second and print back the same.
func checkDates(r Record) error {
	times := []string{r.Block.At}
	switch body := r.Body.(type) {
	case *ProposeBody:
		times = append(times, body.StatedAt, body.AgreeBy)
		for _, row := range body.TermsRows.Rows {
			times = append(times, row.Due.At, row.WindowEnd.At)
		}
		for _, n := range body.TermsRows.Named {
			times = append(times, n.Latest)
		}
	case *RulingBody:
		if body.Remedy != nil {
			times = append(times, body.Remedy.Due)
		}
	}
	for _, s := range times {
		if s == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, s)
		// rowstatus reads no year before 1 (rowstatus.go parseTime).
		if err != nil || t.UTC().Format(time.RFC3339) != s || t.Year() < 1 {
			return stageErr(StageRecord, "%q is not a UTC time to the second that exists", s)
		}
	}
	return nil
}

// SealContext is what the holder knows when it seals a journal record that
// the record alone does not show (§3.1, "Where deal_id is present").
type SealContext struct {
	// OfferExists says the offer of the deal the record serves exists (sent
	// or received) when the record is sealed.
	OfferExists bool `json:"offer_exists"`
	// Admission says an ask or answer is of an admission decision, not
	// between the two parties of a deal.
	Admission bool `json:"admission"`
}

// CheckSealed is Check, and then §3.1's deal_id rule for the journal
// records whose presence depends on when they are sealed: a journey record
// (message, claim, evidence, detail_change, agent_move) carries deal_id
// exactly when its offer exists, and an ask or answer in the journal
// exactly when it is not of an admission. Other records are Check's alone.
func CheckSealed(record any, ctx SealContext) error {
	if err := Check(record); err != nil {
		return err
	}
	_, raw, err := decode(record)
	if err != nil {
		return err
	}
	r, err := Decode(raw)
	if err != nil {
		return err
	}
	if PlacementOf(r) != PlacementJournal {
		return nil
	}
	t, has := r.Block.RecordType, r.Block.DealID != ""
	var want bool
	switch {
	case slices.Contains(journeyTypes, t):
		want = ctx.OfferExists
	case slices.Contains(askTypes, t):
		want = !ctx.Admission
	default:
		return nil
	}
	if has != want {
		return stageErr(StageRecord, "a %s record sealed here carries deal_id only %s", t,
			map[bool]string{true: "when its offer exists", false: "between the two parties of a deal"}[slices.Contains(journeyTypes, t)])
	}
	return nil
}

// checkForums refuses a recourse clause that does not fit its adjudicator:
// an agreement with no adjudicator names no forum and allows no dispute, and
// one with an adjudicator names at least one forum.
func checkForums(adj Null[Adjudicator], forums []string) error {
	switch none := adj.IsNull(); {
	case none && len(forums) > 0:
		return stageErr(StageRecord, "an offer or agreement with no adjudicator names no forum")
	case !none && len(forums) == 0:
		return stageErr(StageRecord, "an offer or agreement with an adjudicator names the forums both sides accept")
	}
	return nil
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// CheckDispute reads a dispute of kind dispute against the agreement of its
// deal book: it goes to the adjudicator the agreement names, in one of the
// forums the agreement accepts (the recourse clause, #404: no forum shopping
// at dispute time). A deal whose agreement names no adjudicator allows no
// dispute. The dispute's agreement_ref must cite this agreement. Both
// records must pass Check.
func CheckDispute(dispute, agreement Record) error {
	d, ok := bodyOf[DisputeBody](dispute.Body)
	if !ok || d.Kind != KindDispute {
		return stageErr(StageAgreement, "not a dispute of kind dispute")
	}
	ag, ok := asAgree(agreement.Body)
	if !ok || ag.Kind != KindAgreement {
		return stageErr(StageAgreement, "not an agreement")
	}
	if dispute.Block.DealID != agreement.Block.DealID {
		return stageErr(StageAgreement, "the dispute is of another deal")
	}
	ad, err := Digest(agreement)
	if err != nil {
		return err
	}
	if dispute.Block.AgreementRef == nil || dispute.Block.AgreementRef.Digest != ad {
		return stageErr(StageAgreement, "the dispute is bound to another agreement (agreement_ref)")
	}
	if ag.Adjudicator.IsNull() || !ag.Adjudicator.Present {
		return stageErr(StageAgreement, "the agreement names no adjudicator: no dispute is possible")
	}
	if d.Adjudicator != ag.Adjudicator.V.KeyID {
		return stageErr(StageAgreement, "the dispute goes to another adjudicator than the agreement names")
	}
	if !slices.Contains(ag.Forums, d.Forum) {
		return stageErr(StageAgreement, "the dispute's forum %s is not one the agreement accepts", d.Forum)
	}
	return nil
}

// CheckRuling reads a ruling against the agreement of the deal: it rules in
// one of the forums the agreement accepts. That the adjudicator the
// agreement names signed it is the reader's, from the ruling's Capsule.
func CheckRuling(ruling, agreement Record) error {
	r, ok := bodyOf[RulingBody](ruling.Body)
	if !ok {
		return stageErr(StageAgreement, "not a ruling")
	}
	ag, ok := asAgree(agreement.Body)
	if !ok || ag.Kind != KindAgreement {
		return stageErr(StageAgreement, "not an agreement")
	}
	if ruling.Block.DealID != agreement.Block.DealID {
		return stageErr(StageAgreement, "the ruling is of another deal")
	}
	if !slices.Contains(ag.Forums, r.Forum) {
		return stageErr(StageAgreement, "the ruling's forum %s is not one the agreement accepts", r.Forum)
	}
	return nil
}

// bodyOf is a body of type T, held as a value or a pointer.
func bodyOf[T any](b Body) (*T, bool) {
	switch x := any(b).(type) {
	case T:
		return &x, true
	case *T:
		return x, x != nil
	}
	return nil, false
}

// checkWitnesses refuses a witness named twice: one key id is one witness,
// whatever URLs it is found at.
func checkWitnesses(ws []Witness) error {
	seen := map[string]bool{}
	for _, w := range ws {
		if seen[w.KeyID] {
			return stageErr(StageRecord, "witness %s is named twice", w.KeyID)
		}
		seen[w.KeyID] = true
	}
	return nil
}

// witnessKeys are the key ids a witness list names, sorted: a witness is its
// key id, and its URL only a hint (#482 Q1), so two lists name the same
// witnesses when their key ids agree, whatever the hints and order.
func witnessKeys(ws []Witness) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.KeyID
	}
	slices.Sort(out)
	return out
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
// it is ignored for an acceptor's agreement. It runs Check on both records
// first.
func CheckAgreement(agreement, offer Record, heldAcceptorAgreement string) error {
	bad := func(format string, args ...any) error { return stageErr(StageAgreement, format, args...) }
	for _, r := range []Record{agreement, offer} {
		if err := Check(r); err != nil {
			return err
		}
	}
	ag, ok := asAgree(agreement.Body)
	if !ok || ag.Kind != KindAgreement {
		return bad("not an agreement")
	}
	of, ok := asPropose(offer.Body)
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
	if !slices.Equal(witnessKeys(ag.Witnesses), witnessKeys(of.Witnesses)) {
		return bad("the agreement's witnesses are not the offer's")
	}
	if !sameAdjudicator(ag.Adjudicator, of.Adjudicator) {
		return bad("the agreement's adjudicator is not the offer's")
	}
	if !slices.Equal(sortedCopy(ag.Forums), sortedCopy(of.Forums)) {
		return bad("the agreement's forums are not the offer's")
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

var v0BodyCheck = sync.OnceValues(func() (*jsonschema.Schema, error) {
	return dealprofile.ProfileSchemaAt("#/$defs/body_check")
})

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
