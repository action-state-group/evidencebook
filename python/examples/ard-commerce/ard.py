# SPDX-License-Identifier: Apache-2.0
"""The demo's ARD catalogue: one ``/.well-known/ard.json`` with an entry per seller, and the
sellers' signed ``evidence-commitment/1`` documents beside it.

Each entry carries the commitment pointer the carrier spec defines (agent-action-capsule
``spec/bindings/evidence-commitment-carriers-v0.md`` §5.1: one ``trustManifest.attestations[]`` entry
with the commitment's profile, URI, media type and digest, and the ``evidence-request`` tag) and the
offer in ``x-demo-offer``, a member of this demo only. The entry is a pointer: the lookup fetches the
commitment, checks its digest as a carrier fact and verifies its signature, which is what it trusts
(§4, V0 to V4)."""
from __future__ import annotations

import hashlib
import json
import urllib.request
from typing import Any

import commitment as cm

PROFILE = "evidence-commitment/1"
WELL_KNOWN = "/.well-known/ard.json"
DESCRIPTION = "Answers evidence requests about its own records; verify the commitment, not this entry"


def commitment_octets(doc: dict) -> bytes:
    """The commitment as served: the octets the digest is taken over (§4)."""
    return json.dumps(doc, sort_keys=True, indent=2).encode()


def catalogue(sellers: dict[str, dict], commitments: dict[str, dict], base_url: str) -> dict:
    """The ard.json document for ``sellers`` (name to label and price), their commitments served
    under ``base_url``."""
    entries = []
    for name, seller in sellers.items():
        octets = commitment_octets(commitments[name])
        entries.append({
            "name": seller["label"], "id": name,
            "description": f"{seller['label']}: conference badges",
            "tags": ["evidence-request", "print"],
            "trustManifest": {"identity": commitments[name]["key_id"], "attestations": [{
                "type": PROFILE, "uri": f"{base_url}/commitments/{name}.json", "mediaType": "application/json",
                "digest": "sha256:" + hashlib.sha256(octets).hexdigest(), "description": DESCRIPTION}]},
            "x-demo-offer": {"item": seller["item"], "price_msat": seller["price_msat"], "currency": "msat"},
        })
    return {"ard": "0", "entries": entries}


def fetch(url: str) -> bytes:
    with urllib.request.urlopen(url, timeout=10) as r:  # a demo-local server
        return r.read()


def lookup(base_url: str) -> list[dict]:
    """Maya's catalogue lookup: each entry with its commitment fetched and checked. An entry whose
    commitment fails a check is returned with ``problem`` set and no commitment."""
    doc = json.loads(fetch(base_url + WELL_KNOWN))
    found = []
    for entry in doc["entries"]:
        att = next(a for a in entry["trustManifest"]["attestations"] if a["type"] == PROFILE)
        item: dict[str, Any] = {"id": entry["id"], "name": entry["name"], "offer": entry["x-demo-offer"],
                                "identity": entry["trustManifest"]["identity"], "uri": att["uri"]}
        octets = fetch(att["uri"])
        item["digest_matches"] = att["digest"] == "sha256:" + hashlib.sha256(octets).hexdigest()
        try:
            doc_c = json.loads(octets)
            cm.verify(doc_c)
            if doc_c["key_id"] != item["identity"]:
                raise ValueError("the commitment's key is not the entry's identity")
            item["commitment"] = doc_c
        except Exception as exc:
            item["problem"] = f"commitment: {exc}"
        found.append(item)
    return found
