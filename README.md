# evidencebook

`evidencebook` is a Go implementation of the evidence layer: a local evidence
store that records evidence and the typed links between records, commits
payload digests while payload bytes are resolved separately, keeps three
distinct classes of index, and answers requests for evidence with portable
bundles that anyone can verify.

A book embeds a commitment substrate and never exposes it. The Checkpointed
Local Log ([CLL Go](https://github.com/action-state-group/checkpointed-local-log/tree/main/go)) is the
reference substrate; a SCITT receipt-holding binding shows the book does not
depend on it. Every record is sealed as an
[Agent Action Capsule](https://github.com/action-state-group/agent-action-capsule),
so a bundle from a book on the CLL substrate verifies, interval and all, with
the neutral Evidence Bundle verifier alone.

Terms Packs (packs): EU AI Act Obligations and Outcomes (draft) are published at
[actionstate.ai/packs](https://actionstate.ai/packs), and Everyday Rules ships with the Muse integration.

## Install

Requires Go 1.27 or newer.

```bash
go get github.com/action-state-group/evidencebook
```

## Use

```go
store, _ := evidencebook.OpenFileStore("book/records")
substrate, _ := evidencebook.OpenCLL("book/cll.jsonl", "orders-log", checkpointKey)
signer, _ := evidencebook.NewEd25519Signer(recordKey)
// Payload bytes live in an artifact store (capsule-emit's artifact.Store
// contract); this is its JSONL backend, trusting the book's signer.
originals, _ := evidencebook.OpenOriginalsFile(ctx, "artifacts.jsonl", "orders", signer.PublicKey())

book, err := evidencebook.Open(ctx, evidencebook.Config{
	BookID: "orders", Operator: "example-operator",
	Store: store, Substrate: substrate, Originals: originals, Signer: signer,
})

observed, err := book.Append(ctx, evidencebook.Entry{
	RecordType:    "observation",
	EpistemicType: evidencebook.ObservedEvent,
	SubjectRef:    "order-17",
	Payloads:      [][]byte{[]byte(`{"status":"approved"}`)},
})

bundle, err := book.Bundle(ctx, evidencebook.BundleRequest{Root: observed.RecordID})
```

Production code must handle every error. The verbs are `Append`, `Query`,
`Bundle`, `Request`, `Respond`, `Reconcile` and `Close`. See
[docs/design.md](docs/design.md) for how each public type maps to the
specification, the cross-implementation checks, and the known limits.

## Answering requests

`Respond` implements the responder side of the Minimum Evidence
Interoperability Profile, `evidence-interop/0` (agent-action-capsule
`spec/interop-profile-v0.md`), over the request map of
`draft-mih-agent-evidence-request-00`. It takes the request octets with their
media type (`application/json` or `application/cbor`) and answers in the same
encoding with a signed envelope carrying the canonical Artifact, or a signed
refusal with exactly one registered reason. The Artifact depends only on the
subject, the resolved anchor and the derivation: never on the requester, the
nonce, the route or the encoding. `Manifest` returns the profile's
history-free manifest.

A protected subject is answered only to a requester the transport has
authenticated: set `RespondOptions.AuthenticatedRequester` from that
authentication. With it empty, only `checkpoints` and the `history_card/1`
derivation are answered and every other subject gets a signed
`not_authorized` refusal. A declared requester id is never trusted and is not
recorded. See the CHANGELOG for the upgrade note.

## Testing evidencebook with your own verifier

To check evidencebook's answers with a verifier you wrote yourself, from the
profile text and with keys you choose to trust, follow
[docs/THIRD-PARTY-VERIFIER.md](docs/THIRD-PARTY-VERIFIER.md). It starts from a
fresh machine. It names the pinned profile, request draft and conformance
vectors, and runs a test responder (`go run ./test/interop/answer`) that
answers your own request octets. For each vector class it gives the request to
send and the result the vectors specify, and it says what to report back. Its
personal-evidence part reproduces the run the Personal-Agent History vectors
were cut from (`go run ./test/interop/personal-history`) and gives the result
specified for each of their cases.

## Checking foreign evidence

Adapters check objects produced by other protocols under each protocol's own
verification method and report every property with the method used. The first
is x402 (the payer's signed transfer authorization, the settlement response,
and an optional payee receipt):

```bash
go run ./cmd/evidencebook adapt x402 -policy trust-policy.json exchange.http
```

The ACP adapter (`adapt acp`) reads the Agentic Commerce Protocol's checkout
session, order and delegated payment token. ACP signs none of them, so it
reports possession of the exact octets and never a native `PASS`.

See [docs/ADAPTERS.md](docs/ADAPTERS.md) for the interface, what each adapter
checks and does not check, and how a failed check maps to a requirement.

## Development

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
```

CI also checks bundles both ways: a bundle produced here is verified by the
Python Agent Action Capsule bundle verifier
(`test/interop/python-aac/verify_bundle.py`) and the Python CLL reference
(`test/interop/python-cll/verify_bundle.py`), and a bundle produced in Python
(`test/interop/python-cll/build_bundle.py`) is verified here
(`test/interop/verify`); a tampered copy must fail in each direction. The
profile's reference verifier checks this responder's replies against the
profile's conformance vectors (`test/interop/responder`,
`test/interop/python-profile/verify_responder.py`), over the native stream,
the A2A carriage (`Book.A2A`) and the MCP carriage (`Book.MCP`), and checks
that all three carry a byte-identical canonical Artifact. CI also reproduces
the responder run of the Personal-Agent History vectors byte for byte
(`test/interop/personal-history`), rebuilds the corpus from it, and runs the
corpus's own test. It does the same for a run that files each step under
`correlation.exchange_id`, as capsule-cli#264's deal steps are filed
(`-correlated`), and the History built from that run passes the same test.

## Other implementations and parity

- **Rust:** the `evidencebook` crate in this repository
  ([`rust/evidencebook`](rust/evidencebook/), moved here from
  checkpointed-local-log with its history) implements the same evidence-layer
  semantics over the Rust Checkpointed Local Log: records, epistemic types,
  links, disclosure, retention, requests and reconcile/close.
- **Parity vectors** shared across implementations, all checked in CI:
  - `schemas/vendor/epistemic-types.json`: the epistemic type value set
    (`TestEpistemicTypeThreeWayParity`);
  - `testdata/refusal-interop/`: a refusal signed in Python, verified here;
    a refusal signed here is verified in Python;
  - `testdata/evidence-request/`: the request draft's spec-derived request,
    resolution and refusal vectors, vendored with their upstream SHA-256s
    (`TestRequestVectors`, `TestResolutionVectors`, `TestRefusalVectors`);
  - `testdata/reconcile-parity/`: exchange halves and the Python correlator's
    outcomes, which `ReconcileHalves` must match.
- **Bundles** are Evidence Bundle v2: anything that verifies one (the Go and
  Python Agent Action Capsule bundle verifiers) checks a bundle from a book.

[docs/design.md](docs/design.md) lists every cross-implementation check.

## License

Apache-2.0.
