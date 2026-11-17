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
   (`dealprofile/standard_history.go`), and `python/examples/ard-commerce` keep their bytes and
   behavior. A demo on 2026-11-19 depends on them. Old logs are never rewritten (design §13, step
   5: "New deals only; old logs are not rewritten"). Today's deals stay readable.
3. **The key-derivation strings stay** (`dealprofile/keys.go:201`, `208`, `219`;
   `rust/evidencebook/src/deal/keys.rs:336`, `344`, `354`; `python/evidencebook/deal/keys.py:368`,
   `375`, `383`), so no existing fingerprint changes. Section 2.4 says how.
4. **Acceptance 9.** A deal book opens at agreement. There is no close and no results log. Term
   rows have due dates and contest windows. Each side makes statements. Both agreement records
   name the same witnesses, and a deal whose records have no time from those witnesses is
   tallied as not counted. The stamp proof follows the order of design §4.3 (section 7). The
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
| `deal_id` | REQUIRED | `^deal-[0-9a-f]{16,64}$`, random and opaque, made by the offerer | Decided: design §4.2, "The deal id"; v0 `PROFILE.md:172` |
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
never sent. Each keeps v0's body and v0's rules between them (`PROFILE.md` §3 and §6, rules 5 and
6), now inside the journal:

- `intent` (v0 `IntentBody`, `types.go:79-92`, unchanged);
- `check` (v0 `CheckBody`, `types.go:262-299`, plus `payment_ref`, the reference the payment will
  be made under, design §4.2): what is about to happen;
- `verdict` (v0 `VerdictBody`, `types.go:354-366`, unchanged): the answer to **one check**,
  `pass`, `pause` or `deny`, with exactly one `checks` ref to its check (`PROFILE.md:208`). It is
  not the deal-level `rules_verdict` (`PROFILE.md:220`), which is one word on the whole deal (3.3)
  (review round 2);
- `approval` (v0 `ApprovalBody`, `types.go:384-391`, unchanged): its `approves` ref still names the
  verdict it answers, or, on `confirm_limits`, the intent, as in v0 (`PROFILE.md:209`).

**How the references migrate** (review round 2). In v0 an `action` carries `authorized_by` to an
`approval` with `proceed: true` (`PROFILE.md:210`). In v1 the act is a deal-book `perform` or
`settle` and cites the `check` by digest (3.3; design §4.2: "The act's record in the deal book
cites the passing check by digest"). A record check of the act's author, run on its own shelf,
follows that check to its one `verdict` in the journal: the act is allowed when the verdict is
`pass`, or when it is `pause` and one `approval` with `proceed: true` approves it. A `deny`, or a
pause with no such approval, refuses the act. The counterparty sees only the act and the check's
digest (design §4.2: "the counterparty has no claim on it and relies on the statement"). A change
of rules and the note of what was shared stay where the library keeps them today. The decision is
Steven's (section 12, Q3).

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
| `message`, `claim`, `evidence`, `detail_change`, `agent_move` | the journal before the holder's agreement exists; that deal book after it |

A record is never sealed into a deal book before its agreement: a v1 deal book's first record is
its agreement (2.3 rule 2), so a flow that has not agreed seals in the journal.

**A listing and its offers.** The listing's content is published in a feed and is not a record
that is sent (design §7, `propose`). The seller's journal `sale` record commits to it. Each buyer's
offer (`propose`) is its own record with its own `deal_id`, and is that buyer's thread: v1 has no
other thread id (3.5). **Proposed:** an offer that answers a listing carries `listing`, the SHA-256
of the published listing bytes it read (3.3). The seller relates each received offer to its
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
no v0 side book. The overlays' names are part of the vocabulary trim Steven decides (Q3).

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
| `witnesses` | REQUIRED | the witnesses the offerer accepts from the listing's list, possibly empty | design §4.3 (witness paragraph); u186 for an empty list; naming is Q1 |
| `ask` | OPTIONAL | how to ask the offerer for evidence: its evidence commitment's address and digest | design §7, `propose` |
| `profiles` | OPTIONAL | the deal profile versions the offerer reads | Proposed (2.3) |
| `listing` | OPTIONAL | the SHA-256 of the published listing bytes this offer answers | Proposed (3.2, "A listing and its offers"; review round 2) |

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
  exist when it is sealed; `adjudicator`, the key id the agreement names (design §8 table).
- `kind: "withdrawal"`: `row` (same table).
- `kind: "challenge"`: `ruling`, digest ref; `grounds_commitment` (same table).

**`ruling`** (the adjudicator's case book; one book per case, design §8). Members are design §8's
table: `row`; `keys`, `{"buyer", "seller"}`; `against`, `buyer` | `seller` | `null`; `value`,
`met` | `not_met`; `remedy`, `{"kind", "amount_minor"?, "currency"?, "due"}` or absent; `read`,
the last record id of each deal book the adjudicator read; `rules_digest`; `final`, a boolean.
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

**`rules_verdict`** (deal-book overlay). Members as v0: `word` (`met` | `not_met` | `unknown` |
`out_of_scope`), `pack_digest`, optional `pack_id` (`types.go:472-476`; `PROFILE.md:220`). Decided
(constraint 4; u195 as the How ack quotes it, #461 comment 6936343259): sealed in the party's own
deal book, never the journal; bound to the deal id (its block) and the pack digest (its body);
one word, at most one per deal; carried in the counterparty cut, with its payload withheld where
the holder's share policy says so ("not shared"); never read by a card, a report row or the row
status. v0 seals it "only after the final `close`" (`PROFILE.md:220`), and v1 has no close.
**Proposed:** seal it only once the holder computes the deal settled, as 9 defines `settled`
(design §4.2, third consequence). A deal that is never settled has no verdict. Q2.

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
status is local, and a card tallies the deal as not counted (acceptance 9). How a witness is named
is Q1, which goes to Steven.

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
3. the offerer's agreement has a record time not after `agree_by`, or the acceptor's book names it.

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
6. `offerer_agreement_time` set and not after `agree_by`: `started`, `local` false,
   `by: offerer_in_time`.
7. `offerer_agreement_time` null and `at` not after `agree_by`: `started`, `local` true (Appendix
   A lines 21 and 22: "a local result until ...").
8. Otherwise: `not_started` (a time after `agree_by`, or none by then, and never named).

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
| acceptor agrees, offerer's agreement has no time by `agree_by` | both | started if the acceptor names it; else not started |

A case with no book computes nothing: there is no book to stamp, and no fact.

### 5.4 Vectors (acceptance 5)

`testdata/deal-v1/start/vectors.json`: each case gives `StartInput`, the expected `StartResult`,
and, for the book-level cases, the signed records of both books from which `StartFromBooks` must
derive that input.

| Case | Expect |
|---|---|
| both agree, offerer's agreement timed before `agree_by` | started, not local |
| both agree, offerer's agreement untimed, T before `agree_by` | started, local |
| the same, T after `agree_by`, not named | not started |
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
| `remedies` | every `settle` or `perform` record whose `remedy_for.row` is the row, by the side the first final ruling is against, that gives what that ruling orders, whenever it was made and whichever ruling it points at, or none (design §8, "The remedy"; line 955 of the export). A payment remedy gives what is ordered when its two legs pass the check of 6.3 for the amount and currency the ruling's `remedy` names. A non-payment remedy gives it when its `act` is the ruling's `remedy.kind`. A record that gives something else is not a remedy of that ruling (review round 1) |

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
  the uncontested statement's record has a time not after the row's window end.
- **The link value** (8.2): `last_received` is the record `seen` would name now.
- **For each statement, when the other side's book first named it** (design §4.3, "The facts"):
  the time of the first record of the other book whose `seen` reaches it.

### 6.3 The payment check

Decided (design §8, "Settlement"): the check on a payment row passes when the two legs are agreed,
the agreed status is settled and the amount equals the terms. It fails on a mismatch, on sealer
conflation, and, with agreed legs, on a status of failed or reversed or a different amount. In
every other case it has not run, and rowstatus gets an empty `check`.

No leg-matching code exists in this repository (a search for `unjoined` across Go, Rust and Python
finds none). **Proposed:** v1 adds `xdealv1.PaymentCheck(payerLeg, payeeLeg, owed)`, following the
settlement draft's states as design §8 lists them, with vectors for each state. The other
recomputed rows wait for design §13, step 3 ("Each recomputed deal row computed in one engine"),
and until then report "not run". The digests the check ran over go in the link value's
`checked_over` (design §4.3; Forms §4.2).

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
**Proposed:** the offerer stamps after its first act, and if the first act has not happened by
`agree_by` minus the owner's margin, it stamps its agreement alone first.

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
2. The profile then requires all of these, or it refuses to act:
   - the store holds a verified proof whose coverage reaches the acceptor's agreement. This is
     read from the store's persistent state, never from one call's events. `ReceiveChanges.Covered`
     reports only coverage a call newly adds (`shelf_receive.go:226-230`, `report` at `387-411`). A
     proof received again returns `Duplicate` before any coverage is reported
     (`shelf_stamp_proof.go:307-310`), and so does a restart that re-reads nothing. No public reader
     of the verified coverage exists today: `verifiedCoverage` is internal
     (`shelf_receive.go:1069`). A count would not be enough either (review round 2). The store's
     coverage is the largest verified proof size for the sender and book log
     (`shelf_receive.go:1067-1078`), and the library checks no profile binding: a proof signed by
     the same key over another shelf log is stored, verified and counted
     (`VerifyStampProof` checks only that one key signed both checkpoints,
     `shelf_stamp_proof.go:46-86`; Forms SF-5). So PR 1 adds a lock-free reader of the stored
     proofs themselves, `Shelf.HeldProofs(ctx, senderKey, logID) ([]HeldProof, error)`: each held
     proof's `StampProof` bytes as received and whether its root recomputed from the records held
     (`shelf_received_proofs.verified`). `CanAct` reads them on every call and accepts only a proof
     that passes every check below;
   - the proof verifies again (`VerifyStampProof`), and its root recomputed from the records held;
   - the fact's `book_id` is the record id of the acceptor's agreement, and its size covers it;
   - the verified proof's `KeyID` equals the agreement's `checkpoint_key`, and the shelf
     checkpoint's log id equals its `shelf_log_id` and, where the acceptor published an evidence
     commitment, the log that commitment names (design §4.3, "The stamp proof"; the profile's
     answer to Forms SF-5).
3. It writes its agreement (`seen` = the acceptor's agreement), and before its first `perform` or
   payer `settle`, `xdealv1.CanAct(deal)` reads `Shelf.HeldProofs` and checks, on each call, that a
   proof meeting every check of step 2 is held. Without one the act is refused. `CanAct` gives the
   same answer after a duplicate proof and after the shelf is closed and opened again, and a proof
   that fails a binding stays refused after a restart, since nothing about it is cached.
4. After the act: `Stamp`, `StampProof(..., AudienceCounterparty)`, and a push with the act's
   record.

**Acceptor**, on the offerer's proof: the same `Receive` and the same checks. **Proposed:** its own
gate applies to its first `perform` or payer `settle` after the offerer's agreement. A payee leg
and a statement are never gated. Appendix A has the seller write its payment received record
(line 27) before the buyer's proof arrives (line 32), and deliver (line 37) after.

**Later**, before either side treats the deal as settled or relies on the other's card, it asks
for the witnessed checkpoint: a shelf checkpoint with a receipt from a named witness, the
consistency proof from the checkpoint it kept, and the stamp proof for the book's newest fact
(design §4.3, "The witnessed checkpoint"; Appendix A lines 42b to 42d). Neither the library nor
Forms has a form for receipts or a shelf consistency proof in a push (`shelf_receive.go:280`;
Forms §6.1). Section 9, PR 1, and Q8.

### 7.3 Vectors

`testdata/deal-v1/order/`: the acceptor's agreement with its proof, accepted; the same without a
proof (the offerer's act is refused); a proof under another shelf log id (refused); a proof by
another checkpoint key (refused); a proof whose book checkpoint the records do not recompute
(refused, evidence kept); the offerer's later proof, accepted; a payee leg sealed before the
offerer's proof (allowed). Added in review round 1: the whole handshake from an offerer that holds
only its journal offer (the acceptor's agreement and proof admitted, 8.3); the same proof sent
twice (`Duplicate`, `CanAct` still true); the proof accepted, the shelf closed and opened again
(`CanAct` still true); and `ProofSender` from each side, for the acceptor's proof received by the
offerer and the offerer's proof received by the acceptor, each returning the proof author's record
key. Added in review round 2: a proof signed by the acceptor's checkpoint key over another shelf
log, stored and verified by the library, then the shelf closed and opened again (`CanAct` still
refuses); and two senders whose books share one log id (8.3, `ProofSender`).

## 8. The StatusProvider and ReceiveProfile implementations

### 8.1 StatusProvider: the status value

`xdealv1.StatusProvider.Values(ctx, book_id, log_id)` returns `("x-deal-v1", link, status, true,
nil)` for a v1 deal book, and `ok` false for every other book (2.4). It reads only through calls
that take no lock (`CountedPrefix`, `ReceivedRecordOf`, `RecordTime`; `shelf_receive.go:38-41`).

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
 "start": { StartInput without "at" },
 "rows": [
   {"id": "as_described",
    "input": { rowstatus.Input without "at", its times in the time forms below },
    "statement_named_by_other": [<time form> | null, ...],
    "ruling_against": "buyer" | "seller" | null}],
 "disputes": [
   {"row": "as_described", "claimant": "buyer" | "seller",
    "outcome": "open" | "for_claimant" | "against_claimant" | "no_finding" | "withdrawn"}]}
```

**Times in a fact** (review rounds 1 and 2). A time the status value carries takes one of three
forms, so that "no record", "a record whose time is not yet known" and "a time already known" are
never confused:

| Form | Means | A reader resolves it to |
|---|---|---|
| `"<RFC 3339>"` | the record exists and its time was known when this fact was written | that time |
| `{"stamp": n}` | the record exists, stamp `n` is the first stamp on this shelf that covered it, and no time from a named witness was known when this fact was written. `n` is the stamp entry's number (`461-shelf-facts.md:159`); for a record this fact's own stamp is the first to cover, `n` is this fact's stamp | the earliest registration time, from a named witness, among the receipts it holds for the shelf checkpoints of stamps `n` up to its anchor. Each of those checkpoints covers stamp `n`'s entries, so each receipt is a valid time for the record, and the earliest is the record's time on this shelf (design §4.3, "A record's time"; the library reads it the same way, "later checkpoints included", `461-shelf-facts.md:190`). With no such receipt the record has no time |
| `null` | no such record exists: a named record not yet written, a statement the other side's book has not yet named | no record. A `Named.time` of `null` leaves the dates it sets not set (design §4.2, "Not set until the record has a time"; line 371 of the export) |

The first stamp unwitnessed and the next one witnessed (review round 2): a record first covered at
stamp 1, whose checkpoint never gets a named receipt, is written `{"stamp": 1}` in the fact of stamp
2. If stamp 2's checkpoint then gets a named receipt and is the anchor, a reader resolves the record
to that receipt's time at once, with no third stamp; the holder's own `RecordTime`
(`shelf_record_time.go:110-175`) gives the same time. The holder's next fact also writes it as a
time, since the value changed (`461-shelf-facts.md:143-147`).

A whole-shelf reader needs the receipts of every stamp checkpoint from `n` to the anchor to find the
earliest. `Handover` carries the anchor's receipts only (`461-shelf-facts.md:199-206`). So PR 1
extends it to carry every receipt `shelf_receipts` holds for the shelf checkpoints it hands over
(11.1; section 13, item 13). A reader with fewer receipts resolves to the earliest of those it holds,
which can only be later than the true time; it reports such a result as not recomputed.

**To rowstatus.** A resolved time is passed as that time; a `{"stamp": n}` with no receipt, and
`null`, are passed as `null`, which rowstatus reads as "a record with no time" (`rowstatus.go:87-89`)
and treats as local. A resolved named record's time sets its dates (`rowstatus.go:415-430`). A
`null` named record and an unresolved one both leave the dates unset until the latest date passes
(`rowstatus.go:13-19`), which is what the design means by both.

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
  its claimant's role, and its outcome.
  **The effective ruling** (review round 2) is the one rowstatus applies: the first final ruling of
  the adjudicator the agreement names, and none at all when the holder holds two conflicting final
  rulings, in which case rowstatus gives `in_dispute` (`rowstatus.go:538-560`; design §8, "A forked
  case book"). `ruling_against` is the effective ruling's `against`, or `null` when there is none.
  The outcome is `withdrawn` after the claimant's withdrawal; `for_claimant` or `against_claimant`
  when the effective ruling is against the other side or the claimant; `no_finding` when it is
  against neither; and `open` otherwise, conflicting final rulings included, so no branch of a
  forked case book decides a card's dispute outcome. `/3` maps these as 10 says, and `forum` is Q4. The
  four did-not-hold splits are AAC#626's (§11.4), and read only row results, owed_by and remedy, so
  the status needs nothing more for them.
- **Which statements go in** (design §4.3: "each side's latest statement that is known to be in
  time, any newer statement that this stamp is the first to cover, and any later concession"): per
  side, the latest statement with a known time not after the window end; every later statement
  whose time is not yet known, that is, written `{"stamp": n}`, whether this stamp or an earlier
  one first covered it; and the first statement after the window end that would be a concession.
  **Carry-forward** (review round 2): a statement an earlier fact carried as `{"stamp": n}` stays in
  every later fact until its time is known. It then enters as a known time: as the side's latest
  in-time statement when its time is not after the window end, or as the concession candidate when
  it is after it, or it drops out when it is neither. A conformance property pins it: `Compute`
  over these statements equals `Compute` over all of them, for every T at or after the stamp and
  for every later fact of the book (11.2).
- Disputes, withdrawals, the first final ruling with its date (or the conflicting flag) and
  remedies are in each row's `input`. The witnesses are deal-level (design §4.3, "For the deal").

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
  - a `decline` or `cancellation` only from the key an offer in our journal was sent `to`, or the
    signer of the offer it cancels;
  - **the acceptor's first records, at an offerer that holds only its offer** (review round 1): a
    deal-book record of a book whose `seq` 1 is an `agree` of kind `agreement` that cites, by
    record digest, a `propose` in our journal, and whose signer is that offer's `to` key. The
    store holds the record part before the proof part (`shelf_receive.go:270-279`), and Admit is
    asked about the agreement itself, so the profile checks the agreement's `offer` and signer
    there; a later record of the same book is admitted from the same key. This is the library's
    "to which an offer in the journal was sent" branch;
  - any other deal-book record only from the `counterparty_key` of our agreement for that
    `deal_id`, or, for an acceptor still waiting, from the signer of the offer it agreed to;
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
  position 1 of a log with that id, one per sender. The profile keeps a candidate only when all of
  these hold: it is a v1 `agree` of kind `agreement`; its `checkpoint_key` is `checkpointKeyID`;
  its `deal_id` is a deal of ours (we hold its offer in our journal, or our own agreement for it);
  and its signer is the key that deal expects (our offer's `to`, or our agreement's
  `counterparty_key`). With exactly one candidate left, it returns that candidate's signer and `ok`
  true. With none, or more than one, it returns `ok` false, and the proof is refused as from a
  sender that may not push it (`shelf_stamp_proof.go:222-227`).

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
 "remedy": "none" | "due" | "made" | "not_honoured" | null,
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
    `"not_set"`.
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
  `uncontested`, or `ruled` with remedy `none` (the final ruling ordered nothing) or `made`. A
  `not_reached` row is left out, as the design says ("it is left out when the deal is settled",
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
  | row `remedy` | `none`, `due`, `made`, `not_honoured`, or `null` for a row that is not ruled |
  | row `check` | `passed`, `failed`, `not_run`, `not_recomputed` |
  | row `due`, `window_end` | an RFC 3339 UTC time, or `not_set` |
  | `audience` | `keep`, `counterparty`, `adjudicator` |
  | booleans | `local`, `witnessed`, `settled`, `final`, `counterparty_layer` |

- A page shows `rows` and `start` as they are. It never recomputes them.

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
  - `witnesses`, and each record's time from those witnesses only, so a deal whose records have no
    such time is tallied as not counted (acceptance 9);
  - `role`, `value_band`, `counterparty_kind`;
  - `agreed_at`, and the first witnessed time of the book's first fact, since design §4.1,
    "Counting only a window", proposes the latter as the window key in place of the time of
    agreement;
  - per row, `owed_by` and the rowstatus inputs, so each row's status at the anchor's time gives
    the card's line, and the four did-not-hold splits can tell rows the holder owed from rows the
    other side owed (design §4.1, the card table);
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
  side's latest facts at 1 Dec, the full expected row results and lines under AAC#626 §11.3 and
  §11.4: the seller's deal has row 3 `agreed` `not_met`, owed by the holder and not `ruled`, so it
  is `did_not_hold` in part `not_remedied`, although the seller refunded; the buyer's has the same
  row owed by the counterparty, so `did_not_hold` in part `other_side`. That first result is what
  Q5 puts to Steven. The ruled path (Appendix A lines 43 to 76) gives `did_not_hold` in part
  `remedied` for the seller and `other_side` for the buyer, and the dispute entries `received`
  and `against` for the seller, `raised` and `for` for the buyer.

## 11. Order of work, and conformance

### 11.1 Pull requests

Each lands alone and keeps the shelf interlock, `row-status-diff` and the `deal-profile` job green
(`ci.yml:100-143`, `540-543`). Go comes first with the vectors. Rust and Python then follow the
same vectors, each in its own pull request.

1. **Library, format-free** (Go, then Rust and Python): the record time of a counterparty
   position covered by the holder's own facts (4.3); and, once AAC#621 fixes its form, receiving
   the witnessed follow-up (receipts and the shelf consistency proof) and the record time it
   gives. If Steven picks key-id naming (Q1), `RecordTime` also takes witnesses by key id,
   reading `shelf_receipts.key_id` (`461-shelf-facts.md:117-125`). The additive rowstatus `late_*`
   outputs land here too (Q9, decided), and `Shelf.SealSent`, which seals a sent journal record
   and its standalone Capsule in one call (3.1). Added in review rounds 1 and 2, both lock-free
   readers so the profile may call them from a stamp or a `Receive`: `Shelf.HeldProofs(ctx,
   senderKey, logID)`, the stored proofs `CanAct` re-checks on every call (7.2), and
   `Shelf.HeldFirst(ctx, logID)`, every held record at position 1 of a received log with that id,
   one per sender, which `ProofSender` narrows to one (8.3). Also from round 2: `Handover` carries
   every receipt `shelf_receipts` holds for the shelf checkpoints it hands over, not only the
   anchor's, so a reader resolves `{"stamp": n}` times to the earliest time (8.1).
2. **`dealprofile/x-deal-v1/`**: `PROFILE.md`, `x-deal-v1.schema.json`, `check_profile.py` (v1), and
   fixtures: Appendix A's two books as positive fixtures, with negatives for each record check of
   section 3. **`dealprofile/xdealv1`**: names, types, Encode and Decode, the record checks,
   `VerifySent` with the decline and cancellation vectors (3.5), and `dealprofile.Version` (2.3).
3. **`Start`, `StartFromBooks`** and the start vectors (5.4).
4. **The status value, the link value, `StatusProvider`, `ReceiveProfile`, `PaymentCheck`,
   `CanAct`**, with the order vectors (7.3), acceptance 3's two remaining vectors (the other side's
   stamp covering our record first; a statement whose in-time state is known only at its first
   stamp), and status vectors.
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
- **Times in a fact** (review rounds 1 and 2). Status vectors, one per form of 8.1: a named record
  not yet written (`null`, its row's dates stay unset); a record this stamp is the first to cover
  (`{"stamp": n}` with `n` this stamp), resolved to the anchor's receipt time and passed to
  rowstatus as that time, so a named record's dates are set and the status is not local; a record
  covered earlier with a known time (the time); and the case of the first stamp unwitnessed and the
  next witnessed, where the fact of stamp 2 writes `{"stamp": 1}`, an anchor at stamp 2 resolves it
  to stamp 2's receipt time, and the holder's `RecordTime` gives the same time. A stamp that turns
  a `{"stamp": n}` into a time writes a new fact.
- **Carry-forward** (review round 2). A statement first covered at a stamp with no named receipt
  stays in each later fact as `{"stamp": n}` until its time is known, then enters as a known time
  (in time, as a concession candidate, or dropped). `Compute` over the selected statements equals
  `Compute` over all of them at every fact and every T, for these cases.
- **Effective ruling** (review round 2). With two conflicting final rulings held, the row is
  `in_dispute`, `ruling_against` is `null`, and each dispute's outcome is `open`.
- **Journey overlay placement** (review round 2). `sale`, `thread`, `thread_opened`, `sale_unpost`,
  `counterparty_profile` and `disclosure` are refused in a deal book; a `message` sealed before the
  agreement is in the journal and one after it is in the deal book; a v1 `verdict` in the journal
  with `pause` and an `approval` with `proceed: true` lets the act that cites its check pass the
  act's record check, and a `deny` refuses it.
- **Offers on one listing.** Two offers citing the same `listing`; the seller agrees to one and
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

**Steven's one ask for AAC#626.** Q4 and Q12 gate AAC#626's exact-head approval (the `/3` card's
`forum` and `by_forum`, and its line for a `not_reached` row and for a deal with no witnessed time),
and Q5 decides a line of the same card. They go to Steven in one ask, with Sherlock2's
recommendations below (Sherlock2, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=dab78e4530acd8dc56936d22778547b3cfce4ef95b62a3beca0b66fffc5e66c9&thread=3d8cda2cdd8468ac2e9490593e42c5325e21c58af6c96156801f9ccc1db684b6, relayed by the coordinator on 2026-11-17).

**Q2. When is `rules_verdict` sealed now that there is no close?** v0 seals it after the final close
(`PROFILE.md:220`). Proposed: once the holder computes the deal settled (9, `settled`). A
consequence to rule on: a deal that is never settled then never gets a verdict.
*Sherlock2's recommendation:* seal at settled, and a deal that is never settled has no verdict,
since a verdict sealed before settlement would be a close by another name. u195's other conditions are kept as they
are (3.3), condition 1 (shared under the holder's share policy: a node shares by default, a
person does not) and condition 4 among them, and the verdict word stays a record in the party's
own book, never a member of a report (u195 condition 3; section 9).

**Q3. The intent, the check before an act, its verdict and the approval.** Design §7 says they need "types of
their own or a place in the overlay; no source settles which". Proposed: a journal-only overlay
of four types (`intent`, `check`, the per-check `verdict`, `approval`), reusing v0's bodies and rules (3.2), and the journey overlay placed by phase (3.2). This touches the trim of the public vocabulary that
design §14, row 7, puts to Steven.

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

**Q12. A row that is not reached, and a deal with no witnessed time, on a card.** The design says a
`not_reached` row is "left out" when a card is counted (design §4.2, "Dates that hang on a
record"); this note has no rule for it, and AAC#620's `/3` draft treats it as undecided. And
acceptance 9 says a deal whose records have no time from the named witnesses is "tallied as not
counted", while the design calls such a status "a local result" only. Both need one answer
before v1's vectors pin them. This is one of the questions that gate AAC#626's exact-head approval
(the one ask above).
*Sherlock2's recommendation:* keep `not_reached` as `undecided` on the card, as AAC#626 §11.3 does,
since "left out" would let a holder drop deals from its card by never timing the named record. The
`settled` rule of 9 still leaves a `not_reached` row out of settlement, as the design says; that
decides when a deal is finished, not its card line.

**Q5. A remedy outside a ruling** (moved here from 12.2 in review round 1). Two cases: a remedy
chosen under the terms (design §4.2, "Remedy rows"), which has no contest window in the design and
no rowstatus input outside a ruling (proposed: a derived row, 4.6); and a remedy made by agreement
after a concession, as in Appendix A's settled-without-ruling path (lines s1 to s12). rowstatus
records a remedy only for a ruled row (`rowstatus.go:138-151`), and AAC#626 §11.4 puts a deal whose
unmet row owed by the holder is not `ruled` in `not_remedied` ("no remedy is made or ordered"). So
on a public card the seller who refunded in Appendix A shows as `not_remedied` (10, vectors).
Whether a remedy outside a ruling should count as remedied is Steven's: it changes a public card
line. It is part of the one ask above.

**Q11. What a decline and a cancellation disclose to the other side.** This belongs to Sherlock2's
door question. Proposed (3.5): the signer's key, the cited offer's digest, the deal id, one closed
reason token (`not_selected` or `other`), the author's own time `at`, and optionally a committed
free-text reason opened only to that side. It discloses nothing about other offers or the winning
terms, and nothing about its place in the journal. The questions: whether `not_selected` may be
told to the losing buyer at all (Muse's PRD says the copy "says so", `PRD_SELLER.md:303-305`);
whether a reason token is allowed or required; and whether the buyer may show the copy to a third
party, since that copy proves the seller's key declined. Sherlock2 confirms or rules, with Steven
where it is his door.

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
13. **Receipts in a handover.** A fact's `{"stamp": n}` time resolves to the earliest named receipt
    of the shelf checkpoints from stamp `n` to the anchor, and `Handover` carries receipts for the
    anchor checkpoint only (`461-shelf-facts.md:199-206`). With the anchor's alone a reader gets a
    valid time but possibly not the earliest. PR 1 extends `Handover` to carry every receipt it
    holds for the checkpoints it hands over (8.1; 11.1), which changes the shelf note's §2.7 list.
14. **Pairing two shelves' facts.** Design §4.3 promises that facts "cannot be paired by a shared
    digest" (line 470) and that a whole-shelf reader does not learn "which book of another shelf a
    fact pairs with" (line 518). v0's shared book name would break both, so v1 uses per-side log
    ids (3.1). Equal record times in two shelves' status openings remain, and allow a guess, as the
    design already says of ruling dates (§8).
15. **Amount on a `/3` entry.** AAC#626's deal entry keeps `/2`'s optional `amount`, from which the
    band is computed (§5.2, §5.3), but design §4.3's readers table says a whole-shelf reader learns
    "never ... an amount". The status value carries `value_band`, not an amount (8.1), so a `/3` host
    counting from facts has the band and no amount. AAC#620 should say which `/3` reads.
