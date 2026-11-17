# Receive vectors

The received store's vectors (evidencebook#461, `docs/design/461-shelf-facts.md` §3 and §7),
written by `shelf_receive_vectors_test.go` with `EB_WRITE_SHELF_VECTORS=1` and otherwise compared
byte for byte. An implementation agrees when it runs `script.json` on a fresh shelf and produces
every step's `expect` and the dump `after.json`, byte for byte.

| File | What it is |
|---|---|
| `script.json` | the vector profile, the store's bounds and retention, the input records and stamp proofs, and the steps, each with its expected outcome |
| `after.json` | the store's tables after the last step, and each sender's counted prefix |

Both files are Go's `encoding/json` `MarshalIndent` with a two-space indent, object keys in the
order the Go structs list them (maps in byte order), `<`, `>` and `&` written as `\u003c`, `\u003e`
and `\u0026`, and one final newline, as the other shelf vectors are (`../README.md`).

## The vector profile

A test profile, not a record format: it stands in for a real profile (x-deal-v0) so any language
can implement `ReceiveProfile` in a few lines.

- A record's bytes are the canonical JSON (RFC 8785) of
  `{"v": 1, "sender", "log", "position", "prev", "digest", "deal", "offer", "sig"}`. `digest` is the
  record's entry in its sender's book log; `prev` is `""` at position 1.
- `sig` is `hex(SHA-256(UTF8(secret) ‖ JCS(the record without "sig")))` under
  `profile.secrets[sender]`.
- **Read** refuses bytes that are not that canonical JSON, a member it does not know, a version other
  than 1, a sender with no secret, or a wrong `sig`.
- **Admit** admits an offer from any sender, and any other record from a sender in
  `profile.admitted`.
- **Answered** is true for the digests in `profile.answered`.
- **ProofSender** maps a proof's checkpoint key id through `profile.proof_keys`; a key not listed is
  refused.

## Stamp proofs

`proofs` holds `StampProof` in its JSON encoding, the one Rust and Python decode:

| Member | Encoding |
|---|---|
| `fact` | the fact document's canonical JSON bytes, standard base64 with padding (RFC 4648 §4) |
| `status_opening` | `{"salt": "<hex>", "value": <the committed JSON value>}`, absent when not given |
| `book_checkpoint`, `shelf_checkpoint` | the COSE checkpoint statements, standard base64 with padding |
| `inclusion` | `{"kind": "cll-mmr-inclusion", "seq": <entry position from 1>, "proof": {"v", "kind", "size", "leaf_index", "witness", "peaks_left", "peaks_right"}}`, digests as lowercase hex |

`p1` and `p2` are two proofs of one book checkpoint of a Go-made shelf, the second under a later
shelf checkpoint after a received-only change; `pc` is a proof from another Go-made shelf under the
same checkpoint key, of a book of the same size with another root. The store keeps a proof's
`inclusion` as its canonical JSON.

## Steps

Each step sets the store's clock to `at` for the whole step (every time the store writes during the
step is `at`, in the form `2006-01-02T15:04:05.000000000Z`), then:

- `receive`: `Receive(record, proof)` with the named record's bytes and the named proof, either or
  both;
- `forget`: `Forget(deal, at, settled)`, `settled` reporting the deal settled or not; retention is
  `retention_seconds`;
- `reopen`: release the shelf and open it again.

`expect` holds the call's error class (`""`, `"invalid"`, `"refused"`, `"over_limit"`, `"busy"` or
`"deleted"`), each part's outcome with its refusal class, the returned changes (each list sorted),
and for `forget` the digests deleted (absent when none).

The script covers §7: a duplicate; a gap stored and not counted; a proof past the records held,
verified and covering the deal when the gap is filled; a duplicate record carrying a proof it lacked
(the same book checkpoint under another shelf checkpoint); the same proof again; a contradicting
proof (refused, its evidence kept); a late fork (the prefix shrinks); a sender not admitted; offers
under a bound of two per sender, the oldest answered offer leaving the bound and the oldest
unanswered one dropped; a `Forget` refused before the deal is settled, then deleting it and leaving
a tombstone; the deleted record pushed again, read as deleted; and a reopen that recomputes the
counted prefix from the store.

## The dump

`after.json` lists, in this order and each in the stated row order:

| Member | Rows |
|---|---|
| `shelf_received` | by `digest` |
| `shelf_received_proofs` | by `sender_key`, `log_id`, `size`, `shelf_checkpoint_sha256` |
| `shelf_received_tombstones` | by `digest` |
| `shelf_evidence` | by `id` |
| `shelf_received_conflicts` | by `sender_key`, `log_id`, `kind`, `at` |
| `shelf_received_vacated` | by `sender_key`, `log_id`, `position`, `digest` |
| `counted_prefixes` | `CountedPrefix` (`size`, `last`, `fork_at`) for each sender and log any of `shelf_received`, `shelf_received_conflicts` and `shelf_received_vacated` names, by sender then log |

Each row is an object of its columns: TEXT and INTEGER as they are (`offer` as 0 or 1), every BLOB
as lowercase hex, and null as null.
