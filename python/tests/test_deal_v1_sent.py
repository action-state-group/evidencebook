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


# -- pairs read and written as Go reads and writes them (#552 review r1) ---------------
# Each outcome below is Go's, from a probe of VerifySent, VerifyBook, PaymentCheck
# and BookPair on the same text.

BASE = next(c for c in PAYMENT["payment"] if c["name"] == "base")["input"]
OFFER = json.dumps(BASE["offer"])
REC, AI = json.dumps(BASE["offer"]["record"]), json.dumps(BASE["offer"]["agent_input"])
BOOK = json.dumps(next(c for c in PAYMENT["book"] if c["valid"])["pair"])


@pytest.mark.parametrize("raw, stage", [
    ('{"record":' + REC + ',"Record":{},"agent_input":' + AI + '}', None),  # the map merges
    ('{"record":{"x-extra":1},"Record":' + REC + ',"agent_input":' + AI + '}', "sent"),
    ('{"record":5,"Record":' + REC + ',"agent_input":' + AI + '}', "sent"),
    (OFFER + " {}", "sent"),  # trailing data is the pair's refusal, not the text's
    (OFFER + " garbage", "sent"),
    (OFFER.replace('"record": {', '"record": {"x": ' + "[" * 127 + "]" * 127 + ", ", 1), "sent"),  # no pair-level bound
])
def test_a_sent_pair_read_as_go_reads_it(raw, stage):
    if stage is None:
        v1sent.verify_sent(raw.encode())
    else:
        with pytest.raises(StageError) as exc:
            v1sent.verify_sent(raw.encode())
        assert exc.value.stage == stage


def test_a_signature_with_white_space_is_not_hex():
    o = BASE["offer"]
    bad = dict(o, record=dict(o["record"], signature=o["record"]["signature"][:2] + " " + o["record"]["signature"][2:]))
    with pytest.raises(StageError) as exc:
        v1sent.verify_sent(text(bad))
    assert (exc.value.stage, exc.value.detail) == ("sent", "the signature is not hex")


def test_a_book_pair_with_trailing_data_is_refused_at_book():
    v1sent.verify_book(BOOK.encode())
    with pytest.raises(StageError) as exc:
        v1sent.verify_book((BOOK + " {}").encode())
    assert exc.value.stage == "book"


def test_payment_check_takes_a_record_member_in_another_case():
    from evidencebook.deal.v1 import payment

    inp = payment.PaymentInput(OFFER.replace('"record"', '"Record"', 1).encode(), BASE["offer_digest"],
                               BASE["acceptor_key"], BASE["row"],
                               [json.dumps(leg).replace('"record"', '"Record"', 1).encode() for leg in BASE["legs"]])
    assert payment.payment_check(inp).check == "pass"


def test_book_pair_takes_and_refuses_what_go_s_marshal_does():
    case = PAYMENT["pair"][0]
    h = bytes.fromhex
    cap, env, hdr, pay = h(case["capsule"]), h(case["envelope"]), h(case["header"]), h(case["payload"])
    assert v1sent.book_pair(cap.decode().replace('"capsule_id"', '"Capsule_ID"', 1).encode(), env, hdr, pay)
    assert b'"header":null' in v1sent.book_pair(cap, env, None, pay)
    for bad in ((cap + b"garbage", env, hdr, pay), (cap[:-1] + b",}", env, hdr, pay), (cap, env, hdr, b"1 2")):
        with pytest.raises(ValueError):
            v1sent.book_pair(*bad)


# -- #552 review r2: each outcome Go's, from the same probe ----------------------------


def test_capsule_id_is_read_occurrence_by_occurrence():
    case = PAYMENT["pair"][0]
    h = bytes.fromhex
    cap, env, hdr, pay = h(case["capsule"]).decode(), h(case["envelope"]), h(case["header"]), h(case["payload"])
    cid = json.loads(cap)["capsule_id"]
    with pytest.raises(ValueError):  # a type error Go keeps to the end
        v1sent.book_pair(cap.replace('"capsule_id":', '"capsule_id":5,"capsule_id":', 1).encode(), env, hdr, pay)
    # A later null leaves the id as it was.
    later_null = cap.replace(f'"capsule_id":"{cid}"', f'"capsule_id":"{cid}","Capsule_ID":null', 1)
    assert v1sent.book_pair(later_null.encode(), env, hdr, pay)


@pytest.mark.parametrize("agent_input, stage", [("1e400", "canonicalization"), ("1e300", "sent")])
def test_a_number_past_float64_is_a_text_refusal(agent_input, stage):
    with pytest.raises(StageError) as exc:
        v1sent.verify_sent(('{"record":{},"agent_input":' + agent_input + "}").encode())
    assert exc.value.stage == stage


@pytest.mark.parametrize("depth, ok", [(1200, True), (9999, True), (10000, False)])
def test_a_raw_member_s_depth_is_go_s_marshal_bound(depth, ok):
    case = PAYMENT["pair"][0]
    h = bytes.fromhex
    args = (h(case["capsule"]), h(case["envelope"]), h(case["header"]), ("[" * depth + "]" * depth).encode())
    if ok:
        assert v1sent.book_pair(*args)
    else:
        with pytest.raises(ValueError):
            v1sent.book_pair(*args)


def test_a_pair_past_go_s_scanner_depth_is_a_text_refusal():
    deep = "[" * 10001 + "]" * 10001
    with pytest.raises(StageError) as exc:
        v1sent.verify_sent(('{"record":{},"agent_input":' + deep + "}").encode())
    assert exc.value.stage == "canonicalization"
