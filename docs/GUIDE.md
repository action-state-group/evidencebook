# evidencebook SDK guide

This guide walks the SDK as four paths, in the order a developer meets them.
Each path names the functions it uses and the runnable Go `Example` that shows
it. The API reference is the package documentation on pkg.go.dev:

- [`github.com/action-state-group/evidencebook`](https://pkg.go.dev/github.com/action-state-group/evidencebook)
- [`github.com/action-state-group/evidencebook/report`](https://pkg.go.dev/github.com/action-state-group/evidencebook/report)

The Examples for paths 0, 1 and 3 are in
[`example_guide_test.go`](../example_guide_test.go). They use only API that
v0.8.0 has, but they were added after it, so pkg.go.dev shows them from the
next tagged release; until then, read them in the file or run them from a
checkout:

```bash
go test -run 'Example_joinTheLoop|ExampleBook_SealResult|ExampleBook_Respond' -v .
```

The path 2 Examples are in `report/v0.1.0` and render on pkg.go.dev now.

Every Example reads a corpus file from its module's `testdata/` by its path
and runs under `go test` in CI, so the guide, the CI run and the vectors are
the same files.
[VECTORS.md](../VECTORS.md) lists where each set comes from.

## Install

Requires Go 1.27 or newer.

```bash
go get github.com/action-state-group/evidencebook@v0.8.0
```

The report builder is its own module, so the root module does not depend on
capsule-cli or the viewer. Add it only for path 2:

```bash
go get github.com/action-state-group/evidencebook/report@v0.1.0
```

## Path 0: join the loop

`record`, then `request`, then `verify`, then `doctor --probe`.

| Step | Function |
|---|---|
| record | `Book.Append` seals an action as an Agent Action Capsule; `Book.Checkpoint` checkpoints the book |
| request | `Book.Request` records that you are asking before you send, and returns the octets to send |
| verify | `Book.RecordResponse` checks the reply under the responder's pinned keys (`ResponderKeys`) and records the outcome: an artifact, a signed refusal, or a reply that failed verification. `Book.RecordAbsence` records that no reply came in the window |
| doctor --probe | `Book.Doctor` lists what your node would emit: what is self-attested, unwitnessed, `NOT_CHECKED` or unsupported. With `DoctorOptions.Probe` it asks your own responder as a stranger and compares the answers with the advertised policy. It seals nothing |

Example: [`Example_joinTheLoop`](../example_guide_test.go). Bob asks Alice for the request draft's vector
`pos-derivation-history-card`, verifies her answer, and Alice runs the doctor
with a probe.

On the command line, `capsulectl book doctor --probe` runs the same check over
a book profile. It is in this repository's `capsulectl/` module on main and not
yet in a capsulectl-book release.

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

Example: [`ExampleBook_SealResult`](../example_guide_test.go). It seals the expected Result of the Deal Reconcile
Contract's vector `pair-matched`.

## Path 2: build a Report

`report.Build` renders a verified, Result-rooted Evidence Bundle v2 into one
offline HTML page under a `report.View`. It verifies before it renders, reads
only the bundle it is given, and refuses to write a page for a bundle that is
not `VALID`, while still saying what it found. A view can carry a wording
layer (`View.Wording`, an `aac.wording-pack/v0` pack over the viewer's own
words) and select the contract report (`View.ContractReport`).
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
`capsulectl book report check`. Like the doctor, they are on main and not yet
in a capsulectl-book release.

## Path 3: serve a History

`respond`, with `request` and `verify` on the other side.

| Step | Function |
|---|---|
| answer | `Book.Respond` answers request octets as received, in the same encoding: a signed envelope carrying the canonical Artifact, or a signed refusal with exactly one registered reason |
| who is answered | `RespondOptions.AuthenticatedRequester`, set only from the transport's own authentication. With it empty, only the `checkpoints` subject and the `history_card/1` derivation are answered |
| what is shared | `SharePolicy` (`DefaultSharePolicy` answers counterparties) |
| a History for one recipient | `Book.RegisterDerivation` registers a derivation definition; its identifier binds the document alone, and only its recipient is answered |
| carriage | `Book.A2A` and `Book.MCP` carry the same Artifact, byte for byte |

Example: [`ExampleBook_Respond`](../example_guide_test.go). It answers every malformed case of the request
draft's corpus with the reason the vector states, then registers the
derivation vectors' definition `for-alice` and serves it to its recipient
only.

To check these answers with a verifier you wrote yourself, follow
[THIRD-PARTY-VERIFIER.md](THIRD-PARTY-VERIFIER.md).

## Other languages

The Rust crate (`rust/evidencebook`) and the Python package
`capsule-evidencebook` (`python/`) implement the book and the request
interaction with the same records and reply octets, and are checked against
the same vectors. The README's
[Other implementations and parity](../README.md#other-implementations-and-parity)
section lists the shared checks.
