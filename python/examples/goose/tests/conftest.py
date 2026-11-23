# SPDX-License-Identifier: Apache-2.0
"""The tests drive real ``capsulectl-deal`` and ``capsule-engine`` binaries: with either unset (or not
installed) every test that needs one skips. ``GOOSE_CAPSULECTL_DEAL`` and ``GOOSE_CAPSULE_ENGINE`` name them."""
from __future__ import annotations

import os
import shutil
import sys
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE / "tests"))


def _have(env: str, default: str) -> bool:
    return bool(shutil.which(os.environ.get(env, default)))


needs_binaries = pytest.mark.skipif(
    not (_have("GOOSE_CAPSULECTL_DEAL", "capsulectl-deal") and _have("GOOSE_CAPSULE_ENGINE", "capsule-engine")),
    reason="capsulectl-deal and capsule-engine are not installed (GOOSE_CAPSULECTL_DEAL, GOOSE_CAPSULE_ENGINE)")


@pytest.fixture(autouse=True)
def _offline(monkeypatch):
    monkeypatch.setenv("GOOSE_NO_WITNESS", "1")  # a node with no public witness: nothing leaves the machine
