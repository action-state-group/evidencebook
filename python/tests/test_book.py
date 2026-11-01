# SPDX-License-Identifier: Apache-2.0
"""Both sides of the evidence request interaction, and the book under them."""
from __future__ import annotations

import base64
import hashlib
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from agent_action_capsule.canonical import jcs
from agent_action_capsule.evidence_request import sign_refusal

import evidencebook as eb
from agent_action_capsule.evidence_request import Refusal
from evidencebook.request import refusal_conforming, request_profile
from evidencebook.book import KIND_CLL_CHECKPOINT, KIND_CLL_INCLUSION_PROOF
from evidencebook.signer import EvidenceSigner

START = datetime(2026, 10, 1, tzinfo=timezone.utc)


def key(label: str) -> eb.Ed25519Signer:
    return eb.Ed25519Signer(hashlib.sha256(label.encode()).digest())


class Clock:
    def __init__(self) -> None:
        self.at = START

    def __call__(self) -> datetime:
        self.at += timedelta(seconds=1)
        return self.at


def book(clock: Clock, name: str = "b", store: eb.Store | None = None, **kwargs) -> eb.Book:
    return eb.Book(name, f"{name}-operator", signer=key(f"{name} signer"), log_id=f"{name}-log",
                   log_key=key(f"{name} log"), store=store, now=clock, **kwargs)


def keys_of(b: eb.Book) -> eb.ResponderKeys:
    return eb.ResponderKeys(signer=b.key_id, checkpoint=b.checkpoints()[-1].key_id)


@pytest.fixture
def clock() -> Clock:
    return Clock()


@pytest.fixture
def responder(clock: Clock) -> tuple[eb.Book, list[eb.Record]]:
    b = book(clock, "responder")
    records = [b.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref=f"order-{i}")) for i in range(3)]
    b.checkpoint()
    records += [b.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref="order-3",
                                  links=(eb.Link(eb.CITES, records[0].record_id),)))]
    b.checkpoint()
    return b, records


PEERS = eb.RespondOptions(authenticated_requester="alice", policy=eb.SharePolicy(history_segments="peers"))


def ask(requester: eb.Book, responder: eb.Book, subject: eb.Subject, **coverage) -> tuple[eb.SentRequest, eb.Reply]:
    coverage = coverage or {"expected_pin": responder.checkpoints()[-1].anchor_digest}
    sent = requester.request(eb.EvidenceRequest(subject, eb.Coverage(**coverage)), "responder")
    reply, _ = responder.respond(sent.message, PEERS)
    return sent, reply


# --- the requester ------------------------------------------------------------------


def test_request_records_the_request_before_sending(clock):
    b = book(clock)
    sent = b.request(eb.EvidenceRequest(eb.Subject("record", "a" * 64), eb.Coverage(expected_pin="b" * 64)), "peer-1")
    record = b.get(sent.record_id)
    assert record.header.record_type == eb.RECORD_TYPE_REQUEST
    assert record.header.statement == {"request": json.loads(sent.octets), "responder_ref": "peer-1"}
    request = json.loads(sent.octets)
    assert request["profile"] == eb.PROFILE and len(request["nonce"]) == 32
    assert sent.octets == jcs(request) and sent.digest == hashlib.sha256(sent.octets).hexdigest()


def test_request_refuses_a_reused_nonce(clock):
    b = book(clock)
    req = eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(min_freshness=1), nonce="n-1")
    b.request(req)
    with pytest.raises(eb.InvalidError, match="nonce already used"):
        b.request(req)
    assert b.size() == 1


def test_request_refuses_a_request_that_does_not_conform(clock):
    b = book(clock)
    with pytest.raises(eb.InvalidError):
        b.request(eb.EvidenceRequest(eb.Subject("record", "A" * 64), eb.Coverage(expected_pin="b" * 64)))
    with pytest.raises(eb.InvalidError):
        b.request(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage()))
    assert b.size() == 0


def test_an_artifact_reply_is_verified_and_recorded(clock, responder):
    r, records = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("record", records[0].record_id))
    assert reply.envelope is not None
    assert [kind for kind, _ in reply.envelope.verification][:2] == [KIND_CLL_CHECKPOINT, KIND_CLL_INCLUSION_PROOF]
    record = q.record_response(sent.record_id, reply.message, keys_of(r))
    assert record.header.record_type == eb.RECORD_TYPE_RESPONSE
    assert record.header.links == (eb.Link(eb.CITES, sent.record_id),)
    assert record.header.statement == {
        "request_digest": sent.digest, "outcome": eb.OUTCOME_ARTIFACT, "key_id": r.key_id,
        "checkpoint_key_id": keys_of(r).checkpoint, "artifact_digest": records[0].record_id,
        "anchor": r.checkpoints()[-1].anchor_digest, "witness": eb.UNWITNESSED,
    }
    assert q.reply_octets(record.record_id) == reply.message.octets
    assert record.header.payload_commitments == (hashlib.sha256(reply.message.octets).hexdigest(),)
    with pytest.raises(eb.InvalidError, match="already recorded"):
        q.record_response(sent.record_id, reply.message, keys_of(r))


def test_a_record_set_reply_names_what_was_not_checked(clock, responder):
    r, records = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("exchange", records[0].record_id))
    artifact = json.loads(reply.envelope.artifact)
    assert [entry["digest"] for entry in artifact["records"]] == [records[3].record_id]
    statement = q.record_response(sent.record_id, reply.message, keys_of(r)).header.statement
    assert statement["outcome"] == eb.OUTCOME_ARTIFACT and statement["not_checked"] == ["anchor_inclusion"]


@pytest.mark.parametrize("subject,derivation", [(eb.Subject("checkpoints"), None),
                                                (eb.Subject("full_history"), eb.DERIVATION_HISTORY_CARD)])
def test_a_checkpoint_chain_is_checked_for_a_stranger(clock, responder, subject, derivation):
    r, _ = responder
    q = book(clock, "requester")
    sent = q.request(eb.EvidenceRequest(subject, eb.Coverage(expected_pin=r.checkpoints()[-1].anchor_digest),
                                        derivation=derivation))
    size = r.size()
    reply, answered = r.respond(sent.message)
    assert answered is None and r.size() == size, "a stranger's request grows no log"
    statement = q.record_response(sent.record_id, reply.message, keys_of(r)).header.statement
    assert statement["outcome"] == eb.OUTCOME_ARTIFACT and "not_checked" not in statement


def test_a_refusal_is_recorded_as_the_responders_act(clock, responder):
    r, _ = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("record", "c" * 64))
    assert reply.refusal is not None and reply.refusal.reason == "no_such_subject"
    statement = q.record_response(sent.record_id, reply.message, keys_of(r)).header.statement
    assert statement["outcome"] == eb.OUTCOME_REFUSAL and statement["reason"] == "no_such_subject"


def resign(envelope: dict, signer: eb.Ed25519Signer) -> bytes:
    signed = ("profile", "request_digest", "subject", "derivation", "anchor", "artifact_digest", "verification",
              "responder", "issued_at")
    envelope["sig"] = signer.sign(jcs({k: envelope[k] for k in signed if k in envelope})).hex()
    return jcs(envelope)


def test_a_signed_envelope_that_fails_a_check_is_received_and_failed(clock, responder):
    r, records = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("record", records[0].record_id))
    envelope = json.loads(reply.message.octets)
    tampered = base64.urlsafe_b64decode(envelope["artifact"] + "==") + b" "
    envelope["artifact"] = base64.urlsafe_b64encode(tampered).rstrip(b"=").decode()
    record = q.record_response(sent.record_id, eb.Message(resign(envelope, key("responder signer"))), keys_of(r))
    assert record.header.statement["outcome"] == eb.OUTCOME_ARTIFACT_FAILED
    assert record.header.statement["failure"].startswith("artifact_digest")


def test_a_reply_not_under_the_pinned_key_is_refused_and_not_recorded(clock, responder):
    r, records = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("record", records[0].record_id))
    size = q.size()
    impostor = eb.ResponderKeys(signer=key("impostor").public_key().hex(), checkpoint=keys_of(r).checkpoint)
    with pytest.raises(eb.InvalidError, match="pinned responder key"):
        q.record_response(sent.record_id, reply.message, impostor)
    forged = json.loads(reply.message.octets)
    with pytest.raises(eb.InvalidError, match="signature"):
        q.record_response(sent.record_id, eb.Message(resign(forged, key("impostor"))), keys_of(r))
    with pytest.raises(eb.InvalidError):
        q.record_response(sent.record_id, eb.Message(b"{}"), keys_of(r))
    with pytest.raises(eb.InvalidError, match="required"):
        q.record_response(sent.record_id, reply.message, eb.ResponderKeys(r.key_id, ""))
    with pytest.raises(eb.NotFoundError):
        q.record_response("d" * 64, reply.message, keys_of(r))
    assert q.size() == size


def test_record_absence(clock):
    q = book(clock, "requester")
    sent = q.request(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(min_freshness=1), route="peer:x"))
    asked = clock.at
    with pytest.raises(eb.InvalidError, match="pending"):
        q.record_absence(sent.record_id, clock.at + timedelta(hours=1))
    clock.at += timedelta(minutes=5)
    with pytest.raises(eb.InvalidError, match="at least"):
        q.record_absence(sent.record_id, asked + timedelta(seconds=30))
    with pytest.raises(eb.InvalidError, match="route"):
        q.record_absence(sent.record_id, asked + timedelta(minutes=2), "a2a:y")
    record = q.record_absence(sent.record_id, asked + timedelta(minutes=2))
    assert record.header.record_type == eb.RECORD_TYPE_ABSENCE
    assert record.header.statement == {
        "outcome": "recorded_absence", "request_digest": sent.digest, "route": "peer:x",
        "window": {"opened": "2026-10-01T00:00:01Z", "closed": "2026-10-01T00:02:01Z"},
        "recorded_at": eb.book.profile_time(clock.at - timedelta(seconds=1)),  # read before the commit
    }
    with pytest.raises(eb.InvalidError, match="already recorded"):
        q.record_absence(sent.record_id, asked + timedelta(minutes=3))


def test_an_absence_waits_for_the_requests_deadline(clock):
    q = book(clock, "requester")
    sent = q.request(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(min_freshness=1),
                                        deadline="2026-10-01T01:00:00Z"))
    clock.at += timedelta(hours=2)
    with pytest.raises(eb.InvalidError, match="deadline"):
        q.record_absence(sent.record_id, START + timedelta(minutes=30))
    q.record_absence(sent.record_id, START + timedelta(hours=1))


# --- the responder ------------------------------------------------------------------------


def test_respond_seals_an_answered_record_naming_no_requester(clock, responder):
    r, records = responder
    request = eb.encode_request(eb.EvidenceRequest(eb.Subject("record", records[0].record_id),
                                                   eb.Coverage(expected_pin=r.checkpoints()[-1].anchor_digest)))
    reply, record = r.respond(eb.Message(request), PEERS)
    assert record.header.record_type == eb.RECORD_TYPE_REQUEST_ANSWERED
    assert record.header.statement == {
        "request_digest": hashlib.sha256(request).hexdigest(), "subject_kind": "record", "outcome": "artifact",
        "relationship": "identified", "anchor": r.checkpoints()[-1].anchor_digest,
        "artifact_digest": records[0].record_id,
    }
    assert "alice" not in json.dumps(record.header.value())
    # Bookkeeping records are never served.
    request = eb.encode_request(eb.EvidenceRequest(eb.Subject("record", record.record_id),
                                                   eb.Coverage(min_freshness=1)))
    assert r.respond(eb.Message(request), PEERS)[0].refusal.reason == "no_such_subject"


def test_the_artifact_does_not_depend_on_who_asks_or_how(clock, responder):
    r, records = responder
    pin = r.checkpoints()[-1].anchor_digest
    replies = []
    for nonce, media_type, who in (("n-1", eb.MEDIA_TYPE_JSON, "alice"), ("n-2", eb.MEDIA_TYPE_CBOR, "bob")):
        request = eb.encode_request(eb.EvidenceRequest(eb.Subject("exchange", records[0].record_id),
                                                       eb.Coverage(expected_pin=pin), nonce=nonce), media_type)
        reply, _ = r.respond(eb.Message(request, media_type),
                             eb.RespondOptions(authenticated_requester=who, policy=eb.SharePolicy(history_segments="peers")))
        assert reply.message.media_type == media_type
        replies.append(eb.parse_reply(reply.message).envelope.artifact)
    assert replies[0] == replies[1]


def test_a_stranger_is_refused_any_record(clock, responder):
    r, records = responder
    request = eb.encode_request(eb.EvidenceRequest(eb.Subject("record", records[0].record_id),
                                                   eb.Coverage(min_freshness=1)))
    reply, record = r.respond(eb.Message(request), eb.RespondOptions(policy=eb.SharePolicy(history_segments="peers")))
    assert reply.refusal.reason == "not_authorized" and record is None


def test_counterparties_tier_answers_only_the_counterparty(clock):
    r = book(clock, "responder")
    mine = r.append(eb.Entry("observation", eb.OBSERVED_EVENT, counterparty_ref="carol"))
    r.checkpoint()
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(eb.Subject("record", mine.record_id),
                                                              eb.Coverage(min_freshness=1))))
    for who, outcome in (("carol", None), ("dave", "not_authorized")):
        reply, record = r.respond(request, eb.RespondOptions(authenticated_requester=who))
        assert (reply.refusal.reason if reply.refusal else None) == outcome
        assert record.header.statement["relationship"] == ("counterparty" if who == "carol" else "identified")
    # The record's octets commit its header by digest, so they name no one.
    assert r.respond(request, eb.RespondOptions(authenticated_requester="carol"))[0].envelope is not None


def test_malformed_traffic_is_refused_and_never_grows_the_log(clock, responder):
    r, _ = responder
    size = r.size()
    for octets in (b"not json", b"[]", b'{"subject":{"record":"x"}}', b"\x00" * (65 << 10)):
        reply, record = r.respond(eb.Message(octets), PEERS)
        assert record is None
        assert reply.refusal.reason in ("request_malformed", "policy_declined")
    with pytest.raises(eb.InvalidError):
        r.respond(eb.Message(b"{}", "text/plain"), PEERS)
    assert r.size() == size


def test_an_unknown_profile_is_refused_with_the_profiles_supported(clock, responder):
    r, records = responder
    request = eb.encode_request(eb.EvidenceRequest(eb.Subject("record", records[0].record_id),
                                                   eb.Coverage(min_freshness=1), profile="evidence-interop/9"))
    reply, record = r.respond(eb.Message(request), PEERS)
    assert reply.refusal.reason == "request_malformed" and record is None
    assert json.loads(reply.message.octets)["detail"] == "profiles supported: evidence-interop/0, evidence-interop/1"
    assert eb.parse_reply(reply.message).detail == "profiles supported: evidence-interop/0, evidence-interop/1"


def test_evidence_interop_1_is_answered_as_0_is(clock, responder):
    """evidencebook#248: a request naming evidence-interop/1 gets the artifact
    it would under /0 (v1 §3.3 step 3), in an envelope naming /1; one naming
    /0 or no profile is answered under /0."""
    r, records = responder
    artifacts = {}
    for asked, named in (("", eb.PROFILE), (eb.PROFILE, eb.PROFILE), (eb.PROFILE_V1, eb.PROFILE_V1)):
        request = eb.encode_request(eb.EvidenceRequest(eb.Subject("record", records[0].record_id),
                                                       eb.Coverage(min_freshness=1), nonce="n" + asked, profile=asked))
        reply, _ = r.respond(eb.Message(request), PEERS)
        assert reply.envelope is not None and reply.envelope.profile == named, asked
        artifacts[asked] = reply.envelope.artifact
    assert artifacts[eb.PROFILE_V1] == artifacts[eb.PROFILE] == artifacts[""]


@pytest.mark.parametrize("policy", [eb.REFUSAL_POLICY_SPECIFIC, eb.REFUSAL_POLICY_UNIFORM])
@pytest.mark.parametrize("profile,want", [(eb.PROFILE, "policy_declined"), (eb.PROFILE_V1, "capacity_exceeded")])
def test_a_limit_is_refused_for_the_requested_profile(clock, policy, profile, want):
    """A limit of the book's own, the request size, the rate limiter or the
    answer item cap, is capacity_exceeded under evidence-interop/1 whatever
    the refusal policy (ER -02 §11), and policy_declined under /0, which
    registers no such token."""
    r = book(clock, "responder", responder=eb.ResponderPolicy(refusal_policy=policy, max_answer_items=1,
                                                              max_request_bytes=400,
                                                              limiter=eb.WindowLimiter(1, timedelta(hours=1))))
    first = r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    for _ in range(2):
        r.append(eb.Entry("observation", eb.OBSERVED_EVENT, links=(eb.Link(eb.CITES, first.record_id),)))
    r.checkpoint()
    for request in (eb.EvidenceRequest(eb.Subject("exchange", first.record_id), eb.Coverage(min_freshness=1), profile=profile),
                    eb.EvidenceRequest(eb.Subject("record", first.record_id), eb.Coverage(min_freshness=1),
                                       route="r" * 400, profile=profile),
                    eb.EvidenceRequest(eb.Subject("record", first.record_id), eb.Coverage(min_freshness=1), profile=profile)):
        reply = r.respond(eb.Message(eb.encode_request(request)), PEERS)[0]
        assert reply.refusal.reason == want
        assert refusal_conforming(reply.refusal, profile)


def test_request_profile_is_that_of_a_well_formed_request():
    """A request refused for a limit asks for the profile of a well-formed
    request, /0 for any other, as Go's requestProfile reads it."""
    asks = b'{"coverage":{"min_freshness":1},"subject":{"checkpoints":null}'
    for octets, want in ((asks + b',"profile":"evidence-interop/1"}', eb.PROFILE_V1),
                         (asks + b',"profile":"evidence-interop/0"}', eb.PROFILE), (asks + b"}", eb.PROFILE),
                         (asks + b',"profile":""}', eb.PROFILE), (asks + b',"profile":"evidence-interop/9"}', eb.PROFILE),
                         (asks + b',"profile":1}', eb.PROFILE), (b'{"profile":"evidence-interop/1"}', eb.PROFILE),
                         (b'{"coverage":{},"profile":"evidence-interop/1","subject":{"checkpoints":null}}', eb.PROFILE),
                         (b'["evidence-interop/1"]', eb.PROFILE), (b'{"profile":', eb.PROFILE),
                         (asks + b',"profile":"evidence-interop/1","profile":"evidence-interop/1"}', eb.PROFILE)):
        assert request_profile(eb.Message(octets)) == want, octets
    cbor = eb.encode_request(eb.EvidenceRequest(eb.Subject("checkpoints", None), eb.Coverage(min_freshness=1),
                                                profile=eb.PROFILE_V1), eb.MEDIA_TYPE_CBOR)
    assert request_profile(eb.Message(cbor, eb.MEDIA_TYPE_CBOR)) == eb.PROFILE_V1
    assert request_profile(eb.Message(asks + b',"profile":"evidence-interop/1"}', "text/plain")) == eb.PROFILE


def test_a_refusal_conforms_under_the_profile_its_request_asked_for():
    """v1 §6.3: ER -02's two reasons conform only to an evidence-interop/1
    request, so a /0 requester records capacity_exceeded as nonconforming."""
    for reason, under_0 in (("capacity_exceeded", False), ("request_unauthenticated", False), ("policy_declined", True)):
        refusal = Refusal(request_digest="ab" * 32, reason=reason, issued_at="2026-10-31T12:00:00Z", key_id="", sig="")
        assert refusal_conforming(refusal) is under_0
        assert refusal_conforming(refusal, eb.PROFILE) is under_0
        assert refusal_conforming(refusal, eb.PROFILE_V1)
        # A profile the package does not implement: any implemented one's tokens.
        assert refusal_conforming(refusal, "evidence-interop/9")


def test_a_freshness_floor_issues_an_anchor_for_an_authenticated_requester(clock, responder):
    r, records = responder
    r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    q = book(clock, "requester")
    floor = r.checkpoints()[-1].entries + 1
    sent, reply = ask(q, r, eb.Subject("record", records[0].record_id), min_freshness=floor)
    assert reply.envelope is not None and reply.envelope.freshness == r.checkpoints()[-1].tree_size
    statement = q.record_response(sent.record_id, reply.message, keys_of(r)).header.statement
    assert statement["outcome"] == eb.OUTCOME_ARTIFACT, statement.get("failure")
    # A floor beyond the log is refused without any work.
    _, reply = ask(q, r, eb.Subject("record", records[0].record_id), min_freshness=r.size() + 10)
    assert reply.refusal.reason == "coverage_unsatisfiable"


def test_uniform_policy_and_limiter(clock):
    r = book(clock, "responder", responder=eb.ResponderPolicy(refusal_policy=eb.REFUSAL_POLICY_UNIFORM,
                                                              limiter=eb.WindowLimiter(1, timedelta(hours=1))))
    r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    r.checkpoint()
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(eb.Subject("record", "e" * 64), eb.Coverage(min_freshness=1))))
    assert r.respond(request, PEERS)[0].refusal.reason == "policy_declined"  # no_such_subject, folded
    assert r.respond(request, PEERS)[0].refusal.reason == "policy_declined"  # over the limit
    with pytest.raises(eb.InvalidError):
        book(clock, "x", responder=eb.ResponderPolicy(refusal_policy="loud"))


def test_a_recipient_specific_derivation_answers_only_its_recipient(clock):
    r = book(clock, "responder")
    for _ in range(2):
        r.append(eb.Entry("step_completed", eb.PRODUCER_CLAIM, subject_ref="deal-1"))
    r.checkpoint()
    definition = eb.DerivationDefinition(b'{"name":"basic/1"}', forms=("correlation",),
                                         fields=("record_type",), recipient="alice")
    derivation = r.register_derivation(definition)
    assert derivation == hashlib.sha256(b'{"name":"basic/1"}').hexdigest()
    assert r.register_derivation(definition) == derivation
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(eb.Subject("correlation", "deal-1"),
                                                              eb.Coverage(min_freshness=1), derivation=derivation)))
    artifact = json.loads(r.respond(request, eb.RespondOptions(authenticated_requester="alice"))[0].envelope.artifact)
    assert [sorted(entry) for entry in artifact["records"]] == [["digest", "leaf_index", "record_type"]] * 2
    assert r.respond(request, eb.RespondOptions(authenticated_requester="bob"))[0].refusal.reason == "not_authorized"
    with pytest.raises(eb.InvalidError, match="different"):
        r.register_derivation(eb.DerivationDefinition(definition.document, forms=("correlation",),
                                                      fields=("record_type",), recipient="bob"))
    for bad in (eb.DerivationDefinition(b"[]", ("correlation",), ("seq",)),
                eb.DerivationDefinition(b"{}", ("checkpoints",), ("seq",)),
                eb.DerivationDefinition(b"{}", ("correlation",), ("secret",)),
                eb.DerivationDefinition(b"{}", (), ("seq",))):
        with pytest.raises(eb.InvalidError):
            r.register_derivation(bad)


def test_a_lapsed_retention_commitment_answers_retention_expired(clock):
    r = book(clock, "responder")
    r.commit_retention(eb.Subject("record", "f" * 64), START + timedelta(seconds=2))
    r.checkpoint()
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(eb.Subject("record", "f" * 64), eb.Coverage(min_freshness=1))))
    assert r.respond(request, PEERS)[0].refusal.reason == "retention_expired"
    with pytest.raises(eb.InvalidError):
        r.commit_retention(eb.Subject("correlation", "x"), START)


# --- the book ---------------------------------------------------------------------


def test_a_checkpoint_commits_changed_index_roots_first(clock):
    b = book(clock)
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref="s"))
    first = b.checkpoint()
    assert [r.header.record_type for r in b.records()] == ["observation", "index_root", "index_root"]
    assert first.entries == 3 and b.checkpoint() == first, "an unchanged log is not checkpointed again"


def test_a_file_store_reopens_the_same_book(clock, tmp_path):
    b = book(clock, store=eb.FileStore(tmp_path))
    sent = b.request(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(min_freshness=1)))
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT, payloads=(b"octets",)))
    cp = b.checkpoint()
    b.close()
    with pytest.raises(eb.ReleasedError):
        b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    again = book(clock, store=eb.FileStore(tmp_path))
    assert [r.record_id for r in again.records()] == [r.record_id for r in b.records()]
    assert again.checkpoints() == [cp]
    again.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    assert again.checkpoint().prev_size == cp.tree_size
    clock.at += timedelta(minutes=5)
    again.record_absence(sent.record_id, clock.at - timedelta(minutes=1))
    again.close()


def test_a_file_store_truncates_a_torn_line_and_refuses_a_corrupt_one(clock, tmp_path):
    b = book(clock, store=eb.FileStore(tmp_path))
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.close()
    journal = tmp_path / "records.jsonl"
    whole = journal.read_bytes()
    journal.write_bytes(whole + b'{"seq":2')
    assert book(clock, store=eb.FileStore(tmp_path)).size() == 1
    assert journal.read_bytes() == whole
    journal.write_bytes(whole + b"not json\n")
    with pytest.raises(eb.CorruptError):
        eb.FileStore(tmp_path)


def test_a_store_of_another_book_or_a_tampered_record_is_refused(clock):
    store = eb.MemoryStore()
    book(clock, "one", store=store).append(eb.Entry("observation", eb.OBSERVED_EVENT))
    with pytest.raises(eb.CorruptError, match="belongs to book"):
        book(clock, "two", store=store)
    record = store.records()[0]
    store._records[record.record_id] = eb.StoredRecord(record.seq, record.record_id, record.header,
                                                       dict(record.capsule, operator="someone"), record.envelope)
    with pytest.raises(eb.CorruptError, match="commit"):
        book(clock, "one", store=store)


def test_entries_that_do_not_validate_are_refused(clock):
    b = book(clock)
    for entry in (eb.Entry("observation", "rumour"),
                  eb.Entry("observation", eb.OBSERVED_EVENT, links=(eb.Link("likes", "a" * 64),)),
                  eb.Entry("observation", eb.OBSERVED_EVENT, links=(eb.Link(eb.CITES, "short"),)),
                  eb.Entry("observation", eb.OBSERVED_EVENT, links=(eb.Link("adjudicates", "a" * 64),)),
                  eb.Entry("", eb.OBSERVED_EVENT)):
        with pytest.raises(eb.InvalidError):
            b.append(entry)
    assert b.size() == 0


def test_a_half_pin_answers_under_the_earliest_anchor_covering_the_citations(clock, responder):
    r, records = responder
    r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    r.checkpoint()
    q = book(clock, "requester")
    half = records[0].record_id
    sent, reply = ask(q, r, eb.Subject("exchange", half), expected_pin=half)
    assert reply.envelope.anchor_digest == half
    assert json.loads(reply.envelope.artifact)["anchor"] == r.checkpoints()[1].anchor_digest
    statement = q.record_response(sent.record_id, reply.message, keys_of(r)).header.statement
    assert statement["outcome"] == eb.OUTCOME_ARTIFACT and statement["not_checked"] == ["anchor_inclusion"]
    _, reply = ask(q, r, eb.Subject("exchange", "9" * 64), expected_pin="9" * 64)
    assert reply.refusal.reason == "no_such_subject"


def test_a_time_floor_states_the_anchors_time(clock, responder):
    r, records = responder
    q = book(clock, "requester")
    floor = eb.book.profile_time_ns(r.checkpoints()[-1].issued_at_ns)
    sent, reply = ask(q, r, eb.Subject("record", records[0].record_id), min_freshness=floor)
    assert reply.envelope.freshness == floor
    assert q.record_response(sent.record_id, reply.message, keys_of(r)).header.statement["outcome"] == "artifact"
    _, reply = ask(q, r, eb.Subject("record", records[0].record_id), min_freshness="2099-01-01T00:00:00Z")
    assert reply.refusal.reason == "coverage_unsatisfiable"


def test_a_passed_deadline_is_refused(clock, responder):
    r, records = responder
    request = eb.encode_request(eb.EvidenceRequest(eb.Subject("record", records[0].record_id),
                                                   eb.Coverage(min_freshness=1), deadline="2026-09-01T00:00:00Z"))
    assert r.respond(eb.Message(request), PEERS)[0].refusal.reason == "deadline_unmet"


def test_an_answer_over_the_item_cap_is_declined(clock):
    r = book(clock, "responder", responder=eb.ResponderPolicy(max_answer_items=1))
    first = r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    for _ in range(2):
        r.append(eb.Entry("observation", eb.OBSERVED_EVENT, links=(eb.Link(eb.CITES, first.record_id),)))
    r.checkpoint()
    request = eb.encode_request(eb.EvidenceRequest(eb.Subject("exchange", first.record_id), eb.Coverage(min_freshness=1)))
    assert r.respond(eb.Message(request), PEERS)[0].refusal.reason == "policy_declined"


def test_a_cbor_reply_is_received(clock, responder):
    r, records = responder
    q = book(clock, "requester")
    sent = q.request(eb.EvidenceRequest(eb.Subject("record", records[0].record_id),
                                        eb.Coverage(expected_pin=r.checkpoints()[-1].anchor_digest)))
    # The requester sends JSON; a responder may answer a CBOR request of the same map in CBOR.
    reply, _ = r.respond(eb.Message(sent.octets), PEERS)
    cbor = eb.request.encode(eb.MEDIA_TYPE_CBOR, reply.envelope.to_value())
    record = q.record_response(sent.record_id, eb.Message(cbor, eb.MEDIA_TYPE_CBOR), keys_of(r))
    assert record.header.statement["outcome"] == eb.OUTCOME_ARTIFACT


def test_range_and_full_history_are_refused_without_a_grant(clock, responder):
    r, _ = responder
    for subject in (eb.Subject("range", (0, 1)), eb.Subject("full_history")):
        request = eb.encode_request(eb.EvidenceRequest(subject, eb.Coverage(min_freshness=1)))
        assert r.respond(eb.Message(request), PEERS)[0].refusal.reason == "not_authorized"


def test_a_correlation_is_answered_per_record(clock):
    # An exact-key lookup, answered under the rule of a record or exchange
    # (Steven's Q1 amendment on #114). Go: TestCorrelationIsAnsweredPerRecord.
    r = book(clock, "responder")
    for who in ("alice", "bob"):
        r.append(eb.Entry("exchange", eb.OBSERVED_EVENT, subject_ref=f"deal-{who}", counterparty_ref=who))
        r.append(eb.Entry("exchange", eb.OBSERVED_EVENT, subject_ref=f"step-{who}", counterparty_ref=who,
                          correlation=eb.Correlation(exchange_id="deal-both")))
    r.checkpoint()

    def reason(who: str, tier: str, key: str) -> str | None:
        request = eb.Message(eb.encode_request(eb.EvidenceRequest(eb.Subject("correlation", key), eb.Coverage(min_freshness=1))))
        reply, _ = r.respond(request, eb.RespondOptions(authenticated_requester=who, policy=eb.SharePolicy(history_segments=tier)))
        return reply.refusal.reason if reply.refusal else None

    for tier in ("counterparties", "prospective"):
        assert reason("alice", tier, "deal-alice") is None and reason("bob", tier, "deal-bob") is None
        for who, key in (("alice", "deal-bob"), ("bob", "deal-alice"), ("alice", "deal-both"), ("bob", "deal-both"),
                         ("carol", "deal-alice"), ("carol", "deal-never")):
            assert reason(who, tier, key) == "not_authorized", (tier, who, key)
    assert reason("carol", "peers", "deal-both") is None
    assert reason("carol", "peers", "deal-") == "no_such_subject"  # the key matches exactly, not by prefix
    assert reason("alice", "off", "deal-alice") == "not_authorized"
    for tier in ("off", "counterparties", "prospective", "peers"):
        assert reason("", tier, "deal-alice") == "not_authorized"


# --- review regressions -----------------------------------------------------------


def test_a_returned_record_cannot_change_the_books(clock):
    b = book(clock)
    req = eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(min_freshness=1), nonce="n-1")
    sent = b.request(req)
    b.get(sent.record_id).header.statement["request"]["nonce"] = "other"
    b.records()[0].header.statement["request"]["nonce"] = "other"
    with pytest.raises(eb.InvalidError, match="nonce already used"):
        b.request(req)
    statement = {"k": ["v"]}
    record = b.append(eb.Entry("observation", eb.OBSERVED_EVENT, statement=statement))
    statement["k"].append("changed")
    record.header.statement["k"].append("changed")
    assert b.get(record.record_id).header.statement == {"k": ["v"]}


def test_a_failed_journal_write_stops_further_writes(clock, tmp_path, monkeypatch):
    store = eb.FileStore(tmp_path)
    b = book(clock, store=store)
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))

    def fail(fd):
        raise OSError("disk")

    monkeypatch.setattr(eb.store.os, "fsync", fail)
    with pytest.raises(OSError):
        b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    monkeypatch.undo()
    with pytest.raises(eb.CorruptError, match="reopen"):
        b.append(eb.Entry("observation", eb.OBSERVED_EVENT))


def test_a_request_with_an_unpaired_surrogate_is_malformed(clock, responder):
    r, _ = responder
    size = r.size()
    for octets in (b'{"coverage":{"min_freshness":1},"ignored":"\\ud800","subject":{"checkpoints":null}}',
                   b'{"coverage":{},"ignored":"\\ud800","subject":{"checkpoints":null}}'):
        reply, record = r.respond(eb.Message(octets), PEERS)
        assert reply.refusal.reason == "request_malformed" and record is None
    assert r.size() == size


def signed_record_set(r: eb.Book, q: eb.Book, records, edit) -> tuple[eb.SentRequest, eb.Message]:
    sent, reply = ask(q, r, eb.Subject("exchange", records[0].record_id))
    envelope = json.loads(reply.message.octets)
    artifact = edit(base64.urlsafe_b64decode(envelope["artifact"] + "=="))
    envelope["artifact"] = base64.urlsafe_b64encode(artifact).rstrip(b"=").decode()
    envelope["artifact_digest"] = hashlib.sha256(artifact).hexdigest()
    return sent, eb.Message(resign(envelope, key("responder signer")))


@pytest.mark.parametrize("edit,check", [
    # A duplicate member: the last anchor is the envelope's.
    (lambda a: a[:1] + b'"anchor":"' + b"0" * 64 + b'",' + a[1:], "not a JSON object"),
    # Hex with whitespace in a record body.
    (lambda a: a.replace(b'"body":"', b'"body":" ', 1), "does not hash"),
])
def test_a_signed_record_set_go_refuses_is_received_and_failed(clock, responder, edit, check):
    r, records = responder
    q = book(clock, "requester")
    sent, reply = signed_record_set(r, q, records, edit)
    statement = q.record_response(sent.record_id, reply, keys_of(r)).header.statement
    assert statement["outcome"] == eb.OUTCOME_ARTIFACT_FAILED and check in statement["failure"]



def test_inputs_go_refuses_or_accepts_are_read_as_go_reads_them(clock, tmp_path):
    with pytest.raises(eb.InvalidError):
        book(clock).register_derivation(eb.DerivationDefinition(b'{"a":1,"a":2}', ("correlation",), ("seq",)))
    book(clock, "x", responder=eb.ResponderPolicy(refusal_policy=""))
    with pytest.raises(eb.InvalidError):
        eb.Book("b", " ", signer=key("s"), log_id="l", log_key=key("l"))
    with pytest.raises(eb.InvalidError):
        eb.encode_request(eb.EvidenceRequest(eb.Subject("range", (0, 1 << 60)), eb.Coverage(min_freshness=1)))
    seed = hashlib.sha256(b"s").digest()
    signer = eb.Ed25519Signer(seed)
    assert eb.Ed25519Signer(seed + signer.public_key()).public_key() == signer.public_key()
    with pytest.raises(eb.InvalidError):
        eb.Ed25519Signer(seed + bytes(32))
    b = book(clock, store=eb.FileStore(tmp_path))
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.close()
    assert (tmp_path / "records.jsonl").stat().st_mode & 0o777 == 0o600


def test_a_malformed_stored_header_or_index_root_is_corruption(clock):
    store = eb.MemoryStore()
    b = book(clock, store=store)
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.checkpoint()
    for i, header in ((0, b'{"links":7}'), (1, None)):
        stored = store.records()[i]
        changed = store.records()
        if header is None:  # an index_root whose statement is not an index root
            value = json.loads(stored.header)
            value["statement"] = "root"
            header = jcs(value)
        broken = eb.MemoryStore()
        for s in changed:
            broken._records[s.record_id] = s if s is not stored else eb.StoredRecord(
                s.seq, s.record_id, header, s.capsule, s.envelope)
        with pytest.raises(eb.CorruptError):
            book(clock, store=broken)


# --- a request another tool sent (evidencebook#245, #247) ----------------------------

LIVE16 = Path(__file__).resolve().parents[2] / "testdata" / "live16-ask"
LIVE16_KEY = "1dd25d9938ba4841f5e127e39e8dcc71df82b2072d9186ece75e431063ab26d9"


def live16(name: str) -> dict:
    return json.loads((LIVE16 / f"{name}.json").read_text())


@pytest.mark.parametrize("name,profile", [("checkpoints", eb.PROFILE), ("history_card", eb.PROFILE),
                                          ("exchange", eb.PROFILE_V1)])
def test_live16_requests_are_recorded_as_sent_and_their_replies_verify(clock, name, profile):
    b = book(clock)
    fixture = live16(name)
    octets = jcs(fixture["request"])
    for member in ('"audience":', '"requester_auth":', '"requester_id":', f'"profile":"{profile}"'):
        assert member.encode() in octets
    sent = b.request_sent(eb.Message(octets, eb.MEDIA_TYPE_JSON), "stevenmih-1")
    assert sent.octets == octets and sent.digest == fixture["reply"]["request_digest"]
    assert b.get(sent.record_id).header.statement == {
        "request": fixture["request"], "responder_ref": "stevenmih-1", "composed_elsewhere": True}
    reply = eb.Message(json.dumps(fixture["reply"]).encode(), eb.MEDIA_TYPE_JSON)
    statement = b.record_response(sent.record_id, reply, eb.ResponderKeys(LIVE16_KEY, LIVE16_KEY)).header.statement
    assert statement["outcome"] == eb.OUTCOME_ARTIFACT and "failure" not in statement


def test_request_sent_refuses_what_was_not_sent(clock):
    b = book(clock)
    ok = '{"audience":"a","coverage":{"min_freshness":1},"nonce":"n1","subject":{"checkpoints":null}}'
    two = '{"coverage":{"expected_pin":"' + "ab" * 32 + '","min_freshness":1},"subject":{"checkpoints":null}}'
    for name, octets, media_type in [
        ("CBOR", ok, eb.MEDIA_TYPE_CBOR),
        ("not JCS", '{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}', eb.MEDIA_TYPE_JSON),
        ("whitespace", '{"coverage": {"min_freshness":1},"subject":{"checkpoints":null}}', eb.MEDIA_TYPE_JSON),
        ("no subject", '{"coverage":{"min_freshness":1}}', eb.MEDIA_TYPE_JSON),
        ("two coverages", two, eb.MEDIA_TYPE_JSON),
        ("unknown profile", '{"coverage":{"min_freshness":1},"profile":"evidence-interop/9",'
                            '"subject":{"checkpoints":null}}', eb.MEDIA_TYPE_JSON),
        ("not JSON", '{"subject":', eb.MEDIA_TYPE_JSON),
    ]:
        with pytest.raises(eb.InvalidError):
            b.request_sent(eb.Message(octets.encode(), media_type), "responder")
    assert b.size() == 0
    b.request_sent(eb.Message(ok.encode()), "responder")
    reused = '{"coverage":{"min_freshness":2},"nonce":"n1","subject":{"checkpoints":null}}'
    with pytest.raises(eb.InvalidError, match="nonce already used"):
        b.request_sent(eb.Message(reused.encode()), "responder")
    with pytest.raises(eb.InvalidError, match="nonce already used"):
        b.request(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(min_freshness=1), nonce="n1"))
    no_nonce = '{"coverage":{"min_freshness":1},"subject":{"checkpoints":null}}'
    for _ in range(2):
        b.request_sent(eb.Message(no_nonce.encode()), "responder")
    assert b.size() == 3
    # A request the book composed is not marked.
    made = b.request(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(min_freshness=1)))
    assert "composed_elsewhere" not in b.get(made.record_id).header.statement


def test_a_refusal_reason_conforms_under_the_profile_the_request_asked_for(clock):
    r = book(clock, "responder")
    r.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref="order-0"))
    r.checkpoint()
    q = book(clock, "requester")
    for profile, want in [(eb.PROFILE_V1, eb.OUTCOME_REFUSAL), (eb.PROFILE, eb.OUTCOME_REFUSAL_NONCONFORMING)]:
        octets = ('{"coverage":{"min_freshness":1},"nonce":"%s","profile":"%s","subject":{"checkpoints":null}}'
                  % (profile, profile)).encode()
        sent = q.request_sent(eb.Message(octets), "responder")
        refusal = sign_refusal(sent.digest, "capacity_exceeded", signer=EvidenceSigner(r._signer),
                                  issued_at="2026-10-31T12:00:00Z")
        reply = eb.request.encode_refusal(refusal, eb.MEDIA_TYPE_JSON)
        statement = q.record_response(sent.record_id, reply.message, keys_of(r)).header.statement
        assert statement["outcome"] == want and statement["reason"] == "capacity_exceeded"


def test_a_record_set_body_is_the_preimage_or_the_ledger_capsule():
    from evidencebook.book import _digest, _record_body_matches
    from evidencebook._capsule import capsule_preimage

    artifact = json.loads(base64.b64decode(live16("exchange")["reply"]["artifact"]))
    assert len(artifact["records"]) == 1
    entry = artifact["records"][0]
    capsule = bytes.fromhex(entry["body"])
    digest = entry["digest"]
    preimage = capsule_preimage(json.loads(capsule), digest)
    assert _record_body_matches(capsule, digest) and _record_body_matches(preimage, digest)
    changed = capsule.replace(b'"action_type":"fyi"', b'"action_type":"fyj"', 1)
    assert changed != capsule
    other = "ab" * 32
    for body, d in [(changed, digest), (capsule, other), (preimage, other), (b"not json", digest)]:
        assert not _record_body_matches(body, d)
    assert _digest(preimage) == digest


def test_a_reproduction_marker_round_trips_in_the_header():
    from evidencebook.record import Header

    value = {"v": 1, "book_id": "b", "seq": 1, "record_type": "r", "epistemic_type": "observed_event",
             "committed_at": "2026-10-31T00:00:00Z", "links": [], "reproduction": {"clock": "caller"}}
    header = Header.from_value(value)
    assert header.reproduction == "caller"
    assert header.value() == value
    del value["reproduction"]
    assert Header.from_value(value).value() == value
