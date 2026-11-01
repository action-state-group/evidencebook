# Changelog: capsule-evidencebook (Python)

## Unreleased

- The Goose reference seals a model call (evidencebook#300): `loop_seal` / `Node.seal_model_call` commit one
  effect-boundary Capsule to the node book (`effect_attestation` `runtime_claimed`, `model_attestation` naming
  the provider and model id, prompt and completion committed by digest) and write it as a verifier reads it;
  `test/interop/goose-capsule` checks it in Go.

### Fixed: x-deal-v0 folds both cases of a Cherokee name to the capitals (evidencebook#283)

- `evidencebook.deal` no longer copies `cases.Fold`'s Cherokee case swap; it
  uses CaseFolding.txt (`str.casefold`), which Go's `dealprofile` now matches.

- The Goose loop reference, `python/examples/goose/` (evidencebook#265): a Goose MCP server whose
  node keeps one book per deal of x-deal-v0 steps on a shelf, imports an existing capsule-emit
  ledger as a host log without re-signing it, runs the cadence, and answers `history_card/1` per
  book and `standard_history/1` as of a tick. CI runs a scripted two-node run over MCP stdio and
  checks each node's card with Go (`test/interop/goose-card`) and each node's deal half with the
  crates a capsules-plugin v0.1.8 Mesh node verifies halves with (`test/interop/goose-mesh-check`).
  It is an example, not part of the package.
- `counterparty_class` (evidencebook#273): the vendored x-deal-v0 schema allows the optional top-level
  member on a pay check, and `standard_history_deal` fills the entry's `counterparty_class` from the
  one record that carries it (a pay check, one of SH's three values); two carriers, a record that is
  no pay check, or another value read as absent. Eleven history and four check cases join the
  Go/Python parity vectors.
- Records whose Capsule their caller seals, and field commitments, as Go's book makes them
  (evidencebook#265, the deal books the Goose reference keeps):
  - `Book.append_capsule(entry, originals, seal)` (Go `Book.AppendCapsule`): the book takes the
    next position and builds the header, the caller's sealer returns a Capsule carrying it in
    `evidence_record_header`, and the book accepts it only if it verifies as that record and is
    signed by the book's signer.
  - `Entry.fields`: salted `sha256-jcs-nonce256` commitments in the header's `field_commitments`,
    each opening kept as an original `opening.<field>`; `Book.opening` reads one back, checked
    against its commitment (`Opening`, `new_opening`, `verify_opening`), and raises the new
    `UnavailableError` for a withheld or deleted record, as Go does.
  - A book reopens records whose Capsule carries its header, as Go's does, and checks every stored
    Capsule as Go's `emit.VerifyCapsule` does (AAC format 4 under JCS, Class 1, its id). Before
    this, a Python book refused a deal book a Go writer left on a shared shelf.
  - `evidencebook.deal.emit_input`: a step's or report's Capsule members as
    `capsule_emit.produce.Input`, for the producer's own `produce.seal`.
  - A Go writer and a Python continuer of one deal book (and the reverse) read the same steps and
    openings (`test/interop/caller-sealed`, CI job `shelf-interlock`).
- x-deal-v0 in Python: `evidencebook.deal`, at byte parity with Go's `dealprofile`
  (evidencebook#264, design evidencebook#254 §8), so a Python shelf can derive
  `standard_history/1` from deal books.
  - Names, `normalize`, `fingerprint`, `deal_key`, `profile_key` and `commit_text`
    (fingerprints under Go's Unicode rules: `strings.ToLower`, `cases.Fold` with the Cherokee small forms folded to the capitals (evidencebook#283), the White_Space
    trim, x/net's `idna.Lookup` and its public suffix tables, vendored byte for byte), the
    record-level stages (`check`, `encode`, `digest`, `check_digest`: canonicalization,
    personal_data, wording, schema) over the profile's vendored schemas, `report_payload`,
    `report_capsule`, `step_capsule` (plain Capsule members for the producer's sealer, as the
    Rust crate's), `preimage_matches` (`body_evidence.preimage`) and
    `standard_history_deal`, the SH §5.2 deal entry that `agent_action_capsule.standard_history`
    derives a card from.
  - A record is a `dict`, not Go's typed structs. The chain stage stays the reference
    checker's (`check_profile.py`). A refusal matches Go's in its stage; its text is
    `jsonschema`'s at the schema stage. The Unicode tables are Python's (`unicodedata`, and
    `idna` for UTS 46), not Go 1.27's (Unicode 17), so a character newer than the running
    Python's tables may normalize differently.
  - Passes the profile's fixtures, `dealprofile/testdata/baseline-vectors.json` and
    `dealprofile/testdata/parity-vectors.json` (Go's answers for Unicode, number and member-type
    edges, the deal entries and the cards derived from them); the chains it builds are Go's
    byte for byte and pass `check_profile.py` (CI job `python`).
  - Domains were checked against Go's `idna.Lookup` with a differential fuzz of about 180,000
    mixed-script, multi-label domains, and every code point alone: the differences left are
    characters newer than the running Python's tables, and a ZERO WIDTH NON-JOINER between an
    Arabic letter and a digit, which Go accepts.
  - The package requires `jsonschema` 4.23 and `idna` 3.20.
  - `counterparty_class` is absent from the deal entry, as in Go; evidencebook#273 names the
    record that will carry it.

- The shelf: one subject key's books in one SQLite file, the file Go's shelf writes
  (evidencebook#263, design evidencebook#254).
  - `Shelf.open_sqlite` takes an exclusive `flock` on `<file>.lock` or a lock the host holds,
    creates or checks the shelf's tables (`shelf_meta`, `shelf_logs`), and opens one book per
    log id: `SqliteStore` keeps its records and checkpoints, `SqliteSubstrate` its Checkpointed
    Local Log over CLL's SQLite backend and checkpoint runner, and its payloads go to the
    shelf's `capsule_emit.artifact` store. A book's open appends a record stored but not yet in
    its log, at its `committed_at`; a log entry with no stored record is corrupt.
  - `Shelf.host_log` and `Shelf.legacy` open logs whose records the host seals itself;
    `Shelf.records` lists committed records without opening a book.
  - `Cadence` (`Shelf.cadence`) cuts ticks on time alone, verifies witness receipts with AAC's
    verifier (`witnessed`), and gives `chain`, `standard_history_anchor` and `import_legacy`.
    Every salt, position, nonce and jitter comes from one random source.
  - The scripted shelf of `testdata/shelf` reproduces Go's dumps byte for byte, and Go and
    Python write and continue each other's files (CI job `shelf-interlock`).
  - `Book` takes a `substrate`, `originals` and `indexes`; a record whose log append fails
    keeps its seq and is appended first. `Header` keeps `field_commitments` and `commit_alg`.
    `UnsupportedError` is new.
  - The package requires `checkpointed-local-log` 0.8 and `capsule-emit` 0.14.

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
