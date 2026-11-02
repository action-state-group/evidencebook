# SPDX-License-Identifier: Apache-2.0
"""Step 07: a Goose node answers evidence requests by polling the mailbox its commitment
advertises, and asks another node the same way (evidencebook, capsule-emit#407).

Two nodes share one in-memory relay. The asked node advertises ``ask.nostr``; the asker reads it
from the signed commitment, seals the request, publishes it, and records what comes back."""
from __future__ import annotations

import json
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE / "tests"))

import commitment as cm  # noqa: E402
import goose_node as gn  # noqa: E402
import mailbox_exchange as mx  # noqa: E402
import nostr_mailbox as mb  # noqa: E402
from evidencebook import OUTCOME_ARTIFACT, OUTCOME_REFUSAL, RECORD_TYPE_ABSENCE, RECORD_TYPE_RESPONSE  # noqa: E402
from fake_relay import FakeRelay  # noqa: E402

AT = datetime(2026, 11, 1, 10, tzinfo=timezone.utc)
WSS = "wss://relay.example"


class Clock:
    def __init__(self) -> None:
        self.at = AT

    def __call__(self) -> datetime:
        return self.at

    def advance(self, **delta: int) -> None:
        self.at += timedelta(**delta)


@pytest.fixture(autouse=True)
def _offline(monkeypatch):
    monkeypatch.setenv("CAPSULE_WITNESS", "off")
    monkeypatch.delenv("CAPSULE_LEDGER", raising=False)


@pytest.fixture
def relay():
    r = FakeRelay()
    yield r
    r.close()


@pytest.fixture
def clock() -> Clock:
    return Clock()


@pytest.fixture
def nodes(tmp_path, clock):
    asker = gn.Node.open(tmp_path / "asker", operator="goose-asker", now=clock)
    asked = gn.Node.open(tmp_path / "asked", operator="goose-asked", now=clock)
    for n in (asker, asked):
        n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id})
    yield asker, asked
    asker.close()
    asked.close()


def advertise(node: gn.Node, **settings) -> dict:
    doc, _ = cm.build(node, settings=cm.Settings(relays=(WSS,), endpoint="nostr:mailbox", answers_within="PT1H",
                                                 poll_interval=60, **settings), now=AT)
    return doc


@pytest.fixture
def factory(relay):
    """Every relay the commitment names is the one in-memory relay."""
    return lambda _url: mb.Relay(relay.url)


def test_the_commitment_names_the_mailbox_and_the_poll_interval_bounds_answers_within(nodes):
    _, asked = nodes
    doc = advertise(asked)
    assert doc["ask"]["nostr"] == {"mailbox_key": asked.mailbox_key, "relays": [WSS]}
    assert doc["ask"]["bindings"] == ["evidencebook/respond"], "the binding defines no token to list"
    assert "checkpoints" in doc["answers"]["subjects"]
    cm.verify(doc)
    for bad in (dict(answers_within="PT30S"), dict(relays=("ws://relay.example",)), dict(relays=(WSS, WSS)),
                dict(relays=("wss://relay.example:443",)), dict(poll_interval=0)):
        args = dict(relays=(WSS,), answers_within="PT1H", poll_interval=60) | bad
        with pytest.raises(gn.Refused):
            cm.build(asked, settings=cm.Settings(**args), now=AT)


def test_a_commitment_that_does_not_verify_is_not_asked_through(nodes, factory):
    asker, asked = nodes
    doc = advertise(asked)
    doc["ask"]["nostr"]["relays"] = ["wss://evil.example"]
    with pytest.raises(gn.Refused, match="signature"):
        mx.ask(asker, doc, "checkpoints", min_freshness=0, relay_factory=factory)
    plain = cm.build(asked, now=AT)[0]
    with pytest.raises(gn.Refused, match="names no mailbox"):
        mx.ask(asker, plain, "checkpoints", min_freshness=0, relay_factory=factory)


@pytest.mark.parametrize("subject,derivation", [("checkpoints", ""), ("full_history", "history_card/1")])
def test_an_asked_node_answers_by_polling_and_the_answer_verifies_under_its_key(nodes, relay, factory, clock, subject,
                                                                              derivation):
    asker, asked = nodes
    doc = advertise(asked)
    sent = mx.ask(asker, doc, subject, derivation=derivation, min_freshness=0, relay_factory=factory, now=clock)
    assert mx.poll(asker, sent["request_event_id"], factory, clock)["status"] == "pending"
    assert mx.answer_round(asked, [WSS], factory) == [sent["request_event_id"]]
    clock.advance(minutes=5)
    got = mx.poll(asker, sent["request_event_id"], factory, clock)
    assert got["status"] == "answered" and got["outcome"] == OUTCOME_ARTIFACT
    record = asker.shelf.open(gn.NODE_BOOK).get(got["record_id"])
    assert record.header.record_type == RECORD_TYPE_RESPONSE
    assert record.header.statement["key_id"] == asked.key_id, "verified under the asked node's listed key"
    checkpoint = asked.shelf.open(gn.NODE_BOOK).checkpoints()[-1]
    assert record.header.statement["anchor"] == checkpoint.anchor_digest, "the answer cites the asked node's book"
    # The request was sealed before it was published, and answering is idempotent.
    request = asker.shelf.open(gn.NODE_BOOK).get(sent["request_record_id"])
    assert request.seq < record.seq
    assert mx.poll(asker, sent["request_event_id"], factory, clock)["record_id"] == got["record_id"]


def test_a_node_answers_each_request_once_and_not_again_after_a_restart(tmp_path, relay, factory, clock):
    asker = gn.Node.open(tmp_path / "asker", operator="goose-asker", now=clock)
    asked = gn.Node.open(tmp_path / "asked", operator="goose-asked", now=clock)
    try:
        asked.note(gn.JOINED, {"operator": asked.operator})
        sent = mx.ask(asker, advertise(asked), "checkpoints", min_freshness=0, relay_factory=factory, now=clock)
        assert mx.answer_round(asked, [WSS], factory) == [sent["request_event_id"]]
        assert mx.answer_round(asked, [WSS], factory) == []
        asked.close()
        asked = gn.Node.open(tmp_path / "asked", operator="goose-asked", now=clock)
        assert mx.answer_round(asked, [WSS], factory) == []
    finally:
        asker.close()
        asked.close()
    replies = [e for e in relay.events if e["kind"] == mb.REPLY_KIND]
    assert len(replies) == 1


def test_a_request_outside_the_disclosure_policy_gets_a_signed_refusal_never_silence(nodes, relay, factory, clock):
    asker, asked = nodes
    first = asked.shelf.open(gn.NODE_BOOK).records()[0].record_id
    sent = mx.ask(asker, advertise(asked), "exchange", subject_value=first, min_freshness=0, relay_factory=factory,
                  now=clock)
    assert mx.answer_round(asked, [WSS], factory) == [sent["request_event_id"]], "a reply was published"
    clock.advance(minutes=1)
    got = mx.poll(asker, sent["request_event_id"], factory, clock)
    assert got["status"] == "answered" and got["outcome"] == OUTCOME_REFUSAL
    statement = asker.shelf.open(gn.NODE_BOOK).get(got["record_id"]).header.statement
    assert statement["key_id"] == asked.key_id and statement["reason"] in ("policy_declined", "not_authorized")


def test_no_answer_by_the_deadline_is_an_absence_recorded_against_the_node(nodes, relay, factory, clock):
    asker, asked = nodes
    sent = mx.ask(asker, advertise(asked), "checkpoints", min_freshness=0, relay_factory=factory, now=clock)
    assert sent["deadline"] == int((AT + timedelta(hours=1)).timestamp()), "the commitment's answers_within"
    clock.advance(minutes=59)
    assert mx.poll(asker, sent["request_event_id"], factory, clock)["status"] == "pending"
    clock.advance(minutes=2)
    got = mx.poll(asker, sent["request_event_id"], factory, clock)
    assert got["status"] == "absent" and got["late"] is False
    record = asker.shelf.open(gn.NODE_BOOK).get(got["record_id"])
    assert record.header.record_type == RECORD_TYPE_ABSENCE
    assert record.header.statement["route"].startswith("nostr:nprofile")
    assert mx.poll(asker, sent["request_event_id"], factory, clock)["record_id"] == got["record_id"], "recorded once"


def test_an_answer_after_the_deadline_is_late_kept_beside_the_absence_and_never_rewrites_it(nodes, relay, factory,
                                                                                         clock):
    asker, asked = nodes
    sent = mx.ask(asker, advertise(asked), "checkpoints", min_freshness=0, relay_factory=factory, now=clock)
    clock.advance(hours=2)
    absent = mx.poll(asker, sent["request_event_id"], factory, clock)
    assert absent["status"] == "absent" and absent["late"] is False
    book = asker.shelf.open(gn.NODE_BOOK)
    written = book.get(absent["record_id"]).header.statement
    mx.answer_round(asked, [WSS], factory)
    late = mx.poll(asker, sent["request_event_id"], factory, clock)
    assert late["status"] == "absent" and late["late"] is True and late["record_id"] == absent["record_id"]
    [(record, statement)] = asker.notes(mx.LATE)
    assert statement["request_event_id"] == sent["request_event_id"] and record.record_id == late["late_record_id"]
    assert book.get(absent["record_id"]).header.statement == written, "the absence stays as written"
    assert mx.poll(asker, sent["request_event_id"], factory, clock)["late"] is True
    assert len(asker.notes(mx.LATE)) == 1, "kept once"


def test_a_reply_from_a_key_other_than_the_asked_node_is_not_an_answer(nodes, relay, factory, clock):
    asker, asked = nodes
    imposter = gn.Node.open(asker.home.parent / "imposter", operator="imposter", now=clock)
    try:
        imposter.note(gn.JOINED, {"operator": "imposter"})
        sent = mx.ask(asker, advertise(asked), "checkpoints", min_freshness=0, relay_factory=factory, now=clock)
        # The imposter reads the asked node's mailbox request and answers under its own keys.
        request = next(e for e in relay.events if e["kind"] == mb.REQUEST_KIND)
        book = imposter.shelf.open(gn.NODE_BOOK)
        reply, _ = book.respond(mx.Message(mb.decode(request["content"]), mx.MEDIA))
        event = mb.sign_event(asked.mailbox_secret, mb.REPLY_KIND,
                              [("e", request["id"]), ("p", request["pubkey"]), ("m", mx.MEDIA)],
                              mb.encode(reply.message.octets), int(AT.timestamp()))
        mb.Relay(relay.url).publish(event)
        assert mx.poll(asker, sent["request_event_id"], factory, clock)["status"] == "pending"
    finally:
        imposter.close()


def test_the_tools_advertise_poll_and_ask_through_the_mailbox(tmp_path, monkeypatch, relay):
    pytest.importorskip("mcp")
    import loop_server as ls

    monkeypatch.setattr(mb, "Relay", lambda _url, real=mb.Relay: real(relay.url))

    def use(home: str):
        if ls._node is not None:
            ls._node.close()
        monkeypatch.setattr(ls, "_node", None)
        monkeypatch.setattr(ls, "HOME", str(tmp_path / home))
        monkeypatch.setattr(ls, "OPERATOR", f"goose-{home}")

    def call(tool, **arguments):
        return json.loads(tool(**arguments))

    try:
        use("asked")
        call(ls.loop_join)
        out = call(ls.loop_advertise, out_dir=str(tmp_path / "out"), relays=f"{WSS}", poll_interval=60,
                   answers_within="PT1H")
        assert out["ok"], out
        assert call(ls.loop_advertise, out_dir=str(tmp_path / "bad"), relays=WSS, poll_interval=7200,
                   answers_within="PT1H")["ok"] is False
        commitment = next(p for p in (tmp_path / "out").glob("*.json"))
        use("asker")
        call(ls.loop_join)
        asked = call(ls.loop_ask_mailbox, commitment_file=str(commitment), subject_form="checkpoints",
                     min_freshness="0")
        assert asked["ok"] and asked["step"] == "07", asked
        poll = lambda: call(ls.loop_ask_mailbox_poll, request_event_id=asked["request_event_id"])  # noqa: E731
        assert poll()["status"] == "pending"
        use("asked")
        assert call(ls.loop_mailbox_poll)["answered"] == [asked["request_event_id"]]
        use("asker")
        assert poll()["status"] == "answered"
    finally:
        if ls._node is not None:
            ls._node.close()
            ls._node = None


def test_a_late_reply_to_one_request_does_not_mark_another_request_late(nodes, relay, factory, clock):
    asker, asked = nodes
    doc = advertise(asked)
    first = mx.ask(asker, doc, "checkpoints", min_freshness=0, relay_factory=factory, now=clock)
    clock.advance(hours=2)
    mx.answer_round(asked, [WSS], factory)
    assert mx.poll(asker, first["request_event_id"], factory, clock)["late"] is True
    second = mx.ask(asker, doc, "full_history", derivation="history_card/1", min_freshness=0, relay_factory=factory,
                    now=clock)
    clock.advance(hours=2)
    got = mx.poll(asker, second["request_event_id"], factory, clock)
    assert got["status"] == "absent" and got["late"] is False and "late_record_id" not in got


def test_an_outcome_bundle_discloses_the_outcome_and_the_request_it_cites_with_the_reply(nodes, relay, factory, clock):
    """What step 07 of the loop judges: the outcome's header and the request's, matched in the bundle,
    and the reply bytes the response record keeps."""
    import hashlib

    from agent_action_capsule import bundle as aac

    asker, asked = nodes
    sent = mx.ask(asker, advertise(asked), "checkpoints", min_freshness=0, relay_factory=factory, now=clock)
    mx.answer_round(asked, [WSS], factory)
    got = mx.poll(asker, sent["request_event_id"], factory, clock)
    built = asker.book_bundle(gn.NODE_BOOK, got["record_id"], disclose=True)
    result = aac.verify_bundle(built, strict=True)
    assert result.verdict == "VALID"
    matched = {d.capsule_id for d in result.disclosures if d.status == "disclosure_match"}
    assert matched == {got["record_id"], sent["request_record_id"]}, "the rest of the interval stays withheld"
    header = built["disclosures"][got["record_id"]]["agent_input"]
    [kept] = header["payload_commitments"]
    reply = next(e for e in relay.events if e["kind"] == mb.REPLY_KIND)
    assert hashlib.sha256(mb.decode(reply["content"])).hexdigest() == kept
    assert kept in built["extensions"][gn.PAYLOADS_EXTENSION]
    assert "disclosures" not in asker.book_bundle(gn.NODE_BOOK, got["record_id"])


def test_a_relay_map_points_an_advertised_relay_at_another_address(nodes, relay, clock, monkeypatch):
    """A rehearsal advertises a wss:// relay and runs a local one; nothing but the connection moves."""
    asker, asked = nodes
    monkeypatch.setenv("GOOSE_RELAY_MAP", json.dumps({WSS: relay.url}))
    sent = mx.ask(asker, advertise(asked), "checkpoints", min_freshness=0, now=clock)
    assert mx.answer_round(asked, [WSS]) == [sent["request_event_id"]]
    assert mx.poll(asker, sent["request_event_id"], now=clock)["status"] == "answered"
    monkeypatch.setenv("GOOSE_RELAY_MAP", "[")
    with pytest.raises(mb.MailboxError, match="GOOSE_RELAY_MAP"):
        mb.Relay(WSS).query({})


def test_a_disclosing_bundle_does_not_disclose_a_caller_sealed_record_twice(nodes):
    """A caller-sealed record (a model call) carries its header inline, so the bundle shows it already."""
    from agent_action_capsule import bundle as aac

    asker, _ = nodes
    call, _ = asker.seal_model_call("rehearsal", "rehearsal-model", "prompt", "completion")
    built = asker.book_bundle(gn.NODE_BOOK, call.record_id, disclose=True)
    assert "disclosures" not in built and aac.verify_bundle(built, strict=True).verdict == "VALID"


def test_the_absence_window_closes_no_sooner_than_a_minute_after_the_request(nodes, relay, factory, clock):
    """A request is sealed a moment before its deadline is counted from; the book records no window shorter
    than MINIMUM_ABSENCE_WINDOW after the request, so the later of the two closes it."""
    asker, asked = nodes
    doc = advertise(asked)
    doc_short = cm.build(asked, settings=cm.Settings(relays=(WSS,), endpoint="nostr:mailbox", answers_within="PT1M",
                                                     poll_interval=30), now=AT)[0]
    clock.at = AT + timedelta(milliseconds=900)  # the seal lands after the second the deadline is counted from
    sent = mx.ask(asker, doc_short, "checkpoints", min_freshness=0, relay_factory=factory, now=lambda: AT)
    clock.at = AT + timedelta(seconds=60, milliseconds=500)  # past the deadline, before a minute after the seal
    assert mx.poll(asker, sent["request_event_id"], factory, clock)["status"] == "pending"
    clock.at = AT + timedelta(seconds=61)
    assert mx.poll(asker, sent["request_event_id"], factory, clock)["status"] == "absent"
