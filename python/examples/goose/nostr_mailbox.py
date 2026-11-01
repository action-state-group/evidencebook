# SPDX-License-Identifier: Apache-2.0
"""The Nostr mailbox carriage of agent-action-capsule's ``spec/bindings/nostr-mailbox-v0.md``
(experimental), as far as the Goose node needs it: build and check NIP-01 events
signed with BIP-340, publish them to a relay, and poll a relay for replies.

This is not a conforming use of the binding: the binding's reply carries an
evidence envelope or refusal (§4.2), and here a reply carries a referee's Result
(``{"result", "capsule"}`` JSON), so what is used is the binding's event kinds,
addressing and reply checks, with a payload the binding does not define. An extension
of the binding for it is tracked in action-state-group/agent-action-capsule (see the
Goose README).

Both parties connect outward; the relay only moves bytes. A request is a kind
4700 event addressed to the mailbox key by a ``p`` tag; a reply is a kind 4701
event whose ``e`` tag names the request event (binding §4). ``content`` is the
carried octets in base64url without padding. The checks of binding §4.2 decide
whether an event is a reply; a reply by content is not used here, because what
the Goose node waits for, a referee's Result, is attributed by the mailbox key
the deal terms name and not by an envelope under the evidence protocol.

Nothing here is trusted: a Result that arrives is verified by the node (the
referee's own check) before it reaches a book, and a relay that drops, delays
or alters an event leaves the request pending until its deadline. The relay
choice and the mailbox key come from the deal terms both halves commit to."""
from __future__ import annotations

import base64
import hashlib
import json
import os
import time
from dataclasses import dataclass
from typing import Any, Callable, Iterable

__all__ = [
    "REQUEST_KIND", "REPLY_KIND", "MailboxError", "Event", "public_key", "new_secret", "sign_event",
    "check_event", "encode", "decode", "nprofile", "Relay", "publish_all", "poll_replies", "serve_round",
    "schnorr_sign", "schnorr_verify",
]

REQUEST_KIND, REPLY_KIND = 4700, 4701

# -- BIP-340 (the reference algorithm, over secp256k1) ------------------------------------------

_P = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F
_N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
_G = (0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798,
      0x483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8)

_Point = tuple[int, int] | None


def _add(a: _Point, b: _Point) -> _Point:
    if a is None:
        return b
    if b is None:
        return a
    if a[0] == b[0] and a[1] != b[1]:
        return None
    if a == b:
        lam = 3 * a[0] * a[0] * pow(2 * a[1], _P - 2, _P) % _P
    else:
        lam = (b[1] - a[1]) * pow(b[0] - a[0], _P - 2, _P) % _P
    x = (lam * lam - a[0] - b[0]) % _P
    return x, (lam * (a[0] - x) - a[1]) % _P


def _mul(point: _Point, k: int) -> _Point:
    result: _Point = None
    while k:
        if k & 1:
            result = _add(result, point)
        point = _add(point, point)
        k >>= 1
    return result


def _tagged(tag: str, data: bytes) -> bytes:
    t = hashlib.sha256(tag.encode()).digest()
    return hashlib.sha256(t + t + data).digest()


def _lift_x(x: int) -> _Point:
    if x >= _P:
        return None
    y_sq = (pow(x, 3, _P) + 7) % _P
    y = pow(y_sq, (_P + 1) // 4, _P)
    if pow(y, 2, _P) != y_sq:
        return None
    return x, y if y % 2 == 0 else _P - y


def public_key(secret: bytes) -> str:
    """The x-only public key of ``secret``, 64 lowercase hex."""
    d = int.from_bytes(secret, "big")
    if not 1 <= d < _N:
        raise MailboxError("not a secp256k1 secret key")
    point = _mul(_G, d)
    assert point is not None
    return point[0].to_bytes(32, "big").hex()


def new_secret() -> bytes:
    """A fresh secret key (a request's carriage key is fresh per request, binding §8)."""
    while True:
        secret = os.urandom(32)
        if 1 <= int.from_bytes(secret, "big") < _N:
            return secret


def schnorr_sign(msg: bytes, secret: bytes, aux: bytes | None = None) -> bytes:
    d0 = int.from_bytes(secret, "big")
    if not 1 <= d0 < _N or len(msg) != 32:
        raise MailboxError("not a 32-byte message and a secp256k1 secret key")
    point = _mul(_G, d0)
    assert point is not None
    d = d0 if point[1] % 2 == 0 else _N - d0
    aux = os.urandom(32) if aux is None else aux
    t = (d ^ int.from_bytes(_tagged("BIP0340/aux", aux), "big")).to_bytes(32, "big")
    px = point[0].to_bytes(32, "big")
    k0 = int.from_bytes(_tagged("BIP0340/nonce", t + px + msg), "big") % _N
    if k0 == 0:
        raise MailboxError("nonce is zero")
    r = _mul(_G, k0)
    assert r is not None
    k = k0 if r[1] % 2 == 0 else _N - k0
    rx = r[0].to_bytes(32, "big")
    e = int.from_bytes(_tagged("BIP0340/challenge", rx + px + msg), "big") % _N
    return rx + ((k + e * d) % _N).to_bytes(32, "big")


def schnorr_verify(msg: bytes, pub: bytes, sig: bytes) -> bool:
    if len(msg) != 32 or len(pub) != 32 or len(sig) != 64:
        return False
    point = _lift_x(int.from_bytes(pub, "big"))
    r, s = int.from_bytes(sig[:32], "big"), int.from_bytes(sig[32:], "big")
    if point is None or r >= _P or s >= _N:
        return False
    e = int.from_bytes(_tagged("BIP0340/challenge", sig[:32] + pub + msg), "big") % _N
    result = _add(_mul(_G, s), _mul(point, _N - e))
    return result is not None and result[1] % 2 == 0 and result[0] == r


# -- NIP-01 events ------------------------------------------------------------------------------


class MailboxError(Exception):
    """A transport condition: a relay that refused or could not be reached, or bad input.
    Never an outcome of the evidence exchange (binding §5.2)."""


@dataclass(frozen=True)
class Event:
    id: str
    pubkey: str
    created_at: int
    kind: int
    tags: tuple[tuple[str, ...], ...]
    content: str
    sig: str

    def to_json(self) -> dict:
        return {"id": self.id, "pubkey": self.pubkey, "created_at": self.created_at, "kind": self.kind,
                "tags": [list(t) for t in self.tags], "content": self.content, "sig": self.sig}

    @classmethod
    def from_json(cls, value: Any) -> "Event":
        try:
            strings = (value["id"], value["pubkey"], value["content"], value["sig"])
            numbers = (value["created_at"], value["kind"])
            if not all(isinstance(x, str) for x in strings) or not all(
                    isinstance(x, int) and not isinstance(x, bool) for x in numbers):
                raise TypeError("id, pubkey, content and sig are strings; created_at and kind are integers")
            if not all(isinstance(t, list) and all(isinstance(x, str) for x in t) for t in value["tags"]):
                raise TypeError("tags are lists of strings")
            return cls(value["id"], value["pubkey"], value["created_at"], value["kind"],
                       tuple(tuple(t) for t in value["tags"]), value["content"], value["sig"])
        except (KeyError, TypeError, ValueError) as exc:
            raise MailboxError(f"not an event: {exc}") from None

    def tag(self, name: str) -> list[tuple[str, ...]]:
        return [t for t in self.tags if t and t[0] == name]


def _event_id(pubkey: str, created_at: int, kind: int, tags: Iterable[Iterable[str]], content: str) -> str:
    # NIP-01 serialization. Json's escaping of control characters other than the ones
    # NIP-01 lists differs from it, which does not arise for base64url content and ASCII tags.
    text = json.dumps([0, pubkey, created_at, kind, [list(t) for t in tags], content],
                      separators=(",", ":"), ensure_ascii=False)
    return hashlib.sha256(text.encode()).hexdigest()


def sign_event(secret: bytes, kind: int, tags: Iterable[Iterable[str]], content: str, created_at: int) -> Event:
    pub = public_key(secret)
    tags = tuple(tuple(t) for t in tags)
    event_id = _event_id(pub, created_at, kind, tags, content)
    return Event(event_id, pub, created_at, kind, tags, content, schnorr_sign(bytes.fromhex(event_id), secret).hex())


def check_event(event: Event) -> bool:
    """NIP-01 checks 5 and 6 of binding §4.2: the ``id`` recomputes and ``sig`` verifies under ``pubkey``."""
    try:
        if event.id != _event_id(event.pubkey, event.created_at, event.kind, event.tags, event.content):
            return False
        return schnorr_verify(bytes.fromhex(event.id), bytes.fromhex(event.pubkey), bytes.fromhex(event.sig))
    except (ValueError, TypeError):
        return False


def encode(octets: bytes) -> str:
    return base64.urlsafe_b64encode(octets).rstrip(b"=").decode()


def decode(content: str) -> bytes | None:
    """base64url without padding (binding §4); ``None`` for content that is not."""
    if not isinstance(content, str) or not content.isascii() or content.strip("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"):
        return None
    try:
        octets = base64.urlsafe_b64decode(content + "=" * (-len(content) % 4))
    except ValueError:
        return None
    return octets if encode(octets) == content else None


# -- NIP-19 nprofile (the route of an absence record, binding §5.3) -------------------------------

_B32 = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"


def _polymod(values: list[int]) -> int:
    gen = (0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3)
    chk = 1
    for v in values:
        top = chk >> 25
        chk = (chk & 0x1FFFFFF) << 5 ^ v
        for i in range(5):
            chk ^= gen[i] if (top >> i) & 1 else 0
    return chk


def _bech32(hrp: str, data: bytes) -> str:
    bits = [int(b) for byte in data for b in f"{byte:08b}"]
    bits += [0] * (-len(bits) % 5)
    words = [int("".join(map(str, bits[i:i + 5])), 2) for i in range(0, len(bits), 5)]
    expand = [ord(c) >> 5 for c in hrp] + [0] + [ord(c) & 31 for c in hrp]
    poly = _polymod(expand + words + [0] * 6) ^ 1
    checksum = [(poly >> 5 * (5 - i)) & 31 for i in range(6)]
    return hrp + "1" + "".join(_B32[w] for w in words + checksum)


def nprofile(pubkey: str, relays: Iterable[str]) -> str:
    """The NIP-21 URI of the NIP-19 ``nprofile`` for ``pubkey`` and ``relays``."""
    tlv = b"\x00\x20" + bytes.fromhex(pubkey)
    for relay in relays:
        raw = relay.encode()
        tlv += b"\x01" + bytes([len(raw)]) + raw
    return "nostr:" + _bech32("nprofile", tlv)


# -- relays --------------------------------------------------------------------------------------


class Relay:
    """One relay, spoken to over a websocket per call (a poll connects, asks and leaves)."""

    def __init__(self, url: str, timeout: float = 10.0) -> None:
        self.url, self.timeout = url, timeout

    def _connect(self):
        try:
            from websockets.sync.client import connect
        except ImportError:
            raise MailboxError("the mailbox transport needs the websockets package installed") from None
        try:
            return connect(self.url, open_timeout=self.timeout, close_timeout=1)
        except Exception as exc:  # unreachable relay, refused handshake: all one transport condition
            raise MailboxError(f"relay {self.url} could not be reached: {exc}") from None

    def publish(self, event: Event) -> None:
        """Send ``event`` and wait for the relay's ``OK``; a refusal is a transport condition."""
        try:
            self._publish(event)
        except MailboxError:
            raise
        except Exception as exc:  # a connection cut mid-exchange, a malformed OK
            raise MailboxError(f"relay {self.url} failed: {exc}") from None

    def _publish(self, event: Event) -> None:
        with self._connect() as ws:
            ws.send(json.dumps(["EVENT", event.to_json()]))
            deadline = time.monotonic() + self.timeout
            while time.monotonic() < deadline:
                try:
                    msg = json.loads(ws.recv(timeout=max(deadline - time.monotonic(), 0.01)))
                except (TimeoutError, ValueError):
                    break
                if isinstance(msg, list) and msg[:1] == ["OK"] and msg[1:2] == [event.id]:
                    if msg[2:3] == [True]:
                        return
                    raise MailboxError(f"relay {self.url} did not accept the event: {msg[3:4]}")
            raise MailboxError(f"relay {self.url} did not answer the event")

    def query(self, flt: dict) -> list[Event]:
        """The stored events matching ``flt``, read to the relay's end-of-stored-events."""
        try:
            return self._query(flt)
        except MailboxError:
            raise
        except Exception as exc:  # a connection cut mid-exchange
            raise MailboxError(f"relay {self.url} failed: {exc}") from None

    def _query(self, flt: dict) -> list[Event]:
        events: list[Event] = []
        with self._connect() as ws:
            ws.send(json.dumps(["REQ", "poll", flt]))
            deadline = time.monotonic() + self.timeout
            while time.monotonic() < deadline:
                try:
                    msg = json.loads(ws.recv(timeout=max(deadline - time.monotonic(), 0.01)))
                except TimeoutError:
                    break
                except ValueError:
                    continue
                if not isinstance(msg, list) or not msg:
                    continue
                if msg[0] == "EVENT" and len(msg) >= 3:
                    try:
                        events.append(Event.from_json(msg[2]))
                    except MailboxError:
                        continue
                elif msg[0] in ("EOSE", "CLOSED"):
                    break
            try:
                ws.send(json.dumps(["CLOSE", "poll"]))
            except Exception:  # the poll is over either way
                pass
        return events


def publish_all(relays: Iterable[Relay], event: Event) -> list[str]:
    """Publish to every relay and return the URLs that accepted it; none is an error
    (binding §4.3: at least one relay of the responder's declared list)."""
    accepted, errors = [], []
    for relay in relays:
        try:
            relay.publish(event)
            accepted.append(relay.url)
        except MailboxError as exc:
            errors.append(str(exc))
    if not accepted:
        raise MailboxError("no relay accepted the request: " + "; ".join(errors))
    return accepted


def poll_replies(relays: Iterable[Relay], request_id: str, media: str, mailbox_key: str) -> list[Event]:
    """The replies to the request event ``request_id`` now stored on ``relays``: events that pass the seven
    checks of binding §4.2, one per ``id``, oldest first. An unreachable relay is skipped
    (a dropped reply is an absence at the deadline, never a failure)."""
    found: dict[str, Event] = {}
    for relay in relays:
        try:
            events = relay.query({"kinds": [REPLY_KIND], "#e": [request_id]})
        except MailboxError:
            continue
        for ev in events:
            if _is_reply(ev, request_id, media, mailbox_key):
                found.setdefault(ev.id, ev)
    return sorted(found.values(), key=lambda e: (e.created_at, e.id))


def _is_reply(ev: Event, request_id: str, media: str, mailbox_key: str) -> bool:
    if ev.kind != REPLY_KIND:                                           # 1 kind
        return False
    if len(ev.tag("e")) != 1 or ev.tag("e")[0][1:2] != (request_id,):  # 2 reply_to
        return False
    if len(ev.tag("m")) != 1 or ev.tag("m")[0][1:2] != (media,):    # 3 media_type
        return False
    if decode(ev.content) is None:                                      # 4 content
        return False
    return check_event(ev) and ev.pubkey == mailbox_key                 # 5 id, 6 signature, 7 author


def serve_round(relays: Iterable[Relay], secret: bytes, answer: Callable[[bytes], bytes | None],
                answered: set[str], now: Callable[[], float] = time.time) -> list[str]:
    """The responder's side of one poll: read the requests addressed to this mailbox key,
    skip any whose ``id`` or signature fails (binding §4.1) or that this mailbox already
    answered, and publish ``answer(request octets)`` as a reply on the relay the request
    was read from. ``answer`` returning ``None`` publishes no reply (binding §5.2).
    Returns the ids of the requests answered."""
    mailbox_key = public_key(secret)
    done = []
    for relay in relays:
        try:
            requests = relay.query({"kinds": [REQUEST_KIND], "#p": [mailbox_key]})
        except MailboxError:
            continue
        for req in requests:
            if req.id in answered or not check_event(req) or len(req.tag("p")) != 1 or len(req.tag("m")) != 1:
                continue
            octets = decode(req.content)
            if octets is None:
                continue
            reply = answer(octets)
            if reply is None:
                answered.add(req.id)
                continue
            event = sign_event(secret, REPLY_KIND,
                               [("e", req.id), ("p", req.pubkey), ("m", req.tag("m")[0][1])], encode(reply),
                               int(now()))
            try:
                relay.publish(event)
            except MailboxError:
                continue  # not answered: the next round tries again
            answered.add(req.id)
            done.append(req.id)
    return done
