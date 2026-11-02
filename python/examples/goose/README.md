# The Goose loop reference

A [Goose](https://github.com/block/goose) extension that runs the loop's deal steps on an
evidencebook shelf, in Python. Copy it to build an agent whose History cards a relying party can
check and staple to its counterparty's.

The node keeps everything its key answers for in one SQLite file, the shelf (design note
[#254](../../../docs/design/254-shelf.md) §3 and §8):

- **One book per deal** (`deal/<deal_id>`). Each step of the deal is an
  [x-deal-v0](../../../dealprofile/x-deal-v0/PROFILE.md) record, sealed as a Capsule that is the
  book's record. Each Capsule carries the deal id, so a relying party can join your half of the deal
  to your counterparty's on it (Evidence Bundle -02 `composed/v1`, `pre_agreed_identifier`).
- **The node book** (`goose/node`). This holds what the node did outside any deal: joining,
  admitting a peer, a counterparty's half it verified, and the evidence requests it sent and the
  answers.
- **Your existing ledger**, if you have one (`goose/ledger`). A capsule-emit ledger is copied once
  into the shelf. Each record is copied exactly as it was sealed, and nothing is re-signed.
- **The cadence** (`goose/cadence`). At each tick it commits every book's checkpoint in one
  checkpoint, which is the one a witness signs and the anchor of your `standard_history/1` card.

The node answers `history_card/1` for any one book, and `standard_history/1` (your deals, counted)
as of a tick.

## Run it

You need Python 3.10 or later, this repository's Python package, and `mcp`:

```bash
python -m venv .venv
.venv/bin/pip install ./python "mcp>=1.20"
```

Add the extension to `~/.config/goose/config.yaml`:

```yaml
extensions:
  goose_loop:
    enabled: true
    type: stdio
    name: goose_loop
    description: "Goose loop steps on an evidencebook shelf"
    cmd: /path/to/.venv/bin/python
    args: ["/path/to/python/examples/goose/loop_server.py"]
    timeout: 60
    envs:
      GOOSE_HOME: "~/.goose-evidence"
      GOOSE_OPERATOR: "acme-co"
```

On first use the node creates the following in `GOOSE_HOME`, each with mode 0600:

- `shelf.db`, the shelf;
- `producer.key`, the key that signs your records, which is your identity to peers;
- `checkpoint.key`, the key that signs your checkpoints;
- `store.secret`, the key under which counterparties are fingerprinted.

Back these files up. Optional settings:

- `GOOSE_DEVELOPER`: the developer name your records carry. The default is `goose-loop@v1`.
- `GOOSE_WITNESSES`: a `witnesses.json` file to check cadence receipts against.

## A deal, tool by tool

Every tool returns one JSON object. A refusal is `{"ok": false, "error": ...}` and seals nothing.
Halves and requests are files that you move to the other party over your own channel. Nothing
here moves money: a payment reference is a value you observed.

| Step | Buyer | Seller |
|---|---|---|
| 12 | `loop_join` | `loop_join` |
| 01 | `loop_seal` (a model call you made: provider, model id, prompt, completion) | |
| 04 | `loop_admit` (the seller's two keys from its `loop_join`, `GO`) | `loop_admit` (the buyer's two keys, `GO`) |
| 05 | `loop_deal_open` (item, price, rail, the user's words; its first step cites your GO), then `loop_deal_push` | |
| 04 | `loop_deal_bundle` (your admission and your record of the deal, one bundle) | |
| 05 | | `loop_deal_respond` (the buyer's half: offer, acceptance, commit), then `loop_deal_push` |
| 05 | `loop_deal_reconcile` (the seller's half: same terms?) | |
| 06 | `loop_settle` (the payment reference you saw on the rail) | |
| 05 | `loop_deal_end_exchange` (asks for the seller's half and writes yours), then `loop_deal_reconcile` on the half the seller sends | the same, for the buyer's half |
| 06 | `loop_deal_close` (`received` or `not_received`; cites the checkpoint of the half you hold) | `loop_deal_close` |
| 05 | each side runs `loop_deal_push` and the other `loop_deal_reconcile` on its final half | |
| 09 | `loop_dispute_ask`, then `loop_dispute_poll` until `received` (the referee's Result, over the mailbox) | the same |
| 02 | `loop_tick`, then `loop_book_bundle` (a record of a book, e.g. the one `loop_seal` returned) | `loop_tick` |
| cards | `loop_standard_history`, `loop_history_card` | the same |
| 03 | `loop_advertise` | `loop_advertise` |
| 07 | `loop_ask_mailbox`, then `loop_ask_mailbox_poll` | `loop_mailbox_poll` (the node answers by polling its mailbox) |

`loop_book_bundle` writes the record's Evidence Bundle (`go run ./test/interop/verify FILE` checks it); beside it go `checkpoint.cose` and, with `GOOSE_WITNESSES` (a directory of one witness), that witness's receipt for this very
checkpoint (`witness-receipt.json`, also in the bundle's `checkpoint.witnesses`) and `witnesses.json`; a witnessed cadence
tick adds `cadence-checkpoint.cose`, `cadence-witness-receipt.json` and the `x-cadence-witness/v0` extension.

`loop_seal` seals the model call as a `runtime_claimed` effect record of the node book and writes the Capsule
(with its `signature` and `key_id`) to a file; `go run ./test/interop/goose-capsule FILE` checks it.
`runtime_claimed` is the node's own claim that the call happened, not the provider attesting it. The prompt is stored in
your node book as the record's payload, so don't seal a prompt you would not disclose with the book; the completion is
kept by digest only.

`loop_advertise` is step 03. It writes the node's `evidence-commitment/1`, which says how the node can be asked and what it
can answer, and never counts anything. It also writes the node's disclosure policy. Both are signed with your signing key.
The files are `<node_id>.json` and `policies/<node_id>.json`, laid out as they go under `nodes/commitments/` in the loop
repository (evidence-network/loop, `steps/03-advertise.md`), where you add them by pull request. The tool publishes nothing.
`node_id` is your node's id in the loop's list; the default is your signing key. The commitment staples your node book's
`history_card/1`, built as `Book.Staple` builds it (over `checkpoints`, pinned to the newest checkpoint, with a nonce); the commitment carries the reply only, since `evidence-commitment/1` has no member for the request. It does not staple a card for each deal, since those would publish every deal id. The node book's card
does show the book's size, which grows with every record it holds, deal openings included. By default it says a person approves each answer (`human_gated`) and that an
asker waits `PT24H` before recording an absence. `endpoint`, `bindings`, `answers_within` and `human_gated` change that.
`loop_join` returns the same commitment and policy, and writes them when you give it `commitment_dir`. The loop's
`scripts/check_commitment.py` is the check: CI runs it on the scripted run's commitments, with the loop checked out at the
commit the workflow pins.

`loop_advertise` with `relays` (comma-separated `wss://` URLs, the relays the node reads) is what makes the node askable with no
listener: the commitment then names `ask.nostr {mailbox_key, relays}` (the node's own secp256k1 mailbox key, `mailbox.key`,
mode 0600), and `poll_interval` (seconds) is how often you promise to call `loop_mailbox_poll`. `answers_within` may not be
shorter than `poll_interval`; the tool refuses it otherwise.

Step 07 over the mailbox (binding `nostr-mailbox-v0`, experimental; the exchange is `evidence-interop/1`'s, carried unchanged):

- **Answering.** `loop_mailbox_poll` reads the evidence requests addressed to the node's mailbox key on its relays and answers
  each from the node book under the node key: `history_card/1` and `checkpoints` to anyone, and for anything else the node's
  disclosure policy decides, with a signed refusal (never silence) for a request outside it. An answered request is sealed in
  the node book (`goose_mailbox_answered`), so a restart does not answer it again. Call it at least as often as `poll_interval`.
- **Asking.** `loop_ask_mailbox` takes the asked node's commitment file, verifies its signature, seals the request in your node
  book, and publishes it to the mailbox the commitment names with a `deadline` of its `answers_within`. `loop_ask_mailbox_poll`
  then returns `answered` (the reply verified under the asked node's listed keys and recorded: an artifact or a signed
  refusal), `pending`, or `absent` once the deadline has passed with no recorded answer, which records the absence against the
  node. A reply that arrives after the absence is kept beside it (`goose_mailbox_late`, signature-checked, `late: true`) and
  never rewrites it (binding §5.3). Arrival is judged by your clock at the poll that first sees the reply, so poll at least
  as often as the asked node's `answers_within`.
  `loop_book_bundle` with `disclose` writes the outcome's Evidence Bundle with the headers of the outcome and of the
  request it cites revealed, and the reply bytes the response record keeps (`capsulectl verify --bundle` reads it).
- The events are not encrypted (binding §8, N-3). Use a relay that authenticates its readers.

The other tools are `loop_import` (your existing ledger), `loop_book` (a book's size and
checkpoint), `loop_probe` (re-check everything on the shelf), and `loop_ask` and `loop_ask_reply`
(an evidence request you send, and its verified answer).

Step 09 is the dispute: the referee the deal terms name seals a Result (capsule-engine's `referee`), and
that Result reaches each judged party's own book over the AAC Nostr mailbox
(`nostr-mailbox-v0`, experimental). This uses the binding's event kinds, addressing and reply checks to
carry a Result, which is not an evidence envelope, so it is not a conforming use of the binding
(§4.2); extending the binding for it is tracked in https://github.com/action-state-group/agent-action-capsule/issues/549. Goose has no inbound listener, so the party polls. The terms
name the referee's `mailbox_key`, its `relays`, `private_relays: true` and `answers_within` (seconds). `loop_dispute_ask`
publishes a request to that mailbox, with a deadline of `answers_within`; its `poll_interval`
may not be longer than `answers_within`. `loop_dispute_poll` fetches the replies once. It returns
`received`, `pending` or, once the deadline has passed with no reply, `absent`, which it seals
once as `goose_dispute_absent` (a reply that arrives later is still taken). Both tools take the
deal file `{"buyer": {"half", "action", "terms"}, "seller": {...}}`: each party's signed half and the
opening of its commitment.

A Result is accepted only if capsule-engine's check passes, after the node binds each opening to
its signed half. It refuses, and seals the refusal with its code as `goose_dispute_refused`, a Result
that does not verify, that is not signed by the referee both halves' terms name, that names other
halves, or about a deal this node's key sealed no half of. An accepted Result is sealed as
`goose_dispute_received` and **cites** the two judged halves, which the book holds as
`goose_dispute_half` records. The record holds the SHA-256 of the Result's bytes (`result_digest`),
not the bytes, and both parties' books hold the same digest. The referee's side of the exchange
(`nostr_mailbox.serve_round`) is the part of the referee node that answers a request with the Result.
The tools need `capsule-engine` installed beside the node: a release that has `capsule_engine.referee`
(0.3.0 does not), or until one ships the commit CI uses,
`pip install "capsule-engine @ git+https://github.com/action-state-group/capsule-engine@f88a6e63ddca79fb0d3070be93fda59a4242cdba#subdirectory=python"`,
and `pip install "websockets==17.4"` for the mailbox. The events are not encrypted (binding §8, N-3), and a Result names both halves and the
referee, so the terms must declare `private_relays: true`, relays that only the two parties and the referee can read, or the
node refuses to ask. Use a relay that authenticates its readers until the binding specifies encryption.

`scripted_run.py` runs that table, except step 09 (which needs a referee node; `tests/test_dispute.py` runs it), with two nodes, each a `loop_server.py` process spoken to
over MCP stdio, as Goose speaks to it:

```bash
.venv/bin/python python/examples/goose/scripted_run.py /tmp/goose-run
```

## What each side seals

The buyer's deal book holds the records capsulectl deal seals for a retail purchase:
the baseline, the check before paying, its verdict, the approval under your standing intent, the
`pay` act, the outcome and the close.

The seller's book holds the typed action records the profile requires when you sell (x-deal-v0
§9): the baseline, the task authority, the offer, the buyer's acceptance (its half, by digest),
the `commit` act, the outcome and the close.

Your own words (what you asked, the payment reference) appear in a record only as commitments.
Their openings stay in your shelf.

A deal counts on your card once it reaches `pay` or `commit`. It counts as `stamped_by_both` once
your node has verified the counterparty's final half (x-deal-v0 §11).

A counterparty's half is checked against the two keys your admission pinned. Its steps must be
signed by the peer's signing key, and its checkpoint, which commits exactly those steps, must be
signed by the peer's checkpoint key. Each step must also chain to the one before it, carry its
deal-step header, and commit its record. The records must pass the profile's record checks and
its chain rules, as checked by the reference checker `dealprofile/x-deal-v0/check_profile.py`. In
a copy of the example outside this repository, point `GOOSE_CHECK_PROFILE` at that file.

### Step 05: what each clause of the loop maps to

The loop's step 05 asserts that the halves cite each other's checkpoints, that the halves are
pushed at completion and the receiver holds the pushed bytes by record id, and that the receiver
seals a record citing the counterparty half and the checkpoint it came under. Goose has no
record-push; its own records answer each clause (evidencebook#303):

| Clause | Goose record |
|---|---|
| each half cites the other's checkpoint | the **close** step's record header carries, beside its chain parent, one `cites` link to the SHA-256 of the checkpoint statement of the counterparty's half this node holds (x-deal-v0 §1) |
| the halves are exchanged at completion | `loop_deal_end_exchange`, at the exchange's end and before the outcome and close: it seals this node's request for the counterparty's half (`goose_half_asked`) and, unless `share=false`, writes this node's own half |
| the receiver holds the pushed bytes by record id | `loop_deal_reconcile` seals `goose_half_received`: the half's bytes are the note's payload, and its statement names the half's final record id (`final_record_id`), the half's digest and the checkpoint digest |
| the receiver's record cites the counterparty half and its checkpoint | that note cites this node's request and states the checkpoint digest the close then cites |
| verified | `verify_half` on the half, which also checks the half's links: the chain parent exactly once on every step after the baseline, the baseline's one extra cite (its admission) never read as a checkpoint, checkpoints only on a close |

Each side's pre-close half is exchanged first because a close cannot cite a checkpoint that covers
the other side's close. `loop_deal_close` refuses when this node holds no counterparty half;
`uncited=true` closes anyway when the counterparty never answered. The final halves are still
exchanged and reconciled afterwards: a held half that ends in a close is the card's `stamped_by_both`.

A closed deal takes no more steps. If `loop_deal_respond` is cut short part-way, call it again
with the same half: it seals only the steps the deal does not hold yet.

## Bringing an existing ledger

If Goose has been sealing into a capsule-emit ledger, run `loop_import` with its path, once.
The node trusts the ledger's own signing key, copies every record in order exactly as it was
sealed, and checks that the shelf holds the same records in the same order. It holds the
ledger's writer lock throughout, and refuses while another writer (an older Goose server) holds
it, so stop that server first. Only after the check does it move
the old files to `GOOSE_HOME/archive/pre-shelf/`. If anything does not match, the old files stay
where they are and the import refuses. Running it again after an interruption resumes it.

The old ledger's signing key is not moved, and it stays the key those records verify under. New
records are signed with `producer.key`.

## Checking a card

A relying party that holds your signing key re-derives your card from a copy of your shelf with
Go's evidencebook:

```bash
go run ./test/interop/goose-card /tmp/goose-run/buyer/shelf.db /tmp/goose-run/buyer-card.json "$(cat /tmp/goose-run/buyer.key)"
```

A Mesh node on capsules-plugin v0.1.8 has no channel to receive a Goose half yet
(evidencebook#291 adds one). CI instead runs each half through exactly the checks such a node runs
over a counterparty's half, with the crates that release pins (`test/interop/goose-mesh-check`).
To run it on a scripted run's halves:

```bash
cargo build --locked --manifest-path test/interop/goose-mesh-check/Cargo.toml
test/interop/goose-mesh-check/target/debug/goose-mesh-check /tmp/goose-run/buyer-final-half.json /tmp/goose-run/seller-final-half.json
```

The card check reads a consistent snapshot of the shelf, so the node may keep running. It accepts the card
only if Go derives exactly the same bytes as of the tick the card names, and every
`standard_history/1` property passes. CI runs the scripted run and this check
(`python/examples/goose/tests`).
