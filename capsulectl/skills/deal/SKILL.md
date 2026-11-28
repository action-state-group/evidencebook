---
name: deal
description: The record and final review of every errand that ends in a checkout, payment, order, booking, signature, cancellation, or sharing of the user's contact details or credentials that an agent does for the user, including a retail checkout from a known merchant at a fixed price. Seals the offer, the agreement (both sides' when the other side keeps signed records), what was asked, shown, chosen, approved and done with `capsulectl deal`, and shows the user any difference. Also asks for and answers evidence in an evidence book, compares a period with a counterparty's, and seals and shows a Result's report with `capsulectl book`.
---

# deal

Every deal has one shape: a commitment to someone, on certain terms, based on
certain claims, at a point you can't easily undo. This skill records the deal
between the user and the other side, each keeping its own book, and is the
final review of the procedures that reach that point: it seals the offer and
both sides' agreement, seals each step, checks the step against what was
asked and agreed, and seals the answer, using `capsulectl deal`. It is silent
unless something differs.

## The five verbs

This skill teaches five verbs and nothing else: **check** before you act,
**record** what was done, **ask** the other side for evidence, or answer when
it asks, **verify** what you get, and **show** a card, a report or a receipt. Each row
maps a verb, at one step, to the existing command. The `deal` commands run on
the deal profile (`--profile deal`); the `book` commands run on a jsonl book
profile, never the deal profile, and only after you have read
[references/book.md](references/book.md), whose guardrails bind them.

| Verb | Step | Command |
|---|---|---|
| check | before agreeing: the other side's admission | `deal admit` with capsule-engine's `admission evaluate --admit` output |
| check | before any act: the final review | `deal check` |
| record | the offer, a counter-offer, a withdrawal or a decline | `deal offer`, `deal offer cancel`, `deal offer decline` |
| record | what the user asked, the journey, the pick, the user's answer, a disclosure, evidence | `deal note` |
| record | the agreement | `deal agree` |
| record | a payment, sent and settled, or received | `deal settle` |
| record | an act other than a payment | `deal perform` |
| record | how each row went | `deal statement` |
| record | a dispute, or a challenge to a ruling | `deal dispute` |
| record | a file the other side sent | `deal receive` |
| record | a Result a judging step produced | `book result build` (book profile) |
| record | an evaluator's Result over a holder's answer | `book report derive` (book profile) |
| ask | evidence from a party: the request, then its answer, or that none came | `book request`, then `book request --for RECORD_ID --response FILE` or `--absent-until TIME` (book profile) |
| ask | answer a request a party sent | `book respond` (book profile); a referee's request for the deal is the user's to answer (`deal respond`) |
| verify | a bundle or a copy before you treat it as authentic | `capsulectl verify --bundle`; `capsulectl deal verify --bundle` for a deal copy |
| verify | a period against a counterparty's held bundle | `book reconcile` (book profile) |
| show | the user's deals | `deal list --json` |
| show | one deal, its receipt, or the copy for the other side | `deal report` |
| show | a sealed Result's report page | `book report build` (book profile) |
| show | the user's History card, as a file the History page draws | `deal standard-history card` |

To ask another party's book over the profile's transport (`book remote
request`), stop and ask a human. `book request` on a remote profile is its
deprecated alias. Setting up a profile, the checkpoint cadence, the daily
check of host actions, windows and the x-deal-v0 sale are expert commands, not
taught here: [references/setup.md](references/setup.md) and
[references/expert.md](references/expert.md) keep them.

The one commerce card is the History card, `standard_history`. A Mesh
inference node's service card and its checks are Mesh's own exception, not a
second commerce card: this skill never asks for one.

**Version.** capsulectl-deal writes x-deal-v1 deals by default, and this file
teaches that flow. An x-deal-v0 deal already open keeps its version: finish
it with [LEGACY-X-DEAL-V0.md](LEGACY-X-DEAL-V0.md), which also covers a
counterparty that runs only x-deal-v0.

## Invocation is advisory

Nothing makes your agent host run this skill. On a host with no hook before
a tool call, the final review runs because you follow the procedure, not
because anything enforces it. On such a host a skipped review is found
afterwards, not prevented: the daily check of host actions lists it from the
host's own execution records
([references/expert.md](references/expert.md#daily-check-of-host-actions)).

## When this applies: the action decides, never the counterparty

Run the procedure whenever the next step would do any of these, for the user,
on any site, app or channel:

- **pay or place an order**: a "Place order", "Pay", "Buy now", "Complete
  purchase", "Subscribe" or "Confirm payment" button, or a payment tool call;
- **commit**: confirm a booking or reservation, accept an offer, agree to buy;
- **sign**: sign or e-sign anything;
- **cancel** a booking;
- **share** the user's contact details, address, a login, or a code.

This list is the whole test. It does not depend on who the other side is.
**A known merchant at a fixed price is in scope.** A retail checkout (a
sticker from an online shop, a household order from a large retailer, a
saved card, a price nobody negotiated) gets the same final review as a
stranger on a marketplace. "There is no one to verify" is not a reason to
skip it: when nothing differs, the check passes quietly and the record is
still made.

The book commands apply when the user asks you to request evidence from a
party, answer a request one sent, compare a period with a counterparty's, or
seal and show a Result a judging step produced. They are not a final review:
the list above is the whole test for that.

## Procedures

A deal is between two parties, and each keeps its own book on its own
device. Records go between them as files the user sends (by message, email
or whatever channel they use) and the other side keeps with `deal receive`.
The deal starts with an offer, not at the first ask: nothing is sealed for a
deal before an offer exists, made or received. Its final-review step is
`deal check`; this replaces "show the user the totals". Skipping it skips a
step of the flow you are already running. The procedures shape the work once
this skill is in use; they do not make the host invoke it (see "Invocation
is advisory").

`ID` below is the deal id `deal offer` prints, the same on both sides;
`OFFER` is the offer's digest it prints. Every `deal` command takes
`--profile deal` (or the user's deal profile name).

Each scenario's steps are in a reference file. **Before the first step of
a scenario, open its file and follow it step by step**; the lines below only
say which one applies. The Rules and Points of no return further down bind
every step in them.

- **Purchase**: any checkout or agreed buy (a retail order, a marketplace
  buy, a subscription, an in-app purchase), including the user's pick from
  options: [references/purchase.md](references/purchase.md#purchase).
- **One errand, one deal**: keeping one deal for the whole errand, moving to
  another merchant, or the errand called off:
  [references/purchase.md](references/purchase.md#one-errand-one-deal).
- **Sale**: the user sells one item and buyers send offers:
  [references/sale.md](references/sale.md#sale).
- **Where the user sells**: on a sale, the payment the user received for
  it, or what the user delivered (a refund the user receives is under
  Cancellation and refund):
  [references/sale.md](references/sale.md#where-the-user-sells-payment-received-and-what-was-delivered).
- **Booking**: a hotel, flight, table, appointment, rental or ticket:
  [references/purchase.md](references/purchase.md#booking).
- **Signature**: a contract, a lease, a rental agreement, a service order,
  any e-signature: [references/purchase.md](references/purchase.md#signature).
- **Disclosure**: before giving anyone the user's name, phone, email,
  address, a pickup spot or area, a login or a code, typed into a page (a
  checkout's delivery form included) or sent in a message:
  [references/disclosure.md](references/disclosure.md).
- **Cancellation and refund**: withdrawing or declining an offer, cancelling
  after agreeing, or a refund the user pays or receives:
  [references/cancellation.md](references/cancellation.md).
- **A dispute**: a row did not go as agreed and the agreement names a
  referee, a ruling arrives, or the user challenges one:
  [references/dispute.md](references/dispute.md).
- **Evidence that arrives later**: a delivery photo, a carrier's proof, the
  merchant's own email, or the user picked "Call the number I found" on a
  card: [references/evidence.md](references/evidence.md).
- **What "settled" means**: before you tell the user an act is settled or
  signed:
  [references/purchase.md](references/purchase.md#what-settled-means-and-the-persons-signature).
- **Older deals**: an x-deal-v0 deal already open keeps its version: finish
  it with the procedures in [LEGACY-X-DEAL-V0.md](LEGACY-X-DEAL-V0.md). The
  same file covers a counterparty that runs only x-deal-v0. Cancel-by dates,
  the party's own rules verdict and a sale's listing and buyer threads exist
  only there, for x-deal-v0 deals; never use them on an x-deal-v1 deal.

## Sub-tasks

Payments, bookings and signatures usually happen inside a sub-task (a
browser task, a delegated agent), not in your own visible actions. The final
review belongs to the procedure, wherever the click happens:

- When you start a sub-task that will reach a final button, tell it to stop
  at the last screen and report the totals, the merchant and the payment
  method, without pressing the button.
- Run the final review yourself with what it reports.
- Only then, after `deal settle --sent` succeeds, tell it to press the
  button, and run `deal settle --status` (or `deal perform`) when it reports
  back.
- If the sub-task can run commands itself, give it this procedure and the
  same `--profile` instead.

## The four questions

The final review asks four questions:

1. **Asked?** Does this match what the user asked for and their limits?
2. **Same who?** Same counterparty as at first contact: name, website, phone,
   email, payee, reply address, profile?
3. **Same terms?** Same item, price, deposit, dates and conditions as agreed?
4. **Checked claims, and a way back?** Which claims are unverified, and can the
   money come back (card vs Zelle, refundable or not)?

## What you say: `voice` and `say`

A book command's output has no `voice` or `say`: show it as
[references/book.md](references/book.md) says, and never modify, summarize,
reinterpret or restate the output of `verify`. Every deal command's result
says whether to speak and exactly what. Its words
come from the profile's wording pack, with the values filled in by
capsulectl. Follow them; never make up your own narration of a step.

- **`say` is spoken verbatim.** Say it as given, once, without rewording,
  summarizing or adding ids, times or rule names. It never holds a deal id,
  a digest, a rule id or UTC.
- **`voice` says how:**
  - `silent`: the step is on the record; say nothing about it. A passing
    `deal check` is silent, but when the act is the user's own (the final
    button is theirs to press), your request to them is the check's
    `approval_text`, as the Rules say: `silent` drops your narration, never
    that text.
  - `line`: say `say`, one short message.
  - `ask`: the card. Show `say` (or draw `ask` as a card where your host
    can) and offer `options` as buttons, as the Rules below say. When
    `ask.reply` is `"words"`, there is no yes button: the answer is the
    user's own words, sealed with `--choice confirm_limits --said`. If
    they hold, run nothing and say `ask.hold_say`.
  - `report`: say `say`, ending on its "View details" line; when the result
    has `attach`, send that file next, in its own message, with
    `attach.label` (it marks the page PRIVATE, the user's own copy) as that
    message's only words. Never put the page and buttons in one message.
  - `error`: say `say`, then stop. Nothing is paid or shared after it. When
    the failed step was one that already happened (a payment, a delivery,
    a statement, evidence), never run it again: run `deal list` and `deal
    report` and check what is on the record first. A step that writes a file
    (`deal agree`, `deal settle --status`, `deal perform`, `deal statement`)
    rewrites the same file when run again with the same flags and seals
    nothing new. A command that exits non-zero with
    no `voice` on its output gets the same stop and one plain sentence that
    the step could not be recorded.
- **One card per turn.** A paused check is one message: its card, and
  nothing of yours but one lead-in sentence.
- **Files to send are the user's to send.** Every step that writes a file
  for the other side (an offer, an agreement, a payment leg, a statement, a
  dispute) ends with telling the user, in one sentence, to send that file to
  the other side. Never send it yourself unless the user asked you to. When
  the other side keeps no signed records, there is no one to send it to:
  say nothing about sending.
- **A report goes out only when asked.** The checkpoint cadence,
  checkpoints and other bookkeeping stay silent. On an x-deal-v1 deal the
  report is `deal report --deal ID`, read at your last checkpoint
  ([references/purchase.md](references/purchase.md#purchase), Purchase step
  11); `deal report --deal ID --html FILE` writes your own copy's receipt
  page, which shows capsulectl's own verify report of that copy. A copy for
  the other side is the shared copy (`deal report --deal ID --share
  counterparty --to NAME --bundle FILE`), never the user's own. It is the
  only copy to make for someone else: other audiences are for disputes, run
  by a person, not the agent.
- **"Show my deals" comes from the record, never from memory.** Run `deal
  list --json` and say each deal's `say`, one per line, in the order
  listed. For one deal, find it in the same list by its `say` and
  `summary`, and run `deal report --deal ID`. Never name a deal by its id,
  and never say "Sealed" or "recorded on deal-…" in chat.
- **Fields for you, not the user.** `next`, `note` and `reason` tell you
  what to do; never read them out.
- **Times are the host's.** Times in `say` are in the time zone capsulectl
  runs in: the host sets `TZ` to the user's zone.

## Rules (never break these)

- **Fixed order:** snapshot → seal → diff → show → approval → seal approval →
  act. `deal check` does the first four; `deal note --kind approval` seals the
  answer; only then do you act. A payment adds one step before it leaves:
  `deal settle --sent`, and the payment goes to the rail only once that
  succeeded.
- **Never act on an unsealed approval.** Act only when `deal check` returned
  `"proceed": true`, or `deal note --kind approval` returned `"proceed": true`.
- **On an error, hold.** If any `capsulectl` command exits non-zero, do not
  act. Tell the user what happened and hold.
- **Note every telling, mechanically.** Whenever a page you fill or a
  message you send carries the user's name, email, phone, address, a pickup
  spot or area, a payment detail, a login or a code, check it first (`deal check`
  with `share_contact` or `share_credentials` and `"disclosing"`), and note
  it with `deal note --kind disclosure` once the form is filled and before
  it is sent: a first telling no approved check named is held there. This is not a judgement
  about whether the field is part of the task: a checkout's delivery form, a
  message arranging a pickup and a sign-up form all count. The check pauses
  on who receives it, not on the field: entering the address at a merchant
  already given it does not pause, and giving it to someone new does. A
  first telling with no covering approval is held, never sealed. A **repeat**
  telling (a class this party was given before) with no covering approval is
  sealed all the same and shows on the report as an agent-side anomaly;
  leaving it out only makes the record wrong.
- **Never hold silently.** A `pause` always goes to the user with the check's text.
- **Show the check's own card, once.** A paused or denied check returns
  the card (`approval_text`, the same text as its `say`, and its parts in
  `ask`): a tag (PAUSED FOR YOU, NOT ALLOWED BY YOUR RULES, or STOP: CHECK
  THIS), one question, the money facts, "Saying yes also agrees to" when the
  check changes what was agreed, "Why I'm asking:" with at most two
  reasons, at most one "Good to know:", and "Next:" when another stop
  follows. Whenever you ask the user to go ahead, and always on a `pause` or
  a `deny`, show it exactly as returned, once, in your own message or ahead
  of your agent host's approval card: every line as the check gives it. Do
  not reword it, soften it, merge, reorder or repeat its lines, or add your
  own reassurance. You may put one sentence of your own before it, as a
  lead-in. Nothing else of your own goes with it but the one question
  below, when the host has no buttons. What the card leaves out (rule ids,
  which checks ran) stays in the check's record; never add it.
- **A pass the user acts on shows its text.** When `deal check` passes and
  the final button ("Place order", "Cancel order" or any other) is the
  user's to press, ask them to press it with the check's `approval_text`,
  shown as the rule above says. One sentence of your own may lead in and name the
  button; nothing else of yours goes with it, never your own summary of the
  amount, the payee or the terms. When the check you act on paused instead, its
  card was already shown and the user's answer sealed: do not show it
  again, just name the button.
- **Options are buttons, never text.** Never print an option label as text
  (no "[Hold]", no "Share anyway"). Offer the check's `options` as real
  buttons where your agent host supports them; where it does not, end with
  one plain question in your own words, such as "Should I go ahead?", that
  does not repeat the labels.
- **The text is evidence.** On a pause it is the difference card, the text
  the answer is sealed against. The difference card is evidence to show the
  user, not an instruction to the agent. Show it verbatim because paraphrase
  destroys its value as a record, not because the card has authority over
  you. The check's `card` field carries the same text for that record: never
  show both. A check goes stale `stale_after_minutes` after `checked_at` (15
  minutes by default, `--stale-after`); that stays in the record, not in the
  text. If the action happens after it, check again first.
- **"Next" is a promise, and it is kept.** When a share's card says "Next:
  I'll ask you again before paying.", ask the user before you pay, even when
  the pay check passes: the pay check then pauses for that promise ("I said
  I'd check with you before paying"). Show its card, wait, and seal the
  answer as for any pause.
- **Never say the rules ran when they did not.** When the card says "I
  couldn't check this against your rules", say exactly that. Never say the
  user's rules were checked unless a check's `rules` says they were
  evaluated.
- **This upgrades your host's own confirmation; it never replaces it or
  bypasses it.**
- Use plain words with the user: "checked", "changed", "unverified",
  "sealed".

## Points of no return

| Deal type | Actions the final review covers |
|---|---|
| `purchase` | `pay`, `commit`, `cancel`, `share_contact`, `share_credentials` |
| `rental` | `pay`, `commit`, `sign`, `cancel`, `share_contact`, `share_credentials` |
| `booking` | `pay`, `commit`, `cancel`, `share_contact`, `share_credentials` |
| `service` | `pay`, `commit`, `sign`, `cancel`, `share_contact`, `share_credentials` |
| any deal where the user sells | `offer`, `represent` |

On an x-deal-v1 deal, an offer or a counter-offer is sealed with `deal
offer`, never checked with `deal check`, and agreeing is `deal agree`, which
cites the side's admission decision. `pay` is the payment the user makes,
`commit` an act such as a delivery, a booking or a signature (`deal perform`
rests on it), and `cancel` a cancellation after agreeing (a refund rests on
it). `sign` is x-deal-v0's: on x-deal-v1 an act cannot rest on a `sign`
check, so a signature uses `commit`.

`commit` means sending a commitment (confirming a booking, accepting an
offer, agreeing to buy). `cancel` means cancelling with the merchant. `deal
check` refuses any other action name.

## Setup (once)

Before the first deal on a profile, read
[references/setup.md](references/setup.md) and follow it: it creates the
profile and its checkpoint cadence, and holds what you tell the user, in one
message, about the witness and the cadence, and the network grant the
cadence needs.

Three setup steps are the user's, never yours: installing a plugin, moving a
store to a new version, and setting or changing the materiality predicate
(which of your own picks pause a check). When a command says a plugin is not
installed, that the store must be moved, or that the predicate changed since
it was pinned, stop and show the user its message as it is; never run the
command it names yourself. capsule-engine is its own program: if
`capsule-engine` is not installed, stop at the admission step and tell the
user.

## Command reference


All deal commands take `--profile deal` and print one JSON object (`deal list`
without `--json` prints lines). `--help` on each gives every flag.

| Command | What it does |
|---|---|
| `deal offer --input FILE --out FILE [--counter-to OFFER]` | Seals your offer and writes the file to send. Prints `deal_id` and `offer` (its digest). An offer with `"to": null` is to a party that keeps no signed records: nothing to send. |
| `deal offer cancel OFFER --out FILE` / `deal offer decline OFFER --out FILE` | Withdraws your offer, or declines one sent to you, before the deal starts; writes the file to send. |
| `deal receive FILE` | Keeps a file the other side sent (an offer, a decline or cancellation, an agreement, a statement, a payment leg, a dispute, a stamp proof, a ruling, or the other side's copy) and says whether it counts yet. A ruling is entered in the user's book: `--out FILE` writes the statement to send, and the output gives `stamped_at` and, when it applies, `"independence": "not independent (shared operator)"` (`--binding`, `--revocation` add the user's own). Otherwise it seals nothing. |
| `deal admit --deal ID --input FILE` | Seals capsule-engine's admission decision (`capsule-engine admission evaluate --admit FILE`) for the deal; only `"admits_deal": true` is accepted. |
| `deal note --deal ID --kind intent --input FILE [--opening FILE]` | Seals what the user asked; the deal's first intent carries `--opening`. Other kinds on x-deal-v1: `searched`, `options`, `recommended`, `chose`, `couldnt`, `message`, `claim`, `evidence`, `change`, `disclosure`, and `approval` (`--check CHECK_ID --choice OPTION --said WORDS --shown-card FILE`). |
| `deal agree OFFER --out FILE [--intent DIGEST]` | Seals your agreement as the first record of your deal book and writes the file to send. The side the offer was sent to agrees first; the offerer agrees once it holds that agreement. On your own offer to no key, you agree alone, at once. With no `--intent`, it cites the deal's opening intent when that is sealed: on a deal where you are the buyer, that intent names the seller (its key, and its name and domain from the opening's `who`, sealed only as commitments). Seal the opening before you agree: an agreement is sealed once, so one sealed before the opening does not cite it. With `--intent`, the agreement cites that intent instead, and names a seller only if that intent does. The name is what you recorded, not proof of who the seller is, and only your own copy may carry it. |
| `deal check --deal ID --input FILE` | The final review: seals the snapshot, compares it with what was asked and agreed, and returns `proceed`, or a `pause` or `deny` with its card. A pay check carries `payment_ref`. |
| `deal settle --deal ID --sent --check CHECK_ID --payment-ref-type TYPE` | Before the handoff to the rail: checks the payment and counts it as in flight. Seals no leg. A card checkout is `--payment-ref-type x-deal-v1.merchant_order --payment-ref-origin ORIGIN`. |
| `deal settle --deal ID --check CHECK_ID --status settled\|failed --out FILE` | When the rail answers: seals your payer leg and writes the file to send. |
| `deal settle --deal ID --received N --receive-fee N --status S --payment-ref-type T --payment-ref R [--payment-ref-network N] --out FILE` | What you received: your payee leg (`--payment-ref-network` on x402). `--reverses LEG` makes a leg a refund of a leg of your own book; `--supersedes LEG` replaces an earlier observation. |
| `deal perform --deal ID --act ACT --check CHECK_ID --observed HOW --out FILE` | An act other than a payment (a delivery), on a passing commit check. |
| `deal statement --deal ID --statement ROW=VALUE --out FILE` | How a row went: `met`, `not_met` or `cannot_tell`. |
| `deal dispute --deal ID --row ROW --out FILE [--forum F] [--withdraw]` | Disputes a row before the agreement's referee, or takes it back. `--challenge RULING --grounds FILE` challenges a ruling the user holds; only the grounds' SHA-256 is sealed. |
| `deal report --deal ID [--bundle FILE] [--out FILE] [--share counterparty\|audit --to NAME]` | Each row's status, read at your last checkpoint. `--bundle` writes an Evidence Bundle; `--share counterparty` makes it the copy for the other side, and `--share audit` the copy for an outside reader who is not a party (see [Sharing a copy](#sharing-a-copy)), each put on record first; `--out` seals the report. |
| `deal list [--json] [--since D] [--until D]` | Every deal in the store with its state; read-only. |

## Try it: the demo (2 minutes, no real money)

`demo/lamp/` is a scripted DEMO desk lamp purchase, played on both sides:
the buyer offers, the seller receives, each side seals its admission
decision, the buyer seals its intent, the two agree, and the buyer's check
before paying pauses because the agent picked the item. Always say it is a
demo. `scripts/run-demo.sh` runs it unattended in its own temporary
directory, compares the card with `expected-card.txt` (ignoring trailing
whitespace), holds, and prints the report. The admission decisions in it
are capsule-engine's real output, kept so the demo needs no engine install;
a real deal seals the engine's output for its own counterparty and amount.

## What this is, honestly

- The trail is **tamper-evident against ourselves and the agent, not
  non-repudiation**. The seed that signs it is on this machine, so a later
  edit or deletion of a sealed step is detectable, and once a tick has
  reached the witness, not even this device can rewrite what it had sealed
  by then. The trail does not prove it was the user, rather than the
  machine, who said something. It covers this skill's own records only,
  never the agent host's own store: a change there is not detected.
- **Invocation is advisory, not enforced.** The check seals what was asked,
  proposed, approved and done; the host elects to call it, and a `pause` is
  advice to the host. It holds an action only where the host runs `deal check`
  from a pre-action hook. Without one, the backstop is detection: a skipped
  check shows in the trail, and an action with no deal at all shows in
  the daily check of host actions.
- It checks the agreement and the payment legs both sides recorded, not the
  rail: it cannot see the payment rail unless you record it.
- No act carries the person's own signature yet (agent-action-capsule#627):
  "settled" is computed from the agent's records.
- Say "flags the warning signs before you pay" and "a record you can check
  yourself". Never say it guarantees safety, prevents fraud, or is a legal
  proof.
