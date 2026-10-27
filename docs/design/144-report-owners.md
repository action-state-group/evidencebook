# evidencebook#144: the report module takes its pieces from their owners

Status: draft for review (Codex, Kimi, Sherlock2). No code changes until each owner's How ack lands.

## Problem

The `report` module (`github.com/action-state-group/evidencebook/report`) imports capsule-cli's `cli` package, so the SDK depends on the CLI. #144 replaces each import with the symbol from its owner, under one rule: **move, never copy**. A piece that leaves capsule-cli is consumed by capsule-cli from its new owner in the same series, so no second implementation is left behind.

Done so far:
- `bundle.Decode` moved to agent-action-capsule/go (agent-action-capsule#426).
- capsule-cli now uses it (capsule-cli#466).
- `DefaultBundleURL` stays in capsule-cli: it names the hosted verification service, not a verifier (Sherlock2 on #426).
- The bundle verdict (`BundleVerdict`, `UnmetClaims`, `PageGate`) stays in capsule-cli until its neutral/deal split is built. The design and Sherlock2's three conditions are on #144.

This note covers the two pieces left: the Result v0 gate (move 2) and the browser runtime (move 3).

## Move 2: the Result v0 gate

**What is there.** capsule-cli's `cli/result.go` (`CheckResult`, `ResultClaim`, `ResultVersion`; capsule-cli#324) is the only Go gate. `report`, `book result build` and `deal terms-result` use it.
- It validates against a schema copy vendored from agent-action-capsule at `4b24505` (digest `288d17a6…`). That copy is behind `schemas/evidence-result-v0.json` on main (digest `2776a444…`, last changed in agent-action-capsule#402).
- Its cross-checks predate the current spec: there's no `out_of_scope` bucket, no `applies_from` revisions and no `requirements_covered` rules.

agent-action-capsule's reference gate is Python, `schemas/check_evidence_result_examples.py` (1,422 lines). It runs the schema, the cross-element rules and the close-state walks over `vectors/evidence-result/`: 51 negative and 20 positive cases, in 88 files counting 15 `.records.json` sidecars, the README and SHA256SUMS. Its Go module has no Result gate and no JSON Schema dependency.

**Decision (coordinator, 2026-10-27).** Don't move capsule-cli's gate: it would put a stale gate in the owner's repository. Move 2 is instead a new Go gate in agent-action-capsule at parity with the Python reference, tracked as **agent-action-capsule#428**.
- It brings a JSON Schema validator into the neutral Go module (capsule-cli uses `santhosh-tekuri/jsonschema/v6`).
- It embeds the schema in `go/`, held byte-equal to `schemas/evidence-result-v0.json` by a test.
- It passes every case in `vectors/evidence-result/` with the Python reference's verdict. Parity is counted in cases (51 negative, 20 positive), not files.

The API names and the dependency are that issue's door, and it starts only after its How ack.

**Then, one PR each, in order:**
1. **capsule-cli adopts the gate** and deletes `cli/result.go`'s copy. This widens what `result build`, `book result build` and `deal terms-result` accept: the current spec allows `out_of_scope` claims and revisions. It also changes some refusal messages, so the PR gets its own CHANGELOG note and Sherlock2's review.
2. **`report` switches** from `cli.CheckResult` to the gate, in the batched report release below.

## Move 3: the browser runtime

**What is there.** Report pages embed a browser runtime (`EvidenceGraphIIFE`) that capsule-cli vendors in `cli/assets/evidence-graph.iife.js`, pinned by digest (`b8dbf296…`; capsule-cli main at ba37e5c, vendored by capsule-cli#464). It is capsule-cli's own build, `scripts/build-report-runtime.mjs`:
- capsule-viewer's `ts/src` page code at `cdc83dc`;
- agent-action-capsule's `ts/src` verifier at `9d653f4` (the ts 0.8.0 release commit);
- every name agent-action-capsule's browser entry exports.

CI rebuilds it byte for byte (capsule-cli `ci.yml`, job `iife`).

The deal page module, `capsulectl.deal-view/v0` (today capsule-cli's `cli/assets/deal-view.js`), calls six names on the runtime's global:
- `registerPresentation`, which comes from the viewer;
- `disclosureOf`, `ed25519Available`, `verifiedPayload`, `verifyDisclosureEnvelope` and `verifyProducerEnvelope`, which come from agent-action-capsule's browser entry.

capsule-viewer already builds a runtime of its own, `ts/scripts/build-iife.mjs`:
- It bundles `ts/src/browser.ts`, taking agent-action-capsule from the npm release its `package.json` pins (0.8.0).
- It stamps the build commit for the "Verified by" line.

Its output (`dist/`) is gitignored, so capsule-viewer's Go module carries the page shell (`emitter`) but no runtime. `browser.ts` exports the viewer's modules only, not the five verifier names `deal-view.js` needs.

**Options.**
- **(i) capsule-viewer/go vendors capsule-cli's combined build:** the script, the pin and the CI rebuild move there. Page bytes don't change, but the viewer then carries a build shaped by capsule-cli's deal page and its export needs.
- **(ii) capsule-viewer/go ships the viewer's own runtime**, and capsule-cli and `report` consume it. **Recommended.** The viewer owns its runtime and its export list, there's one build, and the verifier comes from a release.

**(ii) in detail.**
1. **capsule-viewer PR.**
   - A Go package beside `emitter` (name to be settled in the How ack) embeds the runtime that `ts/scripts/build-iife.mjs` builds at a recorded viewer commit. The built file is committed inside the Go module, under the package's own directory with its `.sha256` pin beside it, as capsule-cli does under `cli/assets/` today. `go:embed` can't reach `dist/`, which is gitignored and outside the module.
   - The block names the page reads (`report-card/v1`, `presentation/v1`, `contract-report/v1`) are declared in the viewer's existing `go/presentation` package, whose `descriptor.go` already keys `contract-report/v1`.
   - A CI job rebuilds the runtime at the recorded commit and compares it byte for byte, as capsule-cli's `iife` job does today. The runtime is committed one commit after the commit it states, so it never has to contain itself.
   - `browser.ts` re-exports, by name, agent-action-capsule's five verifier functions `disclosureOf`, `ed25519Available`, `verifiedPayload`, `verifyDisclosureEnvelope` and `verifyProducerEnvelope`. They're documented as the verifier functions a presentation module may call on the runtime's global, beside `registerPresentation`. That list is the viewer's contract for presentation modules and part of this PR's door. The deal page is today's only caller, but the contract names no module.
   - The PR checks that `go/emitter/runtime-style-hashes.txt` matches the styles of the runtime it ships. Both runtimes are built from the same page code, but that is unchecked today.
   - The NOTICE and THIRD_PARTY_NOTICES move with the asset.
2. **capsule-cli PR.**
   - `EvidenceGraphIIFE`, `CardExtension` and `PresentationExtension` come from the viewer package.
   - Deleted: `cli/assets/evidence-graph.iife.js` and its `.sha256` file; the pin constants `EvidenceGraphIIFEDigest`, `evidenceGraphIIFESource` and the `EvidenceGraphIIFESHA256` embed in `cli/evidence_graph_assets.go`; the runtime's lines in `cli/assets/README.md`; `scripts/build-report-runtime.mjs`, `scripts/iife-sync.sh` and `scripts/build-evidence-graph-iife.sh`; the Makefile's `iife-check`; and the `iife` CI job.
   - **Order with capsule-cli#428.** capsule-cli#428's step 4 moves `capsulectl.deal-view/v0` into evidencebook-deal as its presentation module. This PR lands after that step, and after capsule-cli#428's PR B, which also edits `cli/bundle.go`. Then the export contract's only caller lives in evidencebook-deal, and the browser proof that the deal page works on the viewer's runtime is made there. If move 3 is ready before step 4, this PR keeps `deal-view.js` and shows the deal page working on the viewer's runtime with a browser check.
3. **evidencebook `report`** imports the runtime and the block names from the viewer, and drops `cli.EvidenceGraphIIFE`, `cli.CardExtension` and `cli.PresentationExtension`. Its two golden pages are also read by capsulectl's golden test (`capsulectl/internal/book/presentation_golden_test.go`), which builds them with the `report` version `capsulectl/go.mod` requires (v0.1.0 today). So the `report` PR and a capsulectl PR that requires `report` at the `report` PR's merge commit (a pseudo-version, no release) land back to back, with no other capsulectl change between them. The capsulectl PR's CI runs the golden test against the new pages.

**Pages that change, and how they are re-pinned.** The page embeds the runtime, so every committed page changes bytes, even though the verifier release (0.8.0) and the page code are the same. That's 20 pages:

| Repository | Pages | Re-pinned by |
|---|---|---|
| capsule-cli | `cli/testdata/presentation/{1..9}-*/page.html` | its golden update in the capsule-cli PR |
| evidencebook | `report/testdata/presentation/{4-monthly-outcome,5-monthly-compliance}/page.html` | `EVIDENCEBOOK_UPDATE_PRESENTATION_PAGES=1` in the `report` PR; capsulectl's golden test reads the same files |
| evidencebook-deal | `testdata/presentation/{1..9}-*/page.html` and their browser snapshots | its copy refreshed, then `TestPresentationGoldens` measures them in a browser |

The byte change is expected. Equivalence is checked by behaviour, not bytes, against the evidencebook-deal browser goldens (`snapshot.json`, `build-snapshot.json`).
- Their build-provenance fields change, and must change to the true values: the viewer commit (today `4b08aad14182`) becomes the recorded viewer commit, and the verifier version (today `0.6.1`) becomes the version `ts/src/viewer-build.ts` states (`0.8.0`). The PR checks each new value against its source.
- Every other snapshot difference is reviewed item by item, and is either explained by the new runtime or fixed before merge, never re-pinned silently.

## Release

The `report` module's switches batch into one later release, after moves 2 and 3, tagged on Ethan's go. Every `cli` symbol `report` imports today is accounted for:

| `cli` symbol | After the batch |
|---|---|
| `DecodeBundleJSON` | `bundle.Decode` (agent-action-capsule#426) |
| `CheckResult`, `ResultClaim`, `ResultVersion` | the Go gate (agent-action-capsule#428) |
| `EvidenceGraphIIFE`, `CardExtension`, `PresentationExtension` | capsule-viewer/go (move 3) |
| `SafeError` | dropped: `report` uses the owners' error text (`err.Error()`) once no `cli` hint errors reach it |
| `BundleVerdict`, `UnmetClaims`, `PageGate`, `DefaultBundleURL`, `ErrPartial`, `ErrBundleInvalid`, `ErrInput` | stay in capsule-cli: the verdict until its split, and the URL and classes with it |

So after the batch, `report` still imports capsule-cli for the bundle verdict alone, the narrow dependency the coordinator accepted.

## Doors

- **agent-action-capsule#428:** the Go gate's public names and its JSON Schema dependency in the neutral module. Its own How ack and door.
- **capsule-viewer:** the runtime package's names, and `browser.ts`'s export list as a contract for presentation modules.
- **capsule-cli:** the wider Result acceptance (move 2 adoption). That's a change to what a checker accepts, so it gets Sherlock2's review.

None of them adds product meaning. They move existing code to its owner, or bring a Go gate to parity with an existing reference.
