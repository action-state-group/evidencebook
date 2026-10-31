# SPDX-License-Identifier: Apache-2.0
"""The responder side of the evidence interoperability profile's conformance
vectors, answered by this repository's Python package.

    python responder.py [--profile evidence-interop/1] <dir> <out.json>

It makes the calls test/interop/responder/main.go makes, in the same order,
with the same keys and clock, and writes the same members of its output for
test/interop/python-profile/verify_responder.py to check. The package
answers over the native binding only, so each request Go sends over A2A or
MCP is answered natively here (the A2A and MCP handlers make that one
Respond call), the book's state stays Go's, and the output carries no
carriage, manifest, Agent Card or MCP members: the verifier skips the
carriage cases. Every reply it does write is the bytes Go's run writes.

Every request names --profile, evidence-interop/0 by default, as Go's
-profile does: under /1 the cost-policy refusal is the capacity-exceeded
exchange and declined is a uniform-policy refusal.

The keys are test keys. Never use them for anything else.
"""
from __future__ import annotations

import hashlib
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

from _gojson import marshal_indent
import evidencebook as eb

RESPONDER_LABEL = "draft-mih-agent-evidence-request-00 conformance vectors: primary responder key"
LOG_LABEL = "evidence-interop/0 worked example: CLL log key"
LOG_ID = "evidence-stream-1"


def seeded(label: str) -> eb.Ed25519Signer:
    return eb.Ed25519Signer(hashlib.sha256(label.encode()).digest())


class _LimitNone:
    def allow(self, requester: str, now: datetime) -> bool:
        return False


def carry(m: eb.Message) -> dict:
    """Octets as the vectors write them: JSON as text, CBOR as hex."""
    if m.media_type == eb.MEDIA_TYPE_JSON:
        return {"media_type": m.media_type, "octets_text": m.octets.decode("utf-8")}
    return {"media_type": m.media_type, "octets_hex": m.octets.hex()}


def main(directory: str, out_path: str, profile: str = eb.PROFILE) -> None:
    out_dir = Path(directory)
    clock = [datetime(2026, 10, 12, tzinfo=timezone.utc)]

    def now() -> datetime:
        clock[0] += timedelta(seconds=1)
        return clock[0]

    def open_book(book_id: str, policy: eb.ResponderPolicy | None = None) -> eb.Book:
        return eb.Book(book_id, f"{book_id}-operator", signer=seeded(RESPONDER_LABEL), log_id=LOG_ID,
                       log_key=seeded(LOG_LABEL), now=now, responder=policy)

    book = open_book("responder")
    records = [book.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref=f"order-{i}")) for i in range(3)]
    earlier = book.checkpoint()
    # The later records cite the third, so the exchange of the third is a record set.
    for i in range(3, 7):
        records.append(book.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref=f"order-{i}",
                                            links=(eb.Link(eb.CITES, records[2].record_id),))))
    anchor = book.checkpoint()
    pin = anchor.anchor_digest
    # The vectors' requesters are no record's counterparty: answer under the peers tier.
    peers = eb.SharePolicy(history_segments="peers")
    subject = eb.Subject("record", records[2].record_id)

    def request(s: eb.Subject, pin: str, nonce: str, profile: str, media_type: str) -> eb.Message:
        req = eb.EvidenceRequest(s, eb.Coverage(expected_pin=pin), nonce=nonce, profile=profile)
        return eb.Message(eb.encode_request(req, media_type), media_type)

    def respond(m: eb.Message, requester: str, on: eb.Book = book) -> eb.Reply:
        return on.respond(m, eb.RespondOptions(authenticated_requester=requester, policy=peers))[0]

    out: dict = {} if profile == eb.PROFILE else {"profile": profile}
    out |= {
        "responder_key": seeded(RESPONDER_LABEL).public_key().hex(),
        "log_keys": [{"iss": cp.log_id, "kid": cp.key_id, "sub": f"{cp.log_id}#{cp.tree_size}"}
                     for cp in book.checkpoints()],
        "exchanges": {},
        "invariance_artifacts_hex": {},
        "invariance_record_set_artifacts_hex": {},
        "other_requests": {},
    }
    exchanges = out["exchanges"]

    # Request C (JSON) by alice and request D (CBOR, another nonce) by bob.
    req_c = request(subject, pin, "2026-10-12T00:00:10Z/c", profile, eb.MEDIA_TYPE_JSON)
    req_d = request(subject, pin, "2026-10-12T00:00:20Z/d", profile, eb.MEDIA_TYPE_CBOR)
    reply_c, reply_d = respond(req_c, "alice"), respond(req_d, "bob")
    if reply_c.envelope is None or reply_d.envelope is None:
        raise SystemExit("requests C and D were refused")
    exchanges["valid-exchange"] = {"requests": [carry(req_c)], "replies": [carry(reply_c.message)]}
    exchanges["invariance-two-nonces-two-transports"] = {
        "requests": [carry(req_c), carry(req_d)], "replies": [carry(reply_c.message), carry(reply_d.message)]}
    out["invariance_artifacts_hex"]["alice-json-nonce-c"] = reply_c.envelope.artifact.hex()
    out["invariance_artifacts_hex"]["bob-cbor-nonce-d"] = reply_d.envelope.artifact.hex()
    (out_dir / "artifact-alice-json-nonce-c.bin").write_bytes(reply_c.envelope.artifact)
    (out_dir / "artifact-bob-cbor-nonce-d.bin").write_bytes(reply_d.envelope.artifact)

    # Request R, request C in CBOR: Go sends it natively, over MCP and over A2A.
    r = request(subject, pin, "2026-10-12T00:00:10Z/c", profile, eb.MEDIA_TYPE_CBOR)
    for _ in range(3):
        if respond(r, "alice").envelope is None:
            raise SystemExit("request R was refused")

    # The same two variations for a record-set subject: an exchange.
    for name, nonce, media_type, requester in (
            ("exchange-alice-json-nonce-c", "2026-10-12T00:00:10Z/c", eb.MEDIA_TYPE_JSON, "alice"),
            ("exchange-bob-cbor-nonce-d", "2026-10-12T00:00:20Z/d", eb.MEDIA_TYPE_CBOR, "bob")):
        reply = respond(request(eb.Subject("exchange", records[2].record_id), pin, nonce, profile, media_type),
                        requester)
        if reply.envelope is None:
            raise SystemExit(f"exchange request {name} was refused")
        out["invariance_record_set_artifacts_hex"][name] = reply.envelope.artifact.hex()

    # A record this book does not hold: no_such_subject.
    missing = request(eb.Subject("record", hashlib.sha256(b"never recorded").hexdigest()), pin,
                      "2026-10-12T00:00:10Z/c", profile, eb.MEDIA_TYPE_JSON)
    exchanges["missing-evidence-no-such-subject"] = {"requests": [carry(missing)],
                                                     "replies": [carry(respond(missing, "alice").message)]}

    # A profile this book does not implement: request_malformed.
    v9 = request(subject, pin, "2026-10-12T00:00:40Z/v9", "evidence-interop/9", eb.MEDIA_TYPE_JSON)
    exchanges["unsupported-version-refused"] = {"requests": [carry(v9)], "replies": [carry(respond(v9, "alice").message)]}

    # The requests the envelope mutations are paired with.
    out["other_requests"]["another-record"] = carry(request(eb.Subject("record", records[1].record_id), pin,
                                                            "2026-10-12T00:00:10Z/c", profile, eb.MEDIA_TYPE_JSON))
    out["other_requests"]["another-pin"] = carry(request(subject, earlier.anchor_digest, "2026-10-12T00:00:10Z/c",
                                                         profile, eb.MEDIA_TYPE_JSON))

    # The same request C to a responder whose cost policy declines it.
    declining = open_book("responder", eb.ResponderPolicy(limiter=_LimitNone()))
    declined = {"requests": [carry(req_c)], "replies": [carry(respond(req_c, "alice", declining).message)]}
    if profile == eb.PROFILE_V1:
        # Under /1 a limit is capacity_exceeded; policy_declined is policy:
        # a uniform-policy book that holds no such record.
        exchanges["capacity-exceeded"] = declined
        uniform = open_book("responder", eb.ResponderPolicy(refusal_policy=eb.REFUSAL_POLICY_UNIFORM))
        declined = {"requests": [carry(req_c)], "replies": [carry(respond(req_c, "alice", uniform).message)]}
    exchanges["declined"] = declined

    # A requester's own absence record: request C sent from another book, no reply in a one-minute window.
    requester = open_book("responder")

    def ask(nonce: str, route: str) -> eb.SentRequest:
        return requester.request(eb.EvidenceRequest(subject, eb.Coverage(expected_pin=pin), nonce=nonce, route=route),
                                 "responder")

    def absence_after_window(sent: eb.SentRequest) -> dict:
        clock[0] += timedelta(minutes=2)
        absence = requester.record_absence(sent.record_id, clock[0] - timedelta(seconds=1))
        return carry(eb.Message(absence_statement(absence), eb.MEDIA_TYPE_JSON))

    sent = ask("2026-10-12T00:00:10Z/c", "peer:example-responder")
    exchanges["missing-evidence-recorded-absence"] = {"requests": [carry(sent.message)], "replies": [],
                                                      "absence_record": absence_after_window(sent)}
    # Go sends the next two requests over MCP and A2A, then records their absences.
    via_mcp = ask("2026-10-12T00:00:30Z/m", "mcp:example-responder")
    respond(via_mcp.message, "alice")
    absence_after_window(via_mcp)
    via_a2a = ask("2026-10-12T00:00:30Z/a", "a2a:example-responder")
    respond(via_a2a.message, "alice")
    absence_after_window(via_a2a)

    # Go writes its maps in key order.
    for name in ("exchanges", "invariance_artifacts_hex", "invariance_record_set_artifacts_hex", "other_requests"):
        out[name] = dict(sorted(out[name].items()))
    Path(out_path).write_bytes(marshal_indent(out, " "))


def absence_statement(record: eb.Record) -> bytes:
    from agent_action_capsule.canonical import jcs

    return jcs(record.header.statement)


if __name__ == "__main__":
    args = sys.argv[1:]
    chosen = eb.PROFILE
    if args[:1] == ["--profile"] and len(args) >= 2:
        chosen, args = args[1], args[2:]
    if len(args) != 2:
        raise SystemExit("usage: responder.py [--profile evidence-interop/1] <dir> <out.json>")
    main(args[0], args[1], chosen)
