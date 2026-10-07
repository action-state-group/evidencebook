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

## Install

Requires Go 1.27 or newer.

```bash
go get github.com/action-state-group/evidencebook
```

## Use

```go
store, _ := evidencebook.OpenFileStore("book/records")
substrate, _ := evidencebook.OpenCLL("book/cll.jsonl", "orders-log", checkpointKey)
payloads, _ := evidencebook.OpenPayloadDir("book/payloads")
signer, _ := evidencebook.NewEd25519Signer(recordKey)

book, err := evidencebook.Open(ctx, evidencebook.Config{
	BookID: "orders", Operator: "example-operator",
	Store: store, Substrate: substrate, Payloads: payloads, Signer: signer,
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
(`test/interop/verify`); a tampered copy must fail in each direction.

## Other implementations and parity

- **Rust:** the `evidencebook` crate in
  [checkpointed-local-log](https://github.com/action-state-group/checkpointed-local-log)
  (`rust/evidencebook`) implements the same evidence-layer semantics over the
  Rust Checkpointed Local Log: records, epistemic types, links, disclosure,
  retention, requests and reconcile/close.
- **Parity vectors** shared across implementations, all checked in CI:
  - `schemas/vendor/epistemic-types.json`: the epistemic type value set
    (`TestEpistemicTypeThreeWayParity`);
  - `testdata/refusal-interop/`: a refusal signed in Python, verified here;
    a refusal signed here is verified in Python;
  - `testdata/reconcile-parity/`: exchange halves and the Python correlator's
    outcomes, which `ReconcileHalves` must match.
- **Bundles** are Evidence Bundle v2: anything that verifies one (the Go and
  Python Agent Action Capsule bundle verifiers) checks a bundle from a book.

[docs/design.md](docs/design.md) lists every cross-implementation check.

## License

Apache-2.0.
