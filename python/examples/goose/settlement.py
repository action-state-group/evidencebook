# SPDX-License-Identifier: Apache-2.0
"""Step 06 Settle: the node's signed count of its settlements on one rail, ``settlement-disclosure/v1``.

The loop's step 06 (evidence-network/loop ``steps/06-settle``, its ``ledger_count`` check) compares this count with
the rail's own count of final settlements the node is a party to. The document is exactly the members the step reads,
signed with the node's key (its ``key_id``, the profile's signing key) over its RFC 8785 (JCS) form without ``sig``, so
anyone holding the node's public key checks it offline.

The count comes from the node's own records, never from the rail: each time the node seals its own payment leg as
settled (the payer leg, :meth:`goose_node.Node.pay`; the payee leg, :meth:`goose_node.Node.received`) it seals a
``goose_settled`` record in its node book naming the deal and the rail of the leg's payment reference (every leg a Goose
node seals names ``ln.payment_hash``, so lightning; an x-deal-v1 offer names no rail, so the deal's context is not
used). ``settlements`` is the number of distinct deals with such a record on the asked rail. Only legs sealed by a node
with this record are counted: a call cut short after its leg was sealed, or a leg sealed before this version, is
counted once ``loop_settle`` or ``loop_deal_received`` is called again with the same arguments (the verb finds the
sealed leg and writes it again). A Goose deal has one payment row and no refund, so one
deal is one settlement. The legs are the payment reference the node observed, recorded as settled; the node itself
tests no finality (it does not see a preimage), so the count is what its records say was settled.

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


def settled_deals(node: gn.Node, rail: str) -> list[str]:
    """The deals on ``rail`` for which this node sealed its own payment leg as settled, each once, in the order sealed."""
    out: list[str] = []
    for record in node.notes(gn.SETTLED):
        st = record["statement"]
        if st.get("rail") == rail and st.get("deal_id") not in out:
            out.append(st["deal_id"])
    return out


def disclosure(node: gn.Node, rail: str, through_sequence: int) -> dict:
    """The node's signed ``settlement-disclosure/v1`` for ``rail``, labelled with ``through_sequence``, sealed in the
    node book first."""
    if not isinstance(rail, str) or not rail.strip() or rail != rail.strip():
        raise gn.Refused("rail is the rail the node's deals were opened on, e.g. lightning")
    if isinstance(through_sequence, bool) or not isinstance(through_sequence, int) \
            or not 0 <= through_sequence <= MAX_SEQUENCE:
        raise gn.Refused("through_sequence is the rail's latest ledger sequence the node observed, a whole number "
                         f"from 0 to {MAX_SEQUENCE}")
    doc = {"type": PROFILE, "node": node.operator, "key_id": node.key_id, "rail": rail,
           "through_sequence": through_sequence, "settlements": len(settled_deals(node, rail))}
    node.note(gn.SETTLEMENT_DISCLOSURE, doc)
    return _signed(node, doc)
