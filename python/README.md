# capsule-evidencebook

An evidence book over a Checkpointed Local Log, and the evidence request
interaction (`draft-mih-agent-evidence-request-00`, profiles
`evidence-interop/0` and `evidence-interop/1`) on both sides of it, in Python. Import it as
`evidencebook`.

It implements the same semantics as the Go module in this repository: for
the same calls it seals the same records, issues the same checkpoints and
answers with the same reply octets.

## Asking, receiving and responding

```python
import evidencebook as eb

book = eb.Book("my-book", "my-operator", signer=eb.Ed25519Signer(seed),
               log_id="my-log", log_key=eb.Ed25519Signer(log_seed),
               store=eb.FileStore("book"))

# As a requester: record that you asked, before sending.
sent = book.request(eb.EvidenceRequest(eb.Subject("record", digest),
                                       eb.Coverage(expected_pin=anchor)), "responder-1")
octets = sent.octets  # transmit these

# Then record what came back, verified under the keys you pinned for the
# responder, or, once your waiting window closed with nothing, the absence.
book.record_response(sent.record_id, eb.Message(reply_octets), eb.ResponderKeys(signer_key, log_key_id))
book.record_absence(sent.record_id, window_end)

# As a responder: answer a request. authenticated_requester is the identity
# your transport authenticated, or "" for a caller it did not.
reply, answered = book.respond(eb.Message(request_octets, eb.MEDIA_TYPE_JSON),
                               eb.RespondOptions(authenticated_requester=peer))
```

The records it seals:

| Record type | Sealed by | Statement |
|---|---|---|
| `evidence_request` | `Book.request`, before sending | the request as sent, and the responder asked |
| `evidence_response` | `Book.record_response` | the outcome (`artifact`, `refusal`, or a verified reply that failed a check), with the reply's octets as its payload |
| `recorded_absence` | `Book.record_absence` | the profile's absence record (§6.4) |
| `evidence_request_answered` | `Book.respond`, for an authenticated requester | what was answered, naming no requester; never served |

A reply not signed under the pinned responder key for the request is
refused and not recorded. Malformed and unauthenticated requests are
answered but never grow the log.

## The shelf

A shelf is one subject key's books in one SQLite file (evidencebook#254 §2),
the same file Go's `OpenSQLiteShelf` writes: Go and Python each open,
continue and verify the other's shelf.

```python
import sqlite3
from capsule_emit.artifact.sqlite import SqliteArtifactStore

db = sqlite3.connect("profile.db", isolation_level=None, check_same_thread=False)
db.execute("PRAGMA foreign_keys=ON")
originals = SqliteArtifactStore(db, "my-namespace", [signer.public_key()])
originals.init()

with eb.Shelf.open_sqlite("profile.db", operator="my-operator", signer=signer,
                          checkpoint_key=log_key, originals=originals) as shelf:
    book = shelf.open("deal/1")            # one CLL log per book
    book.append(eb.Entry("observation", eb.OBSERVED_EVENT, payloads=(b"...",)))
    cadence = shelf.cadence(eb.CadenceConfig(log_id="deal-cadence/0123456789abcdef",
                                             ended=is_closed, witness_ids=["w"],
                                             witnesses=directory))
    result = cadence.tick(now)             # due on time alone
    cadence.witnessed(result.tick.n, receipt)
```

- Each book is its own Checkpointed Local Log in the file (`cll.log.sqlite`),
  checkpointed by CLL's checkpoint runner; its records and checkpoints are the
  shelf's `book_records` and `book_checkpoints`, and its payloads go to the
  one artifact store (`capsule_emit.artifact`). A book's open appends a record
  stored but not yet in its log (a crash between the two writes).
- The shelf takes an exclusive `flock` on `<file>.lock` (unix only), or the
  host passes a lock it already holds (`held_lock`).
- `Shelf.host_log` opens a log whose records the host seals itself, in the
  cadence or outside it; `Shelf.legacy` opens a pre-shelf log in the cadence.
- `Cadence` cuts ticks, verifies witness receipts with AAC's verifier, and
  gives a log checkpoint's witnessed chain (`chain`) and a tick's
  `standard_history/1` anchor (`standard_history_anchor`).
  `import_legacy` carries a pre-shelf cadence in.

## What it reuses

The request map, the reply envelope, the signed refusal, RFC 8785 and
capsule ids come from `agent-action-capsule`. The MMR, its proofs, the
checkpoint claims map, the SQLite log and the checkpoint runner come from
`checkpointed-local-log`, and a shelf's artifact store from `capsule-emit`.
The package adds
the book, its record capsules, the responder's decision steps (as Go's
`evidencerequest.Decide` orders them) and the canonical Artifact.

## Scope

It answers over the native binding (JSON or CBOR octets in, octets out);
carrying them over A2A or MCP is the caller's. It keeps the two
authenticated indexes whose roots a checkpoint commits, but serves no
proofs over them, builds no Evidence Bundles and seals no field
commitments (it reads and keeps those a Go book sealed).

## Development

```bash
python -m venv .venv && .venv/bin/pip install -e ".[test]"
.venv/bin/python -m pytest -q
```

The interoperability programs in `../test/interop/python-evidencebook/`
reproduce the Go runs byte for byte; CI runs them (jobs `python` and, for
the shelf, `shelf-interlock`).
