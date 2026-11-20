# Changelog: capsule-evidencebook (Python)

## Unreleased

- The Goose reference's `loop_book_bundle` takes `disclose` (capsule-emit#407): the bundle then reveals the header of the
  record and of each record it cites, and carries their payloads under `evidencebook/payloads`, as Go's `capsulectl disclose`
  writes them. An outcome bundle of a mailbox ask holds the outcome, the request it answers and the reply bytes.
- The Goose reference answers evidence requests by polling the mailbox its commitment advertises, and asks the same
  way (step 07, capsule-emit#407): `loop_advertise` with `relays` writes `ask.nostr {mailbox_key, relays}` and refuses an
  `answers_within` shorter than the node's `poll_interval`; `loop_mailbox_poll` answers the requests addressed to the
  node's mailbox from its node book (a signed refusal for a request outside its disclosure policy, never silence);
  `loop_ask_mailbox` and `loop_ask_mailbox_poll` seal a request, publish it to the asked node's mailbox, and record
  the verified answer, an absence at the deadline, or a late reply kept beside the absence.
- The `evidencebook.evidence` facade, first part (evidencebook#332): `seal`, `ask` and `history` over
  `Book.append`, the request interaction (`Book.request`, `record_response`, `record_absence`) and
  `check_staple`. `ask` reads an outcome as a state, never a score: the evidence request's artifact, refusal (conforming or not),
  recorded absence and a failed artifact check, plus `status_unknown` for `capacity_exceeded` and `request_unauthenticated`.
  `serve`, `find` and `evaluate` raise `NotYetAvailable` naming the work they wait on (agent-action-capsule#554,
  capsule-emit-mesh#237, capsule-cli#523, capsule-engine#276 and #278).
- A History card stapled to the holder's own message (evidencebook#275): `staple(book, derivation)` builds the
  holder's own request (pinned to its newest checkpoint, with a fresh nonce) and the envelope its book answers,
  for `history_card/1` or `standard_history/1` only, so a staple never carries more than the standard card.
  `check_staple(staple, holder)` checks it as Go's `CheckStaple` does, against the vectors Go writes
  (`testdata/staple/staples.json`). The checker refuses a card with members beyond the card's own. The holder's own staple is not counted by
  the book's limiter. The Python
  responder does not answer `standard_history/1` yet (evidencebook#328), so only `history_card/1` is stapled
  from a Python book; a staple of either from another implementation is checked.
- `Header.reproduction` reads and writes the reproduced-scratch-book marker (evidencebook#232),
  so a marked header keeps its identity in Python. Writing a reproduction book is not implemented here.
- The Goose reference's deal cites the admission it rests on (evidencebook#302): the first step of a deal
  cites the node book's `goose_admitted` GO record, a deal opened on a NO or no admission seals nothing, and
  the node book seals `goose_deal_opened`, its record of the deal, citing the same admission. `loop_deal_bundle`
  exports the opening and the admission as one Evidence Bundle, and a NO is cited by no record in it.
- The Goose reference exports a book bundle (evidencebook#301): `loop_book_bundle` / `Node.book_bundle` write
  an `evidence-bundle/v2` for one record of a book (its position, inclusion proof and the checkpoint covering
  it), built by agent-action-capsule's producer and checked by its strict verifier; with a witness directory
  the witness's receipt for the book's own checkpoint is on the bundle's `checkpoint.witnesses` (asked of the witness
  once per checkpoint), and a witnessed cadence tick adds the `x-cadence-witness/v0` extension. `loop_book_bundle` also writes the
  book's `checkpoint.cose`, the receipt and the directory beside the bundle. CI has Go verify the bundle, as a
  witnessed answer too (`test/interop/verify`).
- The Goose reference seals a model call (evidencebook#300): `loop_seal` / `Node.seal_model_call` commit one
  effect-boundary Capsule to the node book (`effect_attestation` `runtime_claimed`, `model_attestation` naming
  the provider and model id; the prompt is kept in the node book as the record's payload, the completion is
  committed by digest only) and write it as a verifier reads it;
  `test/interop/goose-capsule` checks it in Go.
- A receipt's referee is one both sides chose (x-deal-v0, evidencebook#277): `Report.referee`, required on
  an adjudicator's report and refused on the others; `normalize_referee`, `report_referee` and `shared_referee`
  (with `RefereeError` and its reasons), byte for byte with Go's `dealprofile` over the parity vectors.

### Fixed: x-deal-v0 folds both cases of a Cherokee name to the capitals (evidencebook#283)

- `evidencebook.deal` no longer copies `cases.Fold`'s Cherokee case swap; it
  uses CaseFolding.txt (`str.casefold`), which Go's `dealprofile` now matches.

### Fixed: a null deal record is no step (evidencebook#298)

- `standard_history_deal` reads a deal-log record whose payload is JSON `null` as no step, as Go's
  `dealprofile` does, instead of raising (evidencebook#298). Two history cases join the Go/Python/Rust
  parity vectors, with four `xn--` domain cases that pin the label mapping (evidencebook#299).

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

- `Cadence.history_card(n=0)` returns the profile's `history_card/1` over the cadence log as a `Staple`, as Go's
  `Cadence.HistoryCard` does (evidencebook#341); `check_staple` accepts it for `key_id` of the shelf signer.

- `Cadence.history_card` raises the new `OverLimitError` when the card would list more than `DEFAULT_MAX_ANSWER_ITEMS` (10,000) ticks, as Go's `Cadence.HistoryCard` does (evidencebook#354).

- `evidence.evaluate(pack, inputs)` decides admission by running capsule-engine's `admission evaluate` command
  (go/v0.3.0; `$CAPSULE_ENGINE` or `capsule-engine` on `PATH`) and returns a `Decision`; `EvaluateError` when the
  engine makes none (evidencebook#332). This replaces the earlier line saying `evaluate` raises `NotYetAvailable`;
  `find` and `serve` still do.

- The Goose reference's close step cites the checkpoint of the counterparty's half its node holds, and each side requests
  the other's half at the exchange's end and shares its own by default (`loop_deal_end_exchange`). `loop_deal_reconcile` now
  holds any matching half, naming its final record id and checkpoint digest; `loop_deal_close` refuses without a held half
  unless `uncited` is set; `verify_half` checks a step's `cites` links. x-deal-v0's header rule states both extra cites.
  Mesh nodes accept this form beside the current one during the overlap window (capsules-plugin#220). Step 05 maps to
  Goose's records in the example's README (evidencebook#303).

- The Goose reference gains `loop_settlement_disclosure` (the node's signed `settlement-disclosure/v1`: its own count of final settlements on a rail, for step 06's `ledger_count`) and `loop_pay_opening` (the opening of the buyer's `pay` record's payment-reference commitment, so a checker reads the reference the record commits). Neither reads a rail or moves money (capsule-emit#408).

- The Goose reference's commitment staples a `history_card/1` built as `Book.Staple` builds it, over `checkpoints` with a pinned anchor and a nonce, not over `full_history` (evidencebook#345). `evidence-commitment/1` has no member for the request, so the commitment carries the reply only.

- `Book.respond` answers from a snapshot of the log and holds the book's lock only to read the clock, call the limiter, issue an anchor a freshness floor needs and seal `evidence_request_answered`, so a flood of requests no longer blocks appends and checkpoints, as Go's `Book.Respond` does (evidencebook#217).

### Changed: deal records accept rule_inputs_version 4 (#363)

- The x-deal-v0 schemas accept `rule_inputs_version` 3 or 4 (`enum [3, 4]`, was `const 3`); 4 is the rule inputs version in which a rule's `channel` comes from the latest counterparty message only. A positive fixture (`positive-rule-inputs-4`) seals a pay check under 4 (#363).

- `Book` answers `standard_history/1` over `full_history` when given a `StandardHistorySource(holder_kind, inputs)`
  (`Book(..., standard_history=...)`), byte for byte with Go's `StandardHistorySource` (evidencebook#328): to any
  authenticated requester and never a stranger, without a source it is `derivation_unsupported`. `staple` builds a
  `standard_history/1` staple from such a book. `derive_standard_history` is Go's `DeriveStandardHistory`.

- `check_staple` refuses a `history_card/1` staple whose checkpoints are not one chain (signature under the first's key,
  one log, signed `prev_size`/`prev_root`, consistency proofs), as Go's `CheckStaple` does (evidencebook#344).

- Every half the Goose reference node gives carries the deal book's `history_card/1` as a staple (the `history_card` member). Its shelf-level `standard_history/1` card, signed as a staple under the node's producer key (`Node.history_staple`), rides in the `history_staple` member only when the counterparty asks and the user approves: `Node.half(standard_card=True)`, `standard_card=true` on `loop_deal_push` and `loop_deal_end_exchange` ("this sends your standard card"). `verify_half` checks both under the key the half's steps are signed by (`verify_history_staple`) and refuses a staple another key presents, refuses a half without its book card, and holds the book card to the half's checkpoint and the pinned checkpoint key (evidencebook#329).

- `standard_history_deal` prices a deal whose check states currency `BTC` as `{"assetCode": "BTC", "assetScale": 11}` (`amount_minor` counts millisatoshi) instead of raising `Unpriced`; `minor_units("BTC")` stays unpriced, and other codes with no ISO 4217 minor units are still refused (evidencebook#388).

### Changed: depend on agent-action-capsule 0.16 and capsule-emit 0.15 (#436)

- `agent-action-capsule>=0.16.0,<0.17` and `capsule-emit>=0.15.0,<0.16`. The library's `PROFILES` now names `evidence-interop/2`, which the book does not implement; a request naming it is refused like any unknown profile, with the detail `profiles supported: evidence-interop/0, evidence-interop/1`.

### Changed (breaking): `uniform` is the default refusal policy (#225, #206 row 8)

- `ResponderPolicy().refusal_policy` is now `REFUSAL_POLICY_UNIFORM` (an empty string reads as uniform too): a responder answers `policy_declined` wherever a more specific token would tell a counterparty whether the holder has a record for a key it does not hold. `REFUSAL_POLICY_SPECIFIC` is an explicit operator opt-in; pass `ResponderPolicy(refusal_policy=REFUSAL_POLICY_SPECIFIC)` to keep the old answers.

- The checkpoint reader accepts the CWT `sub` as the log id (Checkpointed Local Log -02) as well as the pre-02 `<log id>#<log size>`; any other subject is still refused (#449).

- `standard_history_deal` counts disputes as the deal plugin does: a buyer's dispute step with no ruling is `raised`/`open`, and a ruling step ends it `for` or `against` the holder (`raised` for a buyer's store, `received` for a seller's), so a seller with a ruling against it no longer shows a clean card (#457).

- `evidencebook.deal`: `sale_unpost` joins the record types (a sale's listing taken down; PROFILE.md section 6, rule 15), and the vendored schemas follow x-deal-v0 (#451).

- **TEMPORARY** `deal.TYPE_OUTCOME_NOTE`, the x-deal-v0 `outcome_note` record (evidencebook#462), counted `did_not_hold` by `standard_history_deal`.

### Changed: Python package on checkpointed-local-log 0.9.0 and capsule-emit 0.17.0 (#452)

- `python/` requires checkpointed-local-log `>=0.9.0,<0.10` and capsule-emit `>=0.17.0,<0.18` (were `>=0.8.0,<0.9` and `>=0.15.0,<0.16`). Its checkpoint writer now signs the CLL -02 CWT `sub`, the bare log id, instead of `<log id>#<size>`; the reader still accepts both. The interop twins (`responder.py`, `personal_history.py`) write the bare log id in their log keys, and the checkpoint-parity cases keep the signed `sub` when they change a claim.
- `capsulectl`'s Go ↔ Python round trip now resolves evidencebook v0.14.2 and installs Python CLL from `python/v0.9.0` (the `python/` subdirectory), with agent-action-capsule 0.16.0 and capsule-emit 0.17.0 locked with hashes.
- `capsulectl` keeps answering refusals with the specific tokens, explicitly (`ResponderPolicy{RefusalPolicy: RefusalPolicySpecific}`), because evidencebook v0.14.0 made uniform the library default; whether capsulectl should move to uniform is left to a disclosure decision.

- The Goose node takes `CADENCE_OPTIONS` (the cadence's interval) as a class attribute.

- `evidencebook.row_status` (new): the Go package `rowstatus`'s term row status, result for result over `testdata/row-status/vectors.json` (evidencebook#461, acceptance 6).

- `evidencebook.shelf_facts` (new): the Go package's shelf log entry documents, commitments, openings and audiences, byte for byte over `testdata/shelf/facts/vectors.json` (#461).

- `Cadence.handover` and `shelf_handover.verify_handover`: Go's whole-shelf handover and its replay (#461 §2.7), member for member. A Python shelf hands over to `testdata/shelf/handover.json`, and Python replays any language's handover to `handover-result.json`: every entry up to the anchor, each document as its canonical JSON, each book fact's status opening, its checkpoint and the consistency proofs between a book's facts, the coverage start, whether a receipt verifies against the witness directory, and each book's latest fact.

- `Cadence.migrate`, `Cadence.stamp` and `Shelf.openings`: Go's shelf log of book facts (#461), byte for byte. A Python shelf migrates a shelf Go or Rust wrote, and the reverse, to `testdata/shelf/after-migrate.json`. Shelf file versions 1 and 2 are opened.

- `Shelf.journal` and `JOURNAL_LOG_ID`: Go's shelf journal (#461 §3), byte for byte. The journal is a book of kind journal, opened only on a migrated shelf. Each stamp after it grows writes a journal fact whose size and checkpoint only its status opening holds. It is left out of `Shelf.books`, the cards' extents and the handover's latest facts. A `shelf_logs` table from before the journal's kind is rebuilt with the same rows when the journal is first opened. A Python shelf migrates a shelf Go wrote to the regenerated `testdata/shelf/after-migrate.json`.

- `Shelf.receive`, `Shelf.forget`, `Shelf.counted_prefix`, `Shelf.received_record_of` and `Shelf.evidence`: Go's store of what the other side pushes (#461 §3), with its `ReceiveProfile` protocol (`read`, `admit`, `answered`, `proof_sender`), `ReceiveConfig` and `ReceiveBounds` (the `receive` argument of `Shelf.open_sqlite`), and the new `RefusedError`, `DeletedError` and `BusyError`. `Cadence.stamp_proof` builds a `StampProof` for an audience, and `verify_stamp_proof` checks one; a `StampProof` reads and writes Go's JSON form. A refused part is returned in its outcome and in `ReceiveResult.refusals`, not raised. A store or profile error in the proof part carries the record part's result as its `receive_result` attribute. The readers open their own connection to the file, so a status provider may call them during a stamp, and a profile during `receive`. Python runs `testdata/shelf/receive/script.json` to every step's expected outcome and to `after.json` byte for byte.

- `Shelf.record_time(book_id, position, witnesses)`: Go's record time (#461 §2.6). It returns, in Unix seconds (a registration time may lie past `datetime`'s year 9999), the earliest registration time among the receipts held from the named witnesses (their receipt URLs) for a shelf checkpoint that covers the first fact of the book whose size covers the position. It returns `None` when there is none. A position that is not an integer from 1 to 2^64 - 1 raises `InvalidError`. Like the received-store readers, it takes no lock of the shelf's and reads in one transaction. `Cadence.witnessed` keeps every verified receipt in the new `shelf_receipts` table: one row per witness, with the directory key id the receipt verified under and its registration time, and the earliest time is kept. A witnessed tick that has no row there gets one when the cadence next opens, and after `import_legacy`. `Cadence.handover` carries every receipt held for its anchor, in witness URL order. `verify_handover` reports `witnessed` only for a receipt that gives a registration time. Python runs `testdata/shelf/record-time.json` after the migrate phase, gets every step's expected outcome, and its dump matches `after-record-time.json` byte for byte.

- `Handover.witnessed` and `verify_handover_times`: a handover carries the earlier witnessed shelf
  checkpoints and their receipts, `verify_handover` checks them, and a reader resolves record times from
  them as Go does (evidencebook#461, invariant C; `testdata/shelf/handover/earliest-receipt-not-anchor.json`).

- `shelf_receipts` is keyed by checkpoint, witness URL and directory key id, as Go's
  (evidencebook#492): a witness whose directory row lists two keys keeps a receipt under each.
  Opening a shelf with the old table rebuilds it in place, keeping every row; the shelf version is
  unchanged. The handover orders receipts by URL and then key.

- The whole-shelf handover ties each genesis fact to the old witnessed tree when the holder can (#461 §2.7), as Go does. `Handover.ties` holds one `HandoverTie` per book, in book id order. Each tie carries the old witnessed tick's leaf and path, its root's position in the shelf log, the cadence checkpoint and its receipts, the old book checkpoint, and a consistency proof to the genesis checkpoint. `Cadence.handover` builds each tie from the witnessed tree tick with the log's largest size not past the genesis size. A log with no such tick, or a leaf that kept no checkpoint statement, gives no tie. `verify_handover` checks each tie and raises `InvalidError` on one that does not verify, on two ties of one book, and on a tie whose book has no genesis fact. A genesis fact without a tie is still reported in `untied`. Python's handover of the migrated vector shelf equals `testdata/shelf/handover.json`, and its replay equals `handover-result.json`.

- The Goose reference's `Terms` takes `demo` (default `False`): a deal opened with `demo=True` seals `demo: true` in its
  baseline, the x-deal-v0 baseline's optional member, which a receipt viewer shows as a demonstration deal. A deal
  opened without it is sealed as before, with no `demo` member.

- `history_card/2` (Evidence Request -03 §8.1.2, evidencebook#507), as Go's: `history_card_positions`
  (P(n, W)), the answer a `Book` builds over `checkpoints` or `full_history`
  (`ResponderPolicy.history_window`, 1 to 64, default 16, not capped by `max_answer_items`),
  `check_history_card_2` (the spec's seven ordered checks, also run on a requester's verification) and
  `staple(book, DERIVATION_HISTORY_CARD_2)`; `check_staple` accepts the card. A `checkpoints` subject
  with a range is answered with the entries and a `consistency_to_anchor` proof, without a card, and verified by a requester. It reads the shared
  vectors in `testdata/history-card-2`. Needs agent-action-capsule 0.17.0.

- The ARD two-seller demo moved to evidencebook-skills as `demo/ard` (evidencebook-skills#171; tracked in evidencebook-skills#169), with its work since the last release: its Goose Desktop windows (#483), the seller window that no longer retakes an answered request (#495), and the fee wording on a real rail (#499). It sits there beside the buyer and seller skills it runs and its admission pack. `python/examples/ard-commerce` is removed. The demo reads this repository's Goose loop reference (`python/examples/goose`) and the x-deal-v0 checker from a pinned checkout, so no code of this repository is copied.

### Added: the shelf hooks a deal profile runs on (evidencebook#461 step 5, PR 6c-1)

The Go shelf's hooks from #484, #490, #517, #525, #526, #528 and #530, which the Rust crate gained in #536 and #539:

- **Held readers:** `Shelf.held_first`, `held_by_deal` and `held_proofs` (`HeldProof`).
- **Record times:**
  - `Shelf.record_time_by_key`, `received_record_time` and `received_record_time_by_key`;
  - `Shelf.record_stamp` and `received_record_stamp`.
- **Book writes:**
  - `Book.append_at` appends only the next record; any other position is `BusyError`.
  - `Book.resolve_payload` reads a payload as the record's retention state allows.
- **`Shelf.seal_sent`** (`SealedSent`, `SentCapsule`): a journal record and its standalone Capsule. The Capsule is byte for byte Go's `emit.Seal` output, and a test pins the same values the Rust test pins.
- **Book status:**
  - It may be an object with `book_values(StatusInput)`, handed the stamp's tick, the book and the existing journal, and `before_stamp(at)`, run before each stamp under the receive lock.
  - A book whose status raises keeps its last fact and is listed in `TickResult.unstated`; `TickResult.before_stamp` reports a `before_stamp` error.
  - `Shelf.existing_journal` returns the journal without creating it.
- **Receive:** a receive profile may define `after_receive(result, at)`, which runs on every receive. `RecordOutcome.deal_id` names the record's deal.
- **Fixed:** a lifecycle record's statement is read as Go reads it. `retention_state` matches in any case, and a state that is not a string is corrupt.

- **Added (`evidencebook.deal.v1`):** the x-deal-v1 deal record profile's names, a record's strict reading (`decode`, as Go's `xdealv1.Decode` reads it into its types), its checks (`check`, `check_sealed`, `check_agreement`, `check_dispute`, `check_ruling`), `encode`, `digest`, `rows_digest`, `placement_of`, and when a deal has started (`start`, `start_from_books`), byte for byte with Go (evidencebook#461). A record is read through a table of the Go types that `dealprofile/xdealv1`'s `TestTypesTable` writes by reflection, and the x-deal-v1 differential check in CI now compares Python with Go on every record it mutates.

- **Added (`evidencebook.deal.v1`):** the x-deal-v1 deal book's status value (`build_status`, `resolve_row`, `row_inputs`) and a copy of the report built from books (`build_report`, `report_payload`, `settled`), byte for byte with Go (evidencebook#461). `evidencebook.row_status.compute_late` gives a row's late statements and disputes, as Go's `rowstatus.ComputeLate` does, and passes `testdata/row-status/late.json`. The x-deal-v1 differential check now compares Python with Go on status values and reports too.

- **Added (`evidencebook.deal.v1`):** `check_authorizations`, the x-deal-v1 authorization of acts (design §3.2, invariant D): each deal-book perform or settle that cites a check is read against the holder's journal, with v0's limits in force, approvals and confirmations, as Go's `CheckAuthorizations` reads them (evidencebook#461). It passes `testdata/deal-v1/authorization/approval-migration.json`, and its refusal texts are Go's.

- **Added (`evidencebook.deal.v1`):** byte for byte with Go (evidencebook#461):
  - sent records and book pairs: `pair` and `verify_sent` for an offer, decline or cancellation sent whole, and `book_pair` and `verify_book` for a deal-book record with its header and payload;
  - `terms_leg` and `payment_rows`;
  - `payment_check`, the recomputed check of a payment row over AAC's settlement legs.

  `verify_book` reads the header as Go's `json.Unmarshal` reads `evidencebook.Header`, from the types table, which now holds it. The package passes `testdata/deal-v1/decline` and `testdata/deal-v1/payment`, including Go's `BookPair` bytes.

- **Added (`evidencebook.deal.v1`):** the x-deal-v1 holder's `ReceiveProfile` (`read`, `admit`, `answered`, `proof_sender`, `can_act`) and its act gate (`gate_act`), as Go's `ReceiveProfile` and `GateAct` (evidencebook#461), with `version`, Go's `dealprofile.Version`. Go's `TestHolderTranscripts` writes each receive and gate scenario as a transcript (`testdata/deal-v1/holder`). The Python test replays every step on Python shelves with the same keys, clock and random stream, and compares each output with Go's: the sealed pairs, record ids and stamp proofs byte for byte, and every receive outcome and profile answer.

- **Added (`evidencebook.deal.v1`):** the holder's naming of rulings, as Go's `name_rulings.go` (evidencebook#461, #527): `book_entry`, `name_rulings` and `name_all_rulings`. `ReceiveProfile.after_receive` now names each counted ruling of the deals a receive call touched before the call returns. Go's `TestHolderTranscripts` now also records six naming scenarios, and Python replays each one, comparing the naming statements it seals and the whole book byte for byte.

- **Added (`evidencebook.deal.v1`):** `StatusProvider`, Go's x-deal-v1 `StatusProvider` (evidencebook#461).
  - `book_values` gives a v1 deal book's profile, link value and status value at each stamp.
  - `before_stamp` names any ruling still unnamed.
  - `report` computes a copy of the v1 report from what the shelf holds.
  - Go's `TestHolderTranscripts` now also records the stamp and report scenarios, and Python replays them, matching each fact, opening and report byte for byte.
  - `testdata/deal-v1/forked-finals` holds a forked case book's conflicting rows.
- **Added (`evidencebook`):** `Entry.members`, Go's `Entry.Members`: further top-level members of a record's Capsule (a settlement leg's, for one), which its id and signature cover.

- **Fixed (shelf):** `Shelf.counted_prefix` now reads a sender's records and recorded forks in one read transaction, as Go does (evidencebook#543).

- **Fixed (`evidencebook.deal`):** the x-deal-v0 schema stage reads each schema `pattern` as Go's validator (RE2) does, as x-deal-v1's already does (evidencebook#546). A final `$` no longer matches before a trailing newline, and `\s` no longer takes `\v` or Unicode white space, so a record that Go refuses or passes at the schema stage is refused or passed here too. The parity vectors carry both cases.

- **Added (`evidencebook.deal.v1`):** `verify_report` and `report_pair`, Go's `VerifyReport` and `ReportPair` (evidencebook#554). They check a sealed v1 deal report against its deal's first record, and read the payload against Go's `Report` type from the types table, which now holds it. The package replays Go's `testdata/deal-v1/report-verify` vectors, and the `x-deal-v1` differential compares it with Go.

- **Fixed (`evidencebook.deal.v1`):** as Go now does (evidencebook#559), `verify_sent`, `verify_book` and `verify_report` refuse a pair whose members are not exactly its own, and `verify_book` refuses a header carrying a member its type does not have exactly. `payment_check` reads a settlement member's `leg`, `sealer_role` and `terms_ref` by their exact names.

- A history_card/1 or unranged checkpoints answer whose stated `anchor.freshness` is not what the anchor checkpoint commits is now refused, as for the other answer forms, including a stapled history_card/1 (#521).

### Added: the MCP and A2A entries of `Shelf.receive` (evidencebook#577)

- `evidencebook.push` (`PushMCPServer`, `PushA2AServer`, `a2a_push_extension`) carries a record push and a stamp proof over MCP (the tool `record_push`) and A2A (one Agent Card extension, one `data` part) to `Shelf.receive`, as `spec/bindings/record-push-v0.md` of agent-action-capsule specifies and Go's `Shelf.PushMCP` and `PushA2A` do, on the shared carriage vectors (`testdata/shelf/receive-carriage`). A part's refusal is an answer; a malformed push is an invalid-params error and a receiver fault an internal error, never an answer.

### Added: `check_staple` reads a `standard_history/3` staple (#579)

- `DERIVATION_STANDARD_HISTORY_3` names `standard_history/3`, and `check_staple` accepts a staple of it over `full_history`, checking the card with `agent_action_capsule.standard_history.check_card(..., derivation=...)`. A book still builds no /3 card, so `staple` refuses the token. `standard_history/1` is unchanged.
