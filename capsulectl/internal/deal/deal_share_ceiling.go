package deal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
)

// The user's bounds are private from the counterparty, whichever side the
// user is on: a seller who learns the buyer's maximum (intent.max_total_minor)
// learns what to ask for, and a buyer who learns the seller's floor
// (intent.min_total_minor) learns what to offer. The floor is never sealed at
// all (only its commitment). The adjudicator's copy may carry the maximum (it
// is how "over your limit" is judged). The party role changes only the words.
//
// The bound fields are the private bounds only: intent.max_total_minor,
// intent.min_total_minor, and their openings (bounds_openings and
// commercial_bounds_opening; the maximum has no opening of its own). The
// final close's terms amount and currency openings are not bound fields:
// they travel in `disclose --share counterparty` (§2.10 Decision 3).
//
// Two rules keep them, by what can label a number as a bound (Ethan's
// decision on capsule-cli#278):
//   - Structured amount fields are governed by field rules only. The bound
//     fields are never in a counterparty copy (a record that carries one is
//     withheld), and the floor and its openings are in no shared copy.
//     Every other amount field (the close's amount and its opening, price
//     and deposit terms, snapshot and act amounts, the merchant's charged
//     total) is shared as its field's rules say, even when its value equals
//     a bound: a bare number never says that it is one. The gate still reads
//     a whole copy, so this holds because such a field is either written
//     in minor units, which the money reader does not take as a bound
//     ("200000"), or is an amount the copy shows anyway (dealShownAmounts).
//   - Free text keeps value matching, since it can label a number ("my max
//     is 250"): notes, rendered page and report text, and merchant section
//     prose. The gate refuses a copy whose text states either bound as
//     money, unless it equals an amount the copy may show anyway (the price
//     asked, an amount paid). The merchant section's "your limit" basis is
//     withheld by field: it never states an approved amount that is only
//     the limit.

// dealPrivateBoundKeys are the bound fields no counterparty copy carries.
var dealPrivateBoundKeys = map[string]bool{"max_total_minor": true, "min_total_minor": true}

// dealBasisYourLimit is the merchant section's basis when what was approved
// is only the user's spending limit.
const dealBasisYourLimit = "your limit"

// dealCeiling is a bound as the copy could write it: as money with its
// currency, and as a bare decimal amount. floor marks the user's floor
// (min_total_minor), which no shared copy may carry; the limit
// (max_total_minor) is kept from the counterparty only.
type dealCeiling struct {
	minor    int64
	currency string
	floor    bool
}

// dealFloorKeys name the floor and its openings: no shared copy, for any
// audience, carries them.
var dealFloorKeys = []string{"min_total_minor", "bounds_openings", "commercial_bounds_opening"}

func dealCeilings(events []sealedEvent) []dealCeiling {
	var out []dealCeiling
	currency := ""
	if len(events) > 0 && events[0].Event.Open != nil {
		currency = events[0].Event.Open.Terms.Currency
	}
	for _, se := range events {
		e := se.Event
		var intents []dealIntent
		if e.Open != nil {
			intents = append(intents, e.Open.Intent)
		}
		if e.Intent != nil {
			intents = append(intents, *e.Intent)
		}
		for _, i := range intents {
			if i.MaxTotalMinor != nil {
				out = append(out, dealCeiling{*i.MaxTotalMinor, currency, false})
			}
			if i.MinTotalMinor != nil {
				out = append(out, dealCeiling{*i.MinTotalMinor, currency, true})
			}
		}
	}
	return out
}

// dealShownAmounts are the amounts a counterparty copy may carry: what the
// seller asked and what was paid, which the seller knows already, among them
// the total the merchant's own email says it charged.
func dealShownAmounts(events []sealedEvent) map[int64]bool {
	shown := map[int64]bool{}
	terms := func(t *dealTerms) {
		if t == nil {
			return
		}
		for _, v := range []*int64{t.PriceMinor, t.DepositMinor} {
			if v != nil {
				shown[*v] = true
			}
		}
	}
	currency := ""
	if len(events) > 0 && events[0].Event.Open != nil {
		currency = events[0].Event.Open.Terms.Currency
	}
	for _, se := range events {
		e := se.Event
		if e.Open != nil {
			terms(&e.Open.Terms)
		}
		if e.Change != nil {
			terms(e.Change.Terms)
		}
		if e.Snapshot != nil {
			terms(e.Snapshot.Terms)
			if e.Snapshot.AmountMinor != nil {
				shown[*e.Snapshot.AmountMinor] = true
			}
		}
		if e.Act != nil && e.Act.AmountMinor != nil {
			shown[*e.Act.AmountMinor] = true
		}
		// A charge in another currency shows a different amount, whatever
		// its minor units: ¥6,400 is not $64.00. A receipt that names no
		// currency, or a deal with none, is read in the deal's: the charge
		// is still an amount paid that the copy shows.
		if e.Evidence != nil && e.Evidence.Email != nil && e.Evidence.Email.Parsed.TotalMinor != nil {
			if p := e.Evidence.Email.Parsed; p.Currency == "" || currency == "" || strings.EqualFold(p.Currency, currency) {
				shown[*p.TotalMinor] = true
			}
		}
	}
	return shown
}

// hexRun is a run of 16 or more hex digits.
var hexRun = regexp.MustCompile(`(?i)[0-9a-f]{16,}`)

// removeHexTokens blanks every whole token that is a run of 16 or more hex
// digits (a capsule id, a digest, a key, a signature): one with no letter or
// digit right before or after it.
func removeHexTokens(text []byte) []byte {
	out := append([]byte(nil), text...)
	alnum := func(i int) bool {
		if i < 0 || i >= len(text) {
			return false
		}
		b := text[i] | 0x20
		return (b >= 'a' && b <= 'z') || (text[i] >= '0' && text[i] <= '9')
	}
	for _, loc := range hexRun.FindAllIndex(text, -1) {
		if alnum(loc[0]-1) || alnum(loc[1]) {
			continue
		}
		for i := loc[0]; i < loc[1]; i++ {
			out[i] = ' '
		}
	}
	return out
}

// maskCoseIDs returns the hook dealGateText applies to a shared copy's JSON
// regions for the money checks. It turns the leading digits of the string
// value of every object member whose key is exactly "cose" and whose object
// is a "checkpoint" member's value (or the document root), as the producers
// write it (a COSE checkpoint, base64url bytes), into a letter, so the money reader does not
// read an amount at the start of a random id ("8krSil…" as 8 kr, "8gbp-…" as
// 8 GBP: issue #26). The member is found by walking the region with
// json.Decoder.Token, so its key is known by position, not by bytes: prose
// that imitates JSON, or a key such as "\"cose", is never one (issue #62). A
// "cose" key elsewhere (terms.conditions takes any lowercase name) is free
// text and is read as money.
// A value is masked only when its raw bytes are the decoded value between two
// quotes and it is still a whole mixed-case id of 16 or more [0-9A-Za-z_-]
// after the deal's own ids and the whole hex tokens are taken out, as the
// gate takes them out before it reads money.
//
// Declared residual (false positives only, never a missed bound): a cose
// value in a copy that is not valid JSON, written with escapes, or held in a
// data script that does not decode is read as money, as before #60. Any other
// base64url field is not masked; see TestNoOtherFieldHoldsAnID.
func maskCoseIDs(events []sealedEvent) func([]byte) []byte {
	return func(text []byte) []byte {
		out := append([]byte(nil), text...)
		dec := json.NewDecoder(bytes.NewReader(text))
		type frame struct {
			object, expectKey bool
			key               string
			// checkpoint: the object is a "checkpoint" member's value or the
			// document root, the only places a producer writes "cose".
			checkpoint bool
		}
		var stack []frame
		for {
			tok, err := dec.Token()
			if err != nil {
				return out
			}
			end := int(dec.InputOffset())
			switch v := tok.(type) {
			case json.Delim:
				if v == '{' || v == '[' {
					n := len(stack)
					at := n == 0 || stack[n-1].object && stack[n-1].key == "checkpoint"
					stack = append(stack, frame{object: v == '{', expectKey: v == '{', checkpoint: at})
					continue
				}
				stack = stack[:len(stack)-1]
			case string:
				n := len(stack)
				if n > 0 && stack[n-1].object && stack[n-1].expectKey {
					stack[n-1].key, stack[n-1].expectKey = v, false
					continue
				}
				if n > 0 && stack[n-1].object && stack[n-1].key == "cose" && stack[n-1].checkpoint {
					if start := end - len(v) - 1; start >= 1 && string(text[start-1:end]) == `"`+v+`"` && isWholeID(events, v) {
						for i := start; i < end-1 && text[i] >= '0' && text[i] <= '9'; i++ {
							out[i] = 'x'
						}
					}
				}
			}
			// A value ended: the enclosing object expects a key next.
			if n := len(stack); n > 0 && stack[n-1].object {
				stack[n-1].expectKey = true
			}
		}
	}
}

// isWholeID reports whether v is one random id: 16 or more of [0-9A-Za-z_-]
// with an upper-case and a lower-case letter, unchanged when the deal's own
// ids and the whole hex tokens are taken out of it.
func isWholeID(events []sealedEvent, v string) bool {
	if len(v) < 16 {
		return false
	}
	var upper, lower bool
	for _, b := range []byte(v) {
		switch {
		case b >= 'A' && b <= 'Z':
			upper = true
		case b >= 'a' && b <= 'z':
			lower = true
		case b >= '0' && b <= '9', b == '-', b == '_':
		default:
			return false
		}
	}
	w := []byte(v)
	for _, id := range dealOwnIDs(events) {
		w = removeWholeToken(w, []byte(id))
	}
	return upper && lower && bytes.Equal(removeHexTokens(w), []byte(v))
}

// dealCeilingGate refuses a shared copy that carries a bound it may not: the
// floor (by name, by its openings, or as money) in any shared copy, and the
// spending limit in the counterparty's. It reads the same plain form as the
// share gate (foldText). An amount the copy shows anyway (the price asked, an
// amount paid) is not a leak.
func dealCeilingGate(data []byte, events []sealedEvent, audience string) error {
	if audience == cli.DealAudienceKeep {
		return nil
	}
	counterparty := audience == cli.DealAudienceCounterparty
	// data is the raw copy: dealGateText masks the cose ids in its JSON regions.
	data = dealGateText(data, maskCoseIDs(events))
	// The deal's own ids and every digest, key and signature are taken out
	// first, as the page gate takes out the ids it allows: a run of hex is
	// never prose, and one that holds "3cad8" is not 8 Canadian dollars.
	folded := []byte(foldText(string(data)))
	for _, id := range dealOwnIDs(events) {
		folded = removeWholeToken(folded, []byte(foldText(id)))
	}
	data = removeHexTokens(folded)
	text := strings.ToLower(string(data))
	refuseLimit := cli.InputError("refusing to write the shared copy: the counterparty's copy would carry your spending limit")
	if dealRole(events) == dealRoleSeller {
		refuseLimit = cli.InputError("refusing to write the shared copy: the counterparty's copy would carry the lowest price you will take, or a limit you set")
	}
	refuseFloor := cli.InputError("refusing to write the shared copy: it would carry the lowest price you will take")
	for _, k := range dealFloorKeys {
		if strings.Contains(text, k) {
			return refuseFloor
		}
	}
	if counterparty && strings.Contains(text, "max_total_minor") {
		return refuseLimit
	}
	// The bound as money in any of the ways prose writes it ("$1,700.00",
	// "1.700,00 €", "USD 1 700", "lowest I'll take is 1700"), compared by
	// value.
	money := string(data)
	switch statesBound(money, events, counterparty) {
	case "intent.min_total_minor":
		return refuseFloor
	case "intent.max_total_minor":
		return refuseLimit
	}
	shown := dealShownAmounts(events)
	for _, c := range dealCeilings(events) {
		if shown[c.minor] || (!c.floor && !counterparty) {
			continue
		}
		for _, form := range []string{formatMoney(c.minor, c.currency), fmt.Sprintf("%d.%02d", c.minor/100, c.minor%100)} {
			if regexp.MustCompile(`(^|[^0-9.])` + regexp.QuoteMeta(strings.ToLower(form)) + `([^0-9]|$)`).MatchString(strings.ToLower(money)) {
				if c.floor {
					return refuseFloor
				}
				return refuseLimit
			}
		}
	}
	return nil
}

// dealOwnIDs are the deal's own identifiers, which every shared copy carries (the
// x-deal-v0 deal_id and the log id deal/<deal_id>): random hex, never a private
// value. The gate takes them out before it checks, or a deal id whose hex happens to
// hold a Luhn-valid digit run reads as a card number and the deal can never be
// shared.
func dealOwnIDs(events []sealedEvent) []string {
	if len(events) == 0 || events[0].Event.DealID == "" {
		return nil
	}
	return []string{events[0].Event.DealID}
}

// dealShareGate is the last check before a shared copy is written or a share
// is put on record: the share gate, then the spending-limit gate for the
// counterparty.
func dealShareGate(data []byte, events []sealedEvent, audience string) error {
	allowed := append(dealShareableIDs(events, audience), dealOwnIDs(events)...)
	if err := dealPageGate(data, events, allowed...); err != nil {
		return err
	}
	return dealCeilingGate(data, events, audience)
}
