# evidencebook#254: one shelf in Go, Rust and Python, and History cards that staple

Status: draft for review. Design note only; no code changes here. Written 2026-10-31. Code
references are to this repository at `132832c`, Mesh-LLM/capsules-plugin at `cef06f9` (and the
held branch `ledger-step6-chain-store` at `cf7fe7d`), capsule-emit at `e233c51`,
checkpointed-local-log (CLL) at `5de5897`, agent-action-capsule (AAC) at `5d6f5376`,
evidencebook-deal at `bef5951` and evidence-network/loop at `5907a93`, unless stated.

## Purpose

Steven (Buzz, Ethan-Steven status thread, message 44b522b4): the point is "less about the store":
a History card is built from AAC records and the books on a shelf, and "we want developers to
create interoperable/stapleable history cards for their own ecosystems/agents and this is the
reference (mesh-llm + goose)". Ethan (coordinator chat, 2026-10-31): one shelf, the same in Go,
Rust and Python, with the Mesh capsules plugin on it, "必须统一，不留尾巴" (unified, no loose ends).

So this note defines:

1. one shelf contract that any language implements and that a shared corpus checks (§2, §5);
2. how History cards come from a shelf's books, and how cards from two ecosystems staple
   together for one relying party (§3);
3. the Rust and Python ports (§6), the Mesh plugin's move onto the Rust shelf (§7), and Goose's
   move onto the Python shelf (§8), which together are the reference a developer copies;
4. the one-way doors (§9) and the work items (§10).

## 1. What exists today

### 1.1 The Go shelf

One profile's books in one SQLite file (`shelf.go:17-46`). Each book is its own CLL log in that
file, keyed by its book id (`OpenCLLSQLite`, `substrate_cll.go:62-94`). Records and checkpoints
are rows of `book_records` and `book_checkpoints` (`store_sqlite.go:14-60`). One artifact store
holds every book's originals (`ShelfConfig.Originals`, `shelf.go:36-40`). An exclusive advisory
lock on a lock file makes the shelf the profile's only writer (`shelf_lock_unix.go`). A heartbeat
cadence checkpoints every live log on time alone and commits those checkpoints as salted leaves
of a fixed-depth tree whose root alone reaches the witness (`cadence.go:25-35`,
`cadence_tree.go`). A log from before the shelf joins the cadence as a `LegacyLog`, never
converted (`cadence.go:256-345`), and a pre-shelf cadence is carried in once by
`Cadence.ImportLegacy` (`cadence_import.go:33-60`). A shelf's `standard_history/1` card is
anchored at a cadence tick (`cadence_standard_history.go:16-37`).

Its one consumer is evidencebook-deal (`capsulectl deal`): `internal/deal/deal_shelf.go:76`
opens the shelf on capsule-cli's `deal.db`, `:124` runs the cadence, `:229-236` imports the
pre-shelf cadence. The migration marker and the mutation counter that catch a released
capsulectl writing a migrated store are the deal plugin's own tables (`deal_shelf.go:24-50`),
not the shelf's.

### 1.2 Rust and Python

- Rust (`rust/evidencebook` 0.0.11): `Book` is a read-and-seal trait (`ask.rs:61`), the record
  store traits are read-only (`store.rs:51,95`), and `CllSubstrate` is a file node store plus a
  `checkpoints.jsonl` (`substrate.rs:441-452`). No concrete book, SQLite, lock, cadence,
  originals or legacy import; the only concrete book is the interop test helper `MemBook`
  (`rust/interop/src/lib.rs:147`). The `deal` feature encodes x-deal-v0 records.
- Python (`python/`, unpublished): `Book` over a `MemoryStore` or a JSONL `FileStore`
  (`book.py:320-395`, `store.py:33-133`) with an in-memory CLL substrate (`_cll.py:1-13`), an
  in-process lock only (`book.py:350`), payloads inline instead of an artifact store
  (`store.py:24-31`), and no deal profile.

### 1.3 The layers below are already the same in every language

- **CLL SQLite.** Go (`go/store/sqlite/store.go:21-47`), Rust (`log/sqlite.rs:27-52`) and Python
  (`log/sqlite.py:41-66`) create the same four tables (`cll_meta`, `cll_entries`, `cll_nodes`,
  `cll_witnesses`), and CLL's `storage-interlock.yml` has every language write each format and
  every other language open, continue and verify it. Each language also has a checkpoint runner
  over that backend. Latest releases: Go `go/v0.3.0`, Rust 0.4.0, Python 0.8.0. This repository
  pins Go v0.1.0, Rust 0.3.1 and Python `>=0.7,<0.8` (`go.mod:8`, `rust/evidencebook/Cargo.toml:22`,
  `python/pyproject.toml:27`).
- **The artifact store.** capsule-emit's store has JSONL, SQLite and MySQL backends in Go, Python
  and Rust (`go/artifact/sqlite/schema.sql`, `python/capsule_emit/artifact/sqlite.py`,
  `capsule-emit-artifact` 0.0.1 feature `sqlite`), and `artifact-interlock.yml` cross-checks every
  language's store. Its tables are prefixed `capsule_store_`, so they can share a file with the
  shelf's and CLL's.

So the shelf adds only its own tables and behaviour on top of two layers that are already
interlocked. Nothing below the shelf is reimplemented.

### 1.4 The Mesh plugin and Goose

- The Mesh plugin keeps one flat capsule-emit ledger per node (`<data dir>/ledger/capsules.jsonl`
  and siblings) and serves `history_card/1`, `inference-service-history/0`, `ledger-fetch/1` and
  `/ledger-checkpoint` from it. Its `commitment/1` document staples the node's `history_card/1`
  answer (`crates/evidence-plugin/src/commitment.rs:24,300-348`). Ledger plan step 6
  (capsules-plugin#206) moves that chain onto the CLL entry log plus the artifact store; its branch
  is held (§7.3).
- Goose runs the loop through capsule-emit's Python flat ledger, not through capsulectl deal and
  not through evidencebook: `examples/goose-capsule/loop_server.py:70-75` imports `read_ledger`,
  `seal`, `capsule_emit.bilateral` and `capsule_emit.ledger_layout`.

## 2. The shelf contract

The contract is stated once, here, and is what every implementation passes (§5). Where it says
"as Go does" it means the behaviour at `132832c`; any change to it changes all three.

### 2.1 One shelf per subject key

A shelf is the books of one **subject key**: every record on it is sealed by one signer, and every
checkpoint of it is signed by one checkpoint key (`ShelfConfig.Signer`, `CheckpointKey`). That is
the AAC History frame's subject (HF §4.1: every envelope of one History names the same
`responder`). A host with two subject keys keeps two shelves. This is not a new rule: it is why a
Mesh node and a Goose agent on one machine have two shelves, and why one key never serves both
`inference-service-history/0` and `standard_history/1` (HF §4.8).

A profile maps to a shelf by its host: capsulectl's deal profile names `deal.db`
(`deal_shelf.go:76`), the Mesh plugin its data dir (§7), Goose its store path (§8). The shelf
does not define profiles.

### 2.2 The file

One SQLite file holds:

| Tables | Owner | Contents |
|---|---|---|
| `cll_meta`, `cll_entries`, `cll_nodes`, `cll_witnesses` | CLL | one CLL log per book id, plus the cadence log and any legacy log |
| `book_records`, `book_checkpoints` | the shelf | each book's records (`book_id`, `seq`, `record_id`, `header`, `capsule`, `envelope`) and checkpoints |
| `cadence_logs`, `cadence_live`, `cadence_ticks`, `cadence_witnessed` | the shelf | the cadence (§2.6) |
| host tables (for example `deal_shelf`, `shelf_mutations`) | the host | anything the host keeps beside the shelf |

The shelf's tables are exactly `store_sqlite.go:14-60`, column for column. The file uses WAL, a
busy timeout of 5000 ms and `synchronous=FULL` (`shelf.go:109-113`). The cadence's extents query
uses SQLite's JSON1 functions (`cadence_standard_history.go:98-100`), so an implementation's
SQLite must have them (rusqlite's bundled build and CPython's `sqlite3` both do).

The shelf's schema gets a version row, `shelf_meta (id INTEGER PRIMARY KEY CHECK (id = 1),
version INTEGER NOT NULL)`, written as 1 when the shelf creates its tables. An implementation
refuses to open a file whose version it does not know. Today's Go shelf writes no version; a Go
shelf opening a file without the row writes 1, since every such file has today's layout.

### 2.3 Books

A book is one log id on the shelf. `Open(log_id)` returns the same book for the shelf's lifetime
(`shelf.go:123-157`): a book allocates positions from its own order, so two handles on one log
could give two records one position. A book's store has FileStore's semantics: a record is stored
once (the same record again is a no-op, another one under its id is `corrupt`), records list in
seq order, checkpoints in the order kept, every write one transaction (`store_sqlite.go:62-108`).
A record's row and its log entry are two writes, not one transaction, and the book's open
reconciles a crash between them (`shelf.go:83-92`); a record above the log's entry count is
pending, not committed (`shelf_records.go:31-37`).

The record itself (header, capsule, envelope, payload and field commitments) is the book's, which
the three implementations already share and test byte for byte (`testdata/derivation`,
`testdata/evidence-request`). The shelf adds nothing to it.

`Records(book_ids...)` lists committed records across books without opening any
(`shelf_records.go:39-140`). `Books()` lists the ids that hold records.

### 2.4 The writer lock

An exclusive advisory lock (`flock(LOCK_EX|LOCK_NB)`, retried every 50 ms until the caller's
deadline) on a lock file, `<file>.lock` unless the host names its own, so a host's other writers
and the shelf exclude each other (`shelf.go:24-28`, `shelf_lock_unix.go`). Go, Rust (`libc::flock`
or `fs2`/`fs4`) and Python (`fcntl.flock`) take the same lock on the same file, so a Go and a Rust
writer of one shelf exclude each other. Off unix the shelf does not open (`shelf.go:73-76`), in
every language.

### 2.5 Originals

A book's originals (payloads, openings, caller-sealed capsules) go to one artifact store the host
passes, shared by every book (`shelf.go:36-40`). The store is capsule-emit's, in any of its
interlocked backends; the shelf never reads its files itself. The Go deal plugin passes the JSONL
originals file; a new shelf may pass the SQLite backend on the shelf's own file. Either way the
store, not the shelf, owns the format.

### 2.6 The cadence

As `cadence.go`, `cadence_tree.go` and `cadence_import.go` define it, unchanged:

- a tick is due on time alone (interval at least 1 m, jitter under half of it, defaults 5 m and
  1 m); its time is floored to the minute (`cadence.go:36-93,382-430`);
- each live log is checkpointed locally and committed as a salted leaf
  `SHA-256(0x00 ‖ JCS({checkpoint_sha256, log_id, salt, size}))` at a random position of a sparse
  tree of depth 16, with filler 0 `SHA-256(0x03 ‖ nonce)`, derived fillers up to the lifetime log
  count, and empty subtrees from `SHA-256(0x02)` (`cadence_tree.go:20-200`);
- the root is appended to the cadence log, the log is padded to its bucket with padding records,
  and only the cadence log's checkpoint is queued for the witness (`cadence.go:486-551`);
- a tick is stored before its root is appended, and a stored, unfinished tick is finished as the
  same tick (`cadence.go:466-484`);
- a log leaves the live set once it has ended (the host's `Ended` predicate) and a witnessed tick
  carries its largest checkpoint; any append puts it back (`cadence.go:572-662`,
  `store_sqlite.go:96-120`);
- receipts are verified with AAC's witness verifier, never the shelf's own (`cadence.go:143-173`).

The cadence is optional: a shelf with one book that its witness sees directly (§3.2) runs none.
To make that possible the contract adds one thing Go lacks: **a directly witnessed log** (a book
or a host log, §2.7), whose own checkpoints are queued for the shelf's witnesses in the same commit, as the cadence log's are
today (`openCLLSQLite`'s `witnessIDs`, `substrate_cll.go:69-94`). A shelf refuses to put a directly
witnessed log in its cadence, which would hand the witness that book's checkpoints and so
defeat the tree.

### 2.7 Host logs, legacy logs and import

Not every log on a shelf holds book records. Go already has one such kind: `Shelf.Legacy(log_id)`
opens a log already in the file that is not a book; it joins the cadence, and every write to it
goes through `LegacyLog.Append`, which runs the host's own write under the shelf's lock
(`cadence.go:256-331`). capsulectl deal keeps every pre-shelf deal log this way.

The contract generalizes it as a **host log**: a CLL log on the shelf whose records the host
seals itself, in its own record format, never converted into book records. The shelf gives the
host, under the shelf's lock, the log's CLL backend on the shelf's file and the shelf's artifact
store (namespace = the log id), so the host's own log layer (for the Mesh plugin, capsule-emit's
`Ledger::open_stores_with_indexer` and `CheckpointState::load_log`) runs on the shelf's storage. A
host log is either in the cadence (a legacy log, as Go's) or directly witnessed (§2.6). A book id
and a host log id never coincide (`Legacy` refuses a book, `cadence.go:283-290`).

`Cadence.ImportLegacy` carries a pre-shelf cadence's ticks and logs in once, re-runnably
(`cadence_import.go:33-60`). Importing a host's pre-shelf files into a host log is the host's
(its records, its journal), on the shelf's stores; §7.2 is the Mesh case.

## 3. History cards from a shelf

### 3.1 What exists

| Card | Where defined | Anchor | Streams | Served by |
|---|---|---|---|---|
| `history_card/1` | ER -02 §8.1 (`history-card`) | a checkpoint of one stream | one | Go `interop_artifact.go`, Rust `answer.rs`, Python `book.py`, the Mesh plugin |
| `standard_history/1` (SH) | `spec/standard-history-v0.md` | for a shelf, a cadence tick (x-deal-v0 §11, `cadence_standard_history.go`) | `one` (HF §6.1) | Go (`DeriveStandardHistory`), capsulectl deal |
| `personal_history_basic/1`, `personal_history_outcome/1` (PH, PO) | `spec/personal-agent-history-*` | a witnessed checkpoint tick when deals span streams (PH §2) | `witnessed_tick` | Go, Rust, Python (personal-history interop, `ci.yml:453-461`) |
| `inference-service-history/0` (ISH, the former `service_history/1`) | `spec/inference-service-history-profile-v0.md` | a checkpoint of the node's stream, at a period boundary | `one` | the Mesh plugin |

`history_card/1` stays the staple (AAC#452 "Not in scope"): each History either embeds it
byte-identical (ISH) or carries the anchor's receipt beside it (SH).

### 3.2 One subject key, many books: the shelf is the witnessed tick

PH §2 already says how one History spans several streams of one key: its anchor "MUST be a
witnessed checkpoint tick: one checkpoint of the holder's, countersigned by a witness, that
commits to the checkpoint of each of those streams at that tick", and x-deal-v0 §11 anchors a
deal profile's SH card the same way. The shelf's cadence tick is that object: the cadence
checkpoint commits each book's checkpoint as a leaf, and a book's leaf, salt and path prove which
checkpoint of that book the tick carries (`cadence_chain.go:20-35`). So on a shelf:

- each book answers `history_card/1` for its own stream;
- a History over several books answers at a cadence tick, each book bounded by the largest of its
  checkpoints any tick up to the anchor carries (`Extents`, `cadence_standard_history.go:26-37`);
- a log that is the key's only stream (the Mesh node's host log, §7) answers at its own checkpoint, as ISH
  requires (`streams: one`, `answer_anchor: period_boundary`), and is witnessed directly (§2.6).

This is the "books on a shelf" half of Steven's statement, and it already has a spec.

### 3.3 Two ecosystems: stapling Mesh and Goose cards

A Goose agent buys inference from a Mesh node. The relying party wants the Goose agent's deal
History and the Mesh node's service History together. Under the frame they are two subjects
(HF §4.1), and HF §4.8 forbids serving them under one key. HF §4.7: "They compose only by an
explicit, evidence-backed link between their keys, never by being shown together". The link AAC
defines is the controller binding record (`spec/controller-binding-v0.md`), whose `controller_binding`
join basis Evidence Bundle -02 adopts for `composed/v1` (controller-binding-v0 §8).

What a reference staple is, then, with what exists:

1. the Goose agent's card (`standard_history/1` from its Python deal shelf, §8), with its anchor's
   receipt beside it;
2. the Mesh node's card (`inference-service-history/0` from its Rust shelf, with its embedded
   `history_card/1`);
3. a controller binding record signed by both keys, which is the only thing that lets a verifier
   show the two together;
4. the deal itself joins them at record level: the Goose deal's step and the Mesh exchange record
   cite each other by digest (each party's exchange half, ER "Exchange half").

Each card is asked for and answered by its own evidence request, as ER requires (one request, one
subject, one artifact). What exists does **not** define a single request or a single answer that
carries both cards: ER -02 has no multi-subject request, and HF has no composed History. If
"stapled for one evidence-request" means one request and one answer, that is new spec (a
composed History derivation, or a `composed/v1`-style container of Histories joined by a
controller binding), and is a door for Steven (§9, D4). This note builds everything up to it, so
the reference works with two requests today and with one request once that spec exists, without
a storage change.

### 3.4 What the reference shows a developer

A developer copying the reference gets, in their own language:

- a shelf (§2) holding their agent's books under one key;
- `history_card/1` per book, and their ecosystem's History instance (a counting table and a
  wording pack under the frame, HF §5) over the books at a tick;
- the controller binding to staple their key's cards to a counterparty's.

Mesh shows the one-stream, directly witnessed case in Rust; Goose shows the many-books, cadence
case in Python; capsulectl deal shows the same many-books case in Go.

## 4. What does not change

No record, capsule, envelope, checkpoint, receipt, card or wire byte changes: the shelf is
storage under the book, and the cards are derived as their specs say. `ledger-fetch/1`,
`evidence-request/1`, `record-push/1`, `commitment/1` and `/ledger-checkpoint` keep their shapes.
The cadence tree and its vectors (`testdata/cadence`, from capsule-cli `7a6b101`) do not change.

## 5. Conformance

Two kinds, following the pattern the layers below already use.

1. **Byte vectors** (`testdata/shelf/`, written by Go, read by Rust and Python, regenerated only
   with an environment flag and otherwise compared byte for byte, as
   `interop_derivation_vectors_test.go:274-287` does): cadence leaves, fillers, derived fillers,
   tree roots and paths, padding record ids from fixed nonces, a tick's stored `leaves` JSON, the
   `Records` listing of a fixed shelf, and `Extents` and `StandardHistoryAnchor` of a fixed
   cadence. `testdata/cadence` moves under it and gains Rust and Python readers. Randomness (salts,
   positions, nonces, jitter) comes from one injected source in every implementation, so a vector
   run is deterministic; production uses the system CSPRNG.
2. **A storage interlock** (`test/interop/shelf/`, a CI job in `ci.yml` beside the personal-history
   jobs, and in `rust.yml`'s path filter): each language writes one scripted shelf (two books, a
   legacy log, three ticks with a receipt, a crash left pending), and each other language opens it,
   continues it (appends, ticks, a reopen) and checks that records, checkpoints, ticks, `Records`,
   `Extents` and every checkpoint root are identical; the lock test has a Go and a Rust writer of
   one file exclude each other. It is CLL's `storage-interlock.yml` one layer up.

## 6. The ports

### 6.1 Go first: the contract's two additions

The directly witnessed log (§2.6), the host log (§2.7) and the `shelf_meta` version row (§2.2), with tests, and the
byte vectors and interlock harness (§5). Go moves to CLL `go/v0.3.0` in the same item if the
interlock needs it; CLL's own interlock shows the SQLite files are the same across its releases.

### 6.2 Rust

In `rust/evidencebook`, behind a `shelf` feature (rusqlite through CLL's `sqlite` feature,
capsule-emit-artifact's `sqlite` feature):

- a concrete book that writes: a record store with `put_record`/`put_checkpoint` over
  `book_records`/`book_checkpoints`, and a substrate over CLL's SQLite `Backend` and
  `CheckpointRunner` (CLL 0.4.0), replacing nothing the read traits do today;
- `Shelf` (open, lock, `open`, `books`, `records`, `legacy`, `release`), `Cadence` (tick, finish,
  witnessed, live, import_legacy, chain, standard_history_anchor) as §2 states them;
- originals through `capsule-emit-artifact`.

The crate's `Book` trait and `RecordSource` stay; the shelf's book implements them, so every
answer path (`respond`, `answer`, the derivations) works on a shelf book unchanged. This is what
evidencebook#191 (Rust core at Go parity) needs for Mesh.

### 6.3 Python

In `python/evidencebook`: a `SqliteStore` beside `MemoryStore` and `FileStore`, a substrate over
CLL 0.8.0's `cll.log.sqlite` and checkpoint runner, `Shelf`, `Cadence` and the lock (`fcntl`),
originals through `capsule_emit.artifact`, and the x-deal-v0 profile Goose needs (§8). This
overrides evidencebook#191's "native Python takes no new verbs" for the shelf (Ethan, 2026-10-31:
"python 也要"); #191 is amended to say so.

## 7. The Mesh plugin on the Rust shelf

Line numbers in this section are at capsules-plugin `cef06f9`, `crates/evidence-plugin/src/`.

### 7.1 The node chain stays one stream: a directly witnessed host log

Every record kind the plugin seals goes on one chain whose parent is the node's head: exchange
halves, stage records, owner maintenance, adjudications, admission, asks, settlement, deal steps
and reports, padding (`capsule_emit.rs:675-730,975-2160`, `deal_records.rs:205-232`). Peers see
positions over that whole chain: `/ledger-checkpoint` range proofs (`ledger_checkpoints.rs:421-428,
509-519`), `record-push/1`'s `leaf_index` (`record_push_bridge.rs:55-60`), `evidence-request/1`
records and `evidence_stream == log_id` (`evidence_artifact.rs:757`), and the `commitment/1`
checkpoint (`commitment.rs:23-26`). `inference-service-history/0` counts over that one stream
(`evidence_artifact.rs:286-300`; ISH `streams: one`).

So the chain is not split into books. It becomes **one host log on the node's shelf**, with the
same `log_id` (`<data dir>/log_id`, `owner_maintenance.rs:107-116`), the same capsules, the same
positions and the same checkpoints, directly witnessed as today. Splitting deals into their own
books would change every position a peer holds and the stream ISH counts; this note does not
propose it (§9, D5).

On the shelf:

- the file is `<data dir>/ledger/shelf.db`; the chain's CLL log and its records (capsule-emit's
  artifact store, SQLite backend, namespace = `log_id`) live in it;
- the shelf's lock file is the plugin's own `<real ledger>/.capsules-writer.lock`, which the
  shelf takes in place of the plugin's `flock` on it (`data_dir.rs:312-361`), as capsulectl deal
  passes its `.deal.lock`; the plugin's other guards (`capsules.lock`, the `/proc` scan) stay;
- the plugin opens capsule-emit's `Ledger::open_stores_with_indexer` (`ledger.rs:735`) on the
  host log's entry store and the shelf's artifact store, and `CheckpointState::load_log`
  (`checkpoint.rs:662`) on the host log's backend. Every other call site stays (§7.3);
- the plugin's own files (`received-*.jsonl`, adjudications, `deal-links.jsonl`, file checkpoints,
  `sequence_counters.json` and the rest) stay where they are. Moving them onto the shelf is not
  needed for any card and is not proposed.

### 7.2 Importing a running node

The import is #206's, retargeted: under the shelf's lock, journalled in
`ledger/import-pending.json`, each flat line `put` as stored and `append`ed in order, the flat
checkpoint state and receipts seeded into the CLL state (`commit_cll`, witness rows), entry count,
tip and root checked, and only then the chain's flat files moved to `archive/pre-store/`; a
mismatch keeps the flat files and refuses to start (capsules-plugin#206 "Import of an existing
node"). Only the destination changes: the shelf's SQLite file instead of `ledger/log.jsonl` and
`ledger/artifacts.jsonl`. No released build ever wrote the JSONL store layout (#206 never merged),
so no node is migrated twice.

#206's three conditions carry over unchanged and are the import's acceptance: the same evidence
for a peer (nothing re-signed, same `log_id`, key and MMR; every old checkpoint's root equals the
new log's root at that size), a running node keeps its history (import, killed-and-resumed
import, refusing mismatch), and peers on the current release keep working (`two_node_e2e` against
the current release).

### 7.3 What happens to #206's branch

Reuse it, rebased onto the shelf. Of `cf7fe7d`'s 36 files:

- kept as written: the #208 approver fix (`producer/capsule.rs:1381-1406,1474-1478`, the four
  `emit_owner_maintenance` calls, the Class 1 test), the import journal pattern and its phases,
  `capsules chain-lines` and its output format, `pre-store` outside `next_archive_slot`, and the
  readers moved off the flat files;
- rewritten: `ChainStores` (JSONL paths and the in-process `OPEN` map) becomes the shelf's host
  log handle; `is_store_layout` and the `data_dir` markers key on `ledger/shelf.db`; `copy_flat`
  writes to the shelf's stores.

Option A for unmovable nodes (capsules-plugin#208, decided 2026-10-31) carries over unchanged: a
node whose history holds an `approver: "owner"` record, which the artifact store refuses under
Class 1 (`capsule-emit-artifact` `record.rs:255-257`), stays on the flat ledger until its owner
starts a new history, which opens on the shelf. New owner-maintenance records use `human`/`policy`.
The flat path is removed once no supported release can create such a node; Ethan owns when.

### 7.4 One CLL version in the plugin's graph

The plugin builds on capsule-emit 0.0.9 and evidencebook 0.0.11, both on CLL 0.3.1. The Rust shelf
uses CLL 0.4.0 (its checkpoint runner, and the small-order-key refusal 0.3.1 lacks). capsule-emit's
`Ledger` takes CLL's store traits, so capsule-emit, capsule-emit-artifact and evidencebook must
move to CLL 0.4.0 together, released before the plugin item starts.

### 7.5 loop

loop reads `<data dir>/ledger/capsules.jsonl` directly in steps 04 (`04-admit:740`), 05
(`05-deal:924,1081`), 06 (`06-settle:633-638`) and 07 (`07-ask:868,957,1082-1084,1199`), and
`steps/ledger_proof.py` re-derives the MMR from those lines; 08 checks the copies those steps keep
(`08-verify:453`). After the import that file is archived. As #206 planned, the loop PR in the same
batch has those steps read `capsules chain-lines` output, which is today's line format, so every
copy already published keeps verifying under 08 unchanged. Step 09 reads no chain file at
`5907a93`. The loop-292 lane edits `steps/08-verify` and `08-verify.md` only; this touches neither.

### 7.6 Timing

No plugin storage change lands before Devcon S08 (2026-11-03), so the S08 image's plugin is not
destabilized. The evidencebook items (§10, E1 to E3) do not touch the plugin and can land before.

## 8. Goose on the Python shelf

Goose's loop server keeps its records in capsule-emit's Python flat ledger
(`examples/goose-capsule/loop_server.py:70-75,105-118`) and seals its deal halves with
`capsule_emit.bilateral` (`seal_action`, `seal_request`), not x-deal-v0. A `standard_history/1`
card counts x-deal-v0 deals, one per deal log, at a cadence tick (x-deal-v0 §11), so for Goose to
be the many-books half of the reference it needs:

- the Python shelf (§6.3) and x-deal-v0 in Python (records, fingerprints, the §11 deal-entry
  mapping), at byte parity with Go's `dealprofile` (its fixtures, `dealprofile/testdata`);
- the loop server sealing each deal's steps into that deal's book on the shelf and running the
  cadence, and serving `history_card/1` per book and `standard_history/1` at a tick;
- its existing flat ledger kept as a host log on the shelf (§2.7), imported as the Mesh chain is,
  so nothing it sealed is lost or re-signed.

Whether Goose's deal halves become x-deal-v0 records changes what a Goose counterparty receives;
it is a what for Sherlock2 (§9, D3). capsulectl deal is already the same many-books case in Go
(§1.1); the Python reference is what a Python developer copies.

## 9. One-way doors

| # | Door | Whose | Why |
|---|---|---|---|
| D1 | The shelf's file as a cross-language contract: the table set and columns of §2.2, the `shelf_meta` version row, the host log namespaces | engineering's, with Sherlock2's door confirmation | a storage format with no product meaning; every byte a peer sees is unchanged (§4) |
| D2 | The Mesh node import into `ledger/shelf.db` and the move of the flat chain files to `archive/pre-store/` | engineering's under #206's three conditions | Sherlock2 agreed #206's door is engineering's under those conditions (Buzz 341813f1); only the destination changes, so it is re-confirmed, not re-decided |
| D3 | Goose's deal halves become x-deal-v0 deal records on per-deal books | Sherlock2's what | what a Goose counterparty receives changes |
| D4 | One evidence request (or one answer) that carries Histories of two subject keys, stapled by a controller binding | Steven: public spec direction (ER, HF) | ER -02 and HF define no such request or composed History; this note builds the two-request staple and needs no storage change for either answer |
| D5 | Splitting the Mesh node's deals into their own books | Steven, if ever proposed | changes the positions peers hold and the stream ISH counts; not proposed here |
| D6 | Native Python gains the shelf's verbs, amending evidencebook#191 | Ethan's how (decided 2026-10-31); Steven told after | #191's "no new verbs in native Python" was Ethan's rescope of Steven's Rust-core ruling |

## 10. Work items

Each is filed separately on project 1; none is implemented in this note's lane.

| # | Repository | Item | Depends on |
|---|---|---|---|
| E1 | evidencebook | Go: the shelf contract's additions (directly witnessed log, host log, `shelf_meta`), `testdata/shelf` byte vectors with an injected random source, the storage interlock harness, CLL `go/v0.3.0` | this note |
| E2 | evidencebook | Rust: the `shelf` feature (writing book, SQLite store, `Shelf`, `Cadence`, host log, lock, originals) on CLL 0.4.0, passing E1's vectors and interlock | E1, C1 |
| E3 | evidencebook | Python: `SqliteStore`, `Shelf`, `Cadence`, host log, lock, originals on CLL 0.8.0, and x-deal-v0 in Python at parity with `dealprofile`, passing E1's vectors and interlock; amend #191 (D6) | E1 |
| E4 | evidencebook | Guide: "History cards that staple" (the §3 reference: shelf, per-book `history_card/1`, an instance at a tick, the controller-binding staple of a Mesh and a Goose card), with a worked example CI runs | E2, E3 |
| C1 | capsule-emit | Rust: capsule-emit and capsule-emit-artifact on CLL 0.4.0, released | none |
| P1 | Mesh-LLM/capsules-plugin | the node chain onto the Rust shelf: #206's branch rebased (§7.3), the import retargeted (§7.2), option A kept; after S08 | E2, C1, Devcon S08 |
| L1 | evidence-network/loop | steps 04, 05, 06, 07 and `ledger_proof.py` read `capsules chain-lines`; merged in one batch with P1 | P1 |
| G1 | capsule-emit | Goose's loop server on the Python shelf: per-deal x-deal-v0 books, the cadence, `history_card/1` and `standard_history/1`, its flat ledger imported as a host log | E3, D3 |
| A1 | agent-action-capsule | only if Steven rules for D4: a composed History carried by one request | D4 ruling |
