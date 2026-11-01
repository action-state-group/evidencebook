// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

// StageChain is the stage that reads a record against the records before it
// (PROFILE.md §6).
const StageChain = "chain"

// CheckChain runs the §6 rules over one deal's records in seq order, as the
// reference checker's check_chain does (x-deal-v0/check_profile.py): the
// same refusals, in the same order, with the same text. Each record is the
// record's JSON text ([]byte or json.RawMessage) or its decoded JSON, and
// has already passed Check. A deal's chain is the baseline and the records
// after it; a sale's own log opens with its root. The refusal is a
// *StageError at StageChain, whose Detail begins "seq position N:".
func CheckChain(records []any) (err error) {
	docs := make([]map[string]any, len(records))
	for i, r := range records {
		d, derr := decode(r)
		if derr != nil {
			return derr
		}
		m, ok := d.(map[string]any)
		if !ok {
			return stageErr(StageChain, "seq position %d: a record is a JSON object", i+1)
		}
		docs[i] = m
	}
	defer func() {
		switch p := recover().(type) {
		case nil:
		case chainFail:
			err = &StageError{Stage: StageChain, Detail: string(p)}
		case error, string:
			// A record the record-level stages refuse (a missing member, a
			// member of another type) is not one the rules can read.
			err = stageErr(StageChain, "a record the chain rules cannot read: %v", p)
		default:
			panic(p)
		}
	}()
	if len(docs) > 0 && !isTyped(docs[0]) && block(docs[0])["record_type"] == TypeSale {
		checkSaleChain(docs)
	} else {
		checkDealChain(docs)
	}
	return nil
}

// chainFail is the panic a rule's refusal raises, recovered in CheckChain.
type chainFail string

func failAt(i int, format string, args ...any) {
	panic(chainFail(fmt.Sprintf("seq position %d: ", i+1) + fmt.Sprintf(format, args...)))
}

// The accessors read generic JSON as the checker's dicts read it: an absent
// member and a null are alike, a member of another type reads as absent.
type obj = map[string]any

func block(r obj) obj { return mp(r[Profile]) }

func mp(v any) obj {
	m, _ := v.(map[string]any)
	return m
}

func sl(v any) []any {
	a, _ := v.([]any)
	return a
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func has(m obj, k string) bool { _, ok := m[k]; return ok }

func same(a, b any) bool { return reflect.DeepEqual(a, b) }

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	}
	return true
}

func num(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			panic(fmt.Sprintf("%v is not an integer", x))
		}
		return n
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	}
	panic(fmt.Sprintf("%v is not a number", v))
}

func contains(list any, v any) bool {
	for _, x := range sl(list) {
		if same(x, v) {
			return true
		}
	}
	return false
}

func ref(d string) obj {
	return obj{"type": "deal-record", "digest_alg": "SHA-256", "digest": d}
}

func digestOf(r obj) string {
	d, err := Digest(r)
	if err != nil {
		panic(err)
	}
	return d
}

func indexOf(list []string, d string) int {
	i := slices.Index(list, d)
	if i < 0 {
		panic("a ref names no record")
	}
	return i
}

// viewOf is a typed record as the §6 rules read it (the reference checker's
// typed_view): its header as a block, and its body under the names those
// rules use. Digests are always computed over the record itself.
func viewOf(rec obj) obj {
	t := str(rec["type"])
	body := obj{}
	for k, v := range mp(rec["body"]) {
		body[k] = v
	}
	blk := obj{"profile": t, "canonicalization": rec["canonicalization"], "deal_id": rec["chain_id"],
		"record_type": typedActionTypes[t], "seq": rec["seq"], "at": rec["at"], "typed": t}
	if p := mp(rec["prev"]); p != nil {
		blk["prev"] = ref(str(p["digest"]))
	}
	if p := mp(rec["chain_root"]); p != nil {
		blk["baseline_ref"] = ref(str(p["digest"]))
	}
	if refs, ok := rec["refs"]; ok {
		var out []any
		for _, r := range sl(refs) {
			rm := mp(r)
			x := ref(str(rm["digest"]))
			x["rel"] = rm["rel"]
			out = append(out, x)
		}
		blk["refs"] = out
	}
	if cp, ok := body["counterparty"]; ok {
		blk["counterparty"] = cp
		delete(body, "counterparty")
	}
	switch t {
	case TypeActionEvaluation:
		body["result"] = map[string]any{"DO": "pass", "ASK": "pause", "DENY": "deny"}[str(body["disposition"])]
		body["differences"] = body["findings"]
		if v, ok := body["rendering_commitment"]; ok {
			body["card_commitment"] = v
		}
	case TypeActionApproval:
		switch a := str(body["authority"]); a {
		case "user_approval":
			body["approver"] = "user"
		case "card_answer":
			body["approver"] = "agent_card"
		default:
			blk["record_type"] = a // platform_approval, policy_change, one_shot_override
		}
	case TypeActionOutcome:
		body["differences"] = body["findings"]
		if v, ok := body["attempted"]; ok {
			body["unchecked"] = v
		}
	}
	return obj{Profile: blk, "body": body}
}

func readable(r obj) obj {
	if isTyped(r) {
		return viewOf(r)
	}
	return r
}

var disclosureAction = func() map[string]string {
	m := map[string]string{}
	for _, c := range []string{"name", "phone", "email", "home_address", "address", "pickup_location", "pickup_area", "other_contact"} {
		m[c] = "share_contact"
	}
	for _, c := range []string{"credential", "verification_code", "payment_card", "id_document"} {
		m[c] = "share_credentials"
	}
	return m
}()

var (
	offerTerms          = []string{"amount_minor", "currency", "terms", "recourse"}
	handovers           = []string{"in_person", "shipped"}
	addressClasses      = []string{"home_address", "address", "pickup_location"}
	representationGated = []string{"offer", "commit", "sign", "pay", "share_contact", "share_credentials"}
	identifierKinds     = []string{"payee", "name", "domain", "phone", "email", "relay_address", "profile_id"}
)

// checkHandover is terms.handover (§6 rule 14) on one record: a member of
// the set, on a deal where the user sells, and only in the terms of an offer
// or commit check, a baseline or a sale. It returns the refusal, or "".
func checkHandover(rec obj, partyRole string) string {
	b, body := block(rec), mp(rec["body"])
	t := str(b["record_type"])
	intent := mp(body["intent"])
	statedHere := t == TypeBaseline || t == TypeSale || (t == TypeCheck && (body["action"] == "offer" || body["action"] == "commit"))
	type place struct {
		allowed bool
		terms   any
	}
	places := []place{{statedHere, body["terms"]}, {false, intent["asked"]}}
	switch t {
	case TypeIntent:
		places = []place{{false, body["asked"]}, {false, body["terms"]}}
	case TypeDetailChange, TypeOutcome:
		places = []place{{false, body["terms"]}, {false, body["delivered"]}}
	}
	for _, p := range places {
		terms := mp(p.terms)
		if !has(terms, "handover") {
			continue
		}
		if h, ok := terms["handover"].(string); !ok || !slices.Contains(handovers, h) {
			return "terms.handover is in_person or shipped"
		}
		if partyRole != "seller" {
			return "terms.handover is a seller's term: a deal where the user buys states none"
		}
		if !p.allowed {
			return "terms.handover goes on an offer or commit check or the opening, and nowhere else"
		}
	}
	return ""
}

// validUTC reads a "2006-01-02T15:04:05Z" time as Python's strptime does for
// the profile's pattern: a real date and time of day.
func validUTC(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	return t, err == nil && len(s) == 20
}

func checkSaleChain(docs []obj) {
	fail := func(i int, format string, args ...any) { failAt(i, format, args...) }
	if len(docs) < 2 {
		fail(0, "a sale's log holds its root and its one task authority, then its thread registrations")
	}
	root, ta := docs[0], docs[1]
	blk := block(root)
	if num(blk["seq"]) != 1 {
		fail(0, "a sale's root is the first record of its log")
	}
	rootIntent := mp(mp(root["body"])["intent"])
	if rootIntent["party_role"] != "seller" {
		fail(0, "a sale is made by the seller (party_role seller)")
	}
	if why := checkHandover(root, "seller"); why != "" {
		fail(0, "%s", why)
	}
	if !isTyped(ta) || ta["type"] != TypeTaskAuthority {
		fail(1, "a sale's root is followed by its task authority (task-authority/v0)")
	}
	d0 := digestOf(root)
	if num(ta["seq"]) != 2 || mp(ta["prev"])["digest"] != d0 || mp(ta["chain_root"])["digest"] != d0 || ta["chain_id"] != blk["deal_id"] {
		fail(1, "the sale's task authority follows its root in the same log")
	}
	refs := sl(ta["refs"])
	if len(refs) != 1 || mp(refs[0])["rel"] != "source" || mp(refs[0])["digest"] != d0 {
		fail(1, "the sale's task authority names the sale's root as its source, and nothing else")
	}
	taBody := mp(ta["body"])
	if has(taBody, "sale_authority_commitment") {
		fail(1, "a sale's own task authority names no other sale")
	}
	if rootIntent["bounds_commitment"] != nil && !has(taBody, "bounds_commitment") {
		fail(1, "the sale's task authority carries the floor its root states")
	}
	saleTA := digestOf(ta)
	prev, at := saleTA, str(ta["at"])
	digests := make([]string, len(docs))
	for i, r := range docs {
		digests[i] = digestOf(r)
	}
	registrations, opened := map[string]bool{}, map[string]bool{}
	closed := false
	// A listing post (rule 15): every post check asks, and only the user's own
	// approval, first and unused, of the latest post check authorizes the act.
	kindAt := map[int]string{}
	firstAnswer := map[int]int{}
	used, stale := map[int]bool{}, map[int]bool{}
	latestVerdict, latestCheck := -1, -1
	for i := 2; i < len(docs); i++ {
		r := docs[i]
		var b, body obj
		var t string
		if isTyped(r) {
			view := viewOf(r)
			b, body = block(view), mp(view["body"])
			t = "post_" + str(b["record_type"])
		} else {
			b, body = block(r), mp(r["body"])
			t = str(b["record_type"])
		}
		if !slices.Contains([]string{"thread", "thread_opened", "sale_cut", "sale_close", "post_check", "post_verdict", "post_approval", "post_action"}, t) {
			fail(i, "after its task authority, a sale's log holds only thread registrations, their openings, cuts and its close, "+
				"and a listing post's check, evaluation, approval and action")
		}
		if closed && t != "sale_cut" {
			fail(i, "after a sale's close, its log holds only cuts")
		}
		closed = closed || t == "sale_close"
		if num(b["seq"]) != int64(i+1) || b["deal_id"] != blk["deal_id"] || !same(b["prev"], ref(prev)) ||
			!same(b["baseline_ref"], ref(d0)) || str(b["at"]) < at {
			fail(i, "a sale's record follows its previous record in the same log")
		}
		kindAt[i] = t

		refTo := func(rel, kind string) int {
			var js, others []obj
			for _, x := range sl(b["refs"]) {
				if xm := mp(x); xm["rel"] == rel {
					js = append(js, xm)
				} else {
					others = append(others, xm)
				}
			}
			j := -1
			if len(js) == 1 {
				if k := slices.Index(digests[:i], str(js[0]["digest"])); k >= 0 {
					j = k
				}
			}
			if len(others) > 0 || j < 0 || kindAt[j] != kind {
				fail(i, "a post's %s names exactly one earlier %s of this sale (%s)", t[5:], kind[5:], rel)
			}
			return j
		}

		switch t {
		case "post_check":
			if body["action"] != "post" {
				fail(i, "a sale's own log checks only a listing post (action post)")
			}
			if mp(body["task_authority_ref"])["digest"] != saleTA {
				fail(i, "a post check names the sale's one task authority")
			}
			// A newer proposal supersedes every earlier post check: no answer
			// sealed after it covers an earlier one (capsulectl's approvalChanged).
			latestVerdict, latestCheck = -1, i
		case "post_verdict":
			j := refTo("checks", "post_check")
			if j != latestCheck {
				fail(i, "a post's evaluation evaluates the post proposal sealed right before it, never an earlier one")
			}
			if body["result"] != "pause" {
				fail(i, "every post check asks: its evaluation is ASK")
			}
			if body["proposed_action_digest"] != digests[j] || mp(body["task_authority_ref"])["digest"] != saleTA {
				fail(i, "a post's evaluation names its check and the sale's one task authority")
			}
			if body["rules_status"] != "not_applicable" || has(body, "rules_checks") {
				fail(i, "no rules checker runs on a post: its evaluation says rules_status not_applicable")
			}
			latestVerdict = i
		case "post_approval":
			j := refTo("approves", "post_verdict")
			// What the user was shown is what the post check rendered, as on a
			// thread: the same rendering commitment, required on their own answer.
			shown := mp(r["body"])["rendering_commitment"]
			if (body["approver"] == "user" && shown == nil) || (shown != nil && !same(shown, mp(docs[j]["body"])["rendering_commitment"])) {
				fail(i, "what was shown (rendering_commitment) is not the card the post check rendered")
			}
			if j != latestVerdict {
				stale[i] = true
			}
			if _, ok := firstAnswer[j]; !ok {
				firstAnswer[j] = i
			}
		case "post_action":
			if body["action"] != "post" {
				fail(i, "a sale's own log takes only the post act")
			}
			ja := refTo("authorized_by", "post_approval")
			ab := mp(readable(docs[ja])["body"])
			jv := indexOf(digests, str(mp(sl(docs[ja]["refs"])[0])["digest"]))
			if ab["approver"] != "user" || !truthy(ab["proceed"]) {
				fail(i, "only the user's own approval, in their words, authorizes a post: an answer without the user's words does not")
			}
			if stale[ja] || jv != latestVerdict {
				fail(i, "a newer post check superseded the one this post rests on: show the user the newer card and post only after their yes")
			}
			if fa, ok := firstAnswer[jv]; !ok || fa != ja || used[ja] {
				fail(i, "a post rests on the first answer to its check, and an approval posts once")
			}
			used[ja] = true
			if until := str(mp(docs[jv]["body"])["valid_until"]); str(b["at"]) > until {
				fail(i, "the post check was valid until %s: a post after that needs a new check", until)
			}
			if mp(body["evaluation_ref"])["digest"] != digests[jv] {
				fail(i, "evaluation_ref is the post's evaluation")
			}
			basis := sl(body["authority_basis"])
			if len(basis) != 2 || mp(basis[0])["type"] != "task_authority" || mp(mp(basis[0])["ref"])["digest"] != saleTA ||
				mp(basis[1])["type"] != "user_approval" || mp(mp(basis[1])["ref"])["digest"] != digests[ja] {
				fail(i, "a post's authority_basis is the sale's task authority, then the user's approval")
			}
		case "thread_opened":
			reg := str(mp(sl(b["refs"])[0])["digest"])
			if !registrations[reg] || opened[reg] {
				fail(i, "a thread's opening names an earlier registration of this sale, once")
			}
			opened[reg] = true
		case "sale_cut":
			seen := map[string]bool{}
			dup, unopened := false, false
			for _, h := range sl(mp(r["body"])["thread_heads"]) {
				reg := str(mp(mp(h)["registration"])["digest"])
				dup = dup || seen[reg]
				seen[reg] = true
				unopened = unopened || !opened[reg]
			}
			if dup || unopened {
				fail(i, "a sale cut names each opened thread at most once, and only opened threads")
			}
		}
		prev, at = digests[i], str(b["at"])
		if t == "thread" {
			registrations[prev] = true
		}
	}
}

type dealChain struct {
	docs    []obj // the records as the rules read them
	digests []string
	byDig   map[string]int
	// state, as check_chain keeps it
	i             int
	typedChain    bool
	taskIdx       int
	taskAtVerdict map[int]int
	platformFor   map[int][]int
	partyRole     string
	allowed       any
	maxTotal      any
	delegated     []any
	bounds        any
	boundsKnown   bool
	ordered       any
	repRules      bool
	stated        map[string]bool
	confirmed     map[int]bool
	usedApprovals map[int]bool
	verdictForChk map[int]bool
	profileForChk map[int]bool
	reversed      map[int]bool
	firstAnswer   map[int]int
	lastOutcome   int
	unchecked     int64
	latestOffer   int
	accepted      int
	closedFinal   int
	b, body       obj
	t             string
	byRel         map[string][]int
}

var allowedRels = map[string][]string{
	"evidence": {"about", "confirms"}, "detail_change": {"source"}, "verdict": {"checks"},
	"approval": {"approves"}, "action": {"authorized_by", "reverses", "source"}, "outcome": {"observes"},
	"close": {"outcome"}, "disclosure": {"authorized_by"}, "claim": {"authorized_by"},
	"task_authority": {"source", "approves", "registration"}, "platform_approval": {}, "policy_change": {},
	"check": {"supersedes", "source"}, "counterparty_acceptance": {},
	"counterparty_profile": {"about"},
}

func (c *dealChain) fail(format string, args ...any) { failAt(c.i, format, args...) }

func (c *dealChain) rt(k int) string  { return str(block(c.docs[k])["record_type"]) }
func (c *dealChain) bodyOf(k int) obj { return mp(c.docs[k]["body"]) }

// one is the checker's `one`: the index of the record the rel names, at most
// (or exactly) one, of one of the types.
func (c *dealChain) one(rel string, types []string, required bool) int {
	js := c.byRel[rel]
	if len(js) > 1 || (required && len(js) == 0) {
		if required {
			c.fail("a %s record carries exactly one %s ref", c.t, pyRepr(rel))
		}
		c.fail("a %s record carries at most one %s ref", c.t, pyRepr(rel))
	}
	if len(js) > 0 && !slices.Contains(types, c.rt(js[0])) {
		c.fail("%s must point at a %s record", pyRepr(rel), strings.Join(types, " or "))
	}
	if len(js) == 0 {
		return -1
	}
	return js[0]
}

func (c *dealChain) offerCheckOf(k int) int {
	if c.rt(k) == TypeApproval {
		k = c.byDig[str(mp(sl(block(c.docs[k])["refs"])[0])["digest"])]
	}
	return c.byDig[str(mp(sl(block(c.docs[k])["refs"])[0])["digest"])]
}

// changesDetails is a record after which no earlier check covers a step
// (§6 rules 5 and 13).
func (c *dealChain) changesDetails(k int) bool {
	kb, kbody := block(c.docs[k]), c.bodyOf(k)
	tk := str(kb["record_type"])
	identity := (tk == TypeMessage || tk == TypeEvidence) && (has(kb, "counterparty") || has(kbody, "counterparty_facts"))
	leg := tk == TypeIntent && has(kb, "counterparty")
	pick := (tk == TypeIntent && has(kbody, "move") && has(mp(kbody["asked"]), "item")) ||
		(tk == TypeAgentMove && kbody["move"] == "chose")
	return tk == TypeDetailChange || identity || leg || pick
}

func (c *dealChain) sellerNeedsAcceptance(what string) {
	if c.latestOffer < 0 {
		c.fail("no offer is on record: a seller's %s rests on an offer the other party accepted", what)
	}
	if c.accepted < 0 {
		c.fail("the latest offer has no recorded acceptance: the %s needs one", what)
	}
	for k := c.accepted + 1; k < c.i; k++ {
		if c.changesDetails(k) {
			c.fail("details changed after the other party accepted; the %s needs the offer made and accepted again", what)
		}
	}
	if s := c.byRel["source"]; len(s) != 1 || s[0] != c.accepted {
		c.fail("a seller's %s cites the acceptance it rests on (source)", what)
	}
}

// authorizedCheck is §6 rule 5: the approval proceeds, is unused, is the
// verdict's first answer, and its check is of this action with no change of
// details since. It returns the check.
func (c *dealChain) authorizedCheck(ja int, action, what string) int {
	appr := c.bodyOf(ja)
	if appr["choice"] == "confirm_limits" {
		c.fail("a limits confirmation authorizes no action or disclosure; the step needs its own check")
	}
	if !truthy(appr["proceed"]) {
		c.fail("the referenced approval did not approve proceeding")
	}
	if c.usedApprovals[ja] {
		c.fail("an approval authorizes at most one action or disclosure")
	}
	c.usedApprovals[ja] = true
	jv := c.byDig[str(mp(sl(block(c.docs[ja])["refs"])[0])["digest"])]
	if fa, ok := c.firstAnswer[jv]; !ok || fa != ja {
		n := ""
		if what == "action" {
			n = "n"
		}
		c.fail("a%s %s is held to the verdict's first answer; a later answer needs a new check", n, what)
	}
	jc := c.byDig[str(mp(sl(block(c.docs[jv])["refs"])[0])["digest"])]
	if c.bodyOf(jc)["action"] != action {
		c.fail("the %s differs from the one checked", what)
	}
	for k := jc + 1; k < c.i; k++ {
		if c.changesDetails(k) {
			c.fail("details changed after the check; the %s needs a new check", what)
		}
	}
	return jc
}

// authorizeTyped is typed chains (§9): a DO evaluation authorizes the step
// on the task authority alone; an ASK is answered only by the user's own
// approval. It returns the evaluation, the approval (-1 for none) and the check.
func (c *dealChain) authorizeTyped(jt int, action, what string) (jv, ja, chk int) {
	if c.rt(jt) == TypeVerdict {
		if c.bodyOf(jt)["result"] != "pass" {
			c.fail("only a DO evaluation authorizes on its own; an ASK needs the user's own approval")
		}
		if c.usedApprovals[jt] {
			c.fail("an evaluation authorizes at most one action or disclosure")
		}
		c.usedApprovals[jt] = true
		jc := c.byDig[str(mp(sl(block(c.docs[jt])["refs"])[0])["digest"])]
		if c.bodyOf(jc)["action"] != action {
			c.fail("the %s differs from the one checked", what)
		}
		if c.allowed != nil && !contains(c.allowed, action) && action != "represent" {
			c.fail("the task authority does not allow this action")
		}
		for k := jc + 1; k < c.i; k++ {
			if c.changesDetails(k) {
				c.fail("details changed after the check; the %s needs a new check", what)
			}
		}
		return jt, -1, jc
	}
	if c.bodyOf(jt)["approver"] != "user" {
		c.fail("the deal check asked (it paused); an ask needs the user's own approval, and a card answer does not satisfy it")
	}
	chk = c.authorizedCheck(jt, action, what)
	return c.byDig[str(mp(sl(block(c.docs[jt])["refs"])[0])["digest"])], jt, chk
}

// verifyBasis is the executed phase of the check contract: the evaluation
// relied on still valid, and every authority layer in order, each a ref to
// its record.
func (c *dealChain) verifyBasis(jv, ja int, amount any) {
	v := c.bodyOf(jv)
	if until := str(v["valid_until"]); str(c.b["at"]) > until {
		c.fail("the evaluation was valid until %s: a step after that needs a new check", until)
	}
	if mp(c.body["evaluation_ref"])["digest"] != c.digests[jv] {
		c.fail("evaluation_ref is not the evaluation the step relied on")
	}
	basis := sl(c.body["authority_basis"])
	var types []string
	for _, e := range basis {
		types = append(types, str(mp(e)["type"]))
	}
	if slices.Contains(types, "one_shot_override") {
		c.fail("one_shot_override is reserved: no rule in this profile defines an override")
	}
	nTA := 0
	for _, x := range types {
		if x == "task_authority" {
			nTA++
		}
	}
	if len(types) == 0 || types[0] != "task_authority" || nTA != 1 {
		c.fail("authority_basis starts with exactly one task_authority")
	}
	if mp(mp(basis[0])["ref"])["digest"] != mp(v["task_authority_ref"])["digest"] {
		c.fail("task_authority is not the evaluation's task_authority_ref")
	}
	var asa []obj
	for _, e := range basis {
		if mp(e)["type"] == "user_approval" {
			asa = append(asa, mp(e))
		}
	}
	if ja >= 0 {
		if len(asa) != 1 || mp(asa[0]["ref"])["digest"] != c.digests[ja] {
			c.fail("the user_approval entry is the user's answer the step cites (authorized_by)")
		}
	} else if len(asa) > 0 {
		c.fail("a DO evaluation needs no approval: the step rests on the task authority")
	}
	var seen []int
	for _, e := range basis {
		em := mp(e)
		if em["type"] != "platform_approval" {
			continue
		}
		jp, ok := c.byDig[str(mp(em["ref"])["digest"])]
		if !ok || jp >= c.i || c.rt(jp) != "platform_approval" {
			c.fail("a platform_approval entry names an earlier platform approval")
		}
		pb := c.bodyOf(jp)
		mismatch := amount != nil && has(pb, "amount_minor") && !same(pb["amount_minor"], amount)
		if mismatch != (em["scope"] == "mismatch") {
			c.fail("scope mismatch is stated exactly when the platform's approval stated another amount")
		}
		seen = append(seen, jp)
	}
	want := slices.Clone(c.platformFor[jv])
	sort.Ints(seen)
	sort.Ints(want)
	if !slices.Equal(seen, want) {
		c.fail("authority_basis lists every platform approval observed for the evaluation, and only those")
	}
	wantTypes := []string{"task_authority"}
	if ja >= 0 {
		wantTypes = append(wantTypes, "user_approval")
	}
	for range seen {
		wantTypes = append(wantTypes, "platform_approval")
	}
	if !slices.Equal(types, wantTypes) {
		c.fail("authority_basis lists task_authority, then the user's answer, then platform approvals")
	}
}

func (c *dealChain) limitsInForce() obj {
	out := obj{}
	if c.maxTotal != nil {
		out["max_total_minor"] = c.maxTotal
	}
	if c.allowed != nil {
		out["allowed"] = c.allowed
	}
	if len(c.delegated) > 0 {
		out["delegated"] = c.delegated
	}
	if c.boundsKnown && c.bounds != nil {
		out["bounds_commitment"] = c.bounds
	}
	return out
}

func (c *dealChain) withoutUnknownBounds(d any) any {
	m := mp(d)
	if c.boundsKnown {
		return m
	}
	out := obj{}
	for k, v := range m {
		if k != "bounds_commitment" {
			out[k] = v
		}
	}
	return out
}

func (c *dealChain) hasIntentBetween(j int) bool {
	for k := j + 1; k < c.i; k++ {
		if c.rt(k) == TypeIntent {
			return true
		}
	}
	return false
}

func checkDealChain(raw []obj) {
	c := &dealChain{
		taskAtVerdict: map[int]int{}, platformFor: map[int][]int{}, confirmed: map[int]bool{}, usedApprovals: map[int]bool{},
		verdictForChk: map[int]bool{}, profileForChk: map[int]bool{}, reversed: map[int]bool{}, firstAnswer: map[int]int{},
		stated: map[string]bool{}, lastOutcome: -1, latestOffer: -1, accepted: -1, closedFinal: -1, boundsKnown: true,
		byDig: map[string]int{},
	}
	for i, r := range raw {
		d := digestOf(r)
		c.digests = append(c.digests, d)
		c.byDig[d] = i
		c.docs = append(c.docs, readable(r))
	}
	records := c.docs
	if len(records) == 0 {
		return
	}
	// A chain sealed in the typed action records opens with the baseline and then
	// the user's task authority; its authority steps are typed records.
	c.typedChain = len(records) > 1 && block(records[1])["typed"] == TypeTaskAuthority
	if c.typedChain {
		c.taskIdx = 1
	}
	blk0 := block(records[0])
	if blk0["record_type"] != TypeBaseline {
		failAt(0, "the first record of a deal must be the baseline")
	}
	intent0 := mp(mp(records[0]["body"])["intent"])
	c.allowed = intent0["allowed"]
	c.maxTotal = intent0["max_total_minor"]
	c.delegated = sl(intent0["delegated"])
	c.partyRole = "buyer"
	if pr, ok := intent0["party_role"]; ok {
		c.partyRole = str(pr)
	}
	if c.partyRole == "seller" && !c.typedChain {
		failAt(0, "a deal where the user sells is sealed in the typed action records")
	}
	c.bounds = intent0["bounds_commitment"]
	if c.partyRole == "seller" {
		c.ordered = intent0["ordered_representations"]
	}
	c.repRules = c.ordered != nil

	for i, rec := range records {
		c.i = i
		b, body := block(rec), mp(rec["body"])
		c.b, c.body = b, body
		t := str(b["record_type"])
		c.t = t
		typed := has(b, "typed")
		if typed && !c.typedChain {
			c.fail("a typed record in a chain that did not open with a task authority")
		}
		if t == TypeCheck && has(body, "listing_classes") && !has(c.bodyOf(c.taskIdx), "sale_authority_commitment") {
			c.fail("listing_classes is a rule input of a sale's thread only")
		}
		if (t == TypeCheck || t == TypeAction) && body["action"] == "post" {
			c.fail("a post is a step of a sale's own log (section 6, rule 15), never of a deal's chain")
		}
		if t == TypeVerdict && has(body, "rules_status") {
			c.fail("rules_status not_applicable is a sale's post evaluation only: a deal's checks state their rules_checks")
		}
		lateAnswer := t == TypeApproval && body["choice"] == "confirm_limits" && !truthy(body["proceed"])
		if c.typedChain && !typed && slices.Contains([]string{TypeCheck, TypeVerdict, TypeApproval, TypeAction, TypeOutcome}, t) && !lateAnswer {
			c.fail("in a chain of typed records a %s step is a typed record", t)
		}
		// A check that states its rule inputs version (section 8) carries the count that version
		// defines; one without it re-derives and verifies as before.
		if t == TypeCheck && has(body, "rule_inputs_version") && !(has(body, "material_fields_changed") && has(body, "material_fields_basis")) {
			c.fail("a check that states rule_inputs_version carries material_fields_changed and material_fields_basis")
		}
		if c.typedChain && t == TypeDisclosure && body["authority"] == "approval" {
			c.fail("in a chain of typed records an approved disclosure is an action-record/v0")
		}
		if t == "one_shot_override" {
			c.fail("one_shot_override is reserved: no rule in this profile defines an override")
		}
		var confirms []obj
		for _, r := range sl(b["refs"]) {
			if rm := mp(r); rm["rel"] == "confirms" {
				confirms = append(confirms, rm)
			}
		}
		if c.closedFinal >= 0 {
			// Close is terminal: after it, only later evidence that confirms
			// that close (and commits to its digest) may follow.
			if t != TypeEvidence || len(confirms) != 1 || confirms[0]["digest"] != c.digests[c.closedFinal] {
				c.fail("record after a close whose outcome is final (completed, mismatch, not_selected or called_off; close is terminal): " +
					"only an evidence record that confirms that close may follow")
			}
		} else if len(confirms) > 0 {
			c.fail("a confirms ref names the deal's final close; this deal is not closed")
		}
		if b["deal_id"] != blk0["deal_id"] {
			c.fail("deal_id differs from the baseline's")
		}
		if why := checkHandover(rec, c.partyRole); why != "" {
			c.fail("%s", why)
		}
		if seq := num(b["seq"]); seq != int64(i+1) {
			kind := "gap"
			if seq <= int64(i) {
				kind = "regression"
			}
			c.fail("seq %s: expected %d, got %d", kind, i+1, seq)
		}
		if i > 0 {
			if t == TypeBaseline {
				c.fail("a deal has exactly one baseline")
			}
			if !same(b["prev"], ref(c.digests[i-1])) {
				c.fail("prev is not the digest of the previous record")
			}
			if !same(b["baseline_ref"], ref(c.digests[0])) {
				c.fail("baseline_ref is not the digest of the baseline")
			}
			if str(b["at"]) < str(block(records[i-1])["at"]) {
				c.fail("at is earlier than the previous record's")
			}
		}
		c.byRel = map[string][]int{}
		registrations := 0
		for _, r := range sl(b["refs"]) {
			rm := mp(r)
			rel := str(rm["rel"])
			if !slices.Contains(allowedRels[t], rel) {
				c.fail("rel %s is not allowed on a %s record", pyRepr(rel), t)
			}
			if rel == "registration" {
				// A sale's thread names its registration, a record on the sale's
				// own log: checked with the sale's bundle, never in this chain.
				registrations++
				if registrations > 1 || !has(body, "sale_authority_commitment") {
					c.fail("only a sale's thread names a registration, and exactly one")
				}
				continue
			}
			j, ok := c.byDig[str(rm["digest"])]
			if !ok || j >= i {
				c.fail("ref %s does not name an earlier record of this deal", rel)
			}
			c.byRel[rel] = append(c.byRel[rel], j)
		}

		if t == TypeMessage && has(body, "sent_at") {
			// The platform's own time for the message: a real UTC time, never
			// later than when the message was noted.
			sent, ok := validUTC(str(body["sent_at"]))
			if !ok {
				c.fail("a message's sent_at is not a valid UTC time")
			}
			noted, ok := validUTC(str(b["at"]))
			if !ok {
				panic("at is not a UTC time")
			}
			if sent.After(noted) {
				c.fail("a message's sent_at is later than when it was noted (at)")
			}
		}
		c.step(typed)
	}
}

// pyRepr is Python's repr of a str, as the checker's `{x!r}` shows it.
func pyRepr(s string) string {
	q := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = `"`
	}
	return q + strings.ReplaceAll(s, q, `\`+q) + q
}
