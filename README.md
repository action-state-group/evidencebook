# evidencebook

`evidencebook` is a Go implementation of the evidence layer: a local evidence
store that records evidence and the typed links between records, commits
payload digests while payload bytes are resolved separately, keeps three
distinct classes of index, and answers requests for evidence with portable
bundles that anyone can verify.

A book embeds a commitment substrate and never exposes it. The Checkpointed
Local Log ([cll-go](https://github.com/action-state-group/cll-go)) is the
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

CI also verifies a bundle produced here with the Python CLL reference
(`test/interop/python-cll/verify_bundle.py`).

## License

Apache-2.0.
