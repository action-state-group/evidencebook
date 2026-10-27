# SPDX-License-Identifier: Apache-2.0
"""The checkpoint reader's verdict on every case of the differential test
(test/interop/python-evidencebook/checkpoint_parity.py) where the spec
decides against Go's reader. CI runs the whole differential against Go."""
from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest

_PATH = Path(__file__).resolve().parents[2] / "test" / "interop" / "python-evidencebook" / "checkpoint_parity.py"
_spec = importlib.util.spec_from_file_location("checkpoint_parity", _PATH)
parity = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(parity)

CASES = parity.cases()


def test_the_base_checkpoints_are_read():
    assert parity.python_verdict(CASES["base-first"]) == "accept"
    assert parity.python_verdict(CASES["base-second"]) == "accept"


@pytest.mark.parametrize("name", [n for n in CASES if parity.departure(n)])
def test_where_go_departs_the_reader_gives_the_specs_verdict(name):
    verdict = parity.python_verdict(CASES[name])
    assert ("accept" if verdict == "accept" else "refuse") == parity.departure(name)[0], verdict


def test_every_go_departure_names_a_case():
    names = set(CASES)
    for case, _, _ in parity.GO_DEPARTS:
        assert case in names or {f"first-{case}", f"second-{case}"} & names, case
