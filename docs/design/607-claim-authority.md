# evidencebook#607: the seller's verdict on an agent claim, sealed apart from the claim

Design note for [evidencebook#607](https://github.com/action-state-group/evidencebook/issues/607).
Written 2026-11-21 CST at main `cd9ee08`. It fixes the x-deal-v0 record shape, the copy rules,
the checks in Go, Rust and Python, and the vectors. The producer side (what `disclose` and
`deal report` put in each copy) is evidencebook-deal#244, which follows this note.

## 1. The bar

Steven's ruling u615 (2026-11-19, ops `company-oss/inbox.md:944`): a seller may include, on an
adjudicator copy only, the claims its agent made without authority, by an explicit per-share
opt-in, default off, and **never on the counterparty copy**. Sherlock2 confirmed that keeping the
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
  3. the check, evaluation and approval records themselves, and the report lines naming them.
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
| Place | the record right after its claim (`seq` + 1). Every claim under rule 12 that carries no verdict of its own has exactly one, and no other record has one. |

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
an unordered class or `attributed_to: "user"` needs the user's own approval. An ordered class
counts as stated (the `required_representation_missing` gate) from its authorized
`claim_authority`. The failure messages stay the same where the rule is the same.

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
  - names a withheld record of these kinds by no kind, line or count of its own beyond its place in
    the log (as a shared copy already treats the counterparty-profile step), and carries no
    "unapproved" or "without authority" item;
  - withholds every agent claim sealed in the old shape (carrying a verdict), all of them,
    authorized or not, and opens none of their words (there is no disclosed record to check them
    against). Section 6 says why.
- **An adjudicator copy** is evidencebook-deal#231's: by default it withholds every agent claim,
  and now every `claim_authority` too; with the seller's opt-in (`--include-unauthorized-claims`,
  all or nothing per deal) it discloses every agent claim and every `claim_authority`. The
  represent trail in an adjudicator copy is unchanged here (#231 records its positional residual).

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
  the page says the claims sealed before this change are not shown. New claims in the same deal
  are shown. This is the only change a buyer sees on an existing deal.

## 7. Known limit: positions

A withheld record keeps its place in the log; that is what lets a copy verify. An authorized claim
follows its withheld check (and approval); an agent claim made with no check at all (`no_check`)
may follow no withheld record. So a buyer who counts the withheld steps before a claim can guess,
without proof, that a claim with none before it had no check. The guess is not proof: other
records are withheld too (messages, evidence and notes with private details), a `no_check` claim
can follow any of them, and `not_approved`, `differs_from_check`, `changed_after_check`,
`authority_changed` and `check_expired` claims follow a check exactly as authorized ones do.
Closing it would mean hiding log positions, which every copy's verification rests on. The same
residual was accepted for the adjudicator copy in evidencebook-deal#231's plan review. Sherlock2's
door confirmation is asked whether this limit stays inside u615; if not, it goes to Steven as the
brief says (the buyer loses the check, or sees the flag).

## 8. Work in evidencebook

| Part | Files |
|---|---|
| Profile text | `PROFILE.md`: section 2 (24 types), section 3 table (`claim` row, new `claim_authority` row), section 4 (where openings travel), rule 12 (the verdict record, the copy rules, old and new shape) |
| Schema | `x-deal-v0.schema.json`: the `claim_authority` record type, its body and refs; the claim's verdict members kept for the old shape |
| Go | `names.go` (`TypeClaimAuthority`), `types.go` (`ClaimAuthorityBody`), `chain_steps.go` (the checks move to the new record; the old-shape branch stays; old shape after a `claim_authority` is refused), `record.go`/`schema.go` where types are listed |
| Rust | `deal/names.rs`, `deal/types.rs`, `deal/chain.rs`, the same three changes |
| Python | `check_profile.py` (the reference checker), `python/evidencebook/deal/names.py` and schema data; `x-deal-v1-types.json` only if `TestTypesTable` regenerates it |
| Vectors | new fixtures run by all three (`fixtures_test.go`, `rust/evidencebook/tests/deal_chain.rs`, `python/tests/test_deal.py`): a positive seller thread with an ordered claim (DO), an approved claim (ASK) and an unchecked claim, each followed by its `claim_authority`; a positive deal holding old-shape claims; negatives: a new-shape claim with no `claim_authority` after it, a `claim_authority` not right after its claim, one `about` a non-agent claim, an unchecked one with `authorized_by`, two for one claim, verdict members on both the claim and its `claim_authority`, an old-shape claim after a `claim_authority`, and each rule 12 failure on the new record (wrong class, other words, used check, expired check). `expected-digests.txt` gains the new records. |
| Changelog | `changelog.d/` fragments for Go, Rust and Python |

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
- The old-shape claims a buyer no longer sees on existing deals (section 6): part of the same
  confirmation.

## 10. Test plan

Each implementation passes the shared fixtures (positive and negative, same verdicts and the same
failure for each negative). Go and Rust unit tests cover each rule 12 branch on the new record and
the old-shape branch. `check_profile.py` runs over the fixtures in CI as it does now. The
evidencebook-deal side tests the copies (#244).
