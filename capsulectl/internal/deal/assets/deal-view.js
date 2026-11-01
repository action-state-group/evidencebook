// deal-view.js: the deal page's presentation module, capsulectl.deal-view/v0,
// for the page `capsulectl deal report --html` writes. Three parts: what you
// asked, what the agent did, anomalies on either side. Each item expands to its
// sealed steps.
//
// It is a module-slot script (agent-action-capsule's presentation contract):
// it registers its module with the page's runtime at load, and the runtime's
// resolver selects it for a verified deal bundle. It never verifies and never
// reads the bundle's records or disclosures itself: buildModel reads the
// verified context the runtime built, and what capsulectl checked when it
// built the page (the countersign line, and the words it recomputed against
// their sealed commitments), which the page's bootstrap sets as
// capsulectlDealPage before the page renders. render writes the model into the
// host. A step's line is shown only when its sealed record's disclosure
// verified, otherwise the step shows its capsule_id only. All text is set with
// textContent; nothing from the bundle is parsed as HTML.
//
// Its manifest is assets/deal-view.manifest.json; the copy below is the same
// manifest except executable.script_sha256, which a script cannot carry for
// itself (it is all zeros here). Its stylesheet is DEAL_VIEW_CSS, inserted
// once at render and pinned by the manifest's style_sha256 (assets/deal-view.css
// holds the same bytes).
// versionBefore reports whether version a is older than b: "v0.1.0-rc3" style,
// numbers compared as numbers, a pre-release before its release. An
// unparsable version, or a development build, is never called older.
function versionBefore(a, b) {
  const parse = (v) => {
    const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/.exec(v || "");
    return m ? { n: [Number(m[1]), Number(m[2]), Number(m[3])], pre: m[4] } : undefined;
  };
  const x = parse(a), y = parse(b);
  // A development build is built from source, older or newer than any tag:
  // never call it, or anything next to it, older.
  if (!x || !y || x.pre === "dev" || y.pre === "dev") return false;
  for (let i = 0; i < 3; i++) if (x.n[i] !== y.n[i]) return x.n[i] < y.n[i];
  if (x.pre === y.pre) return false;
  if (x.pre === undefined) return false;
  if (y.pre === undefined) return true;
  const num = (p) => (/^(.*?)(\d+)$/.exec(p) || [p, p, ""]);
  const [, xa, xd] = num(x.pre), [, ya, yd] = num(y.pre);
  if (xa === ya && xd !== "" && yd !== "") return Number(xd) < Number(yd);
  return x.pre < y.pre;
}

// The module's built-in words: the page.* entries of assets/wording/capsulectl-deal-en.json
// and the core's size.* entries,
// written here by TestDealViewDefaultWordsAreThePack (CAPSULECTL_UPDATE_DEAL_VIEW_WORDS=1);
// the pack is the source, and a test holds this table to it.
// BEGIN DEAL_VIEW_WORDS
const DEAL_VIEW_WORDS = {
  "page.act.named": "the {action} step",
  "page.act.unnamed": "a step",
  "page.also.absent": "Also: your agent asked and nothing came back by {day}.",
  "page.also.refused": "Also: they answered on {day} that they would not share.",
  "page.also.refused_unchecked": "Also: a refusal in their name came back on {day}; its signature was not checked here.",
  "page.anomalies.agent": "Agent side",
  "page.anomalies.counterparty": "Counterparty side",
  "page.anomalies.none": "None found.",
  "page.anomalies.title": "Anomalies",
  "page.approver.agent_card": "answered on a card {who} agent showed, recorded by {their} agent",
  "page.approver.standing_intent": "approved under {who} standing limits, recorded by {their} agent",
  "page.approver.task_authority": "allowed by the task {giver} gave {their} agent, recorded by {their} agent",
  "page.approver.user": "{who} yes is in {their} words, recorded by {their} agent",
  "page.asked.exact": "✓ These are the exact words sealed when the deal opened, checked when this page was built.",
  "page.asked.title": "What you asked",
  "page.asked.withheld": "Withheld from this copy.",
  "page.audience.adjudicator": "an adjudicator",
  "page.audience.counterparty": "the other party",
  "page.authority.title": "Authority",
  "page.bar.own": "Your copy",
  "page.bar.shared": "A shared copy",
  "page.build.commit_unknown": "unknown",
  "page.build.earlier_name": "an earlier capsulectl that did not record its version",
  "page.build.earlier_one": "Some steps were sealed by {builds}, the build that sealed deals before {name} did; {name} {version} made this page.",
  "page.build.earlier_other": "Some steps were sealed by {builds}, the builds that sealed deals before {name} did; {name} {version} made this page.",
  "page.build.older": "Produced by an older version ({versions}) than the {name} that made this page ({version}).",
  "page.build.unrecorded": "unrecorded",
  "page.cancel.not_shown": "What this does not show",
  "page.cancel.shows": "What this shows",
  "page.cancel.title": "Your cancellation",
  "page.card.asked": "You asked",
  "page.card.asked_day": "You asked, {day}",
  "page.card.asked_unchecked": "⚠️ The words you asked could not be checked against the sealed record.",
  "page.card.copy_adjudicator": "This is a shared copy. It shows only what an adjudicator may see.",
  "page.card.copy_counterparty": "This is a shared copy. It shows only what the other party may see.",
  "page.card.copy_own": "This is your copy. It shows everything in your book.",
  "page.card.done": "Done: {text}",
  "page.card.nothing_done": "Nothing done yet.",
  "page.card.so_far": "So far: {text}",
  "page.card.title": "Receipt",
  "page.card.title_deal": "Receipt · deal {deal}",
  "page.card.withheld": "What was asked is withheld from this copy.",
  "page.claim.address": "Address",
  "page.claim.authenticity": "Authenticity",
  "page.claim.availability": "Availability",
  "page.claim.condition": "Condition",
  "page.claim.deadline": "Deadline",
  "page.claim.delivery_date": "Delivery date",
  "page.claim.delivery_promise": "Delivery promise",
  "page.claim.features": "Features",
  "page.claim.other": "Other",
  "page.claim.payment_methods": "Payment methods",
  "page.claim.pickup": "Pickup",
  "page.claim.price": "Price",
  "page.claim.refund_terms": "Refund terms",
  "page.claim.service_scope": "Service scope",
  "page.claim.warranty": "Warranty",
  "page.claims.merchant": "It does not prove the merchant shipped, delivered or refunded anything.",
  "page.claims.reported": "It records what the agent reported. It does not show that what was reported was true.",
  "page.claims.tamper": "It is tamper-evident, not non-repudiation: it shows these records were not changed after they were sealed, not who made them.",
  "page.claims.title": "What this does not claim",
  "page.close.agreed": "agreed: their record points at {who} close",
  "page.close.contested": "contested: their record disputes {who} close",
  "page.close.exchange": "their record is of the exchange, not of {who} close",
  "page.close.none_other": "no record from the other side",
  "page.close.none_theirs": "no record from their side",
  "page.close.not_closed": "the deal is not closed in this copy",
  "page.close.unchecked": "their record did not check",
  "page.close.unilateral": "closed by {who} side only: their record is here, and it does not point at {who} close",
  "page.composed.both": "Stamped by both sides",
  "page.composed.keep_note": "This page is your private copy: it carries your own record of this deal in full. To share this receipt, send the composed bundle file, not this page.",
  "page.composed.no_copy": "Your copy is not in this file",
  "page.composed.none": "No record",
  "page.composed.only": "Your receipt only",
  "page.composed.said.absent": "Your agent asked for their record and nothing came back.",
  "page.composed.said.failed": "Their record is in this file, but it did not check.",
  "page.composed.said.refused": "They did not share their record.",
  "page.composed.said.refused_unchecked": "A refusal in their name is in this file.",
  "page.composed.said.verified": "Their record is about {same} as yours.",
  "page.composed.said.verified_stamped": "Their record is about {same} as yours, and each is stamped into its own book.",
  "page.composed.sub_no_copy": "Your own copy of this deal is not in this file.",
  "page.composed.sub_nothing": "Nothing from their side is in this file.",
  "page.composed.title_checked": "{label} · checked",
  "page.composed.title_unchecked": "{label} · not fully checked",
  "page.countersign.unread": "The countersign line could not be read from this file.",
  "page.deadlines.both": "Cancel-by and due dates",
  "page.deadlines.cancel": "Cancel-by dates",
  "page.deadlines.passed_today": "Not part of the report, worked out by this page when you opened it: this date has passed as of today, {day}.",
  "page.demo": "DEMO",
  "page.detail.approval_missing": "the approval for {act} is not in this copy",
  "page.detail.built": "checked when this page was built",
  "page.detail.no_own_record": "your record is not in this file",
  "page.detail.no_signed_key": "no record here is signed with {their} key",
  "page.detail.nobody": "no one else has checked it",
  "page.detail.not_in_file": "it is not in this file",
  "page.detail.order": "{act} does not follow its approval in this log",
  "page.detail.recomputed": "recomputed from the order of this log",
  "page.detail.same_deal": "both records name the same deal; neither points at the other",
  "page.detail.same_request": "both records name the same request; neither points at the other",
  "page.detail.sealed_before_book": "this copy's steps were sealed before they had a place in a book",
  "page.detail.see_more": "see More on this deal",
  "page.detail.theirs_missing": "their record is not in this file",
  "page.detail.unapproved": "{act} was taken without {who} yes",
  "page.detail.withheld": "steps are withheld from this copy",
  "page.detail.witness_unchecked": "this page can't check the witness's signature",
  "page.did.nothing": "Nothing yet.",
  "page.did.sealed": "The summary lines below, and each step's line, were written by capsulectl on this device and sealed with this page: this page checked they were not changed since, not that they are right. Open an item to see the steps it was read from; each step's sealed record is checked.",
  "page.did.title": "What the agent did",
  "page.did.unchecked": "The summary lines below, and each step's line, are written by capsulectl on this device and are not checked by this page. Open an item to see the steps it was read from; each step's sealed record is checked, not its line.",
  "page.doesnt.merchant": "That the merchant shipped, delivered or refunded anything.",
  "page.doesnt.other_side": "Who the other side is. The key shown as theirs is the one the composer of this file named.",
  "page.doesnt.title": "What it doesn't",
  "page.doesnt.true": "That what the agent reported is true. It records what the agent reported.",
  "page.doesnt.who": "Who made the records. It shows a change after they were sealed would show.",
  "page.doesnt.withheld": "Anything left out of this copy: the steps marked contents withheld are proven present, not shown.",
  "page.end": "This page checked each step's sealed record and its place in this deal's log; the verification below says what was and was not checked. The records carry fingerprints, not names or numbers; the words shown come from this device.",
  "page.end.key": "The seal key is on the agent's machine, so this shows the record was not changed after it was made, not who made it. It covers this skill's own records only, never the agent host's own store.",
  "page.end.withheld": "Steps marked contents withheld are proven present and unchanged; what they say is not in this copy.",
  "page.join_and": "{list} and {last}",
  "page.life.as_of": "{text} (as of {when})",
  "page.life.checkpoint": "This report states the deal as of its checkpoint at {when} (the producer's time).",
  "page.life.later": "confirms the close: {text}",
  "page.life.passed": "The expected close, {day}, has passed as of today.",
  "page.life.title": "Where this deal stands",
  "page.life.today": "Not part of the report, worked out by this page when you opened it: today is {day}.{passed}",
  "page.line.close": "Their agreement with how the deal closed",
  "page.line.each_act": "Each act came after {who} yes",
  "page.line.key_signature": "{cap} own key's signature on {their} yes",
  "page.line.settled": "The payment is settled on its rail",
  "page.merchant.approved": "You approved",
  "page.merchant.approved_basis": "You approved ({basis})",
  "page.merchant.cancel_by": "Cancel by (read from the email)",
  "page.merchant.charged": "The merchant's email says (read from the email)",
  "page.merchant.charged_on": "Charged on (the email's date)",
  "page.merchant.dkim": "DKIM signature: {says}",
  "page.merchant.domain": "Signing domain",
  "page.merchant.email": "Merchant email",
  "page.merchant.items": "Items (read from the email)",
  "page.merchant.key": "Signing domain's key",
  "page.merchant.key_size": "{size}, sealed when the email was sealed",
  "page.merchant.key_supplied": "The signing domain's key was supplied by hand, not read from its DNS.",
  "page.merchant.order": "Order {order}",
  "page.merchant.reported": "The agent reported paying",
  "page.merchant.seal": "Our seal: {says}",
  "page.merchant.title": "The merchant's own email",
  "page.merchant.two": "Two different things stand behind each email. The signing domain's DKIM signature is checked against its published key, saved when the email was sealed; it shows what that domain signed, and it does not depend on the agent. Our seal shows this device held these exact bytes no later than the seal time; who signed them is checked by that DKIM signature, not by our seal. Amounts, dates and order numbers below are read from the email by capsulectl and may be misread.",
  "page.more.title": "More on this deal: steps, authority, what was told, anomalies",
  "page.more_theirs": "More on their side",
  "page.mover.agent": "Your agent",
  "page.mover.other": "The other side",
  "page.mover.someone": "Someone",
  "page.mover.user": "You",
  "page.none.text": "Not in this file. This page cannot show your own record of this deal.",
  "page.none.title": "Your side",
  "page.ours.asked": "you asked for it",
  "page.ours.asked_tail": "your words, sealed",
  "page.ours.holder": "The holder's side · in their book",
  "page.ours.own": "Your side · in your book",
  "page.own.checked": "Your copy only · checked",
  "page.own.note": "This file holds your side of the deal. The other side's record is not in it, so this page cannot say whether they agree.",
  "page.own.unchecked": "Your copy only · not fully checked",
  "page.pill.failed": "{count} failed",
  "page.pill.passed": "{count} passed",
  "page.pill.rest": "{count} not checkable here",
  "page.produced": "Produced by {builds}.",
  "page.produced_then": ", then ",
  "page.pron.cap.own": "Your",
  "page.pron.cap.shared": "The holder's",
  "page.pron.giver.own": "you",
  "page.pron.giver.shared": "the holder",
  "page.pron.other": "their",
  "page.pron.their.own": "your",
  "page.pron.their.shared": "their",
  "page.pron.who.own": "your",
  "page.pron.who.shared": "the holder's",
  "page.proves.asked": "The words you asked are sealed, exactly as said, and this page checked them against the record.",
  "page.proves.authority": "Each action recorded here has an approval or a rule recorded for it.",
  "page.proves.nothing": "Nothing on this page is vouched for beyond the stamps above.",
  "page.proves.steps": "Every step listed here is in this deal's log and unchanged since it was sealed.",
  "page.proves.theirs": "Their record of {same} is in this file, signed with their key and stamped into their book, and this page checked it.",
  "page.proves.title": "What this receipt proves",
  "page.rail.lightning_final": "Lightning; {whose} wallet reported it final",
  "page.rail.lightning_sealed": "Lightning; its proof is sealed here, and this page can't check it yet",
  "page.rail.lightning_withheld": "Lightning; this copy does not show its proof",
  "page.rail.none": "this page checks no settlement",
  "page.rail.x402": "x402; the chain lookup is not checked on this page",
  "page.refuse.composed": "⚠️ {why} Do not rely on it; run capsulectl verify on the file.",
  "page.refuse.composition_holds": "This file's composition does not hold up.",
  "page.refuse.composition_record": "This file's composition record could not be checked.",
  "page.refuse.composition_unverified": "This file's composition did not verify.",
  "page.refuse.many_parties": "This file holds records from more than one other party.",
  "page.refuse.opening": "⚠️ The deal's opening step is not in this report.",
  "page.refuse.own_not_composers": "The copy shown as yours is not the composer's own copy of this deal.",
  "page.refuse.own_uncited": "Your own record of this exchange is not cited by your receipt.",
  "page.refuse.own_unverified": "Your own copy on this page did not verify.",
  "page.refuse.record": "⚠️ The deal's text could not be checked against its sealed record. Do not rely on it.",
  "page.refuse.report": "⚠️ The deal's text could not be checked against its sealed report. Do not rely on it.",
  "page.refuse.report_signature": "⚠️ This viewer cannot check the report's signature here. Run capsulectl verify on the file before relying on it.",
  "page.refuse.signatures": "⚠️ This viewer can't check the signatures in this file here. Run capsulectl verify on the file before relying on it.",
  "page.refuse.theirs_yours": "A record shown as theirs is yours under another label.",
  "page.release.changed": "{note} What changed: ",
  "page.release.notes": "the release notes",
  "page.rests.sealed": "The words below were written by capsulectl when this page was made and sealed in this file; this page checked they have not changed since. They are capsulectl's summary of the steps, not the steps themselves.",
  "page.rests.unchecked": "The words below were written by capsulectl when this page was made, and this page does not check them. It checks the sealed records in this file, not those words.",
  "page.rests.unchecked_asked": "Only the words you asked, marked ✓ under More on this deal, are checked against their sealed record.",
  "page.same.deal": "the same deal",
  "page.same.exchange": "the same exchange",
  "page.say.copy": "“Make a copy for the other side”",
  "page.say.send": "“Send me this receipt”",
  "page.say.title": "Tell your agent",
  "page.scope.default": "This receipt covers this one deal. It is not a record of everything the agent did.",
  "page.settle.card": "a card payment; the card processor's report is not in this file",
  "page.settle.none": "no settlement record is in this file",
  "page.settle.other": "a {rail} payment; no settlement record is in this file",
  "page.settled.size": "size {band}",
  "page.settled.theirs": "in their record; ",
  "page.shared.checked": "A shared copy · checked",
  "page.shared.made_for": "Made for {who}. Left out of this copy: {left_out}.",
  "page.shared.note": "A shared copy for {who}. Left out of this copy: {left_out}.",
  "page.shared.unchecked": "A shared copy · not fully checked",
  "page.stamp.failed": "failed",
  "page.stamp.five": "Five words for any stamp: {words}.",
  "page.stamp.missing": "missing",
  "page.stamp.not_checked": "not checked",
  "page.stamp.passed": "passed",
  "page.stamp.unclear": "unclear",
  "page.stamp_line.asked_withheld": "The words asked are withheld from this copy",
  "page.stamp_line.happened": "\"It happened\" rests on what the agent reported",
  "page.stamp_line.no_steps": "No step is listed in this copy",
  "page.stamp_line.nothing_back": "Nothing points back from the other side's book",
  "page.stamp_line.same": "Their record and yours are about {same}",
  "page.stamp_line.sealed": "Your words are sealed exactly as you said them",
  "page.stamp_line.sealed_unchecked": "Your words could not be checked against their sealed record",
  "page.stamp_line.stamped_books": "Each side's record is stamped into its own book and can't be changed after",
  "page.stamp_line.step_missing": "A step's record could not be found in this deal's log",
  "page.stamp_line.steps_holder": "Each step's record is in the holder's book, and a change after would show",
  "page.stamp_line.steps_own": "Each step's record is in your book, and a change after would show",
  "page.stamp_line.witness_both": "An outside witness recorded both books' snapshots",
  "page.stamp_line.witness_holder": "An outside witness saw the holder's book",
  "page.stamp_line.witness_own": "An outside witness saw your book",
  "page.stamps.title": "The stamps",
  "page.stamps.under": "What is under it:",
  "page.step.unverified": "step not verified in this report",
  "page.step.withheld": "contents withheld",
  "page.theirs.absent": "Not in this file. The other side's own record, if it keeps one, is held in its book; this page does not show it and does not say whether it exists.",
  "page.theirs.asked_nothing": "No record. Your agent asked and nothing came back by {day}; that is written down too.",
  "page.theirs.exchange": "their record of the exchange",
  "page.theirs.failed": "Their record is in this file, but it did not check: failed. Do not rely on it.",
  "page.theirs.key": "Their key {key}…",
  "page.theirs.key_note": "Their reply's signature, and the checkpoint it names, are checked by capsulectl verify, not on this page.",
  "page.theirs.no_record": "No record. Nothing from their side is in this file.",
  "page.theirs.other": "The other side",
  "page.theirs.refused": "They answered that they would not share their record, on {day}.",
  "page.theirs.refused_unchecked": "A refusal in their name came back on {day}; its signature was not checked here.",
  "page.theirs.stamped": "Stamped into their book {when}",
  "page.theirs.title": "Their side",
  "page.theirs.verified": "Their side · in their book · as received",
  "page.theirs.words_not_stated": "Their words: not stated",
  "page.told.nothing": "Nothing about you was recorded as shared.",
  "page.told.only_sealed": "Only what the agent sealed as shared is listed here; something it told without sealing it is not.",
  "page.told.title": "What your agent told whom",
  "page.told_buyer.adjudicator": "What the seller's agent told the buyer",
  "page.told_buyer.counterparty": "What the seller's agent told you",
  "page.told_buyer.exact": "✓ These are the exact words sealed when the agent said them, checked when this page was built; this page does not check that they are true.",
  "page.told_buyer.own": "What your agent told the buyer",
  "page.told_buyer.unchecked": "⚠️ A statement here could not be checked against its sealed step.",
  "size.between": "{low} – {high}",
  "size.range": "{low} to under {high} {currency}",
  "size.under": "under {amount}",
  "size.zero": "zero amount, {currency}"
};
// END DEAL_VIEW_WORDS

// W is one consumer word of the page: the entry at key in the page's words
// (capsulectlDealPage.words, the words capsulectl puts on a page it writes
// when they differ from the built-in ones), else the built-in word in
// DEAL_VIEW_WORDS, whose {placeholders} are filled from vals, as capsulectl's
// Wording.Line fills them: a key with no word, or a placeholder with no value,
// words nothing. The built-in words are the module's own because a viewer that
// opens a bundle runs it with no capsulectlDealPage at all. Record data, the
// separators between a row's parts and the glyphs (✓ ⚠️) are not words and
// stay in code.
const W = (key, vals) => {
  const own = ((globalThis.capsulectlDealPage || {}).words || {})[key];
  const text = typeof own === "string" ? own : DEAL_VIEW_WORDS[key];
  if (typeof text !== "string") return "";
  let missing = false;
  const filled = text.replace(/\{([a-z_]+)\}/g, (_, k) => {
    const v = vals ? vals[k] : undefined;
    if (v === undefined || v === null) missing = true;
    return missing ? "" : String(v);
  });
  return missing ? "" : filled;
};

// WR is a refusal's word. A refusal is told by its text being there, so one
// never words nothing: a page with no word for it shows the key.
const WR = (key, vals) => W(key, vals) || key;

// dealJourneyLabel words one move of the sealed journey (capsule-cli#370)
// from the page's words, as capsulectl's Wording.Line words the report's
// journey: a key the page has no word for, or a placeholder with no value,
// words nothing. The facts are sealed without any pack's words: a reason is
// its token (an "other" reason is its own detail) and the classes told are
// tokens, which the page's words (capsulectl worded each reason and class
// already) put into words.
const dealJourneyLabel = (words, move) => {
  const text = words[move.key];
  if (typeof text !== "string") return "";
  const facts = move.facts && typeof move.facts === "object" ? move.facts : {};
  const vals = {};
  // An "other" reason's detail is the reason's value, never a placeholder
  // of its own: the report words the same values.
  for (const [k, v] of Object.entries(facts)) if (typeof v === "string" && k !== "detail") vals[k] = v;
  if (typeof facts.reason === "string") {
    const word = words["journey.reason." + facts.reason];
    vals.reason = facts.reason === "other" ? facts.detail : typeof word === "string" ? word : "";
  }
  if (Array.isArray(facts.classes)) {
    const named = facts.classes.map((c) => (typeof words["class." + c] === "string" ? words["class." + c] : String(c).replace(/_/g, " ")));
    vals.classes = named.length < 2 ? named.join("") : W("page.join_and", { list: named.slice(0, -1).join(", "), last: named[named.length - 1] });
  }
  let missing = false;
  const filled = text.replace(/\{([a-z_]+)\}/g, (_, k) => {
    if (typeof vals[k] !== "string") missing = true;
    return typeof vals[k] === "string" ? vals[k] : "";
  });
  return missing ? "" : filled;
};

// The model: everything render shows, read from the verified context and the
// page's build-time checks. Stricter than the core's gate in one place:
// membership must be proven, not unbound (canRender).
const dealViewVerified = (context) => {
  const verification = context.verification;
  return (
    verification !== undefined &&
    verification.graphClosure.status === "pass" &&
    verification.intervalCoverage.status === "pass" &&
    verification.perRecordMembership.status === "pass" &&
    Object.values(verification.capsuleResults).every((r) => r.ok) &&
    verification.disclosures.every((d) => d.status === "disclosure_match" || d.status === "withheld")
  );
};

// A copy's standalone report (capsule-cli#294) rides in x-deal-report/v0,
// on no log, bound to the deal checkpoint the copy is made at. The page
// checks what `capsulectl verify` checks: its signature, under the key
// that signed the deal's last step (each step's envelope verified here,
// its key_id held to the key it verified under); its disclosure; that it
// is this deal's report for a copy audience; that it binds this bundle's
// checkpoint and states that checkpoint's time; and that the bundle holds
// no report record. Anything less is refused, never shown as checked.
// The evidence record header types of a deal's steps on its book.
const dealStepTypes = ["deal_step", "step_completed", "step_failed"];
const dealHexBytes = (hex) => {
  if (typeof hex !== "string" || hex.length % 2 !== 0 || !/^[0-9a-f]*$/u.test(hex)) return undefined;
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return out;
};
const dealBytesHex = (bytes) => Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
const dealVerifiedKey = async (record) => {
  const sig = dealHexBytes(record && record.signature);
  if (!sig || typeof record.capsule_id !== "string" || typeof record.key_id !== "string") return undefined;
  const result = await EvidenceGraph.verifyProducerEnvelope(record.capsule_id, sig);
  if (!result || !result.ok || !result.publicKey) return undefined;
  const key = dealBytesHex(result.publicKey);
  return key === record.key_id ? key : undefined;
};
const dealStandaloneReport = async (context, bundle, x, sealedId) => {
  const refused = { refusal: WR("page.refuse.report") };
  if (!EvidenceGraph.ed25519Available()) {
    return { refusal: WR("page.refuse.report_signature") };
  }
  if (!x || typeof x !== "object" || Object.keys(x).length !== 2) return refused;
  const record = x.record;
  const input = x.agent_input;
  if (!record || !input || typeof input !== "object" || record.capsule_id !== sealedId || record.action_id !== "capsulectl-deal-report") return refused;
  const reportKey = await dealVerifiedKey(record);
  if (!reportKey) return refused;
  const memberships = ((bundle.completeness_certificate || {}).memberships) || {};
  let lastKey;
  let lastSeq = -1;
  for (const r of context.records) {
    const header = r && r.evidence_record_header;
    if (!header || !dealStepTypes.includes(header.record_type)) continue;
    const key = await dealVerifiedKey(r);
    if (!key) return refused;
    const seq = Number((((memberships[r.capsule_id] || {}).log_coordinates) || {}).seq);
    if (Number.isFinite(seq) && seq > lastSeq) {
      lastSeq = seq;
      lastKey = key;
    }
  }
  if (!lastKey || lastKey !== reportKey) return refused;
  const capsule = {};
  for (const [k, v] of Object.entries(record)) if (k !== "signature" && k !== "key_id") capsule[k] = v;
  const disclosed = await EvidenceGraph.verifyDisclosureEnvelope({ capsule, disclosures: { agent_input: input } });
  if (!disclosed || !disclosed.ok) return refused;
  const logId = ((bundle.completeness_certificate || {}).log_id) || "";
  const audiences = ["counterparty", "adjudicator", "keep"];
  if (input.type !== "deal_report" || input.profile !== "x-deal-v0" || "deal/" + input.deal_id !== logId || !audiences.includes(input.audience)) return refused;
  const cp = bundle.checkpoint || {};
  const bound = input.deal_checkpoint || {};
  if (Object.keys(bound).length !== 3 || bound.log_id !== logId || String(bound.mmr_size) !== String(cp.mmr_size) || bound.root !== cp.root) return refused;
  if (context.records.some((r) => r && r.action_id === "capsulectl-deal-report")) return refused;
  const report = input.report;
  if (!report || typeof report !== "object") return refused;
  const asOf = Date.parse((report.lifecycle || {}).as_of);
  if (!Number.isFinite(asOf) || asOf !== Date.parse(cp.timestamp)) return refused;
  return { report };
};

// lifecycleText is the lifecycle's sentence with the close date in the
// viewer's zone, as the page's times are (evidencebook-deal#57): capsulectl wrote
// the sentence with the close's UTC day (closed_date); the day closed_at falls
// on here replaces it. A report without those fields keeps its sentence.
const lifecycleText = (life) => {
  const t = Date.parse(life.closed_at);
  if (Number.isNaN(t) || typeof life.closed_date !== "string" || life.closed_date === "" || !life.text.includes(life.closed_date)) return life.text;
  return life.text.replace(life.closed_date, new Date(t).toLocaleDateString(undefined, { dateStyle: "medium" }));
};

// dealBuildsCompared compares the builds that sealed the steps with the build
// that made this page: page_producer when the plugin wrote one
// (capsule-cli#326), else the core version a page built inside capsule-cli
// carries. Versions compare only between builds of the same name. On a
// capsulectl-deal page a step sealed by capsulectl itself, with or without a
// recorded version, predates any capsulectl-deal: it is an earlier build, said
// apart, not an older version. The plugin names its own release notes; a page
// without them is from a build inside capsule-cli. buildNote is the sentence
// the page shows, empty when there is nothing to say.
const dealBuildsCompared = (builds, report) => {
  const pp = report.page_producer && typeof report.page_producer === "object" ? report.page_producer : {};
  const pageBuild = {
    name: typeof pp.name === "string" && pp.name ? pp.name : "capsulectl",
    version: typeof pp.version === "string" ? pp.version : typeof report.page_version === "string" ? report.page_version : "",
  };
  const earlier = (b) => b.producer === "capsulectl" && pageBuild.name === "capsulectl-deal";
  const olderBuilds = builds
    .filter((b) => !earlier(b) && b.producer === pageBuild.name && (b.version === undefined || versionBefore(b.version, pageBuild.version)))
    .map((b) => b.version || W("page.build.unrecorded"));
  const earlierBuilds = builds.filter(earlier).map((b) => (b.version === undefined ? b.name : `${b.producer} ${b.version}`));
  const releaseNotes = typeof report.release_notes === "string" && report.release_notes.startsWith("https://") ? report.release_notes : "https://github.com/action-state-group/capsule-cli/releases";
  const said = [];
  if (pageBuild.version && olderBuilds.length > 0) said.push(W("page.build.older", { versions: olderBuilds.join(", "), name: pageBuild.name, version: pageBuild.version }));
  if (pageBuild.version && earlierBuilds.length > 0) {
    said.push(W(earlierBuilds.length > 1 ? "page.build.earlier_other" : "page.build.earlier_one", { builds: earlierBuilds.join(", "), name: pageBuild.name, version: pageBuild.version }));
  }
  return { pageBuild, olderBuilds, earlierBuilds, releaseNotes, buildNote: said.join(" ") };
};

// The receipt's lines as Steven's 9b frame 8 states them (F8.1, F8.7;
// evidencebook-deal#94), each read from its record in this verified
// context. A step's deal record is its disclosed agent_input; its place
// is its book header's seq, and its record digest the header's one payload
// commitment, which a rel ref names (x-deal-v0 section 2). Nothing here
// hashes or verifies: order and band are figures derived from verified
// content (presentation-contract-v0 section 3).
const dealLogSteps = (context) => {
  const steps = [];
  for (const r of context.records) {
    const h = r && r.evidence_record_header;
    if (!h || !dealStepTypes.includes(h.record_type) || !Number.isInteger(h.seq)) continue;
    const input = EvidenceGraph.verifiedPayload(context, r.capsule_id, "agent_input");
    // A typed record (task-authority/v0 and the rest) names its type and
    // refs at its top level; an x-deal-v0 record, in its block.
    const deal = input && (input["x-deal-v0"] || (typeof input.type === "string" ? { record_type: input.type, refs: input.refs } : undefined));
    steps.push({
      id: r.capsule_id,
      seq: h.seq,
      digest: Array.isArray(h.payload_commitments) ? h.payload_commitments[0] : undefined,
      deal: deal && typeof deal === "object" ? deal : undefined,
      body: input && input.body && typeof input.body === "object" ? input.body : {},
      settlement: r.settlement && typeof r.settlement === "object" ? r.settlement : undefined,
    });
  }
  return steps;
};
const dealRefTo = (deal, rel) => ((deal && Array.isArray(deal.refs) ? deal.refs : []).find((r) => r && r.rel === rel) || {}).digest;

// "After your yes" (F8.1), recomputed: each act (a step that cites what
// authorized it) is preceded in this log by that authority, each at an
// earlier position: an approval that proceeds, answering a verdict that
// answers a check; or, on typed records, an evaluation that let it go ahead
// under the task's authority (DO), answering the proposed action. An act
// sealed without an approval (an unchecked_action outcome, x-deal or
// typed; a disclosure with no authority; a claim sealed unchecked) fails,
// named; an authority withheld from this copy is missing, and a copy with
// any step withheld is not checked, since that step may be an act.
const dealApprovalTypes = ["approval", "action-approval/v0"];
const dealEvaluationTypes = ["verdict", "action-evaluation/v0"];
const dealCheckTypes = ["check", "proposed-action/v0"];
const dealAfterYes = (steps) => {
  const byDigest = new Map(steps.filter((s) => typeof s.digest === "string").map((s) => [s.digest, s]));
  const earlier = (digest, than, types) => {
    const s = byDigest.get(digest);
    if (!s) return "absent";
    if (!s.deal) return "withheld";
    return s.seq < than.seq && types.includes(s.deal.record_type) ? s : "bad";
  };
  const acts = [];
  for (const s of steps) {
    if (!s.deal) continue;
    const t = s.deal.record_type;
    if ((t === "outcome" || t === "action-outcome/v0") && s.body.status === "unchecked_action") return { state: "failed", act: s, why: "unapproved" };
    if (t === "disclosure" && s.body.authority === "none") return { state: "failed", act: s, why: "unapproved" };
    if (t === "claim" && s.body.unchecked === true) return { state: "failed", act: s, why: "unapproved" };
    if (dealRefTo(s.deal, "authorized_by") !== undefined || t === "action" || (t === "disclosure" && s.body.authority === "approval")) acts.push(s);
  }
  const withheld = steps.some((s) => !s.deal);
  if (acts.length === 0) return { state: withheld ? "not checked" : "none", approvers: [] };
  const approvers = [];
  let missing;
  for (const act of acts) {
    const by = earlier(dealRefTo(act.deal, "authorized_by"), act, [...dealApprovalTypes, ...dealEvaluationTypes]);
    let evaluation = by;
    let approver;
    if (typeof by === "object" && dealApprovalTypes.includes(by.deal.record_type)) {
      if (by.body.proceed !== true) return { state: "failed", act, why: "order" };
      const answered = dealRefTo(by.deal, "approves");
      evaluation = earlier(answered, by, dealEvaluationTypes);
      // x-deal-v0 section 6 rule 5: only the verdict's first answer
      // authorizes; a later one ("Hold", then "Pay anyway") never does.
      if (steps.some((s) => s.deal && dealApprovalTypes.includes(s.deal.record_type) && s.seq < by.seq && dealRefTo(s.deal, "approves") === answered)) return { state: "failed", act, why: "order" };
      if (by.deal.record_type === "approval") {
        approver = by.body.approver;
        // Standing intent approves only a passing verdict (rule 6).
        if (approver === "standing_intent" && typeof evaluation === "object" && evaluation.body.result !== "pass") return { state: "failed", act, why: "order" };
      } else {
        // A typed ASK is answered only by the user's own approval.
        if (by.body.authority !== "user_approval") return { state: "failed", act, why: "order" };
        approver = "user";
      }
    } else if (typeof by === "object") {
      if (by.body.disposition !== "DO") return { state: "failed", act, why: "order" };
      approver = "task_authority";
    }
    const check = typeof evaluation === "object" ? earlier(dealRefTo(evaluation.deal, "checks"), evaluation, dealCheckTypes) : evaluation;
    if (check === "withheld") {
      missing = missing || act;
      continue;
    }
    if (typeof check === "string") return { state: "failed", act, why: "order" };
    // The check asked about this very act (rule 5).
    if (typeof act.body.action === "string" && check.body.action !== act.body.action) return { state: "failed", act, why: "order" };
    if (approver !== undefined && !approvers.includes(approver)) approvers.push(approver);
  }
  if (missing) return { state: "missing", act: missing, approvers };
  return { state: withheld ? "not checked" : "passed", approvers };
};

// The payment's settlement (F8.1), per rail as its adapter states it
// (agent-action-capsule spec/adapters; Mesh-LLM/capsules-plugin
// adapters/lightning-v0.md): the settled payee-observed legs in this log.
// The page holds no check of a rail's proof (a preimage is a digest, which
// only the runtime may compute), so nothing here says passed. The band is
// lightning-v0's "Band": the side's settled amounts summed, as
// (currency, decade), decade = digits - assetScale - 1, never converted.
const dealSettlement = (steps) => {
  const legs = steps.filter((s) => s.settlement && s.settlement.leg === "payee_observed" && s.settlement.status === "settled");
  if (legs.length === 0) {
    // No leg: the rail a paying act names, so the line can say whose report is not here.
    const paying = steps.find((s) => s.deal && s.deal.record_type === "action" && typeof s.body.rail === "string" && s.body.rail !== "");
    return paying ? { rail: paying.body.rail, legs: false } : undefined;
  }
  const ref = (s) => ((s.settlement.payment_ref || {}).type || "");
  const rail = legs.every((s) => ref(s) === "ln.payment_hash") ? "lightning" : legs.every((s) => ref(s).startsWith("x402.")) ? "x402" : "other";
  // A leg's record withheld from this copy hides whether it holds a preimage.
  const preimage = legs.some((s) => !s.deal) ? "withheld" : legs.every((s) => typeof s.body.preimage === "string" && s.body.preimage !== "");
  let band;
  const amounts = legs.flatMap((s) => [s.settlement.received, s.settlement.receive_fee]).filter((a) => a && typeof a === "object");
  const currency = amounts.length > 0 ? amounts[0].assetCode : undefined;
  const scale = amounts.length > 0 ? Number(amounts[0].assetScale) : NaN;
  if (typeof currency === "string" && Number.isInteger(scale) && amounts.every((a) => a.assetCode === currency && Number(a.assetScale) === scale && /^(0|[1-9][0-9]*)$/.test(String(a.value)))) {
    const sum = amounts.reduce((acc, a) => acc + BigInt(String(a.value)), 0n);
    band = { currency, decade: sum === 0n ? null : String(sum).length - scale - 1 };
  }
  return { rail, legs: true, preimage, band };
};

// The band's words, as the History card names a size (capsule-viewer
// static/standard_history_card.js bandLabel, from the same strings,
// size.*): a currency with a symbol as "$10 – $99", every band below its
// first ten units as "under $10"; another currency in its own units.
const dealBandSymbols = { USD: "$", EUR: "€", GBP: "£" };
const dealPower = (d) => (d > 18 || d < -18 ? "1e" + d : d >= 0 ? "1" + "0".repeat(d) : "0." + "0".repeat(-d - 1) + "1");
const dealGrouped = (digits) => digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
// A leg names Bitcoin by its CAIP-19 asset id (deal_settlement_lightning.go,
// lightningBTC); the band names it by its code, as lightning-v0 writes it.
const dealAssetCodes = { "bip122:000000000019d6689c085ae165831e93/slip44:0": "BTC" };
const dealBandLabel = (shown) => {
  const band = { ...shown, currency: Object.prototype.hasOwnProperty.call(dealAssetCodes, shown.currency) ? dealAssetCodes[shown.currency] : shown.currency };
  const sym = Object.prototype.hasOwnProperty.call(dealBandSymbols, band.currency) ? dealBandSymbols[band.currency] : undefined;
  if (sym && (band.decade === null || band.decade <= 17)) {
    if (band.decade === null || band.decade <= 0) return W("size.under", { amount: sym + "10" });
    return W("size.between", { low: sym + dealGrouped(dealPower(band.decade)), high: sym + dealGrouped(dealPower(band.decade + 1).slice(1).replace(/0/g, "9")) });
  }
  if (band.decade === null) return W("size.zero", { currency: band.currency });
  return W("size.range", { low: dealPower(band.decade), high: dealPower(band.decade + 1), currency: band.currency });
};

// The deal's close, as the other side's records answer it (F8.1, F8.7):
// evidencebook's StatusOfClose over their verified half, which the caller
// has held to their key. A record of their book that rebuts your close
// makes it contested; one that acknowledges it, agreed; neither leaves it
// unilateral. Only their record pointing at yours makes it agreed.
const dealCloseOf = (steps) => {
  const closes = steps.filter((s) => s.deal && s.deal.record_type === "close");
  const last = closes[closes.length - 1];
  // With a step withheld from this copy, no close here does not mean none.
  if (!last) return steps.some((s) => !s.deal) ? { withheld: true } : undefined;
  return { id: last.id };
};
const dealCloseStatus = (closeId, theirRecords, theirBook) => {
  let status = "unilateral";
  for (const r of theirRecords || []) {
    const h = r && r.evidence_record_header;
    if (!h || h.book_id !== theirBook || !Array.isArray(h.links)) continue;
    const to = (type) => h.links.some((l) => l && l.type === type && l.target === closeId);
    if (to("rebuts")) return "contested";
    if (to("acknowledges")) status = "agreed";
  }
  return status;
};

const dealViewModel = async (context) => {
  const page = globalThis.capsulectlDealPage || {};
  // The context's own frozen copy of the bundle, read only for what no
  // record seals: the extension blocks.
  const bundle = context.bundle;
  const payload = (id) => EvidenceGraph.verifiedPayload(context, id, "agent_input");
  // In the verifier's disclosure order, as the context resolved each one.
  const matched = [
    ...new Set(
      context.verification.disclosures
        .filter((d) => d.member === "agent_input" && EvidenceGraph.disclosureOf(context, d.capsuleId, "agent_input").state === "disclosed")
        .map((d) => d.capsuleId),
    ),
  ];
  // The deal's text: the input of the sealed report the x-deal-v0 extension
  // points at, checked like every record (its disclosure matched). A bundle
  // written before reports were sealed carries the text in the extension
  // itself, which no record seals, and the page says so.
  const dealExt = (bundle.extensions || {})["x-deal-v0"] || {};
  const sealedId = typeof dealExt.sealed_report === "string" ? dealExt.sealed_report : undefined;
  const otherReports = matched.filter((id) => {
    const input = payload(id);
    return id !== sealedId && input && input.type === "deal_report";
  });
  // A sealed report's record stays in every later bundle (it is signed, and
  // on the log the bundle covers), so a bundle holding one whose extension
  // names none had its text swapped for unsealed text: refused, never shown
  // under the "not checked" label.
  const reportRecords = context.records.filter((r) => r && r.action_id === "capsulectl-deal-report");
  let report = dealExt;
  // The deal a sealed report names: on a deal's own log the log says it; on
  // a node's one ledger (a Mesh payer's receipt) the report's payload does.
  let reportDealId = "";
  const standalone = (bundle.extensions || {})["x-deal-report/v0"];
  // A copy of a deal on a book whose log holds no report record names a
  // standalone one: inline text there is a stripped copy, refused.
  const logId = ((bundle.completeness_certificate || {}).log_id) || "";
  const bookSteps = context.records.some((r) => r && r.evidence_record_header && dealStepTypes.includes(r.evidence_record_header.record_type));
  if (standalone !== undefined) {
    if (sealedId === undefined || Object.keys(dealExt).length !== 1) {
      return { refusal: WR("page.refuse.report") };
    }
    const checked = await dealStandaloneReport(context, bundle, standalone, sealedId);
    if (checked.refusal) return checked;
    report = checked.report;
  } else if (sealedId !== undefined || otherReports.length > 0 || reportRecords.length > 0) {
    const input = sealedId !== undefined ? payload(sealedId) : undefined;
    if (otherReports.length > 0 || !input || input.type !== "deal_report" || typeof input.report !== "object" || input.report === null) {
      return { refusal: WR("page.refuse.record") };
    }
    report = input.report;
    if (typeof input.deal_id === "string") reportDealId = input.deal_id;
  } else if (bookSteps) {
    return { refusal: WR("page.refuse.report") };
  }
  const shared = report.audience === "counterparty" || report.audience === "adjudicator";
  const base = payload(report.asked_step);
  if (!shared && (!base || !base["x-deal-v0"] || base["x-deal-v0"].record_type !== "baseline")) {
    return { refusal: WR("page.refuse.opening") };
  }

  const logSteps = dealLogSteps(context);

  // Which build sealed the steps, read from the records this page verified
  // (not from the summary), compared with the build that made this page.
  // Both are already in this file: nothing is fetched to do it.
  const builds = [];
  matched.forEach((id) => {
    const rec = payload(id) || {};
    // An x-deal-v0 record names its producer in its block; a typed record, in its header.
    const p = (rec["x-deal-v0"] || rec).producer;
    const producer = (p && p.name) || "capsulectl";
    const name = p && typeof p.version === "string" ? `${producer} ${p.version} (${p.commit || W("page.build.commit_unknown")})` : W("page.build.earlier_name");
    if (!builds.some((b) => b.name === name)) builds.push({ name, producer, version: p && p.version });
  });
  const { pageBuild, olderBuilds, earlierBuilds, releaseNotes, buildNote } = dealBuildsCompared(builds, report);

  // Each statement's class, as sealed with it (undefined when its sealed step
  // holds no such claim).
  const claimClasses = (report.representations || []).map((r) => {
    const rec = payload(r.step);
    const body = rec && rec.body;
    const claim = body && (Number.isInteger(r.index) ? (body.claims || [])[r.index] : body);
    return claim ? claim.class : undefined;
  });

  return {
    page,
    report,
    sealed: sealedId !== undefined,
    standalone: standalone !== undefined,
    shared,
    demo: !!(base && base.body && base.body.demo),
    matched,
    recorded: [...context.recordIndex.keys()],
    builds,
    pageBuild,
    olderBuilds,
    earlierBuilds,
    releaseNotes,
    buildNote,
    claimClasses,
    dealId: logId.startsWith("deal/") ? logId.slice(5) : reportDealId,
    witnessed: (bundle.extensions || {})["x-cadence-witness/v0"] !== undefined,
    // Steps sealed before the deal store was a book (capsule-cli#242) have
    // no place in one: their lines are not read.
    lines: logSteps.length > 0 ? { afterYes: dealAfterYes(logSteps), settlement: dealSettlement(logSteps), close: dealCloseOf(logSteps) } : undefined,
  };
};

// Inserted once, at the head: its bytes hash to the manifest's style_sha256,
// which the page's Content-Security-Policy lists.
// The receipt with the other party's half (9b frames 6 and 7,
// docs/design/two-sided-receipt.md): a composed/v1 bundle whose root is the
// composer's deal composition record, read by capsulectl.deal-view-composed/v0.
// Your half is the composer's keep copy, carried in the page's bootstrap
// (capsulectlDealPage.keep), or the containing bundle's own deal records
// (a Mesh node's one ledger). Their half is each member another party
// answered with, and each absence your agent recorded. The page states only
// what it checked itself: the block's join states are never read as
// "stamped by both".
const dealCompositionActionID = "capsulectl-deal-composition";
const dealRootRecord = (bundle, records) => records.find((r) => r && r.capsule_id === bundle.root);
const dealComposedResult = (context) => {
  const x = (context.verification.extensions || []).find((e) => e && e.kind === "composed/v1");
  return x && x.composed;
};
// The root a deal composition is sealed under (V0's first check); any other
// composition is not this module's.
const dealComposedCanRender = (context) => {
  const root = dealRootRecord(context.bundle, context.records);
  return root !== undefined && root.action_id === dealCompositionActionID && dealComposedResult(context) !== undefined;
};
const dealNonceSources = ["client_supplied", "host_forwarded_nonce"];
const dealMeshHalf = (record) => {
  const poc = (((record || {}).model_attestation || {}).compute_attestation || {})["x-mesh-poc-v1"];
  return poc && typeof poc === "object" ? poc : undefined;
};
// A Mesh half names one exchange only by a client nonce the client sent:
// the placeholder sources name none (D4).
const dealExchangeNonce = (record) => {
  const poc = dealMeshHalf(record);
  return poc && dealNonceSources.includes(poc.client_nonce_source) && typeof poc.client_nonce === "string" && poc.client_nonce !== "" ? poc.client_nonce : undefined;
};
const dealRequestDigest = (record) => (record && record.effect && typeof record.effect.request_digest === "string" ? record.effect.request_digest : undefined);

// Whether a carried response names the member bundle's checkpoint as its
// anchor (V3). The deal wire's response names it `root:mmr_size`. An
// evidence-interop envelope names it by the digest of the checkpoint's
// octets, which this module does not compute (it never hashes): that
// binding, like the envelope's signature, is capsulectl verify's, and the
// page only holds the envelope to its profile.
const dealInteropProfiles = ["evidence-interop/0", "evidence-interop/1"];
const dealAnchorIsCheckpoint = (response, cp) => {
  if (typeof response.anchor === "string") return response.anchor === `${cp.root}:${cp.mmr_size}`;
  return dealInteropProfiles.includes(response.profile) && !!response.anchor && typeof response.anchor.digest === "string";
};

// One of their artifact members, checked V1 to V4. It returns the member's
// state and, for the box, what its sealed record says.
// The payer's citing record for a received half (a Mesh half that was also
// pushed): compute_attestation.received_half, naming the half it cites.
const dealCitingRecord = (records, cited) =>
  records
    .map((r) => (((r || {}).model_attestation || {}).compute_attestation || {}).received_half)
    .find((h) => h && typeof h === "object" && h.cited_capsule_id === cited);
const dealTheirArtifact = async (member, dealID, mine, meshOwn, payerRecords) => {
  const witnessed = ((member.bundle || {}).extensions || {})["x-cadence-witness/v0"] !== undefined;
  const ctx = await EvidenceGraph.buildVerifiedBundleContext(member.bundle);
  const root = dealRootRecord(ctx.bundle, ctx.records);
  const mesh = dealMeshHalf(root) !== undefined;
  const failed = (check) => ({ state: "failed", check, witnessed, mesh });
  // V1: as strict as a single-book page.
  if (!dealViewVerified(ctx)) return failed("V1");
  const cp = ctx.bundle.checkpoint || {};
  // V2: their half's own signature, under the observer it is declared as,
  // anchored under that observer's custody domain.
  if (!root || (await dealVerifiedKey(root)) !== member.observer || cp.key_id !== member.custody) return failed("V2");
  // A Mesh half that was also pushed has the payer's citing record: the key
  // it accepted the half under is their observer, and not one operator on
  // both sides (D4). An ask-only half has none, which proves nothing.
  const citing = mesh ? dealCitingRecord(payerRecords, root.capsule_id) : undefined;
  if (citing && (citing.sender_key_id !== member.observer || citing.same_controller_as_receiver === true)) return failed("V2");
  // V3 beyond the core: the response answered with this very checkpoint.
  if (!member.response || !dealAnchorIsCheckpoint(member.response, cp)) return failed("V3");
  const at = cp.timestamp;
  if (mesh) {
    // V4 for Mesh: the same exchange as the payer's own half.
    const nonce = dealExchangeNonce(root);
    if (!meshOwn || nonce === undefined || nonce !== dealExchangeNonce(meshOwn) || dealRequestDigest(root) === undefined || dealRequestDigest(root) !== dealRequestDigest(meshOwn)) return failed("V4");
    return { state: "verified", mesh: true, at: root.timestamp, stamped: at, key: member.observer, witnessed };
  }
  // V4 for a deal copy: their copy of this deal, its text checked as a
  // single-book page checks it.
  const theirs = await dealViewModel(ctx);
  if (theirs.refusal !== undefined) return failed("V1");
  if (theirs.dealId !== dealID || (mine && mine.dealId !== dealID)) return failed("V4");
  return { state: "verified", mesh: false, report: theirs.report, stamped: at, key: member.observer, witnessed, lines: theirs.lines, records: ctx.records, book: ((ctx.bundle.completeness_certificate || {}).log_id) || "" };
};

const dealComposedModel = async (context) => {
  const refused = (why) => ({ refusal: WR("page.refuse.composed", { why }) });
  const bundle = context.bundle;
  // Every half here is checked by its signature: a viewer that cannot check
  // Ed25519 says so, and never calls their record bad (Sherlock2, #86).
  if (!EvidenceGraph.ed25519Available()) {
    return { refusal: WR("page.refuse.signatures") };
  }
  // V0: the composition itself.
  if (!dealViewVerified(context)) return refused(W("page.refuse.composition_unverified"));
  const root = dealRootRecord(bundle, context.records);
  const composer = await dealVerifiedKey(root);
  const input = EvidenceGraph.verifiedPayload(context, root.capsule_id, "agent_input");
  const block = (bundle.extensions || {})["composed/v1"] || {};
  const composed = dealComposedResult(context);
  if (!composer || !input || input.type !== "deal_composition" || input.profile !== "x-deal-v0" || typeof input.deal_id !== "string") {
    return refused(W("page.refuse.composition_record"));
  }
  const memberFailed = (r) => r.digest === "mismatch" || r.refusalSignature === "invalid" || r.responseSignature === "invalid" || (r.bundle && Object.values(r.bundle).some((c) => c.status === "fail")) || r.findings.length > 0;
  if (
    composed.malformed ||
    composed.status === "fail" ||
    !composed.composedDigest.matches ||
    input.composed_digest !== block.composed_digest ||
    composed.compositionClosure.status !== "pass" ||
    composed.joins.some((j) => j.result === "join_state_mismatch") ||
    composed.members.some(memberFailed)
  ) {
    return refused(W("page.refuse.composition_holds"));
  }
  const dealID = input.deal_id;
  const custody = (bundle.checkpoint || {}).key_id;

  // Your half (D2).
  const page = globalThis.capsulectlDealPage || {};
  let mine;
  let mineContext;
  if (page.keep !== undefined) mineContext = await EvidenceGraph.buildVerifiedBundleContext(page.keep);
  else if ((bundle.extensions || {})["x-deal-v0"] !== undefined) mineContext = context;
  if (mineContext !== undefined) {
    const mineRoot = dealRootRecord(mineContext.bundle, mineContext.records);
    if (!dealViewVerified(mineContext)) return refused(W("page.refuse.own_unverified"));
    mine = await dealViewModel(mineContext);
    if (mine.refusal !== undefined) return mine;
    // On a node's one ledger the root is the composition itself, so the
    // sealed report the half is read from is held to the composer's key too.
    const sealedId = ((mineContext.bundle.extensions || {})["x-deal-v0"] || {}).sealed_report;
    const sealedRecord = mineContext.records.find((r) => r && r.capsule_id === sealedId);
    const sealedKey = sealedRecord ? await dealVerifiedKey(sealedRecord) : composer;
    if (mine.report.audience !== "keep" || !mineRoot || (await dealVerifiedKey(mineRoot)) !== composer || sealedKey !== composer || mine.dealId !== dealID) {
      return refused(W("page.refuse.own_not_composers"));
    }
  }
  // The payer's own exchange half, on a Mesh composition: the record its
  // sealed report cites in did[].steps.
  let meshOwn;
  if (mine && mineContext === context) {
    const cited = new Set((mine.report.did || []).flatMap((d) => (Array.isArray(d.steps) ? d.steps : [])));
    meshOwn = context.records.find((r) => r && cited.has(r.capsule_id) && dealMeshHalf(r));
    // A Mesh composition whose payer half its report does not cite does not
    // say which exchange it is about (D4).
    if (!meshOwn && (block.members || []).some((m) => m.outcome === "artifact" && dealMeshHalf(dealRootRecord(m.bundle || {}, (m.bundle || {}).records || [])))) {
      return refused(W("page.refuse.own_uncited"));
    }
  }

  // Their members (D2), and that they are not you.
  const observers = new Map((block.observers || []).map((o) => [o.id, o]));
  const results = new Map(composed.members.map((r) => [r.id, r]));
  const others = new Set();
  const theirs = [];
  for (const m of block.members || []) {
    if (m.outcome === "absence") {
      theirs.push({ outcome: "absence", state: "absent", at: ((m.absence || {}).window || {}).to });
      continue;
    }
    if (m.observer === composer) continue;
    const obs = observers.get(m.observer) || {};
    others.add(m.observer);
    // They are not you (D2): none of their observer, its custody domain or
    // (for an artifact) its root's key is your key or your checkpoint key.
    const mRoot = m.outcome === "artifact" ? dealRootRecord(m.bundle || {}, (m.bundle || {}).records || []) : undefined;
    const yours = new Set([composer, custody]);
    if ([m.observer, obs.custody_domain, mRoot && mRoot.key_id].some((k) => typeof k === "string" && yours.has(k))) {
      return refused(W("page.refuse.theirs_yours"));
    }
    if (m.outcome === "refusal") {
      const r = results.get(m.id) || {};
      theirs.push({ outcome: "refusal", state: r.refusalSignature === "verified" ? "refused" : "refused_unchecked", at: (m.refusal || {}).issued_at });
      continue;
    }
    theirs.push({ outcome: "artifact", ...(await dealTheirArtifact({ ...m, custody: obs.custody_domain }, dealID, mine, meshOwn, context.records)) });
  }
  if (others.size > 1) return refused(W("page.refuse.many_parties"));

  // The case (D3), in order.
  const latest = (state) => theirs.filter((t) => t.state === state || (state === "refused" && t.state === "refused_unchecked")).sort((a, b) => String(b.at).localeCompare(String(a.at)))[0];
  let theirCase;
  let shown;
  if ((shown = theirs.find((t) => t.state === "failed"))) theirCase = "only";
  else if ((shown = theirs.find((t) => t.state === "verified"))) theirCase = "both";
  else if ((shown = latest("refused"))) theirCase = "only";
  else if ((shown = latest("absent"))) theirCase = "only";
  else theirCase = "none";
  // Without your own copy the page still draws its frame: an empty report,
  // nothing checked on your side.
  const base = mine || { page, report: { steps: [], did: [] }, sealed: false, standalone: false, shared: false, demo: false, matched: [], recorded: [], builds: [], ...dealBuildsCompared([], {}), claimClasses: [], dealId: dealID, witnessed: false };
  return { ...base, composed: { mine: mine !== undefined, theirCase, shown, also: theirs.filter((t) => t !== shown).sort((a, b) => String(a.at).localeCompare(String(b.at))), dealId: dealID } };
};

const DEAL_VIEW_CSS = `#deal { --deal-warn: #9a3b00; --deal-ok: #1d6b35; --deal-ours: #e4edf5; --deal-ours-line: #2b5a85; --deal-theirs: #fbefd9; --deal-theirs-line: #8a6a2f; --deal-none: #eceae5; --deal-none-line: #a9afb8; --deal-good: #e3f1ea; --deal-good-line: #2e7d5b; --deal-pill: #f4f1ea; --deal-ink: #232f42; line-height: 1.45; overflow-wrap: anywhere; max-width: 34rem; margin: 0 auto; }
@media (prefers-color-scheme: dark) { #deal { --deal-warn: #ffb07a; --deal-ok: #7fd49a; --deal-ours: #1d2c3d; --deal-ours-line: #6fa3d4; --deal-theirs: #3a2f1a; --deal-theirs-line: #d1a85c; --deal-none: #2a2c30; --deal-none-line: #6b717b; --deal-good: #173326; --deal-good-line: #4fae83; --deal-pill: #2b2a27; --deal-ink: #e8edf5; } }
#deal h1 { font-size: 1.2rem; margin: 0; }
#deal h2 { font-size: 1.15rem; margin-top: 1.6rem; }
#deal h3 { font-size: 0.95rem; color: var(--aac-muted); margin: 1rem 0 0.3rem; }
#deal .deal-flag summary { color: var(--deal-warn); }
#deal .deal-steps { margin: 8px 0 0; padding-left: 1.4rem; }
#deal .deal-steps li { margin: 4px 0; overflow-wrap: anywhere; }
#deal .deal-at { color: var(--aac-muted); font-size: 0.85rem; }
#deal .deal-note { color: var(--aac-muted); font-size: 0.9rem; }
#deal .deal-demo { display: inline-block; border: 1px solid var(--deal-warn); color: var(--deal-warn); padding: 0 6px; border-radius: 4px; font-size: 0.8rem; }
#deal .deal-bad { color: var(--deal-warn); font-weight: 600; }
#deal details { border: 1px solid var(--aac-line); border-radius: 6px; padding: 8px 12px; margin: 8px 0; }
#deal summary { cursor: pointer; font-weight: 600; }
#deal table.deal-merchant { border-collapse: collapse; width: 100%; margin: 6px 0; }
#deal table.deal-merchant th, #deal table.deal-merchant td { border: 1px solid var(--aac-line); padding: 4px 8px; text-align: left; vertical-align: top; overflow-wrap: anywhere; }
#deal table.deal-merchant th { color: var(--aac-muted); font-weight: 600; width: 40%; }
#deal .deal-ok { color: var(--deal-ok); font-weight: 600; }
#deal code { color: var(--aac-muted); font-size: 0.8rem; overflow-wrap: anywhere; }
#deal .deal-assurance { border: 1px solid var(--aac-line); border-radius: 6px; padding: 8px 12px; margin: 8px 0 16px; }
#deal .deal-rung { font-weight: 600; margin: 0.2rem 0; }
#deal .deal-scope { font-weight: 600; margin: 0.2rem 0 0.6rem; }
#deal .deal-claims { border: none; padding: 0; }
#deal .deal-bar { display: flex; align-items: baseline; gap: 10px; padding: 4px 0 10px; border-bottom: 1px solid var(--aac-line); }
#deal .deal-bar span { margin-left: auto; color: var(--aac-muted); font-size: 0.85rem; }
#deal .deal-col { display: flex; flex-direction: column; gap: 12px; padding-top: 12px; }
#deal .deal-box { border: 1px solid var(--aac-line); border-radius: 12px; padding: 12px 14px; }
#deal .deal-banner { border: 1px solid var(--deal-theirs-line); background: var(--deal-theirs); border-radius: 12px; padding: 12px 14px; }
#deal .deal-banner b { display: block; }
#deal .deal-small { font-size: 0.8rem; color: var(--aac-muted); }
#deal .deal-caps { font-size: 0.72rem; letter-spacing: 0.06em; text-transform: uppercase; color: var(--aac-muted); }
#deal .deal-card { border: 2px solid var(--deal-ink); border-radius: 12px; padding: 12px 14px; display: flex; flex-direction: column; gap: 6px; }
#deal .deal-quote { font-size: 1.05rem; font-weight: 600; }
#deal .deal-half { border-radius: 12px; padding: 12px 14px; display: flex; flex-direction: column; gap: 6px; }
#deal .deal-ours { background: var(--deal-ours); border: 1px solid var(--deal-ours-line); }
#deal .deal-theirs { background: var(--deal-theirs); border: 1px solid var(--deal-theirs-line); }
#deal .deal-none { background: var(--deal-none); border: 1px dashed var(--deal-none-line); }
#deal .deal-stamps { border-radius: 12px; }
#deal .deal-pill { font-size: 0.75rem; padding: 2px 8px; border-radius: 999px; background: var(--deal-pill); font-weight: 600; margin-left: 8px; }
#deal .deal-stamp { display: flex; gap: 8px; padding: 7px 0; border-top: 1px solid var(--aac-line); }
#deal .deal-stamp b.deal-mark { flex: none; }
#deal .deal-list { list-style: none; margin: 6px 0 0; padding: 0; }
#deal .deal-list li { display: flex; gap: 10px; padding: 8px 0; border-top: 1px solid var(--aac-line); }
#deal .deal-list li:first-child { border-top: none; }
#deal .deal-dot { width: 10px; height: 10px; border-radius: 50%; margin-top: 6px; flex: none; background: var(--deal-none-line); }
#deal .deal-dot.deal-yes { background: var(--deal-good-line); }
#deal .deal-say { background: var(--deal-ours); border: 1px solid var(--deal-ours-line); border-radius: 10px; padding: 12px 14px; display: flex; flex-direction: column; gap: 6px; }
#deal .deal-say b { color: var(--deal-ours-line); }
`;

// The stamps ladder's five consumer words (9b, "The stamps"). A stamp the
// page did not check says "not checked", never "passed"; one side missing is
// "missing", never "failed"; "failed" is only a check that ran and did not hold.
const dealStampWords = ["passed", "failed", "missing", "not checked", "unclear"];
// The consumer word shown for a stamp's state (the state itself is a token).
const dealStampWord = (state) => W("page.stamp." + state.replace(/ /g, "_"));

const dealViewRender = (model, regions) => {
  if (!document.getElementById("capsulectl-deal-view-style")) {
    const style = document.createElement("style");
    style.id = "capsulectl-deal-view-style";
    style.textContent = DEAL_VIEW_CSS;
    document.head.append(style);
  }
  const host = document.createElement("div");
  host.id = "deal";
  regions.L0.append(host);
  const el = (tag, text, cls) => {
    const node = document.createElement(tag);
    if (text !== undefined) node.textContent = text;
    if (cls) node.className = cls;
    return node;
  };
  if (model.refusal !== undefined) {
    host.append(el("p", model.refusal, "deal-bad"));
    return;
  }
  const { page, report, sealed, shared } = model;
  const matched = new Set(model.matched);
  const recorded = new Set(model.recorded);
  const byId = new Map((report.steps || []).map((s) => [s.capsule_id, s]));
  const recordOk = (id) => recorded.has(id);
  // Today as the reader's own calendar has it (YYYY-MM-DD, to compare with
  // the report's dates).
  const localDay = () => {
    const d = new Date();
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  };
  // A YYYY-MM-DD date as a reader says it ("Oct 22, 2026").
  const readDay = (ymd) => {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(ymd);
    return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])).toLocaleDateString(undefined, { dateStyle: "medium" }) : ymd;
  };
  // Times as the reader's own clock shows them (capsule-cli#341); a value
  // that is not a time is shown as written.
  const local = (at) => {
    const t = Date.parse(at);
    return Number.isNaN(t) ? at : new Date(t).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  };
  // The receipt's own short time: "Oct 7, 08:12", no year, on a 24-hour clock.
  const short = (at) => {
    const t = Date.parse(at);
    return Number.isNaN(t) ? at : new Date(t).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
  };
  const shortDay = (at) => {
    const t = Date.parse(at);
    return Number.isNaN(t) ? at : new Date(t).toLocaleDateString(undefined, { month: "short", day: "numeric" });
  };
  // In "What the agent did", each step is listed once, under the first item
  // read from it: an approval is the check's, not again the act's
  // (capsule-cli#341). The other sections keep every step they cite.
  let listed;
  const steps = (ids) => {
    const list = el("ol", undefined, "deal-steps");
    ids.forEach((id) => {
      if (listed) {
        if (listed.has(id)) return;
        listed.add(id);
      }
      const step = byId.get(id);
      const li = el("li");
      if (step && matched.has(id)) {
        li.append(el("span", `${local(step.at)} · `, "deal-at"), el("span", step.line));
      } else if (shared && step && step.withheld && recordOk(id)) {
        // A withheld step: its record and its place in the log verified;
        // its contents are not in this copy.
        li.append(el("span", `${local(step.at)} · `, "deal-at"), el("span", step.line), el("span", ` · ${W("page.step.withheld")}`, "deal-at"));
      } else {
        li.append(el("span", `${W("page.step.unverified")} `), el("code", id));
      }
      list.append(li);
    });
    return list;
  };
  // An item's own step (an act, a close) is the item: its line would say
  // the same thing again, so it is checked but not listed.
  const item = (text, ids, cls, self) => {
    const d = el("details", undefined, cls);
    if (typeof self === "string" && self) {
      const step = byId.get(self);
      const withheld = shared && step && step.withheld && recordOk(self);
      if (!matched.has(self) && !withheld) text += ` · ${W("page.step.unverified")}`;
      else if (withheld) text += ` · ${W("page.step.withheld")}`;
      if (listed) listed.add(self);
      d.dataset.self = self;
    }
    d.append(el("summary", text), steps(ids));
    return d;
  };
  // The page's words come from the wording pack chat speaks with, when the
  // page carries one (capsulectlDealPage.words).
  const words = (page && page.words) || {};
  // A part of the receipt's face: a titled box.
  const box = (title, open) => {
    const d = el("details", undefined, "deal-box");
    d.open = open;
    d.append(el("summary", title));
    return d;
  };
  const bullets = (lines, yes) => {
    const ul = el("ul", undefined, "deal-list");
    lines.forEach((t) => {
      const li = el("li");
      li.append(el("span", undefined, yes ? "deal-dot deal-yes" : "deal-dot"), el("span", t));
      ul.append(li);
    });
    return ul;
  };

  // What the deal's records can be read as, on this page: the other side's
  // half is not in a deal's own file, so the page never says "stamped by
  // both" or "your receipt only"; it says what this file holds.
  const opening = report.asked_opening || {};
  const askedChecked = !!(page && page.openings && page.openings.asked === true) && typeof opening.text === "string";
  const askedStep = byId.get(report.asked_step);
  const col = el("div", undefined, "deal-col");

  if (model.demo) host.append(el("span", W("page.demo"), "deal-demo"));
  const bar = el("div", undefined, "deal-bar");
  bar.append(el("h1", W("page.card.title")), el("span", shared ? W("page.bar.shared") : W("page.bar.own")));
  host.append(bar, col);

  // The verdict: said once, in the receipt's own words.
  const banner = el("div", undefined, "deal-banner");
  const stepsAll = report.steps || [];
  const allMatched = stepsAll.length > 0 && stepsAll.every((st) => matched.has(st.capsule_id) || (shared && st.withheld && recordOk(st.capsule_id)));
  const holds = allMatched && (askedChecked || shared);
  banner.dataset.checked = holds ? "yes" : "no";
  const composed = model.composed;
  if (composed) {
    const theirShown = composed.shown;
    const label = !composed.mine
      ? W("page.composed.no_copy")
      : composed.theirCase === "both"
        ? W("page.composed.both")
        : composed.theirCase === "only"
          ? W("page.composed.only")
          : W("page.composed.none");
    const said = {
      verified: W(allMatched ? "page.composed.said.verified_stamped" : "page.composed.said.verified", { same: W(composed.shown && composed.shown.mesh ? "page.same.exchange" : "page.same.deal") }),
      failed: W("page.composed.said.failed"),
      refused: W("page.composed.said.refused"),
      refused_unchecked: W("page.composed.said.refused_unchecked"),
      absent: W("page.composed.said.absent"),
    };
    const ok = composed.mine && holds;
    banner.dataset.checked = ok ? "yes" : "no";
    banner.dataset.case = composed.theirCase;
    const sub = !composed.mine ? W("page.composed.sub_no_copy") : theirShown ? said[theirShown.state] : W("page.composed.sub_nothing");
    banner.append(el("b", W(ok ? "page.composed.title_checked" : "page.composed.title_unchecked", { label })), el("div", sub, "deal-small"));
    // The keep copy rides in this page whole: said on its face, since
    // people forward receipts (Sherlock2, #86 round 2).
    if (page && page.keep !== undefined) {
      const own = el("div", W("page.composed.keep_note"), "deal-small");
      own.dataset.private = "keep";
      banner.append(own);
    }
  } else if (shared) {
    const who = W(report.audience === "counterparty" ? "page.audience.counterparty" : "page.audience.adjudicator");
    banner.append(el("b", holds ? W("page.shared.checked") : W("page.shared.unchecked")), el("div", W("page.shared.made_for", { who, left_out: (report.withheld || []).join(", ") }), "deal-small"));
  } else {
    banner.append(
      el("b", holds ? W("page.own.checked") : W("page.own.unchecked")),
      el("div", W("page.own.note"), "deal-small"),
    );
  }
  col.append(banner);
  // What the deal's text rests on, said on the face of every copy: sealed with
  // the page and checked unchanged, or (a bundle written before reports were
  // sealed) read from the x-deal-v0 extension, which no record seals.
  const rests = el(
    "p",
    sealed
      ? W("page.rests.sealed")
      : W("page.rests.unchecked") + (shared || !askedChecked ? "" : " " + W("page.rests.unchecked_asked")),
    "deal-note",
  );
  if (sealed) rests.dataset.sealed = "x-deal-v0";
  else rests.dataset.unchecked = "x-deal-v0";
  if (!composed || composed.mine) col.append(rests);

  // The receipt: what was asked, in the person's words, then what was done.
  const card = el("div", undefined, "deal-card");
  card.append(el("div", model.dealId ? W("page.card.title_deal", { deal: model.dealId }) : W("page.card.title"), "deal-caps"));
  if (shared) {
    card.append(el("div", W("page.card.withheld"), "deal-small"));
  } else if (askedChecked) {
    card.append(el("div", askedStep && typeof askedStep.at === "string" ? W("page.card.asked_day", { day: shortDay(askedStep.at) }) : W("page.card.asked"), "deal-small"));
    const quote = el("div", `“${opening.text}”`, "deal-quote");
    quote.dataset.asked = report.asked_step;
    card.append(quote);
  } else {
    card.append(el("div", W("page.card.asked_unchecked"), "deal-bad"));
  }
  const did = report.did || [];
  if (did.length > 0) {
    const last = did[did.length - 1];
    const lifeNow = report.lifecycle;
    card.append(el("div", W(lifeNow && lifeNow.state === "open" ? "page.card.so_far" : "page.card.done", { text: String(last && last.text).replace(/^Did: /, "") })));
  } else {
    card.append(el("div", W("page.card.nothing_done"), "deal-small"));
  }
  if (report.money && typeof report.money.text === "string") card.append(el("div", report.money.text));
  card.append(el("div", shared ? (report.audience === "counterparty" ? W("page.card.copy_counterparty") : W("page.card.copy_adjudicator")) : W("page.card.copy_own"), "deal-small"));
  if (!composed || composed.mine) col.append(card);

  // Your side, in your book: the errand's moves in order, a time on every row
  // as the frames show. Each row says whether its sealed step verified.
  const ours = el("div", undefined, "deal-half deal-ours");
  ours.append(el("div", shared ? W("page.ours.holder") : W("page.ours.own"), "deal-caps"));
  const journey = Array.isArray(report.journey) ? report.journey.filter((m) => m && typeof m === "object") : [];
  const row = (when, text, tail) => {
    const line = el("div");
    line.append(el("b", when ? `${when} · ` : ""), el("span", text));
    if (tail) line.append(el("span", ` · ${tail}`, "deal-small"));
    return line;
  };
  const verified = (id) => matched.has(id) || (shared && byId.get(id) && byId.get(id).withheld && recordOk(id));
  // A withheld step on a shared copy is verified present; what it says is not in the copy.
  const rowTail = (id) => (matched.has(id) ? "" : verified(id) ? W("page.step.withheld") : W("page.step.unverified"));
  if (!shared && journey.length === 0 && askedChecked && askedStep) ours.append(row(short(askedStep.at), W("page.ours.asked"), W("page.ours.asked_tail")));
  const movers = { user: W("page.mover.user"), agent: W("page.mover.agent"), other: W("page.mover.other") };
  if (!shared && journey.length > 0) {
    journey.forEach((m) => {
      const step = byId.get(m.step);
      const label = dealJourneyLabel(words, m);
      const who = Object.hasOwn(movers, m.by) ? movers[m.by] : W("page.mover.someone");
      const r = row(step && matched.has(m.step) && typeof step.at === "string" ? short(step.at) : "", `${who}${label ? ` · ${label}` : ""}`, rowTail(m.step));
      r.dataset.journey = label;
      ours.append(r);
    });
  } else {
    did.forEach((i) => {
      const hasSelf = typeof i.self === "string" && i.self !== "";
      const ok = !hasSelf || verified(i.self);
      ours.append(row(ok && typeof i.at === "string" && i.at ? short(i.at) : "", i.text, hasSelf ? rowTail(i.self) : ""));
    });
  }
  const life = report.lifecycle;
  if (life && typeof life.text === "string") ours.append(row("", lifecycleText(life), ""));
  if (composed && !composed.mine) {
    // A composition without your own copy: your side is not on this page.
    const none = el("div", undefined, "deal-half deal-none");
    none.append(el("div", W("page.none.title"), "deal-caps"), el("div", W("page.none.text")));
    col.append(none);
  } else col.append(ours);

  // Their side: on a deal's own file only ever "not in this file"; on a
  // composition, what their member is, as this page checked it (D3).
  const theirShown = composed && composed.shown;
  const theirs = el("div", undefined, theirShown && theirShown.state === "verified" ? "deal-half deal-theirs" : "deal-half deal-none");
  if (!composed) {
    theirs.append(el("div", shared ? W("page.theirs.other") : W("page.theirs.title"), "deal-caps"));
    theirs.append(el("div", W("page.theirs.absent")));
  } else if (theirShown && theirShown.state === "verified") {
    theirs.dataset.their = "verified";
    theirs.append(el("div", W("page.theirs.verified"), "deal-caps"));
    if (theirShown.mesh) {
      theirs.append(row(short(theirShown.at), W("page.theirs.exchange"), ""));
      theirs.append(el("div", W("page.theirs.words_not_stated"), "deal-small"));
    } else {
      (theirShown.report.did || []).forEach((i) => theirs.append(row(typeof i.at === "string" && i.at ? short(i.at) : "", i.text, "")));
      const theirLife = theirShown.report.lifecycle;
      if (theirLife && typeof theirLife.text === "string") theirs.append(row("", lifecycleText(theirLife), ""));
    }
    theirs.append(el("div", W("page.theirs.stamped", { when: short(theirShown.stamped) }), "deal-small"));
  } else {
    theirs.dataset.their = theirShown ? theirShown.state : "none";
    theirs.append(el("div", W("page.theirs.title"), "deal-caps"));
    const day = theirShown && typeof theirShown.at === "string" ? shortDay(theirShown.at) : "";
    const line = !theirShown
      ? W("page.theirs.no_record")
      : theirShown.state === "failed"
        ? W("page.theirs.failed")
        : theirShown.state === "refused"
          ? W("page.theirs.refused", { day })
          : theirShown.state === "refused_unchecked"
            ? W("page.theirs.refused_unchecked", { day })
            : W("page.theirs.asked_nothing", { day });
    theirs.append(el("div", line));
  }
  // Their side's fold-out: whose key it is (only "their key" and its
  // digits, never a name the composer declared), what this page left to
  // capsulectl verify, and their other answers by date (D2, D3; Sherlock2,
  // #86 rounds 2 and 5).
  if (composed && theirShown) {
    const more = el("details", undefined, "deal-their-more");
    more.append(el("summary", W("page.more_theirs")));
    if (theirShown.key) {
      more.append(el("div", W("page.theirs.key", { key: String(theirShown.key).slice(0, 16) }), "deal-small"));
      more.append(el("div", W("page.theirs.key_note"), "deal-small"));
    }
    composed.also
      .filter((t) => t.state !== "verified" && t.state !== "failed")
      .forEach((t) => {
        const day = typeof t.at === "string" ? shortDay(t.at) : "";
        const also = W(t.state === "absent" ? "page.also.absent" : t.state === "refused" ? "page.also.refused" : "page.also.refused_unchecked", { day });
        more.append(el("div", also, "deal-small"));
      });
    theirs.append(more);
  }
  col.append(theirs);

  // The stamps: the proofs ladder in the five consumer words, closed until
  // opened. Only what this page checked, or says it did not.
  const countersign = page && page.countersign && typeof page.countersign.text === "string" ? page.countersign : undefined;
  // [word, line, detail]: a word is "passed" only for a check this page ran.
  const theirArtifact = composed && composed.shown && (composed.shown.state === "verified" || composed.shown.state === "failed") ? composed.shown : undefined;
  const sameWhat = W(theirArtifact && theirArtifact.mesh ? "page.same.exchange" : "page.same.deal");
  const anyWitness = model.witnessed || (composed && [composed.shown, ...composed.also].some((t) => t && t.witnessed));
  // Frame 8's record lines (evidencebook-deal#94): your approval, its order,
  // the settlement and the close, each from its record; your side's copy
  // is the one they are read from.
  const mineLines = composed && !composed.mine ? undefined : model.lines;
  const theirLines = theirArtifact && theirArtifact.state === "verified" && !theirArtifact.mesh ? theirArtifact.lines : undefined;
  const actName = (act) => {
    const step = act && byId.get(act.id);
    if (step && matched.has(act.id) && typeof step.line === "string") return `“${step.line}”`;
    return act && typeof act.body.action === "string" ? W("page.act.named", { action: act.body.action }) : W("page.act.unnamed");
  };
  // A shared copy is read by the other side: its lines name the holder.
  const side = shared ? "shared" : "own";
  const who = W("page.pron.who." + side);
  const Who = W("page.pron.cap." + side);
  const their = W("page.pron.their." + side);
  const giver = W("page.pron.giver." + side);
  const approverWords = Object.fromEntries(["user", "standing_intent", "agent_card", "task_authority"].map((a) => [a, W("page.approver." + a, { who, their, giver })]));

  const recordLines = [];
  if (!mineLines && !(composed && !composed.mine)) {
    // A copy whose steps have no place in a book: none of these lines is read.
    const why = W("page.detail.sealed_before_book");
    recordLines.push(["not checked", W("page.line.each_act", { who }), why], ["not checked", W("page.line.settled"), why], ["not checked", W("page.line.close"), why]);
  } else if (!mineLines) {
    recordLines.push(["missing", W("page.line.each_act", { who }), W("page.detail.no_own_record")]);
  } else if (mineLines.afterYes.state !== "none") {
    const yes = mineLines.afterYes;
    const said = (yes.approvers || []).map((a) => approverWords[a]).filter((t) => t);
    recordLines.push(["missing", W("page.line.key_signature", { cap: Who, their }), said.length > 0 ? said.join("; ") : W("page.detail.no_signed_key", { their })]);
    recordLines.push(
      yes.state === "passed"
        ? ["passed", W("page.line.each_act", { who }), W("page.detail.recomputed")]
        : yes.state === "failed"
          ? ["failed", W("page.line.each_act", { who }), yes.why === "unapproved" ? W("page.detail.unapproved", { act: actName(yes.act), who }) : W("page.detail.order", { act: actName(yes.act) })]
          : yes.state === "missing"
            ? ["missing", W("page.line.each_act", { who }), W("page.detail.approval_missing", { act: actName(yes.act) })]
            : ["not checked", W("page.line.each_act", { who }), W("page.detail.withheld")],
    );
  }
  const legOf = (lines) => lines && lines.settlement && lines.settlement.legs ? lines.settlement : undefined;
  const settled = legOf(mineLines) || legOf(theirLines);
  const settledWhose = legOf(mineLines) ? "" : W("page.settled.theirs");
  const unsettled = (mineLines && mineLines.settlement) || (theirLines && theirLines.settlement);
  const railText = !settled
    ? ""
    : settled.rail === "lightning"
      ? settled.preimage === "withheld"
        ? W("page.rail.lightning_withheld")
        : settled.preimage
        ? W("page.rail.lightning_sealed")
        : W("page.rail.lightning_final", { whose: legOf(mineLines) ? who : W("page.pron.other") })
      : settled.rail === "x402"
        ? W("page.rail.x402")
        : W("page.rail.none");
  if (mineLines || (composed && !composed.mine)) recordLines.push(
    settled
      ? ["not checked", W("page.line.settled"), settledWhose + railText + (settled.band ? ` · ${W("page.settled.size", { band: dealBandLabel(settled.band) })}` : "")]
      : ["missing", W("page.line.settled"), !unsettled ? W("page.settle.none") : unsettled.rail === "card" ? W("page.settle.card") : W("page.settle.other", { rail: String(unsettled.rail).replace(/_/g, " ") })],
  );
  const closed = mineLines && mineLines.close;
  let closeState = "none";
  let closeLine;
  if (!closed) closeLine = ["not checked", W("page.line.close"), mineLines ? W("page.close.not_closed") : W("page.detail.no_own_record")];
  else if (closed.withheld) closeLine = ["not checked", W("page.line.close"), W("page.detail.withheld")];
  else if (theirArtifact && theirArtifact.mesh) closeLine = ["not checked", W("page.line.close"), W("page.close.exchange", { who })];
  else if (theirArtifact && theirArtifact.state !== "verified") closeLine = ["not checked", W("page.line.close"), W("page.close.unchecked")];
  else if (!theirArtifact) closeLine = ["missing", W("page.line.close"), shared ? W("page.close.none_other") : W("page.close.none_theirs")];
  else {
    closeState = dealCloseStatus(closed.id, theirArtifact.records, theirArtifact.book);
    closeLine =
      closeState === "agreed"
        ? ["passed", W("page.line.close"), W("page.close.agreed", { who })]
        : closeState === "contested"
          ? ["failed", W("page.line.close"), W("page.close.contested", { who })]
          : ["missing", W("page.line.close"), W("page.close.unilateral", { who })];
  }
  if (mineLines || (composed && !composed.mine)) recordLines.push(closeLine);
  const stamps = composed ? [
    askedChecked
      ? ["passed", W("page.stamp_line.sealed"), W("page.detail.built")]
      : ["not checked", W("page.stamp_line.sealed_unchecked"), ""],
    !composed.mine
      ? ["missing", W("page.stamp_line.stamped_books"), W("page.detail.no_own_record")]
      : !theirArtifact
      ? ["missing", W("page.stamp_line.stamped_books"), W("page.detail.theirs_missing")]
      : allMatched && !(theirArtifact.state === "failed" && (theirArtifact.check === "V1" || theirArtifact.check === "V2"))
        ? ["passed", W("page.stamp_line.stamped_books"), ""]
        : ["failed", W("page.stamp_line.stamped_books"), allMatched ? W("page.close.unchecked") : W("page.detail.see_more")],
    !composed.mine
      ? ["missing", W("page.stamp_line.same", { same: sameWhat }), W("page.detail.no_own_record")]
      : !theirArtifact
      ? ["missing", W("page.stamp_line.same", { same: sameWhat }), W("page.detail.theirs_missing")]
      : theirArtifact.state === "verified"
        ? ["passed", W("page.stamp_line.same", { same: sameWhat }), theirArtifact.mesh ? W("page.detail.same_request") : W("page.detail.same_deal")]
        : ["failed", W("page.stamp_line.same", { same: sameWhat }), W("page.close.unchecked")],
    ...recordLines,
    [anyWitness ? "not checked" : "missing", W("page.stamp_line.witness_both"), anyWitness ? W("page.detail.witness_unchecked") : ""],
  ] : [
    shared
      ? ["not checked", W("page.stamp_line.asked_withheld"), ""]
      : askedChecked
        ? ["passed", W("page.stamp_line.sealed"), W("page.detail.built")]
        : ["not checked", W("page.stamp_line.sealed_unchecked"), ""],
    stepsAll.length === 0
      ? ["not checked", W("page.stamp_line.no_steps"), ""]
      : allMatched
        ? ["passed", shared ? W("page.stamp_line.steps_holder") : W("page.stamp_line.steps_own"), ""]
        : ["failed", W("page.stamp_line.step_missing"), W("page.detail.see_more")],
    ...recordLines,
    [model.witnessed ? "not checked" : "missing", shared ? W("page.stamp_line.witness_holder") : W("page.stamp_line.witness_own"), model.witnessed ? W("page.detail.witness_unchecked") : ""],
    ["missing", W("page.stamp_line.nothing_back"), W("page.detail.not_in_file")],
    ["not checked", W("page.stamp_line.happened"), W("page.detail.nobody")],
  ];
  const passed = stamps.filter((s) => s[0] === "passed").length;
  const stampBox = el("details", undefined, "deal-box deal-stamps");
  const sum = el("summary", W("page.stamps.title"));
  const failed = stamps.filter((st) => st[0] === "failed").length;
  const rest = stamps.length - passed - failed;
  const pill = el("span", [W("page.pill.passed", { count: passed }), failed > 0 ? W("page.pill.failed", { count: failed }) : "", rest > 0 ? W("page.pill.rest", { count: rest }) : ""].filter((t) => t !== "").join(" · "), failed > 0 ? "deal-pill deal-bad" : "deal-pill");
  sum.append(pill);
  stampBox.dataset.close = closeState;
  stampBox.append(sum, el("div", W("page.stamps.under"), "deal-small"));
  stamps.forEach(([word, text, detail]) => {
    const line = el("div", undefined, "deal-stamp");
    line.dataset.stamp = word;
    line.append(el("b", word === "passed" ? "✓" : "—", "deal-mark " + (word === "passed" ? "deal-ok" : "deal-bad")), el("span", `${text} · ${dealStampWord(word)}${detail ? ` · ${detail}` : ""}`));
    stampBox.append(line);
  });
  if (countersign) stampBox.append(el("div", countersign.text, countersign.flag === true ? "deal-small deal-bad" : "deal-small"));
  stampBox.append(el("div", W("page.stamp.five", { words: dealStampWords.map(dealStampWord).join(" · ") }), "deal-small"));
  col.append(stampBox);

  // What this receipt proves, and what it does not: only what this page checked.
  const proves = box(W("page.proves.title"), true);
  const provesLines = [];
  if (askedChecked && !shared) provesLines.push(W("page.proves.asked"));
  if (allMatched) provesLines.push(W("page.proves.steps"));
  const authority = report.authority || [];
  if (sealed && authority.length > 0 && authority.every((b) => b.covered)) provesLines.push(W("page.proves.authority"));
  if (theirArtifact && theirArtifact.state === "verified") provesLines.push(W("page.proves.theirs", { same: sameWhat }));
  if (provesLines.length === 0) provesLines.push(W("page.proves.nothing"));
  proves.append(bullets(provesLines, true));
  col.append(proves);
  const doesnt = box(W("page.doesnt.title"), true);
  const doesntLines = [
    W("page.doesnt.true"),
    W("page.doesnt.who"),
    W("page.doesnt.merchant"),
  ];
  if (typeof report.scope === "string" && report.scope) doesntLines.push(report.scope);
  if (shared) doesntLines.push(W("page.doesnt.withheld"));
  if (composed) doesntLines.push(W("page.doesnt.other_side"));
  doesnt.append(bullets(doesntLines, false));
  col.append(doesnt);

  // Tell your agent: only what the deal skill does today.
  if (!shared) {
    const say = el("div", undefined, "deal-say");
    say.append(el("div", W("page.say.title"), "deal-caps"), el("b", W("page.say.send")), el("b", W("page.say.copy")));
    col.append(say);
  }

  // Everything else the receipt knows, behind one fold-out: its steps, authority, what was told, dates, anomalies.
  const more = el("details", undefined, "deal-more");
  more.append(el("summary", W("page.more.title")));
  col.append(more);

  if (typeof report.did_line === "string") more.append(el("p", report.did_line, "deal-note"));
  more.append(el("h2", W("page.did.title")));
  more.append(
    el(
      "p",
      sealed
        ? W("page.did.sealed")
        : W("page.did.unchecked"),
      "deal-note",
    ),
  );
  if (did.length === 0) more.append(el("p", W("page.did.nothing"), "deal-note"));
  listed = new Set();
  did.forEach((i) => more.append(item(typeof i.at === "string" && i.at ? `${local(i.at)} · ${i.text}` : i.text, i.steps, undefined, i.self)));
  listed = undefined;
  more.append(el("h2", W("page.asked.title")));
  if (shared) more.append(el("p", W("page.asked.withheld"), "deal-note"));
  else if (askedChecked) more.append(item(`“${opening.text}”`, [report.asked_step]));
  if (askedChecked && !shared) more.append(el("p", W("page.asked.exact"), "deal-note"));

  const authorityBlocks = report.authority || [];
  if (authorityBlocks.length > 0) {
    more.append(el("h2", W("page.authority.title")));
    if (typeof report.authority_order === "string") more.append(el("p", report.authority_order, "deal-note"));
    authorityBlocks.forEach((b) => {
      more.append(el("h3", `${b.covered ? "" : "⚠️ "}${b.action}`));
      (b.layers || []).forEach((l) => {
        const line = item(`${local(l.at)} · ${l.text}`, [l.step], b.covered || l.layer !== "action" ? undefined : "deal-flag");
        if (typeof l.note === "string" && l.note) line.append(el("p", l.note, "deal-note"));
        more.append(line);
      });
    });
  }

  if (life && typeof life.text === "string") {
    more.append(el("h2", W("page.life.title")), el("p", lifecycleText(life)));
    (life.later || []).forEach((l) => more.append(item(`${local(l.at)} · ${W("page.life.later", { text: l.text })}`, [l.capsule_id])));
    more.append(el("p", W("page.life.as_of", { text: life.may_change, when: local(life.as_of) }), "deal-note"));
    // A report off the deal's log (capsule-cli#294) states the deal as of
    // its checkpoint, the deal's last step. Where it stands today, the page
    // works out from the viewer's clock, apart, and says so: it is not part
    // of the report.
    if (model.standalone) {
      const today = localDay();
      const due = life.open && typeof life.open.expect_close_by === "string" ? life.open.expect_close_by : "";
      const passedDue = life.state === "open" && due !== "" && due < today ? ` ${W("page.life.passed", { day: readDay(due) })}` : "";
      more.append(el("p", W("page.life.checkpoint", { when: local(life.as_of) }), "deal-note"));
      more.append(el("p", W("page.life.today", { day: readDay(today), passed: passedDue }), "deal-note"));
    }
  }

  // What this receipt can prove and how to check it.
  const header = el("section", undefined, "deal-assurance");
  header.append(el("p", report.scope || W("page.scope.default"), "deal-scope"));
  // The countersign rung, as capsulectl checked it when it wrote this page
  // (a page cannot resolve a signer against a directory): "Not
  // countersigned" unless the bundle carried a countersignature that
  // verified, and a self-countersignature is NOT INDEPENDENT. The page
  // renders the line capsulectl wrote; it names no rung itself.
  let rung = { text: W("page.countersign.unread") };
  if (countersign) rung = countersign;
  header.append(el("p", rung.text, rung.flag === true ? "deal-rung deal-bad" : "deal-rung"));
  if (typeof rung.note === "string" && rung.note) header.append(el("p", rung.note, "deal-note"));
  const provenance = el("section", undefined, "deal-provenance");
  const builds = model.builds;
  if (builds.length > 0) provenance.append(el("p", W("page.produced", { builds: builds.map((b) => b.name).join(W("page.produced_then")) }), "deal-note"));
  if (typeof report.instructions === "string" && report.instructions) provenance.append(el("p", report.instructions, "deal-note"));
  const { releaseNotes, buildNote } = model;
  if (buildNote) {
    const what = el("p", W("page.release.changed", { note: buildNote }), "deal-note");
    const link = document.createElement("a");
    link.href = releaseNotes;
    link.textContent = W("page.release.notes");
    link.rel = "noopener";
    what.append(link);
    provenance.append(what);
  }
  const notClaimed = el("details", undefined, "deal-claims");
  notClaimed.open = true;
  notClaimed.append(el("summary", W("page.claims.title")));
  const claims = el("ul");
  [
    W("page.claims.tamper"),
    W("page.claims.reported"),
    W("page.claims.merchant"),
  ].forEach((t) => claims.append(el("li", t)));
  notClaimed.append(claims);
  header.append(notClaimed);
  if (shared) {
    const who = W(report.audience === "counterparty" ? "page.audience.counterparty" : "page.audience.adjudicator");
    header.append(el("p", W("page.shared.note", { who, left_out: (report.withheld || []).join(", ") }), "deal-note"));
  }
  more.append(header, provenance);

  // What the agent told whom about the user: each telling, its time and the
  // approval that covered it, or that none did. Your own copy shows what was
  // given; a shared copy names only the kind of thing.
  more.append(el("h2", W("page.told.title")));
  const told = report.told || [];
  if (told.length === 0) more.append(el("p", W("page.told.nothing"), "deal-note"));
  told.forEach((t) => {
    const flagged = t.authority === "none";
    const fields = (t.fields || []).map((f) => (typeof f.value === "string" ? `${f.label}: ${f.value}` : f.label)).join("; ");
    more.append(item(`${flagged ? "⚠️ " : ""}${t.text} · ${local(t.at)} · ${t.authority_text}${shared ? "" : ` (${fields})`}`, t.steps, flagged ? "deal-flag" : undefined));
  });
  more.append(el("p", W("page.told.only_sealed"), "deal-note"));

  // Where the user sells: what their agent told the buyer, each claim's words
  // checked here against the commitment its sealed step carries, labelled by
  // the class sealed with it. A claim that is not the agent's own, or whose
  // words do not match, is never shown as said.
  const said = report.representations || [];
  if (said.length > 0) {
    const heading = !shared ? W("page.told_buyer.own") : report.audience === "counterparty" ? W("page.told_buyer.counterparty") : W("page.told_buyer.adjudicator");
    more.append(el("h2", heading));
    const checked = (page && page.openings && page.openings.representations) || [];
    for (const [i, r] of said.entries()) {
      // Checked against its sealed commitment when this page was built.
      const claimClass = model.claimClasses[i];
      const ok = checked[i] === true && claimClass !== undefined && typeof r.text === "string";
      if (ok) {
        const label = W("page.claim." + claimClass);
        more.append(item(`${label ? `${label}: ` : ""}“${r.text}” ✓`, [r.step]));
      } else {
        more.append(item(W("page.told_buyer.unchecked"), [r.step], "deal-flag"));
      }
    }
    more.append(el("p", W("page.told_buyer.exact"), "deal-note"));
  }

  // Cancel-by dates (the point of no return is a date passing) and due
  // dates (what the user owes by a date). Recorded, never enforced.
  const deadlines = report.deadlines || [];
  if (deadlines.length > 0) {
    more.append(el("h2", deadlines.some((d) => d.due_by) ? W("page.deadlines.both") : W("page.deadlines.cancel")));
    const list = el("ul", undefined, "deal-steps");
    deadlines.forEach((d) => {
      const live = d.status === "open" || d.status === "carried_at_close";
      const li = el("li", undefined, live ? "deal-bad" : undefined);
      li.append(el("span", `${d.cancel_by || d.due_by} · ${d.marking || d.status}: `), el("span", `${d.text}. ${d.holds || ""}`));
      // A report off the deal's log is as of its checkpoint: a date that has
      // passed since, the page says so when opened, apart from the report.
      const date = d.cancel_by || d.due_by || "";
      const today = localDay();
      if (model.standalone && live && date !== "" && date < today) {
        li.append(el("span", ` ${W("page.deadlines.passed_today", { day: readDay(today) })}`, "deal-note"));
      }
      list.append(li);
    });
    more.append(list, el("p", deadlines[0].note, "deal-note"));
  }
  // A cancellation: exactly what is shown, and what is not.
  (report.cancellations || []).forEach((c) => {
    more.append(el("h2", W("page.cancel.title")));
    const shows = el("details", undefined, c.merchant === "confirmed" ? undefined : "deal-flag");
    shows.open = true;
    shows.append(el("summary", W("page.cancel.shows")));
    const proven = el("ul");
    (c.proven || []).forEach((p) => proven.append(el("li", p)));
    const notProven = el("ul", undefined, "deal-note");
    (c.not_proven || []).forEach((p) => notProven.append(el("li", p)));
    shows.append(proven, el("h3", W("page.cancel.not_shown")), notProven, steps(c.steps || []));
    more.append(shows);
  });

  // The merchant's own emails. Two statements, never one: the merchant's
  // DKIM signature (independent of the agent) and our seal (our own record).
  const merchant = report.merchant || [];
  if (merchant.length > 0) {
    more.append(el("h2", W("page.merchant.title")));
    more.append(
      el(
        "p",
        W("page.merchant.two"),
        "deal-note",
      ),
    );
    if (typeof report.email_scope === "string") more.append(el("p", report.email_scope, "deal-scope"));
    merchant.forEach((m) => {
      const d = el("details", undefined, m.verified ? undefined : "deal-flag");
      d.open = true;
      d.append(el("summary", m.order_id ? W("page.merchant.order", { order: m.order_id }) : W("page.merchant.email")));
      d.append(el("p", W("page.merchant.dkim", { says: m.merchant_says }), m.verified ? "deal-ok" : "deal-bad"));
      d.append(el("p", W("page.merchant.seal", { says: m.we_say }), "deal-note"));
      if (m.key_source === "supplied") {
        d.append(el("p", W("page.merchant.key_supplied"), "deal-bad"));
      }
      const table = el("table", undefined, "deal-merchant");
      const row = (label, value) => {
        if (!value) return;
        const tr = el("tr");
        tr.append(el("th", label), el("td", value));
        table.append(tr);
      };
      row(m.approved_basis ? W("page.merchant.approved_basis", { basis: m.approved_basis }) : W("page.merchant.approved"), m.approved);
      row(W("page.merchant.reported"), m.agent_reported);
      row(W("page.merchant.charged"), m.charged);
      row(W("page.merchant.charged_on"), m.charged_on);
      row(W("page.merchant.cancel_by"), m.cancel_by);
      row(W("page.merchant.items"), (m.items || []).join(" · "));
      row(W("page.merchant.domain"), m.domains);
      row(W("page.merchant.key"), m.key_size ? W("page.merchant.key_size", { size: m.key_size }) : "");
      d.append(table, steps(m.steps || []));
      more.append(d);
    });
  }

  more.append(el("h2", W("page.anomalies.title")));
  const anomalies = report.anomalies || [];
  [
    ["agent", W("page.anomalies.agent")],
    ["counterparty", W("page.anomalies.counterparty")],
  ].forEach(([side, label]) => {
    const mine = anomalies.filter((a) => a.side === side);
    more.append(el("h3", label));
    if (mine.length === 0) more.append(el("p", W("page.anomalies.none"), "deal-note"));
    mine.forEach((a) => more.append(item(`⚠️ ${a.text}`, a.steps, "deal-flag")));
  });

  more.append(
    el(
      "p",
      [W("page.end"), shared ? W("page.end.withheld") : "", W("page.end.key")].filter((t) => t !== "").join(" "),
      "deal-note",
    ),
  );
};

EvidenceGraph.registerPresentation({
  manifest: {"spec_version": "aac.presentation-manifest/v0", "id": "capsulectl.deal-view/v0", "presentation_api": "aac.presentation-api/v0", "runtime_min": "0.1.0", "trust_class": "trusted-executable", "requires": {"bundle_kind": "evidence-bundle/v2", "extensions": {"required": ["x-deal-v0"]}}, "forbids": {"extensions": ["composed/v1", "x-history-selection/v0"], "profiles": ["spec_version:report/v1", "result_version:evidence-result-v0", "spec_version:evaluation-summary/v1"]}, "audiences": ["*"], "formats": ["html"], "fallback": false, "priority": 1, "executable": {"carrier": "module-slot", "script_sha256": "0000000000000000000000000000000000000000000000000000000000000000", "style_sha256": ["04759c13ec65dd02645149cb7d009153712b8f8c31027fd73923df35e95f8279"]}},
  canRender: dealViewVerified,
  buildModel: dealViewModel,
  render: dealViewRender,
});

EvidenceGraph.registerPresentation({
  manifest: {"spec_version": "aac.presentation-manifest/v0", "id": "capsulectl.deal-view-composed/v0", "presentation_api": "aac.presentation-api/v0", "runtime_min": "0.1.0", "trust_class": "trusted-executable", "requires": {"bundle_kind": "evidence-bundle/v2", "extensions": {"required": ["composed/v1"]}}, "forbids": {"extensions": ["x-history-selection/v0"], "profiles": ["spec_version:report/v1", "result_version:evidence-result-v0", "spec_version:evaluation-summary/v1"]}, "audiences": ["*"], "formats": ["html"], "fallback": false, "priority": 1, "executable": {"carrier": "module-slot", "script_sha256": "0000000000000000000000000000000000000000000000000000000000000000", "style_sha256": ["04759c13ec65dd02645149cb7d009153712b8f8c31027fd73923df35e95f8279"]}},
  canRender: dealComposedCanRender,
  buildModel: dealComposedModel,
  render: dealViewRender,
});
