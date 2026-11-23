# x-deal-v1: the deal record profile

**Status: draft, v1. Not registered.** This profile ships with EvidenceBook beside x-deal-v0
(`dealprofile/x-deal-v0/`), which stays the default and stays readable. It is the record format
of the evidence layer's deal (evidencebook#461 step 5). The design note
[docs/design/461-deal-profile.md](../../docs/design/461-deal-profile.md) holds the reasons and the
sources for each rule below; this document states the rules a record must meet. Sections of that
note are cited as "design note §N".

Files in this directory:

| File | What it is |
|---|---|
| `PROFILE.md` | This document. |
| `x-deal-v1.schema.json` | JSON Schema (Draft 2020-12) for one record: the block and each v1 body. The journal and journey overlays keep v0's bodies, which `x-deal-v0/x-deal-v0.schema.json` defines. |
| `fixtures/` | The deal of the integrated design's Appendix A, record by record, and one negative for each record check (section 9). Its README says how they are built. |
| `check_profile.py` | The reference checker in Python (section 9). Stdlib only, with `jsonschema` for the schema stage. |

The Go package is `dealprofile/xdealv1`. The key words MUST, MUST NOT, SHOULD and MAY are used as
in BCP 14 (RFC 2119, RFC 8174).

## 1. What a deal record is

A record is a JSON object with exactly two members: the profile block, named `x-deal-v1`, and
`body`.

```json
{"x-deal-v1": {"profile": "x-deal-v1", "canonicalization": "jcs", "deal_id": "deal-…",
               "record_type": "settle", "at": "2026-11-01T10:15:00Z", "seq": 2, …},
 "body": {"leg": "payer", "statements": [{"row": "paid_as_agreed", "value": "met"}], …}}
```

Records are closed: a member the schema does not name fails the schema. Numbers are integers;
a number written with a fraction or an exponent is refused before the schema. Text is read as
written: invalid UTF-8, an escaped surrogate outside a pair and an object member given twice are
refused, since a decoder would read them otherwise than the digest covers. An OPTIONAL member is
absent rather than holding its type's zero value (`false`, `0`, `""`): a row's `recomputed` is
present only as `true`. A text that names one record two ways would give it two digests.

**Versions** (design note §2.3). A record carries exactly one top-level member whose name matches
`^x-deal-v[0-9]+$`, and its block's `profile` equals that name. A typed v0 record (one whose
top-level `type` is a typed record or contract type of x-deal-v0 §9) carries no block and is v0.
`dealprofile.Version` returns the version of a record and refuses one with two blocks, with a
block and a typed `type`, or with neither. A reader that does not know a version refuses the
record as unknown, never as invalid, and never reads it as another version.

A book has the version of its first record, and a record of another version in it is invalid. A
deal has one version on both sides: the offer's. A v0 half and a v1 half are never one deal.

**Keys and fingerprints.** v1 uses v0's key derivations, fingerprints and commitments
(`dealprofile.DealKey`, `ProfileKey`, `Fingerprint`, `CommitText`) unchanged. Their labels
(`x-deal-v0/deal-key`, `x-deal-v0/profile-key`, `x-deal-v0/fp`) are frozen domain-separation
labels, not the version of the record that uses them, so every fingerprint keeps its value.

## 2. The block

| Member | Req | Meaning |
|---|---|---|
| `profile` | REQUIRED | `"x-deal-v1"` |
| `canonicalization` | REQUIRED | `"jcs"`. Any other value fails closed. |
| `deal_id` | section 3 | `^deal-[0-9a-f]{16,64}$`, random and opaque, made by the offerer. |
| `record_type` | REQUIRED | one of the types of section 4. |
| `at` | REQUIRED | the author's own seal time, RFC 3339 UTC to the second with `Z`. It is the author's word, never a record time. |
| `seq` | REQUIRED in a book | the record's position in its book, from 1, with no gaps. |
| `prev` | REQUIRED iff `seq` > 1 | digest ref of the book's record at `seq - 1`. |
| `agreement_ref` | REQUIRED iff in a deal book and `seq` > 1 | digest ref of the book's agreement. |
| `seen` | REQUIRED in a deal book | the record id of the last record its author held from the other side, or `null` (section 6). |
| `refs` | per type | typed citations of earlier records, as v0's rel refs. Only journal records that are not sent carry them (a `verdict`'s `checks`, an `approval`'s `approves`); a deal-book record and a record sent whole carry none (section 5, "A journal record whose digest leaves the holder"). |
| `marks` | OPTIONAL | the named records (the `named` of the rows) this record is, such as `["paid"]`. |
| `counterparty` | OPTIONAL | v0's fingerprint block. |
| `producer`, `commit_alg` | OPTIONAL | as v0. |

Every time a record carries, in its block or its body, MUST name an instant that exists: a time
the pattern admits but the calendar does not (`2026-11-31T15:00:00Z`) is refused.

**Digest.** The record digest is `lowercase_hex(SHA-256(JCS(record)))` over the whole record,
with no normalization. A digest ref is `{"type": "deal-record", "digest_alg": "SHA-256",
"digest": "<64 hex>"}`. `prev`, `agreement_ref`, `refs` and an agreement's `offer` cite record
digests.

**Record id.** The record id is the Capsule id of the sealed book record. It names a record in
`seen` and in a ruling's `read`. It is not the record digest.

## 3. Where `deal_id` is present

- REQUIRED on every deal-book record, on `ruling`, and on the journal records that serve one deal:
  `propose`, `agree` of every kind, the journal overlay's `check` and `verdict`, an `approval`
  that answers a verdict, an `ask` or `answer` between the two parties of a deal, and a journey
  record sealed after its offer exists.
- ABSENT on the journal records that predate or span deals: an `approval` whose choice is
  `confirm_limits`, `intent`, `admit`, an `ask` or `answer` of an admission, `sale`, `thread`,
  `thread_opened`, `sale_unpost`, `counterparty_profile`, `disclosure`, and a journey record
  sealed before any offer.

A journey record (`message`, `claim`, `evidence`, `detail_change`, `agent_move`) and a journal
`ask` or `answer` cannot show on their own which case they are: the sealer knows whether the
offer exists and whether the ask is of an admission. `xdealv1.CheckSealed` takes that context
(`SealContext`) and applies the rule; `xdealv1.Check` admits either.

## 4. The record types, and where each is sealed

| Type | What it records | Where sealed |
|---|---|---|
| `propose` | an offer or a counter-offer | the offerer's journal; sent whole (section 7) |
| `agree` | `kind` `agreement`; `decline`; `cancellation` | an agreement opens a deal book at `seq` 1; a decline or cancellation is in the journal and sent whole |
| `perform` | a delivery or other act; a remedy that is not a payment | deal book |
| `settle` | a payment leg: payment, payment received, refund or another remedy that is a payment | deal book |
| `statement` | statements with no act; the record that names an arrived cancellation or ruling | deal book |
| `dispute` | `kind` `dispute`, `withdrawal` or `challenge` | deal book |
| `ruling` | the adjudicator's ruling | the adjudicator's case book (no `seen`, no `agreement_ref`) |
| `ask`, `answer` | an evidence request and its outcome, wrapping the library's documents | the journal, or the deal book when between the two parties (it then has a `seq`) |
| `admit` | an admission decision | journal |

**The journal overlay**, sealed in the journal only and never sent, with v0's bodies: `intent`,
`check` (v0's body plus `payment_ref`, the reference a payment will be made under), `verdict`
(the answer to one check, with exactly one `checks` ref) and `approval` (its `approves` ref names
the verdict, or on `confirm_limits` the intent).

**The deal-book overlay**: `rules_verdict`, with v0's body, sealed in the party's own deal book
once the holder computes the deal settled, at most one per deal.

**The journey overlay**, with v0's bodies, sealed in the journal only: `message`, `claim`,
`evidence`, `detail_change`, `agent_move`, `disclosure`, `sale`, `thread`, `thread_opened`,
`sale_unpost`, `counterparty_profile`. A reader that does not know an overlay type keeps the
record and ignores it. Overlay records are no input to the deal's start, its statements, its row
status or a card.

v0's `baseline`, `action`, `close`, `outcome`, `outcome_note`, `sale_cut` and `sale_close` have no
v1 counterpart: a v1 book opens with its agreement, an act is a `perform` or `settle`, and a deal
is settled by computation, with no close.

**Placement rules.** A record in a deal book or a case book carries `seq`, and `prev` exactly when
`seq` > 1. A deal-book record carries `seen`, and `agreement_ref` exactly when `seq` > 1. A deal
book opens with its agreement, and an agreement is only ever at `seq` 1. A journal record carries
no `seq`, `prev`, `agreement_ref` or `seen`, so a record sent whole shows no place in the journal.
A decline or cancellation also carries no `refs`, `marks` or `counterparty`.

## 5. The bodies

The schema gives every member. The rules the schema cannot express are these.

A **statement item** is `{"row", "value", "remedy"?}`; `value` is `met`, `not_met` or
`cannot_tell`; `remedy` is set only on a statement that chooses a remedy.

**`propose`.** `role` (the offerer's), `to` (the key the offer is addressed to, or `null` for a
counterparty with no layer), `counter_to`, `terms`, `pack_digest`, `pack_url`, `stated_at`,
`agree_by`, `terms_rows`, `adjudicator` (or `null`), `forums`, `witnesses`, `ask`, `profiles`,
`listing_digest`.

- `terms_rows` holds `rows` and `named`. A row id is given once. A date is an instant (`at`) or
  hangs on a named record (`after`, with `seconds`), and a named record a row hangs on MUST be in
  `named`.
- `forums` is the recourse clause: the forums the offerer accepts for a dispute. It is empty
  exactly when `adjudicator` is `null`, and such an offer allows no dispute.
- `witnesses` names each witness by its key id, with its URL as a hint. A key id is named at most
  once, whatever its URLs.

**`agree`, kind `agreement`.** `offer`, `rows_digest`, `side` (`offerer` or `acceptor`), `role`,
`admit`, `intent` (present when the holder sealed an intent for the task), `witnesses`,
`adjudicator`, `forums`, `counterparty_layer`, `counterparty_key`, `checkpoint_key`,
`shelf_log_id`.

- With `counterparty_layer` `false`, `seen` and `counterparty_key` are `null`; with `true`,
  `counterparty_key` names the counterparty's record key.
- The acceptor's agreement comes first: its `seen` is `null`.
- Read against the offer it cites (`xdealv1.CheckAgreement`): `offer` is that offer's record
  digest and the deal ids match; `rows_digest` is the rows digest of the offer's `terms_rows`
  (`xdealv1.RowsDigest`); `witnesses` name the offer's key ids, `adjudicator` is the offer's and
  `forums` are the offer's as a set; the offerer's `role` is the offer's and the acceptor's is the
  other; and an offerer's agreement with a layered counterparty names, in `seen`, the record id of
  the acceptor's agreement it holds.

**`agree`, kinds `decline` and `cancellation`.** `offer`, the digest ref of the offer it answers
or withdraws; on a decline only, `reason` (`not_selected` or `other`); `reason_commitment`, a
free-text reason committed with `sha256-jcs-nonce256`. `reason` and `reason_commitment` are
OPTIONAL. No other member. A decline is signed by the
key the offer was sent `to`; a cancellation by the offer's signer (section 7).

**`perform`.** `act`, `observed`, `detail_commitment`, `statements`, `check` (the passing journal
check it rests on), `remedy_for`.

**`settle`.** `leg` (`payer` or `payee`), `statements`, `check` (REQUIRED on a payer leg),
`supersedes`, `reverses` (the leg a refund reverses), `remedy_for` (`{"row", "ruling"?}`; a
remedy counts by what it gives, so the ruling is never required). The leg itself travels as the
Capsule's `settlement` member, as v0 carries it (draft-mih-agent-settlement-records-01). A leg
of the payment row's own payment has `terms_ref` set to the id of the offer's sent Capsule, which
carries the terms leg (section 7); a payer leg is the owing party's, a payee leg the other's.

**`statement`.** `statements` and `names`, which names one arrived record: a `cancellation` or a
`ruling`, never both. A statement with empty `statements` and no `names` is invalid. An implementation that
receives a deal's records MUST seal a statement naming each counted ruling of one of the holder's
deals from that deal's adjudicator, so the holder's own status reads it whether or not the ruling
went its way (evidencebook#527). This library's `Shelf.Receive` does it before it returns:
`xdealv1.ReceiveProfile.AfterReceive` calls `xdealv1.NameRulings` with the deals whose counted
records grew and the received record's own deal, so a host cannot skip it. A failed naming is
`Receive`'s error, and the holder's next stamp names it again, whether or not anything more
arrives (`xdealv1.StatusProvider.BeforeStamp` runs `xdealv1.NameAllRulings` before each
stamp); a later `Receive` of a record of that deal, the ruling sent again included, names it
too. A deal book holding a record that is not x-deal-v1 (a lifecycle record) cannot take a
statement at its seq and is never named. Until a ruling is named, the holder's status value
counts it in `rulings_unnamed` (a count, no ids). Each record is sealed through
`xdealv1.BookEntry` and appended with `Book.AppendAt` at the seq it was built for, read again
and rebuilt on `ErrBusy`; a host writing its own deal-book records does the same, so a naming
and a host write never seal two records for one place.

**`dispute`.** Kind `dispute`: `row`, `statements` (the record digests of the statements on that
row when it is sealed), `adjudicator` and `forum`. Read against its book's agreement
(`xdealv1.CheckDispute`): its `agreement_ref` cites that agreement, the agreement names an
adjudicator, `adjudicator` is that adjudicator's key id, and `forum` is one of the agreement's
`forums`. Kind `withdrawal`: `row`. Kind `challenge`: `ruling` and `grounds_commitment`.

**`ruling`.** `row`, `keys` (`buyer`, `seller`), `against` (`buyer`, `seller` or `null`), `value`
(`met` or `not_met`), `remedy` (`kind`, `amount_minor` and `currency` together, `due`), `read` (per
side, the last record id of that side's deal book the adjudicator read, or `null` for a book it did
not read), `forum`, `rules_digest`, `final`. Read
against the deal's agreement (`xdealv1.CheckRuling`): `forum` is one of the agreement's `forums`.
That the agreement's adjudicator signed it is the reader's, from the ruling's Capsule.

**`ask`, `answer`.** `ask` holds `request` (the digest ref of the library's request document) and
`to` (`counterparty` or `adjudicator`); `answer` holds `request`, `outcome` (`evidence`,
`refusal` or `absence`) and `answer` (the digest ref of the library's answer document).

**`admit`.** `outcome` (`GO`, `ASK`, `LIMIT` or `NO`), `tier`, `rows`, `asks`, `pack_digest`,
`card_digest`, `answers`, `nonce`. `tier` is the id of the admission pack's tier the decision
admitted at. `rows` are the ids of the pack's rows the decision read, as the pack names them: an
admission pack row id is `^[a-z][a-z0-9_-]*$`, so a row such as `owner-signed` keeps its hyphen.
`pack_digest` is the pack's `definition_digest`. `card_digest` is the SHA-256 of the JCS bytes of
the counterparty's card document the decision read. `asks` and `answers` cite the holder's own
`ask` and `answer` records of the admission. A holder does not seal a decision without a tier
(a NO) as an `admit`: no agreement cites it, and the holder's local admission decision keeps it.
`outcome` keeps `NO` in its enum for compatibility. An agreement cites an `admit` that admitted
the deal it agrees: `GO`, or `LIMIT` at a tier that holds the deal (a `LIMIT` allows a deal whose
value band is within the limited tier). A host refuses to seal an agreement whose `admit` is `ASK`
or `NO`; whether a `LIMIT`'s tier holds the deal is decided where the decision is made, from the
pack's tiers.

**`check`.** v0's `body_check`, plus `payment_ref` and `nonce`. Without `payment_ref` and `nonce`
the body MUST be valid under v0's closed `body_check`.

**A journal record whose digest leaves the holder carries its own secret** (u528: the admission
decision, and what the person checked, stay in the person's own book). A journal record whose
digest leaves the holder MUST carry a member of its own that nobody else can guess. A secret in a
record it cites does not count, since the cited digest may itself be known: an act shows its
check's digest. A digest leaves the holder when it is cited by a sent or deal-book record, signed
beside a record (section 8's person's signatures over an approval, a `confirm_limits` and the
first intent), or disclosed. Otherwise whoever holds the digest could try each likely content
against it.
- `admit`, `check`, `verdict` and `approval` carry `nonce`: a fresh random 256-bit value, 64
  lowercase hex, drawn for each record with `xdealv1.NewNonce` (`crypto/rand`) and never
  reused. `verdict` and `approval` are v0's bodies plus `nonce`, read against v0's closed bodies
  without it. A verdict's digest does not leave the holder under this rule (section 8 signs the
  approval, not the verdict); its nonce is defence in depth.
- An `intent` carries the required `verbatim_commitment`, a `sha256-jcs-nonce256` commitment.
- `xdealv1.CheckAuthorizations` refuses a journal in which two records share a nonce or a
  `verbatim_commitment`.
- A record sent whole (an offer, a decline, a cancellation; section 7) is exempt. Its content
  leaves with it, so its digest tells nobody anything the record does not.
- The citation stays a plain digest ref, and the lookup in the holder's journal is unchanged, as
  with `reason_commitment`.
- A journal record type whose digest a later version lets leave the holder MUST meet this rule
  too.

## 6. `seen`

Every deal-book record carries `seen`. It is `null` only when its author holds nothing counted
from the other side's deal book; otherwise it is the record id of the last record of that book
the author holds (the library's counted prefix), read when the record is sealed. So the
acceptor's agreement has `seen: null` and the offerer's names the acceptor's agreement. A deal
book of a counterparty with no layer has `seen: null` throughout. Journal records, the overlays
in the journal and `ruling` carry no `seen`.

## 7. Records sent whole

An offer, a decline and a cancellation leave the shelf whole. Each is sealed twice: as a journal
book record whose payload commitment is the record digest, and as a standalone Capsule appended to
no log, with `action_id` `"<deal_id>/sent"`, `action_type` `fyi`, no chain, no `references`,
no `evidence_record_header`, and the record as its payload, so its `agent_input_digest` is the
record digest. It travels as `{"record": <the Capsule>, "agent_input": <the record>}`.

An offer whose terms have exactly one payment row (a recomputed row whose `owed` is exactly
`{"amount_minor", "currency"}` in a currency with a known `assetScale`) MUST carry, on its sent
Capsule only, the settlement terms leg as a top-level `settlement` member: `{"version": "0",
"leg": "terms", "sealer_role", "amount"}`. `sealer_role` is `payer` when the offer's `role` owes
that row, else `payee`. `amount` is the row's `owed` as `{"value": <amount_minor as a string>,
"assetCode": <currency>, "assetScale": <its scale>}` (`xdealv1.TermsLeg`; the sender passes it to
`SealSent` as `Entry.Members`). No other sent record carries a `settlement` member, and
`VerifySent` refuses an offer with one payment row that carries none. The price this puts at the
Capsule's top level is the offer's own terms, which `agent_input` carries beside it. The payment
check is `xdealv1.PaymentCheck`, AAC's `settlement.Verify` over the terms leg and the two sides'
legs. It is not run when the terms have more than one payment row. It runs on the payment's own
legs only: a refund or remedy leg (`reverses`, `remedy_for`) that cites the offer's terms leg is
not checked, and is listed as such (`not_checked`; a status row's `check_not_checked`), so a
refund never hides a mismatch in the payment it follows (Steven, evidencebook#722). A remedy that
is a payment is not counted until it has its own terms leg, which this profile does not define
yet.

`xdealv1.VerifySent` reads such a pair on its own: the Capsule's signature, that the payload is
the record whose digest is `agent_input_digest`, that the record passes sections 1 to 5, and,
given the offer, that `offer` cites it and the signer is the one section 5 names, and that an
offer with one payment row carries exactly its terms leg and that no other record carries a
`settlement` member. The pair does
not show the record's place in the sender's journal, nor when it was written: `at` is the
sender's word.

## 8. Authorization of acts

An act is a deal-book `perform` or `settle` that cites, in `check`, the check in its author's
journal that it rests on. A payer leg always cites one. x-deal-v0's authorization conditions
(x-deal-v0 §6, rules 5 and 6) hold unchanged, with that citation in place of v0's
`authorized_by`: the check is of the act's action (`pay` for a payment leg, `cancel` for a leg
that `reverses`, `commit` for a `perform`) and serves the act's deal; its verdict does not deny;
the verdict's first approval proceeds, is not a `confirm_limits` and was sealed no later than the
act; one approval authorizes one act; no journal record between the check and the act changes the
details; and the limits in force are those the journal's intents and confirmed proposals set, in
journal order. The journal is read in order and its `at` never decreases: a journal record whose
`at` is earlier than the record before it is refused.

`xdealv1.CheckAuthorizations(journal, acts)` checks them, refusing at the `authorization` stage,
and returns per act the record digests the person signs. A settle's leg travels in its Capsule's
`settlement` member, which the record does not carry, so rule 5's amount condition needs
`xdealv1.CheckActs`, which is the same check given each act's settlement member as well. A caller
holding the Capsules uses it. Where both the leg and the check carry them, the leg's asset code
equals the check's `currency`, and its settled amount equals the check's `amount_minor`; on a
`cancel` check that states no `amount_minor` and no `fee_minor` (or `fee_minor` 0), a refund leg's settled amount equals
its `cancelled_amount_minor` (a seller's outgoing cancel records the amount there); with a
`fee_minor` other than 0, who bears the fee is not recorded, so no amount is held, and `CheckActs`
says so (`AmountNotHeld`). A payer leg
settles its `amount`, and a payee leg settles `received` plus `receive_fee`; a payee leg that states no `receive_fee` is refused (0
when none was taken). Amounts compare as
integers at the larger scale, with `amount_minor` read at the currency's minor units. A checked
amount in a currency with no known minor units is refused. The record digests the person signs:

- the approval, when it is the person's own (approver `user` or `agent_card`): the person's
  signature over the approval's record digest. The record's Capsule is signed by the holder's
  record key, the agent's, which never stands for the person's.
- every `confirm_limits` approval whose confirmed proposal put in force the `allowed` a
  `standing_intent` approval relied on (`LimitApprovals`): the person's signature over each one's
  record digest. Without it the agent could widen its own limits and then approve its own act.
- the first intent a `standing_intent` approval rests on (`Intent`): the person's signature over
  its record digest (Steven, evidencebook#500, option b). Without it an agent could seal its own
  intent's `allowed` and approve acts inside it with no person's signature anywhere. A
  `standing_intent` approval with no intent before it in the journal is refused.

An act MUST NOT be relied on until the person's signature over each listed digest, from the
person's own key, is held. The signature travels beside the approval record, never inside it, so
it changes no record format; the member that carries it is defined in
action-state-group/agent-action-capsule#627, not here (section 11). The person's signature
certifies what the approval already says, no more: on a `user` approval, the person's own answer;
on an `agent_card` approval, that the person answered the card the agent composed, which, as in
x-deal-v0 §6 rule 6, is not consent in the person's own words.

**The first-act gate** (Steven, u610). A party's first `perform` or payer `settle` of a deal is
refused until the counterparty's stamp proof covers the counterparty's agreement, on both sides,
whether or not the agreement names a witness (`xdealv1.ReceiveProfile.GateAct`, over `CanAct`). A
payee leg, a statement and any later act are never gated. The offerer stamps its agreement at once
when its first act waits on the acceptor's, so the two never wait on each other. With a
counterparty that runs no layer, the first act goes ahead unguarded and is not refused (Steven,
u611); the receipt and the card say the other side sent no stamp proof, counted from the holder's
receipt only.

## 9. Checks, stages and fixtures

A refusal names one stage:

| Stage | What it checks | Go |
|---|---|---|
| `canonicalization` | the text, the integers, the block's `canonicalization` | `Check` |
| `schema` | `x-deal-v1.schema.json`, and v0's `body_check` for a check | `Check` |
| `record` | sections 2 to 6 on one record | `Check`, `CheckSealed` |
| `agreement` | an agreement against its offer, a dispute or ruling against its agreement | `CheckAgreement`, `CheckDispute`, `CheckRuling` |
| `authorization` | section 8 | `CheckAuthorizations` |
| `sent` | a pair sent whole | `VerifySent` |

`fixtures/` holds the deal of Appendix A as positive fixtures, each with its expected record
digest (also in `fixtures/expected-digests.txt`), and `fixtures/negative/`, one record per check
above that the schema or the record checks can reach, each with the stage and the call that
refuses it. Every implementation of this profile MUST give each positive fixture its expected
digest and refuse each negative at its stage. The vectors in `testdata/deal-v1/` cover the
`deal_id` presence (`records/`), the decline and cancellation pairs (`decline/`) and the
authorization of acts (`authorization/`).

`check_profile.py` is the reference checker in Python, independent of the Go package. Run with no
arguments, it checks every fixture: each positive at every stage and against its expected digest,
the agreements, the dispute and the rulings against the records they are read against, each
positive book's `seq`, `prev` and `agreement_ref`, and each negative refused at its stage with its
text. It ends "ALL OK"; without `jsonschema` it skips the schema stage, lists the negatives it could
not run, and ends "OK, reduced coverage". Given files, it checks each record on its own. It does
not check the authorization of acts (section 8) or a pair sent whole (section 7): the Go package's
vectors in `testdata/deal-v1/` do.

## 10. The sealed deal report

A v1 deal seals no Evidence Result (evidencebook#485). A receipt page reads one sealed report
instead: a snapshot the holder signs of each row's status as it computed it at `as_of` from what
it held, so a page displays it and never recomputes it (design note §9). It is v0's report
Capsule (`action_id` `capsulectl-deal-report`, payload `type` `deal_report`) with `profile`
`x-deal-v1`. It is always standalone: appended to no book, read by no status input, cited by no
record. `xdealv1.BuildReport` computes a copy and `xdealv1.StatusProvider.Report` computes one
from a shelf; the vectors are in `testdata/deal-v1/report/` and `testdata/deal-v1/settled/`.

- **Members.** `as_of`, `deal_checkpoint`, `pack_digest`, `start` (Start's result at `as_of`),
  `settled`, `rows_state` and `rows` (each agreed row's rowstatus result at `as_of`, its `check`,
  its `late` statements and disputes, its effective ruling and the records it relied on), and
  `rests_on`, as design note §9 gives them. A page shows them as they are.
- **The payment legs.** A payment row's `relied_on` also lists each settle leg
  `xdealv1.PaymentCheck` read for the row, after the records it lists otherwise and once each; a
  leg the check excluded, or a check not run over legs, adds none. A page shows each leg's
  settlement member as recorded, found by its `relied_on` id in the bundle extension below; it
  picks no leg, and `check` stays the only verdict.
- **What a page must not misread** (Sherlock2, evidencebook#526 and #528):
  - `rests_on.counterparty_kind` is null while nothing states it; a page shows "not stated".
  - `rests_on.counterparty_proof` reads the counterparty's layer and the held proof together:
    `held`; `not_yet`, a layered counterparty whose proof has not arrived; or `no_layer`, a
    counterparty that sends no stamp proof, which a page words as u611 does: the other side sent
    no stamp proof, counted from our receipt only.
  - A row's `remedy` is `not_counted` when its ruling orders a payment (a refund): no payment
    remedy is counted until the profile defines its terms leg, so the report never says such a
    remedy is due or not honoured. A page shows "remedy is a payment, not checked". The deal is not
    settled while one is. `remedy_made` says the owing side's book holds a remedy for the row,
    ruled or not, and that record is in the row's `relied_on`.
  - A refund after a concession settles: an `agreed` row whose value is `not_met` counts toward
    `settled` only with `remedy_made` (Steven's SH-16 ruling). A ruled refund does not settle yet,
    because the ruling sets an amount and a date the report cannot check until the remedy's terms
    leg is defined.
  - `disputes` lists one entry per `dispute` record, with its outcome. `rulings_unnamed` (absent
    at zero) counts the adjudicator's counted rulings the holder holds that neither book names; a
    page shows "ruling held, unnamed" beside an `open` outcome.
  - `status_fact` is null before the book's first status fact, else whether that fact is the
    status computed now. A page never presents a fact that is not current as the deal's status.
- **What it rests on.** A report is computed from what the holder held at `as_of`: no record it
  rests on is timed after `as_of`. From a shelf, it rests on the holder's book at its latest
  checkpoint, which covers the whole book; making a report writes nothing, the journal included.
- **Audiences.** `keep` and `counterparty` copies list the rows. An `adjudicator` copy names its
  `referee`, has `rows_state` `withheld` and no rows, and carries neither the counterparty's
  prefix, the witnesses, the disputes nor `rulings_unnamed`.
- **Verifying a report.** `xdealv1.VerifyReport` reads a report against its deal's first record,
  the agreement at seq 1 of the holder's deal book. Among its checks (design note §9, "Verifying a
  report"), it refuses a report not signed by the key that signed that record, and a row whose
  `ruling.against` is not `buyer`, `seller` or `null`. A report that passes shows who signed it,
  not that its rows are correct. The vectors are in `testdata/deal-v1/report-verify/`.
- **No close.** The payload has no `lifecycle` and no rules verdict. A page's deal-level line is
  `start.state` and, for a started deal, `settled`.
- **The bundle extension.** A bundle that carries a v1 report carries the extension `x-deal-v1`:
  `{"sealed_report": "<the report Capsule's id>", "book": {"agreement": <pair>, "relied_on":
  {"<record id>": <pair>, ...}}}`, each pair a book pair `{record, header, payload}`. `agreement`
  is the holder's deal book record at seq 1; `relied_on` holds every record the holder's rows rely
  on at `as_of`, whatever the copy's `rows_state`, so the owner's (`keep`), counterparty's and
  adjudicator's copies carry the same records, and `{}` when no row relies on one. A bundle for
  anyone else carries no report and no extension. The host builds and seals the bundle;
  `xdealv1.StatusProvider.ReportWithBook` returns a report and its book from one snapshot.
  `xdealv1.VerifyReportBundle` reads a report with its extension (design note §9, "The payment
  legs and the bundle extension"): among its checks, each pair is signed by a party of the
  agreement, the holder's and the counterparty's sit in the book or counted prefix the report
  says it rests on, and, when the rows are listed, the pairs are exactly the records they rely on.
  An adjudicator's records have no log bound, and an adjudicator's copy, which names no
  counterparty prefix, binds the counterparty's records to one log only; in a copy that lists no
  rows, nothing ties the pairs to rows. Rust has `deal::v1::verify_report_bundle` and Python
  `evidencebook.deal.v1.verify_report_bundle` (evidencebook#598). The vectors
  are in `testdata/deal-v1/report-bundle/`.

## 11. Not defined here yet

The member that carries the person's signature beside an approval (section 8) is defined in
action-state-group/agent-action-capsule#627.
