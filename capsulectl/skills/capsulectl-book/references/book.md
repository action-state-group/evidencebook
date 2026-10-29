# Book verbs

The reference for the verbs in [../SKILL.md](../SKILL.md). Each runs as
`capsulectl book VERB ...` on a book profile. `capsulectl verify`,
`capsulectl disclose` and the other core verbs are in the `capsulectl` skill.

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
claim whose `period` is not the window its Close sealed, when this tool's
`book close` sealed it (its `capsulectl-close/v1` profile names the period); any
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
`join_key` and `state_of_record`; the `period` of a claim whose Close
another producer sealed; a close claim's `verdict` and `sufficiency` beyond
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

## Render a report

```sh
capsulectl book report build --bundle B.json --card CARD --out report.html [--presentation P.json] [--permalink] [--base-url URL] [--dry-run]
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
