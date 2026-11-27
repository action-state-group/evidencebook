# Book verbs

The book commands of the [deal skill](../SKILL.md), in full. The skill teaches
five verbs (check, record, ask, verify, show) and maps each to the book
commands below in its table, "The five verbs". `book close` and `book result
derived-inputs`, and the window commands, are expert commands: an operator's,
not taught to an agent, and kept here as they were taught before.

This was the `capsulectl-book` skill. The book commands work on a book profile
(a jsonl profile with `log_id`, `log: book` (the owner the core requires;
`--log book` when you create it), `operator`, a signing key the profile trusts
and a checkpoint signing key it trusts), never on the deal profile, and extend
the [capsulectl skill](https://github.com/action-state-group/capsule-cli/tree/main/skills/capsulectl): the core verbs (`verify`, `disclose`, `get`, `cll`,
`publish`, `plugin ls` and the rest) are that skill's. They never reason about
business meaning, and never invent a workflow beyond the verb surface below.

## Guardrails for the book commands

**May:**

- call only the book verbs listed in this file
- request evidence from a counterparty, and answer a request received from one
- close a period and reconcile it against a counterparty's held bundle
- acknowledge or rebut the counterparty's Close for a period, only when the person says so
- seal a Result a judging step produced, and render its report
- verify a bundle with `capsulectl verify` before treating it as authentic

**Must never:**

- create or modify a scope file, Evidence Contract, or licence
- write, edit or complete an Evidence Result v0 — seal only the document a judging step produced
- invent a relationship between a requirement and an evidence source
- modify, summarize, reinterpret, or restate the output of `verify`
- describe self-attested evidence as witnessed, countersigned, or independently verified
- transmit data off-machine without first showing exactly what will be transmitted; `book request` and `book respond` write the bytes to send and transmit nothing themselves

**Approval points:**

- `book acknowledge` and `book rebut` are the person's own statement about the counterparty's Close, not a result of reading it: run either only after the person tells you to, never on your own reading of a reconcile, and pass the rebuttal reason in the person's own words. Tell them once that the counterparty reads the reason.
- Stop and ask a human whenever a command fails because of a licence, plugin, or authorization requirement. When a command says the `capsulectl-book` plugin is not installed, show the user the exact command it prints; never run `plugin install` or `plugin remove` yourself.

**Evidence policy:**

Every consequential action taken by this skill must produce an evidence record. If evidence generation fails, report `evidence unavailable`, stop before the next consequential action, and do not infer that the underlying action failed to occur.

## Book verbs

Each verb is run as `capsulectl book VERB ...`, which the core hands to the `capsulectl-book` plugin.

| Verb | Args | When | Emits | How |
|---|---|---|---|---|
| `book request` | `--profile NAME --request FILE [--sent] --responder NAME --output FILE`<br>`--profile NAME --for RECORD_ID --response FILE --responder-key HEX --responder-checkpoint-key HEX [--witness-directory FILE]`<br>`--profile NAME --for RECORD_ID --absent-until TIME` | Ask a party for evidence (the request is recorded before it is sent; with `--sent`, after another tool sent it), then record exactly one outcome for it: the answer that came back, or that nothing came back in the window. The output file is what to transmit; this verb transmits nothing itself. The request is the evidence-interop/0 request map, or with `--sent` a request another tool already sent (such as the capsules plugin's), recorded member for member, in JCS, so its answer verifies (no nonce or request profile is added); the answer is a signed envelope or refusal. A recorded artifact prints `witness`: `witnessed` only when a receipt from a witness in `--witness-directory` covers its anchor checkpoint; `unwitnessed` means it checks only against the holder's own signatures, so never report it as witnessed or fully checked. Run it on a jsonl book profile only: on a remote profile it is the deprecated alias of `book remote request`, which asks another party's book over the profile's transport, so there stop and ask a human. | (each invocation's own book record is the evidence; no wrapper capsule) | primary action (running it *is* the record) |
| `book respond` | `--profile NAME --request FILE --requester ID --output FILE`<br>`--profile NAME --request FILE --requester ID --policy FILE --output FILE` | Answer a received evidence request with a signed artifact or a signed refusal, under the share policy, and record that it was answered. --requester is the identity you authenticated the sender as; without it only public subjects are answered. A malformed request is refused, and an unauthenticated one answered or refused, without a book record. The output file is what to send back; this verb transmits nothing itself. When the share policy sends `policy_declined` in place of the reason it decided on, stderr carries that reason for you alone: never send stderr back with the reply or pass it on. | (the book's record of the answered request is the evidence; no wrapper capsule; an empty `record_id` in the result means the answer is not on record, so show it to the user before it is sent) | primary action (running it *is* the record) |
| `book close` | `--profile NAME --period day\|week\|month --counterparty ID`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty ID`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty ID --since-last`<br>`--profile NAME --period day\|week\|month --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX`<br>`--profile NAME --period day\|week\|month --counterparty ID --capsule-out FILE --bundle-out FILE` | Seal the book's Close for a period that has ended (by default the most recent one; --date names any date inside the period), for one counterparty. The Close seals agent-action-capsule's close/v1 body: the period, and the counts by record type and head of the window it binds, which under --since-last can start before the period. It seals tallies only when the --peer bundle discloses the counterparty's Close for this period, which it then cites; otherwise it states counts only, and the reconciliation it prints (every exchange INSUFFICIENT without a peer bundle) is not sealed, so no reconcile claim can cite it. Repeating it for the same period and counterparty returns the first Close and seals no second one; the peer bundle and --since-last of a repeat are not applied. | (the Close record itself; --capsule-out writes it for `capsulectl verify`) | primary action (running it *is* the record) |
| `book acknowledge` | `--profile NAME --period day\|week\|month --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX` | Only when the person says so: record that this book has seen the counterparty's Close for a period and agrees, which makes that Close AGREED when the counterparty reads it. The Close is the one the --peer bundle discloses for the period, naming this book, under the pinned key (the one `book close` would cite); the bundle need not disclose anything else. It checks nothing about the exchanges: run `book reconcile` first and show the person what it prints. Refused after this book rebutted the same Close. A repeat returns the first record. | (the acknowledgement record itself) | primary action (running it *is* the record) |
| `book rebut` | `--profile NAME --period day\|week\|month --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX --reason TEXT`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX --reason TEXT` | Only when the person says so: record that this book disputes the counterparty's Close for a period, with the person's reason, which makes that Close CONTESTED when the counterparty reads it (a rebuttal outranks an acknowledgement, so it can follow one). The reason is sealed and the counterparty reads it. A repeat with the same reason returns the first record; another reason is refused. | (the rebuttal record itself) | primary action (running it *is* the record) |
| `book reconcile` | `--profile NAME --period day\|week\|month --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX` | Compare this book's period (by default the most recent one that has ended; --date names any date inside it) with a counterparty's held bundle, showing each exchange in one of six states, as of this book's latest checkpoint: an own exchange not yet checkpointed reads INSUFFICIENT until `book close` checkpoints it. It never queries the counterparty and adds no record. | (no capsule — comparing held records is local comparison, not a consequential action) | not consequential — no capsule |
| `book result build` | `--profile NAME --result FILE --out FILE`<br>`--profile NAME --result FILE --contract REF --out FILE --capsule-out FILE` | Seal a caller-supplied, schema-valid Evidence Result v0 into the book as the record a report is rooted on, citing every record its claims name. Refuses a document whose headline values do not recompute from its claims, or that cites a record the book does not hold, or a close_state or tallies that differ from what the cited Close's links and statement read. A repeat of the same document seals nothing new. Only UNILATERAL close claims are reachable today (see the reference). | (the Result's evidence_result record; --capsule-out writes it for `capsulectl verify`) | primary action (running it *is* the record) |
| `book report build` | `--bundle FILE --card attribution\|human_role\|obligation\|outcome\|process\|quality\|settlement --out FILE`<br>`--bundle FILE --card CARD --presentation FILE --out FILE --permalink`<br>`--bundle FILE --card CARD --out FILE --dry-run`<br>`--bundle FILE --card CARD --out FILE --diagnostic`<br>`--bundle FILE --card CARD --out FILE --receipts FILE` | Render a held, disclosed Evidence Bundle whose root is a sealed Result v0 into an offline report.html (and, with --permalink, a viewer permalink) after verifying it; a root that is not a Result gets none. A bundle that fails verification or is incomplete gets no report page; --diagnostic writes a marked diagnostic page instead and still exits with the failure. --dry-run writes the page marked draft and mints no permalink. A permalink carries the whole disclosed bundle in its URL fragment, so sharing the link transmits the bundle. `--receipts FILE` (a JSON list of `{"exchange_capsule_id", "receipt": <bundle>}` or `{"exchange_capsule_id", "held": true}` or `{"exchange_capsule_id", "not_closed": true}`) carries each deal's receipt on a settlement report whose claims are `<deal>::<requirement>`: an entry goes to the one deal whose claims cite its exchange capsule, `held` marks a receipt the holder's share policy keeps off the page, `not_closed` marks a deal that had not closed when the page was built (no receipt yet), one of `receipt`, `held` and `not_closed` per entry, a deal with no entry reads "Receipt not carried with this page", an id no deal cites is ignored and listed as `ignored_receipts` in the output, and one two deals cite is refused. The pairing of a bundle to its deal is the caller's word. Nothing is carried without the flag; it cannot be used with `--permalink` or `--diagnostic`. | (no capsule — rendering a held bundle is local; the disclose act that produced the bundle is already on record) | not consequential — no capsule |
| `book result derived-inputs` | `--profile NAME --response RECORD_ID` | Print what a holder answered to a Terms Pack requested by the digest of its definition, as this book recorded it (`book request --response` printed the record id): the pack digest, the anchor, `proven` and each row's digest, leaf and declared fields. `proven` means each row's digest was checked in the holder's log at a signed checkpoint; the field values are the holder's word, and no proof shows that no row was left out. The evaluator that runs the pack reads this. | (no capsule — reading a recorded answer is local) | not consequential — no capsule |
| `book report derive` | `--profile NAME --response RECORD_ID --pack DIGEST --result FILE --out FILE [--allow-unproven]` | Seal the Evidence Result an evaluator produced over a holder's answer, signed by this book's key, and write the one Evidence Bundle (with `contract-report/v1`) that `capsulectl verify --bundle` checks and `book report build` renders. The holder signed its answer, not the Result. Every claim must cite exactly the response record; `--pack` must be the digest the holder answered; an answer without proofs is refused unless `--allow-unproven`, and then the Result's view title must begin with the sentence "Inputs not proven: the holder's word only. " (the final space included). `--result -` reads stdin. Runs no pack. | (the Result's evidence_result record, and the disclosure record of the bundle) | primary action (running it *is* the record) |

A book store split by time window (`book window init`, or `book window convert` for a JSONL book, then `book window list`, `roll`, `chain` and `publish`), and a report over a range of a holder's windows (`book report derive --chain`, `book report coverage`, `book report build --holder-checkpoint-key`), are below, in [Split a store by time window](#split-a-store-by-time-window-and-report-over-a-range-of-windows).

## Capsule emission

**Records are for consequential actions only** (see Evidence policy under Guardrails, above). The `How` column says which of the two cases applies:

- **primary action** — the verb's entire job already is persisting a signed book record (`book request`, `book respond`, `book close`, `book acknowledge`, `book rebut`, `book result build`, `book report derive`). Running it satisfies the invariant by itself; nothing wraps it, and the record id it returns is what to cite. The exceptions are `book respond` to a malformed request or one with no authenticated requester: its signed answer or refusal may leave no record (`record_id` empty), which is reported, never hidden.
- **not consequential** — `book reconcile`, `book result derived-inputs` and `book report build` only read, compare or render held records. They run and add no record: **no wrapper seal, no synthetic evidence record.**

The sections below give the request, Close, acknowledgement and Result details and what `book result build` refuses.

## Exercise the book verbs

[scripts/run-book-demo.sh](../scripts/run-book-demo.sh) runs every book verb above against two throwaway jsonl book profiles in a temp directory, with `capsulectl` and `capsulectl-book` built from a source checkout: the consequential verbs (`book request`, `book respond`, `book close`, `book acknowledge`, `book rebut`, `book result build`, `book report derive`) each return the signed book record that is their evidence, and the script counts them; `book reconcile`, `book result derived-inputs` and `book report build` run and add no record — the script asserts both halves. It never touches a real profile or a real log. It is a contributor check, run after any change to the verb surface: it needs Go, `jq` and this repository. Using the skill needs neither: install the release binaries of `capsulectl` and `capsulectl-book`.

## Each verb in detail

Each runs as `capsulectl book VERB ...` on a book profile. `capsulectl verify`,
`capsulectl disclose` and the other core verbs are in the `capsulectl` skill.

## Answer the counterparty's Close

```sh
capsulectl book acknowledge --profile NAME --period day|week|month [--date YYYY-MM-DD] --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX
capsulectl book rebut --profile NAME --period day|week|month [--date YYYY-MM-DD] --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX --reason TEXT
```

Both are the person's statement about the counterparty's Close, so run them
only when the person says so, and pass a rebuttal's reason in their words.
The Close answered is the one `book close` would cite: the counterparty's
Close for the period that the --peer bundle discloses, verified under the
pinned key, naming this book as its counterparty, and superseded by no other
it discloses (two such Closes are refused). On a store split by time window
the Close names the window book holding the period's start (for example
`NAME/2026-09`), or the predecessor JSONL book for a period before the
conversion. Unlike `book close`, the bundle need not disclose the Close's
head: an acknowledgement checks that the Close exists, not its tallies. Run
`book reconcile` first when the person wants to see how the exchanges compare.

`acknowledge` appends an `acknowledgement` record with an `acknowledges`
link to that Close; `rebut` appends a `rebuttal` record with a `rebuts` link
and the statement `{"reason": TEXT}`, which the counterparty reads. Either
names the Close by its record id and holds no copy of the bundle (no reconcile
relies on it). The record goes in the current book (on a windowed store, the
current window, which takes one at any time), and a checkpoint covers it, so
a bundle can carry it to the counterparty: `capsulectl disclose --root
RECORD_ID` builds one while the record is in the current book. On a windowed
store `disclose` reads only the current window, so disclose an answer before
its window ends; a repeat that finds it in an earlier window (`window` in the
output) cannot be disclosed from there yet (evidencebook#1014). The counterparty reads the Close as AGREED or
CONTESTED (a rebuttal outranks an acknowledgement) from records of the book
the output names as `book`, signed under this book's checkpoint key.

Refused: `acknowledge` after this book rebutted the same Close (the Close
stays CONTESTED; acknowledge a new Close that supersedes it instead), and a
second `rebut` of the same Close with another reason (the first is sealed).
A repeat returns the first record, from any window, with `already_recorded`
true; a repeat whose record no checkpoint covered yet checkpoints it.

Output: `record_id`, `seq`, `record_type`, `period`, `counterparty`,
`peer_close` (the Close answered), `book` (the book holding the record),
`window` (on a windowed store) and `already_recorded`.

`book close --peer` against a counterparty's Close holds the --peer bundle
in full: before the Close it appends a `held_bundle` record whose payload is
the bundle's bytes as received, and the Close cites it. Its output names it
as `held_bundle`, on a repeat too. The Close's `--bundle-out` bundle withholds
that payload by digest. A Close is kept for good, so the bundle it relies on
is too.

## Seal a Result into the book

```sh
capsulectl book result build --profile NAME --result RESULT.json --out SEALED.json [--contract REF] [--plan PLAN.json] [--capsule-out RECORD.json]
```

Requires a jsonl profile with `log_id`, `log: book` (the owner the core requires; `--log book` when you create it), `operator`, a signing key the
profile trusts and a checkpoint signing key it trusts (the same footing as
`book close`). RESULT is an Evidence Result v0 document (agent-action-capsule
`schemas/evidence-result-v0.json`, vendored in the binary with its digest
pinned); the skill never writes one -- it seals what a judging step
produced. The verb refuses, in one sentence each: a document outside the
schema; buckets that do not partition the claims under their own verdicts,
or coverage counts that do not add up; a claim naming a contract other than
`--contract`; a cited digest (`claims[].evidence`, a disclosure carrier's
evidence, `close.peer_close_ref`) the book holds neither as a published
capsule nor as a record; a claim citing nothing; a close claim whose
`close_ref` (or `peer_close_ref`, when present) is not among its own
`evidence[]`, or does not name a Close; a close claim whose `close_state` is
not what the COUNTERPARTY's links to the cited Close read (`rebuts` ->
CONTESTED, `acknowledges` -> AGREED, neither -> UNILATERAL), where a link
counts only from a record of another book than the Close's, that book the
claim's named `peer`, signed under another key than the Close -- every
record in this book is this book's own, so a link held here never counts --
or whose `peer_close_ref` is not the record whose link decided it; a Close,
or a record linking to it, whose Producer Envelope does not verify under its
`key_id` (the signer is verified, never read as stated); a reconcile
claim whose tallies are not what the cited Close sealed, or whose
`sufficiency` is not the one those tallies derive (`INSUFFICIENT > 0` is GAP,
else `UNRESOLVED > 0` is UNKNOWN, else SATISFIED); a close or reconcile
claim citing a Close sealed before close/v1 (Result v0 §4.1: `close_ref`
cites a close/v1 Close; `book close` returns that period's older Close and
cannot supersede it yet, so such a period backs no claim); a
close or reconcile claim whose `period` is not the `period` its Close sealed;
a reconcile claim citing a Close
that sealed counts only (it cited no peer Close, so it holds no tallies); any
headline count or tally written as other than an integer literal (`7.0`,
`7e0`). On success one
`evidence_result` record is appended -- its statement is the document, its
`cites` links name every resolved record -- and a checkpoint covers it.
Output: `record_id`, `seq`, `contract_refs`, `claims`, `cites`,
`statement_digest` (SHA-256 of `--out`, the document's canonical form),
`already_built`. A repeat of the same document returns the existing record
and, with the same `--out`/`--capsule-out`, rewrites nothing (the files
already hold those bytes); a file holding anything else at either path is
refused by name, nothing overwritten. A repeat whose record is not yet under
a checkpoint (an earlier run sealed it, then failed) checkpoints it.

With `--plan`, the Result is bound to a dataset plan (agent-action-capsule
`spec/evidence-plan-ir-v0.md` §5.1: a `traditional.population` node, then
`traditional.window` and `traditional.filter` nodes) that the verb runs over
the book's committed record headers. It sets the Result's `plan_ref` and
`dataset_ref` (Result v0 §7.3) to the digests of the plan and of the dataset it
selected. When the document already states them, any that differs is refused.
A plan over another book, one whose `through_seq` is past the book's last
record, and one the Plan IR gate refuses (its reason in parentheses, for example
`(field-missing)`) are refused too. The plan reads only records already
committed, so the same plan reproduces both digests however the book grows. The
plan and the dataset are sealed as an `evidence_plan` and an `evidence_dataset`
record, which the Result cites. The dataset cites the plan; its rows name their
records by seq and header digest, and the dataset does not cite them. Output adds `plan_ref`, `dataset_ref` and `rows`. A Result that
states `plan_ref` or `dataset_ref` without `--plan` is refused: the verb never
seals them as stated.

Sealed as the producer states them, not checked: a reconcile claim's
`join_key` and `state_of_record`; a close claim's `verdict` and `sufficiency` beyond
the schema's rule that a CONTESTED Close is never met.

**Only UNILATERAL close claims can be sealed end to end today.** A link
makes a Close AGREED or CONTESTED only from the named peer's book under
another verified key, and a book holds only its own records, so the book's
own `Acknowledge` / `Rebut` of its Close is ignored -- where evidencebook's
own status reading would count it. Sealing AGREED or CONTESTED needs the
peer's acknowledging or rebutting record in the book; no verb imports one
yet (a follow-up).
`disclose --profile NAME --root RECORD_ID --payloads selected --out B.json`
then builds the bundle a report is rendered from; it refuses `--suppress
agent_input` on that root, because the header is the Result.

The book stores digests, never text: a published capsule's record commits
the capsule and its producer envelope only. When a report should show what
the cited capsules were given, add `--attach-input-originals` to `disclose`
(jsonl profiles): each published capsule in the bundle whose agent_input
original the artifact store retains rides in the bundle extension
`capsulectl/agent-input-originals/v1` (capsule id to base64url bytes), after
its JSON-DIGEST is checked against the capsule's `agent_input_digest`; the
disclosure record commits to that bundle. It cannot be combined with
`--suppress agent_input`. A record an earlier build wrote with the original
as a committed payload has those bytes withheld by `--suppress agent_input`
(which then needs `--payloads selected`).

## Serve a Terms Pack by digest, and report over the answer

A Terms Pack is named in an evidence request's `derivation` by the SHA-256 of
the JCS of its compiled definition. Nothing is served by default.

**Holder.** List the pack in the profile, one file per pack in
`<profile database>/book/derivations/*.json`:

```json
{"document": {"...": "the compiled definition, as compiled"},
 "recipients": ["requester-id"], "forms": ["full_history"],
 "fields": ["record_type", "committed_at", "statement.generated_at"],
 "proofs": true}
```

`document`, `forms` and `fields` are required; `recipients` are the requesters
the pack is granted to. With no `recipients` the pack is answered like the
underived subject, so over `full_history` to no one at all. `proofs` is on
unless `false`; a member that is `null` refuses. `fields` are what each answer reveals of a
record: header members or paths into one, and nothing else. Members are read
exactly: an unknown, repeated or differently-cased member refuses. `book
respond` and `book doctor` read the directory; one bad file stops every
answer, public questions included, and a message names the file. `doctor`
lists each digest, its grants, and a pack whose answers carry no proofs.

**Requester.** `book request --response` records the answer. Then:

```sh
capsulectl book result derived-inputs --profile NAME --response RECORD_ID
capsulectl book report derive --profile NAME --response RECORD_ID --pack DIGEST --result RESULT.json --out bundle.json [--allow-unproven]   # --result - reads stdin
capsulectl book report build --bundle bundle.json --card CARD --out report.html
capsulectl verify --bundle bundle.json
```

`derived-inputs` prints `{response, derivation, anchor, proven, records:
[{digest, leaf_index, fields}]}` for your evaluator. The Result it produces is
yours, signed by this book's key; the holder signed its answer. Every claim
must cite exactly `response` as its evidence. `proven` means each row's digest
was checked in the holder's log at a signed checkpoint; the field values are the
holder's word, and no proof shows that no row was left out. `derive` refuses a
wrong `--pack`, an invalid Result, and an existing `--out` (before sealing
anything); a Result sealed before a later failure is reported as
`result_record_id`. A Result citing a private conduct pack is sealed for the
owner and never bundled.

## Split a store by time window, and report over a range of windows

A report's proofs are bounded by the book it draws from. A new book store is
split by window (evidencebook#689): one book per calendar month in UTC that
closes 7 days after it ends, both fixed when the store is created, which
`store init` prints. To fix another window or grace, or one JSONL book
(`--period none`), create the store with `book window init` instead:

```sh
capsulectl book window init --profile NAME --period year --grace 14d   # instead of store init; prints the window
capsulectl book window roll --profile NAME                  # close ended windows
capsulectl book window chain --profile NAME --from 2026-01 --to 2026-12 --out chain.json
capsulectl book window publish --profile NAME               # boundary and final checkpoints to the witness
```

A window's grace (default 7 days for month, quarter and year, 1 day for week,
none for day; shorter than the period) is how long after it ends it still
takes a Close over a period inside it (evidencebook#807). When a window ends
it takes a boundary checkpoint, then takes only such a Close until the grace
ends and it closes; `book window list` shows each window's `closes_at` and
`in_grace`, and `close` over last month seals the Close in last month's book
while it is in grace; its `--bundle-out` is anchored at that window's
checkpoint, and its disclosure record goes in the current window, since a
window in grace takes only Closes. A chain over a window in grace is refused
until it closes.

A profile whose store is one JSONL book converts with `book window convert
--period P [--grace G] --yes` (evidencebook#808). The conversion is one-way:
the book is closed for good by a `book_close` record and its final checkpoint,
stays at `book/log.jsonl` with its records unmoved, and the store's first
window starts at the first whole second after the close (a partial window,
`partial` in `book window list`). The same signing key continues, and it is not
a clear. No verb serves the old book's records afterwards: a chain's
predecessor entry and bundles made before converting carry them, and requests
open in it cannot be answered. A Close over a period that begins before the
conversion is refused, so the period in progress at the conversion cannot be
closed afterwards in either book: close the last whole period before
converting, or convert at a period boundary. A chain from the first window carries the book as its
predecessor. Where the profile names a witness, the book's final checkpoint is
queued in `book/witness.json` and `book window publish` delivers it; with no
witness the conversion says the predecessor is unwitnessed.

The requester asks each window with `expected_pin` set to that window's final
checkpoint digest (SHA-256 of `final_checkpoint` in the chain), records each
answer with `book request --response`, then:

```sh
capsulectl book report derive --profile NAME --chain chain.json --response R1 --response R2 ... --pack DIGEST --result RESULT.json --out bundle.json
capsulectl book report coverage --bundle bundle.json --holder-checkpoint-key HEX
capsulectl book report build --bundle bundle.json --card CARD --holder-checkpoint-key HEX --out report.html
```

Every gap is named (`window 2026-03 has no answer`, `chain breaks at 2026-04`),
and no bundle or page is written over one. On a deal store's report
(capsulectl-deal `deal init --window`), each deal placed in a window needs its
own answer too (`deal/… of window 2026-03 has no answer`), and `coverage` prints
each covered deal's line under `deals`: its answer covers its whole book,
records after its window included. The holder's key must be obtained
independently of the bundle. Without witness receipts a chain shows continuity
only as the holder signed it. Each answer proves its rows are in the holder's
log; the rows' field values are the holder's word; no proof shows that no row
was left out. On a windowed store, `close` and `reconcile` take a period inside
the current window or inside an earlier window in its grace, and a requester
derives over answers recorded in its current window.

## Render a report

```sh
capsulectl book report build --bundle B.json --card CARD --out report.html [--presentation P.json] [--permalink] [--base-url URL] [--dry-run] [--diagnostic] [--receipts FILE]
```

Read-only over a held file; no profile, no store, no record, unless `--seal`
(below). The bundle is
verified first (interval and membership must pass, no disclosed member may
mismatch; a graph closure the bundle declares incomplete is accepted as
withheld), then
its root must be a sealed Result v0 -- a disclosed member that is the
document, or an `evidence_result` record header whose `statement` is the
document (a book bundle; its header is then also checked by the evidence-
book verifier) -- and the document must pass the same schema and
cross-checks `book result build` applies. CARD is one of the Evidence Contract's
profiles (`attribution`, `human_role`, `obligation`, `outcome`, `process`,
`quality`, `settlement`) and is recorded in the page's `report-card/v1`
block; P.json carries at most `producer_display_name`, `logo_data_url`
(inline `data:image/` only) and `title`, recorded as `presentation/v1`. A
bundle already carrying a different block of either kind is refused.
Output: `root`, `form` (`payload` or `book`), `card`, `claims`,
`unsupported_claims` (claims citing evidence the bundle does not hold; the
viewer renders them unsupported, never met), `report`, `bundle_digest`,
`verification` (`pass`, or `graph_closure withheld` when the bundle declares
records of its closure missing -- the claims citing them count as
unsupported), and `permalink` when asked. `--dry-run` writes the
page with `draft: true` in its card block and mints no permalink. The page
is self-contained: the bundle and the browser runtime are inline and nothing
is loaded from the network.

`status_line`, `records` and `records_verified`, and `claims_not_evaluable`
give the build in counts with denominators ("5 of 5 records verified · 0 of 3
claims not evaluable"), never a percentage; the answer itself is in the page.
A bundle that fails verification or is incomplete gets no page. With
`--diagnostic`, it gets a page marked as a diagnostic page
(its title, a banner and `diagnostic: true` in the card block, under the
viewer's own failed-bundle callout; `page` is `diagnostic` and
`diagnostic_reasons` says what failed), and the command still exits with the
bundle's own failure. A diagnostic page gets no permalink and is never sealed
(`--seal` and `--permalink` are refused with it), and a bundle that verifies
is refused under `--diagnostic`. Every page passes the build's page checks before it is written, and a failed
check writes nothing: no foreign script (the page's Content-Security-Policy
admits only its own hashed scripts), a text alternative for each chart, the
core runtime at its pinned digest, and the page frame (language, phone-width
viewport, title, print stylesheet). A chart is checked against its dataset
where it is filled. Accessibility, phone-width and print layout are the viewer's
own renderer tests, which the page records by its runtime digest.

### Seal a page, check a page

```sh
capsulectl book report build ... --seal --profile NAME
capsulectl book report check --profile NAME --page report.html
```

`--seal` appends one `report_page_seal` record to the profile's book after the
page is written (never for `--dry-run`) and checkpoints it. The record holds the
page's SHA-256, the embedded bundle's digest and the Result root. The output's
`page_seal.revision` numbers the pages sealed over one Result, from 1, in log
order: a page that changes only its presentation (`--presentation`, `--card`,
`--wording`) embeds the same Result and seals as the next revision, and a repeat
of a sealed page keeps its revision. `report check`
compares a page file with the book's seals for the bundle it embeds: `match`
(exit 0), `mismatch` (5), `no sealed record` or `not covered by a checkpoint`
(3). `verification` is the embedded evidence's `verify --bundle` verdict, kept
apart; when it is not VALID its exit code (1, 3) comes first. A file that embeds
no bundle is not a page (2). The page's bytes are checked with this command, never in the page.
