# The Goose loop reference

A [Goose](https://github.com/block/goose) extension that runs the loop's deal steps on a
[capsulectl](../../../capsulectl/README.md) deal profile. Copy it to build an agent whose History
cards a relying party can check and staple to its counterparty's.

The extension is a thin MCP layer. Each tool is one or two verbs of the `capsulectl-deal` plugin, run
on the node's own profile (`GOOSE_HOME` is the profile's `HOME`, so two nodes on one machine never share
a store or a key). Python never opens the store, and this example imports none of the `evidencebook`
Python package: the evidence store is Go's, and this is a client of it.

The node's profile holds:

- **a deal** (`deal/<log id>`): an [x-deal-v1](../../../dealprofile/x-deal-v1/PROFILE.md) deal. The
  offer, the admission, the user's intent, the agreement, the payment legs, the delivery and the
  statements are the profile's records. What the other side needs from them is the file each verb
  writes; you carry it over your own channel and the other side keeps it with `deal receive`.
- **the node book** (`goose/node`): what the node did outside any deal: joining, its decision on a peer,
  a deal's context, the model calls it sealed, and the evidence requests it sent and their answers
  (`deal node ...`).
- **the cadence**: `deal tick` commits every log's checkpoint in one checkpoint, the one a witness sees
  and the anchor of the node's cards.

The node answers `history_card/2` (its cadence checkpoints, no deal counts) and `standard_history/3`
(its x-deal-v1 deals, counted) as of its newest checkpoint.

## Run it

You need Python 3.10 or later, `mcp`, `cryptography`, and two programs on `PATH` (or named by
variables):

- `capsulectl-deal`, the plugin `capsulectl plugin install` installs from a
  [capsulectl release](../../../capsulectl/README.md) (`GOOSE_CAPSULECTL_DEAL` names another binary);
- `capsule-engine`, [capsule-engine](https://github.com/action-state-group/capsule-engine)'s command (go/v0.6.0
  or later): a deal's admission decision is its `admission evaluate` (`GOOSE_CAPSULE_ENGINE`).

```bash
python -m venv .venv
.venv/bin/pip install cryptography "mcp>=1.20" "websockets==17.4"
```

Add the extension to `~/.config/goose/config.yaml`:

```yaml
extensions:
  goose_loop:
    enabled: true
    type: stdio
    name: goose_loop
    description: "Goose loop steps on a capsulectl deal profile"
    cmd: /path/to/.venv/bin/python
    args: ["/path/to/python/examples/goose/loop_server.py"]
    timeout: 60
    envs:
      GOOSE_HOME: "~/.goose-evidence"
      GOOSE_OPERATOR: "acme-co"
```

On first use the node runs `deal init --dir $GOOSE_HOME/deal` under the profile `GOOSE_OPERATOR` (the AAC
operator of everything it seals) and declares itself a person's agent for the History card. That creates, with
mode 0600, the key that signs your records (`signing.seed`, your identity to peers) and the key that signs your
checkpoints (`checkpoint.seed`), and the store. Back them up. Optional settings:

- `GOOSE_DEVELOPER`: the developer name your model-call records carry. The default is `goose-loop@v1`.
- `GOOSE_NO_WITNESS=1`: make the profile with no public witness (the default witness is
  `witness.agentactioncapsule.org`: ticks then reach the network). Set it before the first use.
- `GOOSE_SIGNING_KEY_FILE`, `GOOSE_CHECKPOINT_KEY_FILE`: keep an existing node's keys (PKCS#8 PEM or a hex
  seed) so its key ids stay. The history of an earlier Python home (a `shelf.db`) is **not** carried into the
  profile: leave the old home as a read-only archive.

## A deal, tool by tool

Every tool returns one JSON object. A refusal is `{"ok": false, "error": ...}`. A tool that refuses before its verb runs seals nothing; a tool that is cut short part-way (an agreement written to a file that cannot be written, say) is called again with the same arguments and finishes: the decision, the intent and the settled payment are found, not sealed twice.
The deal's files are what you move to the other party over your own channel. Nothing here moves money: a
payment reference is a value you observed.

| Step | Buyer | Seller |
|---|---|---|
| 12 | `loop_join` | `loop_join` |
| 01 | `loop_seal` (a model call you made: provider, model id, prompt, completion) | |
| 04 | `loop_admit` (the seller's key from its `loop_join`, `GO`) | `loop_admit` (the buyer's key, `GO`) |
| 05 | `loop_deal_open` (item, price, rail, the user's words): the offer file | |
| 04 | `loop_deal_bundle` (your decision on the peer and your record of the deal, one bundle, both disclosed: the deal's terms and peer, never your words) | |
| 05 | | `loop_deal_respond` (keeps the offer, runs the admission, agrees): the agreement file |
| 05 | `loop_deal_reconcile` (keeps the seller's agreement), then `loop_deal_agree`: the agreement file | `loop_deal_reconcile` (keeps the buyer's agreement) |
| 06 | `loop_settle` (the payment reference you saw on the rail). A pause returns the card to show the user; `loop_deal_approve` seals their answer, then `loop_settle` again with the `check_id`: the payer leg | `loop_deal_reconcile` (keeps it), then `loop_deal_received` (what you received): the payee leg |
| 06 | `loop_deal_reconcile` (keeps the payee leg), then `loop_settlement_disclosure` (your signed count of settlements on the rail) | `loop_settlement_disclosure` |
| 06 | | `loop_deal_deliver` (a commit review that pauses the same way, then the act): the delivery file |
| 06 | `loop_deal_reconcile` (keeps the delivery), `loop_deal_close` (`received` or `not_received`: the statement) | `loop_deal_close` |
| 06 | `loop_deal_reconcile` (keeps the seller's statement), then `loop_deal_report` (waits for the tick that covers the deal) | the same for the buyer's |
| 09 | not built on x-deal-v1 yet: [#830](https://github.com/action-state-group/evidencebook/issues/830) | |
| 02 | `loop_tick`, then `loop_book_bundle` (a record of a node book, e.g. the one `loop_seal` returned) | `loop_tick` |
| cards | `loop_standard_history`, `loop_history_card` | the same |
| 03 | `loop_advertise` | `loop_advertise` |
| 07 | `loop_ask_mailbox`, then `loop_ask_mailbox_poll` | `loop_mailbox_poll` (the node answers by polling its mailbox) |

The final review of an act (`deal check`) pauses when the user must answer: a price the agent picked, a rail
with no card protection. `loop_settle` and `loop_deal_deliver` then return `paused: true` and the card in `say`.
Show it as it is, seal only what the user answers (`loop_deal_approve`), and call the tool again with the
`check_id`. A leg or an act the tool seals returns `person_signature: missing` until the person has signed it: say "settled, as the agent's records show", never that the user signed. The scripted run plays the user: its answers are the script's, and its records say what the script said, not what a person consented to.

`loop_settlement_disclosure` is step 06's count. It writes the node's signed `settlement-disclosure/v1`
(`type`, `node`, `key_id`, `rail`, `through_sequence`, `settlements`, `sig`), which the loop compares with the rail's own
count of final settlements you are a party to. `settlements` is the number of deals on `rail` (the rail the deal was opened
on) for which your node sealed its own payment leg as settled: the payer leg (`loop_settle`) or the payee leg
(`loop_deal_received`). It is your node's records, not a ledger view: the node reads no rail and tests no finality, so
it counts what its records say was settled. A Goose deal has one payment row and no refund, so one deal is one
settlement. The count is for the asked rail only, and the disclosure carries no amount, counterparty or payment
reference: each payment reference is on the leg each side signed. `through_sequence` is a label, not a filter: pass the
rail's latest ledger sequence you observed, one that covers your settlements, since the loop counts the ledger up to it.
The disclosure is signed with your signing key (Ed25519 over its RFC 8785 form without `sig`), so it is checked offline
with your public key, and it is also sealed in your node book. The tool publishes nothing.

A deal is admitted by `capsule-engine admission evaluate` over the pack in `admission-pack.json` (capsule-engine's
`ladder` conformance pack, `asg/admission_test/0.1.0`: **a test pack, not a policy for real deals**: its trust roots are placeholders (`ctl-trusted`, `judge-a`, `witness-a`), so a node that deals for real gives its own pack), for this price and currency. The node seals the decision (`deal admit`) and the agreement
cites it. With this pack a deal under one USD is a GO; a larger price asks for more (from one USD the peer's `history_card/1` and a settled exchange with you, from ten USD also two vouches) and the tool refuses
(answer the asks in capsule-engine, or give the node a pack of your own). `loop_admit` is the node's own GO or NO on
a peer, sealed in the node book before any deal: a NO stops every deal with it.

`loop_book_bundle` writes the record's Evidence Bundle (`capsulectl-deal verify --bundle FILE` checks it). To carry a
witness's receipt for the checkpoint, submit the checkpoint statement (`loop_book` writes it) to the witness and pass the
receipt file and the witness directory.

`loop_seal` seals the model call as a `runtime_claimed` effect record of the node book and writes the Capsule
(with its `signature` and `key_id`) to a file; `go run ./test/interop/goose-capsule FILE` checks it.
`runtime_claimed` is the node's own claim that the call happened, not the provider attesting it. The prompt is stored in
your node book as the record's payload, so don't seal a prompt you would not disclose with the book; the completion is
kept by digest only.

`loop_advertise` is step 03. It writes the node's `evidence-commitment/1`, which says how the node can be asked and what it
can answer, and never counts anything. It also writes the node's disclosure policy. Both are signed with your signing key,
in this process. The files are `<node_id>.json` and `policies/<node_id>.json`, laid out as they go under `nodes/commitments/`
in the loop repository (evidence-network/loop, `steps/03-advertise.md`), where you add them by pull request. The tool publishes
nothing. `node_id` is your node's id in the loop's list; the default is your signing key. The commitment staples the
profile's `history_card/2` (`deal standard-history staple`); the commitment carries the reply only, since
`evidence-commitment/1` has no member for the request. By default it says a person approves each answer (`human_gated`) and
that an asker waits `PT24H` before recording an absence. `endpoint`, `bindings`, `answers_within` and `human_gated`
change that. `loop_join` returns the same commitment and policy, and writes them when you give it `commitment_dir`. The
loop's `scripts/check_commitment.py` is the check.

`loop_advertise` with `relays` (comma-separated `wss://` URLs, the relays the node reads) is what makes the node askable with no
listener: the commitment then names `ask.nostr {mailbox_key, relays}` (the node's own secp256k1 mailbox key, `mailbox.key`,
mode 0600), and `poll_interval` (seconds) is how often you promise to call `loop_mailbox_poll`. `answers_within` may not be
shorter than `poll_interval`, nor than 90 seconds (a book records no absence window shorter than a minute after the request).

Step 07 over the mailbox (binding `nostr-mailbox-v0`, experimental; the exchange is `evidence-interop/1`'s, carried unchanged):

- **Answering.** `loop_mailbox_poll` reads the evidence requests addressed to the node's mailbox key on its relays and answers
  each from the node book under the node key (`deal node respond`): `history_card/1` and `history_card/2` to anyone, and for
  anything else the node's default policy decides, with a signed refusal (never silence) for a request outside it. An answered
  request is sealed in the node book (`goose_mailbox_answered`), so a restart does not answer it again. Call it at least as often
  as `poll_interval`.
- **Asking.** `loop_ask_mailbox` takes the asked node's commitment file, verifies its signature, seals the request in your node
  book, and publishes it to the mailbox the commitment names with a `deadline` of its `answers_within`. `loop_ask_mailbox_poll`
  then returns `answered` (the reply verified under the asked node's listed keys and recorded: an artifact or a signed
  refusal), `pending`, or `absent` once the deadline has passed with no recorded answer, which records the absence against the
  node, or `transport_error` when no relay could be read (it records nothing: poll again). The ask uses `history_card/2` on the
  `checkpoints` subject when the commitment's `answers.derivations` lists it, unless you name a derivation. A reply that arrives
  after the absence is kept beside it (`goose_mailbox_late`, signature-checked, `late: true`) and never rewrites it (binding
  §5.3). Arrival is judged by your clock at the poll that first sees the reply, so poll at least as often as the asked node's
  `answers_within`.
- The events are not encrypted (binding §8, N-3). Use a relay that authenticates its readers.

The other tools are `loop_book` (a node book's checkpoint), `loop_probe` (the deals this profile holds and the node book's
checkpoint), and `loop_ask` and `loop_ask_reply` (an evidence request you send, and its verified answer).

## Not here

- **Step 09, the dispute** (a referee's Result), ran on x-deal-v0 halves and is not built on x-deal-v1:
  [#830](https://github.com/action-state-group/evidencebook/issues/830) (`deal dispute`, the offer's adjudicator's
  `deal ruling`, `deal receive`).
- **The payment-reference opening** (step 06's `loop_pay_opening`) opened a v0 record's committed reference. An x-deal-v1
  payment leg carries its reference in clear, signed by its side, so there is nothing to open.
- **Importing a flat ledger** (`loop_import`) is not built in capsulectl. A node that has a capsule-emit ledger keeps it
  as it is.
- **The Mesh node's half check** (`goose-deal-half/1`, capsules-plugin v0.1.8): a v1 deal has no half file; its files are
  checked by `deal receive`.

## Run the scripted run

`scripted_run.py` runs that table (except step 09) with two nodes, each a `loop_server.py` process spoken to
over MCP stdio, as Goose speaks to it. It takes about six minutes: a report rests on a cadence checkpoint, and a tick is due
about every five.

```bash
.venv/bin/python python/examples/goose/scripted_run.py /tmp/goose-run
```

With `GOOSE_CARD_SERVICE` set to a service file ([Registering your card](#registering-your-card-optional)), the run also registers each node's card and writes
`<node>-card-statement.json`; unset, it sends nothing.

## What each side seals

Each side's deal book holds the records of an x-deal-v1 deal: its agreement (citing its admission decision and,
for the buyer, the intent), the payment leg it wrote (the buyer's payer leg, the seller's payee leg), the seller's
delivery, and its statement on the deal's two rows (`paid_as_agreed`, owed by the buyer, and `delivered`, owed by the seller; `received` says both were met, `not_received` says the buyer's `delivered` or the seller's `paid_as_agreed` was not). The final review's check and the user's answer are sealed too. Your own
words (what you asked, the payment reference) appear in a record only as commitments; their openings stay in your store.

A deal counts on your card as the profile counts it (x-deal-v1 PROFILE §11); the card is what `deal standard-history card`
derives from the store, and nothing in this example computes it.

## Checking a card

The card is a bundle that `capsulectl-deal verify --bundle` checks offline from the file alone, which is Go's check of it:

```bash
capsulectl-deal --profile goose-buyer verify --bundle /tmp/goose-run/buyer-card.json
```

CI runs it on every bundle the scripted run writes (`python/examples/goose/tests`).

## Registering your card (optional)

A card can be registered on a transparency service, so that a relying party you give the card statement to, and that accepts the service, can check that your key stated this
card no later than a time. This is off until you name a service: there is no default service, and with
`GOOSE_CARD_SERVICE` unset `loop_register_card` refuses and nothing is sent. The card is complete without it, and a
registration that fails changes nothing about the card, but the service may have logged the statement anyway (the error says so). It runs `capsulectl-book card register` (`GOOSE_CAPSULECTL_BOOK`
names another binary), over the node's `history_card/2`.

`card-service.example.json` names the service this demo uses: capsule-anchor at
`https://witness.agentactioncapsule.org`, **run by Action State Group**. That is this demo's choice, not a default of the
library or a recommendation. When we run this demo the node is ours too, so our runs show no check by an independent
party: a service the holder operates confers nothing. If you run it, the node is yours, and whether a service run by
Action State Group counts for you is your relying party's choice; a relying party should list only a service it holds
independent of the holder. Its `keys` entry is the key the service published (its `/.well-known/did.json`); you decide whether to trust it.

```bash
export GOOSE_CARD_SERVICE=python/examples/goose/card-service.example.json
# in Goose, after a finished tick:
#   loop_register_card out_path=/tmp/goose-run/buyer-card-statement.json
```

`loop_register_card` writes the card statement, with the service's Receipt, to `out_path`; keep it beside the card.
A registration is permanent: the service's log is append-only, and it, and anyone who reads its log, learn which of your keys registered a card for
which anchor, and when. The statement carries the card's digest, not the card, but a digest does not hide a small card from
a party that can guess its counts. What a passing Receipt shows, and what it does not, is in
`capsulectl-book card register --help`; a relying party checks it with its own copy of the service's key
(`capsulectl-book card check`), never one read from the statement file.
