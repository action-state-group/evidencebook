// SPDX-License-Identifier: Apache-2.0
// The History card, in the page: capsuleviewer.standard-history/v0, which
// draws a standard_history/1 card (agent-action-capsule's
// spec/standard-history-v0.md, cited as SH) as the History card (9b) canvas
// draws it, boards HistoryCard9b and SharedHistoryLaptop9b.
//
// One self-contained module-slot script (the presentation contract, as
// capsuleviewer.history/v0 is). It imports nothing, reads no wording pack and
// never fetches. `python -m capsule_viewer.standard_history.module_slot
// --write` fills the lines marked "@generated": its manifest, the words
// (compiled in from the consumer/en pack,
// standard_history/wording-consumer-en.json, whose id and sha256 the page names
// in L2) and the two stylesheets it inserts (static/kit.css and
// static/standard_history.css, pinned by the manifest's style_sha256).
//
// The input is an evidence-bundle/v2 whose root record's agent_input is the
// card, as `capsulectl seal` seals a payload, with an x-standard-history/v0
// block in its extensions carrying what the receiver holds beside the card,
// each member optional: `policy` {digest, window} and `as_of` {anchor,
// anchor_time, first_checkpoint_time}, under the names of AAC's reference
// check_card. `as_of` may also carry `checkpoint` (the anchor's cll-checkpoint
// octets, hex, whose SHA-256 is the anchor) and `receipts` (1 to 64 hex
// strings, each one cll-witness-receipt for the anchor, IP1 6.2.2): the
// snapshot's witness Receipts. The module verifies nothing itself: it hands
// them to the runtime's readerAnchorWitness, which hashes the checkpoint and
// runs AAC's checkAnchorWitnessed under the reader's own witness list, never one
// the bundle names (capsule-viewer#183). A block that also carries `deals` (SH 5.2's deal entries) is the
// holder's own copy, never the card someone else is sent: the page draws it
// under the PRIVATE banner and recounts the card from those entries. A shared
// card shows its counts as the holder's statement (SH 6).
//
// checkCard is the twin of the reference (agent_action_capsule.standard_history
// .check_card at agent-action-capsule 1037a1c): the same six properties, in
// the same order, with the same statuses, but for one input JSON.parse cannot
// tell apart (an assetScale written 2.0; see decade), and for the policy
// digest and the anchor, compared with ===, which is Python's == for the strings
// readBlock admits. js-tests run all of
// AAC's vectors through it. Like the reference it throws on a malformed input (the block),
// never on a malformed card. A card the check fails draws none of its counts.
// anchor_inclusion is NOT_CHECKED for a derivation (SH 3). The page says a
// witness recorded the snapshot only when AAC's anchor_witnessed is PASS under
// the reader's witness list and the card's as_of property is PASS; it never
// says the witness checked the counts. The page's verifier checks no
// record signature, so the page never says the holder's key signed the card.
//
// What the page never draws: a deal entry (no amount, deal reference or date
// but the window's), a ratio of two counts or a percentage, a band that merges
// two currencies, and undecided in the agreed tone. All text is set with
// textContent; nothing from the bundle is parsed as HTML.
(function (root) {
  "use strict";

  var MANIFEST = {"spec_version": "aac.presentation-manifest/v0", "id": "capsuleviewer.standard-history/v0", "presentation_api": "aac.presentation-api/v0", "runtime_min": "0.1.0", "trust_class": "trusted-executable", "requires": {"bundle_kind": "evidence-bundle/v2", "extensions": {"required": ["x-standard-history/v0"]}}, "forbids": {"profiles": ["spec_version:report/v1", "spec_version:evaluation-summary/v1", "result_version:evidence-result-v0"], "extensions": ["composed/v1", "x-deal-v0", "x-history-selection/v0"]}, "audiences": ["*"], "formats": ["html"], "fallback": false, "priority": 1, "executable": {"carrier": "module-slot", "script_sha256": "0000000000000000000000000000000000000000000000000000000000000000", "style_sha256": ["246cfdc8ae83e682a220a2c13cef8134c7515999365b33965d4e924bb871f3ea", "688b12fcb6425c417534f44cf06c36f25eb18ceb080cd358c8892006d1a4944c"], "wording_sha256": "291e52043b5379b63fd899ad20ef6d1a4fc4ab21ab83dfcc5af0fd469377e375"}}; // @generated MANIFEST
  var WORDS = {"module.title": "History card", "page.title": "History card", "title": "History card", "title.standard": "Standard", "window.months.one": "last month", "window.months": "last {n} months", "window.all": "everything", "window.dates": "{start} to {end}", "window.dates.all": "Everything up to {end}", "window.no_dates": "The {window} up to the snapshot the card is as of.", "month.1": "Jan", "month.2": "Feb", "month.3": "Mar", "month.4": "Apr", "month.5": "May", "month.6": "Jun", "month.7": "Jul", "month.8": "Aug", "month.9": "Sep", "month.10": "Oct", "month.11": "Nov", "month.12": "Dec", "date": "{month} {day}, {year}", "since": "{month} {year}", "banner.private.label": "PRIVATE", "banner.private.text": "This is your own copy of your History card, with the deal entries it is counted from. Do not forward this file: it is not the card to send. The card others get carries the counts only.", "lead.own": "Your agent helped you buy and sell with other people and their agents. This card is what others can learn about your agent, only as much as you choose to share. The card they get has exactly these counts.", "lead.shared": "This is the History card an agent's holder shares: counts of their deals in a window, and nothing about any one deal.", "card.kicker": "History card · standard", "holder.person": "A person's agent · a person stands behind it, as they declare", "holder.node": "A node's agent · its operator declares it", "holder.since": "keeping records since {month}", "total.of": "{n} of {of}", "total.lead.own": "deals where you had to come through went as agreed.", "total.lead.shared": "deals where the holder had to come through went as agreed.", "total.none": "No deal in this window counts on the card.", "outcome.held": "went as agreed", "outcome.did_not_hold": "didn't", "outcome.undecided": "we couldn't tell", "counts.statement": "These counts are the holder's own statement. This page does not hold the deal entries they are counted from, so it could not recount them.", "counts.statement.entries": "This copy carries the deal entries but not {what}, so the counts were not recounted: they are the holder's own statement.", "counts.recomputed": "Recounted here from the deal entries in this copy: every count matches.", "heading.roles": "Which side", "role.provided.own": "You sold or provided", "role.bought.own": "You bought from people", "role.provided.shared": "Sold or provided", "role.bought.shared": "Bought from people", "count.of": "{n} of {of}", "heading.sizes": "Size range", "sizes.note": "Each currency on its own: sizes in different currencies are never added up or converted.", "sizes.none": "No counted deal, so no size range.", "size.range": "{low} to under {high} {currency}", "size.zero": "zero amount, {currency}", "heading.happened": "The other side agreed it happened", "happened.stamped_by_both": "Stamped by both sides", "happened.receipt_only": "Your receipt only", "happened.receipt_only.shared": "The holder's receipt only", "happened.no_record": "No record", "happened.note": "Stamped by both sides means the book holds the other side's own record of the deal. A receipt only means the book holds the holder's own receipt, not the other side's record. No record means the book holds neither. A missing record is not a failure.", "heading.disputes": "Disputes", "disputes.raised.own": "You raised", "disputes.received.own": "Raised with you", "disputes.raised.shared": "The holder raised", "disputes.received.shared": "Raised with the holder", "disputes.outcomes": "How they ended, of {of}: still open {open} · for {who} {for} · against {who} {against} · split {split} · no finding {no_finding} · withdrawn {withdrawn}", "disputes.forums": "Where, of {of}: a rule both sides agreed {auto} · the network {network} · the payment rail {rail} · somewhere else {external}", "disputes.who.own": "you", "disputes.who.shared": "the holder", "heading.counts": "What is on the card", "counts.what.own": "What counts: deals where you had to come through for someone: pay afterwards, deliver, show up, describe it right.", "counts.what.shared": "What counts: deals where the holder had to come through for someone: pay afterwards, deliver, show up, describe it right.", "counts.not_counted.own": "Not counted: {n} of {of} deals in the window, shop purchases paid up front. Paying a shop up front takes no doing; anyone can. They stay in your book.", "counts.not_counted.shared": "Not counted: {n} of {of} deals in the window, shop purchases paid up front. They stay in the holder's book.", "counts.class_missing": "{n} of {of} counted deals don't say what kind of party they were with. They are counted.", "counts.not_on.own": "Not on it: your name, what you bought or sold, what you paid, who the other side was, anything outside the window.", "counts.not_on.shared": "Not on it: the holder's name, what they bought or sold, what they paid, who the other side was, anything outside the window.", "heading.proves": "What it proves", "proves.sealed": "The card says which snapshot of the holder's log it is as of, and this page checked it against the snapshot carried beside it.", "proves.add_up": "Every count sits inside its total, and the totals agree with each other.", "proves.recomputed": "The counts recount from the deal entries in this copy.", "heading.doesnt": "What it doesn't", "doesnt.who.person": "Who the holder is. Only that a person stands behind the agent, as they declare.", "doesnt.who.node": "Who runs the node. Only what its operator declares.", "doesnt.all": "That these are all the holder's deals. It is the window the holder's policy sets, and shop purchases are not counted.", "doesnt.one": "Anything about a single deal.", "doesnt.statement": "That the counts are right. Without the deal entries they are the holder's own statement.", "doesnt.witness": "That an outside witness saw the snapshot.", "doesnt.signature": "That the holder's key signed it. This page does not check the signature.", "doesnt.next": "What the other side will do with it. That's up to their rules. Most deal small without it and ask for more before a big one.", "heading.say": "Tell your agent", "say.what_happened": "“What happened with the cleaner?”", "say.what_happened.note": "narrated by your agent from your records · it will offer an EvidenceBook version", "say.report": "“Make an EvidenceBook report”", "say.report.note": "sealed and checked: the same page anyone can verify", "say.more_less": "“Share more history” · “Share less”", "say.attach": "“Attach my card when I buy or sell”", "say.who_asked": "“Who asked about me?”", "say.settings": "“Show me my sharing settings”", "stamp": "Policy {policy} · snapshot {anchor}", "heading.everything": "Everything on the card", "wording.provenance": "Words: {id} (sha256 {sha})", "refused.title": "This is not a standard History card", "refused.lead": "This card fails a check a standard card must pass, so none of its counts is shown.", "refused.failed": "What failed:", "refused.item": "Failed: {check}", "unreadable.title": "This History card can't be read", "unreadable.lead": "What travels beside the card can't be read, so none of its counts is shown.", "unverified.title": "This History card did not verify", "unverified.lead": "The file around the card did not pass this page's checks, so none of its counts is shown.", "heading.checks": "Which checks ran", "checks.lead": "Each check of the card, and whether this page ran it. A check that needs what the page doesn't hold says so.", "checks.column.check": "Check", "checks.column.status": "Result", "status.pass": "checked", "status.fail": "failed", "status.not_checked": "not checked here", "status.skipped": "not run: an earlier check failed", "property.card_form": "The card has exactly the members a standard card has", "property.counts_integral": "Every count is a whole number, never a fraction or a share", "property.denominator": "Every count carries its total, and the totals agree", "property.window_from_policy": "The window is the one the holder's policy sets", "property.as_of": "The card is as of the snapshot it names", "property.counts_recompute": "The counts recount from the deal entries", "property.anchor_inclusion": "An outside witness saw the snapshot", "checks.held_beside": "The policy and the snapshot this page checks against are the ones carried beside the card, as the holder states them.", "checks.not_held": "Not carried beside the card: {what}.", "checks.not_held.policy": "the policy", "checks.not_held.as_of": "the snapshot's time", "checks.not_held.deals": "the deal entries", "window.as_of": "as of {month} {day} {hh}:{mm} UTC", "lead.level": "Level they see: Standard · {window} · snapshot {date}", "card.not_counted": "{n} of {of} deals in the window are not counted.", "size.under": "under {amount}", "size.between": "{low} – {high}", "size.chip.all": "all as agreed", "size.chip.held": "{n} as agreed", "size.chip.did_not_hold": "{n} didn't", "size.chip.undecided": "{n} unsure", "doesnt.witness_counts": "That a witness checked the counts. A witness you accept recorded this snapshot; it did not check the counts.", "witness.line": "Snapshot recorded {month} {day}, {year} {hh}:{mm} UTC by {witness}", "witness.line.untimed": "Snapshot recorded by {witness}", "witness.came": "A witness receipt came with the card. This page has not checked it against a witness you accept.", "witness.list.heading": "Witnesses you accept", "witness.list.none": "None. Nothing in the card or its bundle can add one.", "witness.list.some": "{names}. Nothing in the card or its bundle can add one.", "witness.list.help": "A list of the witnesses you accept: their names and the hex DER public keys they sign receipts under. It stays on this page only and is gone when the page closes.", "witness.list.label": "Witness list (JSON)", "witness.list.save": "Save", "witness.list.reset": "Back to none", "witness.list.bad": "That is not a witness list, so nothing changed.", "property.anchor_witnessed": "A witness you accept recorded the snapshot"}; // @generated WORDS
  var PACK = {"id": "capsuleviewer.wording.consumer-en/v0", "sha256": "291e52043b5379b63fd899ad20ef6d1a4fc4ab21ab83dfcc5af0fd469377e375"}; // @generated PACK
  var KIT_CSS = "/* SPDX-License-Identifier: Apache-2.0 */\n/*\n * kit.css -- the presentation component kit's stylesheet.\n *\n * The token block below is generated from capsule_viewer/kit/tokens.py\n * (python -m capsule_viewer.kit tokens --write); tests/test_kit_tokens.py\n * fails if the two disagree. Every colour in the component rules is a\n * var(--cv-*) reference -- the same test refuses a literal colour outside the\n * generated block.\n *\n * Phone behaviour lives here, in the primitives: each responsive component is\n * its own size container and reflows on ITS width (@container), so a module\n * that places a MetricGrid or DataTable in a narrow column gets the phone\n * layout without writing a media query. Nothing in the kit scrolls the page\n * sideways: long tokens (digests, ids) wrap anywhere.\n */\n/* BEGIN GENERATED TOKENS (capsule_viewer.kit.tokens) -- do not edit by hand */\n:root {\n  --cv-font-sans: system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;\n  --cv-font-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;\n  --cv-font-size-sm: 0.8125rem;\n  --cv-font-size-md: 0.9375rem;\n  --cv-font-size-lg: 1.125rem;\n  --cv-line-height: 1.5;\n  --cv-space-1: 0.25rem;\n  --cv-space-2: 0.5rem;\n  --cv-space-3: 0.75rem;\n  --cv-space-4: 1rem;\n  --cv-space-5: 1.5rem;\n  --cv-radius: 0.5rem;\n  --cv-radius-pill: 999px;\n  --cv-color-text: #1b1f27;\n  --cv-color-text-muted: #4d5564;\n  --cv-color-surface: #ffffff;\n  --cv-color-surface-sunken: #f4f5f7;\n  --cv-color-border: #d3d7de;\n  --cv-color-accent: #2445a8;\n  --cv-color-positive-fg: #17603a;\n  --cv-color-positive-bg: #e6f3eb;\n  --cv-color-negative-fg: #a31d1d;\n  --cv-color-negative-bg: #fbe9e9;\n  --cv-color-caution-fg: #6f4700;\n  --cv-color-caution-bg: #fcf1dc;\n  --cv-color-neutral-fg: #3d4452;\n  --cv-color-neutral-bg: #eceef2;\n  --cv-color-info-fg: #1f3c94;\n  --cv-color-info-bg: #e7ecfb;\n}\n/* END GENERATED TOKENS */\n\n.cv-kit { font-family: var(--cv-font-sans); font-size: var(--cv-font-size-md); line-height: var(--cv-line-height); color: var(--cv-color-text); background: var(--cv-color-surface); overflow-wrap: anywhere; }\nbody.cv-kit { margin: 0; }\n.cv-page { max-width: 60rem; margin: 0 auto; padding: var(--cv-space-5) var(--cv-space-4); }\n.cv-kit *, .cv-kit *::before, .cv-kit *::after { box-sizing: border-box; }\n.cv-kit :where(h1, h2, h3, h4, p, ul, ol, dl, dd, figure) { margin: 0; padding: 0; }\n.cv-kit :where(ul, ol) { list-style: none; }\n.cv-mono { font-family: var(--cv-font-mono); font-size: var(--cv-font-size-sm); }\n.cv-muted { color: var(--cv-color-text-muted); }\n\n/* Section */\n.cv-section { margin: 0 0 var(--cv-space-5); }\n.cv-section__title { font-size: var(--cv-font-size-lg); font-weight: 600; margin-bottom: var(--cv-space-3); }\n.cv-section__body { display: flex; flex-direction: column; gap: var(--cv-space-3); min-width: 0; }\n\n/* MetricGrid: as many columns as fit, never narrower than the label needs; one column on a phone. */\n.cv-metrics-wrap { container-type: inline-size; }\n.cv-metrics { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 10rem), 1fr)); gap: var(--cv-space-3); }\n.cv-metric { min-width: 0; border: 1px solid var(--cv-color-border); border-radius: var(--cv-radius); padding: var(--cv-space-3); background: var(--cv-color-surface-sunken); }\n.cv-metric__label { font-size: var(--cv-font-size-sm); color: var(--cv-color-text-muted); }\n.cv-metric__value { font-size: var(--cv-font-size-lg); font-weight: 600; }\n.cv-metric__note { font-size: var(--cv-font-size-sm); color: var(--cv-color-text-muted); }\n@container (max-width: 24rem) { .cv-metrics { grid-template-columns: 1fr; } }\n\n/* DataTable: a real table when there is room; stacked label/value rows when there is not. */\n.cv-table-wrap { container-type: inline-size; }\n.cv-table { width: 100%; border-collapse: collapse; font-size: var(--cv-font-size-sm); }\n.cv-table caption { text-align: left; font-weight: 600; padding-bottom: var(--cv-space-2); }\n.cv-table th, .cv-table td { text-align: left; vertical-align: top; padding: var(--cv-space-2); border-bottom: 1px solid var(--cv-color-border); min-width: 0; }\n.cv-table th { color: var(--cv-color-text-muted); font-weight: 600; background: var(--cv-color-surface-sunken); }\n@container (max-width: 40rem) {\n  .cv-table, .cv-table tbody, .cv-table tr, .cv-table td { display: block; width: 100%; }\n  .cv-table caption { display: block; }\n  .cv-table thead { display: none; }\n  .cv-table tr { border: 1px solid var(--cv-color-border); border-radius: var(--cv-radius); margin-bottom: var(--cv-space-2); }\n  .cv-table td { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 3fr); gap: var(--cv-space-2); }\n  .cv-table td::before { content: attr(data-label); color: var(--cv-color-text-muted); font-weight: 600; }\n  .cv-table tr > td:last-child { border-bottom: 0; }\n}\n\n/* CalendarGrid: always seven columns; the cells shrink, the text wraps. */\n.cv-calendar { font-size: var(--cv-font-size-sm); }\n.cv-calendar__title { font-weight: 600; margin-bottom: var(--cv-space-2); }\n.cv-calendar__grid { display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); gap: 2px; }\n.cv-calendar__dow { text-align: center; color: var(--cv-color-text-muted); font-weight: 600; padding: var(--cv-space-1) 0; }\n.cv-calendar__day { min-width: 0; min-height: 3rem; border: 1px solid var(--cv-color-border); border-radius: 4px; padding: 2px var(--cv-space-1); background: var(--cv-color-surface); }\n.cv-calendar__day--blank { border-color: transparent; background: transparent; }\n.cv-calendar__num { font-weight: 600; }\n.cv-calendar__mark { display: block; font-size: 0.6875rem; line-height: 1.25; border-radius: 3px; padding: 0 2px; }\n\n/* Tones shared by badges, pills, calendar marks and verification outcomes. */\n.cv-tone--positive { color: var(--cv-color-positive-fg); background: var(--cv-color-positive-bg); }\n.cv-tone--negative { color: var(--cv-color-negative-fg); background: var(--cv-color-negative-bg); }\n.cv-tone--caution { color: var(--cv-color-caution-fg); background: var(--cv-color-caution-bg); }\n.cv-tone--neutral { color: var(--cv-color-neutral-fg); background: var(--cv-color-neutral-bg); }\n.cv-tone--info { color: var(--cv-color-info-fg); background: var(--cv-color-info-bg); }\n\n/* Receipt sides: ours blue, theirs amber (always \"as received\"), a missing side grey and dashed. No side is ever the negative tone: one side missing is not a finding. */\n.cv-sides { display: flex; flex-direction: column; gap: var(--cv-space-2); margin: 0 0 var(--cv-space-3); }\n.cv-sides__case { padding: var(--cv-space-1) var(--cv-space-3); border-radius: var(--cv-radius); font-weight: 600; font-size: var(--cv-font-size-sm); }\n.cv-half { min-width: 0; border-radius: var(--cv-radius); padding: var(--cv-space-1) var(--cv-space-3) var(--cv-space-2); border: 1px solid var(--cv-color-border); }\n.cv-half > .cv-section { margin-bottom: 0; }\n.cv-half__label { font-size: var(--cv-font-size-sm); font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: var(--cv-color-text-muted); margin-bottom: var(--cv-space-1); }\n.cv-half--ours { background: var(--cv-color-info-bg); border-color: var(--cv-color-info-fg); }\n.cv-half--theirs { background: var(--cv-color-caution-bg); border-color: var(--cv-color-caution-fg); }\n.cv-half--missing { background: var(--cv-color-neutral-bg); border: 1px dashed var(--cv-color-neutral-fg); }\n.cv-actions { display: flex; flex-wrap: wrap; column-gap: var(--cv-space-3); font-size: var(--cv-font-size-sm); font-weight: 600; color: var(--cv-color-info-fg); }\n.cv-actions__heading { font-weight: 400; color: var(--cv-color-text-muted); }\n\n/* DisclosureBadge and VerdictPill. The pill is scoped to the kit wrapper: the page shell's verification banner is a .cv-pill outside it, and a module's stylesheet must not restyle the banner (the frame check refuses the module). */\n.cv-badge, .cv-kit .cv-pill { display: inline-block; max-width: 100%; font-size: var(--cv-font-size-sm); font-weight: 600; border-radius: var(--cv-radius-pill); padding: 0 var(--cv-space-2); border: 1px solid currentColor; }\n.cv-badge { letter-spacing: 0.02em; }\n/* A state label breaks between words, never inside one. */\n.cv-badge, .cv-kit .cv-pill { overflow-wrap: normal; }\n.cv-refusal { color: var(--cv-color-negative-fg); background: var(--cv-color-negative-bg); border: 1px dashed currentColor; }\n\n/* CitationList */\n.cv-citations { display: flex; flex-direction: column; gap: var(--cv-space-2); counter-reset: cv-cite; }\n.cv-citation { counter-increment: cv-cite; display: grid; grid-template-columns: auto minmax(0, 1fr); gap: var(--cv-space-2); }\n.cv-citation::before { content: \"[\" counter(cv-cite) \"]\"; color: var(--cv-color-text-muted); }\n.cv-citation__ref { display: block; color: var(--cv-color-text-muted); }\n\n/* Drilldown: native details/summary -- opens with no script; prints open. */\n.cv-drilldown { border: 1px solid var(--cv-color-border); border-radius: var(--cv-radius); background: var(--cv-color-surface); }\n.cv-drilldown > summary { cursor: pointer; padding: var(--cv-space-2) var(--cv-space-3); font-weight: 600; color: var(--cv-color-accent); }\n.cv-drilldown__body { padding: 0 var(--cv-space-3) var(--cv-space-3); display: flex; flex-direction: column; gap: var(--cv-space-2); min-width: 0; }\n\n/* EvidenceDetails */\n.cv-evidence { display: flex; flex-direction: column; gap: var(--cv-space-2); }\n.cv-evidence__item { border-left: 3px solid var(--cv-color-border); padding-left: var(--cv-space-3); display: flex; flex-direction: column; gap: var(--cv-space-1); }\n.cv-evidence__head { display: flex; flex-wrap: wrap; gap: var(--cv-space-2); align-items: baseline; }\n.cv-evidence__label { font-weight: 600; }\n\n/* VerificationDetails */\n.cv-verification { display: flex; flex-direction: column; gap: var(--cv-space-2); }\n.cv-verification__outcome { border-radius: var(--cv-radius); padding: var(--cv-space-2) var(--cv-space-3); font-weight: 600; }\n.cv-checks { display: flex; flex-direction: column; gap: var(--cv-space-1); }\n.cv-check { display: flex; flex-wrap: wrap; gap: var(--cv-space-2); align-items: baseline; }\n\n/* PartyCard */\n.cv-party { border: 1px solid var(--cv-color-border); border-radius: var(--cv-radius); padding: var(--cv-space-3); background: var(--cv-color-surface-sunken); }\n.cv-party__role { font-size: var(--cv-font-size-sm); color: var(--cv-color-text-muted); text-transform: uppercase; letter-spacing: 0.04em; }\n.cv-party__name { font-weight: 600; }\n.cv-party__facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--cv-space-1) var(--cv-space-3); margin-top: var(--cv-space-2); font-size: var(--cv-font-size-sm); }\n.cv-party__facts dt { color: var(--cv-color-text-muted); }\n.cv-party-wrap { container-type: inline-size; }\n@container (max-width: 20rem) { .cv-party__facts { grid-template-columns: minmax(0, 1fr); } }\n\n/* Timeline: entries in the order given. */\n.cv-timeline { border-left: 2px solid var(--cv-color-border); padding-left: var(--cv-space-3); display: flex; flex-direction: column; gap: var(--cv-space-3); }\n.cv-timeline__at { display: block; color: var(--cv-color-text-muted); }\n.cv-timeline__label { font-weight: 600; }\n\n@media print {\n  .cv-kit { background: none; }\n  .cv-drilldown > summary { color: var(--cv-color-text); }\n  /* A closed drilldown still prints its body (Chromium's details-content). */\n  .cv-drilldown::details-content { content-visibility: visible; display: block; }\n  .cv-section, .cv-metric, .cv-party, .cv-table tr, .cv-evidence__item { break-inside: avoid; }\n}\n"; // @generated KIT_CSS
  var CARD_CSS = "/* SPDX-License-Identifier: Apache-2.0\n * The History card (capsuleviewer.standard-history/v0), on top of kit.css.\n * Colours are kit tokens only (var(--cv-*)). One column on a phone; from\n * two 22rem columns' width up, the same sections wrap into two columns\n * (SharedHistoryLaptop9b): nothing is added for the bigger screen. */\n.cv-sh { display: flex; flex-direction: column; gap: var(--cv-space-3); }\n.cv-sh__cols { columns: 22rem 2; column-gap: var(--cv-space-5); }\n.cv-sh__cols > * { break-inside: avoid; margin: 0 0 var(--cv-space-3); }\n.cv-sh__lead { border: 1px solid var(--cv-color-border); border-radius: var(--cv-radius); padding: var(--cv-space-3); background: var(--cv-color-surface-sunken); }\n.cv-sh__card { border: 2px solid var(--cv-color-text); border-radius: var(--cv-radius); padding: var(--cv-space-3) var(--cv-space-4); display: flex; flex-direction: column; gap: var(--cv-space-2); }\n.cv-sh__kicker { font-size: var(--cv-font-size-sm); letter-spacing: 0.06em; text-transform: uppercase; color: var(--cv-color-text-muted); }\n.cv-sh__total { font-size: 1.875rem; font-weight: 700; line-height: 1.1; }\n.cv-sh__buckets { display: flex; flex-wrap: wrap; column-gap: var(--cv-space-3); row-gap: var(--cv-space-1); }\n.cv-sh__bucket { display: inline-flex; align-items: baseline; gap: var(--cv-space-1); }\n.cv-sh__dot { display: inline-block; flex: none; width: 0.625rem; height: 0.625rem; border-radius: var(--cv-radius-pill); }\n.cv-sh__dot--held { background: var(--cv-color-positive-fg); }\n.cv-sh__dot--did_not_hold { background: var(--cv-color-negative-fg); }\n/* Undecided is never the agreed tone: grey, outlined, dashed. */\n.cv-sh__dot--undecided { background: var(--cv-color-neutral-bg); border: 1px dashed var(--cv-color-neutral-fg); }\n.cv-sh__rows { display: flex; flex-direction: column; }\n.cv-sh__row { display: flex; flex-wrap: wrap; justify-content: space-between; column-gap: var(--cv-space-3); padding: var(--cv-space-2) 0; border-top: 1px solid var(--cv-color-border); }\n.cv-sh__row:first-child { border-top: none; }\n.cv-sh__currency { font-weight: 600; margin-top: var(--cv-space-2); }\n.cv-sh__list { display: flex; flex-direction: column; gap: var(--cv-space-1); }\n.cv-sh__list > li { padding-left: var(--cv-space-3); border-left: 3px solid var(--cv-color-border); }\n.cv-sh__say { border: 1px solid var(--cv-color-info-fg); border-radius: var(--cv-radius); padding: var(--cv-space-3); background: var(--cv-color-info-bg); display: flex; flex-direction: column; gap: var(--cv-space-1); }\n.cv-sh__say-line { font-weight: 600; color: var(--cv-color-info-fg); }\n.cv-sh__titlerow { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; column-gap: var(--cv-space-3); margin: 0; }\n.cv-sh__meta { font-size: var(--cv-font-size-sm); font-weight: 400; color: var(--cv-color-text-muted); }\n.cv-sh__lead--own { border-color: var(--cv-color-positive-fg); background: var(--cv-color-positive-bg); }\n.cv-sh__lead > p { margin: 0 0 var(--cv-space-1); }\n.cv-sh__lead > p:last-child { margin-bottom: 0; }\n.cv-sh__count:not(.cv-sh__count--text) { border-radius: var(--cv-radius-pill); background: var(--cv-color-neutral-bg); padding: 0 var(--cv-space-2); }\n.cv-sh__bar { display: block; align-self: stretch; height: 0.5rem; border-radius: var(--cv-radius-pill); overflow: hidden; background: var(--cv-color-neutral-bg); }\n.cv-sh__seg--held { fill: var(--cv-color-positive-fg); }\n.cv-sh__seg--did_not_hold { fill: var(--cv-color-negative-fg); }\n/* Undecided is never the agreed tone: grey. */\n.cv-sh__seg--undecided { fill: var(--cv-color-neutral-fg); opacity: 0.45; }\n/* The reader's witness list: the line the card draws, and the editor under the checks. */\n.cv-sh__witness { margin: 0; font-weight: 600; }\n[data-sh-witness-list=\"true\"] textarea { display: block; inline-size: stretch; box-sizing: border-box; margin: var(--cv-space-1) 0; font-size: var(--cv-font-size-sm); }\n[data-sh-witness-list=\"true\"] button { margin-right: var(--cv-space-2); }\n"; // @generated CARD_CSS

  var EXTENSION = "x-standard-history/v0";
  var BLOCK_MEMBERS = ["policy", "as_of", "deals"];
  var AS_OF_WITNESS = ["checkpoint", "receipts"];

  // ---- the check: SH 4 to 6, the twin of the reference check_card ----------

  var DERIVATION = "standard_history/1";
  var PROPERTIES = ["card_form", "counts_integral", "denominator", "window_from_policy", "as_of", "counts_recompute"];
  var COUNTERPARTY_CLASSES = ["person_agent", "business_agent", "shop_preauth"];
  var NOT_COUNTED = "shop_preauth";
  var HOLDER_KINDS = ["person", "node"];
  var CONTROLLER_CLASSES = { person: "person_declared", node: "node_declared" };
  var ROLES = ["provided", "bought"];
  var OUTCOMES = ["held", "did_not_hold", "undecided"];
  var HAPPENED = ["stamped_by_both", "receipt_only", "no_record"];
  var SIDES = ["raised", "received"];
  var DISPUTE_OUTCOMES = ["open", "for", "against", "split", "no_finding", "withdrawn"];
  var FORUMS = ["auto", "network", "rail", "external"];
  var TALLIES = [
    ["in_window", ["counted", "not_counted"]],
    ["class_missing", ["count"]],
    ["outcomes", OUTCOMES],
    ["roles", ROLES],
    ["happened", HAPPENED],
  ];
  var CARD_MEMBERS = ["derivation", "holder_kind", "controller_class", "anchor", "since", "window", "policy", "bands", "disputes"]
    .concat(TALLIES.map(function (t) { return t[0]; }));
  var BAND_KEYS = ["currency", "decade"];
  var MAX_COUNT = 9007199254740991; // 2^53 - 1

  var HEX64 = /^[0-9a-f]{64}$/;
  var MONTH = /^[0-9]{4}-(?:0[1-9]|1[0-2])$/;
  var TIME = /^([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})Z$/;
  var VALUE = /^(?:0|[1-9][0-9]*)$/;
  var CURRENCY = /^(?:[A-Z]{3}|[-a-z0-9]{3,8}:[-_a-zA-Z0-9]{1,32}\/[-a-z0-9]{3,8}:[-.%a-zA-Z0-9]{1,128})$/;

  function InputError(message) { this.message = message; }
  InputError.prototype = Object.create(Error.prototype);
  InputError.prototype.name = "InputError";

  var own = function (o, k) { return Object.prototype.hasOwnProperty.call(o, k); };
  // A JSON object. Copies made here are null-prototype objects, so a "__proto__"
  // member is copied as a member, as the reference's dict copy keeps it.
  var isObj = function (v) { return v !== null && typeof v === "object" && !Array.isArray(v); };
  var isNumber = function (v) { return typeof v === "number"; };
  var has = function (list, v) { return list.indexOf(v) >= 0; };
  var keys = function (o) { return Object.keys(o); };
  function sameSet(a, b) {
    if (a.length !== b.length) return false;
    return a.every(function (x) { return has(b, x); });
  }
  function subset(a, b) { return a.every(function (x) { return has(b, x); }); }

  // Python's == on JSON values: numbers by value, objects by member set, arrays in order.
  function equal(a, b) {
    if (Array.isArray(a) || Array.isArray(b)) {
      return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every(function (x, i) { return equal(x, b[i]); });
    }
    if (isObj(a) || isObj(b)) {
      return isObj(a) && isObj(b) && sameSet(keys(a), keys(b)) && keys(a).every(function (k) { return equal(a[k], b[k]); });
    }
    return a === b;
  }

  var DAYS = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  function daysIn(year, month) {
    var leap = (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
    return month === 2 && leap ? 29 : DAYS[month - 1];
  }

  // An IP 5.2 time, as {y, mo, d, h, mi, s} (the reference's datetime). Throws on any other text.
  function time(text) {
    var m = typeof text === "string" ? TIME.exec(text) : null;
    if (!m) throw new InputError("not an IP §5.2 time: " + JSON.stringify(text));
    var t = { y: +m[1], mo: +m[2], d: +m[3], h: +m[4], mi: +m[5], s: +m[6] };
    if (t.y < 1 || t.mo < 1 || t.mo > 12 || t.d < 1 || t.d > daysIn(t.y, t.mo) || t.h > 23 || t.mi > 59 || t.s > 59) {
      throw new InputError("not an IP §5.2 time: " + JSON.stringify(text));
    }
    return t;
  }
  function timeKey(t) { return [t.y, t.mo, t.d, t.h, t.mi, t.s]; }
  function compare(a, b) {
    var x = timeKey(a), y = timeKey(b);
    for (var i = 0; i < x.length; i += 1) if (x[i] !== y[i]) return x[i] < y[i] ? -1 : 1;
    return 0;
  }

  // SH 5.1: the same time of day and day of the month, `months` calendar months
  // earlier, on the last day of a shorter month; null before year 1.
  function windowStart(anchor, months) {
    var total = anchor.y * 12 + (anchor.mo - 1) - months;
    var year = Math.floor(total / 12), month = total - year * 12 + 1;
    if (year < 1) return null;
    return { y: year, mo: month, d: Math.min(anchor.d, daysIn(year, month)), h: anchor.h, mi: anchor.mi, s: anchor.s };
  }

  function inWindow(agreedAt, window, anchor) {
    if (compare(agreedAt, anchor) > 0) return false;
    if (window.all === true) return true;
    var start = windowStart(anchor, window.months);
    return start === null || compare(agreedAt, start) >= 0;
  }

  // SH 5.3: digits(value) - assetScale - 1; null for a zero amount. One input
  // the reference refuses and this cannot: an assetScale written 2.0, which
  // JSON.parse reads as the integer 2 (the reference reads a float).
  function decade(amount) {
    var value = amount.value, scale = amount.assetScale;
    if (typeof value !== "string" || !VALUE.test(value)) throw new InputError("amount value is not an exact integer string: " + JSON.stringify(value));
    if (!Number.isInteger(scale) || scale < 0 || scale > 255) throw new InputError("amount assetScale out of range: " + JSON.stringify(scale));
    return value === "0" ? null : value.length - scale - 1;
  }

  // The deal entry's form (SH 5.2). A malformed entry is an input error, never a count.
  function checkEntry(deal) {
    if (!isObj(deal)) throw new InputError("a deal entry is not an object");
    if (typeof deal.deal !== "string" || !deal.deal) throw new InputError("deal entry without a deal reference");
    time(deal.agreed_at);
    if (own(deal, "counterparty_class") && !has(COUNTERPARTY_CLASSES, deal.counterparty_class)) {
      throw new InputError("unknown counterparty_class: " + JSON.stringify(deal.counterparty_class));
    }
    [["role", ROLES], ["outcome", OUTCOMES], ["happened", HAPPENED]].forEach(function (p) {
      if (!has(p[1], deal[p[0]])) throw new InputError("deal " + deal.deal + ": " + p[0] + " " + JSON.stringify(deal[p[0]]));
    });
    var amount = deal.amount;
    if (!isObj(amount) || typeof amount.assetCode !== "string" || !CURRENCY.test(amount.assetCode)) {
      throw new InputError("deal " + deal.deal + ": amount without an SR assetCode");
    }
    decade(amount);
    if (!Array.isArray(deal.disputes)) throw new InputError("deal " + deal.deal + ": disputes is not an array");
    deal.disputes.forEach(function (d) {
      if (!isObj(d) || !has(SIDES, d.side) || !has(DISPUTE_OUTCOMES, d.outcome) || !has(FORUMS, d.forum)) {
        throw new InputError("deal " + deal.deal + ": malformed dispute " + JSON.stringify(d));
      }
    });
  }

  function zero(parts) {
    var t = { of: 0 };
    parts.forEach(function (p) { t[p] = 0; });
    return t;
  }

  // SH 4: by currency (code point order), then decade, the zero-amount band first.
  function bandOrder(a, b) {
    if (a.currency !== b.currency) return a.currency < b.currency ? -1 : 1;
    if ((a.decade === null) !== (b.decade === null)) return a.decade === null ? -1 : 1;
    var x = a.decade === null ? 0 : a.decade, y = b.decade === null ? 0 : b.decade;
    return x === y ? 0 : x < y ? -1 : 1;
  }

  // The tallies SH 5 derives from the deal entries.
  function counts(deals, window, anchor) {
    var t = {};
    TALLIES.forEach(function (p) { t[p[0]] = zero(p[1]); });
    var bands = {};
    var order = [];
    var disputes = {};
    SIDES.forEach(function (side) { disputes[side] = { of: 0, by_outcome: zero(DISPUTE_OUTCOMES), by_forum: zero(FORUMS) }; });
    deals.forEach(function (deal) {
      checkEntry(deal);
      if (!inWindow(time(deal.agreed_at), window, anchor)) return;
      t.in_window.of += 1;
      if (deal.counterparty_class === NOT_COUNTED) {
        t.in_window.not_counted += 1;
        return;
      }
      t.in_window.counted += 1;
      t.class_missing.of += 1;
      if (!own(deal, "counterparty_class")) t.class_missing.count += 1;
      [["outcomes", "outcome"], ["roles", "role"], ["happened", "happened"]].forEach(function (p) {
        t[p[0]].of += 1;
        t[p[0]][deal[p[1]]] += 1;
      });
      var d = decade(deal.amount);
      var key = JSON.stringify([deal.amount.assetCode, d]);
      if (!own(bands, key)) {
        bands[key] = { currency: deal.amount.assetCode, decade: d, of: 0, held: 0, did_not_hold: 0, undecided: 0 };
        order.push(bands[key]);
      }
      bands[key].of += 1;
      bands[key][deal.outcome] += 1;
      deal.disputes.forEach(function (x) {
        var tally = disputes[x.side];
        tally.of += 1;
        tally.by_outcome.of += 1;
        tally.by_outcome[x.outcome] += 1;
        tally.by_forum.of += 1;
        tally.by_forum[x.forum] += 1;
      });
    });
    t.bands = order.sort(bandOrder);
    t.disputes = disputes;
    return t;
  }

  function windowForm(w) {
    if (!isObj(w)) return false;
    if (sameSet(keys(w), ["all"])) return w.all === true;
    return sameSet(keys(w), ["months"]) && isNumber(w.months) && w.months >= 1;
  }

  function policyWindow(w) {
    return windowForm(w) && (own(w, "all") || (Number.isInteger(w.months) && w.months <= MAX_COUNT));
  }

  // SH 6 card_form: exactly the tally's parts, each a number, and optionally `of`.
  function tallyForm(t, parts) {
    if (!isObj(t)) return false;
    var k = keys(t);
    if (!subset(parts, k) || !subset(k, parts.concat(["of"]))) return false;
    return k.every(function (x) { return isNumber(t[x]); });
  }

  function cardForm(card) {
    if (!isObj(card) || !sameSet(keys(card), CARD_MEMBERS)) return false;
    if (card.derivation !== DERIVATION || !has(HOLDER_KINDS, card.holder_kind)) return false;
    if (card.controller_class !== CONTROLLER_CLASSES[card.holder_kind]) return false;
    if (!["anchor", "policy"].every(function (m) { return typeof card[m] === "string" && HEX64.test(card[m]); })) return false;
    if (typeof card.since !== "string" || !MONTH.test(card.since)) return false;
    if (!windowForm(card.window)) return false;
    if (!TALLIES.every(function (p) { return tallyForm(card[p[0]], p[1]); })) return false;
    var bands = card.bands;
    if (!Array.isArray(bands)) return false;
    for (var i = 0; i < bands.length; i += 1) {
      var band = bands[i];
      if (!isObj(band) || !subset(BAND_KEYS, keys(band))) return false;
      if (typeof band.currency !== "string" || !CURRENCY.test(band.currency)) return false;
      if (band.decade !== null && !isNumber(band.decade)) return false;
      var rest = Object.create(null);
      keys(band).forEach(function (k) { if (!has(BAND_KEYS, k)) rest[k] = band[k]; });
      if (!tallyForm(rest, OUTCOMES)) return false;
      if (i > 0 && bandOrder(bands[i - 1], band) >= 0) return false;
    }
    var disputes = card.disputes;
    if (!isObj(disputes) || !sameSet(keys(disputes), SIDES)) return false;
    return SIDES.every(function (side) {
      var d = disputes[side];
      if (!isObj(d) || !subset(["by_outcome", "by_forum"], keys(d)) || !subset(keys(d), ["of", "by_outcome", "by_forum"])) return false;
      if (own(d, "of") && !isNumber(d.of)) return false;
      return tallyForm(d.by_outcome, DISPUTE_OUTCOMES) && tallyForm(d.by_forum, FORUMS);
    });
  }

  function numbers(v, out) {
    if (Array.isArray(v)) v.forEach(function (x) { numbers(x, out); });
    else if (isObj(v)) keys(v).forEach(function (k) { numbers(v[k], out); });
    else if (isNumber(v)) out.push(v);
    return out;
  }

  function integral(n, low) { return Number.isInteger(n) && n >= low && n <= MAX_COUNT; }

  // SH 6: every number an integer of at most 2^53 - 1 in magnitude, and 0 or
  // more except a band's decade.
  function countsIntegral(card) {
    var decades = [];
    var rest = card;
    if (isObj(card) && Array.isArray(card.bands)) {
      rest = Object.create(null);
      keys(card).forEach(function (k) { rest[k] = card[k]; });
      rest.bands = card.bands.map(function (band) {
        if (!isObj(band) || !own(band, "decade")) return band;
        decades.push(band.decade);
        var copy = Object.create(null);
        keys(band).forEach(function (k) { if (k !== "decade") copy[k] = band[k]; });
        return copy;
      });
    }
    return numbers(rest, []).every(function (n) { return integral(n, 0); }) &&
      numbers(decades, []).every(function (n) { return integral(n, -MAX_COUNT); });
  }

  function sum(list, f) { return list.reduce(function (s, x) { return s + f(x); }, 0); }
  function partitions(t, parts) { return own(t, "of") && sum(parts, function (p) { return t[p]; }) === t.of; }

  // SH 4 "Denominators". Run only on a card whose form and counts are PASS.
  function denominator(card) {
    for (var i = 0; i < TALLIES.length; i += 1) {
      var name = TALLIES[i][0], t = card[name];
      if (!own(t, "of")) return false;
      if (name === "class_missing") {
        if (t.count > t.of) return false;
      } else if (!partitions(t, TALLIES[i][1])) return false;
    }
    var counted = card.in_window.counted;
    if (["class_missing", "outcomes", "roles", "happened"].some(function (n) { return card[n].of !== counted; })) return false;
    if (card.bands.some(function (band) { return !partitions(band, OUTCOMES) || band.of < 1; })) return false;
    if (["of"].concat(OUTCOMES).some(function (o) { return sum(card.bands, function (b) { return b[o]; }) !== card.outcomes[o]; })) return false;
    return SIDES.every(function (side) {
      var d = card.disputes[side];
      if (!own(d, "of")) return false;
      if (!partitions(d.by_outcome, DISPUTE_OUTCOMES) || !partitions(d.by_forum, FORUMS)) return false;
      return d.by_outcome.of === d.of && d.by_forum.of === d.of;
    });
  }

  var status = function (ok) { return ok ? "PASS" : "FAIL"; };

  // The six card properties of SH 6, in order, from what the caller holds:
  // `policy` {digest, window}, `asOf` {anchor, anchor_time,
  // first_checkpoint_time}, `deals` (every deal entry at the anchor), each
  // optional (null or undefined); a property that needs a missing one is
  // NOT_CHECKED. Throws InputError on a malformed input, never on a malformed card.
  function checkCard(card, policy, asOf, deals) {
    var p = {};
    p.card_form = status(cardForm(card));
    p.counts_integral = status(countsIntegral(card));
    p.denominator = p.card_form === "PASS" && p.counts_integral === "PASS" ? status(denominator(card)) : "NOT_CHECKED";
    if (policy === null || policy === undefined) {
      p.window_from_policy = "NOT_CHECKED";
    } else {
      if (!policyWindow(policy.window)) throw new InputError("not a policy window: " + JSON.stringify(policy.window));
      p.window_from_policy = status(isObj(card) && windowForm(card.window) && equal(card.window, policy.window) && card.policy === policy.digest);
    }
    if (asOf === null || asOf === undefined) {
      p.as_of = "NOT_CHECKED";
    } else {
      time(asOf.first_checkpoint_time);
      p.as_of = status(isObj(card) && card.anchor === asOf.anchor && card.since === asOf.first_checkpoint_time.slice(0, 7));
    }
    var ready = deals !== null && deals !== undefined && policy !== null && policy !== undefined && asOf !== null && asOf !== undefined &&
      ["card_form", "counts_integral", "denominator"].every(function (k) { return p[k] === "PASS"; });
    if (!ready) {
      p.counts_recompute = "NOT_CHECKED";
    } else {
      var window = own(policy.window, "all") ? { all: true } : { months: policy.window.months };
      var derived = counts(deals, window, time(asOf.anchor_time));
      p.counts_recompute = status(keys(derived).every(function (k) { return equal(card[k], derived[k]); }));
    }
    return p;
  }

  // SH 6: a standard card has no FAIL, and its form, counts and denominators PASS.
  function isStandard(p) {
    return PROPERTIES.every(function (k) { return p[k] === "PASS" || p[k] === "NOT_CHECKED"; }) &&
      ["card_form", "counts_integral", "denominator"].every(function (k) { return p[k] === "PASS"; });
  }

  // ---- reading the bundle ----------------------------------------------------

  // The runtime's verification gate (resolveModules, step 1), for a context the
  // runtime did not gate (a test, a host that calls the module directly).
  function bundleVerified(v) {
    var obj = function (x) { return isObj(x) ? x : {}; };
    var claim = function (c) { return { status: obj(c).status, findings: Array.isArray(obj(c).findings) ? obj(c).findings : [] }; };
    var m = claim(obj(v).perRecordMembership);
    var results = obj(obj(v).capsuleResults);
    var unbound = function (f) { return typeof f === "string" && f.indexOf("membership_record_unbound:") === 0; };
    return isObj(v) && claim(v.graphClosure).status === "pass" && claim(v.intervalCoverage).status === "pass" &&
      (m.status === "pass" || (m.status === "fail" && m.findings.length > 0 && m.findings.every(unbound))) &&
      keys(results).every(function (k) { return obj(results[k]).ok === true; }) &&
      Array.isArray(v.disclosures) && v.disclosures.every(function (d) { return obj(d).status === "disclosure_match" || obj(d).status === "withheld"; });
  }

  // Whether the checkpoint covers the root record itself: the gate lets a
  // record whose log position is unbound through (membership_record_unbound).
  function rootCovered(context) {
    var m = isObj(context.verification) && isObj(context.verification.perRecordMembership) ? context.verification.perRecordMembership : {};
    if (m.status === "pass") return true;
    return (Array.isArray(m.findings) ? m.findings : []).indexOf("membership_record_unbound:" + context.root) < 0;
  }

  function block(bundle) {
    var ext = isObj(bundle) && isObj(bundle.extensions) ? bundle.extensions : {};
    return own(ext, EXTENSION) ? ext[EXTENSION] : undefined;
  }

  var HEX_OCTETS = /^(?:[0-9a-f]{2})+$/;
  function hexOctets(v) { return typeof v === "string" && HEX_OCTETS.test(v); }

  // The block's shape: closed; each member's own form is checkCard's (and the
  // anchor's time is read for the dates the page draws).
  function readBlock(b) {
    if (!isObj(b)) throw new InputError("the " + EXTENSION + " block is not an object");
    keys(b).forEach(function (k) { if (!has(BLOCK_MEMBERS, k)) throw new InputError("the block carries an unknown member: " + k); });
    if (own(b, "policy") && (!isObj(b.policy) || !sameSet(keys(b.policy), ["digest", "window"]) || typeof b.policy.digest !== "string")) {
      throw new InputError("the block's policy is not {digest, window}");
    }
    if (own(b, "as_of")) {
      var a = b.as_of;
      var base = ["anchor", "anchor_time", "first_checkpoint_time"];
      if (!isObj(a) || !base.every(function (k) { return own(a, k); }) || keys(a).some(function (k) { return !has(base.concat(AS_OF_WITNESS), k); }) || typeof a.anchor !== "string") {
        throw new InputError("the block's as_of is not {anchor, anchor_time, first_checkpoint_time} with optional checkpoint and receipts");
      }
      time(a.anchor_time);
      if (own(a, "checkpoint") && !hexOctets(a.checkpoint)) throw new InputError("the block's as_of.checkpoint is not lowercase hex");
      if (own(a, "receipts") && !(Array.isArray(a.receipts) && a.receipts.length >= 1 && a.receipts.length <= 64 && a.receipts.every(hexOctets))) {
        throw new InputError("the block's as_of.receipts is not 1 to 64 lowercase hex strings");
      }
    }
    if (own(b, "deals") && !Array.isArray(b.deals)) throw new InputError("the block's deals is not an array");
    return { policy: own(b, "policy") ? b.policy : null, as_of: own(b, "as_of") ? b.as_of : null, deals: own(b, "deals") ? b.deals : null };
  }

  function rootCard(context) {
    var r = context.resolvedDisclosures && context.root ? context.resolvedDisclosures.get(context.root) : undefined;
    var input = r && r.agent_input;
    if (!input || input.state !== "disclosed") throw new InputError("the root record's agent_input, the card, is not disclosed");
    return input.payload;
  }

  // ---- the model -------------------------------------------------------------

  var DEPTHS = ["L0", "L1", "L2"];

  function words(key, values) {
    var text = own(WORDS, key) ? WORDS[key] : key;
    return text.replace(/\{([a-z_]+)\}/g, function (m, name) { return values && own(values, name) ? String(values[name]) : m; });
  }

  function pad2(n) { return n < 10 ? "0" + n : String(n); }

  function dateText(t) { return words("date", { month: words("month." + t.mo), day: t.d, year: t.y }); }

  // The window as dates, when the anchor's time is held: SH 5.1's start to the anchor.
  function windowDates(window, asOf) {
    if (!asOf) return null;
    var end = time(asOf.anchor_time);
    var start = window.all === true ? null : windowStart(end, window.months);
    var dates = start === null ? words("window.dates.all", { end: dateText(end) }) : words("window.dates", { start: dateText(start), end: dateText(end) });
    return dates + " · " + words("window.as_of", { month: words("month." + end.mo), day: end.d, hh: pad2(end.h), mm: pad2(end.mi) });
  }

  function windowText(window) {
    if (window.all === true) return words("window.all");
    return window.months === 1 ? words("window.months.one") : words("window.months", { n: window.months });
  }

  // A decade's bounds as decimal text in the currency's own units: 10^d to 10^(d+1).
  function power(d) {
    if (d > 18 || d < -18) return "1e" + d;
    if (d >= 0) return "1" + new Array(d + 1).join("0");
    return "0." + new Array(-d).join("0") + "1";
  }

  // The canvas draws a currency with a symbol as "$10 – $99", and every band
  // below the first whole unit (decade 0, a sub-unit decade, the zero amount)
  // as one "under $10" row of that currency. Another currency keeps its decade
  // in its own units. A symbol stands only in its currency's own rows.
  var SYMBOLS = { USD: "$", EUR: "\u20ac", GBP: "\u00a3" };

  function grouped(digits) { return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ","); }

  function symbolLabel(sym, decade) {
    if (decade === null || decade <= 0) return words("size.under", { amount: sym + "10" });
    return words("size.between", { low: sym + grouped(power(decade)), high: sym + grouped(power(decade + 1).slice(1).replace(/0/g, "9")) });
  }

  function bandLabel(band) {
    if (own(SYMBOLS, band.currency) && (band.decade === null || band.decade <= 17)) return symbolLabel(SYMBOLS[band.currency], band.decade);
    if (band.decade === null) return words("size.zero", { currency: band.currency });
    return words("size.range", { low: power(band.decade), high: power(band.decade + 1), currency: band.currency });
  }

  // {state, own, properties, card?, findings?}: what render draws.
  function buildModelSync(context) {
    var bundle = isObj(context) ? context.bundle : undefined;
    var b = block(bundle);
    var ownCopy = isObj(b) && own(b, "deals");
    var none = {};
    PROPERTIES.forEach(function (p) { none[p] = "NOT_CHECKED"; });
    if (!bundleVerified(context && context.verification)) return { state: "unverified", own: ownCopy, properties: none };
    var inputs, card, properties;
    try {
      inputs = readBlock(b);
      card = rootCard(context);
      properties = checkCard(card, inputs.policy, inputs.as_of, inputs.deals);
    } catch (err) {
      if (!(err instanceof InputError)) throw err;
      // The error text can quote a deal entry: it is never drawn.
      return { state: "unreadable", own: ownCopy, properties: none };
    }
    var held = { policy: inputs.policy !== null, as_of: inputs.as_of !== null, deals: inputs.deals !== null };
    var witnessIn = inputs.as_of && (own(inputs.as_of, "receipts") || own(inputs.as_of, "checkpoint"))
      ? { anchor: inputs.as_of.anchor, checkpoint: own(inputs.as_of, "checkpoint") ? inputs.as_of.checkpoint : null, receipts: own(inputs.as_of, "receipts") ? inputs.as_of.receipts : null }
      : null;
    // NOT_CHECKED because an earlier check failed, not because an input is missing.
    var first = ["card_form", "counts_integral"].every(function (k) { return properties[k] === "PASS"; });
    var skipped = [];
    if (!first) skipped.push("denominator");
    if (held.policy && held.as_of && held.deals && !(first && properties.denominator === "PASS")) skipped.push("counts_recompute");
    if (!isStandard(properties)) {
      return { state: "refused", own: ownCopy, properties: properties, held: held, witnessIn: witnessIn, skipped: skipped, fails: PROPERTIES.filter(function (p) { return properties[p] === "FAIL"; }) };
    }
    return {
      state: "card",
      own: ownCopy,
      properties: properties,
      held: held,
      skipped: skipped,
      witnessIn: witnessIn,
      covered: rootCovered(context),
      card: card,
      window: windowText(card.window),
      dates: windowDates(card.window, inputs.as_of),
      snapshotDate: inputs.as_of ? dateText(time(inputs.as_of.anchor_time)) : null,
      bands: card.bands.map(function (band) { return { currency: band.currency, decade: band.decade, label: bandLabel(band), of: band.of, held: band.held, did_not_hold: band.did_not_hold, undecided: band.undecided }; }),
    };
  }

  // ---- the witness step (SH-4; IP1 6.2.2; capsule-viewer#183) ---------------

  // "{month} {day}, {year} {hh}:{mm}" UTC from a NumericDate, or null.
  function witnessedAt(seconds) {
    if (typeof seconds !== "number" || !isFinite(seconds)) return null;
    var d = new Date(seconds * 1000);
    if (isNaN(d.getTime())) return null;
    return { month: words("month." + (d.getUTCMonth() + 1)), day: d.getUTCDate(), year: d.getUTCFullYear(), hh: pad2(d.getUTCHours()), mm: pad2(d.getUTCMinutes()) };
  }

  function attach(m, witness) {
    var out = {};
    keys(m).forEach(function (k) { out[k] = m[k]; });
    out.witness = witness;
    // The model before the witness step: the page runs the step again when the reader changes their list.
    Object.defineProperty(out, "pre", { value: m, enumerable: false });
    return out;
  }

  // The reader's list, as names; none when the runtime holds none.
  function acceptedNames(EG) {
    var list = EG && typeof EG.readerWitnessPolicy === "function" ? EG.readerWitnessPolicy() : null;
    return list && Array.isArray(list.witnesses) ? list.witnesses.map(function (w) { return w.name; }) : [];
  }

  // The page's model with the snapshot's witness standing, from the runtime's
  // readerAnchorWitness: the block's checkpoint against its anchor, and the
  // Receipts under the reader's own list. The block's checkpoint is held to its
  // anchor whenever it is carried, on a refused card too: one that is not the
  // anchor's makes the block unreadable. The witness line is shown only when the
  // result is PASS and the card's as_of property is PASS; a FAIL says nothing
  // about a witness, and only NOT_CHECKED says a receipt came with the card,
  // not checked. A runtime without the check is NOT_CHECKED, never a pass.
  function withWitness(m) {
    var w = m.witnessIn;
    if (!w) return Promise.resolve(attach(m, { status: "NOT_PRESENT", shown: false, accepted: [] }));
    var EG = root.EvidenceGraph;
    var accepted = acceptedNames(EG);
    var unchecked = function () { return attach(m, { status: w.receipts ? "NOT_CHECKED" : "NOT_PRESENT", shown: false, accepted: accepted }); };
    if (!EG || typeof EG.readerAnchorWitness !== "function") return Promise.resolve(unchecked());
    return Promise.resolve(EG.readerAnchorWitness(w.anchor, w.checkpoint, w.receipts)).then(function (r) {
      if (r.checkpoint === "mismatch") {
        var none = {};
        PROPERTIES.forEach(function (p) { none[p] = "NOT_CHECKED"; });
        return { state: "unreadable", own: m.own, properties: none };
      }
      var status = r.checkpoint === "unchecked" && r.status === "PASS" ? "NOT_CHECKED" : r.status;
      var shown = m.state === "card" && status === "PASS" && m.properties.as_of === "PASS" && typeof r.witness === "string";
      var witness = { status: status, shown: shown, accepted: accepted };
      if (shown) { witness.witness = r.witness; witness.at = witnessedAt(r.witnessed_time); }
      return attach(m, witness);
    }, function () { return unchecked(); });
  }

  function buildModel(context) { return Promise.resolve(buildModelSync(context)).then(withWitness); }

  // ---- drawing (the host's document; kit class names) -------------------------

  function el(doc, tag, attrs, children) {
    var node = doc.createElement(tag);
    keys(attrs || {}).forEach(function (k) { node.setAttribute(k, attrs[k]); });
    (children || []).forEach(function (c) {
      if (c === null || c === undefined) return;
      node.appendChild(typeof c === "string" ? doc.createTextNode(c) : c);
    });
    return node;
  }

  function section(doc, title, children, attrs) {
    var a = { class: "cv-section", "data-cv": "section" };
    keys(attrs || {}).forEach(function (k) { a[k] = attrs[k]; });
    return el(doc, "section", a, [
      el(doc, "h3", { class: "cv-section__title" }, [title]),
      el(doc, "div", { class: "cv-section__body" }, children),
    ]);
  }

  function drilldown(doc, summary, children, open, attrs) {
    var a = { class: "cv-drilldown", "data-cv": "drilldown" };
    keys(attrs || {}).forEach(function (k) { a[k] = attrs[k]; });
    var node = el(doc, "details", a, [
      el(doc, "summary", {}, [summary]),
      el(doc, "div", { class: "cv-drilldown__body" }, children),
    ]);
    if (open) node.open = true;
    return node;
  }

  function row(doc, label, value, attrs) {
    return el(doc, "div", Object.assign({ class: "cv-sh__row" }, attrs || {}), [el(doc, "span", {}, [label]), el(doc, "span", { class: "cv-sh__count" }, [value])]);
  }

  // The outcome bar (SH 6, HF 4.6 "A count bar"): the three counts of the outcomes tally, held,
  // did_not_hold, undecided, as three segments of one bar. The viewBox is `of` units wide and each
  // segment is as wide as its count in those units, so the bar adds no figure and divides nothing.
  // Drawn only for a tally whose `of` is 1 or more; the counts stay beside it.
  var SVG_NS = "http://www.w3.org/2000/svg";
  function outcomeBar(doc, o) {
    if (!(o.of >= 1)) return null;
    var svg = doc.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "cv-sh__bar");
    svg.setAttribute("viewBox", "0 0 " + o.of + " 1");
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("role", "img");
    svg.setAttribute("data-sh-bar", "outcomes");
    svg.setAttribute("aria-label", ["held", "did_not_hold", "undecided"].map(function (k) { return words("count.of", { n: o[k], of: o.of }) + " " + words("outcome." + k); }).join(", "));
    var at = 0;
    ["held", "did_not_hold", "undecided"].forEach(function (k) {
      if (!o[k]) return;
      var rect = doc.createElementNS(SVG_NS, "rect");
      rect.setAttribute("class", "cv-sh__seg cv-sh__seg--" + k);
      rect.setAttribute("data-seg", k);
      rect.setAttribute("x", String(at));
      rect.setAttribute("y", "0");
      rect.setAttribute("width", String(o[k]));
      rect.setAttribute("height", "1");
      svg.appendChild(rect);
      at += o[k];
    });
    return svg;
  }

  function dot(doc, outcome) { return el(doc, "span", { class: "cv-sh__dot cv-sh__dot--" + outcome, "aria-hidden": "true" }, []); }

  function banner(doc) {
    return el(doc, "p", { class: "cv-notice cv-tone--caution", "data-banner": "private" }, [
      el(doc, "strong", {}, [words("banner.private.label")]), " ", words("banner.private.text"),
    ]);
  }

  var who = function (m) { return m.own ? "own" : "shared"; };

  // The snapshot's witness line (9b frame 1): only a PASS under the reader's own
  // list, on a card whose as_of also passes. A Receipt that came but was not
  // checked says so; a failed one, or none, says nothing.
  function drawWitness(doc, m) {
    var w = m.witness;
    if (!w) return null;
    if (w.shown) {
      var text = w.at ? words("witness.line", { month: w.at.month, day: w.at.day, year: w.at.year, hh: w.at.hh, mm: w.at.mm, witness: w.witness })
        : words("witness.line.untimed", { witness: w.witness });
      return el(doc, "p", { class: "cv-sh__witness", "data-sh-witness": "line" }, [text]);
    }
    return w.status === "NOT_CHECKED" ? el(doc, "p", { class: "cv-muted", "data-sh-witness": "came" }, [words("witness.came")]) : null;
  }

  function drawCard(doc, m) {
    var c = m.card, o = c.outcomes, v = who(m);
    var holder = words(c.holder_kind === "node" ? "holder.node" : "holder.person");
    var since = words("holder.since", { month: words("since", { month: words("month." + Number(c.since.slice(5, 7))), year: c.since.slice(0, 4) }) });
    var total = o.of === 0
      ? [el(doc, "p", { "data-sh-total": "none" }, [words("total.none")])]
      : [
        el(doc, "p", { class: "cv-sh__total", "data-sh-total": "held" }, [words("total.of", { n: o.held, of: o.of })]),
        el(doc, "p", {}, [words("total.lead." + v)]),
        outcomeBar(doc, o),
        el(doc, "p", { class: "cv-sh__buckets" }, [
          el(doc, "span", { class: "cv-sh__bucket", "data-outcome": "held" }, [dot(doc, "held"), words("count.of", { n: o.held, of: o.of }) + " " + words("outcome.held")]),
          el(doc, "span", { class: "cv-sh__bucket", "data-outcome": "did_not_hold" }, [dot(doc, "did_not_hold"), words("count.of", { n: o.did_not_hold, of: o.of }) + " " + words("outcome.did_not_hold")]),
          el(doc, "span", { class: "cv-sh__bucket", "data-outcome": "undecided" }, [dot(doc, "undecided"), words("count.of", { n: o.undecided, of: o.of }) + " " + words("outcome.undecided")]),
        ]),
      ];
    var recounted = m.properties.counts_recompute === "PASS";
    var missing = ["policy", "as_of"].filter(function (k) { return !m.held[k]; }).map(function (k) { return words("checks.not_held." + k); });
    var statementText = recounted ? words("counts.recomputed")
      : m.held.deals ? words("counts.statement.entries", { what: missing.join(", ") }) : words("counts.statement");
    var statement = el(doc, "p", { class: "cv-muted", "data-sh-counts": recounted ? "recomputed" : "statement" }, [statementText]);
    var cardBox = el(doc, "div", { class: "cv-sh__card", "data-sh": "card" }, [
      el(doc, "p", { class: "cv-sh__kicker" }, [words("card.kicker")]),
      el(doc, "p", { "data-sh-holder": c.holder_kind }, [holder + " · " + since]),
      el(doc, "p", { class: "cv-muted", "data-sh-window": "true" }, [m.dates || words("window.no_dates", { window: m.window })]),
    ].concat(total, [
      el(doc, "p", { class: "cv-muted", "data-sh-not-counted-line": "true" }, [words("card.not_counted", { n: c.in_window.not_counted, of: c.in_window.of })]),
      statement,
      drawWitness(doc, m),
      el(doc, "p", { class: "cv-muted cv-mono", "data-sh-stamp": "true" }, [words("stamp", { policy: c.policy.slice(0, 12), anchor: c.anchor.slice(0, 12) })]),
    ]));
    var l0 = [
      m.own ? banner(doc) : null,
      el(doc, "h2", { class: "cv-sh__titlerow", "data-sh-title": "true" }, [
        el(doc, "span", {}, [words("title")]),
        " ",
        el(doc, "span", { class: "cv-sh__meta" }, [words("title.standard") + " · " + m.window]),
      ]),
      el(doc, "div", { class: "cv-sh__lead cv-sh__lead--" + v }, [
        el(doc, "p", {}, [words("lead." + v)]),
        m.own && m.snapshotDate ? el(doc, "p", { class: "cv-muted", "data-sh-lead": "level" }, [words("lead.level", { window: m.window, date: m.snapshotDate })]) : null,
      ]),
      cardBox,
      m.own ? drawSay(doc) : null,
    ];
    var l1 = drilldown(doc, words("heading.everything"), [el(doc, "div", { class: "cv-sh__cols" }, [
      drawRoles(doc, c, v), drawSizes(doc, m), drawHappened(doc, c, v), drawDisputes(doc, c, v), drawCounts(doc, c, v), drawProves(doc, m), drawDoesnt(doc, m),
    ])], false, { "data-sh-level": "L1" });
    return [el(doc, "div", { class: "cv-sh" }, [el(doc, "div", { class: "cv-sh__cols" }, l0)]), l1];
  }

  function drawRoles(doc, c, v) {
    return section(doc, words("heading.roles"), [el(doc, "div", { class: "cv-sh__rows" }, [
      row(doc, words("role.provided." + v), words("count.of", { n: c.roles.provided, of: c.roles.of }), { "data-role": "provided" }),
      row(doc, words("role.bought." + v), words("count.of", { n: c.roles.bought, of: c.roles.of }), { "data-role": "bought" }),
    ])], { "data-sh-section": "roles" });
  }

  // "9 · all as agreed", "27 · 26 as agreed · 1 unsure": the band's total first, then each part that is not zero.
  function sizeChip(b) {
    var parts = [String(b.of)];
    if (b.held === b.of) parts.push(words("size.chip.all"));
    else {
      if (b.held > 0) parts.push(words("size.chip.held", { n: b.held }));
      if (b.did_not_hold > 0) parts.push(words("size.chip.did_not_hold", { n: b.did_not_hold }));
      if (b.undecided > 0) parts.push(words("size.chip.undecided", { n: b.undecided }));
    }
    return parts.join(" \u00b7 ");
  }

  // One group per currency, in the card's order; a row never names a second currency.
  function drawSizes(doc, m) {
    if (!m.bands.length) return section(doc, words("heading.sizes"), [el(doc, "p", {}, [words("sizes.none")])], { "data-sh-section": "sizes" });
    var groups = [];
    m.bands.forEach(function (band) {
      var last = groups[groups.length - 1];
      if (!last || last.currency !== band.currency) groups.push(last = { currency: band.currency, bands: [] });
      last.bands.push(band);
    });
    var body = [el(doc, "p", { class: "cv-muted" }, [words("sizes.note")])];
    groups.forEach(function (g) {
      // Bands of one currency that read the same (the sub-unit decades, the zero amount) are one row.
      var rows = [];
      g.bands.forEach(function (band) {
        var last = rows[rows.length - 1];
        if (last && last.label === band.label) {
          ["of", "held", "did_not_hold", "undecided"].forEach(function (k) { last[k] += band[k]; });
        } else {
          rows.push({ currency: band.currency, label: band.label, of: band.of, held: band.held, did_not_hold: band.did_not_hold, undecided: band.undecided });
        }
      });
      body.push(el(doc, "p", { class: "cv-sh__currency", "data-currency-group": g.currency }, [g.currency]));
      body.push(el(doc, "div", { class: "cv-sh__rows", "data-currency-rows": g.currency }, rows.map(function (band) {
        return el(doc, "div", { class: "cv-sh__row", "data-band-currency": band.currency }, [
          el(doc, "span", {}, [band.label]),
          el(doc, "span", { class: "cv-sh__count cv-sh__count--text" }, [sizeChip(band)]),
        ]);
      })));
    });
    return section(doc, words("heading.sizes"), body, { "data-sh-section": "sizes" });
  }

  function drawHappened(doc, c, v) {
    var h = c.happened;
    return section(doc, words("heading.happened"), [
      el(doc, "div", { class: "cv-sh__rows" }, [
        row(doc, words("happened.stamped_by_both"), words("count.of", { n: h.stamped_by_both, of: h.of }), { "data-happened": "stamped_by_both" }),
        row(doc, words(v === "own" ? "happened.receipt_only" : "happened.receipt_only.shared"), words("count.of", { n: h.receipt_only, of: h.of }), { "data-happened": "receipt_only" }),
        row(doc, words("happened.no_record"), words("count.of", { n: h.no_record, of: h.of }), { "data-happened": "no_record" }),
      ]),
      el(doc, "p", { class: "cv-muted" }, [words("happened.note")]),
    ], { "data-sh-section": "happened" });
  }

  function drawDisputes(doc, c, v) {
    var body = SIDES.map(function (side) {
      var d = c.disputes[side], bo = d.by_outcome, bf = d.by_forum;
      var values = { of: d.of, who: words("disputes.who." + v) };
      DISPUTE_OUTCOMES.forEach(function (k) { values[k] = bo[k]; });
      FORUMS.forEach(function (k) { values[k] = bf[k]; });
      return el(doc, "div", { "data-dispute-side": side }, [
        row(doc, words("disputes." + side + "." + v), String(d.of)),
        d.of ? el(doc, "p", { class: "cv-muted" }, [words("disputes.outcomes", values)]) : null,
        d.of ? el(doc, "p", { class: "cv-muted" }, [words("disputes.forums", values)]) : null,
      ]);
    });
    return section(doc, words("heading.disputes"), body, { "data-sh-section": "disputes" });
  }

  function drawCounts(doc, c, v) {
    return section(doc, words("heading.counts"), [
      el(doc, "p", {}, [words("counts.what." + v)]),
      el(doc, "p", { "data-sh-not-counted": "true" }, [words("counts.not_counted." + v, { n: c.in_window.not_counted, of: c.in_window.of })]),
      c.class_missing.count ? el(doc, "p", { "data-sh-class-missing": "true" }, [words("counts.class_missing", { n: c.class_missing.count, of: c.class_missing.of })]) : null,
      el(doc, "p", {}, [words("counts.not_on." + v)]),
    ], { "data-sh-section": "counts" });
  }

  function list(doc, items) { return el(doc, "ul", { class: "cv-sh__list" }, items.map(function (t) { return el(doc, "li", {}, [t]); })); }

  // Only what ran: the page's own checks, never a witness or a signature.
  function drawProves(doc, m) {
    var items = m.covered && m.properties.as_of === "PASS" ? [words("proves.sealed"), words("proves.add_up")] : [words("proves.add_up")];
    if (m.properties.counts_recompute === "PASS") items.push(words("proves.recomputed"));
    return section(doc, words("heading.proves"), [list(doc, items)], { "data-sh-section": "proves" });
  }

  function drawDoesnt(doc, m) {
    var items = [words(m.card.holder_kind === "node" ? "doesnt.who.node" : "doesnt.who.person"), words("doesnt.all"), words("doesnt.one")];
    if (m.properties.counts_recompute !== "PASS") items.push(words("doesnt.statement"));
    items.push(words("doesnt.signature"), words(m.witness && m.witness.shown ? "doesnt.witness_counts" : "doesnt.witness"), words("doesnt.next"));
    return section(doc, words("heading.doesnt"), [list(doc, items)], { "data-sh-section": "doesnt" });
  }

  function drawSay(doc) {
    var line = function (key) { return el(doc, "p", { class: "cv-sh__say-line" }, [words(key)]); };
    var note = function (key) { return el(doc, "p", { class: "cv-muted" }, [words(key)]); };
    return el(doc, "div", { class: "cv-sh__say", "data-sh": "say" }, [
      el(doc, "p", { class: "cv-muted" }, [words("heading.say")]),
      line("say.what_happened"), note("say.what_happened.note"),
      line("say.report"), note("say.report.note"),
      line("say.more_less"), line("say.attach"), line("say.who_asked"), line("say.settings"),
    ]);
  }

  // L2: every property, anchor_inclusion with them, and what was held beside the card.
  // The reader's own witness list, shown whenever Receipts came with the card,
  // and the way to change it. The list is the reader's: no member of the bundle
  // reaches it.
  function drawWitnessList(doc, m, again) {
    var EG = root.EvidenceGraph;
    var current = EG && typeof EG.readerWitnessPolicy === "function" ? EG.readerWitnessPolicy() : { witnesses: [] };
    var names = current.witnesses.map(function (w) { return w.name; });
    var editable = EG && typeof EG.setReaderWitnessPolicy === "function";
    var status = el(doc, "p", { class: "cv-muted", role: "status", "data-sh-witness-list": "status" }, []);
    var box = el(doc, "textarea", { rows: "6", class: "cv-mono", "data-sh-witness-list": "input", id: "cv-sh-witness-list", spellcheck: "false" }, []);
    box.value = JSON.stringify(current, null, 2);
    var save = el(doc, "button", { type: "button", "data-sh-witness-list": "save" }, [words("witness.list.save")]);
    var reset = el(doc, "button", { type: "button", "data-sh-witness-list": "reset" }, [words("witness.list.reset")]);
    save.addEventListener("click", function () {
      var value;
      try { value = JSON.parse(box.value); } catch (err) { value = undefined; }
      if (value !== undefined && EG.setReaderWitnessPolicy(value)) again();
      else status.textContent = words("witness.list.bad");
    });
    reset.addEventListener("click", function () { EG.setReaderWitnessPolicy(null); again(); });
    return el(doc, "div", { "data-sh-witness-list": "true" }, [
      el(doc, "p", {}, [words("witness.list.heading") + ": " + (names.length ? words("witness.list.some", { names: names.join(", ") }) : words("witness.list.none"))]),
      // Not a toggle of its own: the checks section is already one, and the kit does not nest them.
      editable ? el(doc, "div", {}, [
        el(doc, "p", { class: "cv-muted" }, [words("witness.list.help")]),
        el(doc, "label", { for: "cv-sh-witness-list" }, [words("witness.list.label")]),
        box, save, reset, status,
      ]) : null,
    ]);
  }

  function drawChecks(doc, m, again) {
    // anchor_inclusion stays NOT_CHECKED for a derivation (SH 3). The witness result is its own row, anchor_witnessed
    // (IP1 6.2.2): drawn for a PASS under the reader's list, a Receipt that came and was not checked, and a Receipt
    // that was checked and failed (a failure is never shown as "not checked"); the line itself draws only for a PASS.
    var w = m.witness;
    var rowNames = PROPERTIES.concat(["anchor_inclusion"]);
    if (w && (w.shown || w.status === "NOT_CHECKED" || w.status === "FAIL")) rowNames.push("anchor_witnessed");
    var rows = rowNames.map(function (p) {
      var s = p === "anchor_inclusion" ? "NOT_CHECKED" : p === "anchor_witnessed" ? (w.shown ? "PASS" : w.status === "FAIL" ? "FAIL" : "NOT_CHECKED") : m.properties[p];
      var skip = (m.skipped || []).indexOf(p) >= 0;
      return el(doc, "tr", { "data-property": p, "data-property-status": s, "data-skipped": skip ? "true" : "false" }, [
        el(doc, "td", { "data-label": words("checks.column.check") }, [words("property." + p)]),
        el(doc, "td", { "data-label": words("checks.column.status") }, [words(skip ? "status.skipped" : "status." + s.toLowerCase())]),
      ]);
    });
    var held = m.held || { policy: false, as_of: false, deals: false };
    var missing = ["policy", "as_of", "deals"].filter(function (k) { return !held[k]; }).map(function (k) { return words("checks.not_held." + k); });
    return drilldown(doc, words("heading.checks"), [
      el(doc, "p", {}, [words("checks.lead")]),
      el(doc, "div", { class: "cv-table-wrap", "data-cv": "data-table" }, [el(doc, "table", { class: "cv-table" }, [
        el(doc, "thead", {}, [el(doc, "tr", {}, [el(doc, "th", { scope: "col" }, [words("checks.column.check")]), el(doc, "th", { scope: "col" }, [words("checks.column.status")])])]),
        el(doc, "tbody", {}, rows),
      ])]),
      held.policy || held.as_of ? el(doc, "p", { class: "cv-muted" }, [words("checks.held_beside")]) : null,
      m.witnessIn && m.witnessIn.receipts ? drawWitnessList(doc, m, again) : null,
      missing.length ? el(doc, "p", { class: "cv-muted", "data-sh-not-held": "true" }, [words("checks.not_held", { what: missing.join(", ") })]) : null,
      el(doc, "p", { class: "cv-muted" }, [words("wording.provenance", { id: PACK.id, sha: PACK.sha256 || "-" })]),
    ], false, { "data-sh-level": "L2" });
  }

  function drawRefusal(doc, m) {
    var title = { refused: "refused.title", unreadable: "unreadable.title", unverified: "unverified.title" }[m.state];
    var lead = { refused: "refused.lead", unreadable: "unreadable.lead", unverified: "unverified.lead" }[m.state];
    return el(doc, "div", { class: "cv-sh", "data-sh": m.state }, [
      m.own ? banner(doc) : null,
      el(doc, "h2", {}, [words(title)]),
      el(doc, "p", { class: "cv-notice cv-refusal" }, [words(lead)]),
      m.fails && m.fails.length ? el(doc, "p", {}, [words("refused.failed")]) : null,
      m.fails && m.fails.length ? list(doc, m.fails.map(function (p) { return words("refused.item", { check: words("property." + p) }); })) : null,
    ]);
  }

  // Each stylesheet once per document: their bytes are what style_sha256 pins.
  function ensureStyles(doc) {
    [["cv-kit-css", KIT_CSS], ["cv-sh-css", CARD_CSS]].forEach(function (s) {
      if (!s[1] || doc.getElementById(s[0])) return;
      var style = doc.createElement("style");
      style.id = s[0];
      style.textContent = s[1];
      (doc.head || doc.documentElement).appendChild(style);
    });
  }

  // ---- the module (presentation contract: canRender, buildModel, render) ------

  // Any block: a bad one is drawn as what is wrong with it, never passed over.
  function canRender(context) {
    return block(isObj(context) ? context.bundle : undefined) !== undefined;
  }

  // Draws into host.L0, host.L1 and host.L2: the card, or only why there is none.
  function render(model, host) { draw(model, host, { gen: 0 }); }

  // `state` is this host's: it counts the reader's list changes on it, so only the
  // newest one's check is drawn, and another host's change never supersedes it.
  function draw(model, host, state) {
    var doc = host.L0.ownerDocument;
    // The reader changed their witness list: check the Receipts again, redraw.
    var again = function () {
      state.gen += 1;
      var mine = state.gen;
      withWitness(model.pre || model).then(function (next) {
        if (mine !== state.gen) return; // a later change of the list is newer than this check
        [host.L0, host.L1, host.L2].forEach(function (region) {
          Array.prototype.slice.call(region.children).forEach(function (child) {
            if (child.getAttribute("data-sh-module") === "capsuleviewer.standard-history/v0") region.removeChild(child);
          });
        });
        draw(next, host, state);
      });
    };
    var depth = DEPTHS.indexOf(host.depth) >= 0 ? host.depth : "L0";
    ensureStyles(doc);
    var parts = model.state === "card" ? drawCard(doc, model) : [drawRefusal(doc, model), null];
    parts.push(model.state === "card" || model.state === "refused" ? drawChecks(doc, model, again) : null);
    if (parts[1] && depth !== "L0") parts[1].open = true;
    if (parts[2] && depth === "L2") parts[2].open = true;
    [host.L0, host.L1, host.L2].forEach(function (region, i) {
      if (!parts[i]) return;
      var wrap = doc.createElement("div");
      wrap.className = "cv-kit";
      wrap.setAttribute("data-sh-module", "capsuleviewer.standard-history/v0");
      wrap.appendChild(parts[i]);
      wrap.normalize();
      region.appendChild(wrap);
    });
    host.L0.setAttribute("data-sh-state", model.state);
    host.L0.setAttribute("data-sh-copy", model.own ? "own" : "shared");
  }

  var module = { manifest: MANIFEST, title: words("module.title"), canRender: canRender, buildModel: buildModel, render: render };

  root.CapsuleViewerStandardHistory = {
    EXTENSION: EXTENSION,
    PROPERTIES: PROPERTIES,
    MANIFEST: MANIFEST,
    WORDS: WORDS,
    PACK: PACK,
    module: module,
    buildModelSync: buildModelSync,
    withWitness: withWitness,
    checkCard: checkCard,
    isStandard: isStandard,
    counts: counts,
    bandLabel: bandLabel,
    InputError: InputError,
  };
  if (typeof root.EvidenceGraph !== "undefined" && root.EvidenceGraph && typeof root.EvidenceGraph.registerPresentation === "function") {
    root.EvidenceGraph.registerPresentation(module);
  }
})(typeof globalThis !== "undefined" ? globalThis : this);
