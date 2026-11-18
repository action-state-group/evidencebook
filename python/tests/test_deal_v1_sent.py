# SPDX-License-Identifier: Apache-2.0
"""x-deal-v1 sent pairs and book pairs (evidencebook#461 PR 6d-2b-2), held
to Go by testdata/deal-v1/decline and testdata/deal-v1/payment (book, pair),
which Go's TestSentVectors and TestPaymentVectors write."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from evidencebook.deal.v1 import StageError
from evidencebook.deal.v1 import sent as v1sent

TESTDATA = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1"
DECLINE = json.loads((TESTDATA / "decline" / "vectors.json").read_text())["cases"]
PAYMENT = json.loads((TESTDATA / "payment" / "vectors.json").read_text())


def text(v) -> bytes:
    return json.dumps(v).encode()


def raw_members(path: Path, key: str) -> list[dict[str, str]]:
    """Each case of ``path``'s ``key`` list, its members as raw texts: a pair
    may give a member twice, which a parsed value loses."""
    top = v1sent._members(path.read_text())
    arr, out, dec, i = top[key].strip(), [], json.JSONDecoder(), 1
    while True:
        while arr[i] in " \t\n\r,":
            i += 1
        if arr[i] == "]":
            return out
        _, end = dec.raw_decode(arr, i)
        out.append(v1sent._members(arr[i:end]))
        i = end


RAW_DECLINE = raw_members(TESTDATA / "decline" / "vectors.json", "cases")
RAW_BOOK = raw_members(TESTDATA / "payment" / "vectors.json", "book")


@pytest.mark.parametrize("i", range(len(DECLINE)), ids=[c["name"] for c in DECLINE])
def test_sent_vectors(i):
    case = DECLINE[i]
    offer = None
    if case.get("offer") is not None:
        offer = v1sent.OfferContext(text(case["offer"]), case.get("offer_signer") or "")
    raw = RAW_DECLINE[i]["pair"].encode()
    if case["valid"]:
        assert v1sent.verify_sent(raw, offer).digest == case["digest"]
    else:
        with pytest.raises(StageError) as exc:
            v1sent.verify_sent(raw, offer)
        assert exc.value.stage == case["stage"]


@pytest.mark.parametrize("i", range(len(PAYMENT["book"])), ids=[c["name"] for c in PAYMENT["book"]])
def test_book_vectors(i):
    case = PAYMENT["book"][i]
    raw = RAW_BOOK[i]["pair"].encode()
    if case["valid"]:
        got = v1sent.verify_book(raw)
        assert (got.digest, got.record_id, got.key_id, got.log_id, got.seq, got.prev) == (
            case["digest"], case["record_id"], case["key_id"], case["log_id"], case["seq"], case.get("prev") or "")
    else:
        with pytest.raises(StageError) as exc:
            v1sent.verify_book(raw)
        assert exc.value.stage == case["stage"]


@pytest.mark.parametrize("case", PAYMENT["pair"], ids=[c["name"] for c in PAYMENT["pair"]])
def test_book_pair_vectors(case):
    # Go's BookPair bytes, hex in the vectors.
    h = bytes.fromhex
    got = v1sent.book_pair(h(case["capsule"]), h(case["envelope"]), h(case["header"]), h(case["payload"]))
    assert got == h(case["pair"])


@pytest.mark.parametrize("case", PAYMENT["payment"], ids=[c["name"] for c in PAYMENT["payment"]])
def test_payment_vectors(case):
    from agent_action_capsule.canonical import jcs

    from evidencebook.deal.v1 import payment

    i = case["input"]
    inp = payment.PaymentInput(text(i["offer"]), i.get("offer_digest") or "", i.get("acceptor_key") or "",
                               i.get("row") or "", [text(leg) for leg in i.get("legs") or ()])
    if case.get("refused"):
        with pytest.raises(StageError) as exc:
            payment.payment_check(inp)
        assert exc.value.stage == case["stage"]
    else:
        assert jcs(payment.payment_check(inp).value()) == jcs(case["result"])


def test_pair_carries_the_capsule_book_pair_carries():
    # Pair and BookPair sign the Capsule the same way (Go signedCapsule); BookPair's bytes are Go's.
    case = PAYMENT["pair"][0]
    h = bytes.fromhex
    got = v1sent._members(v1sent.pair(h(case["capsule"]), h(case["envelope"]), h(case["payload"])).decode())
    want = v1sent._members(h(case["pair"]).decode())
    assert sorted(got) == ["agent_input", "record"]
    assert got["record"] == want["record"] and got["agent_input"] == want["payload"]


def test_terms_leg_and_payment_rows():
    pc = next(c for c in PAYMENT["payment"] if c["name"] == "base")
    offer = v1sent.decode(text(pc["input"]["offer"]["agent_input"]))
    assert v1sent.payment_rows(offer.body["terms_rows"]) == ["paid_as_agreed"]
    leg = json.loads(v1sent.terms_leg(offer))
    assert leg["leg"] == "terms" and leg["version"] == "0" and leg["amount"]["assetCode"]
    with pytest.raises(ValueError):
        v1sent.terms_leg(v1sent.decode(text(DECLINE[0]["pair"]["agent_input"])))


def test_a_settle_member_read_as_go_reads_it():
    from evidencebook.deal.v1.payment import _settlement_member

    assert _settlement_member(None) is None
    assert _settlement_member("5") is None
    assert _settlement_member('{"leg": 5}') is None
    assert _settlement_member("null") == {"leg": "", "sealer_role": "", "terms_ref": ""}
    assert _settlement_member('{"LEG": "payer_observed", "x": 1}')["leg"] == "payer_observed"
