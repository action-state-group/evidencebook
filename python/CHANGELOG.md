# Changelog: capsule-evidencebook (Python)

## Unreleased

- `Book.respond` answers `evidence-interop/1` requests (evidencebook#248).
  - A request naming `/1` gets the same artifact as under `/0`, in an envelope naming
    `/1`; a request naming `/0` or no profile is answered as before. An unknown profile
    is refused `request_malformed` with the detail `profiles supported:
    evidence-interop/0, evidence-interop/1`. `PROFILE_V1` names the identifier.
  - A `/1` request refused for a limit of the book's own, its size, the rate limiter or
    the answer item cap, is refused `capacity_exceeded`, under either refusal policy
    (ER -02 §11). A `/0` request keeps `policy_declined`.
  - `refusal_conforming` takes the profile the request asked for, `PROFILE` by default:
    the two reasons ER -02 adds conform only under `/1`. The book still asks under `/0`.
  - The package requires `agent-action-capsule` 0.14, which reads `/1`.

- `Book.request_sent` records a request that another tool composed and sent, exactly as given,
  as Go's `Book.RequestSent` does (evidencebook#247).
  - Every member is kept, including those the request draft does not define (the capsules
    plugin's `audience`, `requester_id` and `requester_auth`), and no nonce or profile is
    added. The octets must be a well-formed request in JCS, so the recorded digest is the one
    the responder signed its reply over. A nonce the book already used is refused; a request
    without one is recorded every time. The record marks the request `composed_elsewhere`, so the
    book is not read as vouching for members it keeps but never checks, such as `requester_auth`.
  - A record-set entry's body may be the capsule as the capsules plugin's ledger holds it,
    bound by its `capsule_id`. The live-16 replies (`testdata/live16-ask/`) verify as
    artifacts.

- `Book.record_response` applies the requester's limits as Go's `RecordResponse` does
  (evidencebook#209).
  - The constants are `MAX_REPLY_BYTES` (8 MiB), `MAX_REPLY_DEPTH` (32),
    `MAX_REPLY_ITEMS` (10,000) and `MAX_REPLY_RECEIPTS` (64).
  - A reply over a limit on its unauthenticated octets is refused, naming the limit, and is
    not recorded.
  - A signed envelope over a count or artifact-depth limit is received and failed, naming
    the limit.
- The response statement gains `witness`, which is `witnessed` or `unwitnessed`. Receipts
  are read from the reply's `witness_receipts` member and checked under the new
  `ResponderKeys.witnesses`, a tuple of `WitnessRow`. It is the requester's reading under its own directory
  when it recorded the answer; the carried receipts stay in the record's original, so
  anyone holding it can check them again.
- `parse_reply` and `strict_json` refuse JSON nested deeper than 32. Before this, on
  Python 3.13 and earlier, `strict_json` raised an uncaught `RecursionError` on deeply
  nested JSON. `record_response` reaches it through a signed envelope whose artifact
  nests deep.

- `Book.respond` answers an underived `correlation` subject under the rule of
  `record` and `exchange`, as Go's `Respond` does (evidencebook#114): under
  `counterparties`, only to a counterparty every record held under the key
  names; under `peers`, to any authenticated peer; never to a stranger.

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
