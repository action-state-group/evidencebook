# evidencebook#461 steps 4a and 4b: a shelf log of book facts, and the migration to it

Design note for [evidencebook#461](https://github.com/action-state-group/evidencebook/issues/461),
acceptance 1 to 4, 7 (the parts the library owns) and 8. Written 2026-11-16 CST, at main
`f078f40`. The issue body quotes the target design it implements; this note fixes what the
target leaves to "the design draft": the bytes, the tables, the calls and the order of work.
Acceptance 5, 6 and 9 belong to the deal profile version (step 5) and the row status function
(#466); acceptance 7's counting rule is `standard_history/3`, whose members the AAC item
defines, so this note builds the replay and the fact inputs a card is counted from and stops
there.

## 1. What exists today

- **The cadence** (`cadence.go:23-43`, `cadence_tree.go:12-23`). On time alone, every tick
  cuts a local checkpoint of every live log, puts each checkpoint as a salted leaf at a random
  position of a depth-16 tree, pads the tree with filler up to the number of logs the shelf has
  ever had (`cadence.go:551-558`), appends the root to the cadence log and checkpoints that log,
  which alone goes to the witness. A log stops being live once it has ended (for a deal, a close)
  and every witnessed tick carries its last size (`cadence.go:28-31`).
- **Recovery** (`cadence.go:575-584`): a tick is stored before its root is appended, so a crash
  resumes the same tick.
- **The file** (`store_sqlite.go:15-70`): `book_records`, `book_checkpoints`, `cadence_logs` (every
  log ever in the cadence), `cadence_live`, `cadence_witnessed`, `cadence_ticks`, `shelf_meta`
  (version 1) and `shelf_logs`. A version the build does not know is refused
  (`store_sqlite.go:116-118`).
- **Cards** are anchored at a witnessed tick (`cadence_standard_history.go:25-41`); the coverage of
  an anchor that commits its logs through a tree is reported as not checked
  (`standard_history_material.go:288-290`).
- **Received records** have no home in the library: the deal plugin keeps the bytes it receives
  in a store of its own.
- Rust (`rust/evidencebook/src/shelf/`) and Python (`python/evidencebook/shelf.py`,
  `cadence.py`) write the same file; the shelf interlock (`ci.yml`, `shelf-interlock`) has each
  continue the others' shelves.

## 2. The shelf log of facts

### 2.1 Entries

The shelf log is today's cadence log, under its id: no new log, so a card's `since` does not
reset. After one format-switch marker (section 4) every entry is the SHA-256 of one fact's
canonical JSON (RFC 8785), a 32-byte id, as every CLL entry is; the fact's bytes are stored
beside it. Before the marker, entries are tree roots, still verified by today's code.

### 2.2 The fact

```
{"v": 1, "kind": "book_fact", "profile": "<profile>", "book_id": "<hex>", "log_id": "<id>",
 "size": <n>, "checkpoint_sha256": "<hex>", "prev_fact_size": <m>,
 "link_commitment": "<hex>", "status_commitment": "<hex>"}
```

- `book_id`: the SHA-256 of the book's first record's canonical bytes, lowercase hex. For a deal
  book that record is the agreement (step 5). A book opened before step 5, whose first record is
  not an agreement, has a `book_id` all the same: the rule is the hash of the first record, not
  "of the agreement".
- `log_id`: the book's CLL log id, as today.
- `size`: the book's MMR size at the checkpoint the fact carries, the same quantity today's leaf
  states (`cadence_tree.go:25-27`).
- `checkpoint_sha256`: the SHA-256 of that checkpoint's COSE bytes, as today's leaf.
- `prev_fact_size`: `size` of this book's previous fact; 0 for its first.
- `profile`: the profile whose `status` the commitment holds (`"deal"`, `"mesh_period"`, or
  `"none"` for a book no profile describes, whose status is `{}`).
- `link_commitment` and `status_commitment`: `hex(SHA-256(salt ‖ JCS(x)))`, one fresh 32-byte salt
  for each commitment of each fact, from the shelf's random source in the order section 7
  states. The opening is `{"salt": "<hex>", "value": x}`.
  - The link value is `{"last_received": {"log_id", "size", "record_digest"} | null,
    "checked_over": ["<hex>", ...]}`: the last record held from the other side, by position and
    digest, and the digests of the records the recomputed checks ran over, sorted.
  - The status value is the profile's. The library takes it from the profile as JSON and
    commits to it; it does not interpret it. The deal profile's status (step 5) is the input of
    the row status function (#466) for each row, plus the deal members section 4.3 of the target
    lists. The row status function takes as given which rulings are the named
    adjudicator's and whether two conflicting final rulings are held; the deal profile derives
    both, from the agreement and the case books it holds, before it builds the status.

A **journal fact** has the same envelope with `"kind": "journal_fact"`, `book_id` the SHA-256 of
the journal's log id, `size`, `checkpoint_sha256` and `prev_fact_size` set to 0, `""` and 0 in the
clear, and their true values inside its status commitment, whose value is
`{"size", "checkpoint_sha256", "prev_fact_size"}`. Its `link_commitment` commits to
`{"last_received": null, "checked_over": []}`.

### 2.3 What the shelf stores beside the log

New tables, all in the same file:

- `shelf_facts (entry_seq PRIMARY KEY, book_id, kind, fact BLOB, link_opening BLOB,
  status_opening BLOB)`: every fact's bytes and both openings, by its position in the shelf log.
- `shelf_stamps (stamp PRIMARY KEY, at, entry_from, entry_to, checkpoint_size, checkpoint BLOB,
  batch BLOB)`: one row per stamp. `batch` is the stored batch (2.5); `checkpoint_size` 0 marks a
  stamp not finished.
- `shelf_received (digest PRIMARY KEY, sender_key, log_id, position, deal_id, record BLOB,
  received_at)` with indexes on `(sender_key, log_id, position)` and `deal_id`: the store's inbox.
- `shelf_evidence (id PRIMARY KEY, kind, a BLOB, b BLOB, noted_at)`: a contradicting checkpoint or a
  fork, kept (section 5).

`cadence_ticks` and the tree tables stay and are no longer written.

### 2.4 The write rule

At each stamp, for each book on the shelf, the shelf computes the fact it would write now: the
book's current size and checkpoint, and the openings' values as the profile gives them. It writes
a fact when the size grew, or when either opening's value differs from the one in the book's last
fact (the salts differ every time, so values are compared, never commitments). A book whose
values are unchanged gets no fact. A grown book's checkpoint is cut at the stamp; an unchanged
one is not cut again. The journal is one of the books. Every stamp then checkpoints the shelf
log, even when it wrote no fact, and queues that checkpoint for the witnesses, as today.

Consecutive facts of a book either have the same `size` and `checkpoint_sha256` (only received
material changed) or a larger size; the consistency proof between their checkpoints is not
stored, because the book's log produces it on demand (as `substrate_cll.go:369` does today), and the handover
(2.7) carries it.

### 2.5 Stamps and recovery

`Stamp(ctx, now)` runs one stamp at once. A stamp is a batch: the shelf first stores the books,
their target sizes and checkpoints, and the facts' bytes and openings (`shelf_stamps.batch`), then
appends the facts, then cuts the shelf checkpoint. On open, and before any stamp, a stored batch
not finished is completed: its missing facts are appended, found by their entry ids after the
previous stamp's last entry, and only then is the checkpoint cut. A checkpoint therefore never
covers part of a batch, and nothing is rewritten.

When a stamp is taken is the owner's: `Stamp` on demand, which the deal profile calls after a
statement, a dispute or a remedy and before its window ends; the host's own schedule, calling
`Stamp`; or the heartbeat, `Tick`, which after the marker stamps instead of building a tree, at
the same interval and jitter. No padding is written (the target, section 4.3: none for now).
Times leaving the device stay coarsened to the minute (`cadence.go:498-499`).

### 2.6 Record time

`RecordTime(book_id, position)` returns the registration time of the earliest witnessed shelf
checkpoint that covers the first fact of that book whose size covers the position, from the
receipts of the witnesses the caller names (the deal profile names the agreement's), or "no
time". The other side's stamps that cover our record are the deal profile's to read, from the
link commitments it holds openings for (step 5). A receipt that passes with no registration time
gives no time.

### 2.7 Handover and replay

`Handover(anchor)` gives a reader the holder chooses: every shelf log entry from the coverage
start to the anchor; each book fact's bytes and its status opening; each journal fact's bytes and
no opening; for each pair of consecutive facts of a book with a larger size, the consistency
proof between their checkpoints; and the anchor checkpoint with its witness receipts.

`VerifyHandover(material, witnesses)` checks that the entries are exactly as many as the anchor
states and recompute its commitment; that each fact hashes to its entry; that each status opening
recomputes its commitment; that each book's consecutive facts satisfy 2.4's rule, with a
consistency proof that verifies when the size grew; that no `book_id` appears before the coverage
start in material that claims to begin there; and it returns each book's latest fact and opening,
the coverage start, and the level reached: Recomputed when everything passed and the anchor has a
witnessed time, else the lower level with its reason. A dropped book is one whose latest fact the
caller does not count; a replaced book breaks its chain and fails. Counting the card from those
latest facts is `standard_history/3`'s, after the AAC item.

## 3. Journal, store and the receive entry

- **The journal** is a book of kind `journal`, one per shelf, opened on first use, whose records
  are full records. The request and share logs of today keep their bare digests and are not
  moved.
- **The store** is `shelf_received`. `Receive(ctx, record, stampProof?)`:
  - checks the sender's signature, and that the record follows the last one held from that sender
    and log;
  - a record already held: acknowledged, not stored again;
  - a record that arrives before the one it follows: stored, not counted, and the result asks for
    the book (an evidence request the caller sends);
  - two different records the sender signed at one position: both kept as a fork in
    `shelf_evidence`; the sender's records from that position on are no longer counted;
  - a stamp proof whose book checkpoint contradicts one already held for the same key and log at
    the same size: refused and kept as evidence; the same checkpoint again is no contradiction.
- **The MCP and A2A bindings** (`interop_mcp.go`, `interop_a2a.go`) gain one entry each that
  carries a record, its payload and openings, and optionally a stamp proof, and calls `Receive`.
- **The `seen` field** is a record member the library sets from `shelf_received`: the digest of the
  latest record held from the other side of that book, when the caller asks for it.
- **The storage interface**: the shelf, the stamp and the card anchor take a `ShelfStore`
  interface; SQLite stays the default and the only implementation in this item.

## 4. Migration (4b)

On open, a version 1 file is migrated to version 2 in one transaction, after the shelf writes a
copy of the file beside it (`<path>.v1.bak`, refusing to overwrite one that exists):

1. Create the tables of 2.3. Set `shelf_meta.version` to 2.
2. Append one marker entry to the cadence log: the SHA-256 of
   `JCS({"v": 1, "kind": "format_switch", "format": "book_facts/1", "last_tick": <n>})`, with its
   bytes in `shelf_facts` as a fact of kind `format_switch`.
3. At the first stamp after the marker, write a genesis fact for every log in `cadence_logs`,
   live or not: its current size and checkpoint, `prev_fact_size` 0, with its largest witnessed
   size (from `cadence_witnessed`) in the handover material and the consistency proof from it, so
   a reader can tie the genesis fact to what a witness already saw.

The **coverage start** is the marker's checkpoint, or the shelf log's first entry when the holder
also hands over the old ticks' material (their leaves, salts, positions and fillers) and it
verifies, which lets a reader check that every log in an old tree has a genesis fact. A card and
a verification result carry it, and claim no completeness before it, whatever `since` says.

Old cards stay readable as old cards. New cards are anchored only after the marker. A reader of
the old format given a new card reports an unknown format. An older pinned build refuses a version
2 file; the upgrade note says a downgrade needs the backup.

## 5. Not in this item

- Counting `standard_history/3` (after the AAC item), the deal profile's status value and its
  ten record types (step 5), and the plugin changes (evidencebook-deal).
- Moving host logs that witness themselves (the Mesh plugin's) onto facts: the library keeps
  refusing to move a node's log into the stamp (`rust/evidencebook/src/shelf/mod.rs:467-470`) until
  step 8.

## 6. Order of work

One pull request per part, each landing alone, each mirrored in Rust and Python to the same
vectors before it merges, and each keeping the shelf interlock green:

1. The fact envelope, commitments and openings, with vectors (`testdata/shelf/facts/`).
2. The stamp: the write rule, the batch and recovery, `Stamp`, the heartbeat switch, and the
   migration of section 4, together, since the version bump and the first fact land at once.
3. Handover and `VerifyHandover`, with the planted failures (an entry held back, a dropped book,
   a replaced book, a changed opening).
4. The journal, the store, `Receive` and the two binding entries.
5. `RecordTime`.

## 7. Conformance

- The shelf vectors gain the facts' bytes, the openings and the random draws in this order per
  stamp: for each book that gets a fact, in `book_id` order, the link salt then the status salt.
- The interlock's script gains stamps with a quiet book, a received-only change, an empty stamp,
  and a crash after the batch is stored and after half its facts are appended.
- A version 1 shelf written by today's code, committed under `testdata/shelf/`, is migrated by
  each implementation to the same bytes.
