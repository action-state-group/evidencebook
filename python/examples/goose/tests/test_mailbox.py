# SPDX-License-Identifier: Apache-2.0
"""Step 07: a Goose node answers evidence requests by polling the mailbox its commitment advertises, and asks
another node the same way (evidencebook, capsule-emit#407), on real capsulectl-deal profiles.

Two nodes share one in-memory relay. The asked node advertises ``ask.nostr``; the asker reads it from the
signed commitment, seals the request, publishes it, and records what comes back."""
from __future__ import annotations

import re
import time
from pathlib import Path

import pytest
from conftest import needs_binaries

import commitment as cm
import goose_node as gn
import mailbox_exchange as mx
import nostr_mailbox as mb
from ctl import Refused
from fake_relay import FakeRelay

pytestmark = needs_binaries
WSS = "wss://relay.example"


@pytest.fixture
def relay():
    r = FakeRelay()
    yield r
    r.close()


@pytest.fixture
def factory(relay):
    """Every relay the commitment names is the one in-memory relay."""
    return lambda _url: mb.Relay(relay.url)


@pytest.fixture
def nodes(tmp_path):
    asker = gn.Node.open(tmp_path / "asker", operator="goose-asker")
    asked = gn.Node.open(tmp_path / "asked", operator="goose-asked")
    for n in (asker, asked):
        n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id}, once="key_id")
    yield asker, asked
    asker.close()
    asked.close()


def advertise(node: gn.Node, within: str = "PT1H") -> dict:
    doc, _ = cm.build(node, settings=cm.Settings(relays=(WSS,), endpoint="nostr:mailbox", answers_within=within,
                                                 poll_interval=30))
    return doc


def test_an_asked_node_answers_by_polling_and_the_answer_verifies_under_its_key(nodes, factory):
    asker, asked = nodes
    sent = mx.ask(asker, advertise(asked), "checkpoints", derivation="history_card/2", min_freshness=0,
                  relay_factory=factory)
    assert mx.poll(asker, sent["request_event_id"], factory)["status"] == "pending"
    assert mx.answer_round(asked, [WSS], factory) == [sent["request_event_id"]]
    got = mx.poll(asker, sent["request_event_id"], factory)
    assert got["status"] == "answered" and got["outcome"] == "artifact"
    [record] = [r for r in asker.notes(mx.RESPONSE) if r["record_id"] == got["record_id"]]
    assert record["statement"]["key_id"] == asked.key_id, "verified under the asked node's listed key"
    assert mx.poll(asker, sent["request_event_id"], factory)["record_id"] == got["record_id"], "recorded once"
    # answering is idempotent: a request answered is not answered again
    assert mx.answer_round(asked, [WSS], factory) == []


def test_a_request_outside_the_disclosure_policy_gets_a_signed_refusal_never_silence(nodes, factory):
    asker, asked = nodes
    first = asked.notes()[0]["record_id"]
    sent = mx.ask(asker, advertise(asked), "exchange", subject_value=first, min_freshness=0, relay_factory=factory)
    assert mx.answer_round(asked, [WSS], factory) == [sent["request_event_id"]], "a reply was published"
    got = mx.poll(asker, sent["request_event_id"], factory)
    assert got["status"] == "answered" and got["outcome"] == "refusal"
    [record] = [r for r in asker.notes(mx.RESPONSE) if r["record_id"] == got["record_id"]]
    assert record["statement"]["key_id"] == asked.key_id
    assert record["statement"]["reason"] in ("policy_declined", "not_authorized")


def test_a_commitment_that_does_not_verify_is_not_asked_through(nodes, factory):
    asker, asked = nodes
    doc = advertise(asked)
    doc["answers_within"] = "PT2H"  # altered after signing
    with pytest.raises(Refused, match="signature does not verify"):
        mx.ask(asker, doc, "checkpoints", min_freshness=0, relay_factory=factory)


def test_an_answers_within_too_short_for_an_absence_is_refused(nodes, factory):
    asker, asked = nodes
    with pytest.raises(Refused, match="shortest waiting window"):
        mx.ask(asker, advertise(asked, "PT1M"), "checkpoints", min_freshness=0, relay_factory=factory)


def test_the_commitment_names_the_mailbox_and_the_poll_interval_bounds_answers_within(nodes):
    _, asked = nodes
    assert advertise(asked)["ask"]["nostr"] == {"mailbox_key": asked.mailbox_key, "relays": [WSS]}
    with pytest.raises(Refused, match="shorter than the poll interval"):
        cm.build(asked, settings=cm.Settings(relays=(WSS,), endpoint="nostr:mailbox", answers_within="PT10S",
                                             poll_interval=60))


def test_no_answer_by_the_deadline_is_an_absence_and_a_late_answer_never_rewrites_it(nodes, factory):
    """The one test that waits: a book records no absence window shorter than a minute."""
    asker, asked = nodes
    sent = mx.ask(asker, advertise(asked, "PT2M"), "checkpoints", derivation="history_card/2", min_freshness=0,
                  relay_factory=factory)
    assert mx.poll(asker, sent["request_event_id"], factory)["status"] == "pending"
    time.sleep(130)  # past the deadline
    absent = mx.poll(asker, sent["request_event_id"], factory)
    assert absent["status"] == "absent" and absent["late"] is False
    [record] = [r for r in asker.notes(mx.ABSENCE) if r["record_id"] == absent["record_id"]]
    written = record["statement"]
    assert written["route"].startswith("nostr:nprofile")
    mx.answer_round(asked, [WSS], factory)  # the answer comes after the absence
    late = mx.poll(asker, sent["request_event_id"], factory)
    assert late["status"] == "absent" and late["late"] is True and late["record_id"] == absent["record_id"]
    [kept] = asker.notes(mx.LATE)
    assert kept["record_id"] == late["late_record_id"]
    [again] = [r for r in asker.notes(mx.ABSENCE) if r["record_id"] == absent["record_id"]]
    assert again["statement"] == written, "the absence stays as written"
    assert len(asker.notes(mx.LATE)) == 1, "kept once"


def test_the_reply_limit_is_the_librarys():
    """``MAX_REPLY_BYTES`` is a copy of Go's ``MaxReplyBytes`` (request_limits.go): this reads it there."""
    root = Path(__file__).resolve().parents[4]
    text = (root / "request_limits.go").read_text()
    match = re.search(r"MaxReplyBytes\s*=\s*(\d+)\s*<<\s*(\d+)", text)
    assert match, "request_limits.go no longer defines MaxReplyBytes as N << M"
    assert mb.MAX_REPLY_BYTES == int(match.group(1)) << int(match.group(2))
