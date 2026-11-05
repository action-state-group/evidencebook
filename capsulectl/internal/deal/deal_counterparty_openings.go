package deal

import (
	"encoding/hex"
	"encoding/json"

	"github.com/action-state-group/evidencebook/dealprofile"
)

// The user's own copy opens who the deal was with and what the buyer wrote
// (evidencebook-deal#56). The owner already knows both, so a copy that stays
// with them tells no new party anything; a shared copy carries neither, and
// not the deal key either, which would let a holder test a guessed name
// against counterparty.ids.name.
//
// counterparty_openings holds the deal's key and, for the counterparty the
// deal ends with, the payee, name and domain, each at a step whose sealed
// record carries the keyed fingerprint it was made into. acceptance_openings holds, per acceptance,
// its salted words (content_commitment). Both are left out when the deal has
// none, so a deal sealed before this change reports as it did.

// counterpartyOpeningKinds are the fingerprints a page may open.
var counterpartyOpeningKinds = []string{"payee", "name", "domain"}

func whoValue(w dealWho, kind string) string {
	switch kind {
	case "domain":
		return w.Domain
	case "payee":
		return w.Payee
	}
	return w.Name
}

// sealedCounterpartyIDs are the keyed fingerprints a record payload seals for
// its counterparty: in the x-deal-v0 block, or, in a typed record, its body.
func sealedCounterpartyIDs(payload []byte) map[string]interface{} {
	var record map[string]interface{}
	if json.Unmarshal(payload, &record) != nil {
		return nil
	}
	return recordCounterpartyIDs(record)
}

func recordCounterpartyIDs(record map[string]interface{}) map[string]interface{} {
	block, _ := record["x-deal-v0"].(map[string]interface{})
	body, _ := record["body"].(map[string]interface{})
	for _, holder := range []map[string]interface{}{block, body} {
		cp, _ := holder["counterparty"].(map[string]interface{})
		if ids, _ := cp["ids"].(map[string]interface{}); ids != nil {
			return ids
		}
	}
	return nil
}

// endingWho is who the deal's last act goes to, as cardWho reads it for the
// check that act rests on: the fold's who, with the check's own name, site and
// payee over it, and the act's payee (which it sealed) over that. A check
// about a named recipient (a platform, someone else) names none.
func endingWho(state dealState, events []sealedEvent) dealWho {
	who := state.who
	// An act before the leg in force went to the earlier counterparty.
	for i := len(events) - 1; i >= currentLegStart(events); i-- {
		act := events[i].Event.Act
		if act == nil {
			continue
		}
		if snap, ok := actSnapshot(events, act.AuthorizedBy); ok && snap.Event.Snapshot != nil && !namedRecipient(snap.Event.Snapshot.DisclosingTo) {
			if w := snap.Event.Snapshot.Who; w != nil {
				who = overlayWho(who, *w)
			}
		}
		if act.Payee != "" {
			who.Payee = act.Payee
		}
		break
	}
	return who
}

// counterpartyOpenings opens the counterparty the deal ends with, as the OK
// card names them: endingWho (cardWho's reading), its payee, name and
// domain, each opened at the last step whose sealed fingerprint it recomputes
// to. It is nil when none is.
func counterpartyOpenings(key []byte, events []sealedEvent) map[string]interface{} {
	if len(key) == 0 {
		return nil
	}
	state, err := foldDeal(events)
	if err != nil {
		return nil
	}
	who := endingWho(state, events)
	sealed := make([]map[string]interface{}, len(events))
	var before []sealedEvent
	for i, se := range events {
		payload, _, err := encodeDealRecord(se.Event, before, key)
		before = append(before, se)
		if err == nil {
			sealed[i] = sealedCounterpartyIDs(payload)
		}
	}
	byStep := map[int]map[string]interface{}{}
	for _, kind := range counterpartyOpeningKinds {
		value := whoValue(who, kind)
		want, err := dealprofile.Fingerprint(key, kind, value)
		if value == "" || err != nil {
			continue
		}
		for i := len(events) - 1; i >= 0; i-- {
			if got, _ := sealed[i][kind].(string); got == want {
				if byStep[i] == nil {
					byStep[i] = map[string]interface{}{"step": events[i].CapsuleID}
				}
				byStep[i][kind] = value
				break
			}
		}
	}
	steps := []interface{}{}
	for i := range events {
		if e := byStep[i]; e != nil {
			steps = append(steps, e)
		}
	}
	if len(steps) == 0 {
		return nil
	}
	return map[string]interface{}{"key": hex.EncodeToString(key), "steps": steps}
}

// acceptanceOpenings open each acceptance's content_commitment: the step, and
// the buyer's words under the nonce they were committed with.
func acceptanceOpenings(events []sealedEvent) []interface{} {
	var out []interface{}
	for _, se := range events {
		a := se.Event.Acceptance
		if a == nil || se.Event.Nonces["accepted_words"] == "" {
			continue
		}
		out = append(out, map[string]interface{}{"step": se.CapsuleID, "nonce": se.Event.Nonces["accepted_words"], "text": a.Words})
	}
	return out
}
