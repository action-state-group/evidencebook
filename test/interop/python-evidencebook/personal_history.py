# SPDX-License-Identifier: Apache-2.0
"""The Personal-Agent History Profile v0 positive run, produced with this
repository's Python package instead of Go's Book.Respond.

    python personal_history.py [--correlated] > responder-run.json

It makes exactly the calls test/interop/personal-history/main.go makes, in
the same order, against a fresh in-memory book with the same keys and the
same clock, and writes the same JSON. The corpus pins that run's SHA-256
(vectors/personal-history-basic-v0/manifest.json responder.run_sha256), so
an equal hash means every record, checkpoint, derivation and reply this
package produced is Go's, byte for byte.

The keys are test keys. Never use them for anything else.
"""
from __future__ import annotations

import argparse
import hashlib
import sys
from datetime import datetime, timedelta, timezone

from agent_action_capsule.canonical import jcs

from _gojson import marshal_indent
import evidencebook as eb

HOLDER_LABEL = "personal-agent-history-profile-v0 conformance vectors: holder key"
LOG_LABEL = "personal-agent-history-profile-v0 conformance vectors: holder log key"
LOG_ID = "holder-stream-1"
RECIPIENT = "recipient-1"
OTHER = "requester-2"
PROFILE = "evidence-interop/0"
START = datetime(2026, 10, 15, tzinfo=timezone.utc)

BASIC_READS = ["committed_at", "epistemic_type", "event_time_claim", "record_type"]
WIDE_READS = ["committed_at", "counterparty_ref", "epistemic_type", "event_time_claim", "record_type", "statement"]


def deal_ref(n: int) -> str:
    return hashlib.sha256(f"personal-agent-history-profile-v0 conformance vectors: deal {n}".encode()).hexdigest()[:32]


def seeded(label: str) -> eb.Ed25519Signer:
    return eb.Ed25519Signer(hashlib.sha256(label.encode()).digest())


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--correlated", action="store_true",
                        help="file each step under correlation.exchange_id, as capsule-cli#264 does")
    correlated = parser.parse_args().correlated

    clock = [START]

    def now() -> datetime:
        clock[0] += timedelta(seconds=1)
        return clock[0]

    signer = seeded(HOLDER_LABEL)
    book = eb.Book("holder", "holder-operator", signer=signer, log_id=LOG_ID, log_key=seeded(LOG_LABEL), now=now)

    d = [deal_ref(1), deal_ref(2), deal_ref(3), deal_ref(4)]
    minutes = lambda m: timedelta(minutes=m)  # noqa: E731
    steps = [
        (d[0], "step_completed", minutes(40)), (d[1], "step_completed", minutes(35)),
        (d[3], "step_completed", minutes(30)), (d[0], "step_completed", minutes(25)),
        (d[2], "step_completed", minutes(20)), (d[3], "step_failed", minutes(15)),
        (d[1], "step_failed", minutes(10)), (d[3], "step_completed", minutes(5)),
    ]
    out: dict = {
        "note": "Test keys from public labels; never use them for anything else.",
        "holder_key": signer.public_key().hex(),
        "recipient": RECIPIENT,
        "deals": d,
        "selection": sorted([d[0], d[1], d[2]]),
        "records": [],
        "checkpoints": [],
        "definitions": {},
        "exchanges": [],
    }
    numbered: dict[str, int] = {}
    for i, (deal, kind, claimed) in enumerate(steps):
        subject_ref, correlation = deal, eb.Correlation()
        if correlated:
            numbered[deal] = numbered.get(deal, 0) + 1
            subject_ref, correlation = f"{deal}/{numbered[deal]}", eb.Correlation(exchange_id=deal)
        record = book.append(eb.Entry(kind, eb.PRODUCER_CLAIM, subject_ref=subject_ref, correlation=correlation,
                                      event_time_claim=clock[0] + timedelta(seconds=1) - claimed))
        row = {"digest": record.record_id, "deal": deal}
        if correlated:
            row["subject_ref"] = subject_ref
        row["record_type"] = kind
        out["records"].append(row)
        if i in (5, 7):
            cp = book.checkpoint()
            out["checkpoints"].append({
                "expected_pin": cp.anchor_digest, "tree_size": cp.tree_size,
                "log_key": {"iss": cp.log_id, "sub": f"{cp.log_id}#{cp.tree_size}", "kid": cp.key_id},
            })

    def define(name: str, reads: list[str]) -> str:
        text = jcs({"completeness": "not_asserted", "kind": "projection", "name": "personal_history_basic/1",
                    "reads": reads, "recipient": RECIPIENT, "selection": out["selection"], "subject_form": "correlation"})
        out["definitions"][name] = text.decode("utf-8")
        return book.register_derivation(eb.DerivationDefinition(text, forms=("correlation",), fields=tuple(reads),
                                                                recipient=RECIPIENT))

    basic, wide = define("basic", BASIC_READS), define("wide_reads", WIDE_READS)
    pin_a, pin_b = out["checkpoints"][0]["expected_pin"], out["checkpoints"][1]["expected_pin"]

    def ask(name: str, requester: str, deal: str, derivation: str, pin: str) -> None:
        text = jcs({"coverage": {"expected_pin": pin}, "derivation": derivation, "nonce": f"{requester}/{name}",
                    "profile": PROFILE, "subject": {"correlation": deal}})
        reply, _ = book.respond(eb.Message(text, eb.MEDIA_TYPE_JSON),
                                eb.RespondOptions(authenticated_requester=requester, policy=eb.SharePolicy()))
        out["exchanges"].append({
            "name": name, "requester": requester,
            "request": {"media_type": eb.MEDIA_TYPE_JSON, "octets_text": text.decode("utf-8")},
            "reply": {"media_type": reply.message.media_type, "octets_text": reply.message.octets.decode("utf-8")},
        })

    for i, deal in enumerate(out["selection"]):
        ask(f"history-{i + 1}", RECIPIENT, deal, basic, pin_b)
    for name, requester, deal, derivation, pin in (("not-selected", RECIPIENT, d[3], basic, pin_b),
                                                   ("other-anchor", RECIPIENT, d[0], basic, pin_a),
                                                   ("other-requester", OTHER, d[0], basic, pin_b)):
        ask(name, requester, deal, derivation, pin)
    for i, deal in enumerate(out["selection"]):
        ask(f"wide-reads-{i + 1}", RECIPIENT, deal, wide, pin_b)
    sys.stdout.buffer.write(marshal_indent(out, "  ") + b"\n")


if __name__ == "__main__":
    main()
