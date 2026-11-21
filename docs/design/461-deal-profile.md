# evidencebook#461 step 5: the deal profile `x-deal-v1`

Design note for [evidencebook#461](https://github.com/action-state-group/evidencebook/issues/461),
acceptance 5 and 9, and the deal profile's part of acceptance 3 and 4. Written 2026-11-17 CST, at
main `a443a83`. Every `file:line` below is at that commit unless it names another repository.
"Design" is the Evidence Layer Integrated Design, Reference tab, as exported on 2026-11-16;
"Appendix A" is its appendix, cited by the line numbers of its step-by-step table ("line 15a").
"Shelf note" is [docs/design/461-shelf-facts.md](461-shelf-facts.md), the merged library parts
(steps 4a and 4b) this profile builds on. "Forms" is agent-action-capsule `spec/shelf-forms-v0.md`
at `132a221`.

Each rule is marked as one of two kinds:

- **Decided**: taken from the design, a ruling, the issue's acceptance text or merged code, with
  its source.
- **Proposed**: this note's choice, where the sources are silent. A reviewer accepts or rejects it.

Section 12 lists what the note could not settle.

## 1. Fixed constraints

These are not reopened here.

1. **A new profile version beside `x-deal-v0`** (#461 acceptance 9; design §13, step 5). The new
   version has a version mechanism, which the profile lacks today (design §13, step 5).
2. **`x-deal-v0` stays unchanged.** Its 23 record types (`dealprofile/names.go:27-62`), its
   `outcome_note` record (`names.go:50-51`), `standard_history/1` and `/2`
   (`dealprofile/standard_history.go`), `python/examples/goose` and the x-deal-v0 checker keep their
   bytes and behavior. A demo on 2026-11-18 depends on them: evidencebook-skills `demo/ard` reads them
   from a pinned checkout (2c1cc90). Old logs are never rewritten (design §13, step
   5: "New deals only; old logs are not rewritten"). Today's deals stay readable.
3. **The key-derivation strings stay** (`dealprofile/keys.go:201`, `208`, `219`;
   `rust/evidencebook/src/deal/keys.rs:336`, `344`, `354`; `python/evidencebook/deal/keys.py:368`,
   `375`, `383`), so no existing fingerprint changes. Section 2.4 says how.
4. **Acceptance 9.** A deal book opens at agreement. There is no close and no results log. Term
   rows have due dates and contest windows. Each side makes statements. Both agreement records
   name the same witnesses. A started deal whose records have no time from any witness is counted, its rows local,
   whether or not its agreement names a witness (Steven, ops u616, 2026-11-19: "count it"; ops u608
   and #570 for a deal that agrees and then goes silent, which starts and is counted, local). AAC
   `/3` SH-12 still leaves such a deal out when its agreement names a witness; AAC#634 changes it. The stamp proof follows the order of design §4.3 (section 7). The
   rules verdict word stays a record in its own deal book, bound to the deal id and the pack
   digest, carried in the counterparty cut under u195's conditions, and never in the journal
   (today's `rules_verdict`, `names.go:49`).
5. **Acceptance 5.** Vectors cover started, awaiting agreement and not started, a late offerer
   agreement, a cancellation, and a counterparty without the layer, whose book is marked so and
   counted from our receipt only. A deal that has not started is in no tally.
6. **The ten record types and the mapping table of design §7**, with what is sealed in the
   journal and what in the deal book.
7. **The seen field.** Its value is the library's `Shelf.CountedPrefix(senderKey, logID).Last`
   (`shelf_receive.go:1121-1136`; #461 comment 6949048721). The profile decides which records
   carry it.
8. **The row status function exists** (`rowstatus/rowstatus.go`, #466). The profile supplies its
   inputs and does not redefine it.
9. **Library layering.** The library knows no profile record format. `ReceiveProfile`
   (`shelf_receive.go:47-70`) and `StatusProvider` (`shelf_stamp.go:44-46`) are the profile's
   hooks, and this profile implements them.
10. **No release.** Merges in this repository also wait until the demo release batch is tagged
    (#461 comment 6938214546).

Steven's written confirmation of #461's target ("approved on evidencebook#461") is recorded in
#461 comment 6950272711, so step 5 may merge once reviewed. AAC normative text still needs his
approval per head (same comment).

## 2. The version mechanism

### 2.1 What exists today

- A deal record is a JSON object whose profile block is a top-level member named after the
  profile: `{"x-deal-v0": block, "body": body}` (`PROFILE.md:33-37`; `dealprofile/record.go:135-148`).
  The block repeats the name in `profile` (`types.go:48-49`; `PROFILE.md:170`).
- The profile already reserves the next name: "Records are closed. Unknown members fail the
  schema. A new member means a new profile version (`x-deal-v1`)" (`PROFILE.md:116-117`).
- A v0 reader refuses any other block. `Record.UnmarshalJSON` decodes with unknown members
  disallowed (`record.go:151-163`, `223-233`), so `{"x-deal-v1": ...}` is an error there, not a
  misread. `standard_history.go:139-151` treats a record with neither an `x-deal-v0` block nor a
  typed `/v0` type as "not a step" and skips it.
- Nothing dispatches between versions: `Profile` is one constant (`names.go:8`), as in Rust
  (`deal/names.rs:5`) and Python (`deal/names.py:5`).

### 2.2 The new profile's name

**Proposed:** `x-deal-v1`, the name `PROFILE.md:117` reserves. Its normative text, schema and
fixtures live in `dealprofile/x-deal-v1/`, beside `dealprofile/x-deal-v0/`. Its Go package is
`dealprofile/xdealv1` (package `xdealv1`). Its Rust module is `evidencebook::deal::v1`, under the
existing `deal` feature (`rust/evidencebook/Cargo.toml:70`). Its Python module is
`evidencebook.deal.v1`. The v0 package, module and directory are not edited, apart from the one
dispatch function of 2.3.

### 2.3 How a reader tells v0 from v1

**Proposed**, as the version rule for every later version too:

1. **A record** carries exactly one top-level member whose name matches `^x-deal-v[0-9]+$`. That
   name is its version, and its block's `profile` equals it. One shape is kept as it is: a **typed
   v0 record** carries no block. Its top-level `type` is one of the typed record and contract types
   of `PROFILE.md` §9 (`task-authority/v0`, `proposed-action/v0`, `action-evaluation/v0`,
   `action-approval/v0`, `action-record/v0`, `action-outcome/v0`, `action-report/v0`,
   `check-request/v0`, `check-response/v0`, `commercial-bounds/v0`; `names.go:65-75`), as in
   `fixtures/positive-typed/02-task-authority.json:5-10`. Today's reader takes that shape as a v0
   step (`standard_history.go:145-149`). `dealprofile.Version(record []byte) (string, error)`
   returns `x-deal-v0` for such a record and for a record with an `x-deal-v0` block, `x-deal-v1`
   for a record with an `x-deal-v1` block, and refuses a record with two blocks, with a block and a
   typed `type`, or with neither. It is the only change to the v0 package, and it is additive. A
   reader dispatches on it. A reader that does not know the version refuses the record as unknown,
   never as invalid, and never reads it as another version.
2. **A book** has one version, that of its first record. Every later record of the book carries
   the same version, and a record of another version in it is invalid. A v0 deal book starts with
   `baseline` (`PROFILE.md:575-576`, rule 2). A v1 deal book starts with an `agree` record whose
   `kind` is `agreement` (section 3).
3. **A deal** has one version on both sides. The offer's version decides: the acceptor's
   agreement carries the offer's version, and a side that does not read that version declines or
   sends nothing. A v0 half and a v1 half are never one deal.
4. **A book fact** names the version in its `profile` member: `"x-deal-v1"`. A reader of the
   whole shelf picks the status value's schema by it. This departs from the shelf note's example
   value `"deal"` (`461-shelf-facts.md:73`; the library's test uses it too,
   `shelf_receive_test.go:729`). Forms §3.2 defines only `"none"` and leaves other names to the
   profile, so nothing merged fixes `"deal"`.
5. **A sealed report** names the version in its payload's `profile` member, as v0's does
   (`report.go:64-66`).

### 2.4 How old deals stay readable, and how the key strings stay

- v0 books are never rewritten. A shelf holds v0 and v1 deal books side by side. The deal plugin
  already routes each deal to its own layout one by one (evidencebook-deal
  `internal/deal/deal_books.go:43-63` at `d17d543`; design §13, step 5 cites lines 56 to 59).
  That routing gains one branch: a book whose first record is `x-deal-v1`.
- The v1 `StatusProvider` claims only v1 books. For a v0 book it returns `ok` false, so the book's
  facts carry profile `"none"` and status `{}`, exactly as today (`shelf_stamp.go:41-46`).
  `standard_history/1` and `/2` keep counting v0 deals from their records, unchanged.
- **The key-derivation strings.** `x-deal-v1` calls the v0 functions `dealprofile.DealKey`,
  `ProfileKey`, `Fingerprint` and `CommitText` (`keys.go:200`, `207`, `214`, `226`) and their Rust
  and Python twins, unchanged. The literal labels `"x-deal-v0/deal-key\x00"`,
  `"x-deal-v0/profile-key\x00"` and `"x-deal-v0/fp\x00"` are frozen domain-separation labels, not
  the version of the record that uses them. v1 defines no `x-deal-v1/...` label. The algorithm
  names `hmac-sha256-deal-key`, `hmac-sha256-profile-key` and `sha256-jcs-nonce256`
  (`names.go:13-20`) carry over unchanged. So every existing fingerprint keeps its value. A
  merchant's profile fingerprint is the same across its v0 and v1 deals, as `keys.go:204-206`
  intends ("one merchant has one such fingerprint across the profile's deals"). A vector checks a
  v1 record's fingerprint against `fixtures/fingerprint-vectors.json` of v0.
- `outcome_note` is v0 only. **Proposed:** a v1 book cannot hold one, so v1 "ignores it" in the
  sense `PROFILE.md:221` asks the new layout to name. A v0 note keeps counting on `/1` and `/2`
  (`standard_history.go:43-49`). Whether `/3` reads v0 books at all is AAC#620's (section 12,
  Q6).

### 2.5 A consequence to know

A v1 deal is not on a `/1` or `/2` card. `readHistoryRecord` skips a record with no `x-deal-v0`
block (`standard_history.go:150-151`), and `StandardHistoryDeal` needs a v0 `commit` or `pay`
action to call a log a deal (`standard_history.go:190-199`). Until `/3` (AAC#620) is served, a
host that seals new deals as v1 stops growing its `/1` card. So new deals stay v0 by default
until `/3` is served, and v1 is selected by a setting (Q7, decided).

## 3. The record types

### 3.1 What all v1 records share

**The block** (`x-deal-v1`). Members carried over from v0 keep v0's meaning (`PROFILE.md:166-186`;
`types.go:48-63`).

| Member | Req | Meaning | Source |
|---|---|---|---|
| `profile` | REQUIRED | `"x-deal-v1"` | Proposed (2.3) |
| `canonicalization` | REQUIRED | `"jcs"` | v0 `PROFILE.md:171`, §5 |
| `deal_id` | see below | `^deal-[0-9a-f]{16,64}$`, random and opaque, made by the offerer | Decided: design §4.2, "The deal id"; v0 `PROFILE.md:172`. Presence proposed (review round 3) |
| `record_type` | REQUIRED | one of the types in 3.2 | Proposed |
| `at` | REQUIRED | the author's own seal time, UTC, whole seconds. It is the author's word, never a record time (4.3) | v0 `PROFILE.md:175` |
| `seq` | REQUIRED in a book | the record's position in its book, from 1, with no gaps | v0 `PROFILE.md:174`, rule 1 |
| `prev` | REQUIRED iff `seq` > 1 | the record digest of record `seq - 1` | v0 `PROFILE.md:176`, rule 3 |
| `agreement_ref` | REQUIRED on every deal-book record after `seq` 1 | the record digest of the book's agreement. It replaces v0's `baseline_ref`, since a v1 deal book starts at its agreement | Proposed; design §4.2: "every record in it is bound to that agreement's rows digest" |
| `seen` | see 3.4 | the record id of the last record held from the other side | Decided (constraint 7); placement proposed in 3.4 |
| `refs` | per type | typed citations of earlier records, as v0's rel refs | v0 `PROFILE.md:179`, `types.go:39-45` |
| `marks` | OPTIONAL | names of the named records (4.4) this record is, such as `["paid"]` | Proposed (4.4) |
| `counterparty` | OPTIONAL | v0's fingerprint block, `hmac-sha256-deal-key` | v0 `PROFILE.md:178` |
| `producer`, `commit_alg` | OPTIONAL | as v0 | v0 `PROFILE.md:180-181` |

**Where `deal_id` is present** (review round 3). The offerer makes the deal id and puts it in its
offer (design §4.2, "The deal id"), so a record sealed before any offer, or one that spans deals,
has none to carry.

- **REQUIRED** on every deal-book record, on `ruling`, and on journal records that serve one deal:
  `propose`, `agree` of every kind (agreement, decline, cancellation), the journal overlay's `check`,
  `verdict`, and an `approval` that answers a verdict (they serve one deal's act), an `ask` or `answer` between the two
  parties, and a journey record (`message`, `claim`, `evidence`, `detail_change`, `agent_move`,
  all sealed in the journal, 3.2) sealed within one offer's thread, that is, after that offer
  exists.
- **Absent** on journal records that predate or span deals: an `approval` on `confirm_limits`,
  which approves an intent's limits rather than one act (`PROFILE.md:609-614`); `intent` (Appendix A seals the buyer's
  intent at line 6, before its offer at line 11; an intent's limit spans every deal that cites it,
  design §4.2), `admit`, an `ask` or `answer` of an admission, `sale`, `thread`, `thread_opened`,
  `sale_unpost` (the listing and its threads span its offers; `thread` keeps the offer's deal id
  only as v0's `thread_ref_commitment`), `counterparty_profile` (one value per merchant across
  deals, `PROFILE.md:219`), `disclosure`, and a journey record sealed before any offer.

A record check refuses a record that carries `deal_id` where this list says absent, or lacks it
where this list says required. Vector: `deal-id-presence` (`testdata/deal-v1/records/`; 11.2), owned by PR 2.

**Digest.** The record digest is `lowercase_hex(SHA-256(JCS(record)))` over the whole record,
with no normalization pass, and `canonicalization` other than `jcs` fails closed. Digests of
cited records use the same construction. This is v0's rule carried over (`PROFILE.md:550-567`).

**Record id and record digest.** Two digests name a record and must not be confused. The
**record digest** is the digest of the x-deal-v1 payload above, which `prev`, `agreement_ref`,
`refs` and an agreement's `offer` cite. The **record id** is the Capsule id of the sealed book
record (`record.go:183-187`). The store keys received records by record id
(`ReceivedRecord.Digest`, `shelf_receive.go:77-82`), so `seen` holds a record id.

**Sealing a deal-book record (Decided in shape, by v0).** A deal-book record is appended to the
deal book through the library's `Book.Append`. Its Capsule carries the book's
`evidence_record_header`, with `record_type` `deal_step`, `epistemic_type` `producer_claim`, the
record digest as its one payload commitment, and exactly one `cites` link to the previous
record's record id (none on the agreement). That is v0's header (`PROFILE.md:57-71`), minus the
extra cites v0's baseline and close carry.

**The deal book's log id (Proposed; review round 1).** Each side mints its own opaque log id
when it seals its agreement: `deal/` followed by 32 lowercase hex of fresh randomness. It is never
derived from the deal id, and the two sides' log ids differ. This departs from v0, whose two
books are both named `deal/<deal_id>` (`PROFILE.md:1223-1224`). The reason is the whole-shelf
reader. A fact shows its `log_id` in the clear (Forms §3.2; `461-shelf-facts.md:68`). The design
promises that two shelves' facts "cannot be paired by a shared digest" (design §4.3, "The facts";
line 470 of the export), and that a reader handed the whole shelf does not learn "which book of
another shelf a fact pairs with" (design §4.3, readers table; line 518). A shared `deal/<deal_id>`
in both shelves' facts would pair them with no opening. Nothing requires a shared id: the library
lets the caller choose a book's log id (`Shelf.Open(ctx, logID)`, `shelf.go:194-208`), the
design requires only that the `book_id` be the hash of the book's first record (design §4.1,
"Book"), which already differs between the two sides because their agreements differ, and Forms
requires only a non-empty string (Forms §3.2). The holder keeps the map from deal id to its own
log id in its own index (evidencebook-deal#212). It learns the counterparty's log id from the
`evidence_record_header` of the first record it receives from that book (8.3, `Read`). A deal id
names one book per side, since the acceptor refuses an id it already holds a book for (design
§4.2, "The deal id").

**Sealing a sent journal record (Proposed).** A journal record that leaves the shelf whole (an
offer, a decline, a cancellation) must not show its place in the journal (design §4.2: "In
neither case is the record's place in the journal shown"). A book record's Capsule shows its
book and position (`book.go:611`, `recordLocator(b.id, header.Seq)`; `record.go:151-155`). So
the record is sealed twice: as a journal book record whose payload commitment is the record
digest, and as a **standalone Capsule**, appended to no log, carrying the same x-deal-v1 record
as its payload, `action_id` `"<deal_id>/sent"`, `action_type` `fyi`, no chain, signed by the same
key. This is v0's standalone report form applied to records (`PROFILE.md:141-160`). The
standalone Capsule is what travels. Its record digest is the journal record's, so an agreement
that cites the offer's record digest cites the same record on both sides.

**One library call seals it (Proposed).** No call does both today. The library gains
`Shelf.SealSent(ctx, entry) (journalRecord, standalone []byte, err)`: it appends the record to the
journal and returns, beside the journal record, the standalone Capsule bytes, signed by the same
key, in one call, so neither the profile nor the plugin builds the standalone form itself. It
is profile-neutral: it takes the record's bytes and its `action_id`. Sending is not the
library's: the standalone bytes go out through the MCP or A2A entry once AAC#621's bindings land,
and until then through the host's own channel (11.1, PR 1).

**Signatures.** Every record is signed by its author's record key. A deal-book record verifies
under the key of the book's agreement. A ruling verifies under the adjudicator key the agreement
names (3.3, `ruling`).

### 3.2 The types, and where each is sealed

Decided: the ten types and their placement are design §7's table ("Ten record types" and the
mapping under it).

| Type | Kinds of record it carries (design §7) | Where sealed |
|---|---|---|
| `propose` | offer, counter-offer | journal; sent whole |
| `agree` | agreement; decline; cancellation | agreement: deal book, `seq` 1; decline and cancellation: journal, sent whole |
| `perform` | delivery or any other act; a remedy that is not a payment | deal book |
| `settle` | payment, payment received, refund or another remedy that is a payment | deal book |
| `statement` | a statement with no act (such as confirming receipt); a concession with no act; the record that names a cancellation or a ruling that arrived, with no rows | deal book |
| `dispute` | dispute, withdrawal, challenge | deal book |
| `ruling` | ruling | the adjudicator's case book |
| `ask`, `answer` | evidence request; the answer, a signed refusal, or the note that nothing arrived | journal, or the deal book when it is between the two parties of the deal |
| `admit` | admission decision | journal |

Design §7 leaves four kinds of record outside the ten: "intent; the check before an act; a change
of rules; the note of what was shared with whom ... These need types of their own or a place in
the overlay; no source settles which". The agreement must cite the intent, and an act must cite
its passing check (design §4.2, "Checks before an act"), so v1 cannot leave them out.
**Proposed:** an **x-deal-v1 journal overlay** of four types, sealed in the journal only and
never sent, each with v0's body:

- `intent` (v0 `IntentBody`, `types.go:79-92`, unchanged);
- `check` (v0 `CheckBody`, `types.go:262-299`, plus `payment_ref`, the reference the payment will
  be made under, design §4.2): what is about to happen;
- `verdict` (v0 `VerdictBody`, `types.go:354-366`, unchanged): the answer to **one check**,
  `pass`, `pause` or `deny`, with exactly one `checks` ref to its check (`PROFILE.md:208`). It is
  not the deal-level `rules_verdict` (`PROFILE.md:220`), which is one word on the whole deal (3.3)
  (review round 2);
- `approval` (v0 `ApprovalBody`, `types.go:384-391`, unchanged): its `approves` ref still names the
  verdict it answers, or, on `confirm_limits`, the intent, as in v0 (`PROFILE.md:209`).

**Authorization (D; review rounds 2 and 3).** The act is a deal-book `perform` or `settle` that
cites its `check` by digest (3.3; design §4.2: "The act's record in the deal book cites the passing
check by digest"); the check, its verdict and any approval stay in the journal, and the
counterparty sees only the act and the check's digest (design §4.2: "the counterparty has no
claim on it and relies on the statement").

- **What must hold:** v0's authorization conditions hold unchanged (`PROFILE.md:581-618`, rules 5
  and 6), with the act's citation of its check in place of v0's `authorized_by`. This note does not
  restate or alter them.
- **Owner:** PR 2 (`xdealv1` record checks), with vectors
  `testdata/deal-v1/authorization/`: **`approval-migration`** (REQUIRED, review round 3): a `pass`
  with no sealed approval, an act whose verdict's first approval is a refusal and a later one
  proceeds, and one approval reused for a second act, are each refused exactly as v0 refuses them;
  and a standing-intent approval outside `allowed`, refused.

A change of rules and the note of what was shared stay where the library keeps them today. The
decision was Steven's, and he took the recommendation (section 12, Q3, answered).

`rules_verdict` stays, under the same name, as a deal-book overlay type (constraint 4; section
3.3). The consumer journey records Muse uses (`message`, `claim`, `evidence`, `detail_change`,
`agent_move`, `disclosure`, `sale`, `thread`, `thread_opened`, `sale_cut`, `sale_close`,
`sale_unpost`, `counterparty_profile`, `baseline`, `verdict`, `action`, `outcome`, `close`,
`outcome_note`) are not among the ten.

**Proposed: the x-deal-v1 journey overlay.** Design §7 "moves the consumer journey records that
Muse uses into an optional overlay" (§14, row 7). These overlay types carry the `x-deal-v1` block
like any v1 record, keep v0's bodies, are no input to start (5), statements, row status, the status
value or a card, and a reader that does not know an overlay type keeps the record and ignores it.

**Where each is sealed, by phase** (review round 2). A deal book exists only from the agreement
(design §4.2, "When a deal book opens"), and what comes before it is in the journal: "A listing
and its thread with each buyer come before agreement, so what is sealed about them is in the
journal" (design, Appendix B; line 1615 of the export), and the seller's journal "holds the record
that it was published" (design §7, `propose`; line 800). So:

| Overlay type | Sealed in |
|---|---|
| `sale` (the listing was published), `thread` (one buyer's thread registered), `thread_opened`, `sale_unpost` | the seller's journal, always: they come before any agreement |
| `counterparty_profile` | the journal, always: it is "never in a shared copy" (`PROFILE.md:219`), and a deal book is copied to the counterparty |
| `disclosure` | the journal, always: design §4.1 puts "what it shared with whom" in the journal |
| `message`, `claim`, `evidence`, `detail_change`, `agent_move` | the journal, always. They are the holder's own working notes (v0 `PROFILE.md:202-206`): how it searched, what the other side asserted, the checks it ran, a changed identifier it flagged. In a deal book they would tell the counterparty and the adjudicator about the holder's private risk process, which Steven keeps out of the deal (the admission decision stays in the person's own book), and the design says the counterparty "has no claim on" the holder's checks (§4.2). An adjudicator gets those the holder cites in a dispute, by the holder's choice; each carries `deal_id` once its offer exists, so it can be cited (Sherlock2, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=211abab2ff32fae38d39cde2d38f111088d936a63ae0734fe5b1404657bf041b&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6) |

A record is never sealed into a deal book before its agreement: a v1 deal book's first record is
its agreement (2.3 rule 2), so a flow that has not agreed seals in the journal.

**A listing and its offers.** The listing's content is published in a feed and is not a record
that is sent (design §7, `propose`). The seller's journal `sale` record commits to it. Each buyer's
offer (`propose`) is its own record with its own `deal_id`, and is that buyer's thread: v1 has no
other thread id (3.5). **Proposed:** an offer that answers a listing carries `listing_digest`, the
SHA-256 of the published listing bytes it read (3.3). The seller relates each received offer to its
listing by that digest, and its journal `thread` record registers the offer's `deal_id`, committed
as v0's `thread_ref_commitment`. When the seller agrees to one offer, it declines each other offer
on that listing with reason `not_selected` (3.5); that decline is each losing buyer's copy, so v1
needs no sale bundle.

**v0 types with no v1 counterpart.** Seven, because v1 replaces what they did: `baseline` (the book
now opens with `agree`), `action` (an act is `perform` or `settle`), `close`, `outcome`,
`outcome_note` and `sale_close` (there is no close: a deal is settled by computation, design
§4.2), and `sale_cut` (it cuts a sale bundle, and v1 sends the decline instead). `verdict` is not
among them: it is the journal overlay's per-check verdict above. A Muse flow that seals one of the
seven on a v0 deal keeps doing so; on a v1 deal it seals the v1 record named. One book per deal, so
no v0 side book. The overlays' names were part of the vocabulary trim Steven decided (Q3,
answered: the overlays as proposed).

**v0 members not carried over.** v0's optional `demo` on a `baseline` (evidencebook#514, the demo's
receipt badge) is not carried into the agreement, nor into any body v1 defines (Ethan,
2026-11-18: "这个设计不要搬进 agreement 里了，没什么必要", do not move this into the agreement,
it is not needed). v1's `sale` overlay reuses v0's `body_sale` unchanged
(`x-deal-v1.schema.json`, `dealprofile.SaleBody`), so its optional `demo` stays as v0 has it.

### 3.3 Each type's body

Members are closed: an unknown member fails the schema, as in v0 (`PROFILE.md:116`). A statement
item, used by several types, is:

```json
{"row": "<row id>", "value": "met" | "not_met" | "cannot_tell", "remedy": "<remedy id>"?}
```

`value` uses rowstatus's words (`rowstatus.go:69-74`). `remedy` is set only on a statement that
chooses a remedy (4.6).

**`propose`** (journal). The offer.

| Member | Req | Meaning | Source |
|---|---|---|---|
| `role` | REQUIRED | the offerer's role: `buyer` or `seller` | design §4.2, "When a deal has started" |
| `to` | REQUIRED, or `null` | the key the offer is addressed to, as the listing names it; `null` for a counterparty with no layer (4.7) | Proposed: the push rule needs it (8.3) |
| `counter_to` | OPTIONAL | digest ref of the offer this one counters | design §4.2: "a counter-offer, which is an offer in the other direction" |
| `terms` | REQUIRED | v0's `terms` (`types.go:66-76`): item, quantity, `price_minor`, currency and the rest | carried over |
| `pack_digest` | REQUIRED | the deal terms pack's digest, 64 hex | design §7, `propose` |
| `pack_url` | OPTIONAL | where the pack is fetched, for a feed with no room for the pack | design §4.2, "The rows digest" |
| `stated_at` | REQUIRED | the offerer's own clock time the fixed dates are computed from | design §4.2: "That date is the offerer's own clock, not a witnessed time" |
| `agree_by` | REQUIRED | the agree-by time, RFC 3339 UTC to the second | design §4.2: "it is the `valid_until` that settlement terms already carry" |
| `terms_rows` | REQUIRED | the rows and the named records they hang on (4.2) | design §4.2 |
| `adjudicator` | REQUIRED, or `null` | `{"key_id", "rules_digest", "rules_url"}`; `null` is an offer with no adjudicator | design §8, "Who it is, and its rules" |
| `forums` | REQUIRED | the forums the offerer accepts for a dispute (Q4's values): the recourse clause, so no forum is picked at dispute time. Empty exactly when `adjudicator` is `null`, which allows no dispute | evidencebook#404 (Steven, 11-05: "the recourse clause that was agreed", "forums accepted"); Sherlock2 on evidencebook-deal#215, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=6c12438b1a1f6fa3f225b0a1acb8dd5be8e9716534cddef9e39a0e105d4fecc4&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6 |
| `witnesses` | REQUIRED | the witnesses the offerer accepts from the listing's list, possibly empty | design §4.3 (witness paragraph); u186 for an empty list; each witness named by its key id with its URL as a hint (Q1, answered) |
| `ask` | OPTIONAL | how to ask the offerer for evidence: its evidence commitment's address and digest | design §7, `propose` |
| `profiles` | OPTIONAL | the deal profile versions the offerer reads | Proposed (2.3) |
| `listing_digest` | OPTIONAL | the SHA-256 of the published listing bytes this offer answers. A third digest, distinct from `pack_digest` (the deal terms pack) and from the rows digest (`terms_rows`): it names the feed bytes the buyer read, so the two must not be merged | Proposed (3.2, "A listing and its offers"; review round 2; named per Sherlock2, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=211abab2ff32fae38d39cde2d38f111088d936a63ae0734fe5b1404657bf041b&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6) |

The offerer's card is stapled beside the offer, never inside it (design §7: "the offerer's card is
stapled beside it when the other side's admission asks for it").

**`agree`**. One body type with a `kind`.

- `kind: "agreement"` (deal book, `seq` 1):

| Member | Req | Meaning | Source |
|---|---|---|---|
| `offer` | REQUIRED | digest ref of the accepted offer | design §4.2, "The rows digest" |
| `rows_digest` | REQUIRED | the rows digest (4.2) | same |
| `side` | REQUIRED | `offerer` or `acceptor` | design §4.2 |
| `role` | REQUIRED | `buyer` or `seller` | design §4.3, "The facts" (the holder's role) |
| `admit` | REQUIRED | digest ref of the holder's journal admission decision | Decided: design §4.2, "When a deal book opens" (line 326 of the export: the agreement "cites by digest ... the side's own intent and admission decision"); Appendix A writes the seller's admission at line 14 and cites it at line 15, and the buyer's at line 9g, cited at line 18 |
| `intent` | REQUIRED when the holder sealed an intent for the task, else absent | digest ref of the holder's journal intent | design line 326 names the intent too. Appendix A has an intent for the buyer only (lines 6 and 18) and none for the seller (line 15), so the intent is required only where one exists; section 13, item 12 |
| `witnesses` | REQUIRED | equal to the offer's | Decided: acceptance 9; design §4.3 |
| `adjudicator` | REQUIRED, or `null` | equal to the offer's | design §8 |
| `forums` | REQUIRED | equal to the offer's, as a set | as `forums` above |
| `counterparty_layer` | REQUIRED | `false` marks a counterparty with no layer (4.7) | Decided: design §4.2, "The rule is for two sides that both run the layer" |
| `counterparty_key` | REQUIRED, or `null` | the counterparty's record key: the offer's signer for the acceptor, the acceptor's agreement signer for the offerer; `null` with no layer | Proposed: the push rule (8.3) |
| `checkpoint_key`, `shelf_log_id` | REQUIRED | the holder's checkpoint key id and shelf log id | Proposed: lets the receiver bind a stamp proof to this shelf log (7.3), which Forms leaves open (SF-5) |

  The rows are not repeated: the agreement cites their digest (design §4.2: "nobody sends a
  second copy of the rows"). A record check refuses an agreement whose `witnesses` or
  `adjudicator` differ from the cited offer's.

- `kind: "decline"` and `kind: "cancellation"` (journal, sent whole): section 3.5 gives them
  exactly.

**`perform`** (deal book).

| Member | Req | Meaning |
|---|---|---|
| `act` | REQUIRED | what was done, a token: `deliver`, `ship`, `serve`, `replace`, or namespaced |
| `observed` | REQUIRED | how directly the act was observed (design §7 S1); the grades are Q10 |
| `detail_commitment` | OPTIONAL | the act's detail (a tracking number, say), committed with `sha256-jcs-nonce256` |
| `statements` | REQUIRED, possibly empty | statement items for the rows this act fulfils |
| `check` | OPTIONAL | digest ref of the passing journal `check` it rests on (design §4.2, "Checks before an act") |
| `remedy_for` | OPTIONAL | `{"row", "ruling"?}` when the act is a remedy that is not a payment (design §8 table, remedy) |

**`settle`** (deal book). The leg itself is the Capsule's top-level `settlement` member, as v0
carries it (`PROFILE.md:205`, `dealprofile/step.go:55-57`, `105`). The body holds:

| Member | Req | Meaning |
|---|---|---|
| `leg` | REQUIRED | `payer` or `payee` |
| `statements` | REQUIRED, possibly empty | for example `[{"row": "paid_as_agreed", "value": "met"}]` |
| `check` | REQUIRED on a payer leg | digest ref of the passing journal `check` that recorded the payment reference |
| `supersedes` | OPTIONAL | digest ref of this side's earlier leg for the same payment (a pending leg replaced by its outcome; design §4.2, §8) |
| `reverses` | OPTIONAL | digest ref of the leg a refund reverses (design §4.2: "an ordinary payment record that points at the first payment") |
| `remedy_for` | OPTIONAL | `{"row", "ruling"?}` on a leg that is a remedy (a refund, say): the row it remedies, and optionally the ruling it points at. The ruling is never required: a remedy counts by what it gives (6.1) |

**`statement`** (deal book). `statements` (REQUIRED, possibly empty) and `names` (OPTIONAL):
`{"cancellation": ref}` or `{"ruling": ref}`, the arrived record it names, which travels to the
store and never into the book. A `statement` with empty `statements` and no `names` is invalid.

**`dispute`** (deal book). One body type with a `kind`, as design §7 puts all three here.

- `kind: "dispute"`: `row`; `statements`, the record digests of the statements on that row that
  exist when it is sealed; `adjudicator`, the key id the agreement names (design §8 table); `forum`,
  the process that resolves it (Q4, answered), one of the agreement's `forums`; a forum outside them is
  refused (`CheckDispute`), and so is a dispute on a deal whose agreement names no adjudicator.
- `kind: "withdrawal"`: `row` (same table).
- `kind: "challenge"`: `ruling`, digest ref; `grounds_commitment` (same table).

**`ruling`** (the adjudicator's case book; one book per case, design §8). Members are design §8's
table: `row`; `keys`, `{"buyer", "seller"}`; `against`, `buyer` | `seller` | `null`; `value`,
`met` | `not_met`; `remedy`, `{"kind", "amount_minor"?, "currency"?, "due"}` or absent; `read`,
the last record id of each deal book the adjudicator read; `rules_digest`; `forum`, the process that
ruled (Q4, answered), one of the agreement's `forums` (`CheckRuling`); `final`, a boolean.
Its block has no `seen` and no `agreement_ref`; `seq` and `prev` are the case book's.

**`ask`, `answer`.** The evidence request interaction already exists in the library and in the
evidence request draft (`request.go`; CLAUDE.md, Layout). **Proposed:** v1 wraps it and does not
redefine it. `ask` holds `request`, the digest ref of the library's request document, and `to`,
`counterparty` | `adjudicator`. `answer` holds `request`, `outcome` (`evidence` | `refusal` |
`absence`) and `answer`, the digest ref of the library's answer document. The documents stay in
the store and originals, as received or sealed today.

**`admit`** (journal). `outcome` (`GO` | `ASK` | `LIMIT` | `NO`), `tier`, `rows` (the pack row
ids that decided it), `asks` (digest refs), `pack_digest`, `card_digest`, `answers` (digest refs).
Decided: design §7, `admit`; Appendix A lines 9 and 9g.
- **The members' meaning** (capsule-engine#303; Sherlock2's door confirmation,
  buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=a5114d0c48a1835b90e8e071a9fd086a3bae8c75c6f0b8ce55419e0c0c8d11e5).
  `tier` is the pack tier admitted at; `rows` are the pack's row ids the decision read, in the
  admission pack loader's grammar `^[a-z][a-z0-9_-]*$` (capsule-engine `go/admission/pack.go`),
  not the profile's `row_id`, which refuses the hyphen of a baseline row such as `owner-signed`;
  `pack_digest` is the pack's `definition_digest`; `card_digest` is the SHA-256 of the JCS bytes
  of the card document the decision read. The widening keeps every record valid that was valid
  (`testdata/deal-v1/records/admit-rows.json`).
- **No `admit` for a NO.** A NO has no tier, and `tier` is required. A deal cites a GO (THE_LOOP
  step 4), so a NO is not sealed as an `admit`; the local `admission_decision` keeps it. A NO is
  never cited by a sent or deal-book record, so it needs no nonce; if that changes, the rule below
  applies (Sherlock2, same message).

**A cited journal record carries its own secret** (decided: Sherlock2, on its twin's privacy P1,
buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=6a07cefde698fa8db9dcafc2aa97914bb931a56995fea0736efb57bdf1e4d927;
it applies u528, so it needs no ruling from Steven).
- **The leak.** `admit` has no secret member: an outcome of four values, a short tier, rows from a
  public pack, and `at` to the second. The agreement's `admit` hands the counterparty its digest,
  so the counterparty could try each outcome until one matches. That breaks u528: the admission
  decision stays in the person's own book. An act's `check` has the same shape, with its action,
  amount and currency guessable from the terms.
- **The rule** (worded in the PR 4 nonce review, Sherlock2, rounds 1 and 3). A journal record
  whose digest leaves the holder MUST carry a member of its own that nobody else can guess. A
  secret in a record it cites does not count, since the cited digest may itself be known: an act
  shows its check's digest. A digest leaves the holder when it is cited by a sent or deal-book
  record, signed beside a record (§8's person's signatures over an approval, a `confirm_limits`
  and the first intent; AAC#627), or disclosed.
  - Why a cited secret does not count (Codex, round 2). An approval's digest travels beside the
    act. The approval's members are guessable, and its `refs` hold the verdict's digest, which
    is guessable from the known check digest. So `approval` carries a `nonce`, as v0's body plus
    `nonce`, read against v0's closed body without it (`neg-approval-without-nonce`).
  - `verdict` carries one too, as defence in depth (`neg-verdict-without-nonce`). Its digest does
    not leave the holder under the rule, since §8 signs the approval, not the verdict.
  - The rule reaches only records whose own digest leaves. `admit` cites `ask` and `answer`
    records, and those carry no nonce and need none: `admit`'s own nonce hides the digests it
    cites.
  - A record sent whole (an offer, a decline, a cancellation) is exempt, since its content leaves
    with it (Codex, same round).
  - A deal-book record and an offer carry no block `refs`. Otherwise `refs` could cite any journal
    record, such as a verdict or an ask, outside the members each type names (Kimi, same round;
    `neg-perform-with-refs`, `neg-offer-with-refs`). Only a journal `verdict` and `approval`
    carry `refs`.
- **What carries it.**
  - `admit`, `check`, `verdict` and `approval` carry `nonce`: 256 bits, 64 lowercase hex,
    fresh per record. `xdealv1.NewNonce` draws one from `crypto/rand`. `CheckAuthorizations`
    refuses a journal in which two records share a nonce or an intent's `verbatim_commitment`,
    a cheap backstop against a host that does not draw fresh ones.
  - An `intent` already carries the required `verbatim_commitment` (`sha256-jcs-nonce256`, as
    v0 defines it). That covers `max_total_minor` and the other members it holds in clear.
- **What stays as it is.** The citation stays a plain digest ref, the journal lookup in
  `CheckAgreement` is unchanged, and the pattern is `reason_commitment`'s.
- **Where it is enforced.** The schema requires the members, and the negatives
  `neg-admit-without-nonce`, `neg-check-without-nonce`, `neg-nonce-not-256-bit-hex` and
  `neg-intent-without-verbatim-commitment` pin them.

**`rules_verdict`** (deal-book overlay). Members as v0: `word` (`met` | `not_met` | `unknown` |
`out_of_scope`), `pack_digest`, optional `pack_id` (`types.go:472-476`; `PROFILE.md:220`). Decided
(constraint 4; u195 as the How ack quotes it, #461 comment 6936343259): sealed in the party's own
deal book, never the journal; bound to the deal id (its block) and the pack digest (its body);
one word, at most one per deal; carried in the counterparty cut, with its payload withheld where
the holder's share policy says so ("not shared"); never read by a card, a report row or the row
status. v0 seals it "only after the final `close`" (`PROFILE.md:220`), and v1 has no close.
**Proposed:** seal it only once the holder computes the deal settled, as 9 defines `settled`
(design §4.2, third consequence). A deal that is never settled has no verdict. Q2, answered: as
proposed.

### 3.4 Which records carry `seen`

Decided: the value (constraint 7), and design §4.2: "Each record in a deal book names, by digest,
the latest record of the other side that its author held when it sealed it."

**Proposed:**

- Every deal-book record carries `seen` (REQUIRED). It is `null` only when the holder's counted
  prefix of the counterparty's deal book is empty. Otherwise it is
  `Shelf.CountedPrefix(counterparty_key, <the counterparty's book log id>).Last`, read when the
  record is sealed (the log id as 3.1 says the holder learns it).
  The acceptor's agreement therefore has `seen: null`. The offerer's agreement names the
  acceptor's agreement (design §4.2: the offerer's agreement "names the first one in its seen
  field"; Appendix A line 18).
- A deal book of a counterparty with no layer has `seen: null` on every record.
- Journal records, the journal overlay and `ruling` carry no `seen`. A sent journal record names
  what it answers by digest (`offer`, `counter_to`). A ruling names what it read in `read`.
- A record check refuses an offerer's agreement with `counterparty_layer: true` whose `seen` is
  not the record id of the acceptor's agreement the holder holds. An agreement with
  `counterparty_layer: false` has `seen: null`, which the check requires, so the case of 4.7 passes
  (review round 1). Its vectors go through Encode, Decode, the record checks and `StartFromBooks`
  (11.2).

### 3.5 The decline and the cancellation

**Decided** (design §4.2, "Either side can say no before the deal starts"; §7, `agree` and the
mapping table):

- A **decline** is the answer of a side that received an offer and does not want it. It is an
  `agree`-type record in the decliner's journal that cites the offer by digest and is sent whole,
  as an offer is. The offerer keeps it in its store and can look elsewhere at once.
- A **cancellation** is the offerer's step back. It is an `agree`-type record in the offerer's
  journal that cites its own offer and is sent to the other side.
- A side that has declined does not agree to that offer afterwards, and an offerer that has
  cancelled does not write its agreement. If one side does both, the other side's choice settles
  it. A plain message declines nothing and cancels nothing.
- A seller's "not selected" for a buyer's offer is a decline (Sherlock2, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=2a004e65fcab92f194db96a01c7142c05b34522386e8fe53c538da1191bfa8f4, as relayed
  by the coordinator on 2026-11-17).

**The product requirement it keeps.** actionstate-muse `docs/PRD_SELLER.md:303-305` (at
`32b9aeb`): every other thread is closed "with `deal close --outcome not_selected`, chained, so
each declined buyer's copy says so and verifies". That is v0's mechanism: a close in a per-buyer
deal log. Under v1 the seller never agreed with a losing buyer, so no deal book exists for that
buyer. The losing buyer's copy is the decline.

**The record.** Proposed, except where marked.

```json
{"x-deal-v1": {"profile": "x-deal-v1", "canonicalization": "jcs",
               "deal_id": "deal-…", "record_type": "agree", "at": "2026-11-01T10:05:00Z",
               "producer": {...}?, "commit_alg": "sha256-jcs-nonce256"?},
 "body": {"kind": "decline",
          "offer": {"type": "deal-record", "digest_alg": "SHA-256", "digest": "<64 hex>"},
          "reason": "not_selected" | "other",
          "reason_commitment": "<64 hex>"?}}
```

| Member | Req | Meaning |
|---|---|---|
| `deal_id` (block) | REQUIRED | the `deal_id` of the offer it answers. The offer's deal id is the offer's thread, and no other thread id exists in v1 |
| `record_type` (block) | REQUIRED | `agree`. Decided: design §7 |
| `kind` | REQUIRED | `"decline"` or `"cancellation"`. This marker tells the record from an agreement (`"agreement"`) and from each other. A record check refuses a body whose members do not match its `kind`: an agreement's members on a decline, say |
| `offer` | REQUIRED | digest ref of the offer record it answers (decline) or withdraws (cancellation), with the record digest of 3.1. Decided: design §4.2, "cites the offer by digest" |
| `reason` | OPTIONAL | one closed token. On a decline: `not_selected` (the seller's "we went with another buyer", `PRD_SELLER.md:303-305`) or `other`. A cancellation carries no reason token. No other token until Sherlock2 words and approves it (Q11) |
| `reason_commitment` | OPTIONAL | any free-text reason, committed with `sha256-jcs-nonce256`, never in the clear. The opening goes only to the side the record is sent to |

- **No other members.** There is no `seq`, `prev`, `agreement_ref`, `marks` or `seen` (3.4: journal
  records carry no `seen`), and no amount, price, item or counterparty fingerprint. muse's PRD asks
  that no seller receipt leak the winning amount to a loser (`PRD_SELLER.md:306-308`), and a decline
  names nothing about any other offer.
- **Who may write it.** A decline is signed by the key the offer was sent `to`. A cancellation is
  signed by the key that signed the offer. A record check refuses one whose signer is not that key.
- **Sealing and sending** (3.1, "Sealing a sent journal record"): it is sealed once as a journal
  book record whose payload commitment is the record digest, and once as a standalone Capsule:
  `action_id` `"<deal_id>/sent"`, `action_type` `fyi`, no chain, no `evidence_record_header`, and
  the record as its payload, so its `agent_input_digest` is the record digest. It is signed by the
  decliner (or canceller). It travels whole as `{"record": <the Capsule with its signature and
  key_id>, "agent_input": <the record>}`, the shape v0's standalone report already uses
  (`names.go:99-102`).

**How the receiver stores it.** `Shelf.Receive(ctx, sent, nil)`. The v1 `ReceiveProfile.Read`
(8.3) verifies the Capsule's signature and that the payload's record digest is its
`agent_input_digest`, decodes the payload strictly, and returns `LogID`
`x-deal-v1/sent/<record digest>`, `Position` 1, `Prev` "", `DealID` the offer's deal id, and
`Offer` false. `Admit` takes a decline only from the key our offer was sent `to`, and a
cancellation only from the signer of the offer it cites, which is the library's rule for "the reply
to our own offer, before our deal book exists" (`shelf_receive.go:51-56`). The store keeps it in
`shelf_received` as received. It is not appended to any book, and no deal book is opened for it:
for a declined offer, none exists on either side. The offerer's own journal holds nothing new. An
acceptor that had already agreed and then receives a cancellation also keeps it in its store, and
then seals one `statement` in its deal book with `names: {"cancellation": <ref>}` (3.3). That is
the case of 5.3 that computes as not started.

**How a third party checks a declined buyer's copy on its own.** The copy is the decline's
`{record, agent_input}` pair. The buyer can add its own offer, the record the decline cites.

1. `capsulectl verify --capsule <file>` on the Capsule. For a bare Capsule it recomputes the
   `capsule_id` with every Class 1 check, verifies the inline signature under its `key_id`, and holds
   that key to the profile's trusted keys. With no trusted key configured it reports
   `producer_trust` `not_performed`. It reports `cll_inclusion` and `business_truth` as
   `not_performed`, and says that a bare Capsule's committed digests are not rehashed
   (capsule-cli `cli/verify_capsule.go:66-143` at `9411813`).
2. The payload binding is therefore checked by the profile: `xdealv1.VerifySent(pair)`, and
   `check_profile.py` for v1. It checks that `SHA-256(JCS(agent_input))` equals the Capsule's
   `agent_input_digest`, that the record passes the schema and record checks of this section, that
   `offer` is the digest of the buyer's offer when that offer is given, and that the decline's
   signer is the offer's `to` key.

What the copy shows: that the holder of that key declined that offer, with the reason token if one
is given, and the author's own time `at`. What it cannot show:

- **The record's place in the seller's journal**, or how long that journal is (design §4.2: the
  place is not shown). This is deliberate.
- **When it was written.** `at` is the seller's word. A journal fact is proved to nobody
  (`shelf_stamp_proof.go:126-129`), so no witnessed time reaches the buyer.
- **That the seller did not also agree to that offer.** The other side's choice settles a decline
  and an agreement for the same offer (design §4.2). The buyer's own book shows which choice it made.
- **Anything about the selected buyer or the winning terms.** The record names none.
- **That the key belongs to the seller the buyer meant.** That is the key's binding and the trusted
  key list, not this record.

**Vectors** (`testdata/deal-v1/decline/`): a decline and a cancellation, each with its record,
standalone Capsule, pair and record digest; `VerifySent` passing on each; failures for a payload
that does not match `agent_input_digest`, a decline signed by a key other than the offer's `to`, a
cancellation signed by a key other than the offer's signer, `kind` `"decline"` with agreement
members, and an unknown reason token; `Receive` storing each with no book opened; and the acceptor's
`statement` that names a received cancellation.

## 4. The agreement, its rows and their dates

### 4.1 What the agreement cites and names

Decided (design §4.2, "When a deal opens" and "The rows digest"; Appendix A lines 15 and 18):
the offer's record digest, the rows digest, and the side's own intent and admission decision by
digest. Both agreements name the same witnesses and the same adjudicator, and the acceptor checks
that each is on its listing before it agrees (design §4.3, witness paragraph; §8, "Who it is").
The table in 3.3 lists the members.

**Witnesses.** An empty list is allowed (u186, as the How ack records it; design §4.3: "a deal with
no witness that both accept has no record times"). Then no record of the deal has a time, every
status is local, and a card counts the deal, its rows local (u608, #570). So does a deal whose
agreement names a witness but none of whose records has a time from one (u616; acceptance 9). A card
leaves no started deal out for want of a witnessed time. A witness is named by
its verified key id, with its URL only as a hint where to find it (Q1, answered).

**The adjudicator** is `{"key_id", "rules_digest", "rules_url"}` or `null`. An agreement with no
adjudicator says so (design §8, "Who it is").

### 4.2 The rows and the rows digest

Decided: every row says who owes it, what is owed, its due date and the date its contest window
ends. Each date is fixed at agreement, as a fixed date or as a number of days after a named
record. Dates are UTC to the second (design §4.2, "Term rows, statements and status"). A row id is
its template's id in the pack, unique within the pack (design §4.2, "The row id").

**Proposed:** `terms_rows` in the offer is

```json
{"rows": [
   {"id": "paid_as_agreed", "owed_by": "buyer", "owed": {"amount_minor": 6000, "currency": "USD"},
    "due": {"at": "2026-11-01T23:59:59Z"}, "window_end": {"at": "2026-11-04T23:59:59Z"},
    "recomputed": true},
   {"id": "delivered", "owed_by": "seller", "owed": {},
    "due": {"after": "paid", "seconds": 345600}, "window_end": {"after": "paid", "seconds": 604800}},
   {"id": "as_described", "owed_by": "seller", "owed": {},
    "due": {"after": "received"}, "window_end": {"after": "received", "seconds": 1209600},
    "remedies": [{"id": "refund", "within_seconds": 604800}, {"id": "replace", "within_seconds": 604800}]}],
 "named": {
   "paid":     {"side": "buyer", "latest": "2026-11-04T23:59:59Z"},
   "received": {"side": "buyer", "latest": "2026-12-01T23:59:59Z"}}}
```

- **The projection to rowstatus** (review round 1). A profile row is not a `rowstatus.Row`: it
  also holds `owed` and `remedies`, which `rowstatus.Row` lacks (`rowstatus.go:108-114`), and
  `ParseInput` refuses unknown members (`rowstatus.go:194-209`). So the profile projects each row,
  and keeps `owed` and `remedies` as its own data:

  | `rowstatus` member | From the profile row |
  |---|---|
  | `Row.id` | `id` |
  | `Row.owed_by` | `owed_by` |
  | `Row.due` | `due` (`{"at"}` or `{"after", "seconds"}`), unchanged |
  | `Row.window_end` | `window_end`, unchanged |
  | `Row.recomputed` | `recomputed`, absent when false |
  | `Input.named[name]` | `{"time": <the named record's time, 4.4>, "latest": named[name].latest}`; `side` is the profile's and is dropped |
  | not projected | `owed`, `remedies` (the profile reads them for the payment check, 6.3, remedies, 4.6, and the report, 9) |

  `owed_by` names a role, and rowstatus's `parties` are `["buyer", "seller"]`.
- `owed` is what the template's parameters fix for this deal, as canonical JSON.
- `named` maps a name to the side that writes the named record and its latest date (design §4.2,
  "Dates that hang on a record": a latest date, written by the side the window protects).
  rowstatus's `Named` takes `{time, latest}` (`rowstatus.go:99-104`); the profile fills `time`
  (4.4).
- Row 2's window is expressed from `paid` (4 + 3 days) rather than from row 2's due date, since
  rowstatus's `Date` hangs only on a named record (`rowstatus.go:91-97`). Appendix A line 51 gives
  the same dates.
- **The rows digest** is `SHA-256(JCS(terms_rows))`, over `rows` and `named` together. Design §4.2
  says the digest is "SHA-256 over their canonical JSON bytes" of the rows. Covering `named` as
  well is proposed, since a row's dates are not fixed without its named record's latest date.
- The acceptor checks, before it agrees, that the rows are the pack's templates with dates
  computed correctly from `stated_at`, that `stated_at` is near its own clock, and that `named`
  follows the templates (design §4.2: "The side that agrees first checks that the rows are the
  pack's templates with the dates computed correctly"; Appendix A line 13). How near is the
  acceptor's setting.

### 4.3 Record time

Decided (design §4.3, "A record's time"; witness paragraph): a record's time is the earliest
registration time, from a witness the agreement names, of the first stamp on either shelf that
covers it. A record with no such time has none, and a status resting on it is local. `at` is
never a record time.

The library computes it for the holder's own book positions: `Shelf.RecordTime(book_id, position,
witnesses)` (`shelf_record_time.go:110-120`). The other two sources the design names have no
library call yet (section 9, PR 1):

- the holder's own stamp covering a counterparty record: the first own fact whose link value's
  `last_received.size` reaches that record's position;
- the counterparty's stamp covering its own record or ours: its stamp proofs, which the store
  keeps (`shelf_received_proofs`, `461-shelf-facts.md:254-257`), with its witnesses' receipts,
  which no library call accepts today. `Receive` takes a record and a proof only
  (`shelf_receive.go:280`), and Forms defines no receipt part (Forms §6.1).

The shelf note gives these two cases of acceptance 3 to step 5 (`461-shelf-facts.md:192-195`).

### 4.4 Named records, through `marks`

Decided: a named record is "the first record of a named kind by a named side", and "only a record
that says the item was received starts the window" (design §4.2, "Dates that hang on a record").

**Proposed:** a record is of a named kind when its block's `marks` lists that name. The named
record is the first record, in the named side's book order, that carries the mark. Its time
(4.3) is `Named.time`. The payment record carries `marks: ["paid"]`, and the buyer's receipt
`statement` carries `marks: ["received"]`. A statement never stands in for the mark: a statement
"row 2: met" without `marks` starts no window (same paragraph). A record check refuses a mark on
a record by the wrong side, and a mark the agreement's `named` does not define.

### 4.5 Statements

Decided: three values; a record of an act carries the statements for the rows that act fulfils; a
side with no act to record writes a `statement` record (design §4.2, "A statement"; §7 mapping).
A concession is a statement (design §7). A statement's time is its record's time (4.3). Which
statements count, and when, is rowstatus's (`rowstatus.go:28-37`).

### 4.6 Remedies

Decided: a ruling may order a remedy by a date, and rowstatus reads it (`rowstatus.go:138-151`,
`Ruling.RemedyDue`, `Remedy`). A remedy record is an ordinary `settle` or `perform` record on the
row by the side ruled against (design §8, "The remedy").

Design §4.2, "Remedy rows", also lets the terms name remedies a side chooses, with the remedy's
due date hanging on the choosing statement. rowstatus has no input for a chosen remedy outside a
ruling (`rowstatus.go:153-176`). **Proposed:** a statement with `remedy` set creates a derived row
`<row id>/<remedy id>`, owed by the other side, due `within_seconds` after the choosing record,
which v1 treats as a named record of its own. The design gives such a row no contest window. Q5.

### 4.7 A counterparty without the layer

Decided (design §4.2, "The rule is for two sides that both run the layer"): there is no second
agreement; the holder's agreement says so, and the book's facts carry the mark, so a reader can
tell such a book from one whose agreement was never answered. The deal has started at the holder's
first record of an act. Its rows are computed from the holder's own statements. A card files it,
under how it happened, as held by the holder's receipt only.

Proposed encoding: `counterparty_layer: false`, `counterparty_key: null`, `seen: null` throughout,
and the status value's `counterparty_layer` and `start.first_act` (8.1). The offer such a deal
cites is the holder's own `propose`, with `to: null`, kept in the journal and never sent.

## 5. When a deal has started

### 5.1 The rule

Decided (design §4.2, "When a deal has started", its table, and the paragraph on declines and
cancellations; Appendix A, last paragraph). A deal between two sides with the layer has started
when all three hold:

1. the two agreements cite the same offer record and the same rows digest;
2. the offerer's agreement names the acceptor's agreement in `seen`, and the acceptor's book names
   no cancellation of that offer, unless it named the offerer's agreement first;
3. the offerer's agreement has a record time not after `agree_by`, or the acceptor's book names it,
   or the offerer's agreement has no record time at all (#511: then the deal is started locally,
   also after `agree_by`, and counts; a time after `agree_by` makes it not started).

A record of the acceptor's book names the offerer's agreement when its `seen` is that agreement's
record id or a later record id of the offerer's book. Until the offerer's agreement has a time or
is named, "started" is local. An acceptor holding only its own agreement is **awaiting
agreement** up to `agree_by` and **not started** after it. A deal with no layer on the other side
has started at the holder's first `perform` or `settle` record.

### 5.2 The function

**Proposed:** `xdealv1.Start(in StartInput) (StartResult, error)`, a pure function of values, and
`xdealv1.StartFromBooks`, which reads them from the holder's deal book and the counterparty's
records in the store. Both sides compute it, each from what it holds. Its inputs are exactly the
status value's `start` member (8.1), so a reader of facts computes the same result.

```json
StartInput {
  "at": "<T>",
  "side": "offerer" | "acceptor",
  "counterparty_layer": true,
  "agree_by": "<RFC 3339>",
  "counterpart_agreement_held": true,
  "same_offer_and_rows": true | false | null,
  "offerer_names_acceptor": true | false | null,
  "offerer_agreement_time": "<RFC 3339>" | null,
  "acceptor_names_offerer": true,
  "cancellation_named_first": false,
  "first_act": {"time": "<RFC 3339>" | null} | null
}
StartResult {"state": "started" | "awaiting_agreement" | "not_started",
             "local": true | false,
             "by": "offerer_in_time" | "acceptor_named" | "first_act" | null}
```

Evaluation, in order:

1. `counterparty_layer` false: `started` when `first_act` is set, `local` when its time is
   `null`, `by: first_act`; otherwise `not_started`. `agree_by` plays no part (proposed: the design
   names no deadline for this case).
2. `cancellation_named_first`: `not_started`, whatever comes later (design §4.2: "the deal never
   starts, whatever the offerer sends later").
3. The counterpart's agreement not held, or held with `same_offer_and_rows` false (it is then
   about another deal and is set aside): `awaiting_agreement` while `at` is not after `agree_by`,
   else `not_started`. Only an acceptor can be here. An offerer always holds the acceptor's
   agreement, since it wrote its own after it, so this case is an error for an offerer.
4. `offerer_names_acceptor` false: `not_started` while `at` is after `agree_by`, else
   `awaiting_agreement` (the offerer's record does not join the acceptor's).
5. `acceptor_names_offerer`: `started`, `local` false, `by: acceptor_named`.
6. `offerer_agreement_time` set: `started`, `local` false, `by: offerer_in_time` when it is not after
   `agree_by`, else `not_started` (a late agreement, never named).
7. `offerer_agreement_time` null: `started`, `local` true, at any `at`, before or after `agree_by`
   (Appendix A lines 21 and 22: "a local result until ..."; after `agree_by`, #511: Steven's "count
   it", ops u608, a counterparty that agrees and then goes silent is counted, so the silent side's
   failure shows). `local` says the result may differ for a reader who holds a witnessed time: one
   after `agree_by` gives step 6's `not_started`, a naming gives step 5.

`offerer_agreement_time` comes from the witnesses the agreement names (4.3). A receipt from
another witness gives no time (`shelf_record_time.go:110-115`).

Until a deal has started its rows have no status, no dispute on it counts and no card counts it
(design §4.2). The status value still carries its rows' inputs, and a reader computes rows only
for a started deal.

### 5.3 What each case leaves (design §4.2 table)

| After the offer | Books | Start, on the acceptor's side |
|---|---|---|
| acceptor agrees, offerer agrees in time | both | started |
| counter-offer | none; two offers in two journals | no book; the roles swap |
| decline | none; decline in the acceptor's journal and the offerer's store | no book |
| nothing sent | none | no book |
| offerer cancels before acceptor agreed | none; cancellation in the offerer's journal and the acceptor's store | no book |
| acceptor agrees, then offerer cancels | acceptor's: agreement, then a `statement` naming the cancellation | not started |
| acceptor agrees, offerer sends nothing | acceptor's, agreement only | awaiting agreement, then not started |
| acceptor agrees, offerer's agreement has no time by `agree_by` | both | started, local; settled for good once a time in time or the acceptor's naming comes, not started if a time after `agree_by` does (#511) |

A case with no book computes nothing: there is no book to stamp, and no fact.

### 5.4 Vectors (acceptance 5)

`testdata/deal-v1/start/vectors.json`: each case gives `StartInput`, the expected `StartResult`,
and, for the book-level cases, the signed records of both books from which `StartFromBooks` must
derive that input.

| Case | Expect |
|---|---|
| both agree, offerer's agreement timed before `agree_by` | started, not local |
| both agree, offerer's agreement untimed, T before `agree_by` | started, local |
| the same, T after `agree_by`, not named | started, local (#511) |
| acceptor only, T before `agree_by` | awaiting agreement |
| acceptor only, T after `agree_by` | not started |
| late offerer agreement (timed after `agree_by`), acceptor names it | started |
| late offerer agreement, acceptor names a later record of the offerer's book | started |
| late offerer agreement, not named | not started |
| acceptor names the cancellation, then the offerer's agreement arrives in time | not started |
| acceptor named the offerer's agreement, then a cancellation | started |
| two agreements citing different rows digests | acceptor: awaiting, then not started |
| offerer's agreement timed only by a witness the agreement does not name | as untimed |
| empty witness list | every started result local, or named |
| no layer, before the holder's first act | not started |
| no layer, after a timed act | started, not local, by first act |
| no layer, after an untimed act | started, local |

A tally vector pins "in no tally": a shelf with one started deal and one not-started book, whose
facts a test count reads, counts one deal, until `/3` counts (the shelf note does the same for a
dropped book, `461-shelf-facts.md:241-244`).

## 6. Statements, `seen`, and the row status inputs

### 6.1 From both books to rowstatus

For each started deal and each row, the profile builds one `rowstatus.Input`
(`rowstatus.go:153-176`) and calls `Compute` (`rowstatus.go:220-221`). Nothing here changes the
rule.

| rowstatus input | From |
|---|---|
| `at` | the T the reader asks about |
| `parties` | `["buyer", "seller"]` |
| `row` | the agreed row (4.2) |
| `named` | each named record's latest date, and its time once the first marked record has one (4.4) |
| `no_concession` | `false`; Mesh's `true` is step 8's |
| `statements` | each side's statement items on the row, in its book's order, from the holder's book and the counterparty's counted prefix only, each with its record's time or `null` |
| `check` | for a recomputed row, its check (6.3); otherwise empty |
| `disputes` | each `dispute` record of kind `dispute` on the row, with `seen_in_time` (6.2) |
| `withdrawals` | each `dispute` of kind `withdrawal` on the row |
| `rulings` | the case book rulings on the row by the agreement's adjudicator key, in case book order |
| `conflicting_final_rulings` | the holder holds two forks of that case book with different first final rulings (design §8, "A forked case book") |
| `remedies` | every `settle` or `perform` record whose `remedy_for.row` is the row, by the side the first final ruling is against, that gives what that ruling orders, whenever it was made and whichever ruling it points at, or none (design §8, "The remedy"; line 955 of the export). A payment remedy gives what is ordered when its two legs pass the check of 6.3 for the amount and currency the ruling's `remedy` names. Until a remedy has its own terms leg (6.3), a payment remedy is not counted. A non-payment remedy gives it when its `act` is the ruling's `remedy.kind`. A record that gives something else is not a remedy of that ruling (review round 1) |

Only the counted prefix counts (`461-shelf-facts.md:294-298`): a record past a gap, or from a
fork's position on, is not an input. Each `Receive` call reports the deal ids whose rows to
compute again (`ReceiveChanges`, `shelf_receive.go:219-231`).

**Remedy vectors** (`testdata/deal-v1/remedy/`): a refund made before any ruling, then a final
ruling ordering that refund (remedy `made`, no second refund owed); a remedy that points at an
earlier non-final ruling (counted); a remedy with no `ruling` member (counted); a refund for less
than the ruling orders (not counted, remedy `due`, then `not_honoured`); a `perform` whose `act` is
not the ruling's `remedy.kind` (not counted); a remedy by the side the ruling is not against (not
counted).

### 6.2 What `seen` decides

- **Start** (5.1): whether the offerer's agreement names the acceptor's, and whether the
  acceptor's book names the offerer's agreement or a later record.
- **A late dispute's third condition** (design §4.2, rule 2; §8, "A dispute after the contest
  window"): `seen_in_time` is true when a record of the claimant's own book whose `seen` reaches
  the uncontested statement's record has a time not after the row's window end. The uncontested
  statement is the other side's standing one on the row: its latest statement rowstatus does not
  hold late (PR 4c-1 review round 1).
- **The link value** (8.2): `last_received` is the record `seen` would name now.
- **For each statement, when the other side's book first named it** (design §4.3, "The facts"):
  the time of the first record of the other book whose `seen` reaches it.

### 6.3 The payment check

Decided (design §8, "Settlement"): the check on a payment row passes when the two legs are agreed,
the agreed status is settled and the amount equals the terms. It fails on a mismatch, on sealer
conflation, and, with agreed legs, on a status of failed or reversed or a different amount. In
every other case it has not run, and rowstatus gets an empty `check`.

**Decided (PR 4a):** option A. The coordinator's decision for Ethan: "A; reuse AAC
settlement.Verify; no duplicate join". Sherlock2 confirmed that the terms leg has no product
meaning beyond the agreed price, so there is no Steven ask
(buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=c67e727202ac7aebfeb723b664f1766b24216958c2db67dcf3e082b3d838619e).
Its local twin gave the same answer, with the disclosure condition below. Settlement Records -01
§Terms defines the terms leg as sealed by the party that set the terms, and a terms leg no
counterparty leg cites as one party's statement of them, not an agreement. That is what an offer
already is, and the x-deal agreement stays the acceptance.

- **The terms leg.** The offer's sent Capsule (§7 of the profile) carries the terms leg as its
  top-level `settlement` member, `xdealv1.TermsLeg(offer)`. It holds version `0`, leg `terms`, the
  offerer's role in the payment, and the payment row's `owed` as its `amount`. The role is
  `payer` when the offer's `role` owes the payment row, and `payee` otherwise; a counter-offer
  makes its author the offerer.
  - `owed` `{"amount_minor", "currency"}` is the amount `{value, assetCode, assetScale}`, where
    `assetScale` is `dealprofile.AssetScale`: BTC's as x-deal-v0 defines it, else ISO 4217.
  - A payment row is a recomputed row whose `owed` is exactly such an amount
    (`xdealv1.PaymentRows`).
  - An offer with exactly one payment row MUST carry it (Sherlock2, PR 4a review: otherwise such
    a row would read "not run" with no sign of why). `VerifySent` refuses such an offer without
    it, a `settlement` member on any other record, and one that is not exactly `TermsLeg` of
    that offer. The sender seals it through `Entry.Members`, which `SealSent` puts on the
    standalone Capsule only.
- **One amount per terms leg** (both Sherlock2 answers). An offer carries a terms leg only when its
  terms have exactly one payment row. The check runs on that row's own payment only. A refund or
  another remedy that is a payment, and a second paid row (a deposit and a balance), need their own
  terms leg, which the profile does not define yet. Until then:
  - a refund or remedy leg (`reverses`, `remedy_for`) citing the offer's terms leg leaves the
    check not run;
  - terms with more than one payment row leave it not run, and a payment remedy is not counted
    (6.1).
- **The join is scoped to the started deal's offer.** `xdealv1.PaymentCheck` takes the sent offer
  pair and the record digest both agreements cite, and refuses another offer.
  - Each side's leg is a deal-book `settle` whose Capsule's `settlement` member's `terms_ref` is
    the sent Capsule's id. A leg answering another terms leg is passed over.
  - A payer leg is the payment row's owing party's, and a payee leg the other's. AAC's verifier
    applies the parties' keys as its key policy.
  - A leg superseded (`supersedes`) by a later record of its side is not read, whichever terms
    leg that record answers.
  - A leg's content is the counterparty's to get wrong, so a leg AAC's verifier refuses (a
    malformed `settlement` member), or one whose side, leg and role disagree, takes no part
    (`PaymentResult.Excluded`) and the check reads the others, rather than stopping (PR 4a
    review round 1). AAC's `unjoined` has its own reason.
  - AAC's `settlement.Verify` derives the state, so there is no second join here.
  - **Pass:** the legs are agreed, settled, at the terms' amount.
  - **Fail:** a mismatch, or agreed legs that are failed, reversed or at another amount.
  - **Not run:** everything else (terms only, one side stated, unjoined, pending).
  - Sealer conflation cannot arise: the parties' keys are distinct, and a leg under the other
    role's key is refused.
  - `RowBooks.Checks` carries the result to rowstatus.
- **Disclosure** (the twin's condition). The terms leg puts the price at the Capsule's top level,
  where it is in the clear. Nothing new is disclosed, because the sent Capsule travels only as the
  §7 pair `{record, agent_input}`, and `agent_input` already holds the offer's terms. A copy that
  carries the sent Capsule carries its record. No copy the library writes holds the Capsule
  without it.

The other recomputed rows wait for design §13, step 3 ("Each recomputed deal row computed in one
engine"), and until then report "not run". The digests the check ran over
(`PaymentResult.CheckedOver`) go in the link value's `checked_over` (design §4.3; Forms §4.2).

## 7. The stamp proof order

### 7.1 The order

Decided as acceptance 9 (design §4.3, "The stamp proof", proposed there on 2026-11-16; Appendix
A lines 15a, 16a and 17a):

1. The acceptor writes its agreement, stamps right after, and sends its stamp proof with the
   agreement.
2. The offerer checks that proof before its first act, and does not act without it.
3. The offerer writes its agreement, acts, then stamps.

Design §4.2 also says "The offerer stamps its agreement before the agree-by time ... and acts
after it has written it". Step 3 meets both only when the first act comes before `agree_by`.
**Decided** (Steven, u610, with Sherlock2's first condition): the offerer stamps after its first act,
except that it stamps its agreement at once, and sends that proof, when its first act waits on the
acceptor's. Since the acceptor's first act waits for the offerer's proof too (7.2), the two would
otherwise wait on each other. If the first act has not happened by `agree_by` minus the owner's
margin, it stamps its agreement alone first.

### 7.2 Calls on each side

**Acceptor**, after it seals its agreement:

1. `Cadence.Stamp(ctx, now)` (`shelf_stamp.go:538`).
2. `Cadence.StampProof(ctx, book_id, 0, AudienceCounterparty)` (`shelf_stamp_proof.go:126-130`).
   It carries the status opening and never a link opening (8.2).
3. It sends the record push `{record: agreement, stamp_proof}` (Forms §6.1).

**Offerer**, on receiving them:

1. `Shelf.Receive(ctx, agreement, &proof)` (`shelf_receive.go:280`). The store checks the record,
   then the proof: both checkpoints, one key, the fact's inclusion, and the book checkpoint
   against the records held (`shelf_stamp_proof.go:46-124`; Forms §5.3, §6.3).
2. **The coverage gate (B).** What must hold: an act is allowed only while the counterparty's
   current counted prefix includes its agreement, and a proof bound to the right `book_id` (the
   record id of that agreement), checkpoint key (the agreement's `checkpoint_key`) and shelf log
   (its `shelf_log_id`, and the log its published evidence commitment names) covers it. "Current"
   means as the store computes it at the moment of the act, after any fork found since (the
   store's coverage is capped to the counted prefix, `shelf_receive.go:1067-1078`), never from one
   `Receive` call's events or a stored flag alone. Owner: PR 1 (a lock-free library reader that lets
   the profile re-check every held proof's bindings) and PR 4 (`xdealv1.CanAct`). Vectors: 7.3.
3. It writes its agreement (`seen` = the acceptor's agreement), and before its first `perform` or
   payer `settle`, `xdealv1.ReceiveProfile.GateAct` applies the gate of step 2 (`CanAct`). Without
   it the act is refused. **Decided** (Steven, u610, buzz 42c1e73f; ops company-oss 3797bda0): the
   gate always applies, whether or not the agreement names a witness, to the first `perform` or
   payer `settle` only.
4. After the act: `Stamp`, `StampProof(..., AudienceCounterparty)`, and a push with the act's
   record.

**Acceptor**, on the offerer's proof: the same `Receive` and the same checks. **Decided** (u610): its
own gate applies to its first `perform` or payer `settle` after the offerer's agreement. A payee leg
and a statement are never gated, nor is any act after the first. Appendix A has the seller write its
payment received record (line 27) before the buyer's proof arrives (line 32), and deliver (line 37)
after.

**A counterparty without the layer** (Steven, u611, "layer ii"; buzz 20704793, ops 8ff23c29) has
no agreement or proof to wait for, and its first act goes ahead: it is not refused. `GateAct` allows
it and marks it unguarded (`GateNoLayer`). The receipt and the card say the other side sent no
stamp proof, so the deal is counted from the holder's receipt only. The status value carries this as
`counterparty_layer` false with `counterparty_proof_held` false (8.1), and the report and card read
those (section 9). A gate that never applies without that label is not adopted.

**Later**, before either side treats the deal as settled or relies on the other's card, it asks
for the witnessed checkpoint: a shelf checkpoint with a receipt from a named witness, the
consistency proof from the checkpoint it kept, and the stamp proof for the book's newest fact
(design §4.3, "The witnessed checkpoint"; Appendix A lines 42b to 42d). Neither the library nor
Forms has a form for receipts or a shelf consistency proof in a push (`shelf_receive.go:280`;
Forms §6.1). Section 9, PR 1, and Q8.

### 7.3 Vectors

`testdata/deal-v1/order/`, owned by PR 4 (the gate, `ReceiveProfile`) with PR 1's readers:

- the acceptor's agreement with its proof, accepted; the same without a proof (the offerer's act is
  refused); a proof under another shelf log id (refused); a proof by another checkpoint key
  (refused); a proof whose book checkpoint the records do not recompute (refused, evidence kept);
  the offerer's later proof, accepted; a payee leg sealed before the offerer's proof (allowed);
- **`handshake-from-offer`**: the whole handshake from an offerer that holds only its journal offer
  (the acceptor's agreement and proof admitted, 8.3);
- **`duplicate-proof`** and **`restart`**: the same proof sent twice, and the shelf closed and opened
  again after it was accepted (the act still allowed);
- **`proof-sender-both-sides`**: `ProofSender` for the acceptor's proof received by the offerer and
  the offerer's proof received by the acceptor, each returning the proof author's record key;
- **`wrong-shelf-then-restart`** (round 2): a proof signed by the acceptor's checkpoint key over
  another shelf log, stored and verified by the library, then a restart (the act still refused);
- **`genesis-fork-after-proof`** (REQUIRED, round 3): a verified proof covering the acceptor's
  agreement, then a second record from the same sender at position 1, which the store keeps as a
  fork and which ends the counted prefix before position 1 (`shelf_receive.go:571-602`); the act is
  refused, both before and after a restart, although the stored proof stays marked recomputed
  (`checkHeldProofs` re-checks only unchecked proofs, `shelf_receive.go:1010-1015`);
- **`shared-log-id`** (round 2): two senders whose books share one log id; `ProofSender` returns the
  one candidate that matches (8.3), and refuses when both match;
- **`fork-at-position-1`** (REQUIRED, round 3): two different agreements from one sender at
  position 1 of one log; `HeldFirst` returns both, and `ProofSender` refuses the proof rather than
  pick one (8.3), also when the two agreements name different `checkpoint_key`s, so filtering by
  key would leave one (round 4).

## 8. The StatusProvider and ReceiveProfile implementations

### 8.1 StatusProvider: the status value

`xdealv1.StatusProvider` is a `BookStatusProvider` (#525): at each stamp the shelf calls its
`BookValues` with the stamp's tick, the book and the journal, and it returns `("x-deal-v1", link,
status, true, nil)` for a v1 deal book and `ok` false for every other book (2.4). Its own `Values`
claims no book. It reads its own book and the journal through those handles, and the received
books only through readers that take no lock (`HeldByDeal`, `HeldFirst`, `CountedPrefix`,
`HeldProofs`, `RecordTimeByKey`, `RecordStamp`, `ReceivedRecordTimeByKey`,
`ReceivedRecordStamp`). A provider error for one book keeps that book's last fact and is reported
in the stamp's `Unstated`; the other books' facts are written (PR 4c-2b-2 review).

Decided: what the status holds is design §4.3, "The facts", and the shelf note's rule that it
holds inputs, never a computed status, so a quiet book gets no new fact when a date passes
(`461-shelf-facts.md:81-87`). **Proposed** encoding:

```json
{"v": 1,
 "role": "buyer" | "seller",
 "side": "offerer" | "acceptor",
 "agreed_at": "<the holder's agreement's at>",
 "counterparty_layer": true,
 "counterparty_kind": "person_agent" | "business_agent" | "shop_preauth" | null,
 "value_band": {"currency": "USD", "decade": 1} | null,
 "witnesses": [ ... as the agreement names them ... ],
 "adjudicator": {"key_id": "…", "rules_digest": "…"} | null,
 "counterparty_proof_held": true,
 "start": { StartInput without "at", its offerer_agreement_time and first_act time as <time> },
 "rows": [
   {"id": "as_described",
    "input": { rowstatus.Input without "at", its times as A below requires },
    "statement_named_by_other": [<time> | null, ...],
    "ruling_against": "buyer" | "seller" | null,
    "remedy_made": <time> | null}],
 "disputes": [
   {"row": "as_described", "claimant": "buyer" | "seller",
    "outcome": "open" | "for_claimant" | "against_claimant" | "no_finding" | "withdrawn",
    "forum": "<the dispute record's forum>"}]}
```

**The start member and a counterparty without the layer (#575).** `start.first_act` is `null` when
the counterparty runs the layer or the holder has written no `perform` or `settle` yet, else
`{"time": <time>}`: the time of the holder's first such record, a string, `{"stamp": n}`, or `null`
when the record has no time yet (a start that is then local, 5.2 step 1). A reader of a status
opening with `counterparty_layer` false so computes `Start` from the fact alone, and the deal
counts in a tally once the holder has acted. A report resolves the time from its stamps as it does
`offerer_agreement_time`.

**Times, and which statements a fact carries (A).** A fact carries only some of the holder's
statements: each side's latest one known to be in time, newer ones whose time is not yet known, and
a later concession (design §4.3, "The facts"); a fact states no time for a record its own stamp is
the first to cover (design §4.3, line 470 of the export). Its time values tell three cases apart: no
such record, a record whose time is not yet known, and a known time.

- **What must hold:** `Compute` over the statements a fact carries, with its times resolved as a
  reader resolves them, equals `Compute` over all the holder's statements with their record times
  (4.3), at every T at or after that fact's stamp, and for every later fact of the book.
- **Owner:** PR 4 (the status value). Its encoding of times and its statement selection are that
  PR's to define against this invariant; this note fixes neither.
- **Proposed by PR 4c** (for review):
  - *Times.* A time in a fact is one of three values: a string, the record's known time; `null`,
    no such record (a named record not yet written); or `{"stamp": n}`, a record whose time is not
    known when the fact is written, where `n` is the holder's first stamp that covers it (its own
    record, or a counterparty record its link value's `last_received` first reached). A reader
    resolves `{"stamp": n}` to the earliest receipt, from a witness the agreement names, of stamp
    `n`'s shelf checkpoint or of a later one (the library's `RecordTime` and
    `ReceivedRecordTime` do the same for the holder), and, for a counterparty record, to the
    earlier of that and the earliest named receipt of a counterparty stamp proof it holds that
    covers the record (design §4.3: the first stamp on either shelf). That second source needs
    receipts to travel with stamp proofs, which no form carries yet (section 9, Q8); until then a
    counterparty record resolves from the holder's stamps only, which can be later than its §4.3
    time. It stays untimed until a receipt exists. A fact never states a time for a record its
    own stamp is the first to cover: such a record is `{"stamp": <this stamp>}`. `n` is a local
    stamp index, not a digest, so it pairs with nothing on the other shelf.
  - *Statements carried.* Per row and side, in book order: every statement whose time is not
    known; for each of those, the earlier statement of that side with the greatest known time
    (the lower bound rowstatus reads, `rowstatus.go:278-303`; a book's record times never
    decrease, since a stamp covering a record covers those before it, so it is also the latest);
    the latest statement with a known time
    not after the window end; and every statement with a known time after the window end that
    gives its author the other side's in-time value (a possible concession). While the window end
    is not known (its named record has no time), every statement is carried. Anything else is
    dropped, since rowstatus can never read it.
  - *Check.* A randomized differential test holds the selection to the invariant: for generated
    rows, statements and times, `Compute` over the carried statements equals `Compute` over all
    statements at every T at or after the fact, with every `{"stamp": n}` resolved to a time not
    before the fact for the holder's records, and to any time not before the record before it in
    its book for the counterparty's (Sherlock2, on this proposal: P1 and P2).
- **Vectors** (`testdata/deal-v1/status/`): **`times-three-cases`** (a named record not yet written,
  so its row's dates stay unset; a record first covered by this fact's stamp, resolved to a time and
  passed to rowstatus as that time; a record with a time already known); **`unwitnessed-then-witnessed`**
  (a record first covered at stamp 1, whose checkpoint gets no named receipt, and stamp 2's
  checkpoint witnessed: a reader of the fact of stamp 2 resolves the same time the holder's
  `RecordTime` gives); **`carry-forward`** (a statement whose time is not yet known stays until it
  is known); and **`carry-forward-lower-bound`** (REQUIRED, review round 3): after the window end,
  with the buyer `met` and the seller `not_met` in time, the seller then states `cannot_tell` with
  a time and then `met` with no time. Over the full input rowstatus gives `in_disagreement` with
  `local` false (Codex ran it on the Python implementation); the fact's statements must give the
  same, although the earlier timed statement is neither in time nor a concession, because rowstatus
  uses a side's earlier timed statement to judge whether its later untimed one is late
  (`rowstatus.go:278-303`, `449-467`).

**The receipts a handover carries (C).** A reader handed the whole shelf resolves record times from
the witness receipts it is handed (A, above).

- **What must hold:** a reader of the handover resolves every record time the holder's `RecordTime`
  gives (`shelf_record_time.go:110-175`).
- **Owner:** PR 1 (`Handover` and `VerifyHandover`). Today `Handover` hands over one shelf
  checkpoint, the anchor, and its receipts (`shelf_handover.go:30-51`; the verifier checks receipts
  against the anchor only, `shelf_handover.go:468-469`), and a receipt verifies only against its
  full signed checkpoint statement (`cadence.go:167-179`). So meeting the invariant means handing
  over, and verifying, the earlier shelf checkpoints whose receipts give an earliest time, with
  those receipts; the form is that PR's (section 13, item 13).
- **Vector** (`testdata/shelf/handover/`): **`earliest-receipt-not-anchor`** (REQUIRED, review
  round 3): the earliest named receipt for a record belongs to a shelf checkpoint before the anchor;
  the handover carries that checkpoint's statement and receipt, `VerifyHandover` checks its
  signature, its shelf log and that its root agrees with the handed-over entries, and the reader's
  time equals `RecordTime`'s.

- `counterparty_kind` uses SH §7's `counterparty_class` values, as v0 reads them
  (`standard_history.go:38-42`).
- `value_band` is a currency and a power of ten (design §4.1, "Card"; AAC SH lines 299 to 305),
  from the agreed price.
- `counterparty_proof_held` says the holder holds an accepted stamp proof covering the
  counterparty's agreement. With `counterparty_layer` it gives the card's "how it happened"
  (design §4.1, "Card").
- `statement_named_by_other` runs parallel to `input.statements`.
- **No shared identifier** (review round 1, with 3.1). The status opening goes to a reader the
  holder chooses, a whole-shelf reader among them (`OpeningsFor`, `shelf_facts.go:333-365`). So it
  holds no deal id, no record id or digest of either book, no offer or rows digest and no log id:
  nothing the other side's facts also carry that would pair the two shelves (design line 470: "so
  that two shelves' facts cannot be paired by a shared digest"). The link opening, which does name
  the counterparty's log, goes only to the counterparty, the adjudicator and an allowed asker. What
  is left in common is the content both books share by nature: the record times and the dates, the
  roles, the witness list, the adjudicator's key and rules digest, and the pack's row ids. These are
  public configuration or shared content, not deal identifiers. Two shelves' facts can still be
  guessed to pair by equal times, which the design already says of ruling dates (design §8, "Which
  rulings went against a key"); section 13, item 14.
- **Card facts that rowstatus does not carry** (review round 1). `rowstatus.Ruling` has no
  `against` and `rowstatus.Remedy` only a time (`rowstatus.go:138-151`). `/3`'s deal entry needs
  `disputes` as `{"side", "outcome", "forum"}` (AAC#626 `spec/standard-history-v0.md` §5.4, §11.2,
  at `46c4cd3`), and each row's `owed_by` as `holder` or `counterparty` (§11.2). So the status adds
  `ruling_against` per row, and `disputes`, one entry per dispute record of either side: its row,
  its claimant's role, its outcome, and the dispute record's `forum` (Q4, answered).
- **A remedy made without a ruling** (Q5, answered: it counts as remedied). rowstatus records a
  remedy only for a ruled row (`rowstatus.go:138-151`), and 6.1's `remedies` input holds only those
  that give what the first final ruling orders. So the status adds `remedy_made` per row: the time
  (as A requires) of the first `settle` or `perform` record whose `remedy_for.row` is the row, sealed
  by the side that owes the row, with or without a ruling; `null` when there is none. A reader of
  facts counts an unruled row that did not hold as `remedied` when `remedy_made` is set. Owner: PR 4,
  with a status vector telling an unruled refunded row from an unruled unrefunded one by the
  status opening alone.
  **The effective ruling** (review round 2) is the one rowstatus applies: the first final ruling of
  the adjudicator the agreement names, and none at all when the holder holds two conflicting final
  rulings, in which case rowstatus gives `in_dispute` (`rowstatus.go:538-560`; design §8, "A forked
  case book"). `ruling_against` is the effective ruling's `against`, or `null` when there is none.
  The outcome is `withdrawn` after the claimant's withdrawal; `for_claimant` or `against_claimant`
  when the effective ruling is against the other side or the claimant; `no_finding` when it is
  against neither; and `open` otherwise, conflicting final rulings included, so no branch of a
  forked case book decides a card's dispute outcome. `/3` maps these as 10 says, and `forum` is the
  dispute record's (Q4, answered). The
  card-level `remedied` count is AAC#626's, and reads only row results, owed_by and remedy, so the
  status needs nothing more for it (Steven's AAC#626 ruling, items 1 and 2 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=bf79bafd5ab90fe17815ab4bae0bd4de8ebdf840a1076a7c30b18382ca9a2875; u603) replaced the four did-not-hold splits with it).
- Disputes, withdrawals, the first final ruling with its date (or the conflicting flag) and
  remedies are in each row's `input`. The witnesses are deal-level (design §4.3, "For the deal").

**Built in PR 4c-2b-2** (`xdealv1.StatusProvider`, `xdealv1.BuildStatus`, vectors
`testdata/deal-v1/status/vectors.json`). It follows A above, and decides these points for review:

- **Where it reads.** It is a `BookStatusProvider` (#525). It reads its own book and the journal
  through the handles the stamp hands it. It reads the counterparty's counted prefix and the case
  book through lock-free readers: `HeldByDeal`, `CountedPrefix` and the received records' `prev`.
  The offer's sent pair comes from the host (`OfferPair`), because the library keeps no sent
  Capsule. Without it, a payment row's check is not run.
- **As of when.** The rows are read as of the latest time the fact states as known. The fact is
  written after each of them, so a window end hanging on a named record is known once that
  record's time is. Reading as of the agreement instead would leave every such window end unknown
  and carry every statement.
- **Ruling times.** A ruling is an input once either side's book names its arrival: a `statement`
  whose `names.ruling` cites it, in the holder's book or the counterparty's counted prefix. It
  takes the earliest such naming: a known time before a stamp, else the earlier time or the lower
  stamp. So the side a ruling goes against cannot keep it off its own status by not naming it
  while the winner names it (Sherlock2's twin, PR 4c-2b-2 review, P1). PROFILE says a receiving
  library MUST name a counted ruling's arrival (#527).
  - **Naming is the receive call's** (#527). `Shelf.Receive` calls the profile's `AfterReceive`
    before it returns, on every call, and x-deal-v1's runs `xdealv1.NameRulings` with
    `Changes.Grew` and the received record's deal (`RecordOutcome.DealID`), so a duplicate retries
    a naming an earlier call failed. That seals,
    in the holder's deal book, a statement naming each counted ruling of the deal's adjudicator
    that the book does not name yet. So a holder's own status reads every ruling it has received,
    and no host can skip it.
    - Each statement is built from a fresh read of the book and of the counterparty's counted
      prefix (`seen`). It is appended at the place it was built for (`Book.AppendAt`), so a write
      in between makes it read again.
    - A book with a record not readable as x-deal-v1 (a withheld payload, a lifecycle record) is
      never written to.
    - **The next stamp retries** (Sherlock2 and the coordinator, #528 round 2). A ruling is often
      a deal's last record, so nothing may arrive to retry a failed naming. The status provider
      is a `BeforeStampProvider`: `Stamp`, and `Tick` when a stamp is due, call its
      `BeforeStamp` before taking the shelf's lock, holding the receive lock so no `Receive`
      moves the counterparty's counted prefix between its read and its append (Codex, #528
      round 3), and it runs `xdealv1.NameAllRulings` over
      every v1 deal book. So the stamp whose values would read a ruling unnamed names it first,
      and the guarantee does not depend on the host. Its error does not stop the stamp
      (`TickResult.BeforeStamp`).
    - A book with a record that is not x-deal-v1 (a lifecycle record a retention change
      appends) cannot take a statement at its seq, so it is never named, only counted.
    - Every x-deal-v1 deal-book write, the host's own included, is `BookEntry` appended with
      `Book.AppendAt` and rebuilt on `ErrBusy`, so a naming and a host write never take one place.
  - **`rulings_unnamed`** (coordinator, #528 round 2). The status value counts the counted
    rulings of the deal's adjudicator the holder holds that neither book names (a count, no ids;
    left out at zero). The provider computes it beside the case book over the book's v1 prefix. A
    nonzero count at a stamp is a naming that failed again just before it, or a book that is not
    wholly x-deal-v1.
  - **Caveat** (Sherlock2, PR 4c-2b-2 and #528 reviews). A ruling neither side has named is not an
    input: its arrival has no time on the holder's shelf, whose stamps cover only its own book and
    the counterparty's. That remains only where an implementation's receive does not name it, or
    where a naming failed and no stamp or `Receive` has named it since, or the book is not wholly
    x-deal-v1, and only while the counterparty names it neither. An `open` outcome
    may then hide an unnamed ruling; `rulings_unnamed` says one is held, and PR 5's card shows it
    beside the outcome rather than present the dispute as unresolved.
- **Every case book.** One book per case (3.2), so the provider reads every log of the
  adjudicator held for the deal. Each counted prefix is walked from position 1, one held follower
  at a time, and the walk stops at a counted record not held for the deal: another deal's in the
  same log, one received after the held records were read, or one forgotten under retention.
- **A forked case book.** Each held branch of the adjudicator's log, from position 1 to a record
  no other held record follows, applies its own first final ruling per row, as rowstatus does. A
  row conflicts when two branches both have one and their decisions differ: `value`, `against`
  or `remedy` (Codex, the same review). A later final after a common first one does not conflict.
- **seen_in_time is recomputed.** A dispute's `seen_in_time` rests on times the fact may not
  know. So a reader recomputes it once those times resolve (`xdealv1.ResolveRow`): the claimant's
  book named the other side's standing statement (its `statement_named_by_other`, the first such
  naming) at a time not after the row's window end. The value the fact states stands while the
  window end is unknown (Codex, the same review, P1; vector `seen-in-time-resolves`).
- **The payment check on the row.** `check` is `PaymentCheck`'s pass, fail or empty.
  `check_reason` says why the check failed or did not run. `check_excluded` is how many settle
  records it left out, never which: their digests are record digests, which the status value
  never holds (no shared identifier). The digests the check ran over go in the link value's
  `checked_over` (8.2).
- **`counterparty_kind`** is `null` until the counterparty's profile record feeds it. No v1 record
  carries SH §7's class yet.
- **The u611 marking.** A deal whose counterparty runs no layer has `counterparty_layer` false and
  `counterparty_proof_held` false. Counted from the holder's receipt only, that is what the
  receipt and the card read to say the other side sent no stamp proof (PR 5).
- **One dispute record is one entry** (Steven's #95 ruling, as Sherlock2 read it on
  evidencebook-deal#224). A contest is the `dispute` record itself, with no `rebuts` link or
  contest verb. `disputes` and the row's rowstatus input each carry it once
  (`TestBuildStatusAppendixA`).
- **A shared status shows earlier statements** (the twin's product note on 4c). The status
  opening goes to the counterparty and a reader the holder chooses (8.2). It carries the holder's
  statements that A keeps, earlier ones included, so that reader sees them.

### 8.2 StatusProvider: the link value

The link value is the library's `LinkValue` (`shelf_facts.go:58-69`; Forms §4.2):

- `last_received`: `{"log_id": <the counterparty's book log id>, "size": CountedPrefix.Size, "record_digest":
  CountedPrefix.Last}` for the counterparty's deal book, or `null` when the counted prefix is
  empty. `size` is a record count, as the library's own test builds it
  (`shelf_receive_test.go:726-727`). Forms §1 defines "size" as an MMR size unless a rule says
  entries, and Forms §4.2 says "the size of that log at it". See section 13, item 5.
- `checked_over`: the record ids each recomputed check ran over, sorted, each once (Forms §4.2).

Audiences follow `OpeningsFor` (`shelf_facts.go:333-365`): the link opening to the counterparty,
the adjudicator and an allowed asker; the status opening to the counterparty and a reader the
holder chooses. A stamp proof never carries a link opening (`shelf_stamp_proof.go:126-129`; Forms
§8). Design §4.2's stamp-proof message lists `link_opening?`; section 13, item 2.

### 8.3 ReceiveProfile

`xdealv1.ReceiveProfile` implements the four methods of `shelf_receive.go:47-70`:

- **`Read`** takes either a deal-book record (the bundle record form: Capsule, signature and key
  id, with its payload) or a sent journal record (the standalone Capsule of 3.1). It verifies the
  signature, decodes the payload strictly as `x-deal-v1`, runs the record checks, and returns:
  - a deal-book record: `Digest` = its record id; `SenderKey` = its key; `LogID` = the header's
    book log id (the sender's own opaque id, 3.1); `Position` = `seq`; `Prev` = its header's one
    `cites` target ("" at `seq` 1); `DealID` = `deal_id`; `Offer` false;
  - a sent journal record: `LogID` = `x-deal-v1/sent/<record digest>`, `Position` 1, `Prev` "".
    That one-record log hides the journal position (3.1). `Offer` is true for a `propose`;
    `DealID` = its `deal_id`;
  - a `ruling`: `LogID` = the case book's log id, `Position` = its `seq`.
- **`Admit`**, the push rule as the library states it (`shelf_receive.go:51-56`: "any other record
  only from a key a book on the shelf names as its counterparty or adjudicator, whose offer our side
  answered, or to which an offer in the journal was sent"). Forms SF-6 says that rule is not yet
  ruled. Its inputs from v1 records:
  - a `propose` from any key;
  - a `decline` only of a `propose` in our journal (an offer or a counter-offer), matched by the
    record digest it cites, of the same deal, from the key that `propose` was sent `to`; a
    `cancellation` only of a held `propose` of the same deal, from that offer's signer (PR 4b
    review round 1);
  - **the acceptor's first records, at an offerer that holds only its offer** (review round 1): a
    deal-book record of a book whose `seq` 1 is an `agree` of kind `agreement` that cites, by
    record digest, a `propose` in our journal, and whose signer is that offer's `to` key. The
    store holds the record part before the proof part (`shelf_receive.go:270-279`), and Admit is
    asked about the agreement itself, so the profile checks the agreement's `offer` and signer
    there. This is the library's "to which an offer in the journal was sent" branch;
  - any other deal-book record only from the `counterparty_key` of our agreement for that
    `deal_id`, and only on a book that answers the same offer;
  - every deal-book record, at any position, only on a log whose first record from that sender
    (`HeldFirst`, or the record itself at position 1) is that sender's agreement of this deal,
    citing the offer the deal answers: our agreement's `offer`, or, at an offerer holding only
    its offer, a `propose` in our journal sent `to` that sender. A sender cannot open a log
    without an admitted agreement and push records on it (PR 4b review round 1);
  - a `ruling` only from our agreement's `adjudicator.key_id`.
- **`Answered(offer)`** (review round 1). The store hands the offer as held, with its record id
  (`shelf_receive.go:57-64`). The profile reads it (`ReceivedRecordOf`), takes its record digest
  (3.1), and answers true when any of these cites that digest: an `agree` of kind `agreement`
  opening one of our **deal books** (an agreement is sealed in the deal book, 3.2, not the
  journal); an `agree` of kind `decline` in our **journal**; or a `propose` in our journal whose
  `counter_to` cites it. An offer we agreed to therefore stays answered and is never dropped by
  the offer bounds (`shelf_receive.go:735-747`), so the terms its agreement cites stay held.
- **`ProofSender(checkpointKeyID, logID)`** (review rounds 1 and 2). A proof is the
  counterparty's, so it is matched against the counterparty's agreement, never against ours. It
  never returns the agreement's `counterparty_key`, which names our own key. The library's proof
  check then reads the records held under the returned key and log
  (`shelf_stamp_proof.go:222-229`, `293-305`).
  The store keeps a log id per sender, not across senders: its index is `(sender_key, log_id,
  position)` and nothing makes a log id unique across senders (`store_sqlite.go:106-117`). The
  holder's random log ids constrain only its own books, never what another key sends. So the
  profile keeps the sender dimension. PR 1 adds a lock-free reader,
  `Shelf.HeldFirst(ctx, logID) ([]HeldRecord, error)`, that returns **every** held record at
  position 1 of a log with that id: across senders, and both records of a fork at position 1 from
  one sender, which the store keeps (`shelf_receive.go:571-602`; no uniqueness on sender, log and
  position, `store_sqlite.go:106-117`). First, before any filtering, the profile refuses every
  sender whose log holds two different records at position 1 (a fork at the genesis): such a
  sender's proofs are refused whatever their `checkpoint_key`. Then it keeps a candidate only when
  all of these hold: it is a v1 `agree` of kind `agreement`; its `checkpoint_key` is `checkpointKeyID`;
  its `deal_id` is a deal of ours (we hold its offer in our journal, or our own agreement for it);
  and its signer is the key that deal expects (our offer's `to`, or our agreement's
  `counterparty_key`). With exactly one candidate left, it returns that candidate's signer and `ok`
  true. With none, or more than one (two senders, or two forked agreements of one sender), it
  returns `ok` false, and the proof is refused as from a sender that may not push it
  (`shelf_stamp_proof.go:222-227`). It never picks one candidate arbitrarily. Vectors
  `shared-log-id` and `fork-at-position-1` (7.3).

## 9. The sealed deal report

Decided: a receipt page "is drawn from the computed status and is not a record" (design §1,
"Receipt", line 75 of the export). The coordinator asked on 2026-11-17 that the receipt pages
(capsule-viewer#322, actionstate-muse#378) not port rowstatus, and instead read one sealed
`deal_report` that lists each row with its computed status, so a page only displays it.

What exists: v0's report is a Capsule with `action_id` `capsulectl-deal-report`, payload
`{"type": "deal_report", "profile", "deal_id", "audience", "nonce", "report"}`, plus
`deal_checkpoint` for a standalone report and `referee` on an adjudicator's copy (`PROFILE.md:122-160`;
`names.go:89-103`; `report.go:18-89`). capsule-viewer reads the `report` body's members, and the
deal's state from `lifecycle.state` alone (capsule-viewer `src/capsule_viewer/receipt/model.py:413-440`
at `99d61f4`, `state` at line 425). v0's report stays unchanged.

**Built in PR 5** (`xdealv1.BuildReport`, `xdealv1.StatusProvider.Report`, `xdealv1.ReportPayload`;
PROFILE §10). It is as proposed below, with these additions and limits:

- **Card members** (Sherlock2, #526 and #528 reviews; the coordinator made them PR 5's acceptance).
  `rests_on.counterparty_kind` (the status value's, null: a page shows "not stated");
  `rests_on.counterparty_proof`, one of `held`, `not_yet` and `no_layer`, reading the layer and the
  held proof together (u611's no-layer marking); `disputes`, the status value's one entry per
  dispute record; `rulings_unnamed` (#527), shown as "ruling held, unnamed" beside an `open`
  outcome, absent at zero; and `status_fact`, null before the book's first status fact, else
  `{"current": bool}`, whether that fact's status value is the one computed now (false after a
  stamp left the book in `TickResult.Unstated`, or after a change no stamp has stated yet). An
  adjudicator's copy carries neither `disputes` nor `rulings_unnamed`.
- **`late`** is present: rowstatus's `ComputeLate` (Q9) landed in PR 1a (#484).
- **Times.** A report reads each record's time as a stamp's status value does; a time only a
  stamp of the holder's gives, with no witness receipt yet, is no time, so its rows are `local`.
  `relied_on.witnessed` is true exactly when a time is given. `BuildReport` refuses books holding
  a record timed after `as_of`, so disputes, rulings and `relied_on` read what was held then
  (Codex and the stand-in, #530 review).
- **The deal checkpoint** is the holder's book's latest checkpoint, which must cover the whole
  book (a stamp cuts it). `Report` writes nothing: it reads the journal with
  `Shelf.ExistingJournal`, which never creates it, and reads again, up to three times, when the
  book moves during the read. A book holding a record not readable as x-deal-v1 is refused for
  good, not as busy (Codex and the stand-in, #530 review).
- **`relied_on`** includes the remedy record `remedy_made` rests on.
- **A payment remedy is not counted** (Sherlock2, the twin and the coordinator, #530 review).
  RowInputs counts no remedy that is a payment (6.1): it needs its own terms leg, which the
  profile does not define. The report must never state a breach its records contradict, so a
  ruled row whose ruling orders a payment has `remedy` `not_counted`, never `due` or
  `not_honoured`, and keeps the deal unsettled. Its `remedy_made` says whether the owing side's
  book holds a remedy for the row. Appendix A's refund reads `not_counted` with `remedy_made`
  true from 25 Nov. Defining the remedy's terms leg is Sherlock2's and Steven's call; the
  design's "26 Nov, remedy made, settled" vector waits for it. The two refund paths differ on
  purpose: a refund after a concession settles its row on `remedy_made`, since nothing ordered an
  amount or a date; a ruled refund stays `not_counted`, since the ruling sets an amount and a date
  the report cannot check yet.
- **Vectors built** (`testdata/deal-v1/report/`): Appendix A's buyer at each time above, the
  books as held then; its counterparty and adjudicator copies; a not-started book (the seller
  holding its own agreement only); an agree-then-silent book (#511: the offerer's agreement untimed
  and never named, T after `agree_by`: started, local, rows listed); an untimed own book; a counterparty without the layer; the
  settled-without-ruling path (lines s1 to s12: the seller concedes row 3 not met and refunds
  with no ruling, so the row is `agreed` `not_met` with `remedy_made`, and the deal is settled);
  the same concession without the refund (`agreed` `not_met`, not settled); an empty witness
  list (no record has a time, so the rows that rest on statements are `local`); and a ruling held
  unnamed with a status fact that is not current.
  `testdata/deal-v1/settled/` holds the `settled` cases.
- **Not here:** the `x-deal-v1` bundle extension's `sealed_report` pointer and the sealing call
  are the host's (evidencebook-deal#212); `report/` (`capsulectl book report`) refuses a bundle
  carrying the `x-deal-v1` extension and points to this report (#485).

**Proposed: the v1 report.**

- **Type and Capsule.** The same Capsule form and names (`ReportActionID`, `ReportType`
  `deal_report`), with payload `profile` `"x-deal-v1"`.
- **How a page finds it.** A v1 bundle carries the extension `x-deal-v1` (the record version's
  name, 2.3), whose `sealed_report` member names the report Capsule, as a v0 bundle's `x-deal-v0`
  extension does (capsule-viewer `src/capsule_viewer/receipt/model.py:41,485-489` at `99d61f4`).
  A standalone report travels in the same carrier extension as v0's, `x-deal-report/v0`
  (`names.go:99-102`): that name is a carrier, not a profile version, and the report's own
  `profile` member says which version it is. A page reads `x-deal-v1`, never `x-deal-v0`, for a
  v1 deal. A v1
  report is always standalone: it is appended to no book. So it adds no fact, changes no book
  checkpoint, and shows no one when or how often the deal was shared (`PROFILE.md:141-145`). It is
  not one of the ten record types and is not a step.
- **Not a record anything depends on.** No status input reads a report: not rowstatus, not
  `Start`, not the status value, not a card. `Receive` refuses a report (its `Read` finds no deal
  record), and a report is never cited by `seen`, `refs` or a fact. It is a snapshot the holder
  signs, of what it computed at T from what it held.
- **Payload.**

```json
{"type": "deal_report", "profile": "x-deal-v1", "deal_id": "deal-…",
 "audience": "keep" | "counterparty" | "adjudicator", "nonce": "<64 hex>",
 "referee": "<adjudicator's copy only>",
 "as_of": "<T, RFC 3339 UTC to the second>",
 "deal_checkpoint": {"log_id": "<the holder's book log id>", "mmr_size": 7, "root": "<hex>"},
 "pack_digest": "<the deal terms pack's digest, as the agreement cites it>",
 "start": {"state": "started" | "awaiting_agreement" | "not_started", "local": false,
           "by": "offerer_in_time" | "acceptor_named" | "first_act" | null},
 "settled": false,
 "rows_state": "listed" | "not_started" | "withheld",
 "rows": [ ... one per agreed row, in agreement order, when rows_state is "listed" ... ],
 "rests_on": {
   "own": {"log_id": "<the holder's book log id>", "size": 7, "last": "<record id>"},
   "counterparty": {"log_id": "<the counterparty's book log id>", "counted": 6, "last": "<record id>",
                    "fork_at": 0} | null,
   "counterparty_layer": true,
   "witnesses": [ ... as the agreement names them ... ]},
 "report": { ... the producer's text, as v0's ... }}
```

- **One row:**

```json
{"id": "as_described", "owed_by": "seller", "owed": { ... as the agreed row's owed ... },
 "due": "2026-11-04T09:12:00Z" | "not_set",
 "window_end": "2026-11-18T09:12:00Z" | "not_set",
 "status": "<rowstatus status word>", "value": "met" | "not_met" | null,
 "remedy": "none" | "due" | "made" | "not_honoured" | "not_counted" | null,
 "local": false,
 "check": "passed" | "failed" | "not_run" | "not_recomputed",
 "late": {"statements": [{"side": "seller", "value": "met", "record": "<record id>"}],
          "disputes": [{"side": "buyer", "record": "<record id>"}]},
 "ruling": {"record": "<record id>", "value": "not_met", "against": "seller",
            "final": true, "remedy_due": "2026-11-27T23:59:59Z"} | null,
 "relied_on": [{"record": "<record id>", "side": "buyer",
                "time": "<RFC 3339>" | null, "witnessed": true}]}
```

  - `status`, `value`, `remedy`, `local`, `due` and `window_end` are `rowstatus.Result` as
    `Compute` returns it at `as_of` (`rowstatus.go:178-192`), with an empty date written
    `"not_set"`; the one exception is `remedy` on a ruled row whose ruling orders a payment,
    which `BuildReport` overwrites with `not_counted` (`report.go:424-427`), a word rowstatus
    never returns.
  - `check` is 6.3's result: `not_recomputed` for a row the terms do not mark recomputed.
  - `late`: rowstatus does not report late statements or late disputes (its `Result` has six
    members, `rowstatus.go:180-192`). **Decided (coordinator, Q9):** an additive rowstatus output, `late_statements`
    and `late_disputes` (indices into the input), computed by the same rule in Go, Rust and Python
    with new vectors, and no change to any existing result (decided, Q9). Until it lands, `late` is
    absent.
  - `ruling` is the first final ruling rowstatus applied, or `null`.
  - `relied_on` lists each record the row's inputs came from, with its record time (4.3) and
    whether that time came from a named witness.
- `rests_on` says what the whole report rests on: the holder's book at the `deal_checkpoint` it is
  made at, and the counterparty's counted prefix it used (`CountedPrefix`,
  `shelf_receive.go:1121-1136`), including a fork's position.
- **`settled`** (review round 1). It is true when all of these hold at `as_of`: the deal has
  started (`start.state` is `started`); and every row that is not `not_reached` is `agreed`,
  `uncontested`, or `ruled` with remedy `none` (the final ruling ordered nothing) or `made`. An
  `agreed` row whose value is `not_met` counts only with a remedy made (`remedy_made`): under
  Steven's SH-16 ruling (Q5) the remedy is what makes a conceded row remedied (Sherlock2, #530
  review). A
  `not_reached` row is left out of settlement, as the design says (on the card it is `undecided`,
  Q12, answered) ("it is left out when the deal is settled",
  design §4.2, "A latest date"; line 372 of the export). A row `ruled` with remedy `due` or
  `not_honoured` keeps the deal unsettled. A started deal whose rows are all `not_reached` is
  settled. The same definition decides when `rules_verdict` may be sealed (3.3), and `settled`
  says nothing about a card's line for a `not_reached` row, which is Q12. Vectors
  (`testdata/deal-v1/settled/`): a not-started deal (false); one `not_reached` row and the rest
  agreed (true); `ruled` with remedy `none` (true), `due` (false), `made` (true) and
  `not_honoured` (false); every row `not_reached` (true).
- **Row text.** A row carries its `id` and its agreed `owed`, not free text. A page names a row
  by the deal terms pack whose digest `pack_digest` gives (design §4.2, "The row id": the pack's
  author gives each template its id and text); a page that does not hold the pack shows the id.
  So the report keeps no second copy of the terms that could disagree with the pack.
- **`rows_state`.** `listed`: `rows` holds every agreed row. `not_started`: the deal has not
  started (`start.state` is not `started`) and `rows` is empty. `withheld`: this audience may not
  have row statuses and `rows` is empty. Proposed, following the status opening's audiences
  (shelf note §2.2; AAC `spec/shelf-forms-v0.md` §4.3): `keep` and `counterparty` get `listed`;
  `adjudicator` gets `withheld`, since an adjudicator computes from both books it is handed, not
  from a party's statement of the result. Sherlock2 confirmed this (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=06ba99c88913ee2777e51d5581f45cfc9af1641452e52907d04577e560d5666e&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6, relayed by the
  coordinator on 2026-11-17): SF §4.3 and #461 acceptance 2 are right. The adjudicator gets the
  link opening by right, and the status opening only if the holder chooses (Forms §4.3: the
  adjudicator's row is "yes" for the link opening and "no" for the status opening;
  `OpeningsFor`, `shelf_facts.go:345-346`), so its report copy withholds the rows.
- **What each audience's copy holds** (proposed; the door question, Q11). Statements, disputes and
  rulings are both sides' own records, so a counterparty's copy shows it nothing it does not hold.
  What is new is what says what the holder holds: `relied_on` (each record's time and whether it
  is witnessed), `check`, and `rests_on` (how much of the other side's book the holder counted,
  and `fork_at`).

  | Member | `keep` | `counterparty` | `adjudicator` |
  |---|---|---|---|
  | `start`, `settled`, `as_of`, `deal_checkpoint`, `pack_digest` | yes | yes | yes |
  | `rows` (status, value, remedy, dates, `owed`, `ruling`, `late`) | yes | yes | no (`rows_state` `withheld`) |
  | row `check`, row `relied_on` | yes | yes | no |
  | `rests_on.own` | yes | yes | yes |
  | `rests_on.counterparty`, `rests_on.witnesses` | yes | yes | no |

  A copy for anyone else (a third party the holder shows a receipt page to) is the holder's own
  share decision, as for v0 (capsule-viewer README: what a relying party gets is the share
  builder's decision); the profile defines no fourth audience.
- **No rules verdict in the report.** The verdict word stays a record in the party's own book,
  never a member a report adds (u195 condition 3); the payload has no such member.
- **The deal-level line.** A v1 report's `report` text body has no `lifecycle` member, and a page
  never reads `report.lifecycle.state` for a v1 deal. Its deal-level line is `start.state`, and,
  for a started deal, `settled`: true shows "settled", false shows nothing (a v1 deal is never
  closed by a record, design §4.2). A v0 report keeps its close and its `lifecycle`.
- **`as_of`** is the time T at which the holder computed every status in the report, from what it
  held then; it is not a time any witness attests.
- **`late`** is optional: absent until the additive rowstatus output lands (Q9, decided), and a
  page shows nothing for it while absent.
- **The closed word sets**, every word a page has to word (a wording pack covers exactly these):

  | Member | Words |
  |---|---|
  | `start.state` | `started`, `awaiting_agreement`, `not_started` |
  | `start.by` | `offerer_in_time`, `acceptor_named`, `first_act`, or `null` |
  | `rows_state` | `listed`, `not_started`, `withheld` |
  | row `status` | rowstatus's ten (`rowstatus.go:56-66`): `not_reached`, `not_due`, `no_statement`, `waiting`, `uncontested`, `agreed_so_far`, `agreed`, `in_disagreement`, `in_dispute`, `ruled` |
  | row `value` | `met`, `not_met`, or `null` (`cannot_tell` is never a value: rowstatus counts it as no statement) |
  | row `remedy` | `none`, `due`, `made`, `not_honoured`, `not_counted` (a ruled payment remedy, PR 5), or `null` for a row that is not ruled |
  | row `check` | `passed`, `failed`, `not_run`, `not_recomputed` |
  | row `due`, `window_end` | an RFC 3339 UTC time, or `not_set` |
  | `audience` | `keep`, `counterparty`, `adjudicator` |
  | `rests_on.counterparty_proof` | `held`, `not_yet`, `no_layer` (PR 5) |
  | row `ruling.against` | `buyer`, `seller`, or `null` |
  | booleans | `local`, `witnessed`, `settled`, `final`, `counterparty_layer`, `remedy_made`, `status_fact.current` |

- A page shows `rows` and `start` as they are. It never recomputes them.
- **Verifying a report** (`xdealv1.VerifyReport`), so every reader of a report applies one rule
  (capsule-viewer#322, evidencebook-deal#212).
  A reader checks a report against its deal's first record: the record at seq 1 of the holder's
  deal book, as a book pair. The report travels as a sent pair does, `{"record": the report
  Capsule with its signature and key_id, "agent_input": the payload}`, with exactly these two
  members, named exactly so. It is refused unless:
  - its Capsule verifies under its `key_id`, is a report Capsule (`capsulectl-deal-report`,
    `fyi`) and commits the payload as its `agent_input_digest`;
  - the payload is a `deal_report` of `x-deal-v1` that parses as `Report` and would encode to the
    same bytes, with no null where a report holds a list or an object (`rows`, `report`, and each
    row's `relied_on` and `late` lists), each row's `owed` an object, no negative
    `rulings_unnamed`, and each `relied_on` record `witnessed` exactly when it has a `time`;
  - each row's `ruling.against` is `buyer`, `seller` or `null`;
  - the first record is a verified book pair at seq 1 holding an agreement of the report's
    `deal_id`, in the log named by `rests_on.own.log_id`;
  - the report is signed by the key that signed that first record. In x-deal-v1 a deal book has
    one signing key: the counterparty admits a deal-book record only from the key its agreement
    names (§8.3), so the counterparty stops receiving a book whose key changed.

  A report that passes shows who signed it and which deal it is about, not that its row statuses
  are correct: it is the holder's own snapshot (PROFILE §10), and a reader who needs the status
  recomputes it from the books. The vectors are in `testdata/deal-v1/report-verify/`.

**Vectors** (`testdata/deal-v1/report/`), each from Appendix A's two books, at fixed nonces:
T = 1 Nov after line 22 (all not due), 3 Nov (row 1 agreed so far), 9 Nov (row 2 agreed, row 3
waiting), 12 Nov (row 3 in dispute), 21 Nov (ruled, remedy due), 26 Nov (ruled, remedy made,
settled), and 28 Nov on a variant with no refund (remedy not honoured); the settled-without-ruling
path of Appendix A (lines s1 to s12); a deal with an empty witness list (every row local); a
not-started book (empty `rows`); and the counterparty-without-layer book.

## 10. standard_history

- **`/1` and `/2`: no change.** Their inputs stay v0's (`standard_history.go:61-91`). They do not
  see v1 deals (2.5).
- **What `/3` needs from this profile** (AAC#620 defines `/3`; this is only the list of inputs). All
  of it comes from each v1 book's latest fact's status opening (8.1), as of the anchor:
  - the start state inputs, so a not-started book is in no window and no tally (acceptance 5);
  - `counterparty_layer` and `counterparty_proof_held`, for how it happened (stamped by both, our
    receipt only, no record);
  - `witnesses`, and each record's time from those witnesses only, so a started deal whose records have no
    such time is counted, local, whether or not its agreement names a witness (u616, u608, #570;
    AAC `/3` SH-12 says otherwise for a named witness until AAC#634 lands);
  - `role`, `value_band`, `counterparty_kind`;
  - `agreed_at`, and the first witnessed time of the book's first fact, since design §4.1,
    "Counting only a window", proposes the latter as the window key in place of the time of
    agreement (not adopted: `/3` keeps `agreed_at`, AAC#626 SH-13);
  - per row, `owed_by` and the rowstatus inputs, so each row's status at the anchor's time gives
    the card's line, and only rows the holder owed can make the deal `did_not_hold` (Steven's AAC#626 ruling, items 1 and 2 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=bf79bafd5ab90fe17815ab4bae0bd4de8ebdf840a1076a7c30b18382ca9a2875; u603));
  - `disputes` (8.1), for `/3`'s `{"side", "outcome", "forum"}`;
  - the profile name `x-deal-v1`, so a `/3` reader knows the status schema.
- **How the profile's words map to `/3`'s** (stated once, from Sherlock2's review, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=dab78e4530acd8dc56936d22778547b3cfce4ef95b62a3beca0b66fffc5e66c9&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6).
  AAC#626 (`spec/standard-history-v0.md` at `46c4cd3`, §11.2) writes a row's `owed_by` as `holder`
  or `counterparty`, and §5.4 a dispute's `side` as `raised` or `received`. This profile writes
  roles. For the shelf whose fact it is, with `role` the status value's `role`:
  - row `owed_by`: `holder` when the profile row's `owed_by` equals `role`, else `counterparty`;
  - dispute `side`: `raised` when the dispute's `claimant` equals `role`, else `received`;
  - dispute `outcome`: `withdrawn` as is; `for_claimant` is `for` when raised and `against` when
    received; `against_claimant` is `against` when raised and `for` when received; `no_finding` as
    is; `open` as is;
  - `/3`'s `role` (`provided` or `bought`): `provided` for `seller`, `bought` for `buyer`, as v0
    maps it (`standard_history.go:30-31`);
  - `/3`'s `deal` member (a non-empty deal reference, §5.2): the book's `book_id`, never the deal
    id, which the status value does not carry (8.1).
- **Vectors.** The settled-without-ruling path of Appendix A (lines s1 to s12) gives, from each
  side's latest facts at 1 Dec, the full expected row results and lines under AAC#626's card rules:
  the seller's deal has row 3 `agreed` `not_met`, owed by the holder and not `ruled`, with the
  refund made, so it is `did_not_hold`, counted in `remedied` (Q5, answered: a refund without a
  ruling counts as remedied). The status value carries that a remedy was made on an unruled row
  (`remedy_made`, 8.1), so a reader of facts can tell. The buyer's deal, whose
  failed row the counterparty owed, is not `did_not_hold` (Steven's AAC#626 ruling, items 1 and 2 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=bf79bafd5ab90fe17815ab4bae0bd4de8ebdf840a1076a7c30b18382ca9a2875; u603)); where it lands on the card is
  AAC#626's text. The ruled path (Appendix A lines 43 to 76) gives `did_not_hold`, counted in
  `remedied`, for the seller, and the dispute entries `received` and `against` for the seller,
  `raised` and `for` for the buyer.

## 11. Order of work, and conformance

### 11.1 Pull requests

Each lands alone and keeps the shelf interlock, `row-status-diff` and the `deal-profile` job green
(`ci.yml:100-143`, `540-543`). Go comes first with the vectors. Rust and Python then follow the
same vectors, each in its own pull request.

1. **Library, format-free** (Go, then Rust and Python): the record time of a counterparty
   position covered by the holder's own facts (4.3); and, once AAC#621 fixes its form, receiving
   the witnessed follow-up (receipts and the shelf consistency proof) and the record time it
   gives. Steven picked key-id naming (Q1, answered), so `RecordTime` also takes witnesses by key
   id, reading `shelf_receipts.key_id` (`461-shelf-facts.md:117-125`). The additive rowstatus `late_*`
   outputs land here too (Q9, decided), and `Shelf.SealSent`, which seals a sent journal record
   and its standalone Capsule in one call (3.1). Lock-free readers the profile calls from a
   stamp or a `Receive`: one that lets the profile re-check every held proof's bindings against the
   current counted prefix (gate B, 7.2), and `Shelf.HeldFirst(ctx, logID)`, every held record at
   position 1 of a received log with that id (8.3). `Handover` and `VerifyHandover` meet invariant C
   (8.1). Acceptance: vectors `genesis-fork-after-proof`, `wrong-shelf-then-restart`, `restart`,
   `shared-log-id`, `fork-at-position-1` (with PR 4) and `earliest-receipt-not-anchor`.
2. **`dealprofile/x-deal-v1/`**: `PROFILE.md`, `x-deal-v1.schema.json`, `check_profile.py` (v1), and
   fixtures: Appendix A's two books as positive fixtures, with negatives for each record check of
   section 3. **`dealprofile/xdealv1`**: names, types, Encode and Decode, the record checks,
   `VerifySent` with the decline and cancellation vectors (3.5), and `dealprofile.Version` (2.3).
   Its record checks meet invariant D (3.2). Acceptance: vectors `approval-migration` (REQUIRED)
   and `deal-id-presence` (3.1).
3. **`Start`, `StartFromBooks`** and the start vectors (5.4).
4. **The status value, the link value, `StatusProvider`, `ReceiveProfile`, `PaymentCheck`,
   `CanAct`**, with the order vectors (7.3), acceptance 3's two remaining vectors (the other side's
   stamp covering our record first; a statement whose in-time state is known only at its first
   stamp), and status vectors. The status value meets invariant A (8.1) with the holder's stamps;
   a counterparty record's earlier time from its author's stamp waits for receipts to travel with
   proofs (Q8). `CanAct` gate B
   (7.2). Acceptance: vectors `times-three-cases`, `unwitnessed-then-witnessed`, `carry-forward`,
   `carry-forward-lower-bound` (REQUIRED), and the order vectors of 7.3.
5. **The v1 report** and its vectors (section 9).
6. **Rust `deal::v1`**, then **Python `evidencebook.deal.v1`**, to the same vectors, with a CI
   differential job like `row-status-diff`.

Outside this repository, each already filed (#461 comment 6940058546):
evidencebook-deal#212 moves the plugin onto v1 (journal checks, records sent when sealed, no
results log and no close comparison); AAC#620 (`/3`); AAC#621 (forms); capsule-viewer#322 and
actionstate-muse#378 (pages that read the v1 report).

### 11.2 Conformance

- **Records.** Every positive fixture's record digest, in Go, Rust and Python, equals
  `fixtures/expected-digests.txt`. Every negative fails at its named stage. `check_profile.py` (v1)
  passes the chains Go, Rust and Python build, as the v0 job does (`ci.yml:100-143`).
- **Version.** `Version` returns `x-deal-v0` for every v0 fixture, the typed ones in
  `fixtures/positive-typed/` and `fixtures/positive-typed-seller/` included, and `x-deal-v1` for
  every v1 fixture. It refuses a record with two blocks, a block and a typed `type`, or neither.
  Every v0 fixture, typed ones included, still decodes and re-encodes to the same bytes
  (`TestRecordRoundTrip`), and `StandardHistoryDeal` gives the same entries for them as before.
- **Agreement checks** (review round 1). Refused: an agreement with no `admit`; an offerer's
  agreement with `counterparty_layer: true` whose `seen` is not the acceptor's agreement. Accepted:
  an agreement with `counterparty_layer: false` and `seen: null`, carried through Encode, Decode,
  the record checks and `StartFromBooks` to the no-layer start results of 5.4.
- **Projection** (review round 1). Each Appendix A row, projected as 4.2 says, is accepted by
  `rowstatus.ParseInput` in Go, Rust and Python and computes the same result in all three; the
  unprojected profile row is refused by `ParseInput`.
- **No pairing across shelves** (review rounds 1 and 2; scoped to identifiers). For Appendix A, no
  identifier of one side's deal book appears in the other side's facts or status openings: no log
  id, `book_id`, record id or record digest, and no deal id, offer digest or rows digest in either.
  The two sides' deal-book log ids differ. The shared content 8.1 names (times, dates, roles, the
  witness list, the adjudicator's key and rules digest, the pack's row ids) is allowed to appear on
  both sides and is not checked.
- **Status value (A; PR 4).** `times-three-cases`, `unwitnessed-then-witnessed`, `carry-forward`
  and **`carry-forward-lower-bound`** (REQUIRED, review round 3), as 8.1 lists them. For each,
  `Compute` over the statements the fact carries equals `Compute` over all the holder's statements
  at every T.
- **Coverage gate (B; PR 1 and PR 4).** The vectors of 7.3, including **`genesis-fork-after-proof`**
  (REQUIRED, review round 3) and **`fork-at-position-1`** (REQUIRED, review round 3).
- **Handover receipts (C; PR 1).** **`earliest-receipt-not-anchor`** (REQUIRED, review round 3), as
  8.1 lists it.
- **Authorization (D; PR 2).** **`approval-migration`** (REQUIRED, review round 3), as 3.2 lists it.
- **`deal_id` presence (PR 2; review round 3).** `deal-id-presence`: a deal-book record, a
  `propose`, a decline, a cancellation, a journal `check` and an `approval` answering a verdict
  without `deal_id` are refused; an `intent`, `admit`, `sale`, `thread`, `counterparty_profile`,
  `disclosure`, a pre-offer `message` and an `approval` on `confirm_limits` (raised before any
  offer) with `deal_id` are refused; each listed record with the presence 3.1 gives is accepted.
- **Effective ruling** (review round 2). With two conflicting final rulings held, the row is
  `in_dispute`, `ruling_against` is `null`, and each dispute's outcome is `open`.
- **Journey overlay placement** (review round 2). `sale`, `thread`, `thread_opened`, `sale_unpost`,
  `counterparty_profile`, `disclosure`, `message`, `claim`, `evidence`, `detail_change` and
  `agent_move` are refused in a deal book, before and after the agreement.
- **Offers on one listing.** Two offers citing the same `listing_digest`; the seller agrees to one and
  sends the other a decline with reason `not_selected`.
- **Answered** (review round 1). With the offer bounds reached, an offer our deal book's agreement
  cites, and one our journal declined, are kept; the oldest unanswered offer is dropped.
- **Remedies, settled, card mapping.** The vectors of 6.1, 9 (`settled`) and 10.
- **Keys.** A v1 fixture's fingerprints and commitments equal those of v0's
  `fingerprint-vectors.json` for the same secret and values.
- **Start.** Section 5.4, in all three languages, byte for byte on the JCS of each result.
- **Status.** For each stamp of Appendix A, each side's status and link values (JCS) are equal in
  all three languages. For every T from the stamp to 1 Jan 2027 in one-hour steps, `Compute` over
  the selected statements equals `Compute` over all of them (8.1).
- **Order.** Section 7.3.
- **Reports.** Section 9's vectors, byte for byte (fixed nonces).
- **Interlock.** One language writes the acceptor's book and another the offerer's. Each receives
  the other's records and proofs through `Receive`, stamps, and dumps its facts, and the dumps
  equal the committed vectors for every pair of languages, as the shelf interlock does today.
- **Loop gate.** No loop step goes from pass to fail or to not checked (#461 acceptance 10).

## 12. Open questions

### 12.1 For Steven

**Q1. How does an agreement name its witnesses?** The library names a witness by its receipt URL,
an opaque string, so one URL is one witness, and it keeps the directory key id each receipt
verified under (`shelf_record_time.go:20-25`; `461-shelf-facts.md:117-125`).

- *Recommendation (Sherlock2's):* name each witness by its verified key id, with the URL only as a
  hint where to find it: `{"key_id": "…", "url": "…"}`. A URL is a locator, and the key is what
  signs the receipt, so a moved witness stays the same witness, and two URLs of one witness are
  not counted as two.
- *The library's engineering default today:* an opaque id, the receipt URL, where one URL is one
  witness. That is how the library stores a witness, not a proposal for how the agreement names
  one; the agreement's rule is Steven's.
- An empty list is allowed under u186; every status is then local.

Key-id naming needs `RecordTime` to filter by key id (11.1, PR 1). URL naming needs no library
change.
**Answered** (Steven, 2026-11-17: "482 Q1-3 recs" (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=9f75d69a01bf5cca16ba4c2123e6d6f4434217cbabb7e83b6993ec9d0c1dc6c5; Sherlock2's recommendations, recorded as u604, ops a86cd2f9)): name each witness by its verified key id, with the URL as a hint:
`{"key_id": "…", "url": "…"}`. The library's `RecordTime` gains naming by key id (11.1, PR 1).

**Steven's one ask for AAC#626.** Q4 and Q12 gated AAC#626's exact-head approval (the `/3` card's
`forum` and `by_forum`, and its line for a `not_reached` row and for a deal with no witnessed time),
and Q5 decided a line of the same card. Steven answered all three on 2026-11-17 (each is marked
**Answered** below). They went to Steven in one ask, with Sherlock2's
recommendations below (Sherlock2, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=dab78e4530acd8dc56936d22778547b3cfce4ef95b62a3beca0b66fffc5e66c9&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6, relayed by the coordinator on 2026-11-17).

**Q2. When is `rules_verdict` sealed now that there is no close?** v0 seals it after the final close
(`PROFILE.md:220`). Proposed: once the holder computes the deal settled (9, `settled`). A
consequence to rule on: a deal that is never settled then never gets a verdict.
*Sherlock2's recommendation:* seal at settled, and a deal that is never settled has no verdict,
since a verdict sealed before settlement would be a close by another name. u195's other conditions are kept as they
are (3.3), condition 1 (shared under the holder's share policy: a node shares by default, a
person does not) and condition 4 among them, and the verdict word stays a record in the party's
own book, never a member of a report (u195 condition 3; section 9).
**Answered** (Steven, 2026-11-17: "482 Q1-3 recs" (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=9f75d69a01bf5cca16ba4c2123e6d6f4434217cbabb7e83b6993ec9d0c1dc6c5; Sherlock2's recommendations, recorded as u604, ops a86cd2f9)): seal at settled; a deal that is never settled has no verdict.

**Q3. The intent, the check before an act, its verdict and the approval.** Design §7 says they need "types of
their own or a place in the overlay; no source settles which". Proposed: a journal-only overlay
of four types (`intent`, `check`, the per-check `verdict`, `approval`), reusing v0's bodies and rules (3.2), and the journey overlay placed by phase (3.2). This touches the trim of the public vocabulary that
design §14, row 7, puts to Steven.
**Answered** (Steven, 2026-11-17: "482 Q1-3 recs" (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=9f75d69a01bf5cca16ba4c2123e6d6f4434217cbabb7e83b6993ec9d0c1dc6c5; Sherlock2's recommendations, recorded as u604, ops a86cd2f9)): the journal overlay as proposed, with `listing_digest` optional on an offer (3.3).

**Q4. evidencebook#404 against the ten types.** #404 (sourced to Steven, 2026-11-05) asks for four
kinds: `dispute`, `dispute_response`, `adjudication` and `revocation`, plus a forum enum and a
card count by forum. Design §7 and §8 map recourse onto `dispute` (dispute, withdrawal,
challenge), `ruling`, and a `statement` that names a ruling that arrived. There is no
`dispute_response`, `revocation` or forum. This note follows the design. Which shape stands needs
Steven's word, since #404 flags the record shape as hard to reverse. A consequence for the card:
AAC#620's `/3` table has `dispute_entry` with a `forum` and `disputes.by_forum`, and today's card
counts disputes by forum (AAC SH §4). With no forum in v1, a `/3` card has nothing to fill
`by_forum` with: either v1 keeps a forum on `dispute`, or `/3` drops `by_forum`, which is a card
change.
**Answered** (Steven's AAC#626 ruling, 2026-11-17 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=bf79bafd5ab90fe17815ab4bae0bd4de8ebdf840a1076a7c30b18382ca9a2875; read by Sherlock2 as u603, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=71e5098f96ceeadf8970c251112bdb8f9677084483f5994ffcc5781294410057)): yes, dispute and ruling records carry a forum. A `dispute` of kind `dispute` and a
`ruling` carry `forum` (3.3), one of #404's values: `auto:twin`, `auto:ref`, `network:<id>`,
`rail:<program>` or `external:<class>`. Its prefix is the class `/3` counts in `by_forum`. The spelling
after the colon (`[a-z0-9][a-z0-9._-]{0,63}`) is syntax with no door, as long as `auto` stays closed to
`twin` and `ref` and no list of allowed names comes with it (Sherlock2, and the local twin by session
message, both 2026-11-17: buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=96dbf3c6c35ec3ea041f5f1e33cf91ba5b55739bfef443e2fbe99bf3ddfb68e4&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6). Landed in #487. The
other #404 kinds (`dispute_response`, `revocation`) stay out, as the design maps them.

**Q12. A row that is not reached, and a deal with no witnessed time, on a card.** The design says a
`not_reached` row is "left out" when a card is counted (design §4.2, "Dates that hang on a
record"); this note has no rule for it, and AAC#620's `/3` draft treats it as undecided. And
acceptance 9 says a deal whose records have no time from the named witnesses is "tallied as not
counted", while the design calls such a status "a local result" only. Both need one answer
before v1's vectors pin them. This is one of the questions that gate AAC#626's exact-head approval
(the one ask above).
*Sherlock2's recommendation, which departs from the design's text* (design §4.2: a `not_reached`
row is "left out" when a card is counted; the ask says so): keep `not_reached` as `undecided` on
the card, as AAC#626 §11.3 does,
since "left out" would let a holder drop deals from its card by never timing the named record. The
`settled` rule of 9 still leaves a `not_reached` row out of settlement, as the design says; that
decides when a deal is finished, not its card line.
**Answered** (Steven's AAC#626 ruling, 2026-11-17 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=bf79bafd5ab90fe17815ab4bae0bd4de8ebdf840a1076a7c30b18382ca9a2875; read by Sherlock2 as u603, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=71e5098f96ceeadf8970c251112bdb8f9677084483f5994ffcc5781294410057)): any `not_reached` row makes the deal undecided on the card, replacing the design's
"left out" (item 5). A deal with no time from a witness its agreement names was to stay left
out (acceptance 9); Steven's later ruling, u616 (2026-11-19, "count it"), counts it, rows local. Items 1 and 2 of the same ruling set three card outcomes, `held`, `undecided` and
`did_not_hold`, and take `other_side` out of the did-not-hold split: a deal where only the
counterparty failed is not `did_not_hold` (10). How the card text orders `not_reached` against
`did_not_hold` is AAC#626's.

**Q5. A remedy outside a ruling** (moved here from 12.2 in review round 1). Two cases: a remedy
chosen under the terms (design §4.2, "Remedy rows"), which has no contest window in the design and
no rowstatus input outside a ruling (proposed: a derived row, 4.6); and a remedy made by agreement
after a concession, as in Appendix A's settled-without-ruling path (lines s1 to s12). rowstatus
records a remedy only for a ruled row (`rowstatus.go:138-151`). AAC#626 at its head 361f7b4b now
proposes counting such a remedy as remedied (its SH-16, Steven's decision 3 in that PR's list), so a
seller who refunded as in Appendix A would be `remedied`. The ask is that Steven confirms #626's
proposal; the profile then has to carry what the card needs to see a remedy made outside a ruling
(10). It is part of the one ask above (Sherlock2, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=211abab2ff32fae38d39cde2d38f111088d936a63ae0734fe5b1404657bf041b&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6).
**Answered** (Steven's AAC#626 ruling, 2026-11-17 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=bf79bafd5ab90fe17815ab4bae0bd4de8ebdf840a1076a7c30b18382ca9a2875; read by Sherlock2 as u603, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=71e5098f96ceeadf8970c251112bdb8f9677084483f5994ffcc5781294410057)): yes. A refund without a ruling counts as remedied (#626's SH-16), and the counterparty
answers by disputing the row. The status value carries it as `remedy_made` (8.1), so a
reader of facts sees the remedy; the vectors of 10 pin `remedied`.

**Q11. What a decline and a cancellation disclose to the other side.** This belongs to Sherlock2's
door question. Proposed (3.5): the signer's key, the cited offer's digest, the deal id, one closed
reason token (`not_selected` or `other`), the author's own time `at`, and optionally a committed
free-text reason opened only to that side. It discloses nothing about other offers or the winning
terms, and nothing about its place in the journal. The questions: whether `not_selected` may be
told to the losing buyer at all (Muse's PRD says the copy "says so", `PRD_SELLER.md:303-305`);
whether a reason token is allowed or required; and whether the buyer may show the copy to a third
party, since that copy proves the seller's key declined. Confirmed, no Steven door (Sherlock2,
buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=dab78e4530acd8dc56936d22778547b3cfce4ef95b62a3beca0b66fffc5e66c9&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6, item 2): `not_selected` may go to the losing buyer, the reason token stays optional,
and the buyer may show its copy to a third party (PRD_SELLER.md:304 promises that it "verifies");
the free-text reason stays a commitment only the buyer can open.

### 12.2 Left open by the design, not settled here

- **Q6. Does `/3` count v0 books?** v0 books carry profile `"none"` facts (2.4). Whether `/3` reads
  them, maps them, or leaves them to `/1` and `/2` is AAC#620's.
- **Q7. The gap until `/3`.** v1 deals are on no `/1` or `/2` card (2.5). Decision (coordinator,
  2026-11-17): new deals stay v0 by default until `/3` is served; v1 is selected by a setting.
  Reason: the 2026-11-19 demo and the `/1` card stay untouched. The plugin applies it
  (evidencebook-deal#212).
- **Q8. The witnessed follow-up message** (receipts and the shelf consistency proof, design §4.2,
  "The stamp-proof message") has no form in Forms or in the library. That is AAC#621's.
- **Q9. Late statements and disputes in the report.** Decision (coordinator, 2026-11-17): additive
  rowstatus output; reason: the sealed report needs it, nothing existing changes. Existing callers
  and every existing result stay unchanged, and every existing rowstatus vector
  (`testdata/row-status/`) passes unchanged; the new output gets vectors of its own (section 9).
- **Q10. The grades of `perform.observed`.** Design §7 says an act is "graded by how directly it was
  observed" and lists no grades.
- **Mesh** period books (profile `mesh_period`) are step 8, not this note.

## 13. Where the sources disagree

1. **The number of v0 record types.** Design §7 says 20 in the schema and 19 in the text. Design
   §14, row 7, says 21 (at `125b2d9`). Today `PROFILE.md:195` says 23 and `names.go:57-62` lists
   23, with `outcome_note` added by #462.
2. **A link opening in the stamp proof.** Design §4.2's stamp-proof message has `link_opening?`.
   The library never puts one in a stamp proof (`shelf_stamp_proof.go:126-129`), and Forms §8 says
   "A stamp proof never carries a link opening". This note follows the library. The counterparty
   gets the link opening through `Openings`.
3. **When the offerer stamps.** Design §4.2 says the offerer stamps its agreement before
   `agree_by` and acts after writing it. Acceptance 9 says it stamps after its agreement and its
   first act. These agree only when the first act precedes `agree_by` (7.1).
4. **The fact's `profile` value.** The shelf note's example and the library's test use `"deal"`
   (`461-shelf-facts.md:73`; `shelf_receive_test.go:729`). This note proposes `"x-deal-v1"` (2.3).
5. **The link value's `size`.** Forms §1 defines size as an MMR size unless a rule says entries, and
   Forms §4.2 says "the size of that log at it". The library's test fills it with
   `CountedPrefix.Size`, a record count (`shelf_receive_test.go:726-727`). AAC#621 should settle
   which.
6. **Record time from the other shelf.** Design §4.3 takes a record's time from the first stamp on
   either shelf. The library's `RecordTime` covers only the holder's own positions
   (`shelf_record_time.go:110-120`), and `Receive` takes no receipts (`shelf_receive.go:280`). The
   shelf note gives the remaining vectors to step 5 (`461-shelf-facts.md:192-195`), so PR 1 adds
   the calls.
7. **The rows digest.** Design §4.2 hashes "the rows". The latest dates of named records also fix
   the rows' dates, so this note hashes the rows and the named records together (4.2).
8. **#404 against design §7 and §8:** see Q4.
9. **"Late statements as rowstatus reports them"** (the coordinator's report requirement): rowstatus
   reports neither (`rowstatus.go:180-192`). See Q9.
10. **What the viewer reads.** The report requirement says capsule-viewer reads only
    `lifecycle.state`. It reads the state from `lifecycle.state` alone (`model.py:425`), but its
    `_report` also reads `did`, `money`, `lifecycle.text`, `later`, `may_change`, `as_of`, `scope`,
    `told`, `deadlines`, `cancellations` and `anomalies` (`model.py:413-440`). The v1 report keeps
    `report` for that text.
11. **The stamp proof's timing.** Design §4.3 first says each side sends its proof "After the deal's
    first act", then proposes that the acceptor sends it with its agreement. Acceptance 9 takes the
    proposal, and so does this note.
12. **The intent an agreement cites.** Design §4.2 (line 326 of the export) says the agreement
    cites "the side's own intent and admission decision"; Appendix A's seller has no intent and its
    agreement cites none (line 15). The note requires `admit` always and `intent` only where one
    exists (3.3).
13. **Receipts in a handover.** Design §4.3 takes a record's time from the earliest named receipt
    of a stamp that covers it, and the library's `RecordTime` reads later checkpoints too
    (`461-shelf-facts.md:190`). `Handover` hands over one shelf checkpoint, the anchor, with its
    receipts (`shelf_handover.go:30-51`, `468-469`; shelf note §2.7). So a whole-shelf reader can
    miss an earlier time, and invariant C (8.1) changes the shelf note's §2.7 list in PR 1.
14. **Pairing two shelves' facts.** Design §4.3 promises that facts "cannot be paired by a shared
    digest" (line 470) and that a whole-shelf reader does not learn "which book of another shelf a
    fact pairs with" (line 518). v0's shared book name would break both, so v1 uses per-side log
    ids (3.1). Equal record times in two shelves' status openings remain, and allow a guess, as the
    design already says of ruling dates (§8).
15. **Amount on a `/3` entry.** AAC#626's deal entry keeps `/2`'s optional `amount`, from which the
    band is computed (§5.2, §5.3), but design §4.3's readers table says a whole-shelf reader learns
    "never ... an amount". The status value carries `value_band`, not an amount (8.1), so a `/3` host
    counting from facts has the band and no amount. AAC#620 should say which `/3` reads.
