# SPDX-License-Identifier: Apache-2.0
"""A record whose Capsule its caller seals (Book.append_capsule), and field
commitments (Entry.fields), as Go's Book.AppendCapsule and Entry.Fields
make them. test/interop/caller-sealed checks the same records across Go and
Python on one shelf file."""
from __future__ import annotations

import dataclasses
import hashlib
import json
from datetime import datetime, timedelta, timezone

import pytest
from agent_action_capsule.canonical import jcs
from capsule_emit import produce
from capsule_emit.artifact import NEVER_RETAINED, PAYLOAD_DIGEST, PRESENT, Artifact
from capsule_emit.artifact.memory import MemoryArtifactStore

import evidencebook as eb
from evidencebook import deal

START = datetime(2026, 10, 1, tzinfo=timezone.utc)
SEED = hashlib.sha256(b"deal signer").digest()
SIGNER = eb.Ed25519Signer(SEED)
OTHER_SEED = hashlib.sha256(b"someone else").digest()


class Clock:
    def __init__(self) -> None:
        self.at = START

    def __call__(self) -> datetime:
        self.at += timedelta(seconds=1)
        return self.at


@pytest.fixture
def originals() -> MemoryArtifactStore:
    return MemoryArtifactStore("shelf", [SIGNER.public_key()])


def book(originals, store=None, clock=None) -> eb.Book:
    return eb.Book("deal/d1", "op", signer=SIGNER, log_id="deal/d1", log_key=eb.Ed25519Signer(b"\x11" * 32),
                   store=store if store is not None else eb.MemoryStore(), originals=originals,
                   now=clock or Clock())


PAYLOAD = jcs({"x-deal-v0": {"deal_id": "d1"}, "body": {}})


def step(seq: int, parent: str = "", *, seed: bytes = SEED, extra=(), header_member=True):
    """A step's entry, originals and sealer, as a deal producer makes them."""
    capsule, header = deal.step_capsule(deal.StepInput(
        deal_id="d1", seq=seq, operator="op", developer="dev", at=START,
        parent=deal.Chain(parent, deal.RELATION_FOLLOWS) if parent else None, local=f"local {seq}",
        amount="1500", currency="USD"))
    entry = eb.Entry(header.record_type, header.epistemic_type, subject_ref=header.subject_ref,
                     links=tuple(eb.Link(eb.CITES, c) for c in header.cites), fields=dict(header.fields))
    identity = produce.new_ed25519_signing_identity(seed)
    seen = {}

    def seal(h, header_json):
        seen["header"], seen["json"] = h, header_json
        members = [deal.Extension(eb.RECORD_HEADER_MEMBER, header_json)] if header_member else []
        return produce.seal(produce.SealInput(capsule=deal.emit_input(capsule, *members, *extra), identity=identity,
                                              payload=json.loads(PAYLOAD)))

    return entry, [Artifact("payload", PRESENT, PAYLOAD_DIGEST, PAYLOAD)], seal, seen


def test_a_caller_sealed_step_is_the_record_and_carries_its_header(originals):
    b = book(originals)
    entry, held, seal, seen = step(1)
    record = b.append_capsule(entry, held, seal)
    assert record.seq == 1 and record.header.record_type == deal.STEP_RECORD_TYPE
    assert record.header.subject_ref == "d1/1"
    assert seen["json"] == jcs(record.header.value())
    # The payload is the one payload commitment; the three fields are salted.
    assert record.header.payload_commitments == (hashlib.sha256(PAYLOAD).hexdigest(),)
    assert dict(record.header.field_commitments).keys() == {"amount", "currency", "deal_local"}
    assert record.header.commit_alg == eb.COMMIT_ALG_NONCE256 and record.header.retention_state == "AVAILABLE"
    stored = originals.get(record.record_id)
    capsule = json.loads(stored.capsule)
    assert capsule["action_id"] == "d1/1" and capsule["deal_id"] == "d1"
    assert jcs(capsule[eb.RECORD_HEADER_MEMBER]) == seen["json"]
    assert "agent_input_digest" in capsule["model_attestation"]["compute_attestation"]
    assert b.opening(record.record_id, "amount").text == "1500"
    assert b.opening(record.record_id, "deal_local").text == "local 1"
    opening = b.opening(record.record_id, "currency")
    assert eb.verify_opening(dict(record.header.field_commitments)["currency"], opening)
    assert not eb.verify_opening(dict(record.header.field_commitments)["amount"], opening)


def test_a_chain_of_steps_reopens_from_its_store(originals):
    store = eb.MemoryStore()
    b = book(originals, store)
    first = b.append_capsule(*step(1)[:3])
    second = b.append_capsule(*step(2, first.record_id)[:3])
    assert second.header.links == (eb.Link(eb.CITES, first.record_id),)
    again = book(originals, store)
    assert [r.record_id for r in again.records()] == [first.record_id, second.record_id]
    assert again.opening(second.record_id, "deal_local").text == "local 2"


def test_field_commitments_on_a_record_the_book_seals(originals):
    b = book(originals)
    record = b.append(eb.Entry("observation", eb.OBSERVED_EVENT, fields={"terms": "net 30"}))
    assert record.header.payload_commitments == ()
    assert record.header.retention_state == "AVAILABLE"
    assert b.opening(record.record_id, "terms") == eb.Opening(b.opening(record.record_id, "terms").nonce, "net 30")
    with pytest.raises(eb.NotFoundError):
        b.opening(record.record_id, "other")


@pytest.mark.parametrize("name", ["", "Upper", "_lead", "a" * 57, "dash-ed"])
def test_a_field_name_outside_the_grammar_is_refused(originals, name):
    with pytest.raises(eb.InvalidError, match="field name"):
        book(originals).append(eb.Entry("observation", eb.OBSERVED_EVENT, fields={name: "x"}))


def test_fields_and_caller_sealed_records_need_an_artifact_store():
    b = eb.Book("b", "op", signer=SIGNER, log_id="b", log_key=SIGNER)
    with pytest.raises(eb.InvalidError, match="artifact store"):
        b.append(eb.Entry("observation", eb.OBSERVED_EVENT, fields={"terms": "x"}))
    with pytest.raises(eb.InvalidError, match="artifact store"):
        b.append_capsule(*step(1)[:3])
    assert b.size() == 0


def test_a_capsule_without_its_header_member_is_refused(originals):
    b = book(originals)
    entry, held, seal, _ = step(1, header_member=False)
    with pytest.raises(eb.InvalidError, match="does not commit its header"):
        b.append_capsule(entry, held, seal)
    assert b.size() == 0


def test_a_capsule_carrying_another_header_is_refused(originals):
    b = book(originals)
    entry, held, seal, _ = step(1)

    def other(h, header_json):
        return seal(h, jcs(dict(json.loads(header_json), subject_ref="d1/9")))

    with pytest.raises(eb.InvalidError, match="header other than its record's"):
        b.append_capsule(entry, held, other)
    assert b.size() == 0


def test_a_capsule_signed_by_another_key_is_refused(originals):
    b = book(originals)
    entry, held, seal, _ = step(1, seed=OTHER_SEED)
    with pytest.raises(eb.InvalidError, match="not signed by the book's signer"):
        b.append_capsule(entry, held, seal)
    assert b.size() == 0


def test_a_capsule_whose_payload_is_its_header_too_is_refused(originals):
    b = book(originals)
    entry, held, _, _ = step(1)
    capsule, _ = deal.step_capsule(deal.StepInput(deal_id="d1", seq=1, operator="op", developer="dev", at=START))
    identity = produce.new_ed25519_signing_identity(SEED)

    def twice(h, header_json):
        return produce.seal(produce.SealInput(
            capsule=deal.emit_input(capsule, deal.Extension(eb.RECORD_HEADER_MEMBER, header_json)),
            identity=identity, payload=json.loads(header_json)))

    with pytest.raises(eb.InvalidError, match="twice"):
        b.append_capsule(entry, held, twice)


def test_a_sealer_error_writes_nothing_and_the_position_is_reused(originals):
    b = book(originals)

    def broken(h, header_json):
        raise RuntimeError("crashed")

    entry, held, seal, _ = step(1)
    with pytest.raises(eb.InvalidError, match="seal record: crashed"):
        b.append_capsule(entry, held, broken)
    assert b.append_capsule(entry, held, seal).seq == 1


def test_originals_are_checked_before_sealing(originals):
    b = book(originals)
    entry, held, seal, seen = step(1)
    with pytest.raises(eb.InvalidError, match="field opening"):
        b.append_capsule(entry, [Artifact("opening.x", PRESENT, "", b"x")], seal)
    with pytest.raises(eb.InvalidError, match="more than once"):
        b.append_capsule(entry, held + held, seal)
    with pytest.raises(eb.InvalidError, match="never retained"):
        b.append_capsule(entry, [Artifact("note", NEVER_RETAINED, "", b"x")], seal)
    with pytest.raises(eb.InvalidError, match="payloads are passed as originals"):
        b.append_capsule(dataclasses.replace(entry, payloads=(b"x",)), held, seal)
    assert not seen and b.size() == 0
    # A never-retained original is stored as declared and not committed.
    record = b.append_capsule(entry, held + [Artifact("note", NEVER_RETAINED)], seal)
    assert len(record.header.payload_commitments) == 1


def test_a_tampered_header_member_is_refused_on_reopen(originals):
    store = eb.MemoryStore()
    record = book(originals, store).append_capsule(*step(1)[:3])
    stored = store.records()[0]
    capsule = dict(stored.capsule)
    capsule[eb.RECORD_HEADER_MEMBER] = dict(capsule[eb.RECORD_HEADER_MEMBER], subject_ref="d1/9")
    store._records[record.record_id] = eb.StoredRecord(stored.seq, stored.record_id, stored.header, capsule,
                                                       stored.envelope)
    with pytest.raises(eb.CorruptError, match="does not commit its header"):
        book(originals, store)


class Forging:
    """An artifact store that hands back another opening than the one put."""

    def __init__(self, inner):
        self.inner, self.namespace = inner, inner.namespace

    def put(self, record):
        self.inner.put(record)

    def get(self, capsule_id):
        held = self.inner.get(capsule_id)
        forged = [Artifact(a.name, a.state, a.binding, jcs({"nonce": "0" * 64, "text": "net 60"}))
                  for a in held.artifacts]
        return dataclasses.replace(held, artifacts=forged)


def test_an_opening_that_does_not_open_its_commitment_is_corrupt(originals):
    b = book(Forging(originals))
    record = b.append(eb.Entry("observation", eb.OBSERVED_EVENT, fields={"terms": "net 30"}))
    with pytest.raises(eb.CorruptError, match="does not open"):
        b.opening(record.record_id, "terms")


def test_an_opening_checks_its_nonce_and_text():
    assert eb.Opening("0" * 64, "t").commitment() == hashlib.sha256(jcs({"nonce": "0" * 64, "text": "t"})).hexdigest()
    with pytest.raises(eb.InvalidError, match="nonce"):
        eb.Opening("0" * 63, "t").canonical()
    with pytest.raises(eb.InvalidError, match="UTF-8"):
        eb.Opening("0" * 64, "\ud800").canonical()
    assert not eb.verify_opening("not hex", eb.Opening("0" * 64, "t"))
    assert len(eb.new_opening("t").nonce) == 64
