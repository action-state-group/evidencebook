# evidencebook#607: the seller's verdict on an agent claim, sealed apart from the claim

Design note for [evidencebook#607](https://github.com/action-state-group/evidencebook/issues/607).
Written 2026-11-21 CST at main `cd9ee08`. It fixes the x-deal-v0 record shape, the copy rules,
the checks in Go, Rust and Python, and the vectors. The producer side (what `disclose` and
`deal report` put in each copy) is evidencebook-deal#244, which follows this note.

## 1. The bar

Steven's ruling u615 (2026-11-19, ops `company-oss/inbox.md:944`): a seller may include, on an
adjudicator copy only, the claims its agent made without authority, by an explicit per-share
opt-in, default off, and **never on the counterparty copy**. Sherlock confirmed that keeping the
claim and moving the seller's verdict off it is a fix under that ruling, not a new consent
question (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=2abeb751210e53195d88667f4c9babdf6c5d6545790c54d22859e79067d017c4).
Steven's 2026-11-21 ruling on the adjudicator copy ("yes, uniformly": it hides the claims and
their words unless the seller opts in;
buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=7d9210a9ffeaa627876cbb939d9ef3c61ac0ed9520d2cc118f2a29f72b5bf87d)
is carried out by evidencebook-deal#231 and is kept as is here.

When this item and evidencebook-deal#244 are done, a counterparty copy of a seller's deal carries
every agent claim's committed words, and the buyer can still check the words it was told against
them, while nothing in the copy says which claims the seller's agent made without authority, apart
from the positional limit in section 7.

## 2. What exists today

- **The claim record carries the verdict.** Under rule 12 (`PROFILE.md:700-765`) an agent claim
  carries either `authority_basis` + `evaluation_ref` + one `authorized_by` ref, or
  `unchecked: true` + `rule` (`PROFILE.md:204`, the schema's `claim` at
  `x-deal-v0.schema.json:232`, Go `types.go:117-128`, Rust `deal/types.rs:190-206`). The record
  digest binds every byte, and a copy discloses a record whole or not at all
  (evidencebook-deal `internal/deal/deal_share.go:71-75`).
- **The checks** read the verdict off the claim: Go `chain_steps.go:255-297`, Rust
  `deal/chain.rs:1579-1650`, Python `check_profile.py:1338-1368`. Python's own package runs no
  chain rules; the reference checker does (`python/evidencebook/deal/check.py:6`).
- **The counterparty copy shows the whole authorization trail.** A probe on evidencebook-deal
  main `4ae1b63` (a sale thread with an ordered condition claim, an approved warranty claim and an
  unchecked refund claim, `disclose --share counterparty`) gives these disclosed steps:
  the represent snapshot ("About to tell the buyer about the condition"), its check ("Check: no
  differences"), the claim; the warranty snapshot, its check ("flagged 1 difference"), the user's
  approval ("Your answer: proceed"), the claim; then the refund claim with no check before it. The
  raw copy contains `"unchecked"`, `"authority_basis"` and `"representing"`. So the verdict leaks
  three ways, not one:
  1. the claim's own `unchecked`/`rule` and `authority_basis` members;
  2. the represent check's `representing.text_commitment`, which equals an authorized claim's
     `text_commitment` because the claim is sealed under the check's nonce (`PROFILE.md:742-745`),
     so equal values say "these words were checked";
  3. the check, evaluation and approval records themselves, and the report lines naming them;
  4. the gate on ordered classes. A second probe (an ordered `condition` claim made with no check,
     then an offer check) gives a disclosed offer evaluation carrying the finding
     `required_representation_missing` right after the disclosed claim: the claim of an ordered
     class was not authorized. The finding sits on the evaluation of every gated check (`offer`,
     `commit`, `sign`, `pay`, `share_contact`, `share_credentials`, `PROFILE.md:758-763`), and an act
     the gate holds is sealed unchecked with that rule (evidencebook-deal `internal/deal/deal.go:1724-1746`).
- **The rule inputs read the verdict off the claim too.** On a seller's check, `deal_claims` gives
  each earlier agent claim with "the claim's own `authority_basis`, exactly as sealed, or
  `unchecked: true`" (`PROFILE.md:978-984`); evidencebook-deal builds it from the sealed claim
  (`internal/deal/rules_checker.go:727-749`).
- **x-deal-v1 is not affected.** Its claims are journal records only (`x-deal-v1/PROFILE.md:95`,
  `122`), and no shared copy carries the journal.

## 3. The two shapes considered

**A. A separate verdict record (chosen).** The claim record keeps the words and nothing about
authority; a new record right after it, `claim_authority`, carries the verdict and the
`authorized_by` ref. A copy withholds it like any other record.

**B. A per-audience projection.** The claim keeps one record, with the verdict replaced by a
commitment (`authority_commitment`) whose opening only the own and adjudicator copies carry.

B is rejected:
- the `authorized_by` ref sits in the record's `refs` (`PROFILE.md` section 2), so it would have to
  move inside the committed object too, and then the profile checker could no longer check rule 12
  from the records alone: it would need every opening as an input, a new checker contract in
  three languages;
- the adjudicator opt-in (evidencebook-deal#232) and every other copy rule work by withholding
  whole records; a projection adds a second disclosure mechanism for one field;
- neither shape alone fixes leaks 2 and 3 of section 2. Both need the same copy rule for the
  represent trail (section 5), so B buys nothing there.

## 4. The record

**`claim_authority`** (the 24th record type of x-deal-v0): the seller's own record of what
authorized its agent's claim, or that nothing did.

| | |
|---|---|
| Body | exactly one of: `{authority_basis, evaluation_ref}` (as the claim carried them), or `{unchecked: true, rule}` (`rule` from rule 12's two lists). No other member. |
| Refs | exactly one `about` → the claim it judges; with `authority_basis`, exactly one `authorized_by` → the evaluation (DO) or the user's approval (ASK), as on the claim today. No other ref. |
| Block | no `counterparty`, no `channel`. |
| Place | the record right after its claim (`seq` + 1). Every claim under rule 12 that carries no verdict of its own has exactly one, and no other record has one. A deal whose last record is such a claim is valid (its verdict is not sealed yet, so the gate does not yet count its class); any record after it must be its `claim_authority`. |

**The claim** (`source_kind: agent`, under rule 12) then carries `text_commitment`,
`source_kind`, `class` and optionally `source_ref_commitment`, and no `authority_basis`,
`evaluation_ref`, `unchecked`, `rule` or ref. Its `class` stays in the clear: the buyer's page
labels the words by class, and the class of a claim the buyer was told is not the seller's
verdict.

**The rules move unchanged.** Every rule 12 check now reads the claim through the
`claim_authority`'s `about` ref and checks the verdict there: one of the two forms; an unchecked
verdict names no `authorized_by`; an authorized one rests on a `represent` check whose class and
commitments equal the claim's; the check is unused, within `valid_until`, under the task authority
in force, with no detail change between the check and the claim (the claim's position, as now);
an unordered class or `attributed_to: "user"` needs the user's own approval. The check's
`valid_until` is compared with the claim's `at`, not the `claim_authority`'s: the words were said
when the claim was sealed, and a `claim_authority` sealed later (below) must not expire a valid
authorization or revive an expired one. An ordered class counts as stated (the
`required_representation_missing` gate) from its authorized `claim_authority`, never from the
claim. The failure messages stay the same where the rule is the same.

**Sealing the pair.** A producer appends one record at a time, so it can stop between the claim and
its `claim_authority`. Before it seals any other record of the deal, a producer seals the missing
`claim_authority` of a trailing new-shape claim, with the verdict as of the claim: the check and
approval the claim was noted with, the claim's `at` for `valid_until`. The verdict is computed when
the claim is noted and depends on nothing sealed after it, so the recovery seals the same verdict.

**The rule inputs.** `deal_claims` keeps its shape. For a claim in the new shape, its
`authority_basis` or `unchecked: true` is read from the claim's `claim_authority`; for an old-shape
claim, from the claim, as now.

## 5. The copy rules (normative, for producers)

PROFILE.md gains a paragraph under rule 12 and section 4's "Where openings travel":

- **The user's own copy** carries everything, as now.
- **A counterparty copy** of a seller's deal:
  - discloses each agent claim sealed in the new shape, and opens its words as now
    (`representations`, `claim_openings`: `PROFILE.md:497-500`), so the buyer's check of the words
    is unchanged;
  - withholds every `claim_authority` record;
  - withholds the represent trail: every `check` whose action is `represent` (typed: the
    `proposed-action/v0` with `representing`), its `action-evaluation/v0`, and every
    `action-approval/v0` or `approval` of such a check, whatever its answer;
  - where the deal follows rule 12, withholds every `action-evaluation/v0` (the seller's check of
    any step, whatever its disposition) and every outcome whose `status` is `unchecked_action`, so
    the `required_representation_missing` finding (leak 4) is in no disclosed record. Withholding
    only the records that carry that finding would single them out;
  - where the deal follows rule 12, also withholds (evidencebook#613, found in
    evidencebook-deal#244's plan review), by kind and whatever each record's result or answer:
    - every answer to a check (`action-approval/v0` of authority `user_approval` or
      `card_answer`), since a `hold` on a gated check shows the gate where an authorized twin has
      no answer at all. The `counterparty_acceptance` and `platform_approval` observations answer
      no check and stay;
    - every telling (every `disclosure`, and every `action-record/v0` with `disclosed`): a share
      the gate holds is sealed with `authority: none` and `rule: required_representation_missing`
      (evidencebook-deal `deal_profile.go:1279-1294`), and withholding only those would branch on
      the verdict (Sherlock's condition 1); the buyer knows from its own side what it was told;
    - every `close`: its `unchecked_actions` counts the unchecked acts (evidencebook-deal
      `deal.go:2550`). The report says how the deal ended from the outcome the close refs, which
      the copy discloses unless it is an `unchecked_action` outcome; a close on an unchecked
      outcome leaves the end "not shared". A late `evidence` or `rules_verdict` that `confirms` the
      withheld close still verifies (its ref is a digest). The counterparty's section 10 admission
      of this party's half then has no payload to read, so it no longer sees that the half is a
      final close; the half's opening still verifies;
    - and discloses every `terms_pack`, the x-deal-v0 record evidencebook#613 adds: the deal-terms
      pack a check ran under, sealed right after its evaluation with one `checks` ref and only
      `pack_digest`, so the buyer's "under the seller's terms, pack X" line (evidencebook-deal#145)
      survives the withheld evaluation. Sherlock chose to keep the line this way and confirmed the
      shape on four conditions: deal kind only (a producer rule no checker here can see, since a
      typed evaluation seals no kind; evidencebook-deal#244 tests that a conduct pack seals no
      `terms_pack`), the same per-role audience as today, one per pack, and the digest checked
      against the evaluation
      (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=beacc21982dc9dfd848ed3cb86ff83a5c9b4a5c3ee399800ffd09a62dab8cc41&thread=5d78d8545f22a3e27ccdf1fac5266bad8afc474f69c151e9b52d7b6177481260,
      buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=d4fc9af52bc6ceb792637abb3075cebf12b8b49bab55982aba034c30cd545863&thread=5d78d8545f22a3e27ccdf1fac5266bad8afc474f69c151e9b52d7b6177481260);
  - says nothing derived from a record this section withholds: its report (`steps`, `did`,
    `told`, `anomalies` and every other section) names such a record by no kind, line, result or
    count of its own beyond its place in the log (as a shared copy already treats the
    counterparty-profile step), carries no "Check: no differences" or difference count for it, no
    count of unchecked acts, and no "unapproved" or "without authority" item. evidencebook-deal#244
    tests it by comparing the counterparty copies of two deals that differ only in whether an
    ordered claim was authorized: their report text and disclosed records differ in nothing but
    digests, capsule ids, commitments, the number and places of withheld steps, and the steps
    section 7's second limit names. `x-deal-v0/fixtures/counterparty-copy-vectors.json` holds
    such twins with the exact set a counterparty copy withholds under this section and the set it
    must disclose; Go's `dealprofile.CounterpartyWithheld` and `RequiredDisclosed` and
    `check_profile.py` compute both, and evidencebook-deal#244 compares its copies with them;
  - withholds every agent claim sealed in the old shape (carrying a verdict), all of them,
    authorized or not, and opens none of their words (there is no disclosed record to check them
    against). Section 6 says why.
- **An adjudicator copy** is evidencebook-deal#231's: by default it withholds every agent claim,
  every `claim_authority` and the represent trail (Sherlock on evidencebook-deal#245,
  buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=effe0673354608775358f3fa42a5ac47106fb4a3f0e34b99b6679f2b6667a5b5);
  with the seller's opt-in (all or nothing per deal) it carries all of them.

## 6. Versioning and existing books

- **Within x-deal-v0, additive**, as `rules_verdict` and `outcome_note` were added: a new record
  type in the closed set, and a claim shape with fewer members. A checker built before this
  refuses a book holding a `claim_authority` (an unknown `record_type`), which is the closed set's
  purpose; the new checkers accept both shapes. No profile version, no digest of any existing
  record changes.
- **Per claim, then fixed.** An agent claim under rule 12 is in the old shape (verdict on it,
  sealed by an earlier producer) or the new one (followed by its `claim_authority`). Once a deal
  holds a `claim_authority`, a later agent claim in the old shape is refused, so one producer
  upgrade moves a deal forward once and never back. A deal or thread opened before rule 12 keeps
  the earlier rules, as now.
- **Existing books are not rewritten**: their records are sealed and their digests are in
  checkpoints others hold. Old-shape claims exist only in seller deals opened since rule 12
  (capsule-cli#269). Their counterparty copies withhold every old-shape agent claim, uniformly, so
  the buyer loses the word check on those claims and learns nothing about which were authorized;
  the page says the claims sealed before this change are "not shared" (u615's word). New claims in
  the same deal are shown. Copies already shared before this change still carry the verdict
  fields and cannot be recalled; the CHANGELOG says so.

## 7. Known limits: positions, and steps resting on a held act

A withheld record keeps its place in the log; that is what lets a copy verify. An authorized claim
follows its withheld check (and approval); an agent claim made with no check at all (`no_check`)
may follow no withheld record. So a buyer who counts the withheld steps before a claim can guess,
without proof, that a claim with none before it had no check. The guess is not proof: other
records are withheld too (messages, evidence and notes with private details), a `no_check` claim
can follow any of them, and `not_approved`, `differs_from_check`, `changed_after_check`,
`authority_changed` and `check_expired` claims follow a check exactly as authorized ones do.
Closing it would mean hiding log positions, which every copy's verification rests on. The same
residual was kept, stated, as a reviewer's finding in evidencebook-deal#231's plan review (not a
ruling). Steven accepted this limit on the condition that PROFILE.md and the CHANGELOG state it
(section 9).

**Steps resting on a held act (evidencebook#613).** An act the representation gate held that the
agent did anyway is sealed unchecked and is not in force: only an approved proceed adopts an
offer's terms (evidencebook-deal `deal_rules.go:1178`), and an acceptance is recorded only for an
offer act that was authorized (`deal.go:623-634`). So the unauthorized twin has no recorded
acceptance, no commit or address share resting on one, and an end outcome that compares the
delivery with the terms in force without that offer (`mismatch` where the authorized twin says
`completed`). Its counterparty copy lacks steps the buyer may know happened. A buyer who knows they
happened can tell they were not authorized, never why. Unlike the position hint, this can prove that a step was not
authorized. Closing it would mean withholding every act and outcome, including the offers rule 10
gives the buyer in full. Sherlock routed it to Steven with the recommendation to accept it as a
stated limit (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=dc33a5bc87d97eefbfca7fc7e858f7837d9ddaa2f57749752a296f695f9590c2&thread=5d78d8545f22a3e27ccdf1fac5266bad8afc474f69c151e9b52d7b6177481260).

## 8. Work in evidencebook

| Part | Files |
|---|---|
| Profile text | `PROFILE.md`: section 2 (24 types), section 3 table (`claim` row, new `claim_authority` row), section 4 (where openings travel), rule 12 (the verdict record, the copy rules, old and new shape) |
| Schema | `x-deal-v0.schema.json`: the `claim_authority` record type, its body and refs; the claim's verdict members kept for the old shape |
| Go | `names.go` (`TypeClaimAuthority`), `types.go` (`ClaimAuthorityBody`), `record.go` (the body type), `chain.go` (the rel table: `claim_authority` takes `about` and `authorized_by`, `claim` keeps `authorized_by` for the old shape; `verifyBasis` takes the claim's `at`), `chain_steps.go` (one rule 12 routine called from the old-shape claim and from `claim_authority`; the trailing-claim and old-after-new rules) |
| Rust | `deal/names.rs`, `deal/types.rs`, `deal/record.rs`, `deal/chain.rs` (rel table and the same routine), and the vendored schema `rust/evidencebook/schemas/vendor/x-deal-v0/` |
| Python | `check_profile.py` (the reference checker, which also generates the fixtures and `testdata/chain-vectors.json`), `python/evidencebook/deal/names.py`, the vendored schema `python/evidencebook/deal/_data/schemas/`; `x-deal-v1-types.json` only if `TestTypesTable` regenerates it |
| Vectors | new fixtures run by all three (`fixtures_test.go`, `rust/evidencebook/tests/deal_chain.rs`, `python/tests/test_deal.py`): a positive seller thread with an ordered claim (DO), an approved claim (ASK) and an unchecked claim, each followed by its `claim_authority`; a positive deal holding old-shape claims; a positive deal ending at a new-shape claim (verdict not sealed yet); a positive deal holding
old-shape claims then new-shape ones; the gate: an authorized ordered `claim_authority` then a
gated check that passes, and an unchecked one then a gated act that fails with the gate message;
`valid_until`: a claim inside it whose `claim_authority` is sealed after it (valid) and a claim
after it (refused, `check_expired`); negatives: a new-shape claim followed by any other record, a `claim_authority` not right after its claim, one `about` a non-agent claim, an unchecked one with `authorized_by`, two for one claim, verdict members on both the claim and its `claim_authority`, an old-shape claim after a `claim_authority`, and each rule 12 failure on the new record (wrong class, other words, used check, expired check). `expected-digests.txt` gains the new records. |
| Changelog | `changelog.d/` fragments for Go, Rust and Python |

**Copies and the checkers.** The chain checks take a whole book (the holder's own records: nothing
in `dealprofile/chain*.go` reads a withheld record, and no production code runs them on a copy). A
shared copy is verified as a bundle (disclosures and positions), so a withheld `claim_authority`
or represent trail is never read as missing by them.

**Not in evidencebook:** the copy rules of section 5 are carried out by evidencebook-deal#244
(`dealWithholdRecords`, the report's step kinds, the old-shape withholding), and sealing the two
records on `note --kind claim` is evidencebook-deal's too. evidencebook-deal#244 needs this in a
release: the evidencebook Go module (its checker refuses a `claim_authority` until then) and, since
it runs the reference checker, the same tag's `check_profile.py`.

## 9. Doors

- A new public record type name and claim shape in x-deal-v0, in a public repository. It carries
  out u615 and adds no product meaning (the verdict is the one rule 12 already seals, now in its own
  record); engineering's call with the ruling linked, and Sherlock2's door confirmation that it
  carries out u615 and stays inside the neutrality boundary.
- The copy rule that withholds the represent trail from the counterparty copy changes what the
  buyer sees (today it sees the seller's checks and approvals of representations). It is needed for
  u615 (section 2, leaks 2 and 3), and it is part of the same door confirmation.
- Sherlock confirmed the first two (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=0597a2f1589b5578d4ff3f083c97e4f482f480aa344bbcbf1cfaa1714bb8579e&thread=5d78d8545f22a3e27ccdf1fac5266bad8afc474f69c151e9b52d7b6177481260).
- Two calls were Steven's, each with Sherlock's recommendation "yes" (same thread): the old-shape
  claims a buyer no longer sees on existing deals (section 6), since no format change can reach
  them (Sherlock2's rule in 2abeb751), and the positional limit (section 7). Steven: "607 yes"
  (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=c0fa0900a46e53bdd791f7179cbc8b555064113ca118608b6cfaecc6e179a993),
  recorded by Sherlock as (1) old-shape claims withheld uniformly with no flag and (2) the
  position hint accepted, its limit stated in PROFILE.md and the CHANGELOG (ops 58970b48;
  buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=f1bfdf6cd0d1548052f4e4cc08e8f20610571c18231eae9ab07d5ec7d3b8b3e5).
- Withholding every evaluation and every unchecked act's outcome from a rule 12 deal's
  counterparty copy (leak 4): inside u615, no Steven call (Sherlock,
  buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=ebc66c63feee9b572ae7f483687ab8d375cce8d25f6f41a2ff84457fd93160e6&thread=5d78d8545f22a3e27ccdf1fac5266bad8afc474f69c151e9b52d7b6177481260),
  on two conditions: the rule is scoped by deal ("this deal follows rule 12"), with no per-record
  branching inside such a deal; and evidencebook-deal#244 tests that on the probe deal (an ordered
  claim with no check, then an offer) the string `required_representation_missing` is in no
  disclosed record of the counterparty copy and the copy still verifies.
- evidencebook#613's additions (every answer to a check, every telling, every close): inside
  u615, no new product meaning (Sherlock,
  buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=69334be4443defcbd62abab6c808751efffd19ef2be2a413ddbcec1d7dc06e7a&thread=5d78d8545f22a3e27ccdf1fac5266bad8afc474f69c151e9b52d7b6177481260),
  narrowed to answers to a check, and widened to every telling in place of only the unauthorized
  ones, Sherlock's decision (withholding only the latter branches on the verdict); the report says
  "not shared" where no end outcome is disclosed
  (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=dc33a5bc87d97eefbfca7fc7e858f7837d9ddaa2f57749752a296f695f9590c2&thread=5d78d8545f22a3e27ccdf1fac5266bad8afc474f69c151e9b52d7b6177481260).
  The second limit of section 7 is Steven's call, asked in the same message.

## 10. Test plan

Each implementation passes the shared fixtures (positive and negative, same verdicts and the same
failure for each negative). Go and Rust unit tests cover each rule 12 branch on the new record and
the old-shape branch. `check_profile.py` runs over the fixtures in CI as it does now. The
evidencebook-deal side tests the copies (#244).

**evidencebook#613 adds:** `counterparty-copy-vectors.json`, whose sets Go
(`CounterpartyWithheld`, `RequiredDisclosed`) and `check_profile.py` compute and whose chains Go,
Rust and `check_profile.py` check, with its negatives (a `terms_pack` of a pack of kind
`conduct`, of a pack with no kind, after rules that did not run); the `terms_pack` fixture and
negatives in `positive-typed-seller-represent`; and a Go test that the twins' disclosed records are
equal once digests, commitments and times are set aside, apart from gap 3's steps.
evidencebook-deal#244 tests, on its own copies:
- the twin pair as a standalone seller deal and as a sale's thread (a thread carries its sale's
  intent unchanged, evidencebook-deal `deal_profile.go:1494-1497`, so its baseline puts it under
  rule 12 as `CheckChain` and `CounterpartyWithheld` read it), comparing report text and disclosed
  records;
- Sherlock's two close conditions: the report says how the deal ended, or "not shared", and a
  late `evidence` or `rules_verdict` confirming the withheld close still verifies;
- `terms_pack`: a check under a conduct pin, or a pin with no kind, seals none; the four
  pack-receipt tests pass unchanged outside rule 12 and give the same line from `terms_pack`
  inside it.
