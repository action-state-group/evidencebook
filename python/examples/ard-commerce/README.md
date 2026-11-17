# ARD two-seller demo

Maya asks her agent for 200 conference badges by Friday, under 300 msat. Two sellers are found
through ARD: **Alder Print Co.** at 289 msat and **Kestrel Print** at 268 msat. Maya's library asks
each for its evidence, checks each signed answer, assesses each seller with an admission pack, and a
popup lets the presenter pick for Maya. The deal and a real Lightning payment follow the pick. It
runs on the [Goose loop reference](../goose/README.md)'s library: four nodes in one process (Maya,
Alder, Kestrel and the sample buyer that ran their history), one private relay on localhost and one
local web server.

**What it is not.** The sellers' history is *sample history, run by the demo operator*. Kestrel's
card comes from its own book: three sample deals carry the owner's temporary `outcome_note`
([#462](https://github.com/action-state-group/evidencebook/issues/462)); a hand-written sample card is the fallback. The screen says "assessment", never "admission": there is no payment gate, and the
presenter's pick is the one control (a LIMIT seller can be picked, and then nothing is paid). The
deal profile still gives no *did not hold* from records ([#212](https://github.com/action-state-group/evidencebook/issues/212)).

## What runs

| Step | Code |
|---|---|
| 2 ARD discovery: `/.well-known/ard.json` with one entry per seller, each pointing at the seller's signed `evidence-commitment/1` | `ard.py`, `server.py` |
| 3 evidence: `history_card/1` (the Goose reference's ask and poll), `standard_history/1` and the selection, each as a request sealed in Maya's book and answered over the sellers' mailbox | `buyer.gather`, `responder.py`, `relay.py` |
| 4 assessment: `capsule-engine admission evaluate` under the demo pack, one decision per seller, sealed in Maya's book | `buyer.assess`, `pack.py`, `pack/pack-source.yaml` |
| 4b the on-stage edit: `max_adverse_permille` 100 → 50, version 1.0.0 → 1.1.0, recompiled with evidencebook-skills' `pack_compile.py`, both sellers re-assessed on the saved evidence | `Run.edit` |
| 5 the popup and the pick, sealed before any payment | `popup.html`, `buyer.seal_pick` |
| 6 deal opened from the pick; Shockley's wallet issues an invoice for the picked price, Brattain's wallet pays it | `buyer.open_deal`, `pay.py` |
| 7 both halves compared, deal closed, Alder's card after the next checkpoint | `deals.py`, `demo_node.py` |

The demo pack is a fork of `admission-baseline`: pack id `demo/ard_admission/<version>` (never
`asg/`), `claims-held` removed (no card carries claims yet), trust roots naming the demo operator,
sat-band tiers T0 under 1 msat, T1 under 100 msat, T2 under 1,000 msat, `BTC` accepted, and the row
`card.band_history_or_stake` at T2 (`k: 10`, `max_adverse_permille`: 100 at the start, 50 after the
edit). All thresholds are placeholders for this demo only.

## Run sheet

Needs Python 3.10+, this package, a `capsule-engine` binary and an
`evidencebook-skills` checkout (for compiling the pack).

```bash
python -m venv python/.venv
python/.venv/bin/pip install ./python pyyaml rfc8785 websockets
export CAPSULE_ENGINE=/path/to/capsule-engine        # go build ./cmd/capsule-engine in capsule-engine/go
export ARD_SKILLS=/path/to/evidencebook-skills       # the pack is compiled under $ARD_SKILLS/demo-ard/
cd python/examples/ard-commerce
../../.venv/bin/python demo.py seed                  # once, about 40 s: 48 + 48 sample deals
../../.venv/bin/python demo.py run --rail lightning  # the demo; --rail fake moves no money
```

`run` pauses at each step (Enter) and asks for the new threshold (default 50). The popup opens in the
browser; click "Pay Alder Print Co. 289 msat". `Ctrl-C` ends a run; running again starts over on the
same sample history. `demo.py reset` forgets the demo home (`$ARD_HOME`, default
`~/.config/ard-commerce-demo`), keys and history included.

After a paid run Alder's card reads 49 deals. Run `reset` and `seed` before the next rehearsal so the
audience sees 48 again. The card counts the sale after the next checkpoint (one minute here, the
shortest cadence the profile allows), so a run within a minute of the previous tick waits.

### Real payment

`--rail lightning` runs only `pay-lightning control` `balance`, `fund` and `send` on the two test
nodes over `gcloud compute ssh` (Shockley `mc53-u755-provider` issues the invoice, Brattain
`mc53-u755-payer` pays it, `max_fee_msat` 1,000). Pricing, wallet policy and config are never touched.
One payment per run, for the picked price in msat; the screen shows the invoice amount, never a
balance difference (Shockley's wallet keeps a flat 100 msat receive fee, a presenter note, not on
screen). Preimages are never printed or kept. You need `gcloud` signed in to the project.

### The one switch

`--kestrel-card sentinel` (the default; `seed` writes it) reads Kestrel's card from its own book: three
deals carry the book owner's `outcome_note` (the temporary record of #462, in evidencebook v0.14.2),
which the card counts as did not hold. `--kestrel-card sample` is the fallback: Kestrel's hand-written
sample card (45 held, 3 did not hold), for a package without that record. Use the same value for `seed`
and `run`. The card does not show who wrote the note, and the screen says it is the seller's own
note, not confirmed by the buyer or a ruling.

## What differs from the design note

- The sellers answer `standard_history/1` themselves: the book answers that card only to an
  authenticated requester, so the seller agent signs it for Maya's request (as `Node.history_staple`
  signs it for the node's own).
- The selection is the demo's own form (a signed statement of unanswered disputes bound to Maya's
  nonce, `selection/1`); it carries no inclusion proofs and the screen says so.
- The Python deal profile has no producer for `ec:deal-reconcile@1`, so a deal counts as *held* when
  the node's own reconcile check (both closes name the same amount and currency) says so
  (`demo_node.py`).
- Maya holds no loop admission of a seller, only her assessment, so she checks a seller's half under the
  checkpoint key its verified commitment names (`deals._reconcile`), not under the key a GO admission pinned.
- The sellers' commitments name `wss://ard-demo.relay.local`, which this process maps to its one
  local relay (the mailbox events are not encrypted, so the relay is localhost only).

## Tests

```bash
cd python/examples/ard-commerce
../../.venv/bin/python -m pytest -q tests          # the unit tests; the demo tests skip without the two paths
CAPSULE_ENGINE=… ARD_SKILLS=… ../../.venv/bin/python -m pytest -q tests   # + the demo end to end on a fake rail, about 40 s
```

The end-to-end tests are not run in CI (they need a capsule-engine binary and an evidencebook-skills
checkout): run them with the two paths set before each rehearsal.
