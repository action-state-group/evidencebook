# evidencebook SDK guide

This guide walks the SDK as five paths, in the order a developer meets them.
Each path names the functions it uses and the runnable Go `Example` that shows
it. The API reference is the package documentation on pkg.go.dev:

- [`github.com/action-state-group/evidencebook`](https://pkg.go.dev/github.com/action-state-group/evidencebook)
- [`github.com/action-state-group/evidencebook/report`](https://pkg.go.dev/github.com/action-state-group/evidencebook/report)

Every Example below renders there with its output, from v0.9.0 for the root
module and `report/v0.3.0` for the report module; path 4's from the first
release after v0.12.2. To run them from a
checkout:

```bash
go test -run 'Example_joinTheLoop|ExampleBook_SealResult|ExampleBook_Respond|Example_staple' -v .
```

Every Example reads a corpus file from its module's `testdata/` by its path
and runs under `go test` in CI, so the guide, the CI run and the vectors are
the same files.
[VECTORS.md](../VECTORS.md) lists where each set comes from.

## Install

Requires Go 1.27 or newer.

```bash
go get github.com/action-state-group/evidencebook@v0.9.0
```

The report builder is its own module, so the root module does not depend on
capsule-cli or the viewer. Add it only for path 2:

```bash
go get github.com/action-state-group/evidencebook/report@v0.3.0
```

## Path 0: join the loop

`record`, then `request`, then `verify`, then `doctor --probe`.

| Step | Function |
|---|---|
| record | `Book.Append` seals an action as an Agent Action Capsule; `Book.Checkpoint` checkpoints the book |
| request | `Book.Request` records that you are asking before you send, and returns the octets to send |
| verify | `Book.RecordResponse` checks the reply under the responder's pinned keys (`ResponderKeys`) and records the outcome: an artifact, a signed refusal, or a reply that failed verification. `Book.RecordAbsence` records that no reply came in the window |
| doctor --probe | `Book.Doctor` lists what your node would emit: what is self-attested, unwitnessed, `NOT_CHECKED` or unsupported. With `DoctorOptions.Probe` it asks your own responder as a stranger and compares the answers with the advertised policy; a book that serves `standard_history/1` also reports the card a signed requester receives (`probe_standard_history`, the card in `card`), built in-process. It seals nothing |

Example: [`Example (JoinTheLoop)`](https://pkg.go.dev/github.com/action-state-group/evidencebook#example-package-JoinTheLoop)
(`example_guide_test.go`). Bob asks Alice for the request draft's vector
`pos-derivation-history-card`, verifies her answer, and Alice runs the doctor
with a probe.

On the command line, `capsulectl book doctor --probe` runs the same check over
a book profile. It is in capsulectl-book `capsulectl/v0.1.0-rc24` and later, a
release candidate (pre-release).

`Doctor` reports a list of findings and never a score. `NOT_CHECKED` means the
check was not run, never that it passed or failed.

## Path 1: write a Terms Pack and seal a Result

Writing a Terms Pack and evaluating evidence against it (`evaluate`, the pack
verbs) are not part of this module, and evidencebook does not wrap them:

- **Packs and evaluation:** [capsule-engine](https://github.com/action-state-group/capsule-engine)
  implements them. Its public Go module is
  `github.com/action-state-group/capsule-engine/go` ([go/README.md](https://github.com/action-state-group/capsule-engine/blob/main/go/README.md)).
  Its package `pack` loads a pack and its definitions at their digests, and
  its package `guard` runs a pack's checks and writes the per-rule report.
- **Judges:** [evidencebook-skills](https://github.com/action-state-group/evidencebook-skills)
  supplies the judges and the Terms Packs' `pack-source.yaml` sources.
- **Not yet shipped:** the pack authoring verbs (`init`, `lint`, `test`,
  `publish`, `run`, `compare`), tracked in
  [evidencebook-skills#56](https://github.com/action-state-group/evidencebook-skills/issues/56),
  and one pack format for live and after-the-fact packs, tracked in
  [capsule-engine#252](https://github.com/action-state-group/capsule-engine/issues/252).

What this module does is seal the Result an evaluator produced:
`Book.SealResult` records an Evidence Result v0 document as one
`evidence_result` record about the contract it reports, and makes sure a
checkpoint covers it, so a bundle can be built on it at once.

Example: [`Book.SealResult`](https://pkg.go.dev/github.com/action-state-group/evidencebook#example-Book.SealResult)
(`example_guide_test.go`). It seals the expected Result of the Deal Reconcile
Contract's vector `pair-matched`.

## Path 2: build a Report

`report.Build` renders a verified, Result-rooted Evidence Bundle v2 into one
offline HTML page under a `report.View`. It verifies before it renders, reads
only the bundle it is given, and refuses to write a page for a bundle that is
not `VALID`, while still saying what it found. A view can carry a wording
layer (`View.Wording`, an `aac.wording-pack/v0` pack over the viewer's own
words), select the contract report (`View.ContractReport`), and carry the
compiled Evidence Contract the Result cites (`View.Contract`, pinned to its
digest like the wording layer), from which the contract report reads each
row's tier. Without a contract, every row's tier is undecided, never
must-have. Like a page with a wording layer, a page built with a contract
gets no permalink.
`report.SealPage` seals the page's digest in the holder's book, and
`report.CheckPage` later tells whether a page file is one the book sealed.

Examples, in package `report` (`report/example_test.go`):

- [`Build`](https://pkg.go.dev/github.com/action-state-group/evidencebook/report#example-Build)
  renders the monthly-outcome presentation fixture and writes exactly the
  committed page.
- [`Build (Wording)`](https://pkg.go.dev/github.com/action-state-group/evidencebook/report#example-Build-Wording)
  renders the same fixture as the contract report, worded by a layer.
- [`Build (Incomplete)`](https://pkg.go.dev/github.com/action-state-group/evidencebook/report#example-Build-Incomplete)
  gets no page for an `INCOMPLETE` bundle, and reports what is unmet.
- [`SealPage`](https://pkg.go.dev/github.com/action-state-group/evidencebook/report#example-SealPage)
  seals a page and checks it, untouched and changed.

On the command line these are `capsulectl book report build --seal` and
`capsulectl book report check`. Like the doctor, they are in capsulectl-book
`capsulectl/v0.1.0-rc24` and later.

## Path 3: serve a History

`respond`, with `request` and `verify` on the other side.

| Step | Function |
|---|---|
| answer | `Book.Respond` answers request octets as received, in the same encoding: a signed envelope carrying the canonical Artifact, or a signed refusal with exactly one registered reason |
| who is answered | `RespondOptions.AuthenticatedRequester`, set only from the transport's own authentication. With it empty, only the `checkpoints` subject and the `history_card/1` derivation are answered |
| what is shared | `SharePolicy` (`DefaultSharePolicy` answers counterparties) |
| a History for one recipient | `Book.RegisterDerivation` registers a derivation definition; its identifier binds the document alone, and only its recipient is answered |
| carriage | `Book.A2A` and `Book.MCP` carry the same Artifact, byte for byte |

Example: [`Book.Respond`](https://pkg.go.dev/github.com/action-state-group/evidencebook#example-Book.Respond)
(`example_guide_test.go`). It answers every malformed case of the request
draft's corpus with the reason the vector states, then registers the
derivation vectors' definition `for-alice` and serves it to its recipient
only.

To check these answers with a verifier you wrote yourself, follow
[THIRD-PARTY-VERIFIER.md](THIRD-PARTY-VERIFIER.md).

## Path 4: History cards that staple

Each party keeps its own shelf and answers its own History. A relying party
staples two parties' evidence of one deal by composing their answers and
joining them on the deal id. Design note
[#254](design/254-shelf.md) §3 gives the reasons.

| Step | Function |
|---|---|
| a shelf | `OpenSQLiteShelf` opens one file for every log a key answers for; `Shelf.Open` opens a book in it, one per deal (`deal/<deal_id>`) and one for what the agent does outside any deal |
| a deal step | `dealprofile.StepCapsule` gives the Capsule of an x-deal-v0 step, which carries the deal id as its `deal_id` member, and `Book.AppendCapsule` commits it as the book's record |
| the cadence | `Shelf.Cadence` and `Cadence.Tick` commit every book's checkpoint in one checkpoint at each tick |
| `history_card/1` per log | `Book.Respond`, as in path 3: each log answers for its own stream |
| a History over books at a tick | `Cadence.StandardHistoryAnchor` gives the tick's anchor and how far each book is committed; `dealprofile.StandardHistoryDeal` gives one deal entry per deal book; `DeriveStandardHistory` derives the `standard_history/1` card, and agent-action-capsule's `standardhistory.Check` checks it |
| each party's half | `Book.Bundle` gives an Evidence Bundle rooted at the party's record of the deal |
| the staple | agent-action-capsule's `bundle.ComposedDigest` and `bundle.VerifyComposed` build and verify a `composed/v1` block (Evidence Bundle -02, "The composed/v1 Extension") whose members are the parties' Bundles |

Example: [`Example (Staple)`](https://pkg.go.dev/github.com/action-state-group/evidencebook#example-package-Staple)
(`example_staple_test.go`). A Goose agent buys inference from a Mesh node.

- **The Goose agent** keeps a book for the deal, a node book and the
  cadence. Each book answers `history_card/1`, and its `standard_history/1`
  card is as of a tick, with every property passing.
- **The Mesh node's** whole history is one stream, which answers
  `history_card/1` at its own checkpoint.
- **The relying party** composes the two halves and joins them with
  `pre_agreed_identifier`. The join compares the SHA-256 of the `deal_id`
  member of each half's root record. It derives `agree`, and because the
  two halves come from different custody domains, `corroborating`.
- **One operator's two keys.** The Mesh node's operator also runs a Goose
  agent under a key of its own. The two keys are joined only by a
  `controller_binding` join, through a binding record both keys signed with
  the claim `same_controller_asserted` (controller-binding-v0). The join
  derives `agree` but is reported `redundant`: one controller's keys
  confirm nothing about each other. Without the binding record the join is
  `not_derivable`. A controller binding never joins counterparties.

`shared_artifact_digest` does not join the two halves of an exchange. It
joins only records that cite the same third artifact, such as the agreed
terms.

The same reference in other languages:

- **Goose, many books at a tick, in Python:**
  [`python/examples/goose`](../python/examples/goose/README.md). CI checks
  each card it writes with Go (`test/interop/goose-card`).
- **Mesh, one stream, in Rust:** a capsules-plugin node keeps its chain on
  its shelf from release v0.1.11
  ([capsules-plugin#213](https://github.com/Mesh-LLM/capsules-plugin/pull/213)).

### What is not defined yet

- **A History answer as a `composed/v1` member.** A member is an Evidence
  Bundle, a refusal or an absence, and a join reads the member's root
  record. Whether a History answer can be a member, and what its root
  record would be, is not stated (#254 D4). Until it is, the staple joins
  the parties' halves of the deal, and each party's History is checked on
  its own.
- **The Mesh node's History, `inference-service-history/0`.** A
  capsules-plugin node derives it over its one stream at a period boundary,
  with that stream's `history_card/1` embedded unchanged. No library outside
  the plugin derives or checks it yet, so the example stops at the
  `history_card/1` the History embeds.

## Other languages

The Rust crate (`rust/evidencebook`) and the Python package
`capsule-evidencebook` (`python/`) implement the book and the request
interaction with the same records and reply octets, and are checked against
the same vectors. The README's
[Other implementations and parity](../README.md#other-implementations-and-parity)
section lists the shared checks.
