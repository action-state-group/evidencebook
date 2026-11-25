# SPDX-License-Identifier: Apache-2.0
"""Step 06 Settle: the node's signed count of its settlements on one rail, ``settlement-disclosure/v1``.

The loop's step 06 (evidence-network/loop ``steps/06-settle``, its ``ledger_count`` check) compares this count with
the rail's own count of final settlements the node is a party to. The document is exactly the members the step reads,
signed with the node's key (its ``key_id``, the profile's signing key) over its RFC 8785 (JCS) form without ``sig``, so
anyone holding the node's public key checks it offline.

The count comes from the node's own sealed payment legs, never from the rail: ``deal list --json`` lists, for each
x-deal-v1 deal, the latest own leg of each payment (``own_legs``: the payer leg :meth:`goose_node.Node.pay` seals, the
payee leg :meth:`goose_node.Node.received` seals), with its status and its payment reference's type, read from the deal
book without a checkpoint. ``settlements`` is the number of deals with an own leg whose latest status is ``settled`` and
whose reference type :data:`RAILS` maps to the asked rail; a type the table does not know counts on no rail. An
x-deal-v1 offer names no rail, so the rail is the one the leg's reference is on. Every leg the node sealed counts,
including legs sealed before this tool existed. A Goose deal has one payment row and no refund (a refund leg is not
listed), so one deal is one settlement. The node itself tests no finality (it does not see a preimage): the count is
what its sealed legs say was settled.

The count is scoped to the asked rail: deals on any other rail never enter it, so a disclosure never reveals the
node's payment volume across rails (Sherlock2's condition on capsule-emit#408). It carries no amount, sum,
counterparty or payment reference: each payment reference is on the leg each side signed (Sherlock2, evidencebook#920).
``through_sequence`` is a label, not a filter: the caller passes the rail's latest ledger sequence it observed, which
must cover the node's settlements, since the step counts the ledger up to it.

Each disclosure is also sealed in the node book (``goose_settlement_disclosure``), so the node can later hand it over
in a node-book bundle that Go checks offline. This reads no rail and moves no money.
"""
from __future__ import annotations

import goose_node as gn
from commitment import _signed

PROFILE = "settlement-disclosure/v1"
#: The largest integer the loop's canonical form takes (``check_commitment.canonical``: integers within 2**53).
MAX_SEQUENCE = 2 ** 53 - 1
#: The rail each payment reference type is on. A leg whose type is not here counts on no rail.
RAILS = {"ln.payment_hash": "lightning"}


def settled_deals(node: gn.Node, rail: str) -> list[str]:
    """The deals for which this node's latest own leg of the payment is settled on ``rail``, in deal id order.
    Refused when the listing has no ``own_legs`` for an x-deal-v1 deal with a book (a capsulectl-deal from before
    the member), rather than counting that deal as unsettled and signing a short count."""
    out = []
    for d in node._run("list", "--json"):
        if d.get("version") != "x-deal-v1" or not d.get("log_id"):
            continue  # an x-deal-v0 deal, or one with no book of its own yet: no leg of this node's
        if "own_legs" not in d:
            raise gn.Refused("this capsulectl-deal lists no own_legs, so the count would miss every payment: install "
                             "a capsulectl-deal that lists them (see the README)")
        if any(leg.get("status") == "settled" and RAILS.get(leg.get("payment_ref_type")) == rail
               for leg in d["own_legs"]):
            out.append(d["deal_id"])
    return out


def disclosure(node: gn.Node, rail: str, through_sequence: int) -> dict:
    """The node's signed ``settlement-disclosure/v1`` for ``rail``, labelled with ``through_sequence``, sealed in the
    node book first."""
    if not isinstance(rail, str) or not rail.strip() or rail != rail.strip():
        raise gn.Refused("rail is the rail the node's payments were on, e.g. lightning")
    if isinstance(through_sequence, bool) or not isinstance(through_sequence, int) \
            or not 0 <= through_sequence <= MAX_SEQUENCE:
        raise gn.Refused("through_sequence is the rail's latest ledger sequence the node observed, a whole number "
                         f"from 0 to {MAX_SEQUENCE}")
    doc = {"type": PROFILE, "node": node.operator, "key_id": node.key_id, "rail": rail,
           "through_sequence": through_sequence, "settlements": len(settled_deals(node, rail))}
    node.note(gn.SETTLEMENT_DISCLOSURE, doc)
    return _signed(node, doc)
