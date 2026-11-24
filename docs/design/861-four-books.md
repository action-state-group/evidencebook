# evidencebook#861: the adjudicator gathers four books, and where it runs

Design note for [evidencebook#861](https://github.com/action-state-group/evidencebook/issues/861)
and [ledger-cloud#40](https://github.com/action-state-group/ledger-cloud/issues/40) (the
adjudicator's hosting and API). Written 2026-11-24 CST at evidencebook main `88691585` and
ledger-cloud main `9e52270`. File references are to those commits; `PROFILE.md` is
`dealprofile/x-deal-v1/PROFILE.md`; bare `case_check.go`, `types.go` and `report.go` are in
`dealprofile/xdealv1/`, and other bare `.go` paths are in the repository root.

## 0. The bar and who decides

- **The bar** (Steven, Ethan and Steven call of 2026-11-24, 00:09:27-00:14:23): the adjudicator
  "gets four books": each side sends its own book and the book it received from the other, so a
  silent counterparty is still covered by the other side's copy. A request from the adjudicator
  defaults to full disclosure ("I think so. That's right.", 00:14:23). Ops records it as a
  DECISION: `action-state-ops` `evidence-network/inbox.md:596` (u207,
  buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=0c3bb8740ccff751e35f940c3f3ae6805708176b18c49d17f9f6e0661158edf8).
- **Steven rules the what**: §6 lists the questions. **Ethan decides the how and the order**: §5.
- Out of this note: deploys and spend (Steven's), and evidencebook#833's files.

The four books of one deal, named the way this note uses them:

| Name | Whose records | Who holds and sends it |
|---|---|---|
| B-own | the buyer's deal book | the buyer |
| B-held | the buyer's deal book, as far as the seller received it | the seller |
| S-own | the seller's deal book | the seller |
| S-held | the seller's deal book, as far as the buyer received it | the buyer |

B-own and B-held are one log signed by one key, so B-held is a prefix of B-own unless the buyer
forked its book. The same holds for S-own and S-held. Four sources, two logs.

**Scope: deals where both sides keep a book.** A deal with a counterparty that runs no layer has
one book, and `CheckCase` refuses to open a case on it today (`case_check.go:96-103` and
`:176-181`: with one copy the other side's key must be the agreement's `counterparty_key`; with
two, each agreement must name the other as its layered counterparty). This note keeps that rule;
adjudicating a no-layer deal is a separate question, not part of #861.

## 1. What refers to a received book today (acceptance 2)

There is no separate "book reference capsule". Three things on main refer to the book a party
received, and none of them says where to request it.

1. **`seen`, in every deal-book record.** PROFILE.md:68 and §6 (PROFILE.md:312-319): "the record
   id of the last record its author held from the other side, or `null`". Go type:
   `dealprofile/xdealv1/types.go:48` (`Seen Null[string]`). Producer:
   `capsulectl/internal/deal/deal_v1_book.go:239-258` sets it from
   `Shelf.CountedPrefix(counterpartyKey, counterpartyLogID).Last` when the record is sealed.
   Payload: one record id (the Capsule id of the counterparty's record). No log id, no size, no
   address. It is signed by the party, inside its own book.
2. **The book fact's link value, at each stamp.** `shelf_facts.go:57-68`:
   `{"last_received": {"log_id", "size", "record_digest"} | null, "checked_over": [...]}`.
   Producer: `dealprofile/xdealv1/status_provider.go:116-139` (the counterparty's log id, the
   counted prefix's length and the last record's record id; the member is named `record_digest`
   but holds the record id). It sits in the fact as a salted commitment
   (`docs/design/461-shelf-facts.md` §2.2), and `OpeningsFor` (`shelf_facts.go:339-365`) gives
   the link opening to the counterparty, the adjudicator and an asker. Payload: log id, size, last
   record id. No address.
3. **The report's `rests_on.counterparty`.** `dealprofile/xdealv1/report.go:65-73`:
   `{"log_id", "counted", "last", "fork_at"}`. `report.go:324-332` leaves it out of an
   adjudicator's copy (PROFILE.md:487-489), along with the rows.

The agreement names the adjudicator as `{"key_id", "rules_digest", "rules_url"}`
(`x-deal-v1.schema.json:738-759`) and carries no party address; ledger-cloud#35 already notes
"the agreement carries no address".

**What is missing, and what this note adds.** The reference needs no address, because the
adjudicator asks for the received copy from **the party that holds it**, the same party it asks
for its own book (§2). What is missing is the copy itself: nothing today lets a party hand over
the records it received, and the adjudicator copy omits even the log id and size. This note adds
the held copy to the adjudicator's copy (§2.2) and no member to any deal record. The party's own
signed `seen` and its link openings are the reference the adjudicator checks the copy against.

## 2. Gathering

### 2.1 One request per party, not two

The adjudicator sends each party one evidence request. The answer carries that party's own deal
book and the copy of the other side's book it holds. Steven's description ("sees the link to the
other bundle and requests that too") becomes a check, not a second round trip:

- The copy is held by the same party that answers, so a second request would go to the same
  place.
- One answer is cut at one checkpoint, so the own book and the copy are consistent: each own
  record's `seen` must be a record of the copy, and the copy must reach at least the last own
  record's `seen`.
- A second request opens a window in which the party's checkpoint moves between the two answers.

**The request.** An evidence request (AAC `evidencerequest.Request`, agent-action-capsule go
v0.24.0, `evidencerequest/request.go:185-202`) with subject `{"correlation": "<deal_id>"}`
(`SubjectCorrelation`, AAC `evidencerequest/request.go:52`) and a derivation token the x-deal-v1 profile defines for
"the deal's full adjudicator copy" (its exact token is fixed in PR 1 against the request draft's
token rule). The request has no selective form: full disclosure is what the request asks, not a
flag the party sets. The responder answers in full or refuses (`policy_declined`), and a refusal
is recorded as a refusal. Nothing in this request lets a party answer with part of its book.

### 2.2 What a full-disclosure answer holds

Today's adjudicator copy (`deal report --share adjudicator --bundle`) carries the agreement and
the records the rows rely on (`ReportBook`, `dealprofile/xdealv1/report_bundle.go:28-38`), with
rows withheld and no counterparty prefix. The full answer holds:

1. **The whole own deal book**, every record from seq 1 to the checkpoint, as book pairs. This is
   the deal book only, not the journal: journal records stay out as today, except those the
   relied-on records already cite. Two standing rulings hold unchanged: the private floor
   (`intent.min_total_minor` and its openings, a journal member) never goes to an adjudicator
   copy, which keeps the max only (u547, ops `company-oss/inbox.md:795`); and the claims a
   seller's agent made without authority go on an adjudicator copy only by the seller's explicit
   opt-in, default off, reading "not shared" when absent (u615, `company-oss/inbox.md:944`;
   `--include-agent-claims`, `capsulectl/internal/deal/commands.go:76-84`). Full disclosure here
   means the whole deal book and the held copy, not those.
2. **The held copy**: the counted prefix of the other side's book the party holds (records
   stored past a gap are not counted and not sent), from the store (`Shelf.HeldByDeal`,
   `shelf_held.go:34`), and **every** stamp proof the party holds for that log
   (`Shelf.HeldProofs`, `shelf_held.go:92`) with the witness receipts behind them. Each record and
   proof is signed by the other side, so the answering party cannot alter them, only leave some
   out (§3.1 says what that can do).
3. **Every book fact of the own book up to the checkpoint**, each with its stamp proof, its
   witness receipts and its **link opening**. A received record's time is the earliest named
   registration of the shelf checkpoint covering the holder's first fact whose `last_received`
   reaches it (`ReceivedRecordTime`, `shelf_record_time.go:167-210`), so the adjudicator needs
   all of them, not only the last. The status openings are not added: the rows stay withheld as
   today, since the adjudicator computes from the books, not from a party's statement of the
   result (`docs/design/461-deal-profile.md:1709-1717`).

Format: two new members of the x-deal-v1 bundle extension's `book`, `records` (the whole book, by
record id) and `received` (`{"log_id", "records", "proofs", "receipts"}`), and `facts` (each fact
with its stamp proof, receipts and link opening) beside the bundle.

`VerifyReportBundle` gains the checks for a full copy: the own records chain by `prev` from the
agreement to the checkpoint; every `received` record is signed by the agreement's
`counterparty_key` and the copy chains from its seq 1; the copy reaches every own record's `seen`;
each link opening recomputes its fact's commitment and names the copy's log, with a size the copy
reaches; and the facts form the chain `VerifyHandover` already checks per book (`prev_fact_size`,
sizes that never shrink, consistency proofs).

Record times come from these bytes with no shelf. Own records reuse the resolver
`HandoverTimes` already is (`shelf_handover_times.go:15-49`: facts and witnessed checkpoints
with their receipts, no shelf): PR 1 lets it be built from a book's facts and stamp proofs as
well as from a whole-shelf `Handover`, which an adjudicator answer must not carry (it holds
other books' facts and status openings). Only the received-record time is new,
`xdealv1.CaseRecordTimes`: the earlier of the sender's held stamp proofs and the holder's
linking fact, as the profile does today (`shelf_record_time.go:170-174`). The shelf's own
functions keep their behavior; the vectors of PR 1 check both give one time.

**The earlier selective copy stays.** The copy that opened the case (`POST /v1/cases`, which
`CheckCase` requires) is kept, not replaced: the adjudicator stores each intake object once
(ledger-cloud's `PutIfAbsent`), and a full answer must agree with it (the selective copy's
`rests_on.own.last` must be the record at that position of the full book, else it is fork
evidence, §2.3). On the party's side, each copy sent to the adjudicator, the case-opening one
included, is linked by an `answer` record in the party's journal (PROFILE.md §5, `ask`,
`answer`), the party's own book for records that serve no counterparty; PR 2 makes sure the
case-opening copy gets one too.

### 2.3 Reading four sources into one case

A new library function, `xdealv1.GatherCase` (beside `CheckCase`, `case_check.go`), takes the
dispute, the case-opening copies and up to two full answers, applies `CheckCase`'s checks, and
returns for each side the book the adjudicator reads:

- **Both of a side's sources shown**: the own book and the other party's copy of it must agree
  record for record over the copy's length. Two different records at one position, both signed
  by that side, are a fork: both are kept as evidence, the ruling records the position
  (`fork_at`, §3.2), and which branch governs is Steven's question (§6). Where the two agree
  and one only extends the other (the answers were cut at different times, so a record the side
  sent after its own answer is in the copy), the longer source is read and `fork_at` stays 0:
  `fork_at` is set only where two different records sit at one position.
- **Only the held copy shown** (that side is silent): the book read is the copy, up to its counted
  prefix, timed as §2.2 says.
- **Only the own book shown** (the other side did not answer, so no held copy of this side).
- **A party that opened the case but sent no full answer**: its own source is its case-opening
  copy, read as `CheckCase` reads it today, and it holds no copy of the other side.
- **Neither**: that side's book is not read.

capsulectl-deal's `deal ruling` and the hosted adjudicator both call `GatherCase`, so the two
cannot drift (ledger-cloud#35's rule).

## 3. A silent party, and the ruling

### 3.1 What a non-answering party leaves visible, and its limits

Its book up to the last record the other side received and counted: every act and statement it
sent during the deal, each signed by it, timed as §2.2 says. Not visible: anything it sealed but
never sent, and its own book past what it sent.

Two limits the ruling and its page state, not hide:

- **The other side chose what it handed over.** It cannot alter the silent side's records or
  proofs, but it can leave out records after its last `seen` and any of the silent side's stamp
  proofs it holds. Leaving out a proof can only make a record's time later (its time falls back
  to the holder's linking fact), never earlier. Nothing on main lets the adjudicator fetch the
  silent side's receipts itself (the library verifies receipts handed to it,
  `witness_receipt.go:19-53`; it has no lookup of another party's checkpoint at a witness). The
  silent side's remedy is a `challenge` (PROFILE.md §5, `dispute` kind `challenge`) with its own
  book, which the hosted service already takes (`POST /v1/cases/{case_log}/records`). When a
  time the ruling turns on came from the holder's linking fact rather than the silent side's own
  stamp, the ruling's page says so ("time from the other side's held proofs; not shown by the
  silent side") and names the source that timed it.
- **Nothing shows the silent side received the request.** The adjudicator holds no party address,
  so its request is published at the case, and a party that never disputed learns of the case
  from the dispute record the disputing side sends it. The adjudicator records its request in its
  journal before publishing it (`Book.Request`, `request.go:52`) and at the deadline records the
  absence (`Book.RecordAbsence`, `request.go:689`). The page says "request published <time>; no
  answer by <deadline>; delivery not shown", never "refused" and never "asked and ignored", as
  ledger-cloud#35 already treats the adjudicator's sent time ("never as proof of delivery").
  Whether a ruling may go against a side on these terms is Steven's question (§6).

### 3.2 The ruling's inputs and what it names (acceptance 4)

Inputs: the dispute, the agreement, the per-side books of §2.3, the disputed row's terms, and the
published rules (`rules_digest`).

The ruling already names, per side, the last record of that side's book it read, or `null` for a
book it did not read (`read`, PROFILE.md §5; `RulingRead`, `types.go:282-285`; set in
`case_check.go:164-171`). That names two books, not four. This note adds one OPTIONAL member to
the ruling body, `sources`:

```json
"sources": {"buyer":  {"own": "<record id>" | null, "held_by_seller": "<record id>" | null, "fork_at": 0},
            "seller": {"own": "<record id>" | null, "held_by_buyer":  "<record id>" | null, "fork_at": 0}}
```

`own` and `held_by_*` are the last record id of that source the adjudicator read, or `null` for a
source not shown. `fork_at` is the position of a fork in that side's book (§2.3), 0 for none, as
`ReportCounterpartLog.ForkAt` (`report.go:70-75`). `read` keeps its meaning (the last record of
each side's book read, from any source). A page shows a `null` source as "not shown to the
adjudicator" (Sherlock2's string, ledger-cloud#35) and never as "refused".

x-deal-v1 records are closed (PROFILE.md §1), so a party on a capsulectl-deal release before
`sources` refuses a ruling that carries it on `deal receive` and cannot name it. That is a forced
upgrade, stated as one: the hosted adjudicator writes `sources` only from the capsulectl-deal
release that reads it (PR 2), and its RUNBOOK and its `/status.json` name that release as the
minimum a party needs.

### 3.3 Tests

Vectors in `testdata/deal-v1/` (PR 1), read by Go (Rust and Python are frozen, §5):

- **Acceptance 5**: the buyer disputes a late delivery; the seller does not answer. Sources: B-own
  and S-held from the buyer. `sources` is
  `{"buyer": {"own": <id>, "held_by_seller": null, "fork_at": 0}, "seller": {"own": null, "held_by_buyer": <id>, "fork_at": 0}}`,
  and the seller's `perform` is timed from the buyer's held proofs.
- **Acceptance 1**: both answer; each held copy checks against the other's own book and the
  answering party's `seen`.
- **Acceptance 3**: a party's case-opening selective copy and its later full answer are both kept;
  a selective copy that disagrees with the full book is fork evidence.
- Refusals: a held copy short of an own record's `seen`; a link opening whose size the copy does
  not reach; a received record not signed by `counterparty_key`; a fact chain with a gap.
- A fork between B-own and B-held (`fork_at` set), and an own book that the held copy only
  extends (the longer read, `fork_at` 0).
- A seller proof left out: the `perform` time moves later, never earlier.
- A reader before `sources` refuses a ruling that carries it (the closed-record rule).

ledger-cloud's end-to-end run (PR 3) repeats acceptance 5 with two real capsulectl-deal parties.

## 4. Hosting and API (ledger-cloud#40)

### 4.1 Where it runs

The hosted adjudicator is built (ledger-cloud#36, merged at `95cdec76`; #38 since) and not
deployed: a public front with no key, and one operator process that holds the key and writes the
shelf (ledger-cloud#35). Its store is either Cloud Storage or a directory
(`cmd/internal/buckets/buckets.go`: a path that does not start with `gs://` is the directory
stand-in), so the same binaries run on one machine with no cloud resource.

| Option | What it takes | Cost |
|---|---|---|
| A. Steven runs it on his machine, as he said on the call (00:14:23) | `adjudicator-front` and `adjudicator-operator` with a directory store, the parties on the same machine or network | none |
| B. Deploy ledger-cloud's RUNBOOK as written | Cloud Run front, one e2-small operator VM, buckets, Secret Manager | about USD 15 to 16 a month (`RUNBOOK.md`) |
| C. No service: `deal ruling`, files passed by hand | capsulectl-deal only | none |

**Recommendation: A now, B when Steven chooses to deploy.** A is the call's own plan, needs no
spend, and runs the code B would deploy, so nothing is built twice. C stays the offline fallback
the loop already has.

How parties reach it: the agreement names the adjudicator by key and rules, never by address, and
the call settled that the adjudicator is not discovered at dispute time; its ARD listing lets
either side address it later (meeting record, Decision 3). For option A the address is Steven's
local URL, given to both parties' runbook; for B, the listed endpoint.

### 4.2 API

ledger-cloud#36's API stays; the changes are additions. Parties push, as today: the adjudicator
holds no party address, so its request is a signed document the parties fetch.

| Route (ledger-cloud `front/front.go:102-107`) | Today | Change |
|---|---|---|
| `POST /v1/cases` | the dispute and one or two adjudicator copies | unchanged; its 201 names the case's requests URL |
| `POST /v1/cases/{case_log}/records` | a later withdrawal or challenge | unchanged; it is also the silent side's way to challenge (§3.1) |
| `POST /v1/cases/{case_log}/books` | a party's whole book as a bundle, its stamp proof and receipts, for the entry check after a ruling | also takes a full answer (§2.2): the bundle with `records` and `received`, the `facts`, and the `request` it answers. One intake for party books, not a second route |
| `GET /v1/cases/{case_log}/rulings[/{digest}]` | party-signed | unchanged; a ruling carries `sources` (§3.2) |
| `GET /v1/cases/{case_log}/requests` | none | new, party-signed like the ruling reads: the adjudicator's request to that party (one per party), with its deadline |
| `GET /status.json` | the evidencebook commit and key id | also the minimum capsulectl-deal release (§3.2) |

Operator: `request` (seal the request in the adjudicator's journal, then publish it), `absence`
(record a missed deadline), and `rule` reading the case through `GatherCase`.

## 5. PRs and their order

Each PR is reviewed under the usual rules; none starts before Steven's ruling on §6.

1. **evidencebook, Go**: PROFILE.md and the schema (`sources`; the extension's `records` and
   `received`; the full-copy derivation token), `VerifyReportBundle`'s full-copy checks,
   `GatherCase`, `HandoverTimes` built from one book's facts, `CaseRecordTimes`, the full
   adjudicator copy in
   `StatusProvider.ReportWithBook`, and the vectors of §3.3. **Rust and Python: frozen, not
   needed** (Go is full, Rust follows only what the Mesh plugin calls, Python is frozen;
   `docs/PORT-PARITY.md:3-5`). The Mesh plugin calls no x-deal-v1 path: its only
   `evidencebook::deal` uses are x-deal-v0's `encode` and `step_capsule`
   (Mesh-LLM/capsules-plugin main `2b0577d`, `crates/evidence-plugin`, evidencebook `=0.0.24`
   with feature `deal`, `Cargo.toml:189`). PR 1 files the frozen-port issues naming the new Go
   entry points and vectors, adds their `frozen #N` rows to `PORT-PARITY.tsv`, and keeps the
   `deal-v1-diff` job's inputs to what Go, Rust and Python already share.
2. **evidencebook, capsulectl-deal**: answering a fetched request in full (no selective flag), the
   journal `answer` for every copy sent to the adjudicator, `deal ruling` over `GatherCase`, and
   reading `sources` on `deal receive`; then a `capsulectl/v*` release, which 3 needs before it
   writes `sources`.
3. **ledger-cloud**: move the evidencebook pin; `GET .../requests`, the full answer on
   `POST .../books`, the operator's `request`, `absence` and `rule`; the minimum release on
   `/status.json`; the end-to-end run of acceptance 5; RUNBOOK's local run (option A).
4. **Loop step 09** (evidencebook#830): the dispute, ruling and receive step on the new flow.

## 6. Questions for Steven (the what)

1. **Full disclosure contents.** A party's answer gives the adjudicator its whole deal book and
   every record of the other side's book it received (§2.2), without the other side's consent
   beyond the agreement's adjudicator term. This carries out u207; confirm that forwarding the
   received copy is within it.
2. **Ruling against a silent side.** Nothing shows a silent side received the request, and its
   record times rest on proofs the other side chose to hand over (§3.1). Proposed: the ruling may
   still go against it, its page says "delivery not shown" and names the sources, and its remedy
   is a challenge with its own book.
3. **A fork.** When a side's own book and the copy the other side holds differ (§2.3), proposed:
   the branch the other side received governs, since the signer showed it to its counterparty,
   the ruling records `fork_at`, and both branches are kept and shown, since a fork is itself
   evidence.
4. **The answer deadline.** How long a party has before its books count as "not shown to the
   adjudicator". Proposed: 7 days from the request, a service constant stated in the practice
   statement, as the 30-day challenge window is (ledger-cloud `ledger_cloud/adjudicator/RUNBOOK.md:74`).
5. **Hosting.** Option A (Steven's machine) now, B on his decision to deploy (§4.1).

Engineering's doors, for Sherlock2's door confirmation (format shapes with no product meaning
beyond u207 and the answers above): the ruling's `sources` member, the bundle extension's
`records` and `received`, the full-copy derivation token, and the forced upgrade of §3.2.
