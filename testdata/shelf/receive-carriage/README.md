# Receive carriage vectors

The MCP and A2A entries of `Shelf.Receive` (evidencebook#577), built to
`spec/bindings/record-push-v0.md` §2 to §4 of agent-action-capsule (merged 561b9b23). They are
the evidencebook side of open point RP-1 there: this item writes no Internet-Draft, and the file
is mirrored into agent-action-capsule's `vectors/` when that repository takes carriage vectors.
An implementation agrees when it runs each track on a fresh shelf and returns, for every `call`
case, the message in `expect`.

`script.json` is written by `interop_push_vectors_test.go` with `EB_WRITE_SHELF_VECTORS=1` and is
otherwise compared as it stands; it is Go's `encoding/json` `MarshalIndent` with a two-space
indent. Compare the returned message with `expect` as JSON values, not as text.

## Shelf

Each track opens a fresh shelf with the receive vectors' profile, bounds and retention
(`../receive/README.md`, "The vector profile", and `bounds` and `retention_seconds` of
`../receive/script.json`), and sets the shelf's clock to a case's `at` before the case.

## Cases

| `op` | What to do |
|---|---|
| `call` | send `request` to the entry and compare what it returns with `expect` |
| `forget`, `reopen` | as the receive script's steps (`deal`, `settled`), so the store reaches the state the next push meets |

`carriage` is `mcp` or `a2a`. On `a2a`, `a2a_version` is the `A2A-Version` value (`""` when the
request carries none) and `a2a_extensions` the `A2A-Extensions` URIs; a case with neither member
carries none. A server version is the host's, so the `_meta` of an MCP result (its server info) is
removed before comparing. An A2A task's `id`, `contextId` and `artifactId` are random: replace each
with `"<id>"` before comparing (each must be 32 lowercase hex digits).

## What the tracks hold

Each track starts with the receive script's `receive` steps, in order, as pushes (the record's
bytes, and the stamp proof's JSON as Go's `encoding/json` writes it, each in base64url without
padding), so each answer is that step's `expect` object (`record` and `proof`, the latter as
`stamp_proof`). Then the edge cases, at `2026-10-21T00:00:00Z`: a record the profile cannot read
(refused `invalid`, empty `digest`); a stamp proof that is not one (refused `invalid`); each part
answered on its own; a push with no member, an extra member, padding, a character outside
base64url, trailing bits set, one character left over, or a member that is not a string (invalid params, no answer); and, per carriage, an
unknown tool, arguments that are not an object, `tools/list`, `server/discover` and another
protocol version (MCP); the push in a `raw` or a `text` part, a data part of another media type, `acceptedOutputModes` empty or without `application/json`, an id above 2^53 (echoed as written) with a fraction in `metadata`,
a message that names a task, two parts, a message from the agent, the extension not activated,
no `A2A-Version` and `GetTask` (A2A).

Not here: the sender-side cases of RP-1 (an answer Artifact on a task that is not completed, a
completed task without one), which classify a reply a sender receives; and a receiver fault
(internal error, no answer) and the message bound, which need a failing store or a large message
and are tested in `interop_push_test.go`.
