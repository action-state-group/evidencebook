# SPDX-License-Identifier: Apache-2.0
"""Each Goose window's recipe is thin: the party's facts and the rail, then the skill it follows, read from the
evidencebook-skills checkout (evidencebook-skills#169). No engine needed."""
from __future__ import annotations

import base64
import importlib.util
import json
import os
import re
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
_spec = importlib.util.spec_from_file_location("open_windows", HERE / "goose" / "open-windows.py")
ow = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(ow)


@pytest.fixture
def skills(tmp_path, monkeypatch):
    for name in ("buy-with-evidence", "sell-with-evidence"):
        d = tmp_path / "skills" / name
        d.mkdir(parents=True)
        (d / "SKILL.md").write_text(f"---\nname: {name}\ndescription: x\n---\n# {name} body\n\nThe {name} procedure.\n")
    monkeypatch.setenv("ARD_SKILLS", str(tmp_path))
    monkeypatch.setenv("CAPSULE_ENGINE", "/bin/true")
    return tmp_path


def _recipe(link: str) -> dict:
    return json.loads(base64.urlsafe_b64decode(link.split("config=", 1)[1]))


def test_maya_follows_the_buyer_skill_with_her_facts_and_the_rail(skills):
    text = _recipe(ow.link("maya", "fake"))["instructions"]
    assert text.startswith("You are Maya's buying agent. Maya needs 200 conference badges by Friday, under 300 msat.")
    assert "no money moves" in text and "Follow the buy-with-evidence skill below" in text
    assert "The buy-with-evidence procedure." in text and "name: buy-with-evidence" not in text


def test_a_real_rail_says_real_money_moves(skills):
    assert "real money moves" in _recipe(ow.link("maya", "lightning"))["instructions"]


def test_the_sellers_follow_the_seller_skill(skills):
    for role in ("alder", "kestrel"):
        r = _recipe(ow.link(role, "fake"))
        assert "Follow the sell-with-evidence skill below" in r["instructions"]
        assert "The sell-with-evidence procedure." in r["instructions"]
        assert r["extensions"][0]["name"] == f"ard_{role}"


def test_the_recipe_carries_nothing_of_its_own_beyond_the_facts(skills):
    text = _recipe(ow.link("maya", "fake"))["instructions"]
    own = text.split("Follow the buy-with-evidence skill below", 1)[0]
    assert len(own.splitlines()) <= 3, own


@pytest.mark.skipif(not os.environ.get("ARD_SKILLS"), reason="ARD_SKILLS names the evidencebook-skills checkout")
def test_every_demo_tool_a_skill_names_is_a_tool_of_this_demo():
    tools = set(re.findall(r'^def (ard_\w+)\(', (HERE / "desktop_server.py").read_text(), re.M))
    root = Path(os.environ["ARD_SKILLS"]) / "skills"
    for name in ("buy-with-evidence", "sell-with-evidence"):
        named = set(re.findall(r"`(ard_\w+)`", (root / name / "SKILL.md").read_text()))
        assert named and named <= tools, (name, named - tools)
