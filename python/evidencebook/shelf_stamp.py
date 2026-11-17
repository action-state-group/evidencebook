# SPDX-License-Identifier: Apache-2.0
"""The shelf log of book facts (evidencebook#461, docs/design/461-shelf-facts.md
§2.4, §2.5 and §4), byte for byte with Go's ``shelf_stamp.go``.

After :meth:`Cadence.migrate`, every tick of the cadence is a stamp. A stamp
appends one fact for each log whose fact would differ from its last one,
then a stamp entry, and checkpoints the cadence log, which alone goes to the
witness. A stamp is a cadence tick whose stored leaves carry its batch
(``TickLeaves.stamp``), so the tick's storage, recovery, witnessing and card
anchor are the cadence's own: a stamp stored but not finished is finished
first, as the same stamp, before any checkpoint is cut.

The functions here run under the shelf's lock; :class:`Cadence` calls them.
"""
from __future__ import annotations

import base64
import hashlib
import json
import os
import sqlite3
from dataclasses import dataclass, replace
from datetime import datetime, timedelta
from typing import Any

from agent_action_capsule.canonical import jcs
from cll.checkpoint import core

from . import cadence as _cadence
from . import shelf_facts
from .errors import CorruptError, InvalidError, NotFoundError
from .shelf import SHELF_VERSION_FACTS

__all__ = ["SHELF_VERSION_FACTS", "AudienceFact", "MIGRATION_BACKING_UP", "MIGRATION_DONE", "MIGRATION_MIGRATING",
           "openings"]


#: Migration states (``shelf_migration.state``).
MIGRATION_BACKING_UP, MIGRATION_MIGRATING, MIGRATION_DONE = "backing_up", "migrating", "done"

_INSERT_TICK = ("INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) "
                "VALUES (?, ?, ?, 0, 0, x'', ?)")


def migration_state(shelf: Any) -> str:
    """The stored migration state, ``""`` before any."""
    with shelf._read() as db:
        row = db.execute("SELECT state FROM shelf_migration WHERE id = 1").fetchone()
    return "" if row is None else row[0]


def _invalid(exc: shelf_facts.Invalid) -> InvalidError:
    return InvalidError(str(exc))


def _jcs(value: Any) -> bytes:
    try:
        return jcs(value)
    except Exception as exc:  # the AAC canonicalizer's own refusals
        raise InvalidError(f"canonical JSON: {exc}") from None


# -- Migrate ------------------------------------------------------------------------------


def migrate_locked(c: Any, now: datetime) -> None:
    """Go ``Cadence.Migrate``: each step recoverable, a call after a crash
    resumes from the stored state."""
    shelf = c._shelf
    state = migration_state(shelf)
    if state == MIGRATION_DONE:
        return
    if state == "":
        # Step 1: no tree root is ever appended after the marker, so a tick
        # stored but not finished is finished by today's code first.
        last = c._last_locked()
        if last is not None and last.checkpoint_size == 0:
            c._finish_locked(last)
        # Step 2, first transaction: from here no older build writes the file.
        with shelf._tx() as db:
            db.execute("UPDATE shelf_meta SET version = ? WHERE id = 1", (SHELF_VERSION_FACTS,))
            db.execute("INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, ?, '')",
                       (MIGRATION_BACKING_UP,))
        state = MIGRATION_BACKING_UP
    if state == MIGRATION_BACKING_UP:
        _backup_locked(shelf)
        _store_migration_batch_locked(c, now)
    # Step 4: append through the cadence log's own handle, as a stamp is.
    last = c._last_locked()
    if last is None or last.leaves.stamp is None:
        raise CorruptError("the migration's stamp is not stored")
    if last.checkpoint_size == 0:
        finish_stamp_locked(c, last)
    # Step 5.
    with shelf._tx() as db:
        db.execute("UPDATE shelf_migration SET state = ? WHERE id = 1", (MIGRATION_DONE,))


def _file_sha256(path: str) -> str:
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def _backup_locked(shelf: Any) -> None:
    """Write ``<path>.v1.bak`` (§4 step 2): a consistent snapshot by VACUUM
    INTO, set back to version 1 with no migration row, renamed into place
    only once its digest is recorded, and reused on a restart."""
    final, tmp = shelf.path + ".v1.bak", shelf.path + ".v1.bak.tmp"
    with shelf._read() as db:
        recorded = db.execute("SELECT backup_sha256 FROM shelf_migration WHERE id = 1").fetchone()[0]
    if recorded:
        try:
            got = _file_sha256(final)
        except OSError:
            got = None
        if got is not None:
            if got != recorded:
                raise InvalidError(f"{final} does not match the backup this migration recorded; move it aside")
            return
        try:
            got = _file_sha256(tmp)
        except OSError:
            got = None
        if got == recorded:
            os.rename(tmp, final)
            return
    if os.path.lexists(final):
        raise InvalidError(f"{final} exists and is not this migration's backup; move it aside")
    # A temporary file from an attempt that did not finish: VACUUM INTO
    # refuses an existing file.
    try:
        os.remove(tmp)
    except FileNotFoundError:
        pass
    try:
        with shelf._read() as db:
            db.execute("VACUUM INTO ?", (tmp,))
    except sqlite3.Error as exc:
        raise InvalidError(f"back up the shelf: {exc}") from None
    _reset_backup(tmp)
    digest = _file_sha256(tmp)
    with shelf._tx() as db:
        db.execute("UPDATE shelf_migration SET backup_sha256 = ? WHERE id = 1", (digest,))
    os.rename(tmp, final)


def _reset_backup(path: str) -> None:
    """Make the snapshot the version 1 file it was before step 2."""
    db = sqlite3.connect(path, isolation_level=None)
    try:
        for statement in ("UPDATE shelf_meta SET version = 1 WHERE id = 1", "DELETE FROM shelf_migration",
                          "PRAGMA journal_mode=DELETE"):
            try:
                db.execute(statement).fetchall()
            except sqlite3.Error as exc:
                raise InvalidError(f"reset the backup to version 1: {exc}") from None
    finally:
        db.close()


def _store_migration_batch_locked(c: Any, now: datetime) -> None:
    """Store the marker, the genesis facts and a stamp entry as the
    migration's stamp (§4 step 3), with state migrating, in one transaction."""
    shelf = c._shelf
    last = c._last_locked()
    last_tick = 0 if last is None else last.n
    at = _minute(now)
    sizes, facts = [], []
    for log_id in _cadence_log_ids(shelf):
        log = shelf._cadence_log_locked(log_id)
        size = log.mmr_size()
        sizes.append((log_id, size))
        if size == 0:
            continue
        facts.append(_fact_for(c, log, at, None))
    try:
        marker = shelf_facts.format_switch(last_tick, sizes)
    except shelf_facts.Invalid as exc:
        raise _invalid(exc) from None
    head = [_cadence.BatchEntry(shelf_facts.ENTRY_KIND_FORMAT_SWITCH, _jcs(marker))]
    tick = _build_stamp_locked(c, last_tick + 1, at, now, facts, head)
    with shelf._tx() as db:
        db.execute(_INSERT_TICK, (tick.n, _cadence._rfc3339(tick.at), _cadence._rfc3339(tick.due_next),
                                  tick.leaves.encode()))
        db.execute("UPDATE shelf_migration SET state = ? WHERE id = 1", (MIGRATION_MIGRATING,))


# -- facts --------------------------------------------------------------------------------


def _minute(now: datetime) -> datetime:
    ns = _cadence._ns(now)
    return _cadence._from_ns(ns - ns % (60 * _cadence._NS))


def _cadence_log_ids(shelf: Any) -> list[str]:
    """Every log of the lifetime table, sorted."""
    with shelf._read() as db:
        return [row[0] for row in db.execute("SELECT log_id FROM cadence_logs ORDER BY log_id")]


@dataclass(frozen=True)
class _LastFact:
    """The newest fact the shelf log holds for a log."""

    fact: dict | None = None
    link: Any = None
    status: Any = None

    @property
    def found(self) -> bool:
        return self.fact is not None


def _last_fact_of(shelf: Any, log_id: str) -> _LastFact:
    with shelf._read() as db:
        row = db.execute("SELECT doc, link_opening, status_opening FROM shelf_entries WHERE log_id = ? AND kind = ? "
                         "ORDER BY entry_seq DESC LIMIT 1", (log_id, shelf_facts.FACT_KIND_BOOK)).fetchone()
    if row is None:
        return _LastFact()
    try:
        fact = json.loads(bytes(row[0]))
        link, status = json.loads(bytes(row[1])), json.loads(bytes(row[2]))
        if not isinstance(fact, dict) or not isinstance(link, dict) or not isinstance(status, dict):
            raise ValueError("not a JSON object")
        return _LastFact(fact, link.get("value"), status.get("value"))
    except (ValueError, TypeError) as exc:
        raise CorruptError(f"a stored fact of {log_id}: {exc}") from None


def _book_id_of(shelf: Any, log: Any) -> str:
    """A log's book id (§2.2): its first record's id for a book, its first
    CLL entry's id for a host log."""
    from .shelf import _BookLog

    if isinstance(log, _BookLog):
        with shelf._read() as db:
            row = db.execute("SELECT record_id FROM book_records WHERE book_id = ? ORDER BY seq LIMIT 1",
                             (log.log_id,)).fetchone()
        if row is None:
            raise NotFoundError(f"book {log.log_id} holds no record")
        return row[0]
    entries = log.substrate.entries_after(0, 1)
    if not entries:
        raise InvalidError(f"log {log.log_id} has no entry")
    return bytes(entries[0].value).hex()


@dataclass(frozen=True)
class _PendingFact:
    """One log's fact before its salts are drawn."""

    profile: str
    book_id: str
    log_id: str
    size: int
    checkpoint_sha256: str
    prev_fact_size: int
    link: Any
    status: Any
    statement: bytes | None
    grown: bool


_EMPTY_LINK = {"last_received": None, "checked_over": []}


def _fact_for(c: Any, log: Any, at: datetime, last: _LastFact | None) -> _PendingFact:
    """What a log's fact would be now (§2.4); ``last`` is its previous fact,
    or ``None`` for a genesis fact."""
    shelf = c._shelf
    book_id = _book_id_of(shelf, log)
    profile, link, status = shelf_facts.PROFILE_NONE, None, None
    # Only a book is described by a profile; a host log's fact is always
    # profile "none" with the empty link value and {} (§2.4, §4).
    from .shelf import _BookLog

    if isinstance(log, _BookLog) and c._cfg.status is not None:
        claimed = c._cfg.status(book_id, log.log_id)
        if claimed is not None:
            profile, link, status = claimed
    size = log.mmr_size()
    found = last is not None and last.found
    prev = last.fact.get("size", 0) if found else 0
    grown = not found or size > prev
    statement = None
    if grown:
        cp = log.cut_local(at)
        statement, size = cp.statement, cp.tree_size
        checkpoint_sha256 = hashlib.sha256(statement).hexdigest()
    else:
        checkpoint_sha256 = last.fact.get("checkpoint_sha256", "")
    return _PendingFact(profile, book_id, log.log_id, size, checkpoint_sha256, prev, link, status, statement, grown)


def _link_value(link: Any) -> dict:
    """The link value as the design fixes it (Go ``linkValue``): checked,
    its checked-over digests sorted."""
    link = _EMPTY_LINK if link is None else link
    if not isinstance(link, dict):
        raise InvalidError("a fact's link value is {last_received, checked_over}")
    try:
        return shelf_facts._link(link.get("last_received"), list(link.get("checked_over") or []))
    except shelf_facts.Invalid as exc:
        raise _invalid(exc) from None


def _status_value(status: Any) -> dict:
    """The profile's status (Go ``statusValue``): absent is ``{}``, anything
    else a JSON object."""
    if status is None:
        return {}
    if not isinstance(status, dict):
        raise InvalidError("a fact's status is a JSON object")
    return status


def _changed(f: _PendingFact, last: _LastFact) -> bool:
    """Whether ``f``'s fact would differ from ``last`` (§2.4): the size grew,
    or a committed value differs. Values are compared, never commitments."""
    if not last.found or f.grown:
        return True
    return (_jcs(_link_value(f.link)) != _jcs(last.link)) or (_jcs(_status_value(f.status)) != _jcs(last.status))


def _opening_json(opening: shelf_facts.Opening) -> bytes:
    """An opening as stored: the RFC 8785 bytes of ``{"salt", "value"}``, as
    every entry document is."""
    return _jcs(opening.to_json())


def _build_stamp_locked(c: Any, n: int, at: datetime, now: datetime, facts: list[_PendingFact],
                        head: list) -> Any:
    """Draw each fact's salts in append order (facts in book id order), then
    the jitter, and return the stamp as a tick to store."""
    random = c._shelf.random
    facts = sorted(facts, key=lambda f: (f.book_id.encode(), f.log_id.encode()))
    entries = list(head)
    deals: dict[str, Any] = {}
    for f in facts:
        link, status = _link_value(f.link), _status_value(f.status)
        # Go validates before it draws; so does this, so a refusal draws nothing.
        _check_fact_input(f)
        salts = (random.bytes(32), random.bytes(32))
        try:
            fact = shelf_facts.book_fact(profile=f.profile, book_id=f.book_id, log_id=f.log_id, size=f.size,
                                         checkpoint_sha256=f.checkpoint_sha256, prev_fact_size=f.prev_fact_size,
                                         last_received=link["last_received"], checked_over=link["checked_over"],
                                         status=status, salts=salts)
        except shelf_facts.Invalid as exc:
            raise _invalid(exc) from None
        entries.append(_cadence.BatchEntry(shelf_facts.FACT_KIND_BOOK, _jcs(fact.doc), fact.doc["book_id"],
                                           fact.doc["log_id"], _opening_json(fact.link_opening),
                                           _opening_json(fact.status_opening)))
        statement = ""
        if f.statement is not None:
            statement = base64.urlsafe_b64encode(f.statement).rstrip(b"=").decode()
        deals[f.log_id] = _cadence.TickLeaf(f.log_id, f.size, f.checkpoint_sha256, "", 0, statement)
    try:
        stamp_doc = shelf_facts.stamp_entry(n, len(facts))
    except shelf_facts.Invalid as exc:
        raise _invalid(exc) from None
    entries.append(_cadence.BatchEntry(shelf_facts.ENTRY_KIND_STAMP, _jcs(stamp_doc)))
    jitter = 0
    jitter_ns = c._cfg.jitter // timedelta(microseconds=1) * 1000
    if jitter_ns > 0:
        jitter = random.uniform(2 * jitter_ns + 1) - jitter_ns
    interval_ns = c._cfg.interval // timedelta(microseconds=1) * 1000
    due = _cadence._ns(now) + interval_ns + jitter
    leaves = _cadence.TickLeaves(deals, 0, "", stamp=_cadence.StampBatch(1, tuple(entries)))
    return _cadence.CadenceTick(n, at, _cadence._from_ns(due - due % _cadence._NS), 0, 0, b"", leaves)


def _check_fact_input(f: _PendingFact) -> None:
    """Go ``newBookFact``'s checks before its draws."""
    if not (f.profile and f.book_id and f.log_id and f.checkpoint_sha256):
        raise InvalidError("a fact needs its profile, book, log and checkpoint")
    if f.size < f.prev_fact_size:
        raise InvalidError("a fact's size is below its previous fact's")
    if not (shelf_facts._digest(f.book_id) and shelf_facts._digest(f.checkpoint_sha256)):
        raise InvalidError("a fact's book id and checkpoint digest are 64 lowercase hex")


# -- Stamp --------------------------------------------------------------------------------


def stamp_locked(c: Any, now: datetime) -> Any:
    """Go ``Cadence.stampLocked``."""
    shelf = c._shelf
    if migration_state(shelf) != MIGRATION_DONE:
        raise InvalidError("the shelf is not a log of book facts yet; call migrate")
    last = c._last_locked()
    if last is not None and last.checkpoint_size == 0:
        return finish_stamp_locked(c, last)
    n = 1 if last is None else last.n + 1
    at = _minute(now)
    facts = []
    for log_id in _cadence_log_ids(shelf):
        log = shelf._cadence_log_locked(log_id)
        if log.mmr_size() == 0:
            continue
        prev = _last_fact_of(shelf, log_id)
        f = _fact_for(c, log, at, prev)
        if _changed(f, prev):
            facts.append(f)
    tick = _build_stamp_locked(c, n, at, now, facts, [])
    # Stored before anything is appended, so a crash resumes this stamp.
    with shelf._tx() as db:
        db.execute(_INSERT_TICK, (tick.n, _cadence._rfc3339(tick.at), _cadence._rfc3339(tick.due_next),
                                  tick.leaves.encode()))
    return finish_stamp_locked(c, tick)


def finish_stamp_locked(c: Any, tick: Any) -> Any:
    """Append the batch's entries not yet in the cadence log, in order,
    record each in ``shelf_entries``, then cut the checkpoint and complete
    the tick's row. Entries already appended (a crash after some) are found
    after the previous tick's checkpoint, never appended again."""
    shelf = c._shelf
    batch = tick.leaves.stamp
    after = 0
    if tick.n > 1:
        previous = c._tick_locked(tick.n - 1)
        # Every entry the previous tick's checkpoint covers, an old tick's
        # padding included.
        try:
            after = core.leaf_count(previous.checkpoint_size)
        except Exception:
            raise CorruptError(f"tick {previous.n}'s checkpoint size {previous.checkpoint_size} "
                               "is not a complete MMR") from None
    held = c._log.entries_after(after, len(batch.entries) + 1)
    last_seq = 0
    for i, e in enumerate(batch.entries):
        entry_id = hashlib.sha256(e.doc).hexdigest()
        if i < len(held):
            if bytes(held[i].value).hex() != entry_id:
                raise CorruptError(f"the cadence log holds another entry where stamp {tick.n}'s entry {i} belongs")
            seq = held[i].seq
        else:
            seq = c._log.append(entry_id, tick.at)
        with shelf._tx() as db:
            db.execute("INSERT OR IGNORE INTO shelf_entries (entry_seq, kind, book_id, log_id, doc, link_opening, "
                       "status_opening, tick) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                       (seq, e.kind, e.book_id, e.log_id, e.doc, e.link_opening or None, e.status_opening or None,
                        tick.n))
        last_seq = seq
    # One checkpoint over the whole batch, however long (Go CheckpointWhole).
    cp = c._log.checkpoint_whole(tick.at)
    with shelf._tx() as db:
        db.execute("UPDATE cadence_ticks SET entry_seq = ?, checkpoint_size = ?, checkpoint = ? WHERE tick = ?",
                   (last_seq, cp.tree_size, cp.statement, tick.n))
    tick = replace(tick, entry_seq=last_seq, checkpoint_size=cp.tree_size, checkpoint=cp.statement)
    return _cadence.TickResult(True, tick.due_next, tick)


# -- openings -----------------------------------------------------------------------------


@dataclass(frozen=True)
class AudienceFact:
    """One fact of a book with the openings an audience may have (``None``
    where it may not)."""

    entry_seq: int
    fact: dict
    link_opening: shelf_facts.Opening | None
    status_opening: shelf_facts.Opening | None


def openings(shelf: Any, book_id: str, audience: str) -> list[AudienceFact]:
    """Every fact of a book (by its book id) with the openings ``audience``
    may have, as :func:`shelf_facts.openings_for` says (Go
    ``Shelf.Openings``). It is the only call that hands openings out."""
    try:  # an unknown audience is refused first, whatever the book holds
        shelf_facts.openings_for(shelf_facts.FACT_KIND_BOOK, audience)
    except shelf_facts.Invalid as exc:
        raise InvalidError(str(exc)) from None
    with shelf._mu:
        shelf._check_open()
        with shelf._read() as db:
            rows = db.execute("SELECT entry_seq, kind, doc, link_opening, status_opening FROM shelf_entries "
                              "WHERE book_id = ? AND kind IN (?, ?) ORDER BY entry_seq",
                              (book_id, shelf_facts.FACT_KIND_BOOK, shelf_facts.FACT_KIND_JOURNAL)).fetchall()
        out = []
        for seq, kind, doc, lo, so in rows:
            try:
                want_link, want_status = shelf_facts.openings_for(kind, audience)
            except shelf_facts.Invalid as exc:
                raise _invalid(exc) from None
            try:
                fact = json.loads(bytes(doc))
                link = _opening(bytes(lo)) if want_link else None
                status = _opening(bytes(so)) if want_status else None
            except (ValueError, TypeError, KeyError) as exc:
                raise CorruptError(f"stored fact {seq}: {exc}") from None
            out.append(AudienceFact(seq, fact, link, status))
        return out


def _opening(raw: bytes) -> shelf_facts.Opening:
    value = json.loads(raw)
    return shelf_facts.Opening(value["salt"], value["value"])
