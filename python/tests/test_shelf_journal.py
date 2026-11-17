# SPDX-License-Identifier: Apache-2.0
"""The shelf's journal (evidencebook#461 §2.2 and §3), as Go's
shelf_journal_test.go checks it."""
from __future__ import annotations

import json
from datetime import timedelta

import pytest

from evidencebook.cadence import CadenceConfig
from evidencebook.errors import InvalidError
from evidencebook.shelf import JOURNAL_LOG_ID, _kind_of
from evidencebook.shelf_facts import FACT_KIND_JOURNAL, PROFILE_NONE, _digest, verify_openings
from evidencebook.shelf_handover import verify_handover

from test_shelf_stamp import START, fx, obs, script, stamp_cadence  # noqa: F401  (fx is a fixture)


def migrated_with_journal(shelf):
    """A migrated shelf with one deal book and a journal holding one record,
    stamped once after the journal's first record."""
    c, _ = stamp_cadence(shelf)
    shelf.open("deal/a").append(obs("a", "1"))
    c.tick(START)
    c.migrate(START + timedelta(minutes=1))
    journal = shelf.journal()
    journal.append(obs("journal", "1"))
    c.stamp(START + timedelta(minutes=2))
    return c, journal


def journal_facts(shelf) -> list[dict]:
    with shelf._read() as db:
        return [json.loads(bytes(doc)) for (doc,) in db.execute(
            "SELECT doc FROM shelf_entries WHERE kind = ? ORDER BY entry_seq", (FACT_KIND_JOURNAL,))]


def test_the_journal_gets_a_journal_fact_that_hides_its_size(fx):
    with fx.open() as shelf:
        migrated_with_journal(shelf)
        facts = journal_facts(shelf)
        assert len(facts) == 1
        f = facts[0]
        assert f["log_id"] == JOURNAL_LOG_ID
        assert f["size"] == 0 and f["checkpoint_sha256"] == "" and f["prev_fact_size"] == 0
        assert f["profile"] == PROFILE_NONE
        # The owner's opening holds the true size and checkpoint.
        got = shelf.openings(f["book_id"], "owner")
        assert len(got) == 1
        status = got[0].status_opening.value
        assert status["size"] > 0
        assert _digest(status["checkpoint_sha256"])
        verify_openings(got[0].fact, got[0].link_opening, got[0].status_opening)


def test_the_journal_gets_a_fact_only_when_it_grows(fx):
    """The journal follows the write rule: a fact only when it grew."""
    with fx.open() as shelf:
        c, journal = migrated_with_journal(shelf)
        c.stamp(START + timedelta(minutes=3))
        assert len(journal_facts(shelf)) == 1, "a quiet journal gets no fact"
        journal.append(obs("journal", "2"))
        c.stamp(START + timedelta(minutes=4))
        assert len(journal_facts(shelf)) == 2
        got = shelf.openings(journal_facts(shelf)[0]["book_id"], "owner")
        assert len(got) == 2
        first, second = got[0].status_opening.value, got[1].status_opening.value
        assert second["prev_fact_size"] == first["size"]
        assert second["size"] > first["size"]


def test_the_journal_is_in_no_count_and_no_handover(fx):
    """The journal counts toward no completeness and no tally: it is not one
    of the shelf's books, has no extent on a card, and a handover returns no
    latest fact for it."""
    with fx.open() as shelf:
        c, _ = migrated_with_journal(shelf)
        assert shelf.books() == ["deal/a"]
        anchor = c.standard_history_anchor(c.last().n)
        assert JOURNAL_LOG_ID not in anchor.extents
        assert "deal/a" in anchor.extents
        h = c.handover(0)
        got = verify_handover(h, script.CHECKPOINT_KEY.public_key().hex(), script.DIRECTORY)
        assert all(x.fact["log_id"] != JOURNAL_LOG_ID for x in got.latest)
        for d in h.documents:
            if json.loads(d.doc)["kind"] == FACT_KIND_JOURNAL:
                assert d.status_opening is None, "the journal's opening is never handed over"


def test_the_journal_is_opened_only_as_the_journal(fx):
    with fx.open() as shelf:
        _, journal = migrated_with_journal(shelf)
        assert shelf.journal() is journal
        with pytest.raises(InvalidError):
            shelf.open(JOURNAL_LOG_ID)
        with pytest.raises(InvalidError):
            shelf.host_log(JOURNAL_LOG_ID, True)


def test_the_journal_needs_a_shelf_of_book_facts(fx):
    with fx.open() as shelf:
        with pytest.raises(InvalidError):
            shelf.journal()


def test_the_journal_refuses_a_log_that_already_has_its_id(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open(JOURNAL_LOG_ID).append(obs("a", "1"))
        c.tick(START)
        c.migrate(START + timedelta(minutes=1))
        with pytest.raises(InvalidError):
            shelf.journal()


def test_the_journal_refuses_a_book_on_disk_that_holds_its_id(fx):
    """The same, on a reopened shelf, where the book is not cached."""
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open(JOURNAL_LOG_ID).append(obs("a", "1"))
        c.tick(START)
        c.migrate(START + timedelta(minutes=1))
    with fx.open() as shelf:
        with pytest.raises(InvalidError, match="already a book"):
            shelf.journal()


def test_an_older_shelf_logs_table_takes_the_journal(fx):
    """A file whose shelf_logs predates the journal's kind is rebuilt with the
    same rows when the journal is first opened."""
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        c.tick(START)
        c.migrate(START + timedelta(minutes=1))
        with shelf._tx() as db:
            for statement in (
                    "CREATE TABLE shelf_logs_old (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL "
                    "CHECK (kind IN ('book', 'host')))",
                    "INSERT INTO shelf_logs_old SELECT log_id, kind FROM shelf_logs",
                    "DROP TABLE shelf_logs",
                    "ALTER TABLE shelf_logs_old RENAME TO shelf_logs"):
                db.execute(statement)
        shelf.journal()
        with shelf._read() as db:
            assert _kind_of(db, "deal/a") == "book"
            assert _kind_of(db, JOURNAL_LOG_ID) == "journal"


def test_the_journal_refuses_the_cadence_logs_id(fx):
    """A shelf whose cadence log is named as the journal refuses the journal
    and changes nothing, in this process and after a reopen with no cadence
    open (Go ``TestTheJournalRefusesTheCadenceLogsID``)."""
    config = CadenceConfig(log_id=JOURNAL_LOG_ID, ended=lambda _: False, witnesses=script.DIRECTORY)
    with fx.open() as shelf:
        c = shelf.cadence(config)
        shelf.open("deal/a").append(obs("a", "1"))
        c.tick(START)
        c.migrate(START + timedelta(minutes=1))
        with pytest.raises(InvalidError, match="cadence log"):
            shelf.journal()
    with fx.open() as shelf:
        with pytest.raises(InvalidError, match="another log's entries"):
            shelf.journal()
        with shelf._read() as db:
            assert _kind_of(db, JOURNAL_LOG_ID) == "", "nothing was claimed"
        shelf.cadence(config)  # the cadence still opens
