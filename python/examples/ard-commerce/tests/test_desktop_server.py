# SPDX-License-Identifier: Apache-2.0
"""The Goose Desktop extension (desktop_server.py) on a fake rail: the tools in the story's order, a
refusal out of order, the LIMIT pick that pays nothing, and the pay step only after a sealed pick.

Needs ``CAPSULE_ENGINE`` and ``ARD_SKILLS`` like test_demo.py; the 96 sample deals take about 40 seconds."""
from __future__ import annotations

import pytest

from conftest import engine_only

pytestmark = engine_only


@pytest.fixture(scope="module")
def tools(tmp_path_factory):
    import os

    import seed
    from world import World

    home = tmp_path_factory.mktemp("desktop-home")
    with World(home) as world:
        seed.seed(world, say=lambda *_: None, kestrel_card="sentinel")
    os.environ["ARD_HOME"], os.environ["ARD_RAIL"] = str(home), "fake"
    import desktop_server as ds

    yield ds
    if ds._session is not None:
        ds._session.server.close()
        ds._session.world.close()
        ds._session = None


def test_out_of_order_calls_are_refused_and_change_nothing(tools):
    assert "REFUSED: call ard_find_sellers first" in tools.ard_show_history()
    assert "REFUSED: call ard_show_history first" in tools.ard_assess()


def test_the_story_find_history_assess_limit_then_pay(tools):
    found = tools.ard_find_sellers()
    assert "Alder Print Co." in found and "Kestrel Print" in found and "commitment verified" in found
    history = tools.ard_show_history()
    assert "48 deals counted" in history and "✗" not in history
    assert "History cards" in history and "3 of 48 deals did not hold" in history
    assessed = tools.ard_assess(50)
    assert "Alder Print Co.    GO:" in assessed.split("4b  Assessment again")[1]
    assert "Kestrel Print      LIMIT:" in assessed.split("4b  Assessment again")[1]

    assert "REFUSED: no sealed pick of Alder" in tools.ard_pay("alder")
    limit = tools.ard_pick("kestrel")
    assert "over this seller's limit" in limit and "no deal opens and nothing is paid" in limit
    assert "REFUSED: no sealed pick of Kestrel" in tools.ard_pay("kestrel")
    assert tools._session.run.rail.paid == []

    assert "within its limit" in tools.ard_pick("alder")
    paid = tools.ard_pay("alder")
    assert "invoice 289 msat" in paid and "Alder Print Co.'s card now counts 49 deals" in paid
    assert tools._session.run.rail.paid == [289]
    assert "REFUSED: Alder Print Co. was already paid" in tools.ard_pay("alder")


def test_an_unknown_seller_is_refused(tools):
    assert "REFUSED: unknown seller" in tools.ard_pick("nobody")


def test_a_second_server_on_the_same_home_is_refused(tools):
    tools.session()  # the first server holds the home
    with pytest.raises(RuntimeError, match="another Goose chat already runs"):
        tools.Session()
