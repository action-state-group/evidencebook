# SPDX-License-Identifier: Apache-2.0
import os
import sys
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent / "goose"))
os.environ.setdefault("CAPSULE_WITNESS", "off")


def needs_engine() -> bool:
    return bool(os.environ.get("CAPSULE_ENGINE") and os.environ.get("ARD_SKILLS"))


engine_only = pytest.mark.skipif(not needs_engine(), reason="CAPSULE_ENGINE and ARD_SKILLS name the evaluator and the pack compiler")
