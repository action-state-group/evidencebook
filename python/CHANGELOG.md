# Changelog: capsule-evidencebook (Python)

## Unreleased

- **The package** (evidencebook#131), imported as `evidencebook`: a book over
  an in-memory Checkpointed Local Log, with `MemoryStore` and the JSONL
  `FileStore`, and the evidence request interaction on both sides. As a
  requester, `Book.request` seals `evidence_request` before sending,
  `Book.record_response` verifies a reply under the pinned responder keys and
  seals `evidence_response`, and `Book.record_absence` seals
  `recorded_absence`. As a responder, `Book.respond` answers with a signed
  envelope or refusal and seals `evidence_request_answered` for an
  authenticated requester, with `Book.register_derivation` and
  `Book.commit_retention`. It passes the corpora the Go module passes:
  `testdata/evidence-request/` (33 request, 18 resolution and 21 refusal
  cases), 20 of the 53 interop profile cases (the 8 A2A and MCP carriage
  cases are not in this package), and the Personal-Agent History responder
  run, which it reproduces byte for byte. Not yet published.
