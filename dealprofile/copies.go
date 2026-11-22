// SPDX-License-Identifier: Apache-2.0

package dealprofile

// CounterpartyWithheld returns the positions (0-based, in seq order) of the
// records that PROFILE.md section 6 rule 12 "Copies" withholds from a
// counterparty copy of a deal under that rule (evidencebook#607,
// evidencebook#613). It is the reference a producer's copy is tested
// against. It gives the rule 12 part only: other copy rules withhold more
// (a message with private details, the holder's own note, a rules verdict
// the holder does not share), so a copy withholds at least these records,
// and it discloses the records RequiredDisclosed names.
//
// A deal is under rule 12 when the user sells and its baseline intent
// carries ordered_representations. For any other deal, and for a sale's own
// log, the result is empty. Withholding is by kind, uniformly across the
// deal, never by a record's result or answer:
//   - every claim_authority;
//   - every represent check (a proposed-action/v0 with representing), and
//     every share check (share_contact, share_credentials), whose rule inputs
//     count only tellings given with authority;
//   - every evaluation (action-evaluation/v0);
//   - every answer to a check (an action-approval/v0 whose authority is
//     user_approval or card_answer), whatever the check or the answer; the
//     counterparty_acceptance and platform_approval observations and the
//     user's limit confirmations answer no check and stay;
//   - every outcome with status unchecked_action;
//   - every telling: a disclosure, and an action-record/v0 that discloses;
//   - every close, whatever its unchecked_actions;
//   - every agent claim that carries its own verdict (sealed before #607).
//
// A deal where the user sells is sealed in the typed action records
// (CheckChain refuses one that is not), so its checks, evaluations,
// approvals, acts and outcomes are typed records.
//
// Each record is the record's JSON text ([]byte or json.RawMessage) or its
// decoded JSON, as for CheckChain, which the records should already pass.
func CounterpartyWithheld(records []any) ([]int, error) {
	docs, err := readableDocs(records)
	if err != nil || !underRule12(docs) {
		return nil, err
	}
	var out []int
	for i, rec := range docs {
		if counterpartyWithholds(rec) {
			out = append(out, i)
		}
	}
	return out, nil
}

// RequiredDisclosed returns the positions of the records that rule 12
// "Copies" requires a counterparty copy of a deal under that rule to
// disclose: every agent claim sealed in the new shape (no verdict of its
// own), whose words the copy opens where they hold none of the user's
// private details; the outcome each close refs when its status is not
// unchecked_action, from which the copy's report says how the deal ended;
// and every terms_pack, from which it names the terms that bind.
// Empty for a deal not under rule 12.
func RequiredDisclosed(records []any) ([]int, error) {
	docs, err := readableDocs(records)
	if err != nil || !underRule12(docs) {
		return nil, err
	}
	byDig := map[string]int{}
	for i, r := range records {
		d, err := Digest(r)
		if err != nil {
			return nil, err
		}
		byDig[d] = i
	}
	need := map[int]bool{}
	for i, rec := range docs {
		b, body := block(rec), mp(rec["body"])
		switch b["record_type"] {
		case TypeClaim:
			if body["source_kind"] == "agent" && !claimCarriesVerdict(body) {
				need[i] = true
			}
		case TypeTermsPack:
			need[i] = true
		case TypeClose:
			for _, r := range sl(b["refs"]) {
				if rm := mp(r); rm["rel"] == "outcome" {
					if j, ok := byDig[str(rm["digest"])]; ok && mp(docs[j]["body"])["status"] != "unchecked_action" {
						need[j] = true
					}
				}
			}
		}
	}
	out := make([]int, 0, len(need))
	for i := range docs {
		if need[i] {
			out = append(out, i)
		}
	}
	return out, nil
}

func readableDocs(records []any) ([]obj, error) {
	docs := make([]obj, len(records))
	for i, r := range records {
		d, err := decode(r)
		if err != nil {
			return nil, err
		}
		m, ok := d.(map[string]any)
		if !ok {
			return nil, stageErr(StageChain, "seq position %d: a record is a JSON object", i+1)
		}
		docs[i] = readable(m)
	}
	return docs, nil
}

// underRule12 is a deal whose baseline intent says the user sells and orders
// representations (an empty list included).
func underRule12(docs []obj) bool {
	if len(docs) == 0 || block(docs[0])["record_type"] != TypeBaseline {
		return false
	}
	intent := mp(mp(docs[0]["body"])["intent"])
	return intent["party_role"] == "seller" && intent["ordered_representations"] != nil
}

func claimCarriesVerdict(body obj) bool {
	return has(body, "authority_basis") || has(body, "unchecked")
}

func counterpartyWithholds(rec obj) bool {
	b, body := block(rec), mp(rec["body"])
	if b["typed"] == TypeActionApproval {
		a := body["authority"]
		return a == "user_approval" || a == "card_answer"
	}
	switch b["record_type"] {
	case TypeClaimAuthority, TypeVerdict, TypeClose, TypeDisclosure:
		return true
	case TypeCheck:
		// A represent check is the represent trail; a share check's rule
		// inputs (repeat_disclosures, recipient_told_before) count only
		// tellings given with authority, so they show whether an earlier one was.
		a := body["action"]
		return a == "represent" || a == "share_contact" || a == "share_credentials"
	case TypeAction:
		return has(body, "disclosed")
	case TypeOutcome:
		return body["status"] == "unchecked_action"
	case TypeClaim:
		return body["source_kind"] == "agent" && claimCarriesVerdict(body)
	}
	return false
}
