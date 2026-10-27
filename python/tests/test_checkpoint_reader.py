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


def test_cbor2_decodes_no_tag_semantically_beyond_the_kept_list():
    """The reader keeps SEMANTIC_TAGS raw; a cbor2 that decodes another tag
    into a Python value would let it pass as the type it wraps."""
    import cbor2

    from evidencebook._cll import SEMANTIC_TAGS

    found = []
    for n in range(0, 70000):
        try:
            value = cbor2.loads(cbor2.dumps(cbor2.CBORTag(n, 0)))
        except Exception:  # noqa: BLE001 -- a tag cbor2 cannot decode over 0 is semantic too
            found.append(n)
            continue
        if not (isinstance(value, cbor2.CBORTag) and value.tag == n):
            found.append(n)
    assert tuple(found) == SEMANTIC_TAGS
