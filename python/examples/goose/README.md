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
| 04 | `loop_admit` (the seller's two keys from its `loop_join`, `GO`) | `loop_admit` (the buyer's two keys, `GO`) |
| 05 | `loop_deal_open` (item, price, rail, the user's words), then `loop_deal_push` | |
| 05 | | `loop_deal_respond` (the buyer's half: offer, acceptance, commit), then `loop_deal_push` |
| 05 | `loop_deal_reconcile` (the seller's half: same terms?) | |
| 06 | `loop_settle` (the payment reference you saw on the rail) | |
| 06 | `loop_deal_close` (`received` or `not_received`) | `loop_deal_close` |
| 05 | each side runs `loop_deal_push` and the other `loop_deal_reconcile` on its final half | |
| 02 | `loop_tick` | `loop_tick` |
| cards | `loop_standard_history`, `loop_history_card` | the same |

The other tools are `loop_import` (your existing ledger), `loop_book` (a book's size and
checkpoint), `loop_probe` (re-check everything on the shelf), and `loop_ask` and `loop_ask_reply`
(an evidence request you send, and its verified answer).

`scripted_run.py` runs that whole table with two nodes, each a `loop_server.py` process spoken to
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

It reads a consistent snapshot of the shelf, so the node may keep running. It accepts the card
only if Go derives exactly the same bytes as of the tick the card names, and every
`standard_history/1` property passes. CI runs the scripted run and this check
(`python/examples/goose/tests`).
