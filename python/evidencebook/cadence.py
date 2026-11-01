# SPDX-License-Identifier: Apache-2.0
"""The shelf's heartbeat cadence (evidencebook#254 §2.6).

On time alone, each tick checkpoints every live log of a shelf locally,
commits those checkpoints as salted leaves of a sparse, fixed-depth tree,
appends the root to the profile's cadence log and checkpoints that log,
which alone goes to the witness. A log is live while it has not ended, or
while it has grown past the largest checkpoint of it a witnessed tick
carries. Delivering the cadence checkpoint is the host's; the host reports
the receipt with :meth:`Cadence.witnessed`, which verifies it with AAC's
witness verifier.

Every construction and stored value is Go's (``cadence.go``,
``cadence_tree.go``, ``cadence_chain.go``, ``cadence_standard_history.go``,
``cadence_import.go``), byte for byte; ``testdata/shelf`` holds the vectors.
"""
from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field, replace
from datetime import datetime, timedelta, timezone
from typing import Any

from agent_action_capsule.canonical import jcs
from agent_action_capsule.witness import verify_witness_receipts
from cll.checkpoint import core
from cll.checkpoint.cose_wire import verify_checkpoint_cose_offline

from ._cll import read_checkpoint
from ._gojson import Ordered, go_json
from ._random import RandomSource
from ._time import utc
from .errors import CorruptError, InvalidError, NotFoundError

__all__ = [
    "CADENCE_DEPTH", "CADENCE_EXTENSION", "WITNESS_VERIFIED", "WITNESS_NOT_CHECKED", "WITNESS_INVALID",
    "Cadence", "CadenceConfig", "CadenceTick", "LegacyCadence", "StandardHistoryAnchor", "TickLeaf", "TickLeaves",
    "TickResult", "WitnessCheck", "WitnessReceipt", "WitnessRejected", "derived_fillers", "padding_record_id",
]

#: The tree's fixed depth: every path is 16 hashes.
CADENCE_DEPTH = 16
_CAPACITY = 1 << CADENCE_DEPTH

#: The bundle extension a cadence chain travels in.
CADENCE_EXTENSION = "x-cadence-witness/v0"

WITNESS_VERIFIED, WITNESS_NOT_CHECKED, WITNESS_INVALID = "VERIFIED", "NOT_CHECKED", "INVALID"

_SIGNED_FIELDS = ("log_id", "mmr_size", "root", "key_id", "timestamp", "prev_size", "prev_root")


# -- the tree --------------------------------------------------------------------------


def cadence_leaf(log_id: str, size: int, checkpoint_sha256: str, salt: str) -> bytes:
    """``SHA-256(0x00 ‖ JCS({checkpoint_sha256, log_id, salt, size}))``."""
    body = jcs({"checkpoint_sha256": checkpoint_sha256, "log_id": log_id, "salt": salt, "size": size})
    return hashlib.sha256(b"\x00" + body).digest()


def filler_leaf(nonce: bytes) -> bytes:
    """``SHA-256(0x03 ‖ nonce)``."""
    return hashlib.sha256(b"\x03" + nonce).digest()


def _node(left: bytes, right: bytes) -> bytes:
    return hashlib.sha256(b"\x01" + left + right).digest()


def _empty_roots() -> list[bytes]:
    levels = [hashlib.sha256(b"\x02").digest()]
    for _ in range(CADENCE_DEPTH):
        levels.append(_node(levels[-1], levels[-1]))
    return levels


#: ``EMPTY[h]`` is the root of an empty subtree of height ``h``.
EMPTY = _empty_roots()


def cadence_tree(leaves: dict[int, bytes], index: int) -> tuple[bytes, list[bytes]]:
    """The root of the sparse tree over ``leaves`` and the path of ``index``:
    the sibling at each height, leaf level first, an empty subtree's root
    where there is none."""
    level, path = dict(leaves), []
    for h in range(CADENCE_DEPTH):
        path.append(level.get(index ^ 1, EMPTY[h]))
        parents: dict[int, bytes] = {}
        for i in level:
            parent = i >> 1
            if parent not in parents:
                parents[parent] = _node(level.get(parent << 1, EMPTY[h]), level.get(parent << 1 | 1, EMPTY[h]))
        level, index = parents, index >> 1
    return level.get(0, EMPTY[CADENCE_DEPTH]), path


def cadence_fold(leaf: bytes, index: int, path: Sequence[bytes]) -> bytes:
    """Recompute a root from a leaf, its position and its path."""
    h = leaf
    for level, sibling in enumerate(path):
        h = _node(h, sibling) if (index >> level) & 1 == 0 else _node(sibling, h)
    return h


def derived_fillers(seed: bytes, count: int, used: set[int]) -> dict[int, bytes]:
    """Fillers 1..``count`` of a tick, by position. Filler ``i`` is
    ``SHA-256(0x03 ‖ n_i)``, ``n_i = SHA-256(seed ‖ uint64be(i))``, at the
    first unused position among, for attempt ``a = 0, 1, …``,
    ``uint64be(SHA-256(seed ‖ "pos" ‖ uint64be(i) ‖ uint32be(a))[0:8]) mod
    2^16``. ``used`` is updated."""
    if len(used) > _CAPACITY or count > _CAPACITY - len(used):
        raise InvalidError(f"{count} fillers do not fit beside {len(used)} leaves")
    out: dict[int, bytes] = {}
    for i in range(1, count + 1):
        ib = i.to_bytes(8, "big")
        nonce = hashlib.sha256(seed + ib).digest()
        a = 0
        while True:
            digest = hashlib.sha256(seed + b"pos" + ib + a.to_bytes(4, "big")).digest()
            position = int.from_bytes(digest[:8], "big") % _CAPACITY
            if position not in used:
                used.add(position)
                out[position] = filler_leaf(nonce)
                break
            a += 1
    return out


def padding_record_id(nonce: str) -> str:
    """The id of an Evidence Layer padding record whose only content is
    ``nonce`` (64 lowercase hex)."""
    body = jcs({"record_type": "padding", "epistemic_type": "producer_claim", "store_nonce": nonce})
    return hashlib.sha256(body).hexdigest()


def random_position(random: RandomSource, used: set[int]) -> int:
    """A fresh, uniform, unused tree position."""
    if len(used) >= _CAPACITY:
        raise InvalidError("the cadence tree is full")
    while True:
        i = random.uniform(_CAPACITY)
        if i not in used:
            used.add(i)
            return i


@dataclass(frozen=True)
class TickLeaf:
    """One log's leaf in one tick, as stored on the device. ``statement`` is
    the log's checkpoint itself (base64url COSE); the leaf commits to its
    SHA-256."""

    log_id: str
    size: int
    checkpoint_sha256: str
    salt: str
    index: int
    statement: str = ""

    def value(self) -> Ordered:
        out = Ordered(log_id=self.log_id, size=self.size, checkpoint_sha256=self.checkpoint_sha256, salt=self.salt,
                      index=self.index)
        if self.statement:
            out["statement"] = self.statement
        return out

    @classmethod
    def from_value(cls, value: dict) -> TickLeaf:
        return cls(value["log_id"], int(value["size"]), value["checkpoint_sha256"], value["salt"],
                   int(value["index"]), value.get("statement", ""))


@dataclass(frozen=True)
class TickLeaves:
    """Every leaf of one tick: the logs under ``deals``, filler 0, and the
    derived fillers that keep its occupancy at the lifetime log count."""

    deals: dict[str, TickLeaf]
    filler_index: int
    filler_nonce: str
    filler_seed: str = ""
    filler_count: int = 0

    def encode(self) -> str:
        """Go's ``json.Marshal`` of its ``TickLeaves``, as ``cadence_ticks.leaves`` holds it."""
        out = Ordered(deals={k: self.deals[k].value() for k in sorted(self.deals, key=lambda k: k.encode())},
                      filler_index=self.filler_index, filler_nonce=self.filler_nonce)
        if self.filler_seed:
            out["filler_seed"] = self.filler_seed
        if self.filler_count:
            out["filler_count"] = self.filler_count
        return go_json(out)

    @classmethod
    def decode(cls, text: str) -> TickLeaves:
        try:
            value = json.loads(text)
            return cls({k: TickLeaf.from_value(v) for k, v in (value.get("deals") or {}).items()},
                       int(value.get("filler_index", 0)), value.get("filler_nonce", ""),
                       value.get("filler_seed", ""), int(value.get("filler_count", 0)))
        except (ValueError, TypeError, KeyError, AttributeError) as exc:
            raise CorruptError(f"tick leaves: {exc}") from None

    def tree(self, index: int) -> tuple[bytes, list[bytes]]:
        """Rebuild the tick's tree from what it stored: the root and the path
        of the leaf at ``index``."""
        if not 0 <= index < _CAPACITY or not 0 <= self.filler_index < _CAPACITY:
            raise CorruptError(f"a cadence position is under {_CAPACITY}")
        try:
            nonce = bytes.fromhex(self.filler_nonce)
        except ValueError as exc:
            raise CorruptError(f"filler nonce: {exc}") from None
        leaves, used = {self.filler_index: filler_leaf(nonce)}, {self.filler_index}
        for leaf in self.deals.values():
            if not 0 <= leaf.index < _CAPACITY:
                raise CorruptError(f"position {leaf.index} of {leaf.log_id} is past the tree")
            if leaf.index in used:
                raise CorruptError(f"two leaves at position {leaf.index}")
            leaves[leaf.index] = cadence_leaf(leaf.log_id, leaf.size, leaf.checkpoint_sha256, leaf.salt)
            used.add(leaf.index)
        if self.filler_count > 0:
            try:
                seed = bytes.fromhex(self.filler_seed)
            except ValueError:
                seed = b""
            if len(seed) != 32:
                raise CorruptError("filler seed")
            leaves.update(derived_fillers(seed, self.filler_count, used))
        return cadence_tree(leaves, index)


# -- ticks and receipts ---------------------------------------------------------------


@dataclass(frozen=True)
class WitnessReceipt:
    """The witness's receipt for a tick's cadence checkpoint."""

    ts_url: str
    entry_hash: str
    receipt: bytes
    leaf_index: int = 0
    tree_size: int = 0

    def encode(self) -> str:
        """Go's ``json.Marshal`` of its ``WitnessReceipt``."""
        return go_json(Ordered(ts_url=self.ts_url, entry_hash=self.entry_hash,
                               receipt=base64.b64encode(self.receipt).decode(), leaf_index=self.leaf_index,
                               tree_size=self.tree_size))

    @classmethod
    def decode(cls, text: str) -> WitnessReceipt:
        try:
            value = json.loads(text)
            return cls(value["ts_url"], value["entry_hash"], base64.b64decode(value["receipt"], validate=True),
                       int(value["leaf_index"]), int(value["tree_size"]))
        except (ValueError, TypeError, KeyError) as exc:
            raise CorruptError(f"tick receipt: {exc}") from None

    def trimmed(self) -> WitnessReceipt:
        """With the witness's URL carried without a trailing slash, as a
        verifier finds its directory row."""
        return replace(self, ts_url=self.ts_url.rstrip("/"))


@dataclass(frozen=True)
class CadenceTick:
    """One tick as stored. ``checkpoint_size`` and ``checkpoint`` are the
    cadence log's checkpoint carrying the root; zero while unfinished."""

    n: int
    at: datetime
    due_next: datetime
    entry_seq: int
    checkpoint_size: int
    checkpoint: bytes
    leaves: TickLeaves
    witnessed: bool = False
    receipt: WitnessReceipt | None = None


@dataclass(frozen=True)
class TickResult:
    """What one :meth:`Cadence.tick` did: ``ticked`` is false when no tick
    was due, and ``due_next`` says when one is."""

    ticked: bool
    due_next: datetime
    tick: CadenceTick | None = None


@dataclass(frozen=True)
class WitnessCheck:
    """What checking one receipt found: VERIFIED, NOT_CHECKED or INVALID, and why."""

    state: str
    reason: str = ""


class WitnessRejected(InvalidError):
    """A receipt that is INVALID; ``check`` says why."""

    def __init__(self, message: str, check: WitnessCheck):
        super().__init__(message)
        self.check = check


def check_witness_receipt(statement: bytes, receipt: WitnessReceipt, directory: Any) -> WitnessCheck:
    """Verify ``receipt`` for the signed cadence checkpoint ``statement``
    under ``directory`` with AAC's verifier, the check a bundle's
    ``checkpoint.witnesses`` gets. Raises :class:`InvalidError` when the
    statement itself does not verify."""
    verified = verify_checkpoint_cose_offline(statement)
    if not verified.ok or verified.decoded is None:
        raise InvalidError("the cadence checkpoint statement does not verify")
    signed = {name: getattr(verified.decoded, name) for name in _SIGNED_FIELDS}
    record = {"ts_url": receipt.ts_url, "entry_hash": receipt.entry_hash,
              "receipt_b64": base64.b64encode(receipt.receipt).decode(), "leaf_index": receipt.leaf_index,
              "tree_size": receipt.tree_size}
    status, findings, results = verify_witness_receipts([record], signed, statement, directory)
    parts = list(findings)
    if len(results) == 1 and results[0].reason:
        parts.append(results[0].reason)
    reason = ": ".join(parts)
    if status == "pass":
        return WitnessCheck(WITNESS_VERIFIED, reason)
    if status == "withheld":
        return WitnessCheck(WITNESS_NOT_CHECKED, reason)
    return WitnessCheck(WITNESS_INVALID, reason)


# -- the configuration ------------------------------------------------------------------


@dataclass(frozen=True)
class CadenceConfig:
    """A shelf's cadence (host parameters, §2.2). ``interval`` is at least
    1 minute and ``jitter`` under half of it (defaults 5 and 1 minutes;
    ``no_jitter`` makes it zero). ``witness_ids`` each get a pending delivery
    with every cadence checkpoint; ``witnesses`` is the witness directory
    receipts are verified under (a ``witnesses.json`` mapping; ``None``
    trusts no witness). ``pad_bucket`` pads the cadence log to a multiple of
    that many entries per tick (at most 1024; 0 is 1). ``ended`` is the
    profile's predicate: whether a log has ended. It must not call the shelf."""

    log_id: str
    ended: Callable[[str], bool]
    interval: timedelta = timedelta(0)
    jitter: timedelta = timedelta(0)
    no_jitter: bool = False
    witness_ids: Sequence[str] = ()
    witnesses: Any = None
    pad_bucket: int = 0

    def checked(self) -> CadenceConfig:
        interval = self.interval or timedelta(minutes=5)
        jitter = self.jitter if self.jitter or self.no_jitter else timedelta(minutes=1)
        bucket = self.pad_bucket or 1
        if not self.log_id:
            raise InvalidError("a cadence needs its log id")
        if self.ended is None:
            raise InvalidError("a cadence needs the profile's ended predicate")
        if interval < timedelta(minutes=1):
            raise InvalidError("the cadence interval must be at least 1m")
        if self.no_jitter and self.jitter:
            raise InvalidError("a cadence with no_jitter has no jitter")
        if jitter < timedelta(0) or 2 * jitter >= interval:
            raise InvalidError("the cadence jitter must be at least 0 and under half the interval")
        if bucket > 1024:
            raise InvalidError("the cadence pad bucket must be at most 1024")
        return replace(self, interval=interval, jitter=jitter, pad_bucket=bucket, witness_ids=tuple(self.witness_ids))


@dataclass(frozen=True)
class LegacyCadence:
    """A pre-shelf writer's cadence, on the same cadence log: its logs (each a
    log of the file that is not a book) and its finished ticks, in
    cadence-log order (``n`` is ignored)."""

    logs: Sequence[str]
    ticks: Sequence[CadenceTick] = ()


@dataclass(frozen=True)
class StandardHistoryAnchor:
    """A cadence tick as the anchor of a shelf's ``standard_history/1``
    card: its cadence checkpoint's digest and time, the time of the cadence
    log's first checkpoint, and for every log the ticks up to it carried,
    how many of its records the tick commits."""

    tick: CadenceTick
    anchor: str
    anchor_time: str
    first_checkpoint_time: str
    extents: dict[str, int] = field(default_factory=dict)


# -- the cadence ------------------------------------------------------------------------

_NS = 1_000_000_000


def _ns(moment: datetime) -> int:
    moment = utc(moment)
    return int(moment.replace(microsecond=0).timestamp()) * _NS + moment.microsecond * 1000


def _from_ns(ns: int) -> datetime:
    return datetime.fromtimestamp(ns // _NS, timezone.utc) + timedelta(microseconds=(ns % _NS) // 1000)


def _rfc3339(moment: datetime) -> str:
    return utc(moment).strftime("%Y-%m-%dT%H:%M:%SZ")


def _parse_rfc3339(text: str, what: str) -> datetime:
    try:
        return datetime.strptime(text, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
    except (ValueError, TypeError):
        raise CorruptError(f"{what}: {text!r} is not an RFC 3339 time") from None


def _profile_time(ns: int) -> str:
    return datetime.fromtimestamp(ns // _NS, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


_WITNESS_INDEX = """INSERT INTO cadence_witnessed (log_id, checkpoint_sha256, mmr_size, tick) VALUES (?, ?, ?, ?)
ON CONFLICT (log_id, checkpoint_sha256) DO UPDATE SET tick = excluded.tick WHERE excluded.tick > cadence_witnessed.tick"""


def _held_size(db, log_id: str) -> int:
    """The MMR size of everything a log holds: a book's stored records
    (stored before their log append, so a pending one counts), else its CLL
    entries."""
    from .shelf import mmr_size

    leaves = db.execute("SELECT COUNT(*) FROM book_records WHERE book_id = ?", (log_id,)).fetchone()[0]
    if not leaves:
        leaves = db.execute("SELECT COUNT(*) FROM cll_entries WHERE log_id = ?", (log_id,)).fetchone()[0]
    return mmr_size(leaves)


class Cadence:
    """A shelf's heartbeat ticks. Open it with :meth:`Shelf.cadence`."""

    def __init__(self, shelf: Any, config: CadenceConfig, log: Any):
        self._shelf = shelf
        self._cfg = config
        self._log = log

    @property
    def log_id(self) -> str:
        return self._cfg.log_id

    @property
    def config(self) -> CadenceConfig:
        return self._cfg

    # -- reading ticks --------------------------------------------------------------------

    def _tick_locked(self, n: int) -> CadenceTick:
        with self._shelf._read() as db:
            row = db.execute("SELECT at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt "
                             "FROM cadence_ticks WHERE tick = ?", (n,)).fetchone()
        if row is None:
            raise NotFoundError(f"tick {n}")
        at, due, entry_seq, size, checkpoint, leaves, witnessed, receipt = row
        return CadenceTick(n, _parse_rfc3339(at, f"tick {n} time"), _parse_rfc3339(due, f"tick {n} due time"),
                           entry_seq, size, bytes(checkpoint), TickLeaves.decode(leaves), bool(witnessed),
                           None if receipt is None else WitnessReceipt.decode(receipt))

    def _last_locked(self) -> CadenceTick | None:
        with self._shelf._read() as db:
            n = db.execute("SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks").fetchone()[0]
        return self._tick_locked(n) if n else None

    def last(self) -> CadenceTick | None:
        """The newest stored tick, or ``None``."""
        with self._shelf._mu:
            self._shelf._check_open()
            return self._last_locked()

    def get(self, n: int) -> CadenceTick:
        """Stored tick ``n``."""
        with self._shelf._mu:
            return self._tick_locked(n)

    def live(self) -> list[str]:
        """The live set, in log id order."""
        with self._shelf._mu:
            return self._live_locked()

    def _live_locked(self) -> list[str]:
        with self._shelf._read() as db:
            return [row[0] for row in db.execute("SELECT log_id FROM cadence_live ORDER BY log_id")]

    # -- ticking ------------------------------------------------------------------------

    def tick(self, now: datetime) -> TickResult:
        """Cut a tick when one is due at ``now`` (at or after the last tick's
        ``due_next``); otherwise say when one is. A stored, unfinished tick is
        finished first, as the same tick."""
        shelf = self._shelf
        with shelf._mu:
            shelf._check_open()
            last = self._last_locked()
            if last is not None and last.checkpoint_size == 0:
                return self._finish_locked(last)
            if last is not None and utc(now) < last.due_next:
                return TickResult(False, last.due_next)
            n = 1 if last is None else last.n + 1
            now_ns = _ns(now)
            # Times leaving this device are coarsened to the minute.
            at = _from_ns(now_ns - now_ns % (60 * _NS))
            live = self._live_locked()
            with shelf._read() as db:
                lifetime = db.execute("SELECT COUNT(*) FROM cadence_logs").fetchone()[0]
            if len(live) >= _CAPACITY or lifetime >= _CAPACITY:
                raise InvalidError("too many logs for one cadence tree")
            # Every draw is from the shelf's one source, in this order: filler
            # 0's position and nonce; each live log's salt and position, in log
            # id order; the derived fillers' seed when there are any; the
            # jitter; then each padding record's nonce (_finish_locked).
            random, used = shelf.random, set()
            filler_index = random_position(random, used)
            filler_nonce = random.hex(32)
            deals: dict[str, TickLeaf] = {}
            for log_id in live:
                log = shelf._cadence_log_locked(log_id)
                # A host log opened in the cadence and not yet written holds no
                # entry, so it has no checkpoint to carry: the tick skips it.
                if log.mmr_size() == 0:
                    continue
                cp = log.cut_local(at)
                salt = random.hex(32)
                deals[log_id] = TickLeaf(log_id, cp.tree_size, hashlib.sha256(cp.statement).hexdigest(), salt,
                                         random_position(random, used),
                                         base64.urlsafe_b64encode(cp.statement).rstrip(b"=").decode())
            filler_seed, filler_count = "", 0
            # The occupancy floor: H + 1 occupied positions.
            if lifetime > len(deals):
                filler_seed, filler_count = random.hex(32), lifetime - len(deals)
            jitter = 0
            jitter_ns = self._cfg.jitter // timedelta(microseconds=1) * 1000
            if jitter_ns > 0:
                jitter = random.uniform(2 * jitter_ns + 1) - jitter_ns
            interval_ns = self._cfg.interval // timedelta(microseconds=1) * 1000
            due = now_ns + interval_ns + jitter
            leaves = TickLeaves(deals, filler_index, filler_nonce, filler_seed, filler_count)
            tick = CadenceTick(n, at, _from_ns(due - due % _NS), 0, 0, b"", leaves)
            # The tick is stored before its root reaches the cadence log, so a
            # failure after this resumes this tick rather than cutting another.
            with shelf._tx() as db:
                db.execute("INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, "
                           "leaves) VALUES (?, ?, ?, 0, 0, x'', ?)",
                           (n, _rfc3339(tick.at), _rfc3339(tick.due_next), leaves.encode()))
            return self._finish_locked(tick)

    def _finish_locked(self, tick: CadenceTick) -> TickResult:
        """Put a stored tick's root in the cadence log once, pad the log to
        the bucket, checkpoint it and complete the tick's row."""
        shelf = self._shelf
        root, _ = tick.leaves.tree(tick.leaves.filler_index)
        root_id = root.hex()
        after = self._tick_locked(tick.n - 1).entry_seq if tick.n > 1 else 0
        # Read only what follows the previous tick's entry.
        entry_seq, end = 0, after
        while True:
            entries = self._log.entries_after(end, 1000)
            if not entries:
                break
            for entry in entries:
                if entry.value.hex() == root_id and entry_seq == 0:
                    entry_seq = entry.seq
            end = entries[-1].seq
        if entry_seq == 0:
            entry_seq = end = self._log.append(root_id, tick.at)
        count = end
        while count % self._cfg.pad_bucket:
            self._log.append(padding_record_id(shelf.random.hex(32)), tick.at)
            count += 1
        cp = self._log.checkpoint(tick.at)
        with shelf._tx() as db:
            db.execute("UPDATE cadence_ticks SET entry_seq = ?, checkpoint_size = ?, checkpoint = ? WHERE tick = ?",
                       (entry_seq, cp.tree_size, cp.statement, tick.n))
        tick = replace(tick, entry_seq=entry_seq, checkpoint_size=cp.tree_size, checkpoint=cp.statement)
        return TickResult(True, tick.due_next, tick)

    # -- witnessing ---------------------------------------------------------------------

    def witnessed(self, n: int, receipt: WitnessReceipt) -> WitnessCheck:
        """Check a witness's receipt for tick ``n``'s cadence checkpoint. Only
        a VERIFIED receipt witnesses the tick: each log checkpoint it carries
        gets a ``cadence_witnessed`` row, and each of those logs that has
        ended and not grown past its largest witnessed checkpoint leaves the
        live set, in one transaction. NOT_CHECKED changes nothing; INVALID
        changes nothing and raises :class:`WitnessRejected`."""
        receipt = receipt.trimmed()
        if not receipt.ts_url or not receipt.entry_hash or not receipt.receipt:
            reason = "a witnessed tick needs its receipt, its entry hash and its witness's URL"
            raise WitnessRejected(reason, WitnessCheck(WITNESS_INVALID, reason))
        shelf = self._shelf
        with shelf._mu:
            shelf._check_open()
            tick = self._tick_locked(n)
            if tick.checkpoint_size == 0:
                raise InvalidError(f"tick {n} is not finished, so no witness has seen its root")
            try:
                check = check_witness_receipt(tick.checkpoint, receipt, self._cfg.witnesses)
            except InvalidError as exc:
                raise CorruptError(f"tick {n}: {exc}") from None
            if check.state == WITNESS_INVALID:
                raise WitnessRejected(f"tick {n}'s witness receipt is rejected: {check.reason}", check)
            if check.state != WITNESS_VERIFIED:
                return check
            ended = {log_id: bool(self._cfg.ended(log_id)) for log_id in tick.leaves.deals}
            with shelf._tx() as db:
                db.execute("UPDATE cadence_ticks SET witnessed = 1, receipt = COALESCE(receipt, ?) WHERE tick = ?",
                           (receipt.encode(), n))
                for log_id, leaf in tick.leaves.deals.items():
                    db.execute(_WITNESS_INDEX, (log_id, leaf.checkpoint_sha256, leaf.size, n))
                    witnessed = db.execute("SELECT COALESCE(MAX(mmr_size), 0) FROM cadence_witnessed WHERE log_id = ?",
                                           (log_id,)).fetchone()[0]
                    if ended[log_id] and _held_size(db, log_id) <= witnessed:
                        db.execute("DELETE FROM cadence_live WHERE log_id = ?", (log_id,))
            return check

    def witnessed_tick(self, log_id: str, checkpoint_sha256: str, mmr_size: int) -> tuple[str, CadenceTick, TickLeaf]:
        """The witnessed tick anchoring a bundle of ``log_id``'s checkpoint:
        ``"all"`` when a witnessed tick carries that very checkpoint, else
        ``"part"`` with the newest witnessed tick carrying an earlier one.
        :class:`NotFoundError` when none carries the log up to that size."""
        with self._shelf._mu:
            self._shelf._check_open()
            return self._witnessed_tick_locked(log_id, checkpoint_sha256, mmr_size)

    def _witnessed_tick_locked(self, log_id: str, checkpoint_sha256: str,
                               mmr_size: int) -> tuple[str, CadenceTick, TickLeaf]:
        with self._shelf._read() as db:
            row = db.execute("SELECT tick FROM cadence_witnessed WHERE log_id = ? AND checkpoint_sha256 = ?",
                             (log_id, checkpoint_sha256)).fetchone()
            extent = "all"
            if row is None:
                extent = "part"
                row = db.execute("SELECT tick FROM cadence_witnessed WHERE log_id = ? AND mmr_size < ? "
                                 "ORDER BY mmr_size DESC, tick DESC LIMIT 1", (log_id, mmr_size)).fetchone()
        if row is None:
            raise NotFoundError(f"no witnessed tick carries {log_id}")
        tick = self._tick_locked(row[0])
        leaf = tick.leaves.deals.get(log_id)
        if leaf is None:
            raise CorruptError(f"witnessed tick {tick.n} holds no leaf of {log_id}")
        return extent, tick, leaf

    # -- what a tick proves -------------------------------------------------------------

    def chain(self, statement: bytes) -> dict:
        """The witnessed cadence chain for a log's checkpoint (its COSE
        statement), member for member as Go's ``Cadence.Chain`` and
        capsule-cli's released verifier read it. :class:`NotFoundError` when
        no witnessed tick carries the log up to this checkpoint."""
        try:
            current = read_checkpoint(statement)
        except InvalidError as exc:
            raise CorruptError(f"checkpoint statement: {exc}") from None
        shelf = self._shelf
        with shelf._mu:
            shelf._check_open()
            extent, tick, leaf = self._witnessed_tick_locked(
                current.log_id, hashlib.sha256(statement).hexdigest(), current.tree_size)
            if tick.receipt is None:
                raise CorruptError(f"witnessed tick {tick.n} holds no receipt")
            _, path = tick.leaves.tree(leaf.index)
            cadence_cp = read_checkpoint(tick.checkpoint)
            out = {
                "state": "witnessed", "extent": extent, "checkpoint_at": _rfc3339(tick.at),
                "log_id": leaf.log_id, "size": leaf.size, "salt": leaf.salt, "index": leaf.index,
                "path": [node.hex() for node in path],
                "cadence": {
                    "log_id": self._cfg.log_id, "entry_index": tick.entry_seq - 1,
                    "checkpoint": {"cose": base64.urlsafe_b64encode(tick.checkpoint).rstrip(b"=").decode()},
                    "inclusion_proof": self._log.inclusion(tick.entry_seq, cadence_cp),
                    "witnesses": [{
                        "ts_url": tick.receipt.ts_url, "entry_hash": tick.receipt.entry_hash,
                        "receipt_b64": base64.b64encode(tick.receipt.receipt).decode(),
                        "leaf_index": tick.receipt.leaf_index, "tree_size": tick.receipt.tree_size,
                    }],
                },
            }
            if extent == "all":
                return out
            try:
                earlier_statement = base64.urlsafe_b64decode(leaf.statement + "=" * (-len(leaf.statement) % 4))
                earlier = read_checkpoint(earlier_statement)
            except (ValueError, InvalidError) as exc:
                raise CorruptError(f"tick {tick.n}'s statement of {leaf.log_id}: {exc}") from None
            if hashlib.sha256(earlier_statement).hexdigest() != leaf.checkpoint_sha256 or \
                    earlier.log_id != current.log_id or earlier.tree_size != leaf.size:
                raise CorruptError(f"tick {tick.n}'s statement of {leaf.log_id} is not its leaf's checkpoint")
            log = shelf._cadence_log_locked(current.log_id)
            out["earlier"] = {
                "checkpoint": {"cose": leaf.statement},
                "consistency_proof": log.substrate.consistency(earlier, current),
            }
            return out

    def standard_history_anchor(self, n: int = 0) -> StandardHistoryAnchor:
        """The anchor of tick ``n``, or of the newest finished tick when ``n``
        is 0. :class:`NotFoundError` when there is no such finished tick."""
        shelf = self._shelf
        with shelf._mu:
            shelf._check_open()
            with shelf._read() as db:
                if n == 0:
                    n = db.execute("SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0"
                                   ).fetchone()[0]
                    if n == 0:
                        raise NotFoundError("the cadence has no finished tick")
                first = db.execute("SELECT checkpoint FROM cadence_ticks WHERE checkpoint_size > 0 ORDER BY tick LIMIT 1"
                                   ).fetchone()
            tick = self._tick_locked(n)
            if tick.checkpoint_size == 0:
                raise NotFoundError(f"tick {n} is not finished")
            if first is None:
                raise CorruptError("the cadence has no finished tick")
            try:
                at, first_at = read_checkpoint(tick.checkpoint), read_checkpoint(bytes(first[0]))
            except InvalidError as exc:
                raise CorruptError(f"a cadence checkpoint: {exc}") from None
            return StandardHistoryAnchor(tick, hashlib.sha256(tick.checkpoint).hexdigest(),
                                         _profile_time(at.issued_at_ns), _profile_time(first_at.issued_at_ns),
                                         self._extents_locked(n))

    def _extents_locked(self, n: int) -> dict[str, int]:
        """Per log, the largest MMR size any finished tick up to ``n`` carries,
        as a record count."""
        with self._shelf._read() as db:
            rows = db.execute(
                "SELECT j.key, MAX(json_extract(j.value, '$.size')) FROM cadence_ticks AS t, "
                "json_each(t.leaves, '$.deals') AS j WHERE t.tick <= ? AND t.checkpoint_size > 0 GROUP BY j.key",
                (n,)).fetchall()
        extents = {}
        for log_id, size in rows:
            try:
                extents[log_id] = core.leaf_count(int(size))
            except (ValueError, TypeError):
                raise CorruptError(f"a tick carries {log_id} at size {size}, not a complete MMR") from None
        return extents

    # -- importing a pre-shelf cadence --------------------------------------------------

    def import_legacy(self, legacy: LegacyCadence) -> None:
        """Carry a pre-shelf cadence into this one, in one transaction, as Go's
        ``ImportLegacy``: every legacy log is registered as a host log in the
        cadence; each tick not already held is stored after the cadence's own,
        with each witnessed tick's receipt verified (an import whose receipt
        does not verify imports nothing); re-runnable."""
        cfg, shelf = self._cfg, self._shelf
        logs = set()
        for log_id in legacy.logs:
            if not log_id or log_id == cfg.log_id:
                raise InvalidError(f"legacy log {log_id!r}")
            logs.add(log_id)
        ticks, receipts = list(legacy.ticks), []
        for i, tick in enumerate(ticks):
            if tick.checkpoint_size == 0 or tick.entry_seq == 0 or not tick.checkpoint:
                raise InvalidError(f"legacy tick at entry {tick.entry_seq} is not finished")
            if i and tick.entry_seq <= ticks[i - 1].entry_seq:
                raise InvalidError(f"legacy ticks are not in cadence-log order (entry {tick.entry_seq} "
                                   f"after {ticks[i - 1].entry_seq})")
            for log_id in tick.leaves.deals:
                if log_id not in logs:
                    raise InvalidError(f"legacy tick at entry {tick.entry_seq} carries {log_id}, "
                                       "which is not a legacy log")
            encoded = None
            if tick.witnessed:
                if tick.receipt is None:
                    raise InvalidError(f"witnessed legacy tick at entry {tick.entry_seq} has no receipt")
                receipt = tick.receipt.trimmed()
                if not receipt.ts_url or not receipt.entry_hash or not receipt.receipt:
                    raise InvalidError(f"witnessed legacy tick at entry {tick.entry_seq} needs its receipt, its "
                                       "entry hash and its witness's URL")
                ticks[i] = replace(tick, receipt=receipt)
                encoded = receipt.encode()
            receipts.append(encoded)

        def verify(tick: CadenceTick) -> None:
            try:
                check = check_witness_receipt(tick.checkpoint, tick.receipt, cfg.witnesses)
            except InvalidError as exc:
                raise InvalidError(f"witnessed legacy tick at entry {tick.entry_seq}: {exc}") from None
            if check.state != WITNESS_VERIFIED:
                raise InvalidError(f"witnessed legacy tick at entry {tick.entry_seq}: its receipt is "
                                   f"{check.state}: {check.reason}")

        with shelf._mu:
            shelf._check_open()
            with shelf._read() as db:
                from .shelf import _kind_of, _record_count

                for log_id in logs:
                    host = shelf._hosts.get(log_id)
                    if _record_count(db, log_id) or log_id in shelf._books:
                        raise InvalidError(f"{log_id} is a book, not a legacy log")
                    if host is not None and not host.in_cadence:
                        raise InvalidError(f"{log_id} is a host log outside the cadence")
                    known = db.execute("SELECT COUNT(*) FROM cadence_logs WHERE log_id = ?", (log_id,)).fetchone()[0]
                    if _kind_of(db, log_id) == "host" and not known:
                        raise InvalidError(f"{log_id} is a host log outside the cadence")
            ended = {log_id: bool(cfg.ended(log_id)) for log_id in logs}
            with shelf._tx() as db:
                from .shelf import _claim_kind, _mark_live

                last, last_entry = db.execute(
                    "SELECT COALESCE(MAX(tick), 0), COALESCE(MAX(entry_seq), 0) FROM cadence_ticks").fetchone()
                if db.execute("SELECT COUNT(*) FROM cadence_ticks WHERE checkpoint_size = 0").fetchone()[0]:
                    raise InvalidError("the cadence's last tick is unfinished; tick first, which finishes it, "
                                       "then import")
                for log_id in sorted(logs):
                    _claim_kind(db, log_id, "host")
                    _mark_live(db, log_id)

                def witness(n: int, leaves: TickLeaves) -> None:
                    for log_id, leaf in leaves.deals.items():
                        db.execute(_WITNESS_INDEX, (log_id, leaf.checkpoint_sha256, leaf.size, n))

                for i, tick in enumerate(ticks):
                    held = db.execute("SELECT tick, checkpoint, witnessed, leaves FROM cadence_ticks "
                                      "WHERE entry_seq = ? AND checkpoint_size > 0", (tick.entry_seq,)).fetchone()
                    if held is not None:
                        held_n, held_cp, held_witnessed, held_leaves = held
                        if bytes(held_cp) != tick.checkpoint:
                            raise InvalidError(f"the cadence holds another tick at entry {tick.entry_seq}")
                        if tick.witnessed and not held_witnessed:
                            verify(tick)
                            db.execute("UPDATE cadence_ticks SET witnessed = 1, receipt = COALESCE(receipt, ?) "
                                       "WHERE tick = ?", (receipts[i], held_n))
                            witness(held_n, TickLeaves.decode(held_leaves))
                        continue
                    if tick.entry_seq <= last_entry:
                        raise InvalidError(f"legacy tick at entry {tick.entry_seq} is older than the cadence's "
                                           f"newest tick (entry {last_entry})")
                    last += 1
                    if tick.witnessed:
                        verify(tick)
                    db.execute("INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, "
                               "leaves, witnessed, receipt) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                               (last, _rfc3339(tick.at), _rfc3339(tick.due_next), tick.entry_seq,
                                tick.checkpoint_size, tick.checkpoint, tick.leaves.encode(), int(tick.witnessed),
                                receipts[i]))
                    last_entry = tick.entry_seq
                    if tick.witnessed:
                        witness(last, tick.leaves)
                for log_id in logs:
                    if not ended[log_id]:
                        continue
                    witnessed = db.execute("SELECT COALESCE(MAX(mmr_size), 0) FROM cadence_witnessed WHERE log_id = ?",
                                           (log_id,)).fetchone()[0]
                    if witnessed and _held_size(db, log_id) <= witnessed:
                        db.execute("DELETE FROM cadence_live WHERE log_id = ?", (log_id,))
