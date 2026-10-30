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

This note settles how. Only the Go root package and the plugin change: the
Rust crate reads Close status (`close_status`) but seals no Close, and the
Python package has no Close.

## The body

```
record_version    "close/v1"
record_id         "<book_id>#<seq>"           the Close's own position: the string the
                                              book's record capsule carries as action_id
epistemic_type    "producer_claim"            the header says producer_claim too
closed_at         the header's committed_at   the same instant, byte for byte
period            {start, end}                the caller's period, RFC 3339 UTC
counts_by_kind    {record_type: n}            every record in the own window, by record_type
head              {SHA-256, record id}        the record at the window's end position (ToSeq)
reconcile?        {tallies, status}           only when a peer Close is cited
x-evidence-links? [{cites, peer Close id}]    exactly when reconcile is present
```

**`record_id`.** The book's record id is the capsule id, a digest over the
header that holds this body, so the body cannot name it. `<book_id>#<seq>` is
already the capsule's `action_id` (`book.go:559`) and unique in the book. It is
a locator, not an identity: every link target, `head.digest` and the
`x-evidence-links` target are record ids (digests), never locators, and the
CHANGELOG says so.

**The own window.** `Book.Close` first flushes a pending record
(`flushPendingLocked`), so the window, head, counts and `closes` links are all
taken against the log the Close lands in. It then resolves the own window:
`FromSeq == 0` is 1, `ToSeq == 0` is the book's size; `ToSeq` beyond the size,
or `FromSeq > ToSeq + 1`, is refused (`ErrInvalid`). A book with no record is
refused: there is nothing for `head` to name. The same resolved window feeds the
reconciliation, the counts and the head.

**`head` and `counts_by_kind`.** `head` is the record id at the resolved
`ToSeq`. `counts_by_kind` counts every record in `[FromSeq, ToSeq]` by
`record_type`, including the book's own index roots and earlier Closes, never
the Close itself. So the window is recoverable from the body: `ToSeq` is the
head record's `seq`, and `FromSeq = ToSeq - Σcounts + 1`. An empty window
(`FromSeq = ToSeq + 1`, `Σcounts = 0`) still has a head: the record just
before the window. This replaces the sealed `reconciliation.from_seq/to_seq`
that `--since-last` and `--bundle-out` read.

Both describe the records the Close binds, which is narrower than the
schema's wording: `head` is not the log tip when the Close is appended
(`book close` commits a checkpoint just before sealing, and pinned records can
sit after the period), and under `--since-last` the counted window can start
before `period.start` (below). The spec half must define `head` as the last
record the Close binds and `counts_by_kind` as over the records it binds; that
requirement is posted on #125 for it, and this code merges only after it.

**`closed_at`.** The book assigns `seq` and `committed_at` inside its commit
step. `commitEntryLocked` gains an unexported callback that receives both and
returns the statement, so `record_id` and `closed_at` are derived from the
book's values, never supplied.

**`reconcile`.** Present only when the caller names a peer Close. Tallies map
`MATCHED…UNRESOLVED` to the schema's lowercase keys. `status` is always
`UNILATERAL` at seal time: the body is the Close's own belief, and status is
still read only from the counterparty's `acknowledges`/`rebuts` links
(`StatusOfClose`, unchanged). The single `cites` link goes in the body's
`x-evidence-links`, where the schema puts it, and not in the header: a header
link to a record this book does not hold makes every bundle rooted at the
Close `declared_incomplete` (`closureLocked`, `bundle.go:363-378`; `:339-341`),
which `--bundle-out` bundles are not today. The header keeps its `closes` links
to every placed own record and its `supersedes` link.

Without a peer Close there is no `reconcile` and no tallies in the body, even
when a peer bundle was reconciled (the door confirmation's counts-only rule).
The CLI still prints the live tallies it computed.

**Validation.** `Book.Close` refuses inputs that would break the schema (no
period, `start >= end`, a `PeerClose` that is not a record id). The tests
validate every sealed v1 body against `schemas/vendor/close-v1.json`, vendored
from agent-action-capsule at `5ce8c94` and pinned by its sha256.

## Root package API

```go
// CloseStatement is the close/v1 body.
type CloseStatement struct {
    RecordVersion string          `json:"record_version"`
    RecordID      string          `json:"record_id"`
    EpistemicType EpistemicType   `json:"epistemic_type"`
    ClosedAt      string          `json:"closed_at"`
    Period        ClosePeriod     `json:"period"`
    CountsByKind  map[string]int  `json:"counts_by_kind"`
    Head          DigestRef       `json:"head"`
    Reconcile     *CloseReconcile `json:"reconcile,omitempty"`
    EvidenceLinks []Link          `json:"x-evidence-links,omitempty"`
}

// LegacyCloseStatement is the body Closes carried before close/v1.
type LegacyCloseStatement struct { Counterparty, Profile string; Reconciliation Reconciliation; Reason string }

// ReadClose reads a Close record's body. It refuses a record that is not a
// Close. record_version "close/v1" fills v1, after checking record_id is the
// header's book_id#seq, closed_at its committed_at and epistemic_type
// producer_claim; an absent record_version fills legacy; any other version
// is refused (ErrInvalid).
func ReadClose(h Header) (v1 *CloseStatement, legacy *LegacyCloseStatement, err error)

type CloseInput struct {
    Reconcile    ReconcileInput
    Counterparty string
    Period       ClosePeriod // required, start < end
    PeerClose    string      // optional: the peer's Close record id to cite
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
  today, because idempotency and peer matching key on it. The CHANGELOG and
  skill docs say that `counts_by_kind` and `head` describe the bound window,
  which under `--since-last` can start before `period.start`.
- **Which peer Close is cited.** Of the verified peer bundle's records, only
  those with a verified header and capsule under its authenticated anchor
  (`HeaderVerified && CapsuleOK && peer.Covers(seq)`) count. A candidate is a
  Close from the counterparty's book, with `counterparty_ref` naming this
  book, that `ReadClose` accepts, and whose period equals this one: a v1
  `period`, or a legacy profile whose period `closeProfilePeriod` derives.
  Candidates that another candidate supersedes are dropped. None left:
  counts only. One: it is cited. More than one: refused, naming them.
- **The peer window follows the cited Close.** With a Close cited, the
  reconciliation's peer window is that Close's own window, not one derived
  from the period, so the tallies answer the Close they cite: v1, `head`'s
  `seq` and `Σcounts`; legacy, `reconciliation.from_seq/to_seq`. A v1 head
  the bundle does not disclose is refused. With none cited, the peer window
  is derived from the period as today (`peerWindow`).
- **`--bundle-out` and repeats.** The disclosed window comes from the live
  input on a fresh Close, and from the body on a repeat (v1: head and counts;
  legacy: `reconciliation`).
- **Output.** `closeResult` keeps `record_id`, `seq`, `period`,
  `counterparty`, `already_closed` and the file fields. `reconciliation` is
  the live reconciliation on a fresh Close and is omitted on a repeat of a v1
  Close (the body has no pairs). A new `close` member carries the sealed body
  as stored. The CHANGELOG notes the output change.
- **Result claims (`resultbuild.go`).** A close or reconcile claim's period
  is checked against a v1 Close's `period` directly, and against a legacy
  Close's profile string as today. A reconcile claim's tallies come from
  `reconcile.tallies` (v1) or `reconciliation.tallies` (legacy). A reconcile
  claim citing a v1 Close with no `reconcile` is refused ("that Close sealed
  counts only").
- **`--period month`** (#122): the UTC calendar month, `[start, end)`, keyed
  `month:YYYY-MM`; no `--date` picks the most recent ended month; an unended
  month is refused. `closeProfilePeriod` learns `month:` too, for reading.
  Flag help, errors and the skill docs list `day`, `week`, `month`.

**What users lose, stated in the plugin CHANGELOG.**
- A Close sealed without a citable peer Close has no tallies, so a reconcile
  claim can no longer cite a no-peer Close and derive GAP from its
  INSUFFICIENT counts, as it can today.
- Of two books closing the same period, the first to close has no peer Close
  to cite, and a repeat returns that counts-only Close. Only the second
  book's Close carries `reconcile`. A path for the first book to supersede
  its counts-only Close once the peer's is citable (the root already takes
  `Supersedes`) is filed as a follow-up, not built here.

## Tests

Root: a v1 body validates against the vendored schema (counts only, and with
`reconcile` and one `cites`); `record_id`, `closed_at`, `head` and
`counts_by_kind` match the header and window; an empty window, whose head is
the record before it; open windows (`FromSeq`/`ToSeq` zero) and refusals for
`ToSeq` past the size, `FromSeq > ToSeq + 1` and an empty book; a pending
record flushed before the window is taken; `ReadClose` on a v1 body, a legacy
body (the existing `testdata/legacy-citation-purpose` book's Closes still read
and verify), an unknown version, a non-Close record, and a v1 body whose
`record_id` or `closed_at` disagrees with its header; refusals for a missing
period, `start >= end`, and a `PeerClose` that is not a record id.

Plugin: idempotency against a v1 and a legacy prior Close; `--since-last`
after each; `--peer` with zero, one and two matching peer Closes, one that
another supersedes, and one whose header is unverified; the peer window taken
from a cited v1 and a cited legacy Close, and a cited v1 Close whose head is
not disclosed; repeats with `--bundle-out` disclose the same window as the
first run; claim checks on v1 and legacy Closes, the reconcile-claim tests
moved to a fixture whose Close cites a peer Close, and a reconcile claim
against a counts-only Close refused; month boundaries (leap February,
December to January).

## Order and gates

The spec half (agent-action-capsule: `type: close` normative in
evidence-result-v0 §4.1, with the `head` and `counts_by_kind` reading above)
has not landed (`spec/evidence-result-v0.md:8-10` at `b4df957` still says
PROPOSED). Both PRs can be reviewed now; they merge after it, per the How
ack's order.
