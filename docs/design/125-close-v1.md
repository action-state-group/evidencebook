# evidencebook#125: the Close record seals the `close/v1` body

Status: draft for review (Codex, Kimi, Sherlock2). No code until it converges.

## Problem

`Book.Close` (`reconcile.go:442-470` at `05ef665`) seals
`CloseStatement{counterparty, profile, reconciliation, reason}` with header
`epistemic_type: derived_metric`. agent-action-capsule's `close/v1`
(`schemas/judge/close-v1.json`, last changed in agent-action-capsule#176,
`5ce8c94`; sha256 `183774e7…`) is a closed object that requires
`record_version`, `record_id`, `epistemic_type: producer_claim`, `closed_at`,
`period`, `counts_by_kind` and `head`. Its optional `reconcile {tallies, status}`
then requires exactly one `cites` link in `x-evidence-links` whose target is the
peer's Close for the same period. Steven's C-5 ruling makes `type: close`
normative on that body and has evidencebook's Close carry
`record_version: "close/v1"`. Stamping the version on today's body would label
a body that fails the schema, so the body changes shape.

The How ack (#125, comment 6419989456) and Sherlock2's door confirmation
(comment 6420064995) settled:

- `counterparty`, `profile`, `reconciliation.pairs`, the seq window and
  `reason` leave the body (`close/v1` has no slot for them).
- With no peer Close to cite, the Close seals counts only, with no
  `reconcile`; `close` never refuses for that reason.
- Older Closes are read by dispatching on `record_version`.
- #122's `--period month` folds into this work.

This note settles how.

## The body

```
record_version  "close/v1"
record_id       "<book_id>#<seq>"            the Close's own position; the same string
                                             the book's record capsule carries as action_id
epistemic_type  "producer_claim"             the header says producer_claim too
closed_at       the header's committed_at    the same instant, byte for byte
period          {start, end}                 the caller's period, RFC 3339 UTC
counts_by_kind  {record_type: n}             every record in the own window, by record_type
head            {SHA-256, record id}         the last record of the own window
reconcile?      {tallies, status}            only when a peer Close is cited
x-evidence-links? [{cites, peer Close id}]   exactly when reconcile is present
```

**`record_id`.** The book's record id is the capsule id, a digest over the
header that holds this body, so the body cannot name it. `<book_id>#<seq>` is
already the capsule's `action_id` (`book.go:559`), unique in the book, and a
reader checks it against the header's `book_id` and `seq`.

**`closed_at`.** The book assigns `committed_at` inside its commit step, after
flushing a pending record (which can move `seq`). The body is therefore built
inside that step: `commitEntryLocked` gains an unexported callback that
receives the assigned `seq` and commit time and returns the statement. `seq`
and `committed_at` stay the book's; `record_id` and `closed_at` are derived
from them, never supplied.

**`head` and `counts_by_kind`.** Both describe the own window
`[FromSeq, ToSeq]` the reconciliation ran over. `head` is the record id at
`ToSeq` (the window's last position, always present: `seqWindow` refuses
`to == 0`). `counts_by_kind` counts every record in the window by
`record_type`, including the book's own index roots and earlier Closes, and
never the Close itself. So the window is recoverable from the body alone:
`ToSeq` is the head record's `seq`, and `FromSeq = ToSeq - Σcounts + 1`
(an empty window has `Σcounts = 0`). This replaces the sealed
`reconciliation.from_seq/to_seq` that `--since-last` and `--bundle-out` read.

We read "the head digest at close time" as the head of the range the Close
binds, not the log's tip when the Close is appended: `book close` commits a
checkpoint just before sealing, and pinned records may sit after the period,
so the tip names neither the period nor the window.

**`reconcile`.** Present only when the caller names a peer Close. Tallies map
`MATCHED…UNRESOLVED` to the schema's lowercase keys. `status` is always
`UNILATERAL` at seal time: the body is the Close's own belief, and status is
still read only from the counterparty's `acknowledges`/`rebuts` links
(`StatusOfClose`, unchanged). The single `cites` link goes in the body's
`x-evidence-links`, as the schema places it; the header does not repeat it,
because a header link to a record outside this book would be a new kind of
dangling link for bundle verification. The header's `closes` links to every
placed own record and its `supersedes` link stay as today.

Without a peer Close there is no `reconcile` and no tallies in the body, even
when a peer bundle was reconciled. That is the door confirmation's
counts-only rule. The CLI still prints the live tallies it computed.

**Validation.** `Book.Close` refuses inputs that would break the schema (no
period, `start >= end`, a `PeerClose` that is not a record id); the tests
validate every sealed v1 body against `schemas/vendor/close-v1.json`,
vendored from agent-action-capsule at `5ce8c94` and pinned by its sha256.

## Root package API

```go
// CloseStatement is the close/v1 body.
type CloseStatement struct {
    RecordVersion string           `json:"record_version"`
    RecordID      string           `json:"record_id"`
    EpistemicType EpistemicType    `json:"epistemic_type"`
    ClosedAt      string           `json:"closed_at"`
    Period        ClosePeriod      `json:"period"`
    CountsByKind  map[string]int   `json:"counts_by_kind"`
    Head          DigestRef        `json:"head"`
    Reconcile     *CloseReconcile  `json:"reconcile,omitempty"`
    EvidenceLinks []Link           `json:"x-evidence-links,omitempty"`
}

// LegacyCloseStatement is the body Closes carried before close/v1.
type LegacyCloseStatement struct { Counterparty, Profile string; Reconciliation Reconciliation; Reason string }

// ReadClose dispatches on record_version: "close/v1" fills v1; an absent
// record_version fills legacy; any other version is refused (ErrInvalid).
func ReadClose(h Header) (v1 *CloseStatement, legacy *LegacyCloseStatement, err error)

type CloseInput struct {
    Reconcile    ReconcileInput
    Counterparty string
    Period       ClosePeriod   // required, start < end
    PeerClose    string        // optional: the peer's Close record id to cite
    Supersedes   string
}
```

`Profile` and `Reason` leave `CloseInput`, a breaking change to the root
module (`feat!`, CHANGELOG under Unreleased). `CloseStatement` changes meaning;
code that unmarshalled old bodies into it moves to `ReadClose`.

## The book plugin (`capsulectl/internal/book`)

capsulectl requires the root module at a version and CI refuses a `replace`
(`capsulectl.yml:42-47`), so this lands as two PRs: the root change, then the
plugin bumping its require to that merge commit's pseudo-version, as
`bd514a7` did for `report`.

- **Idempotency.** A Close for the counterparty already exists when a v1
  Close has the same `period.start` and `period.end`, or a legacy Close has
  `profile == closeProfile(p)` (today's rule). Mixed books keep working.
- **`--since-last`.** The furthest position reached is the max over prior
  Closes of the v1 head's `seq` (looked up by record id) or the legacy
  `reconciliation.to_seq`. The period stays the nominal calendar period, as
  today: a `--since-last` window can begin before `period.start`, and
  `counts_by_kind` with `head` shows exactly how far.
- **`--peer`.** The plugin cites a peer Close when the verified peer bundle
  discloses exactly one Close from the counterparty's book, with
  `counterparty_ref` naming this book, whose period equals this one: a v1
  `period`, or a legacy profile whose period `closeProfilePeriod` derives.
  None: counts only. More than one: refused, naming them (the bundle is
  ambiguous about which Close the tallies answer).
- **`--bundle-out` and repeats.** The disclosed window comes from the live
  input on a fresh Close, and from the body on a repeat (v1: head and counts;
  legacy: `reconciliation`).
- **Output.** `closeResult` keeps `record_id`, `seq`, `period`,
  `counterparty`, `already_closed` and the file fields. `reconciliation`
  becomes the live reconciliation on a fresh Close and is omitted on a
  repeat of a v1 Close (the body has no pairs); a new `close` member carries
  the sealed body as stored. CHANGELOG notes the output change.
- **Result claims (`resultbuild.go`).** A close or reconcile claim's period
  is checked against a v1 Close's `period` directly, and against a legacy
  Close's profile string as today. A reconcile claim's tallies come from
  `reconcile.tallies` (v1) or `reconciliation.tallies` (legacy); a reconcile
  claim citing a v1 Close with no `reconcile` is refused ("that Close sealed
  counts only").
- **`--period month`** (#122): the UTC calendar month, `[start, end)`, keyed
  `month:YYYY-MM`; no `--date` picks the most recent ended month; an unended
  month is refused. `closeProfilePeriod` learns `month:` too, for reading.
  Flag help, errors and the skill docs list `day`, `week`, `month`.

## Tests

Root: a v1 body validates against the vendored schema (counts only, and with
`reconcile` and one `cites`); `record_id`, `closed_at`, `head` and
`counts_by_kind` match the header and window; an empty window; `ReadClose` on
a v1 body, a legacy body (the existing `testdata/legacy-citation-purpose`
book's Closes still read and verify) and an unknown version; refusals for a
missing period, `start >= end`, and a `PeerClose` that is not a record id.

Plugin: idempotency against a v1 and a legacy prior Close; `--since-last`
after each; `--peer` with zero, one and two matching peer Closes; repeats
with `--bundle-out` disclose the same window as the first run; claim checks
on v1 and legacy Closes, including a reconcile claim against a counts-only
Close; month boundaries (leap February, December to January).

## Order and gates

The spec half (agent-action-capsule: `type: close` normative in
evidence-result-v0 §4.1) has not landed (`spec/evidence-result-v0.md:8-10` at
`b4df957` still says PROPOSED). Both PRs can be reviewed now; they merge after
it, per the How ack's order.

## Questions for reviewers

1. `head` as the window's last record rather than the log tip.
2. `record_id` as `<book_id>#<seq>`.
3. Refusing a peer bundle that discloses two matching Closes, rather than
   citing the latest.
4. The nominal period with a `--since-last` window that starts earlier.
