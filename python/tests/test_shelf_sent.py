# SPDX-License-Identifier: Apache-2.0
"""Go's ``shelf_sent_test.go``: a sent journal record and its standalone
Capsule, with the Capsule's bytes pinned to what Go's ``emit.Seal`` seals
for the same inputs (the Rust crate pins the same values)."""

from __future__ import annotations

import hashlib
import json
import sqlite3
from datetime import datetime, timedelta, timezone

import pytest
from capsule_emit.artifact.sqlite import SqliteArtifactStore

from evidencebook import Entry, OBSERVED_EVENT
from evidencebook.cadence import CadenceConfig
from evidencebook.errors import CorruptError, InvalidError, ReleasedError
from evidencebook.shelf import JOURNAL_LOG_ID, Shelf
from evidencebook.signer import Ed25519Signer

from test_shelf_stamp import script

SENT = b'{"canonicalization":"jcs","deal_id":"deal-0123456789abcdef","profile":"x-deal-v1","record_type":"propose"}'
AT = datetime(2026, 10, 31, 12, 0, 30, tzinfo=timezone.utc)
SIGNER = Ed25519Signer(hashlib.sha256(b"signer").digest())


def open_shelf(path: str) -> Shelf:
    db = script.open_db(path)
    originals = SqliteArtifactStore(db, "test", [SIGNER.public_key()])
    originals.init()
    return Shelf.open_sqlite(path, operator="test-operator", signer=SIGNER, checkpoint_key=script.CHECKPOINT_KEY,
                             originals=originals, now=lambda: AT, lock_timeout=5)


def migrated(tmp_path) -> Shelf:
    shelf = open_shelf(str(tmp_path / "shelf.db"))
    c = shelf.cadence(CadenceConfig(log_id="deal-cadence/0123456789abcdef", ended=lambda _: False,
                                    witnesses=script.DIRECTORY))
    shelf.open("deal/a").append(Entry("observation", OBSERVED_EVENT, payloads=(b"1",)))
    c.tick(AT)
    c.migrate(AT + timedelta(minutes=1))
    return shelf


def sent_entry(payload: bytes = SENT) -> Entry:
    return Entry("deal_step", "producer_claim", payloads=(payload,))


def test_seal_sent_seals_the_journal_record_and_its_standalone_capsule(tmp_path):
    shelf = migrated(tmp_path)
    journal = shelf.journal()
    before = journal.size()
    sent = shelf.seal_sent(sent_entry(), {"deal_carried": {"n": 1}}, "deal-0123456789abcdef/sent")
    assert sent.error is None
    digest = hashlib.sha256(SENT).hexdigest()
    assert journal.size() == before + 1
    assert sent.record.header.book_id == JOURNAL_LOG_ID
    assert list(sent.record.header.payload_commitments) == [digest]
    c = sent.capsule
    assert c.payload == SENT
    doc = json.loads(c.capsule)
    assert doc["action_id"] == "deal-0123456789abcdef/sent" and doc["action_type"] == "fyi"
    assert doc["model_attestation"]["compute_attestation"]["agent_input_digest"] == digest
    assert doc["deal_carried"] == {"n": 1}, "the members travel"
    assert "chain" not in doc and "references" not in doc
    assert f"{JOURNAL_LOG_ID}#".encode() not in c.capsule
    # Go's SealSent seals these bytes for the same inputs.
    assert c.capsule_id == "b23e34ad49c092d272ebd6686428078d43f7c95d07e8af38b1cee448e40263c0"
    assert hashlib.sha256(c.capsule).hexdigest() == "825d6696bc13399d31736d93fd938a44b19aaf4d760010dea696edf4036c6810"
    assert hashlib.sha256(c.envelope).hexdigest() == "196a9c1db5de93811cb9e1f69dae6e47b0a0a773f57322d9c802d70c8d9f38eb"
    shelf.release()


def test_seal_sent_refuses_without_writing(tmp_path):
    shelf = migrated(tmp_path)
    journal = shelf.journal()
    before = journal.size()
    for name, entry, action_id, members in [
        ("no action id", sent_entry(), "", {}),
        ("no payload", Entry("deal_step", "producer_claim"), "a/sent", {}),
        ("two payloads", Entry("deal_step", "producer_claim", payloads=(SENT, SENT)), "a/sent", {}),
        ("not JSON", sent_entry(b"offer"), "a/sent", {}),
        ("not canonical", sent_entry(b'{"b":1, "a":2}'), "a/sent", {}),
        ("trailing data", sent_entry(SENT + b"{}"), "a/sent", {}),
        ("an inline record header", sent_entry(), "a/sent", {"evidence_record_header": {}}),
        ("a name capsule-emit refuses", sent_entry(), "a/sent", {"Bad": 1}),
        ("a base-profile member", sent_entry(), "a/sent", {"operator": "x"}),
        ("a null member", sent_entry(), "a/sent", {"deal_x": None}),
        ("a float in a member", sent_entry(), "a/sent", {"deal_x": {"a": [1.5]}}),
        ("an unsafe integer in a member", sent_entry(), "a/sent", {"deal_x": 9_007_199_254_740_992}),
    ]:
        with pytest.raises(InvalidError):
            shelf.seal_sent(entry, members, action_id)
            pytest.fail(name)
    with pytest.raises(Exception):
        shelf.seal_sent(Entry("", "producer_claim", payloads=(SENT,)), {}, "a/sent")
    assert journal.size() == before, "a refused sent record writes nothing"
    shelf.release()


def test_seal_sent_needs_the_journal(tmp_path):
    shelf = open_shelf(str(tmp_path / "shelf.db"))
    with pytest.raises(InvalidError):
        shelf.seal_sent(sent_entry(), {}, "a/sent")
    shelf.release()
    with pytest.raises(ReleasedError):
        shelf.seal_sent(sent_entry(), {}, "a/sent")


def test_seal_sent_returns_a_pending_record_with_its_capsule(tmp_path):
    shelf = migrated(tmp_path)
    journal = shelf.journal()
    before = journal.size()
    real = journal._substrate.append
    calls = {"n": 0}

    def failing(record_id, at=None):
        calls["n"] += 1
        if calls["n"] == 1:
            raise sqlite3.OperationalError("disk I/O error")
        return real(record_id, at)

    journal._substrate.append = failing
    sent = shelf.seal_sent(sent_entry(), {}, "deal-0123456789abcdef/sent")
    assert isinstance(sent.error, CorruptError)
    assert sent.capsule.capsule_id and sent.capsule.payload == SENT
    assert journal.size() == before, "not committed yet"
    nxt = journal.append(Entry("observation", OBSERVED_EVENT, payloads=(b"next",)))
    assert nxt.seq == sent.record.seq + 1
    shelf.release()
