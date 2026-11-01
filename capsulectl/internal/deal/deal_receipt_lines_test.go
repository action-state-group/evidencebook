package deal

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receipt's lines as Steven's 9b frame 8 states them (F8.1, F8.7;
// evidencebook-deal#94): the approval says what it is and that no record
// is signed with the person's own key; "after your yes" is recomputed from
// the log's order; the settlement says its rail and band and never passes
// a proof the page cannot check; the close is read only from the other
// side's records.

// stampLine is the receipt's stamp line whose text starts with prefix.
func stampLine(t *testing.T, got map[string]any, prefix string) string {
	t.Helper()
	for _, s := range got["stampText"].([]any) {
		text := strings.TrimLeft(s.(string), "✓—")
		if strings.HasPrefix(text, prefix) {
			return text
		}
	}
	t.Fatalf("no stamp line %q in %v", prefix, got["stampText"])
	return ""
}

func noStampLine(t *testing.T, got map[string]any, prefix string) {
	t.Helper()
	for _, s := range got["stampText"].([]any) {
		assert.False(t, strings.HasPrefix(strings.TrimLeft(s.(string), "✓—"), prefix), "no %q line: %v", prefix, got["stampText"])
	}
}

func TestTheReceiptSaysWhatTheApprovalIsAndRecomputesItsOrder(t *testing.T) {
	dealFixture(t)
	id, _, _ := fawnDeal(t)

	own := renderJourney(t, id, false)
	require.Empty(t, own["refusal"])
	assert.Equal(t, "Your own key's signature on your yes · missing · your yes is in your words, recorded by your agent",
		stampLine(t, own, "Your own key's signature"), "the approval record is there; the signature with the person's key is what is missing")
	assert.Equal(t, "Each act came after your yes · passed · recomputed from the order of this log", stampLine(t, own, "Each act came after"))
	assert.Equal(t, "The payment is settled on its rail · missing · a card payment; the card processor's report is not in this file",
		stampLine(t, own, "The payment is settled"), "a card payment carries no settlement record, so nothing is checked")
	assert.Equal(t, "Their agreement with how the deal closed · not checked · the deal is not closed in this copy", stampLine(t, own, "Their agreement"))
	for _, s := range own["stampText"].([]any) {
		assert.NotContains(t, s, "signed with your key ·", "never says the approval is signed")
	}

	// An approval withheld from the copy is missing, naming the act it was for.
	withheld := renderJourney(t, id, false, "withhold-approval")
	require.Empty(t, withheld["refusal"])
	assert.Regexp(t, `^Each act came after your yes · missing · the approval for .+ is not in this copy$`, stampLine(t, withheld, "Each act came after"))

	// The other side's copy names the holder, never "your", and withheld steps are not checked.
	shared := renderJourney(t, id, true)
	require.Empty(t, shared["refusal"])
	assert.Equal(t, "Each act came after the holder's yes · not checked · steps are withheld from this copy", stampLine(t, shared, "Each act came after"))
	assert.Equal(t, "Their agreement with how the deal closed · not checked · steps are withheld from this copy", stampLine(t, shared, "Their agreement"),
		"with steps withheld, no close in the copy does not mean the deal is open")
}

// A deal with no act needing approval draws no approval or order line.
func TestAReceiptWithNoActDrawsNoApprovalLine(t *testing.T) {
	pair := closedPair(t, received, received)
	party(t, pair.buyer.config)
	got := renderJourney(t, pair.id, false)
	require.Empty(t, got["refusal"])
	noStampLine(t, got, "Your own key's signature")
	noStampLine(t, got, "Each act came after")
	assert.Equal(t, "Their agreement with how the deal closed · missing · no record from their side", stampLine(t, got, "Their agreement"),
		"a closed deal with only your copy: their side is missing, never agreed")
	assert.Equal(t, "none", got["close"])

	// The shared copy of a closed deal never reads as "not closed".
	shared := renderJourney(t, pair.id, true)
	require.Empty(t, shared["refusal"])
	assert.NotContains(t, stampLine(t, shared, "Their agreement"), "not closed")
}

// A Lightning payment the seller received: its rail and band, and no passed
// stamp, since the page cannot check the preimage itself (a digest is the
// runtime's to compute). Synthetic preimage (deal_settlement_lightning_test.go).
func TestTheReceiptStatesALightningSettlementAndItsBand(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	page := func(id string) map[string]any {
		path := filepath.Join(t.TempDir(), "page.html")
		dealRun(t, "report", "--deal", id, "--html", path)
		return renderPageUnderNode(t, node, path)
	}
	id, _ := sellerDealWithDueDelivery(t)
	terms, _ := newBuyerSide(t).lnTerms()
	require.NoError(t, noteLightningReceived(t, id, terms, lnInvoice, lnTransaction("succeeded")))
	got := page(id)
	require.Empty(t, got["refusal"])
	assert.Equal(t, "The payment is settled on its rail · not checked · Lightning; its proof is sealed here, and this page can't check it yet · size 0.000001 to under 0.00001 BTC",
		stampLine(t, got, "The payment is settled"), "495,000 msat credited plus the 5,000 msat fee is one 500 sat band, in BTC")

	other, _ := sellerDealWithDueDelivery(t)
	terms2, _ := newBuyerSide(t).lnTerms()
	without := strings.Replace(lnTransaction("succeeded"), `,"preimage":"`+lnPreimage+`"`, "", 1)
	require.NoError(t, noteLightningReceived(t, other, terms2, lnInvoice, without))
	assert.Equal(t, "The payment is settled on its rail · not checked · Lightning; your wallet reported it final · size 0.000001 to under 0.00001 BTC",
		stampLine(t, page(other), "The payment is settled"))

	// A failed payment is not settled: no leg to state.
	failed, _ := sellerDealWithDueDelivery(t)
	terms3, _ := newBuyerSide(t).lnTerms()
	require.NoError(t, noteLightningReceived(t, failed, terms3, lnInvoice, lnTransaction("failed")))
	assert.Equal(t, "The payment is settled on its rail · missing · no settlement record is in this file", stampLine(t, page(failed), "The payment is settled"))
}

// The three cases on the receipt with the other party's half: stamped by
// both (their copy checks: the close is read from it), your receipt only
// (an absence recorded: missing, never failed) and no record of yours.
func TestTheComposedReceiptReadsTheCloseFromTheirRecord(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	pair := closedPair(t, received, received)
	party(t, pair.buyer.config)
	keepPath := filepath.Join(t.TempDir(), "keep.json")
	dealRun(t, "report", "--deal", pair.id, "--bundle", keepPath)
	keep := exactBundle(t, string(mustRead(t, keepPath)))
	own := askFor(t, pair.buyer, pair.buyer, pair.id, map[string]any{"min_freshness": map[string]any{"size": 1}})
	theirs := askFor(t, pair.buyer, pair.seller, pair.id, map[string]any{"min_freshness": map[string]any{"size": 1}})
	require.Equal(t, "artifact", theirs.outcome)
	absent := recordedAbsence(t, pair.buyer, pair.id)
	open := func(withKeep map[string]interface{}, member askedCopy) map[string]any {
		party(t, pair.buyer.config)
		out := filepath.Join(t.TempDir(), "composed.json")
		text, err := dealInvoke(t, "", "--profile", "deal", "deal", "compose", "--deal", pair.id, "--output", out, "--member", own.recordID, "--member", member.recordID)
		require.NoError(t, err, text)
		page, err := composedPage(exactBundle(t, string(mustRead(t, out))), withKeep, dealVerifyCommand("page.html"), cli.BuiltinWording())
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "page.html")
		require.NoError(t, os.WriteFile(path, []byte(page), 0o600))
		got := renderPageUnderNode(t, node, path)
		require.Empty(t, got["refusal"])
		return got
	}

	both := open(keep, theirs)
	assert.Equal(t, "unilateral", both["close"], "no deal verb seals an acknowledgement yet, so their record never points at your close")
	assert.Equal(t, "Their agreement with how the deal closed · missing · closed by your side only: their record is here, and it does not point at your close", stampLine(t, both, "Their agreement"))
	assert.Equal(t, "The payment is settled on its rail · missing · no settlement record is in this file", stampLine(t, both, "The payment is settled"))

	only := open(keep, absent)
	assert.Equal(t, "Their agreement with how the deal closed · missing · no record from their side", stampLine(t, only, "Their agreement"))
	assert.Equal(t, "none", only["close"])

	none := open(nil, theirs)
	assert.Equal(t, "Each act came after your yes · missing · your record is not in this file", stampLine(t, none, "Each act came after"))
	assert.Equal(t, "Their agreement with how the deal closed · not checked · your record is not in this file", stampLine(t, none, "Their agreement"))
	assert.Equal(t, "The payment is settled on its rail · missing · no settlement record is in this file", stampLine(t, none, "The payment is settled"))
}

// dealViewFunctionsDriver loads the deal view's script under node with a
// stub runtime and runs the cases on stdin through its own functions:
// the derivations the receipt's lines are drawn from.
const dealViewFunctionsDriver = `
const fs = require("fs");
const vm = require("vm");
const [scriptPath] = process.argv.slice(2);
const context = vm.createContext({ console, EvidenceGraph: { registerPresentation: () => {} } });
vm.runInContext(fs.readFileSync(scriptPath, "utf8"), context);
const cases = JSON.parse(fs.readFileSync(0, "utf8"));
context.cases = cases;
process.stdout.write(JSON.stringify(vm.runInContext(` + "`" + `({
  close: cases.close.map((c) => dealCloseStatus(c.close, c.records, c.book)),
  band: cases.band.map((b) => dealBandLabel(b, {})),
  afterYes: cases.afterYes.map((steps) => { const r = dealAfterYes(steps); return r.state + (r.why ? ":" + r.why : "") + (r.act ? ":" + r.act.id : "") + (r.approvers && r.approvers.length ? " by " + r.approvers.join(",") : ""); }),
  settlement: cases.settlement.map((steps) => dealSettlement(steps)),
})` + "`" + `, context)));
`

func runDealViewFunctions(t *testing.T, cases map[string]any) map[string]any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "deal-view.js")
	require.NoError(t, os.WriteFile(script, []byte(DealViewJS), 0o600))
	driver := filepath.Join(dir, "driver.js")
	require.NoError(t, os.WriteFile(driver, []byte(dealViewFunctionsDriver), 0o600))
	in, err := json.Marshal(cases)
	require.NoError(t, err)
	cmd := exec.Command(node, driver, script)
	cmd.Stdin = strings.NewReader(string(in))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got), string(out))
	return got
}

// The close status is evidencebook's StatusOfClose: a rebuttal of your
// close wins over an acknowledgement, only a link to your close counts,
// and only from their book.
func TestTheCloseStatusIsReadFromTheirLinks(t *testing.T) {
	rec := func(book string, links ...map[string]any) map[string]any {
		return map[string]any{"evidence_record_header": map[string]any{"book_id": book, "links": links}}
	}
	link := func(typ, target string) map[string]any { return map[string]any{"type": typ, "target": target} }
	const mine, theirs = "close-1", "deal/theirs"
	got := runDealViewFunctions(t, map[string]any{
		"close": []map[string]any{
			{"close": mine, "book": theirs, "records": []any{rec(theirs, link("cites", "x"))}},
			{"close": mine, "book": theirs, "records": []any{rec(theirs, link("acknowledges", mine))}},
			{"close": mine, "book": theirs, "records": []any{rec(theirs, link("acknowledges", mine)), rec(theirs, link("rebuts", mine))}},
			{"close": mine, "book": theirs, "records": []any{rec(theirs, link("acknowledges", "another close"))}},
			{"close": mine, "book": theirs, "records": []any{rec("deal/someone-else", link("acknowledges", mine))}},
			{"close": mine, "book": theirs, "records": []any{}},
		},
		"band": []any{}, "afterYes": []any{}, "settlement": []any{},
	})
	assert.Equal(t, []any{"unilateral", "agreed", "contested", "unilateral", "unilateral", "unilateral"}, got["close"])
}

// The band's words are the History card's (capsule-viewer
// standard_history_card.js bandLabel): a symbol's sub-ten bands are one
// "under $10", others keep their decade in their own units.
func TestTheSettlementBandIsWordedAsTheHistoryCardWordsIt(t *testing.T) {
	band := func(currency string, decade any) map[string]any {
		return map[string]any{"currency": currency, "decade": decade}
	}
	got := runDealViewFunctions(t, map[string]any{
		"close": []any{}, "afterYes": []any{}, "settlement": []any{},
		"band": []any{band("USD", 0), band("USD", nil), band("USD", 1), band("USD", 3), band("USD", 16), band("USD", 17), band("BTC", -6), band("BTC", 0), band("BTC", nil), band("USDC", -2)},
	})
	assert.Equal(t, []any{"under $10", "under $10", "$10 – $99", "$1,000 – $9,999", "$10,000,000,000,000,000 – $99,999,999,999,999,999", "$100,000,000,000,000,000 – $999,999,999,999,999,999", "0.000001 to under 0.00001 BTC", "1 to under 10 BTC", "zero amount, BTC", "0.01 to under 0.1 USDC"}, got["band"])
}

// "After your yes", recomputed over a deal's steps: the approval, its
// verdict and its check are each earlier in the log; an act without an
// approval fails, named.
func TestAfterYourYesIsRecomputedFromTheLogsOrder(t *testing.T) {
	step := func(id string, seq int, typ string, body map[string]any, refs ...map[string]any) map[string]any {
		return map[string]any{"id": id, "seq": seq, "digest": "d-" + id, "deal": map[string]any{"record_type": typ, "refs": refs}, "body": body}
	}
	ref := func(rel, id string) map[string]any { return map[string]any{"rel": rel, "digest": "d-" + id} }
	check := step("c", 2, "check", map[string]any{"action": "pay"})
	verdict := step("v", 3, "verdict", map[string]any{}, ref("checks", "c"))
	approval := step("a", 4, "approval", map[string]any{"proceed": true, "approver": "user"}, ref("approves", "v"))
	act := step("p", 5, "action", map[string]any{"action": "pay"}, ref("authorized_by", "a"))
	held := step("a", 4, "approval", map[string]any{"proceed": false, "approver": "user"}, ref("approves", "v"))
	late := step("a", 6, "approval", map[string]any{"proceed": true, "approver": "user"}, ref("approves", "v"))
	withheld := map[string]any{"id": "a", "seq": 4, "digest": "d-a", "body": map[string]any{}}
	unchecked := step("u", 5, "outcome", map[string]any{"status": "unchecked_action"})
	told := step("t", 5, "disclosure", map[string]any{"authority": "none"})
	typedUnchecked := step("tu", 5, "action-outcome/v0", map[string]any{"status": "unchecked_action"})
	uncheckedClaim := step("uc", 5, "claim", map[string]any{"unchecked": true, "rule": "no_check"})
	hidden := map[string]any{"id": "h", "seq": 6, "digest": "d-h", "body": map[string]any{}}
	// Rule 5: the verdict's first answer only, the user's own approval for
	// a typed ASK, standing intent only on a pass, and the check's action.
	first := step("a0", 4, "approval", map[string]any{"proceed": false, "approver": "user"}, ref("approves", "v"))
	second := step("a", 5, "approval", map[string]any{"proceed": true, "approver": "user"}, ref("approves", "v"))
	actAfter := step("p", 6, "action", map[string]any{"action": "pay"}, ref("authorized_by", "a"))
	cardAnswer := step("ty", 4, "action-approval/v0", map[string]any{"authority": "card_answer", "proceed": true}, ref("approves", "e"))
	paused := step("v", 3, "verdict", map[string]any{"result": "pause"}, ref("checks", "c"))
	standing := step("a", 4, "approval", map[string]any{"proceed": true, "approver": "standing_intent"}, ref("approves", "v"))
	otherCheck := step("c", 2, "check", map[string]any{"action": "sign"})
	// Typed records: an act the task's authority let go ahead (DO), or one
	// the user approved after an evaluation that asked.
	proposed := step("pc", 2, "proposed-action/v0", map[string]any{"action": "commit"})
	do := step("e", 3, "action-evaluation/v0", map[string]any{"disposition": "DO"}, ref("checks", "pc"))
	ask := step("e", 3, "action-evaluation/v0", map[string]any{"disposition": "ASK"}, ref("checks", "pc"))
	typedYes := step("ty", 4, "action-approval/v0", map[string]any{"authority": "user_approval", "proceed": true}, ref("approves", "e"))
	typedAct := step("ta", 5, "action-record/v0", map[string]any{"action": "commit"}, ref("authorized_by", "e"))
	typedApproved := step("ta", 5, "action-record/v0", map[string]any{"action": "commit"}, ref("authorized_by", "ty"))
	got := runDealViewFunctions(t, map[string]any{
		"close": []any{}, "band": []any{}, "settlement": []any{},
		"afterYes": []any{
			[]any{proposed, do, typedAct},
			[]any{proposed, ask, typedAct},
			[]any{proposed, ask, typedYes, typedApproved},
			[]any{check, verdict, approval, act},
			[]any{check, verdict, held, act},
			[]any{check, verdict, late, act},
			[]any{check, verdict, withheld, act},
			[]any{check, verdict, act},
			[]any{check, verdict, approval, unchecked},
			[]any{told},
			[]any{proposed, typedUnchecked},
			[]any{uncheckedClaim},
			[]any{check, verdict, approval, act, hidden},
			[]any{check, verdict, first, second, actAfter},
			[]any{proposed, ask, cardAnswer, typedApproved},
			[]any{check, paused, standing, act},
			[]any{otherCheck, verdict, approval, act},
			[]any{check},
		},
	})
	assert.Equal(t, []any{"passed by task_authority", "failed:order:ta", "passed by user", "passed by user", "failed:order:p", "failed:order:p", "missing:p", "failed:order:p", "failed:unapproved:u", "failed:unapproved:t", "failed:unapproved:tu", "failed:unapproved:uc", "not checked by user", "failed:order:p", "failed:order:ta", "failed:order:p", "failed:order:p", "none"}, got["afterYes"])
}

// The settlement as the legs state it: the rail from the payment
// reference, whether the preimage is held (or withheld from this copy),
// and a band only when every amount is in one asset.
func TestTheSettlementIsReadFromItsLegs(t *testing.T) {
	amount := func(code string, scale int, value string) map[string]any {
		return map[string]any{"assetCode": code, "assetScale": scale, "value": value}
	}
	leg := func(ref, code string, fee map[string]any, body map[string]any, withheld bool) map[string]any {
		s := map[string]any{"id": "l", "seq": 3, "body": body, "settlement": map[string]any{
			"leg": "payee_observed", "status": "settled", "payment_ref": map[string]any{"type": ref}, "received": amount(code, 6, "2500000"), "receive_fee": fee,
		}}
		if !withheld {
			s["deal"] = map[string]any{"record_type": "evidence"}
		}
		return s
	}
	usdc := amount("USDC", 6, "0")
	got := runDealViewFunctions(t, map[string]any{
		"close": []any{}, "band": []any{}, "afterYes": []any{},
		"settlement": []any{
			[]any{leg("x402.transaction", "USDC", usdc, map[string]any{}, false)},
			[]any{leg("ln.payment_hash", "USDC", usdc, map[string]any{"preimage": "ab"}, false)},
			[]any{leg("ln.payment_hash", "USDC", usdc, map[string]any{}, true)},
			[]any{leg("x402.transaction", "USDC", amount("EUR", 2, "1"), map[string]any{}, false)},
			[]any{map[string]any{"id": "p", "seq": 2, "deal": map[string]any{"record_type": "action"}, "body": map[string]any{"rail": "card"}}},
			[]any{},
		},
	})
	band := map[string]any{"currency": "USDC", "decade": float64(0)}
	assert.Equal(t, []any{
		map[string]any{"rail": "x402", "legs": true, "preimage": false, "band": band},
		map[string]any{"rail": "lightning", "legs": true, "preimage": true, "band": band},
		map[string]any{"rail": "lightning", "legs": true, "preimage": "withheld", "band": band},
		map[string]any{"rail": "x402", "legs": true, "preimage": false},
		map[string]any{"rail": "card", "legs": false},
		nil,
	}, got["settlement"])
}
