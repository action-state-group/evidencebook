# Shelf vectors

The shelf contract's byte vectors (evidencebook#254 §5), written by the Go
implementation and read by every other. Each file is regenerated only with
`EB_WRITE_SHELF_VECTORS=1` and otherwise compared byte for byte.

| File | What it is | Written by |
|---|---|---|
| `cadence/` | the cadence tree and a witnessed chain written by capsule-cli's code at `7a6b101`, moved from `testdata/cadence` unchanged | capsule-cli |
| `tree.json` | cadence leaves, fillers, derived fillers, empty roots, trees with every path, padding record ids, and draws from the shelf's random source | `shelf_vectors_test.go` |
| `script.json` | the scripted shelf: its host parameters, keys and two phases of steps | `internal/shelfscript` |
| `correlation.json` | records carrying each correlation member or none, and the positions each identifier answers with (`correlationOf`) | `shelf_correlation_vectors_test.go` |
| `after-write.json`, `after-continue.json`, `after-migrate.json`, `after-recover-tick.json`, `after-recover-migration.json` | the dump of the scripted shelf after each phase; the migrate phase switches it to a log of book facts (evidencebook#461) and stamps it | `internal/shelfscript` |
| `receive/script.json`, `receive/after.json` | the received store's script of pushes, proofs, forgets and a reopen, each step's expected outcome, and the store's dump after it (evidencebook#461 §3, §7; `receive/README.md`) | `shelf_receive_vectors_test.go` |
| `handover.json`, `handover-result.json` | the whole-shelf handover of the shelf after the migrate phase, at its newest stamp, and its replay (evidencebook#461 §2.7) | `internal/shelfscript` |
| `record-time.json`, `after-record-time.json` | the record time phase run on the shelf after the migrate phase, each step's receipt and expected outcome, and the dump after it (evidencebook#461 §2.6) | `internal/shelfscript` |

## The random source

A shelf draws every salt, tree position, filler nonce and seed, jitter and
padding nonce from one injected source. The vectors use the stream whose block
`i` (from 0) is `SHA-256(UTF8(seed) ‖ uint64be(i))`. The draws are defined on
the bytes they read:

- `bytes(n)`: the next `n` bytes;
- `uniform(n)`: the next 8 bytes as a big-endian `x`, read again while `x` is
  at or above the largest multiple of `n` not over 2^64; the value is
  `x mod n`;
- a tree position: `uniform(2^16)`, drawn again while it is taken.

A tick draws, in this order: filler 0's position and its nonce (32 bytes);
for each live log that holds an entry, in log id order, its salt (32 bytes)
and its position (a live log with no entry yet, a host log opened in the
cadence and not yet written, is skipped and counted among the derived
fillers); the derived fillers' seed (32 bytes) when the tick has any; the jitter,
`uniform(2 × jitter_ns + 1) − jitter_ns` nanoseconds, when the cadence has
jitter; then each padding record's nonce (32 bytes).

## The dump

A dump is the shelf's tables row for row (stored JSON as its text, record
capsules and envelopes as their SHA-256), every CLL entry and each CLL log's
node digest, and what the shelf derives: `Records` (with any pending record),
each finished tick's tree root and `StandardHistoryAnchor` with its
`Extents`, and the cadence chain (`Cadence.Chain`) of every checkpoint a tick
carries, keyed `"<tick> <log id>"`. An implementation agrees when its dump of
the same script equals the vector's byte for byte. The dump's encoding is Go's
`encoding/json` `MarshalIndent` with a two-space indent: object keys in
byte order, `<`, `>` and `&` written as `\u003c`, `\u003e` and `\u0026`,
no other escaping beyond JSON's, and one final newline.

The storage interlock (CI job `shelf-interlock`) runs the phases in separate
processes: Go's `test/interop/shelf`, Python's
`test/interop/python-evidencebook/shelf.py` and Rust's `rust/interop` bin
`shelf`. Every language writes, and continues every language's file
(its own included), and every language's dump is compared with these files.

## Readers

- Python: `python/tests/test_shelf.py` reads every file here, `correlation.json` included.
- Rust: `rust/evidencebook/src/shelf/tree.rs` reads `tree.json`.
  `rust/interop/tests/shelf_vectors.rs` reads `correlation.json`, and runs
  `script.json` and compares the script's dumps with `after-write.json` and
  `after-continue.json`.

## The migrate phase (evidencebook#461)

The migrate phase runs `Migrate`, then stamps on demand and on the heartbeat.
Migration draws, for each genesis fact in book id order, its link salt then
its status salt (32 bytes each), then the jitter; every stamp draws the same
for each fact it writes, then the jitter. A version 2 dump adds two tables:
`shelf_entries` row for row (`doc` and both openings as their stored text) and
`shelf_migration` as `{state, backed_up}`, leaving out the backup's digest,
which covers SQLite's own bytes. A stamp's `tick_roots` entry is
`stamp:<SHA-256 of its stamp entry>`, and a fact whose checkpoint did not grow
has no chain.

Every dump lists `shelf_receipts` (evidencebook#461 §2.3) by `checkpoint_sha256`, then
`witness_id`, then `key_id` (evidencebook#492): `key_id` the directory key it verified under, each verified receipt as the stored text `cadence_ticks.receipt` holds,
`registered_at` in Unix seconds or null. The scripted receipts carry no registration time,
so after the migrate phase the handover replays as recomputed and unwitnessed.

## The recovery scenarios (evidencebook#461 §7)

On the shelf after the continue phase, Go plants a crash and any language finishes it
with the recover phase (`Migrate`, then a stamp):

- `after-recover-tick.json`: an empty host log in the cadence and a tree tick stored but
  not finished (`plant-tick`). `Migrate` finishes the tick first, lists the empty log in
  the marker at size 0 and gives it no fact.
- `after-recover-migration.json`: a migration that stopped in its step 4 (`plant-migration`):
  version 2, state `migrating`, its backup recorded and in place, its batch stored, and the
  batch's first two entries appended. `Migrate` resumes it, reusing the backup and appending
  only the missing entries.

A plant runs the interrupted operation on a copy and copies back only what the crash would
have left; after the continue phase every book is checkpointed at its size, so no book
checkpoint is involved.

## The handover (evidencebook#461 §2.7)

`handover.json` is `Cadence.Handover` of the shelf after the migrate phase at
its newest finished stamp (tick 0 asks for it), and `handover-result.json` is
`VerifyHandover` of it under the script's checkpoint key and witness
directory, both in the dump's encoding. Byte strings (`doc`, the statements,
`anchor`, a receipt's `receipt`) are standard base64 with padding, as Go's
`encoding/json` writes `[]byte`; a consistency proof is
`ConsistencyEvidence`'s JSON. `latest` and `untied` are in book id order. A
genesis fact comes with its tie to the old witnessed tree (`ties`, in book id
order: the tick's leaf, its 16-hash `path` in lowercase hex, the root's
`entry_seq`, the `cadence_checkpoint` and its `receipts`, the `old_checkpoint`,
and a `consistency` proof to the genesis checkpoint unless the two are of one
size), taken from the witnessed tree tick with the log's largest size not past
the genesis size; after the migrate phase every book is tied. Every language hands the shelf over to `handover.json` and
replays every language's handover to `handover-result.json` (the
`shelf-interlock` job); the commands are `handover DIR` and
`verify-handover FILE`.

## Record time (evidencebook#461 §2.6)

`record-time.json` holds the record time phase: run its steps on the shelf after the migrate
phase, in order, and every step's `expect` must match; the dump afterwards is
`after-record-time.json`. A `witness` step reports its `receipt` (as the handover encodes one)
for the newest finished tick and expects that tick and the check's state; a `record_time` step
calls `RecordTime(book_id, position, witnesses)`, witnesses being receipt URLs, and expects
`{"time": <RFC 3339 UTC or null>}` or `{"error": "invalid"}`; a `handover` step hands the shelf
over at its newest stamp, verifies it under the script's checkpoint key and directory, and
expects `{"witnessed": <bool>}`. The phase covers a record whose only covering receipt has no
registration time, the plain case, a witness not named, a position no fact covers, position 0,
the journal, a later checkpoint covering an earlier fact, the same witness again with an
earlier time (kept) and with none (ignored), and a handover witnessed once its anchor's receipt
has a time. Each runner's `record-time DIR` runs the phase on a shelf after the migrate phase
and fails on any outcome that is not the vector's; the `shelf-interlock` job runs it in every
pair of languages and compares every dump afterwards.
