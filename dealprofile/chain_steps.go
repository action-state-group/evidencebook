// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"slices"
	"strings"
)

// step is the rule of one record's type, the second half of the reference
// checker's check_chain loop body (§6): c.i is the record, c.b its block,
// c.body its body, c.byRel the indexes its refs name.
func (c *dealChain) step(typed bool) {
	i, b, body, t, records := c.i, c.b, c.body, c.t, c.docs
	byRelHas := func(rel string) bool { _, ok := c.byRel[rel]; return ok }
	asOne := func(rel string, types ...string) int { return c.one(rel, types, true) }
	asOpt := func(rel string, types ...string) int { return c.one(rel, types, false) }

	switch {
	case t == "task_authority":
		// The user's task authority: as asked at the baseline, or a new version
		// the user confirmed in their words (previous_ref names the one replaced).
		if asOpt("source", TypeBaseline) >= 0 {
			if i != 1 {
				c.fail("the opening task authority directly follows the baseline")
			}
			intent0 := mp(c.bodyOf(0)["intent"])
			for _, k := range []string{"max_total_minor", "allowed", "ordered_representations"} {
				if !same(body[k], intent0[k]) {
					c.fail("the task authority's %s is not the one asked at the baseline", k)
				}
			}
			if has(body, "bounds_commitment") != has(intent0, "bounds_commitment") {
				c.fail("the task authority states a floor exactly when the baseline does")
			}
		} else {
			j := asOne("approves", TypeIntent)
			prop := c.bodyOf(j)
			if c.confirmed[j] || c.hasIntentBetween(j) {
				c.fail("a proposal replaced by a later intent, or already confirmed, gives no new task authority")
			}
			if mp(body["previous_ref"])["digest"] != c.digests[c.taskIdx] {
				c.fail("previous_ref is not the task authority in force")
			}
			keys := []string{"max_total_minor", "allowed", "ordered_representations"}
			inForce := obj{}
			for _, kv := range []struct {
				k string
				v any
			}{{"max_total_minor", c.maxTotal}, {"allowed", c.allowed}, {"ordered_representations", c.ordered}} {
				if kv.v != nil {
					inForce[kv.k] = kv.v
				}
			}
			next := obj{}
			for k, v := range inForce {
				next[k] = v
			}
			for _, k := range keys {
				if prop[k] != nil && (k != "ordered_representations" || c.ordered != nil) {
					next[k] = prop[k]
				}
			}
			got := obj{}
			for _, k := range keys {
				if v, ok := body[k]; ok {
					got[k] = v
				}
			}
			if !same(got, next) {
				c.fail("the new task authority is not what the proposal asked for")
			}
			if has(prop, "bounds_commitment") && !same(body["bounds_commitment"], prop["bounds_commitment"]) {
				c.fail("the new task authority's floor is not the one the proposal stated")
			}
			more := (prop["max_total_minor"] != nil && c.maxTotal != nil && num(prop["max_total_minor"]) > num(c.maxTotal)) ||
				(prop["allowed"] != nil && c.allowed != nil && anyNotIn(prop["allowed"], c.allowed)) ||
				(prop["ordered_representations"] != nil && c.ordered != nil && !same(prop["ordered_representations"], c.ordered))
			if !more {
				c.fail("the proposal asks for no higher limit and no new action: nothing to confirm")
			}
			c.confirmed[j] = true
			c.maxTotal, c.allowed = next["max_total_minor"], next["allowed"]
			if c.ordered != nil {
				c.ordered = next["ordered_representations"]
			}
		}
		c.taskIdx = i
	case t == "platform_approval":
		// An observation of another platform's approval interaction for a proposed
		// action: listed beside that action's evaluation, never as its answer.
		j, ok := c.byDig[str(mp(body["proposed_action_ref"])["digest"])]
		if !ok || j >= i || c.rt(j) != TypeCheck {
			c.fail("proposed_action_ref names no earlier proposed action of this chain")
		}
		jv := -1
		for k := j + 1; k < i; k++ {
			if c.rt(k) == TypeVerdict && mp(sl(block(records[k])["refs"])[0])["digest"] == c.digests[j] {
				jv = k
				break
			}
		}
		if jv < 0 {
			c.fail("a platform approval observation follows the evaluation of its proposed action")
		}
		if str(body["observed_at"]) > str(b["at"]) {
			c.fail("observed_at is later than the record")
		}
		c.platformFor[jv] = append(c.platformFor[jv], i)
	case t == TypeCheck && body["action"] == "offer":
		if c.partyRole != "seller" {
			c.fail("an offer is made on a deal where the user sells (party_role seller)")
		}
		j := c.one("supersedes", []string{TypeCheck}, c.latestOffer >= 0)
		if j != c.latestOffer {
			c.fail("an offer supersedes exactly the latest earlier offer")
		}
		// The proposal it answers: one message the counterparty sent, or,
		// when the counterparty sent none before it, no_proposal; never both.
		j = asOpt("source", TypeMessage)
		if j >= 0 && c.bodyOf(j)["from"] != "counterparty" {
			c.fail("an offer answers a message from the counterparty (source)")
		}
		if body["no_proposal"] == true {
			if j >= 0 {
				c.fail("an offer carrying no_proposal answers no message: it has no source ref")
			}
			for k := 0; k < i; k++ {
				if c.rt(k) == TypeMessage && c.bodyOf(k)["from"] == "counterparty" {
					c.fail("no_proposal on an offer after a message from the counterparty")
				}
			}
		}
		c.latestOffer, c.accepted = i, -1
	case t == TypeCheck:
		if byRelHas("supersedes") {
			c.fail("only an offer supersedes an earlier offer")
		}
		if byRelHas("source") || has(body, "no_proposal") {
			c.fail("only an offer answers a message (source) or carries no_proposal")
		}
		if body["action"] == "represent" && !c.repRules {
			c.fail("a represent check is made on a deal where the user sells, opened with ordered_representations")
		}
	case t == "counterparty_acceptance":
		// An observation that the counterparty accepted one exact offer. It
		// authorizes nothing by itself; a seller's commit rests on it.
		j, ok := c.byDig[str(mp(body["proposed_action_ref"])["digest"])]
		if !ok || j >= i || c.rt(j) != TypeCheck || c.bodyOf(j)["action"] != "offer" {
			c.fail("proposed_action_ref names no earlier offer of this chain")
		}
		if j != c.latestOffer {
			c.fail("a later offer superseded that one: only the latest offer can be accepted")
		}
		made := false
		for k := j + 1; k < i && !made; k++ {
			if c.rt(k) != TypeAction || c.bodyOf(k)["action"] != "offer" {
				continue
			}
			for _, r := range sl(block(records[k])["refs"]) {
				if rm := mp(r); rm["rel"] == "authorized_by" && c.offerCheckOf(c.byDig[str(rm["digest"])]) == j {
					made = true
				}
			}
		}
		if !made {
			c.fail("that offer was checked but never made: the offer action comes before its acceptance")
		}
		if str(body["observed_at"]) > str(b["at"]) {
			c.fail("observed_at is later than the record")
		}
		c.accepted = i
	case t == "policy_change":
		// its bindings are structural (the schema); no chain step depends on it yet
	case t == TypeIntent:
		if pr, ok := body["party_role"]; ok && pr != c.partyRole {
			c.fail("an intent cannot change the deal's party_role (%s, set when it opened)", c.partyRole)
		}
		// An intent narrows the limits in force; asking for more is a
		// proposal that applies only once the user confirms it.
		if body["max_total_minor"] != nil && (c.maxTotal == nil || num(body["max_total_minor"]) < num(c.maxTotal)) {
			c.maxTotal = body["max_total_minor"]
		}
		if body["bounds_commitment"] != nil && !same(body["bounds_commitment"], c.bounds) {
			c.boundsKnown = false
		}
		if body["allowed"] != nil {
			next := []any{}
			for _, a := range sl(body["allowed"]) {
				if c.allowed == nil || contains(c.allowed, a) {
					next = append(next, a)
				}
			}
			c.allowed = next
		}
		// Naming the item, or picking an option, takes the item back
		// from the agent's delegation.
		if mp(body["asked"])["item"] != nil || body["move"] == "picked" {
			next := []any{}
			for _, d := range c.delegated {
				if d != "item" {
					next = append(next, d)
				}
			}
			c.delegated = next
		}
	case t == TypeAgentMove:
		// The agent chooses an item only where the user's words handed
		// it the choice.
		if body["move"] == "chose" && !contains(c.delegated, "item") {
			c.fail("an agent's chose needs the user's delegation of the item in force")
		}
	case t == TypeCounterpartyProfile:
		// The payee keyed per profile, sealed right after the check it is
		// about: one per check.
		j := asOne("about", TypeCheck)
		if j != i-1 {
			c.fail("a counterparty_profile record follows the check it is about")
		}
		if c.profileForChk[j] {
			c.fail("a check has at most one counterparty_profile record")
		}
		c.profileForChk[j] = true
	case t == TypeEvidence:
		asOne("about", TypeClaim, TypeBaseline)
		asOpt("confirms", TypeClose)
		if has(body, "resolves_obligation") {
			j, ok := c.byDig[str(mp(body["resolves_obligation"])["digest"])]
			if !ok || j >= i || !has(c.bodyOf(j), "obligation") {
				c.fail("resolves_obligation must name an earlier record holding an obligation")
			}
		}
	case t == TypeClaim:
		if c.repRules && body["source_kind"] == "agent" && has(body, "text_commitment") {
			class := str(body["class"])
			if !has(body, "class") {
				c.fail("a seller's representation names its class")
			}
			if has(body, "authority_basis") == (body["unchecked"] == true) {
				c.fail("a seller's representation carries exactly one of authority_basis or unchecked")
			}
			if body["unchecked"] == true {
				if byRelHas("authorized_by") {
					c.fail("an unchecked representation names nothing that authorized it")
				}
			} else {
				jt := asOne("authorized_by", TypeApproval, TypeVerdict)
				jv, ja, chk := c.authorizeTyped(jt, "represent", "representation")
				c.verifyBasis(jv, ja, nil)
				rep := mp(c.bodyOf(chk)["representing"])
				if rep["class"] != body["class"] {
					c.fail("the representation's class differs from the one checked")
				}
				// Sealed under the check's own nonces: equal commitments
				// mean the words said are the words checked.
				if !same(rep["text_commitment"], body["text_commitment"]) || !same(rep["source_ref_commitment"], body["source_ref_commitment"]) {
					c.fail("the representation's words differ from the ones checked")
				}
				if tk, ok := c.taskAtVerdict[jv]; !ok || tk != c.taskIdx {
					c.fail("the task authority changed after the check: the representation needs a new check")
				}
				if ja < 0 && !contains(c.bodyOf(c.taskIdx)["ordered_representations"], body["class"]) {
					c.fail("a representation the task authority does not order needs the user's own approval")
				}
				// The user's own claim (capsule-engine#189): whatever the
				// task authority orders, only the user's own approval, in
				// their words, says the words are theirs.
				if rep["attributed_to"] == "user" && ja < 0 {
					c.fail("a representation attributed to the user needs the user's own approval")
				}
				c.stated[class] = true
			}
		} else if has(body, "authority_basis") || has(body, "unchecked") || byRelHas("authorized_by") {
			c.fail("only a seller's representation under the representation rules carries an authority")
		}
	case t == TypeDetailChange:
		asOpt("source", TypeMessage, TypeEvidence)
		kinds := map[string]bool{}
		for k := range mp(mp(b["counterparty"])["ids"]) {
			kinds[k] = true
		}
		want := map[string]bool{}
		for _, x := range sl(body["changed"]) {
			if s := str(x); slices.Contains(identifierKinds, s) {
				want[s] = true
			}
		}
		if !same(kinds, want) {
			c.fail("counterparty.ids kinds must equal the identifier kinds listed in changed")
		}
	case t == TypeVerdict:
		j := asOne("checks", TypeCheck)
		if c.verdictForChk[j] {
			c.fail("a check has at most one verdict")
		}
		c.verdictForChk[j] = true
		// Every check of a claim attributed to the user asks.
		if mp(c.bodyOf(j)["representing"])["attributed_to"] == "user" && body["result"] == "pass" {
			c.fail("a check of a claim attributed to the user asks; it never passes on its own")
		}
		// pack_id is optional (records sealed before v0.1.0-rc7 carry it); when
		// either side names one, the two must agree.
		if !same(body["pack_id"], c.bodyOf(j)["pack_id"]) {
			c.fail("verdict pack_id differs from the check's")
		}
		if typed {
			// The check contract on the evaluation: the ProposedAction it
			// evaluated and the task authority in force; its proposed-phase
			// basis is that authority.
			c.taskAtVerdict[i] = c.taskIdx
			if body["proposed_action_digest"] != c.digests[j] {
				c.fail("proposed_action_digest is not the digest of the proposed action this evaluation evaluates")
			}
			if mp(body["task_authority_ref"])["digest"] != c.digests[c.taskIdx] {
				c.fail("task_authority_ref is not the task authority in force at the evaluation")
			}
			if !same(body["authority_basis"], []any{obj{"type": "task_authority", "ref": body["task_authority_ref"]}}) {
				c.fail("the evaluation's authority_basis is its task_authority_ref")
			}
		}
	case t == TypeApproval && body["choice"] == "confirm_limits":
		c.confirmLimits(asOne("approves", TypeIntent))
	case t == TypeApproval:
		j := asOne("approves", TypeVerdict)
		if _, ok := c.firstAnswer[j]; !ok {
			c.firstAnswer[j] = i
		}
		v := c.bodyOf(j)
		if body["approver"] == "standing_intent" {
			chk := c.bodyOf(c.byDig[str(mp(sl(block(records[j])["refs"])[0])["digest"])])
			if v["result"] != "pass" {
				c.fail("a standing-intent approval needs a passing verdict; a pause needs the user's answer")
			}
			if c.allowed != nil && !contains(c.allowed, chk["action"]) {
				c.fail("standing-intent approval for an action the intent does not allow")
			}
		} else if r := v["result"]; r == "pause" || r == "deny" {
			if !contains(v["options"], body["choice"]) {
				c.fail("the user's choice is not one of the verdict's options")
			}
		}
		// The card the answer was given on is committed under the verdict's own card nonce,
		// so equal commitments mean the card shown is the card checked.
		if has(body, "card_commitment") {
			if !has(v, "card_commitment") {
				c.fail("the approval commits to a shown card, but the verdict it answers rendered none")
			} else if !same(body["card_commitment"], v["card_commitment"]) {
				c.fail("the card shown is not the card checked: the approval's card_commitment differs from the verdict's")
			}
		}
		if typed && has(body, "rendering_commitment") && !same(body["rendering_commitment"], v["card_commitment"]) {
			c.fail("what was shown (rendering_commitment) is not what the evaluation rendered")
		}
	case t == TypeDisclosure:
		actions := map[string]bool{}
		for _, f := range sl(body["fields"]) {
			actions[disclosureAction[str(mp(f)["class"])]] = true
		}
		if len(actions) != 1 {
			c.fail("contact details and credentials are separate disclosures, each covered by its own check")
		}
		if body["authority"] == "none" {
			if byRelHas("authorized_by") {
				c.fail("a disclosure with authority none carries no authorized_by")
			}
		} else {
			if !byRelHas("authorized_by") {
				c.fail("a disclosure with authority approval references that approval (authorized_by)")
			}
			var action string
			for a := range actions {
				action = a
			}
			c.authorizedCheck(asOne("authorized_by", TypeApproval), action, "disclosure")
		}
	case t == TypeAction:
		c.action(typed)
	case t == TypeOutcome:
		asOpt("observes", TypeAction)
		if body["status"] == "unchecked_action" {
			c.unchecked++
		}
		if o := body["outcome"]; o == "not_selected" || o == "called_off" {
			for k := 0; k < i; k++ {
				if c.rt(k) == TypeAction {
					c.fail("%s means nothing was done on the deal, and an action is on record", str(o))
				}
			}
		}
		c.lastOutcome = i
	case t == TypeClose:
		if c.lastOutcome < 0 {
			if len(c.byRel["outcome"]) > 0 {
				c.fail("close references an outcome but none exists")
			}
			if body["outcome"] != "open" {
				c.fail("without an outcome record a close can only be open")
			}
		} else {
			j := asOne("outcome", TypeOutcome)
			if j != c.lastOutcome {
				c.fail("close must reference the latest outcome record")
			}
			if !same(body["outcome"], c.bodyOf(j)["outcome"]) {
				c.fail("close outcome differs from the referenced outcome")
			}
			for _, k := range []string{"actor", "reason"} {
				if !same(body[k], c.bodyOf(j)[k]) {
					c.fail("close %s differs from the referenced outcome's", k)
				}
			}
		}
		if num(body["unchecked_actions"]) != c.unchecked {
			c.fail("unchecked_actions must count unchecked_action outcomes (%d)", c.unchecked)
		}
		if body["outcome"] != "open" {
			c.closedFinal = i
		}
		for _, ob := range sl(body["carried_obligations"]) {
			cm := mp(ob)
			j, ok := c.byDig[str(mp(cm["obligation"])["digest"])]
			var held obj
			if ok {
				held = mp(c.bodyOf(j)["obligation"])
			}
			date := "cancel_by"
			if has(cm, "due_by") {
				date = "due_by"
			}
			if !ok || j >= i || !same(held[date], cm[date]) {
				c.fail("carried_obligations must name earlier records holding that date")
			}
		}
	}
}

func anyNotIn(list, in any) bool {
	for _, a := range sl(list) {
		if !contains(in, a) {
			return true
		}
	}
	return false
}

func (c *dealChain) confirmLimits(j int) {
	body := c.body
	prop := c.bodyOf(j)
	// A late answer (the proposal was replaced by a later intent, or is
	// already confirmed) is sealed as said but changes nothing.
	if c.confirmed[j] || c.hasIntentBetween(j) {
		if truthy(body["proceed"]) {
			c.fail("a proposal replaced by a later intent, or already confirmed, cannot be confirmed (proceed must be false)")
		}
		return
	}
	if !truthy(body["proceed"]) {
		c.fail("a confirmation of a standing proposal proceeds")
	}
	inForce := c.limitsInForce()
	limits := mp(body["limits"])
	if !same(c.withoutUnknownBounds(limits["previous"]), any(inForce)) {
		c.fail("limits.previous is not the limits in force")
	}
	next := obj{}
	for k, v := range inForce {
		next[k] = v
	}
	for _, k := range []string{"max_total_minor", "allowed", "bounds_commitment"} {
		if prop[k] != nil {
			next[k] = prop[k]
		}
	}
	var added []any
	for _, d := range sl(prop["delegated"]) {
		if !contains(c.delegated, d) {
			added = append(added, d)
		}
	}
	if len(added) > 0 {
		next["delegated"] = append(append([]any{}, c.delegated...), added...)
	}
	var got any = limits["new"]
	if !has(prop, "bounds_commitment") {
		got = c.withoutUnknownBounds(limits["new"])
	}
	if !same(got, any(next)) {
		c.fail("limits.new is not what the intent proposed")
	}
	// A floor cannot be compared (only its commitment is sealed): a proposal that states a
	// floor other than the one in force may be asking for a lower one, so it needs confirming.
	more := (prop["max_total_minor"] != nil && c.maxTotal != nil && num(prop["max_total_minor"]) > num(c.maxTotal)) ||
		(prop["allowed"] != nil && c.allowed != nil && anyNotIn(prop["allowed"], c.allowed)) ||
		(prop["bounds_commitment"] != nil && !same(prop["bounds_commitment"], c.bounds)) ||
		len(added) > 0
	if !more {
		c.fail("the intent asks for no higher limit and no new action: nothing to confirm")
	}
	c.confirmed[j] = true
	c.maxTotal, c.allowed = next["max_total_minor"], next["allowed"]
	c.delegated, _ = next["delegated"].([]any)
	c.bounds, c.boundsKnown = mp(limits["new"])["bounds_commitment"], true
}

func (c *dealChain) action(typed bool) {
	b, body := c.b, c.body
	has2 := func(rel string) bool { _, ok := c.byRel[rel]; return ok }
	if !has2("authorized_by") {
		c.fail("an action must reference the sealed approval that authorized it (authorized_by)")
	}
	if !typed && has2("source") {
		c.fail("only a seller's commit or address cites an acceptance (source)")
	}
	var chk int
	if typed {
		var jv, ja int
		jv, ja, chk = c.authorizeTyped(c.one("authorized_by", []string{TypeApproval, TypeVerdict}, true), str(body["action"]), "action")
		c.verifyBasis(jv, ja, body["amount_minor"])
		if c.repRules && slices.Contains(representationGated, str(body["action"])) {
			var missing []string
			for _, o := range sl(c.ordered) {
				if !c.stated[str(o)] {
					missing = append(missing, str(o))
				}
			}
			if len(missing) > 0 {
				c.fail("the user ordered the agent to tell the buyer about %s first: "+
					"no offer, commit, sign, pay or share goes ahead before it", strings.Join(missing, ", "))
			}
		}
		if has(body, "disclosed") {
			actions := map[string]bool{}
			for _, f := range sl(mp(body["disclosed"])["fields"]) {
				actions[disclosureAction[str(mp(f)["class"])]] = true
			}
			if !same(actions, map[string]bool{str(body["action"]): true}) {
				c.fail("the disclosed classes are not the ones this action covers")
			}
		}
		addr := false
		for _, f := range sl(mp(body["disclosed"])["fields"]) {
			addr = addr || slices.Contains(addressClasses, str(mp(f)["class"]))
		}
		if c.partyRole == "seller" && body["action"] == "commit" {
			c.sellerNeedsAcceptance("commit")
			// On exactly the accepted offer's terms: its amount and currency,
			// terms and recourse, as the two checks seal them.
			offered, committed := c.bodyOf(c.latestOffer), c.bodyOf(chk)
			for _, k := range offerTerms {
				if !same(offered[k], committed[k]) {
					c.fail("the commit's terms differ from the offer the other party accepted (changed_after_acceptance)")
				}
			}
		} else if c.partyRole == "seller" && addr {
			c.sellerNeedsAcceptance("address")
		} else if has2("source") {
			c.fail("only a seller's commit or address cites an acceptance (source)")
		}
	} else {
		chk = c.authorizedCheck(c.one("authorized_by", []string{TypeApproval}, true), str(body["action"]), "action")
	}
	cb := c.bodyOf(chk)
	if has(body, "amount_minor") && has(cb, "amount_minor") && !same(body["amount_minor"], cb["amount_minor"]) {
		c.fail("the amount differs from the one checked")
	}
	if has(body, "cancelled_amount_minor") && has(cb, "cancelled_amount_minor") && !same(body["cancelled_amount_minor"], cb["cancelled_amount_minor"]) {
		c.fail("the cancelled amount differs from the one checked")
	}
	if has(body, "currency") && has(cb, "currency") && !same(body["currency"], cb["currency"]) {
		c.fail("the currency differs from the one checked")
	}
	crail := mp(cb["recourse"])["rail"]
	if has(body, "rail") && truthy(crail) && !same(body["rail"], crail) {
		c.fail("the payment rail differs from the one checked")
	}
	pAct := mp(mp(b["counterparty"])["ids"])["payee"]
	pChk := mp(mp(block(c.docs[chk])["counterparty"])["ids"])["payee"]
	if truthy(pAct) && truthy(pChk) && !same(pAct, pChk) {
		c.fail("the payee fingerprint differs from the one checked")
	}
	// Section 6, rule 8: an action that returns money names the pay it
	// reverses, once, with the same amount and currency.
	if j := c.one("reverses", []string{TypeAction}, false); j >= 0 {
		undone := c.bodyOf(j)
		dir, ok := undone["direction"]
		if !ok {
			dir = "out"
		}
		if undone["action"] != "pay" || dir != "out" {
			c.fail("reverses must name an earlier pay action")
		}
		if body["direction"] != "in" {
			c.fail("an action that reverses a payment carries direction in")
		}
		if !same(body["amount_minor"], undone["amount_minor"]) || !same(body["currency"], undone["currency"]) {
			c.fail("a reversal returns the amount and currency of the pay it reverses")
		}
		if c.reversed[j] {
			c.fail("a payment is reversed at most once")
		}
		c.reversed[j] = true
	} else if body["direction"] == "in" && has(body, "amount_minor") {
		// A cancel that returns no sealed payment carries direction in
		// with its amount as cancelled_amount_minor: nothing returned,
		// nothing to name.
		c.fail("an action with direction in names the pay it reverses (reverses)")
	}
}
