# evidencebook#461 steps 4a and 4b: a shelf log of book facts, and the migration to it

Design note for [evidencebook#461](https://github.com/action-state-group/evidencebook/issues/461),
acceptance 1 to 4, 7 (the parts the library owns) and 8. Written 2026-11-16 CST, at main
`f078f40`. The issue body quotes the target design it implements; this note fixes what the
target leaves to "the design draft": the bytes, the tables, the calls and the order of work.
Acceptance 5, 6 and 9 belong to the deal profile version (step 5) and the row status function
(#466); acceptance 7's counting rule is `standard_history/3`, whose members the AAC item
defines, so this note builds the replay and the fact inputs a card is counted from and stops
there. The wire forms of the record push and the stamp proof on the MCP and A2A bindings are the
AAC item's too (the How ack's table, §14 rows 6 and 9); the library's `Receive` lands first and
the binding entries follow that item.

## 1. What exists today

- **The cadence** (`cadence.go:23-43`, `cadence_tree.go:12-23`). On time alone, every tick
  cuts a local checkpoint of every live log, puts each checkpoint as a salted leaf at a random
  position of a depth-16 tree, pads the tree with filler up to the number of logs the shelf has
  ever had (`cadence.go:551-558`), appends the root to the cadence log and checkpoints that log,
  which alone goes to the witness. A log is live while it has not ended (for a deal, a close), or
  while it has grown past the largest checkpoint of it a witnessed tick carries
  (`cadence.go:28-31`).
- **Recovery** (`cadence.go:486-490`, `575-584`): a tick is stored before its root is appended, and
  a stored tick not finished is finished first, as the same tick.
- **The file** (`store_sqlite.go:15-70`): `book_records`, `book_checkpoints`, `cadence_logs` (every
  log ever in the cadence, empty host logs included), `cadence_live`, `cadence_witnessed`,
  `cadence_ticks` (one `receipt` column per tick, the first that verified: `cadence.go:735-736`),
  `shelf_meta` (version 1) and `shelf_logs`. A version the build does not know is refused
  (`store_sqlite.go:116-118`). The shelf's handle and each CLL log's handle are separate
  connections, both WAL (`shelf.go:109-117`, `157`).
- **A checkpoint of a log that has not grown** is the previous one returned again
  (`substrate_cll.go:199-211`; Rust `shelf/log.rs:241-256`): a log must grow to get a new
  checkpoint.
- **Cards** are anchored at a witnessed tick, and their extents read `cadence_ticks`
  (`cadence_standard_history.go:41-119`); a card's coverage is checked only when the holder hands
  over the tree material, and reported as not in a checkable form without it
  (`standard_history_material.go:288-290`).
- **Received records** have no home in the library: the deal plugin keeps the bytes it receives
  in a store of its own.
- Rust (`rust/evidencebook/src/shelf/`) and Python (`python/evidencebook/shelf.py`,
  `cadence.py`) write the same file; the shelf interlock (`ci.yml`, `shelf-interlock`) has each
  continue the others' shelves.

## 2. The shelf log of facts

### 2.1 Entries

The shelf log is today's cadence log, under its id: no new log, so a card's `since` does not
reset. After the format-switch marker (section 4) every entry is the SHA-256 of one entry
document's canonical JSON (RFC 8785), a 32-byte id, as every CLL entry is; the document's bytes
are stored beside it. Entries before the marker are tree roots, still verified by today's code.
There are three kinds of entry document after the marker: a fact (2.2), a stamp entry (2.5) and,
once, the marker itself (section 4).

### 2.2 The fact

```
{"v": 1, "kind": "book_fact", "profile": "<profile>", "book_id": "<hex>", "log_id": "<id>",
 "size": <n>, "checkpoint_sha256": "<hex>", "prev_fact_size": <m>,
 "link_commitment": "<hex>", "status_commitment": "<hex>"}
```

- `book_id`: the record id of the book's first record (`record.go`, `RecordID`: its capsule id,
  already a SHA-256), lowercase hex. For a deal book that record is the agreement (step 5); a book
  opened before step 5 has a `book_id` all the same. A host log, whose entries are not records,
  takes the CLL entry id of its first entry. A log with no entry has no `book_id` and no fact yet.
  The vectors carry a first record and its expected `book_id`.
- `log_id`: the book's CLL log id, as today.
- `size`: the book's MMR size at the checkpoint the fact carries, the quantity today's leaf states
  (`cadence_tree.go:25-27`).
- `checkpoint_sha256`: the SHA-256 of that checkpoint's COSE bytes, as today's leaf.
- `prev_fact_size`: `size` of this book's previous fact; 0 for its first.
- `profile`: the profile whose `status` the commitment holds (`"deal"`, `"mesh_period"`, or
  `"none"` for a book no profile describes, whose status is `{}`).
- `link_commitment` and `status_commitment`: `hex(SHA-256(salt ‖ JCS(x)))`, one fresh 32-byte salt
  for each commitment of each fact, drawn in the order section 7 states. The opening is
  `{"salt": "<hex>", "value": x}`.
  - The link value is `{"last_received": {"log_id", "size", "record_digest"} | null,
    "checked_over": ["<hex>", ...]}`: the last record held from the other side, by position and
    digest, and the digests the recomputed checks ran over, sorted.
  - The status value is the profile's. The library takes it from the profile as JSON and commits
    to it; it does not interpret it. The deal profile's status (step 5) holds, for each row, the
    input of the row status function (#466), plus the deal members section 4.3 of the target
    lists: inputs, never a computed status, so a quiet book gets no new fact when a date passes.
    The row status function takes as given which rulings are the named adjudicator's and whether
    two conflicting final rulings are held; the deal profile derives both, from the agreement and
    the case books it holds, before it builds the status.

**Who gets which opening** (acceptance 2), and only these: the link opening to the counterparty,
the adjudicator the agreement names, and an asker the holder's disclosure pack lets see that deal;
the status opening to the counterparty and to readers the holder chooses, a card reader handed the
whole shelf among them. A journal fact's opening goes to the owner and an auditor it appoints, and
to nobody else. The library hands an opening only through a call that names the audience
(`Openings(book_id, audience)`, which returns the book's facts with the openings that audience
may have), `VerifyOpenings` checks that each opening given recomputes its commitment, and a test
checks, for each audience, exactly which openings and members it receives.

A **journal fact** has the same envelope with `"kind": "journal_fact"`, `profile` `"none"`,
`log_id` the journal's log id, `book_id` the SHA-256 of that log id, `size` 0, `checkpoint_sha256` `""` and `prev_fact_size` 0 in the clear, and
their true values inside its status commitment, whose value is `{"size", "checkpoint_sha256",
"prev_fact_size"}`. Its `link_commitment` commits to `{"last_received": null, "checked_over": []}`.
What it shows a whole-shelf reader, knowingly: that the journal changed at a stamp, so how busy
the owner's private bookkeeping is, never how long it is.

### 2.3 What the shelf stores beside the log

New tables, all in the same file:

- `shelf_entries (entry_seq PRIMARY KEY, kind, book_id, doc BLOB, link_opening BLOB,
  status_opening BLOB)`: every entry document after the marker and both openings of a fact.
- `shelf_stamps (stamp PRIMARY KEY, at, due_next, entry_from, entry_to, checkpoint_size,
  checkpoint BLOB, batch TEXT)`: one row per stamp; `checkpoint_size` 0 marks a stamp not
  finished; `batch` is the stored batch (2.5).
- `shelf_receipts (checkpoint_sha256, witness_id, receipt TEXT, registered_at, PRIMARY KEY
  (checkpoint_sha256, witness_id))`: every receipt that verified, one per witness, for any shelf
  checkpoint, with its registration time or null.
- `shelf_book_checkpoints (log_id, size, checkpoint BLOB, PRIMARY KEY (log_id, size))`: the COSE
  bytes of every book checkpoint a fact carries, so a handover can include them.
- `shelf_received`, `shelf_received_proofs` and `shelf_evidence`: the store (section 3).
- `shelf_migration (id = 1, state, backup_sha256, batch TEXT)`: the migration's progress (section 4).

`cadence_ticks` and the tree tables are kept and no longer written after the marker.

### 2.4 The write rule

At each stamp, for each book that has a first record, and for each host log in the cadence
(`cadence_logs`) that has an entry (profile `"none"`, link value
`{"last_received": null, "checked_over": []}`), the shelf computes the fact it would write now: the book's current size and checkpoint, and the openings' values as the profile gives them.
It writes a fact when the size grew, or when either opening's value differs from the one in the
book's last fact (the salts differ every time, so values are compared, never commitments). A book
whose values are unchanged gets no fact. A grown book's checkpoint is cut at the stamp. No book is
ever retired: a book that has ended (today, a close) stays a book, and gets a fact whenever its
values change. The journal follows the same write rule.

Consecutive facts of a book either have the same `size` and `checkpoint_sha256` (only received
material changed) or a larger size, joined by a consistency proof between their checkpoints,
which the book's log produces on demand (as `substrate_cll.go:369` does today) and the handover
carries. The journal's facts are bound by the same rule when the owner checks them, but no other
reader checks the journal's chain (2.7).

### 2.5 Stamps and recovery

A stamp appends, in this order: one fact for each book the write rule selects, in `book_id` order,
the journal's (if any) among them by its `book_id`; then one **stamp entry**,
`{"v": 1, "kind": "stamp", "stamp": <n>, "facts": <count>}`. The stamp entry makes the shelf log
grow at every stamp, so every stamp cuts a new shelf checkpoint with a new time, even one with no
fact, which a log that did not grow would not get (section 1). The shelf checkpoint is then cut and
queued for the witnesses, as today.

A stamp is a batch. Before appending, the shelf stores it in `shelf_stamps.batch`, a JSON object
`{"v": 1, "stamp": n, "at", "due_next", "entries": [{"kind", "book_id", "doc", "link_opening",
"status_opening"}, ...], "book_checkpoints": [{"log_id", "size", "checkpoint"}]}` (bytes as
base64url, no padding), with the stamp entry last in `entries`. Then it appends the entries in
that order, then cuts the shelf checkpoint and fills the row's `checkpoint_size` and
`checkpoint`. On open, and before any stamp, a stored batch not finished is completed: the shelf
compares the log's entries after the previous stamp's `entry_to` with the batch's, appends the
ones missing, and only then cuts the checkpoint. A checkpoint therefore never covers part of a
batch, any implementation can finish another's batch, and nothing is rewritten.

When a stamp is taken is the owner's: `Stamp(ctx, now)` on demand, which the deal profile calls
after a statement, a dispute or a remedy and before its window ends; the host's own schedule,
calling `Stamp`; or the heartbeat, `Tick`, which after the marker stamps instead of building a
tree, at the same interval. The random draws of a stamp are, in this order: each fact's link salt
then status salt, in append order; then the jitter of `due_next` (today's tick draws it too,
`cadence.go:560-567`). No padding is written (the target, section 4.3: none for now).
Times leaving the device stay coarsened to the minute (`cadence.go:498-499`).

`Witnessed(checkpoint, witness_id, receipt)` verifies a receipt for any shelf checkpoint and keeps
it in `shelf_receipts`, one per witness, a later receipt from another witness kept beside it.

### 2.6 Record time

`RecordTime(book_id, position, witnesses)` returns the earliest registration time, among the
receipts in `shelf_receipts` from the named witnesses, of a shelf checkpoint that covers the first
fact of that book whose size covers the position; or "no time". A receipt from a witness not
named, and a receipt with no registration time, give no time. The library's vectors cover those
two and the plain case. The other two cases of acceptance 3 (the other side's stamp covering our
record first, and a statement whose in-time state is known only at its first stamp) need the
counterparty's facts and the profile's status, so they are step 5's vectors, built on this call.

### 2.7 Handover and replay

`Handover(anchor)` gives a reader the holder chooses: every shelf log entry up to the anchor (the
entries before the marker as the opaque ids they are), every entry document after the marker,
each book fact's status opening, no journal opening and no link opening, the COSE bytes of every
book checkpoint a book fact carries, the consistency proof between each pair of consecutive book
facts whose size grew, each genesis fact's tie when the holder has one (below), the anchor
checkpoint, its receipts, and the coverage start
(section 4).

`VerifyHandover(material, witnesses)` fails when:

- the entries are not exactly as many as the anchor states, or do not recompute its commitment (an
  entry held back);
- an entry after the marker has no document, or a document does not hash to its entry (a fact left
  out);
- a status opening does not recompute its commitment (a changed opening);
- a book checkpoint does not hash to its fact's `checkpoint_sha256`, is not signed by the holder
  key, or names another log or size;
- two consecutive facts of one `book_id` break the chain a verifier can check without the profile:
  `prev_fact_size` equals the previous fact's `size`; `size` does not shrink; a fact with the same
  `size` has the same `checkpoint_sha256`; and a fact whose size grew has a consistency proof from
  the previous one that verifies (a replaced book). Whether a fact was due under the write rule
  needs the profile's inputs, so the verifier does not judge it;
- a genesis fact that comes with a tie fails to verify it. A **tie** is the old witnessed tick's
  leaf for that log (its log id, size, checkpoint digest and salt), its path to that tick's tree
  root, that root's entry in the cadence log under a cadence checkpoint with a receipt from a named
  witness, the old book checkpoint's COSE bytes, and the consistency proof from it to the genesis
  fact's checkpoint. A genesis fact with no tie is reported untied: nothing is claimed about the
  log's continuity before the coverage start, which is all the coverage start already says.

It does not check the journal's chain or ask for its openings, and the journal counts toward no
completeness and no tally. It returns each book's latest fact and opening, the coverage start, and
two separate answers: **recomputed** (everything above passed), and **witnessed** (the anchor has
a receipt from a named witness with a registration time). A shelf with no witness is a supported
default (u186): its signed shelf checkpoint is a valid anchor, the result is recomputed and
unwitnessed, and every row status computed from it is local. A **dropped book**, a `book_id` in the
handover whose latest fact a card does not count, is caught by the count: counting the card again
from the returned latest facts gives a different card, which fails. Counting is
`standard_history/3`'s, after the AAC item; until then the planted dropped-book case is a test
count over the returned facts.

## 3. Journal, store and receiving

- **The journal** is a book of kind `journal`, one per shelf, opened on first use, whose records
  are full records. The request and share logs of today keep their bare digests and are not moved.
- **The store** is `shelf_received (digest PRIMARY KEY, sender_key, log_id, position, deal_id,
  record BLOB, received_at)`, indexed by `(sender_key, log_id, position)` and `deal_id`;
  `shelf_received_proofs (sender_key, log_id, size, book_checkpoint BLOB, fact BLOB,
  inclusion_proof BLOB, shelf_checkpoint BLOB, received_at, PRIMARY KEY (sender_key, log_id,
  size))`, every stamp proof accepted, so a restart keeps what it covered and a later
  contradiction is found against it; and `shelf_evidence (id, kind, a BLOB, b BLOB, noted_at)` for
  a contradicting checkpoint or a fork.
  Nothing in the store is appended to a book.
- **`Receive(ctx, record?, proof?)`** takes a record, a stamp proof, or both, and handles each part
  on its own, so a record sent again with a proof it lacked the first time still delivers the
  proof:
  - the record: its signature is checked; one already held is acknowledged and not stored again;
    one whose position is past the sender's **counted prefix** (the longest run of positions from
    the first with no gap) is stored and not counted, and the result asks for the book; when a gap
    is filled, the stored records after it join the counted prefix up to the next gap;
  - two different records the sender signed at one position: both kept as a fork in
    `shelf_evidence`, and the counted prefix ends before that position for good;
  - the proof: its book checkpoint is checked against the records held (2.7's checks on one
    book), and its inclusion proof against its shelf checkpoint; a proof whose book checkpoint
    contradicts one already held for the same key and log at the same size is refused and kept as
    evidence; the same checkpoint again is no contradiction.
  - Each call returns what changed: the deal ids whose counted prefix grew, shrank or forked, and
    those whose records gained coverage or a registration time from an accepted proof or receipt,
    so the profile recomputes every row of each (acceptance 4). A restart recomputes from the
    store.
- **The `seen` field** is a record member the library fills, when asked, with the digest of the
  last record of the counted prefix held from the other side of that book.
- **The storage interface**: the shelf, the stamp and the card anchor take a `ShelfStore`
  interface; SQLite stays the default and the only implementation in this item.

## 4. Migration (4b)

`Migrate(ctx)` is an explicit call; opening a version 1 file without it reads as today and refuses
any write that the new format needs, naming `Migrate`. A read-only open never migrates. Migration
is one-way, and recoverable at every step:

1. **Finish the old cadence.** A tick stored but not finished is finished by today's code first,
   so no tree root is ever appended after the marker.
2. **Back up.** The shelf writes `VACUUM INTO '<path>.v1.bak.tmp'`, which is a consistent snapshot
   including what is still in the WAL, records the snapshot's SHA-256 in `shelf_migration`, and
   renames it to `<path>.v1.bak`. On a restart: a `.v1.bak` whose SHA-256 matches the recorded one
   is reused; a `.v1.bak.tmp` whose SHA-256 matches it is renamed; a `.v1.bak.tmp` with no
   recorded digest, or another one, is from an attempt that did not finish and is removed before
   `VACUUM INTO` runs again (it refuses an existing file); a `.v1.bak` that does not match is
   refused, and the shelf asks the owner to move it.
3. **Store the batch.** `shelf_migration.batch` holds the marker
   `{"v": 1, "kind": "format_switch", "format": "book_facts/1", "last_tick": <n>,
   "logs": [{"log_id", "size"}, ...]}`, listing every log in `cadence_logs`, sorted by log id
   (byte order of its UTF-8), with its current size
   (0 for a log with no entry), and a genesis fact for every log of that list with an entry: its
   current size and checkpoint, `prev_fact_size` 0, profile `"none"` for a host log in the cadence
   (the Mesh node's own log is not one: a host log that witnesses itself is outside the cadence,
   `rust/evidencebook/src/shelf/mod.rs:467-470`, and stays so until step 8), and for a log with a
   witnessed size (`cadence_witnessed`) the batch keeps the tie of 2.7 from the largest one. A log listed with size 0 gets its first fact when it first has an entry. In
   the same shelf transaction as the batch, `shelf_meta.version` becomes 2 and
   `shelf_migration.state` `migrating`, so from here an older build refuses the file and can
   never tick a tree root after the marker.
4. **Append and checkpoint.** The marker, then the genesis facts in `book_id` order, then a stamp
   entry, are appended through the cadence log's own CLL handle; then the shelf checkpoint is cut
   and queued for the shelf's witnesses, as every stamp's is. The shelf records this as stamp 1 in
   `shelf_stamps` (its entries, checkpoint, `at`, and `due_next` from the cadence's interval and
   jitter). This checkpoint is the **coverage start**.
5. **Finish.** `shelf_migration.state` becomes `done`. A new build that opens a file in state
   `migrating` repeats step 4 from the stored batch, appending only what is missing, as a stamp is
   finished (2.5), before anything else; a crash before step 3's transaction leaves a version 1
   file with nothing after its last tick, which an older build still opens as before.

The coverage start is that checkpoint, or the shelf log's first entry when the holder also hands
over the old ticks' material (their leaves, salts, positions and fillers) and it verifies, which
lets a reader check that every log of an old tree has a genesis fact. A card and a verification
result carry it, and claim no completeness before it, whatever `since` says. No card is anchored
before it.

**Old cards and old readers.** `standard_history/1` and `/2` stay served on a migrated shelf: their
anchor is a stamp's checkpoint after the coverage start, and their extents are each book's latest
fact at that stamp as a record count (`mmr.LeafCount(size)`, refusing a size that is not an MMR
size, as `extentsLocked` counts today, `cadence_standard_history.go:112-116`), in place of
`cadence_ticks`; their counting rules do not change. A test
adds a deal after migration and checks that a `/1` card counts it. Old cards stay readable as old
cards. A reader of the old format given a `/3` card reports an unknown format. An older pinned
build refuses a version 2 file; the upgrade note says a downgrade needs the backup.

## 5. Not in this item

- Counting `standard_history/3` (after the AAC item), the deal profile's status value and its ten
  record types (step 5), and the plugin changes (evidencebook-deal).
- The MCP and A2A entries for `Receive`, after the AAC item fixes the push and stamp-proof forms.
- Moving host logs that witness themselves (the Mesh plugin's) onto facts: the library keeps
  refusing to move a node's log into the stamp (`rust/evidencebook/src/shelf/mod.rs:467-470`)
  until step 8.

## 6. Order of work

One pull request per part, each landing alone, each mirrored in Rust and Python to the same
vectors before it merges, and each keeping the shelf interlock green:

1. The entry documents (fact, journal fact, stamp entry, marker), commitments, openings and
   `Openings` by audience, with vectors (`testdata/shelf/facts/`).
2. The stamp (write rule, batch, recovery, `Stamp`, the heartbeat switch, `Witnessed`), and the
   migration of section 4, together, since the version bump and the first facts land at once; `/1`
   and `/2` served from stamps.
3. `Handover` and `VerifyHandover`, with the planted failures.
4. The journal, the store and `Receive`.
5. `RecordTime`.

## 7. Conformance

- The shelf vectors gain the entry documents' bytes, the openings and the random draws in 2.5's
  order, and a first record with its `book_id`.
- The interlock's script gains stamps with a quiet book, a received-only change, an empty stamp
  (its stamp entry and a new checkpoint), a quiet stamp past a due date, and a crash after the
  batch is stored and after half its entries are appended, each finished by another language.
- A version 1 shelf written by today's code, with a tick stored but not finished and an empty
  host log, is committed under `testdata/shelf/`; each implementation migrates it to the same
  bytes, including after a crash at each step of section 4.
- `VerifyHandover` vectors: a passing handover, and each failure of 2.7, one change each.
- `Receive` vectors: a duplicate, a duplicate carrying a proof it lacked, a gap and its fill, a
  late fork, a contradicting proof, and the returned changes for each.
