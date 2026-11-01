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
| `after-write.json`, `after-continue.json` | the dump of the scripted shelf after each phase | `internal/shelfscript` |

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
- Rust: `rust/evidencebook/src/shelf/tree.rs` reads `tree.json`, and
  `rust/interop/tests/shelf_vectors.rs` also reads `correlation.json` and runs `script.json` and compares its
  dumps with `after-write.json` and `after-continue.json`.
