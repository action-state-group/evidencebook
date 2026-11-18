# SPDX-License-Identifier: Apache-2.0
"""The demo's admission pack: compile it, edit its threshold, and run the evaluator on it.

The pack is evidencebook-skills' ``packs/ard-demo-buyer`` (a demo pack derived from its ``admission-baseline``),
read from the ``ARD_SKILLS`` checkout, as the buyer skill there names it (evidencebook-skills#169).
Compiling is that repository's ``scripts/pack_compile.py`` (its ``--out`` must sit inside the
checkout, so the work goes under ``<checkout>/demo-ard/``); deciding is ``capsule-engine admission
evaluate``, run through :func:`evidencebook.evidence.evaluate`. Neither is reimplemented here."""
from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

HERE = Path(__file__).resolve().parent
PACK = Path("packs") / "ard-demo-buyer" / "pack-source.yaml"  # in the ARD_SKILLS checkout


@dataclass(frozen=True)
class Pack:
    pack_id: str
    version: str
    digest: str
    path: Path  # the compiled pack-definition.json
    max_adverse_permille: int
    source: str = ""  # the pack source text it was compiled from

    def tiers(self) -> list[dict]:
        return json.loads(self.path.read_text())["tiers"]

    def ceiling_msat(self, tier: str) -> int:
        """The most a deal may be in ``tier``: tier ceilings are sat-band decades (10^(decade+1) sat)."""
        for t in self.tiers():
            if t["id"] == tier:
                return int(10 ** (t["max_decade"]["sat"] + 1 + 3))
        raise KeyError(tier)


class PackError(RuntimeError):
    pass


def skills_checkout() -> Path:
    path = os.environ.get("ARD_SKILLS")
    if not path or not (Path(path) / "scripts" / "pack_compile.py").is_file():
        raise PackError("set ARD_SKILLS to an evidencebook-skills checkout (scripts/pack_compile.py)")
    return Path(path)


def engine() -> str:
    exe = os.environ.get("CAPSULE_ENGINE")
    if not exe or not os.access(exe, os.X_OK):
        raise PackError("set CAPSULE_ENGINE to a capsule-engine binary")
    return exe


def threshold(source: str) -> int:
    m = re.search(r"max_adverse_permille:\s*(\d+)", source)
    if not m:
        raise PackError("the pack source has no max_adverse_permille")
    return int(m.group(1))


def edited(source: str, permille: int, version: str) -> str:
    """The source with its threshold, ``pack_version`` and the ``pack_id`` suffix changed (lint
    requires the id to end in the version)."""
    out = re.sub(r"max_adverse_permille:\s*\d+", f"max_adverse_permille: {permille}", source)
    out = re.sub(r"(?m)^pack_version:.*$", f"pack_version: {version}", out)
    out = re.sub(r"(?m)^pack_id: (\S+)/[^/\s]+$", rf"pack_id: \1/{version}", out)
    return out


def compile_source(text: str, version: str, work: Path | None = None) -> Pack:
    """Compile ``text`` (a pack source) with the skills checkout's compiler. Returns the pack as the
    compiler printed it (id and ``definition_digest``)."""
    root = skills_checkout()
    out_dir = (work or root / "demo-ard") / f"v{version}"
    if out_dir.exists():
        shutil.rmtree(out_dir)
    out_dir.mkdir(parents=True)
    src = out_dir / "pack-source.yaml"
    src.write_text(text)
    run = subprocess.run([sys.executable, "scripts/pack_compile.py", "compile", str(src), "--out", str(out_dir / "compiled")],
                         cwd=root, capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=120)
    if run.returncode != 0:
        raise PackError(f"pack_compile refused the pack: {(run.stdout + run.stderr).strip()}")
    printed = json.loads(run.stdout)
    return Pack(printed["pack_id"], printed["pack_version"], printed["definition_digest"],
                out_dir / "compiled" / "pack-definition.json", threshold(text), text)


def source_path() -> Path:
    """The demo pack's source in the evidencebook-skills checkout."""
    path = skills_checkout() / PACK
    if not path.is_file():
        raise PackError(f"the ARD_SKILLS checkout has no {PACK}: use an evidencebook-skills commit that has the demo pack")
    return path


def starting() -> Pack:
    return compile_source(source_path().read_text(), "1.0.0")
