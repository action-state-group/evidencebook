# SPDX-License-Identifier: Apache-2.0
"""Shared fixtures of the received store's tests (evidencebook#461 §3): a
shelf with a receive config and a clock the test sets."""
from __future__ import annotations

import sqlite3
from datetime import datetime, timezone
from pathlib import Path

from capsule_emit.artifact.sqlite import SqliteArtifactStore

from evidencebook.errors import BusyError, DeletedError, InvalidError, OverLimitError, RefusedError
from evidencebook.shelf import Shelf

from test_shelf_stamp import script

START = datetime(2026, 10, 19, 12, 0, 30, tzinfo=timezone.utc)


class Clock:
    def __init__(self, at: datetime):
        self.t = at

    def __call__(self) -> datetime:
        return self.t


class ReceiveFixture:
    """One shelf file, opened as often as a test asks, under one receive
    config and one clock."""

    def __init__(self, directory: Path, receive=None, at: datetime = START):
        self.path = str(directory / "shelf.db")
        self.receive = receive
        self.clock = Clock(at)
        self.dbs: list[sqlite3.Connection] = []
        self.shelves: list[Shelf] = []

    def open(self) -> Shelf:
        db = script.open_db(self.path)
        self.dbs.append(db)
        originals = SqliteArtifactStore(db, "shelf", [script.SIGNER.public_key()])
        originals.init()
        shelf = Shelf.open_sqlite(self.path, operator="op", signer=script.SIGNER,
                                  checkpoint_key=script.CHECKPOINT_KEY, originals=originals, now=self.clock,
                                  lock_timeout=5, receive=self.receive)
        self.shelves.append(shelf)
        return shelf

    def close(self) -> None:
        for shelf in self.shelves:
            try:
                shelf.release()
            except Exception:  # noqa: BLE001 -- best effort at teardown
                pass
        for db in self.dbs:
            db.close()


def error_class(err: BaseException | None) -> str:
    """Go's ``errorClass`` of one error."""
    if err is None:
        return ""
    for cls, name in ((RefusedError, "refused"), (OverLimitError, "over_limit"), (BusyError, "busy"),
                      (DeletedError, "deleted"), (InvalidError, "invalid")):
        if isinstance(err, cls):
            return name
    return f"other: {err}"


def joined_class(errors: list[BaseException]) -> str:
    """Go's ``errorClass`` of ``errors.Join(errors...)``: the first class, in
    errorClass's order, that any of them is."""
    for name in ("refused", "over_limit", "busy", "deleted", "invalid"):
        if any(error_class(e) == name for e in errors):
            return name
    return error_class(errors[0]) if errors else ""
