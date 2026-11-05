package deal

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// dealPageOpenings checks, before a deal page is written, the words the page
// shows against the commitments the deal sealed to them: the user's words
// (the report's asked_opening, against the baseline's
// intent.verbatim_commitment) and each statement the agent made (the
// report's representations, against the claim's text_commitment) and, on
// the user's own copy, each card they answered on (card_openings, against
// the approval's card_commitment), each SHA-256 over JCS({nonce, text}).
// The page's own script does not recompute them: it shows what this check
// found, worded as checked when the page was built. Beside the asked words
// and the statements, its result holds counterparty and acceptances: for each
// opening of the user's own copy, in order, whether it recomputed.
//
// It reads the bundle the page embeds, as the page does: the report is the
// sealed report the x-deal-v0 extension names (or, in a bundle written before
// reports were sealed, the extension itself), and each payload is the
// record's disclosed agent_input, which pageGate has already checked against
// its committed digest. An asked-words, card or statement opening that is
// present but does not recompute refuses the page; the error names what,
// never the words. A counterparty or acceptance opening that does not
// recompute is reported not verified instead, and the page shows no value.
// The result says,
// for the asked words and for each representation in order, whether it was
// checked; one that is absent, or whose sealed claim is not the agent's, is
// not checked, and the page says so.
func dealPageOpenings(b map[string]interface{}) (map[string]interface{}, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	bundle, err := cli.DecodeBundleJSON(raw)
	if err != nil {
		return nil, err
	}
	disclosures, _ := bundle["disclosures"].(map[string]interface{})
	input := func(id string) map[string]interface{} {
		d, _ := disclosures[id].(map[string]interface{})
		in, _ := d["agent_input"].(map[string]interface{})
		return in
	}
	report, _, _ := cli.DealSealedReport(bundle)
	out := map[string]interface{}{"asked": false, "representations": []interface{}{}}
	if report == nil {
		return out, nil
	}
	opens := func(opening map[string]interface{}, commitment interface{}) (bool, bool) {
		nonce, ok1 := opening["nonce"].(string)
		text, ok2 := opening["text"].(string)
		want, ok3 := commitment.(string)
		if !ok1 || !ok2 || !ok3 {
			return false, false
		}
		got, err := dealprofile.CommitText(nonce, text)
		return true, err == nil && got == want
	}

	audience, _ := report["audience"].(string)
	if audience != cli.DealAudienceCounterparty && audience != cli.DealAudienceAdjudicator {
		opening, _ := report["asked_opening"].(map[string]interface{})
		step, _ := report["asked_step"].(string)
		body, _ := input(step)["body"].(map[string]interface{})
		intent, _ := body["intent"].(map[string]interface{})
		present, ok := opens(opening, intent["verbatim_commitment"])
		if present && !ok {
			return nil, errors.New("refusing to write the page: the words you asked do not match the commitment sealed when the deal opened (step " + step + ")")
		}
		out["asked"] = ok
	}

	// The cards the user answered on (card_openings, the user's own copy
	// only): each recomputes to its approval's card_commitment (in a typed
	// record, rendering_commitment). A page may show each as the card the
	// answer was given on, so one that names no approval, repeats a step or
	// rides on a shared copy refuses the page as a mismatch does, and so
	// does a card_openings that is not a list. The refusal names the step
	// only when it is a step of this bundle: the field is unchecked input.
	if raw, present := report["card_openings"]; present {
		if audience == cli.DealAudienceCounterparty || audience == cli.DealAudienceAdjudicator {
			return nil, errors.New("refusing to write the page: a shared copy carries the cards you answered on, which only your own copy may")
		}
		cards, ok := raw.([]interface{})
		if !ok {
			return nil, errors.New("refusing to write the page: the cards you answered on are not a list of openings")
		}
		seen := map[string]bool{}
		for i, c := range cards {
			opening, _ := c.(map[string]interface{})
			step, _ := opening["step"].(string)
			record := input(step)
			body, _ := record["body"].(map[string]interface{})
			block, _ := record["x-deal-v0"].(map[string]interface{})
			// Only an answer's own commitment: its check's card_commitment
			// is the same value, but the page shows the card as answered on.
			var commitment interface{}
			switch {
			case block["record_type"] == "approval":
				commitment = body["card_commitment"]
			case record["type"] == dealprofile.TypeActionApproval:
				commitment = body["rendering_commitment"]
			}
			if present, ok := opens(opening, commitment); !present || !ok || seen[step] {
				where := "card " + strconv.Itoa(i+1)
				if record != nil {
					where += ", step " + step
				}
				return nil, errors.New("refusing to write the page: a card you answered on does not match the commitment sealed with your answer (" + where + ")")
			}
			seen[step] = true
		}
	}

	// Who the deal was with and what the buyer wrote (evidencebook-deal#56),
	// the user's own copy only, reported as counterparty and acceptances, one
	// result per opening in order. A shared copy carrying either, or the deal
	// key, refuses the page. An opening that does not recompute against its
	// step's sealed fingerprint or content_commitment is reported as not
	// verified and its value is never shown; one that names no step of this
	// bundle is not verified either.
	cpRaw, hasCP := report["counterparty_openings"]
	accRaw, hasAcc := report["acceptance_openings"]
	if hasCP || hasAcc {
		if audience == cli.DealAudienceCounterparty || audience == cli.DealAudienceAdjudicator {
			return nil, errors.New("refusing to write the page: a shared copy carries who the deal was with or what the buyer wrote, which only your own copy may")
		}
	}
	// The results line up with the openings, entry by entry: a step named
	// twice, or none, verifies no entry for it.
	if hasCP {
		cp, _ := cpRaw.(map[string]interface{})
		key, _ := hex.DecodeString(strOf(cp["key"]))
		steps, _ := cp["steps"].([]interface{})
		checked := make([]interface{}, len(steps))
		seen := map[string]bool{}
		for i, e := range steps {
			entry, _ := e.(map[string]interface{})
			step, _ := entry["step"].(string)
			ids := recordCounterpartyIDs(input(step))
			kinds := map[string]interface{}{}
			for _, kind := range counterpartyOpeningKinds {
				value, present := entry[kind].(string)
				if !present {
					continue
				}
				want, _ := ids[kind].(string)
				got, err := dealprofile.Fingerprint(key, kind, value)
				kinds[kind] = len(key) > 0 && want != "" && err == nil && got == want && !seen[step+"\x00"+kind]
				seen[step+"\x00"+kind] = true
			}
			checked[i] = kinds
		}
		out["counterparty"] = checked
	}
	if hasAcc {
		list, _ := accRaw.([]interface{})
		checked := make([]interface{}, len(list))
		seen := map[string]bool{}
		for i, a := range list {
			opening, _ := a.(map[string]interface{})
			step, _ := opening["step"].(string)
			body, _ := input(step)["body"].(map[string]interface{})
			var commitment interface{}
			if body["kind"] == counterpartyAcceptanceKind {
				commitment = body["content_commitment"]
			}
			present, ok := opens(opening, commitment)
			checked[i] = present && ok && !seen[step]
			seen[step] = true
		}
		out["acceptances"] = checked
	}

	said, _ := report["representations"].([]interface{})
	checked := make([]interface{}, len(said))
	for i, r := range said {
		checked[i] = false
		rep, _ := r.(map[string]interface{})
		step, _ := rep["step"].(string)
		body, _ := input(step)["body"].(map[string]interface{})
		claim := body
		if index, ok := rep["index"].(json.Number); ok {
			n, err := index.Int64()
			claims, _ := body["claims"].([]interface{})
			if err != nil || n < 0 || n >= int64(len(claims)) {
				continue
			}
			claim, _ = claims[n].(map[string]interface{})
		}
		if claim == nil || claim["source_kind"] != "agent" {
			continue
		}
		present, ok := opens(rep, claim["text_commitment"])
		if present && !ok {
			return nil, errors.New("refusing to write the page: a statement the agent made does not match the commitment sealed when it was made (step " + step + ")")
		}
		checked[i] = ok
	}
	out["representations"] = checked
	return out, nil
}

func strOf(v interface{}) string {
	s, _ := v.(string)
	return s
}
