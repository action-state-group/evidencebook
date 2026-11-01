// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// The standard_history/1 deal entry (agent-action-capsule
// spec/standard-history-v0.md §5.2, SH-2) as this profile fills it from a
// deal's sealed records. The mapping is here, with the profile, so every
// producer that counts x-deal-v0 deals counts them the same way
// (evidencebook-deal#85; Sherlock2's rows, evidencebook#237). A member with
// no sealed record behind it is never guessed:
//
//   - A deal is a deal's own log holding an `action` (or typed
//     `action-record/v0`) `commit` or `pay`. A sale's log is not a deal; a
//     deal that never reached such a step was never agreed.
//   - agreed_at is that first step's committed_at, to the minute: a tick's
//     anchor time is a minute floor, and a step sealed after it within the
//     same minute would sort after the anchor and drop out of every count.
//   - role is the baseline intent's party_role: seller is "provided", buyer
//     or absent "bought".
//   - amount is the amount_minor and currency of the check (typed
//     proposed-action/v0) the step rests on, scaled by the currency's ISO
//     4217 minor units. The profile lets a check omit them, and SH gives an
//     amount no unknown value, so a deal without one is ErrUnpriced: the
//     host refuses the card, it never drops the deal.
//   - counterparty_class is the one member of that name a pay check carries
//     at the top of its record (SH §7: the record that recorded how the deal
//     was paid), when it is one of the three SH values. A deal whose
//     records carry the member on two or more records, or on a record that
//     is no pay check, or with another value, lacks it: absent, never
//     guessed, and counted in class_missing.
//   - outcome is the newest verified ec:deal-reconcile@1 Result's claims:
//     at least one claim and every claim met is "held", any not_met is
//     "did_not_hold", and anything else (no claim, met with undecided, no
//     verified Result) is "undecided".
//   - happened is "stamped_by_both" only when the counterparty's half is
//     received and verifies (the host's check, as the deal view's); else
//     "receipt_only" when the holder's own final close observed a
//     "received" outcome; else "no_record".
//   - disputes is empty: the profile seals no IP §11.2 disputes statement.

// HistoryRecord is one sealed record of a deal's own log.
type HistoryRecord struct {
	// Payload is the record's JSON text, the Capsule's payload.
	Payload []byte
	// CommittedAt is when the holder's book committed it.
	CommittedAt time.Time
}

// HistoryResult is one Result the deal's results book sealed.
type HistoryResult struct {
	// Result is the evidence-result-v0 document (the deal_terms_result
	// payload's "result").
	Result []byte
	// Verified is the host's check that the Result's record signature and
	// seal verify. A Result that does not is no Result.
	Verified bool
}

// HistoryInput is what the host holds for one deal under the card's anchor.
type HistoryInput struct {
	// Records are the deal log's records the anchor commits, in log order.
	Records []HistoryRecord
	// Results are the deal's sealed Results the anchor commits, oldest
	// first. Results live on the deal's results book (deal/<id>/results),
	// not on its log, so the host passes only those within the results
	// book's extent in StandardHistoryAnchor.Extents: a Result sealed after
	// the anchor would change a card that claims to be as of it, and the
	// card could not be reproduced.
	Results []HistoryResult
	// CounterpartyHalfVerified is, under the anchor like every other input,
	// whether the counterparty's half of the deal is received and its
	// signature verifies.
	CounterpartyHalfVerified bool
}

// ErrUnpriced is a deal whose agreeing step rests on a check with no
// amount, or a currency with no ISO 4217 minor units: SH has no entry for
// it, so the card cannot be built (agent-action-capsule#516).
var ErrUnpriced = errors.New("dealprofile: a deal has no recorded amount")

// historyRecord is the members of a record (x-deal-v0 or typed) the entry
// reads.
type historyRecord struct {
	digest      string
	committedAt time.Time
	kind        string // x-deal-v0 record_type, or the typed type
	dealID      string
	refs        map[string][]string
	body        map[string]json.RawMessage
	// class is the record's top-level counterparty_class member, hasClass
	// whether it has one at all (any value, including a non-string).
	class    string
	hasClass bool
}

// readHistoryRecord reads a step record of either kind. ok is false for a
// record that is no step, such as a sealed report, which says nothing the
// entry reads.
func readHistoryRecord(r HistoryRecord) (h historyRecord, ok bool, err error) {
	var top struct {
		Block *struct {
			DealID     string   `json:"deal_id"`
			RecordType string   `json:"record_type"`
			Refs       []RelRef `json:"refs"`
		} `json:"x-deal-v0"`
		Class   json.RawMessage            `json:"counterparty_class"`
		Type    string                     `json:"type"`
		ChainID string                     `json:"chain_id"`
		Refs    []TypedRelRef              `json:"refs"`
		Body    map[string]json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(r.Payload, &top); err != nil {
		return historyRecord{}, false, fmt.Errorf("dealprofile: a deal record is not JSON: %w", err)
	}
	h = historyRecord{committedAt: r.CommittedAt, refs: map[string][]string{}, body: top.Body}
	if top.Class != nil {
		h.hasClass = true
		_ = json.Unmarshal(top.Class, &h.class) // a non-string leaves "", which no class is
	}
	switch {
	case top.Block != nil:
		h.kind, h.dealID = top.Block.RecordType, top.Block.DealID
		for _, ref := range top.Block.Refs {
			h.refs[ref.Rel] = append(h.refs[ref.Rel], ref.Digest)
		}
	case strings.HasSuffix(top.Type, "/v0"):
		h.kind, h.dealID = top.Type, top.ChainID
		for _, ref := range top.Refs {
			h.refs[ref.Rel] = append(h.refs[ref.Rel], ref.Digest)
		}
	default:
		return historyRecord{}, false, nil
	}
	if h.digest, err = Digest(json.RawMessage(r.Payload)); err != nil {
		return historyRecord{}, false, err
	}
	return h, true, nil
}

func (h historyRecord) str(member string) string {
	var s string
	_ = json.Unmarshal(h.body[member], &s)
	return s
}

// ref is the one digest the record names under rel, or "".
func (h historyRecord) ref(rel string) string {
	if digests := h.refs[rel]; len(digests) == 1 {
		return digests[0]
	}
	return ""
}

// StandardHistoryDeal is one deal's SH §5.2 entry. ok is false for a log
// that is not an agreed deal (it is not on the card). ErrUnpriced, or any
// other error, means the card cannot be built.
func StandardHistoryDeal(in HistoryInput) (deal standardhistory.Deal, ok bool, err error) {
	records := make([]historyRecord, 0, len(in.Records))
	byDigest := map[string]historyRecord{}
	for _, r := range in.Records {
		h, step, err := readHistoryRecord(r)
		if err != nil {
			return standardhistory.Deal{}, false, err
		}
		if !step {
			continue
		}
		records = append(records, h)
		byDigest[h.digest] = h
	}
	if len(records) == 0 || strings.HasPrefix(records[0].dealID, "sale-") || records[0].kind == TypeSale {
		return standardhistory.Deal{}, false, nil
	}
	var agreed *historyRecord
	for i := range records {
		r := &records[i]
		if (r.kind == TypeAction || r.kind == TypeActionRecord) && (r.str("action") == "commit" || r.str("action") == "pay") {
			agreed = r
			break
		}
	}
	if agreed == nil {
		return standardhistory.Deal{}, false, nil
	}
	dealID := records[0].dealID
	amount, err := agreedAmount(*agreed, byDigest)
	if err != nil {
		return standardhistory.Deal{}, false, fmt.Errorf("deal %s: %w", dealID, err)
	}
	return standardhistory.Deal{
		Deal:              dealID,
		AgreedAt:          agreed.committedAt.UTC().Truncate(time.Minute).Format("2006-01-02T15:04:05Z"),
		Role:              dealRole(records),
		Amount:            amount,
		CounterpartyClass: counterpartyClass(records),
		Outcome:           reconcileOutcome(in.Results),
		Happened:          happened(records, byDigest, in.CounterpartyHalfVerified),
		Disputes:          []standardhistory.Dispute{},
	}, true, nil
}

// agreedAmount is the amount of the check the agreeing step rests on:
// action → approval (authorized_by) → verdict (approves) → check (checks),
// or action-record/v0 → action-evaluation/v0 (evaluation_ref) →
// proposed-action/v0 (proposed_action_digest).
func agreedAmount(step historyRecord, byDigest map[string]historyRecord) (standardhistory.Amount, error) {
	var check historyRecord
	var found bool
	if step.kind == TypeActionRecord {
		var ref TypedRef
		_ = json.Unmarshal(step.body["evaluation_ref"], &ref)
		if evaluation, ok := byDigest[ref.Digest]; ok && evaluation.kind == TypeActionEvaluation {
			check, found = byDigest[evaluation.str("proposed_action_digest")]
			found = found && check.kind == TypeProposedAction
		}
	} else if approval, ok := byDigest[step.ref("authorized_by")]; ok && approval.kind == TypeApproval {
		if verdict, ok := byDigest[approval.ref("approves")]; ok && verdict.kind == TypeVerdict {
			check, found = byDigest[verdict.ref("checks")]
			found = found && check.kind == TypeCheck
		}
	}
	if !found {
		return standardhistory.Amount{}, fmt.Errorf("%w: the %s step's check is not on its log", ErrUnpriced, step.str("action"))
	}
	var minor *int64
	_ = json.Unmarshal(check.body["amount_minor"], &minor)
	currency := check.str("currency")
	if minor == nil || *minor < 0 || currency == "" {
		return standardhistory.Amount{}, fmt.Errorf("%w: its check states no amount and currency", ErrUnpriced)
	}
	scale, ok := MinorUnits(currency)
	if !ok {
		return standardhistory.Amount{}, fmt.Errorf("%w: currency %q has no ISO 4217 minor units", ErrUnpriced, currency)
	}
	return standardhistory.Amount{Value: strconv.FormatInt(*minor, 10), AssetCode: currency, AssetScale: scale}, nil
}

func dealRole(records []historyRecord) string {
	for _, r := range records {
		if r.kind == TypeBaseline {
			var intent struct {
				PartyRole string `json:"party_role"`
			}
			_ = json.Unmarshal(r.body["intent"], &intent)
			if intent.PartyRole == "seller" {
				return "provided"
			}
			return "bought"
		}
	}
	return "bought"
}

// reconcileOutcome reads the newest verified Result's claims.
func reconcileOutcome(results []HistoryResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		if !results[i].Verified {
			continue
		}
		var doc struct {
			Claims []struct {
				Verdict string `json:"verdict"`
			} `json:"claims"`
		}
		if json.Unmarshal(results[i].Result, &doc) != nil || len(doc.Claims) == 0 {
			return "undecided"
		}
		met := 0
		for _, c := range doc.Claims {
			switch c.Verdict {
			case "not_met":
				return "did_not_hold"
			case "met":
				met++
			}
		}
		if met == len(doc.Claims) {
			return "held"
		}
		return "undecided"
	}
	return "undecided"
}

// happened is SH §5.5 over the holder's records: the counterparty's half,
// else the holder's own final close of a received outcome.
func happened(records []historyRecord, byDigest map[string]historyRecord, counterpartyHalf bool) string {
	if counterpartyHalf {
		return "stamped_by_both"
	}
	var final *historyRecord
	for i := range records {
		if records[i].kind == TypeClose && records[i].str("outcome") != "open" {
			final = &records[i]
		}
	}
	if final != nil {
		if outcome, ok := byDigest[final.ref("outcome")]; ok && (outcome.kind == TypeOutcome || outcome.kind == TypeActionOutcome) && outcome.str("status") == "received" {
			return "receipt_only"
		}
	}
	return "no_record"
}

// MinorUnits is a currency's ISO 4217 minor units: the assetScale of an
// amount in minor units. ok is false for a code with none (a metal, a
// fund code without them) or an unknown one.
func MinorUnits(currency string) (scale int, ok bool) {
	if scale, ok := minorUnitExceptions[currency]; ok {
		return scale, true
	}
	if strings.Contains(twoDecimalCurrencies, " "+currency+" ") && len(currency) == 3 {
		return 2, true
	}
	return 0, false
}

// ISO 4217 codes with minor units other than two.
var minorUnitExceptions = map[string]int{
	"BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "ISK": 0, "JPY": 0, "KMF": 0, "KRW": 0, "PYG": 0,
	"RWF": 0, "UGX": 0, "UYI": 0, "VND": 0, "VUV": 0, "XAF": 0, "XOF": 0, "XPF": 0,
	"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
	"CLF": 4, "UYW": 4,
}

// ISO 4217 codes with two minor units, including ANG and BGN, withdrawn
// for XCG and EUR but still on older deals.
const twoDecimalCurrencies = " AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BMD BND BOB BOV BRL BSD BTN BWP BYN BZD" +
	" CAD CDF CHE CHF CHW CNY COP COU CRC CUP CVE CZK DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GTQ GYD" +
	" HKD HNL HTG HUF IDR ILS INR IRR JMD KES KGS KHR KPW KYD KZT LAK LBP LKR LRD LSL MAD MDL MGA MKD MMK MNT MOP MRU" +
	" MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD PAB PEN PGK PHP PKR PLN QAR RON RSD RUB SAR SBD SCR SDG SEK" +
	" SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TOP TRY TTD TWD TZS UAH USD USN UYU UZS VED VES WST XCD XCG" +
	" YER ZAR ZMW ZWG "

// counterpartyClass is SH §7 over the deal's records: the class of the one
// record that carries the member, a pay check (x-deal-v0 PROFILE.md section
// 11), when it is one of SH's three values. nil is absent.
func counterpartyClass(records []historyRecord) *string {
	var carrier *historyRecord
	for i := range records {
		if !records[i].hasClass {
			continue
		}
		if carrier != nil {
			return nil // two carriers read as none
		}
		carrier = &records[i]
	}
	if carrier == nil || carrier.kind != TypeCheck || carrier.str("action") != "pay" || !slices.Contains(standardhistory.CounterpartyClasses, carrier.class) {
		return nil
	}
	class := carrier.class
	return &class
}
