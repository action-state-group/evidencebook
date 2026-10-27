# SPDX-License-Identifier: Apache-2.0
"""Where a book keeps its records and checkpoints.

A record is stored whole (its canonical header, its sealed capsule, its
Producer Envelope and its payload octets) before it is admitted, so the log
never commits an identity the book cannot produce. The log itself is
rebuilt from the stored records when a book opens.
"""
from __future__ import annotations

import base64
import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Protocol

from .errors import CorruptError

__all__ = ["StoredRecord", "Store", "MemoryStore", "FileStore"]


@dataclass(frozen=True)
class StoredRecord:
    seq: int
    record_id: str
    header: bytes  # the header's JCS
    capsule: dict
    envelope: bytes
    payloads: tuple[bytes, ...] = field(default=(), repr=False)


class Store(Protocol):
    def put_record(self, record: StoredRecord) -> None: ...

    def records(self) -> list[StoredRecord]: ...

    def put_checkpoint(self, statement: bytes) -> None: ...

    def checkpoints(self) -> list[bytes]: ...

    def close(self) -> None: ...


class MemoryStore:
    """A store that lasts as long as the process."""

    def __init__(self) -> None:
        self._records: dict[str, StoredRecord] = {}
        self._checkpoints: list[bytes] = []

    def put_record(self, record: StoredRecord) -> None:
        existing = self._records.get(record.record_id)
        if existing is not None:
            if existing.seq != record.seq or existing.capsule != record.capsule:
                raise CorruptError(f"record {record.record_id} already stored with different content")
            return
        self._records[record.record_id] = record

    def records(self) -> list[StoredRecord]:
        return sorted(self._records.values(), key=lambda r: r.seq)

    def put_checkpoint(self, statement: bytes) -> None:
        if statement not in self._checkpoints:
            self._checkpoints.append(bytes(statement))

    def checkpoints(self) -> list[bytes]:
        return list(self._checkpoints)

    def close(self) -> None:
        pass


def _b64(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii")


class FileStore(MemoryStore):
    """Two append-only JSONL journals in ``directory``: ``records.jsonl`` and
    ``checkpoints.jsonl``, readable only by their owner. A torn final line left
    by a crash is truncated; a malformed complete line is corruption. The
    journals are this package's own format, not Go's ``FileStore``'s: a
    record line also carries its payloads, and a checkpoint line only its
    signed statement, from which everything else is read."""

    def __init__(self, directory: str | os.PathLike):
        super().__init__()
        self._broken = False
        root = Path(directory)
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self._record_file = self._open(root / "records.jsonl", self._load_record)
        self._checkpoint_file = self._open(root / "checkpoints.jsonl", self._load_checkpoint)

    def _load_record(self, line: dict) -> None:
        MemoryStore.put_record(self, StoredRecord(
            seq=line["seq"], record_id=line["record_id"], header=line["header"].encode("utf-8"),
            capsule=line["capsule"], envelope=base64.b64decode(line["envelope"], validate=True),
            payloads=tuple(base64.b64decode(p, validate=True) for p in line.get("payloads", ())),
        ))

    def _load_checkpoint(self, line: dict) -> None:
        MemoryStore.put_checkpoint(self, base64.b64decode(line["statement"], validate=True))

    @staticmethod
    def _open(path: Path, apply):
        file = os.fdopen(os.open(path, os.O_RDWR | os.O_CREAT | os.O_APPEND, 0o600), "a+b")
        file.seek(0)
        offset = 0
        for raw in file.read().splitlines(keepends=True):
            if not raw.endswith(b"\n"):
                file.truncate(offset)
                break
            try:
                apply(json.loads(raw))
            except (ValueError, KeyError, TypeError) as exc:
                file.close()
                raise CorruptError(f"{path.name} at offset {offset}: {exc}") from None
            offset += len(raw)
        file.seek(0, os.SEEK_END)
        return file

    def _append(self, file, value: dict) -> None:
        # A write that fails part way may or may not have reached the disk, so
        # the journal takes no further write: reopening it reads what is there.
        if self._broken:
            raise CorruptError("an earlier journal write failed; reopen the store")
        try:
            file.write(json.dumps(value, separators=(",", ":"), ensure_ascii=False).encode("utf-8") + b"\n")
            file.flush()
            os.fsync(file.fileno())
        except BaseException:
            self._broken = True
            raise

    def put_record(self, record: StoredRecord) -> None:
        if record.record_id in self._records:
            return super().put_record(record)
        self._append(self._record_file, {
            "seq": record.seq, "record_id": record.record_id, "header": record.header.decode("utf-8"),
            "capsule": record.capsule, "envelope": _b64(record.envelope), "payloads": [_b64(p) for p in record.payloads],
        })
        super().put_record(record)

    def put_checkpoint(self, statement: bytes) -> None:
        if statement in self._checkpoints:
            return
        self._append(self._checkpoint_file, {"statement": _b64(statement)})
        super().put_checkpoint(statement)

    def close(self) -> None:
        self._record_file.close()
        self._checkpoint_file.close()
