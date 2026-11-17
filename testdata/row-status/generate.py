# SPDX-License-Identifier: Apache-2.0
# Usage: python3 testdata/row-status/generate.py testdata/row-status/vectors.json
# Writes testdata/row-status/vectors.json. Every "expect" below is written by hand from the rule
# (evidencebook#461 acceptance 6; the design's section 4.2), never computed by an implementation.
import json, sys, copy
P = ["buyer", "seller"]
def at(s): return {"at": s}
def after(n, sec): return {"after": n, "seconds": sec}
D = 86400
ROW1 = {"id": "paid_as_agreed", "owed_by": "buyer", "due": at("2026-11-01T23:59:59Z"), "window_end": at("2026-11-04T23:59:59Z")}
ROW2 = {"id": "delivered", "owed_by": "seller", "due": after("buyer_payment", 4*D), "window_end": after("buyer_payment", 7*D)}
ROW3 = {"id": "as_described", "owed_by": "seller", "due": after("buyer_receipt", 0), "window_end": after("buyer_receipt", 14*D)}
PAY = {"buyer_payment": {"time": "2026-11-01T12:00:00Z", "latest": "2026-11-04T23:59:59Z"}}
RCPT = {"buyer_receipt": {"time": "2026-11-04T15:00:00Z", "latest": "2026-12-01T23:59:59Z"}}
R1D = {"due": "2026-11-01T23:59:59Z", "window_end": "2026-11-04T23:59:59Z"}
R2D = {"due": "2026-11-05T12:00:00Z", "window_end": "2026-11-08T12:00:00Z"}
R3D = {"due": "2026-11-04T15:00:00Z", "window_end": "2026-11-18T15:00:00Z"}
def st(side, value, time): return {"side": side, "value": value, "time": time}
def res(status, dates, value=None, remedy=None, local=False):
    r = {"status": status, "local": local}
    if value: r["value"] = value
    if remedy: r["remedy"] = remedy
    r.update(dates)
    return r
cases = []
def case(name, why, at_, row, expect, **kw):
    inp = {"at": at_, "parties": P, "row": row}
    inp.update(kw)
    cases.append({"name": name, "why": why, "input": inp, "expect": expect})

# Rule 6 and the due date, inclusive.
case("r6-not-due-at-due-date", "no statement, T is the due date itself: not_due (up to and including)", "2026-11-01T23:59:59Z", ROW1, res("not_due", R1D))
case("r6-no-statement-after-due", "no statement, T one second after the due date", "2026-11-02T00:00:00Z", ROW1, res("no_statement", R1D))
case("r6-cannot-tell-is-none", "cannot_tell counts as no statement", "2026-11-02T00:00:00Z", ROW1, res("no_statement", R1D),
     statements=[st("buyer", "cannot_tell", "2026-11-01T12:00:00Z")])
case("r6-record-after-T-not-held", "a statement whose time is after T does not exist yet", "2026-11-01T11:00:00Z", ROW1, res("not_due", R1D),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z")])
# Rule 5.
case("r5-waiting", "one side met, inside the window", "2026-11-01T13:00:00Z", ROW1, res("waiting", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z")])
case("r5-waiting-at-window-end", "T is the window end itself: still inside", "2026-11-04T23:59:59Z", ROW1, res("waiting", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z")])
case("r5-uncontested", "one side met, one second after the window end", "2026-11-05T00:00:00Z", ROW1, res("uncontested", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z")])
case("r5-latest-cannot-tell-clears", "the side's latest in-time statement is cannot_tell, so it has none; the other side's met stands alone", "2026-11-02T00:00:00Z", ROW1, res("waiting", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z"), st("buyer", "cannot_tell", "2026-11-01T13:00:00Z"), st("seller", "met", "2026-11-01T12:30:00Z")])
case("r5-late-statement-ignored", "a statement after the window does not count and is not a concession (it is not the other side's value)", "2026-11-06T00:00:00Z", ROW1, res("uncontested", R1D, "met"),
     statements=[st("seller", "met", "2026-11-01T12:30:00Z"), st("buyer", "not_met", "2026-11-05T10:00:00Z")])
# Rule 4.
case("r4-agreed-so-far", "Appendix A, 1 Nov: both say row 1 met, inside the window", "2026-11-01T13:00:00Z", ROW1, res("agreed_so_far", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "met", "2026-11-01T12:30:00Z")])
case("r4-agreed", "Appendix A: row 1 agreed from 5 Nov", "2026-11-05T00:00:00Z", ROW1, res("agreed", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "met", "2026-11-01T12:30:00Z")])
case("r4-agreed-not-met", "short case 2: both say not met, after the window: agreed, value not_met", "2026-11-19T00:00:00Z", ROW3, res("agreed", R3D, "not_met"),
     named=RCPT, statements=[st("buyer", "not_met", "2026-11-10T10:00:00Z"), st("seller", "not_met", "2026-11-11T10:00:00Z")])
# Rule 3.
case("r3-disagreement", "Appendix A, 10 Nov: buyer not met, seller met, both in time", "2026-11-10T12:00:00Z", ROW3, res("in_disagreement", R3D),
     named=RCPT, statements=[st("seller", "met", "2026-11-04T14:00:00Z"), st("buyer", "not_met", "2026-11-10T10:00:00Z"), st("seller", "met", "2026-11-10T11:00:00Z")])
case("r3-failed-check-overrides-agreement", "short case 1: both say met, the recomputed check (legs match) fails", "2026-11-02T00:00:00Z", dict(ROW1, recomputed=True), res("in_disagreement", R1D),
     check="fail", statements=[st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "met", "2026-11-01T12:30:00Z")])
case("r3-failed-check-one-statement", "a failed recomputed check with one statement is still in disagreement", "2026-11-02T00:00:00Z", dict(ROW1, recomputed=True), res("in_disagreement", R1D),
     check="fail", statements=[st("buyer", "met", "2026-11-01T12:00:00Z")])
case("r4-passed-check", "a passing check changes nothing", "2026-11-02T00:00:00Z", dict(ROW1, recomputed=True), res("agreed_so_far", R1D, "met"),
     check="pass", statements=[st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "met", "2026-11-01T12:30:00Z")])
# Concessions.
DIS1 = [st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "not_met", "2026-11-02T10:00:00Z")]
case("cn-before-window-end-is-a-change", "inside the window a new statement simply replaces the side's own", "2026-11-03T12:00:00Z", ROW1, res("agreed_so_far", R1D, "met"),
     statements=DIS1 + [st("seller", "met", "2026-11-03T10:00:00Z")])
case("cn-disagreement-after-window", "the in-time values differ after the window", "2026-11-06T00:00:00Z", ROW1, res("in_disagreement", R1D), statements=DIS1)
case("cn-concession-counts", "after the window the seller gives the buyer's in-time value: agreed", "2026-11-07T00:00:00Z", ROW1, res("agreed", R1D, "met"),
     statements=DIS1 + [st("seller", "met", "2026-11-06T10:00:00Z")])
case("cn-concession-from-none", "a side with no in-time statement concedes to the other's value", "2026-11-07T00:00:00Z", ROW1, res("agreed", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "met", "2026-11-06T10:00:00Z")])
case("cn-cannot-tell-is-not-a-concession", "a late cannot_tell concedes nothing", "2026-11-07T00:00:00Z", ROW1, res("in_disagreement", R1D),
     statements=DIS1 + [st("seller", "cannot_tell", "2026-11-06T10:00:00Z")])
case("cn-only-first-counts", "the buyer concedes first; the seller's later concession does not count", "2026-11-07T00:00:00Z", ROW1, res("agreed", R1D, "not_met"),
     statements=DIS1 + [st("buyer", "not_met", "2026-11-06T10:00:00Z"), st("seller", "met", "2026-11-06T11:00:00Z")])
case("cn-simultaneous-count-none", "both concede at the same record time: neither counts", "2026-11-07T00:00:00Z", ROW1, res("in_disagreement", R1D),
     statements=DIS1 + [st("buyer", "not_met", "2026-11-06T10:00:00Z"), st("seller", "met", "2026-11-06T10:00:00Z")])
case("cn-no-concession-profile", "a profile with no concession (Mesh): the late statement does not count", "2026-11-07T00:00:00Z", ROW1, res("in_disagreement", R1D),
     no_concession=True, statements=DIS1 + [st("seller", "met", "2026-11-06T10:00:00Z")])
# Rule 2.
R3ST = [st("seller", "met", "2026-11-04T14:00:00Z"), st("buyer", "not_met", "2026-11-10T10:00:00Z"), st("seller", "met", "2026-11-10T11:00:00Z")]
DSP = [{"side": "buyer", "time": "2026-11-12T10:00:00Z", "seen_in_time": True}]
case("r2-in-dispute", "Appendix A, 12 Nov: the buyer's dispute", "2026-11-12T12:00:00Z", ROW3, res("in_dispute", R3D), named=RCPT, statements=R3ST, disputes=DSP)
case("r2-withdrawn", "the claimant withdraws: the rules below apply", "2026-11-12T13:00:00Z", ROW3, res("in_disagreement", R3D), named=RCPT, statements=R3ST, disputes=DSP,
     withdrawals=[{"side": "buyer", "time": "2026-11-12T12:30:00Z"}])
case("r2-withdrawal-before-dispute", "a withdrawal is final for its side whichever was sealed first", "2026-11-12T13:00:00Z", ROW3, res("in_disagreement", R3D), named=RCPT, statements=R3ST, disputes=DSP,
     withdrawals=[{"side": "buyer", "time": "2026-11-12T09:00:00Z"}])
case("r2-other-side-withdrawal-no-effect", "only the claimant can withdraw its dispute", "2026-11-12T13:00:00Z", ROW3, res("in_dispute", R3D), named=RCPT, statements=R3ST, disputes=DSP,
     withdrawals=[{"side": "seller", "time": "2026-11-12T12:30:00Z"}])
case("r2-nonfinal-ruling", "Appendix A, 13 Nov: a ruling not yet final leaves the row in dispute", "2026-11-13T12:00:00Z", ROW3, res("in_dispute", R3D), named=RCPT, statements=R3ST, disputes=DSP,
     rulings=[{"final": False, "value": "not_met", "time": "2026-11-13T10:00:00Z", "remedy_due": "2026-11-27T23:59:59Z"}])
AGR1 = [st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "met", "2026-11-01T12:30:00Z")]
case("r2-late-dispute", "after the window, on an agreed row, by a claimant that had seen the statement in time: late, no effect", "2026-11-07T00:00:00Z", ROW1, res("agreed", R1D, "met"),
     statements=AGR1, disputes=[{"side": "seller", "time": "2026-11-06T10:00:00Z", "seen_in_time": True}])
case("r2-late-dispute-unseen", "the claimant had not named the statement before the window ended: not late", "2026-11-07T00:00:00Z", ROW1, res("in_dispute", R1D),
     statements=AGR1, disputes=[{"side": "seller", "time": "2026-11-06T10:00:00Z", "seen_in_time": False}])
case("r2-late-dispute-failed-check", "a row whose check fails can always be disputed", "2026-11-07T00:00:00Z", dict(ROW1, recomputed=True), res("in_dispute", R1D),
     check="fail", statements=AGR1, disputes=[{"side": "seller", "time": "2026-11-06T10:00:00Z", "seen_in_time": True}])
case("r2-late-dispute-on-disagreement", "in-time statements that differ are neither agreed nor uncontested: not late", "2026-11-07T00:00:00Z", ROW1, res("in_dispute", R1D),
     statements=DIS1, disputes=[{"side": "buyer", "time": "2026-11-06T10:00:00Z", "seen_in_time": True}])
case("r2-late-dispute-uncontested", "after the window, on an uncontested row: late", "2026-11-07T00:00:00Z", ROW1, res("uncontested", R1D, "met"),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z")], disputes=[{"side": "seller", "time": "2026-11-06T10:00:00Z", "seen_in_time": True}])
case("r2-late-dispute-ignores-concession", "lateness reads the in-time statements alone: a later concession does not make a dispute late", "2026-11-08T00:00:00Z", ROW1, res("in_dispute", R1D),
     statements=DIS1 + [st("seller", "met", "2026-11-06T10:00:00Z")], disputes=[{"side": "seller", "time": "2026-11-07T10:00:00Z", "seen_in_time": True}])
FR = {"final": True, "value": "not_met", "time": "2026-11-21T10:00:00Z", "remedy_due": "2026-11-27T23:59:59Z"}
case("r2-conflicting-final-rulings", "two forks of the case book with different first final rulings: no ruling applies, in dispute", "2026-11-22T00:00:00Z", ROW3, res("in_dispute", R3D),
     named=RCPT, statements=R3ST, rulings=[FR], conflicting_final_rulings=True)
# Rule 1.
NF = {"final": False, "value": "not_met", "time": "2026-11-13T10:00:00Z", "remedy_due": "2026-11-27T23:59:59Z"}
case("r1-ruled-remedy-due", "Appendix A, 21 Nov: final ruling, remedy due by 27 Nov", "2026-11-21T12:00:00Z", ROW3, res("ruled", R3D, "not_met", "due"),
     named=RCPT, statements=R3ST, disputes=DSP, rulings=[NF, FR])
case("r1-ruled-remedy-made", "Appendix A, 25 Nov: the refund is made", "2026-11-25T12:00:00Z", ROW3, res("ruled", R3D, "not_met", "made"),
     named=RCPT, statements=R3ST, disputes=DSP, rulings=[NF, FR], remedies=[{"time": "2026-11-25T10:00:00Z"}])
case("r1-ruled-not-honoured", "the remedy date passed without one", "2026-11-28T00:00:00Z", ROW3, res("ruled", R3D, "not_met", "not_honoured"),
     named=RCPT, statements=R3ST, disputes=DSP, rulings=[NF, FR])
case("r1-ruled-late-remedy", "a remedy after its date: the date passed without one", "2026-11-29T00:00:00Z", ROW3, res("ruled", R3D, "not_met", "not_honoured"),
     named=RCPT, statements=R3ST, disputes=DSP, rulings=[NF, FR], remedies=[{"time": "2026-11-28T10:00:00Z"}])
case("r1-first-final-wins", "of the named adjudicator's rulings only the first marked final counts", "2026-11-23T00:00:00Z", ROW3, res("ruled", R3D, "met", "none"),
     named=RCPT, statements=R3ST, disputes=DSP, rulings=[{"final": True, "value": "met", "time": "2026-11-21T10:00:00Z"}, dict(FR, time="2026-11-22T10:00:00Z")])
case("r1-ruled-without-dispute", "a final ruling decides the row whatever else holds", "2026-11-23T00:00:00Z", ROW3, res("ruled", R3D, "met", "none"),
     named=RCPT, statements=R3ST, rulings=[{"final": True, "value": "met", "time": "2026-11-21T10:00:00Z"}])
# Dates that hang on a record.
case("d-not-due-until-named-record", "Appendix A, 4 Nov: the seller said met, but row 3 is not due until the receipt record has a time", "2026-11-04T14:30:00Z", ROW3,
     res("not_due", {}), named=RCPT, statements=[st("seller", "met", "2026-11-04T14:00:00Z")])
case("d-waiting-after-receipt", "Appendix A, 4 Nov: once the receipt has a time, row 3 waits for the buyer, window to 18 Nov", "2026-11-04T16:00:00Z", ROW3,
     res("waiting", R3D, "met"), named=RCPT, statements=[st("seller", "met", "2026-11-04T14:00:00Z")])
case("d-sound-item-uncontested", "the item arrived sound and nobody wrote again: uncontested after 18 Nov", "2026-11-18T15:00:01Z", ROW3,
     res("uncontested", R3D, "met"), named=RCPT, statements=[st("seller", "met", "2026-11-04T14:00:00Z")])
case("d-row2-agreed-so-far", "Appendix A, 4 Nov: row 2 due 5 Nov (4 days after the payment record), agreed so far", "2026-11-04T16:00:00Z", ROW2,
     res("agreed_so_far", R2D, "met"), named=PAY, statements=[st("seller", "met", "2026-11-04T14:00:00Z"), st("buyer", "met", "2026-11-04T15:00:00Z")])
case("d-row2-agreed", "row 2 agreed once its window, 3 days after its due date, has ended", "2026-11-08T12:00:01Z", ROW2,
     res("agreed", R2D, "met"), named=PAY, statements=[st("seller", "met", "2026-11-04T14:00:00Z"), st("buyer", "met", "2026-11-04T15:00:00Z")])
NORCPT = {"buyer_receipt": {"time": None, "latest": "2026-12-01T23:59:59Z"}}
case("d-unset-up-to-latest", "no receipt record yet, T is the latest date: not due", "2026-12-01T23:59:59Z", ROW3, res("not_due", {}), named=NORCPT)
case("d-not-reached", "no receipt record by its latest date: not reached", "2026-12-02T00:00:00Z", ROW3, res("not_reached", {}), named=NORCPT,
     statements=[st("seller", "met", "2026-11-04T14:00:00Z")])
case("d-named-after-latest", "a payment record whose time is after its latest date does not set the dates", "2026-11-06T00:00:00Z", ROW2, res("not_reached", {}),
     named={"buyer_payment": {"time": "2026-11-05T10:00:00Z", "latest": "2026-11-04T23:59:59Z"}})
case("d-named-after-T", "the named record exists only from its time: as of an earlier T the date is unset", "2026-11-01T11:00:00Z", ROW2, res("not_due", {}), named=PAY)
# Local results.
case("l-untimed-statement", "a statement with no time counts, and the result is local", "2026-11-01T13:00:00Z", ROW1, res("waiting", R1D, "met", local=True),
     statements=[st("buyer", "met", None)])
case("l-untimed-after-late", "an untimed statement after one of the same side that is already late is late too", "2026-11-06T00:00:00Z", ROW1, res("uncontested", R1D, "met"),
     statements=[st("seller", "met", "2026-11-01T12:30:00Z"), st("buyer", "not_met", "2026-11-05T10:00:00Z"), st("buyer", "met", None)])
case("l-untimed-dispute", "a dispute with no time is never late and makes the result local", "2026-11-07T00:00:00Z", ROW1, res("in_dispute", R1D, local=True),
     statements=AGR1, disputes=[{"side": "seller", "time": None, "seen_in_time": True}])

invalid = [
    {"name": "unknown-party", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "statements": [st("carol", "met", "2026-11-01T12:00:00Z")]}},
    {"name": "bad-value", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "statements": [st("buyer", "yes", "2026-11-01T12:00:00Z")]}},
    {"name": "fractional-second", "input": {"at": "2026-11-02T00:00:00.5Z", "parties": P, "row": ROW1}},
    {"name": "offset-time", "input": {"at": "2026-11-02T01:00:00+01:00", "parties": P, "row": ROW1}},
    {"name": "lowercase-z", "input": {"at": "2026-11-02T00:00:00z", "parties": P, "row": ROW1}},
    {"name": "leap-second", "input": {"at": "2026-12-31T23:59:60Z", "parties": P, "row": ROW1}},
    {"name": "bad-day", "input": {"at": "2026-02-29T00:00:00Z", "parties": P, "row": ROW1}},
    {"name": "negative-offset", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "named": PAY, "row": dict(ROW2, due=after("buyer_payment", -1))}},
    {"name": "named-bad-latest", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "named": {"buyer_payment": {"time": None, "latest": "soon"}}, "row": ROW2}},
    {"name": "three-parties", "input": {"at": "2026-11-02T00:00:00Z", "parties": ["buyer", "seller", "carol"], "row": ROW1}},
    {"name": "member-case", "input": {"At": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1}},
    {"name": "null-check", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "check": None}},
    {"name": "null-statements", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "statements": None}},
    {"name": "null-row", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": None}},
    {"name": "string-final", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "rulings": [{"final": "yes", "value": "met", "time": "2026-11-01T12:00:00Z"}]}},
    {"name": "fractional-offset", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "named": PAY, "row": dict(ROW2, due={"after": "buyer_payment", "seconds": 1.5})}},
    {"name": "same-parties", "input": {"at": "2026-11-02T00:00:00Z", "parties": ["buyer", "buyer"], "row": ROW1}},
    {"name": "window-before-due", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": dict(ROW1, window_end=at("2026-11-01T00:00:00Z"))}},
    {"name": "date-both-forms", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": dict(ROW1, due={"at": "2026-11-01T23:59:59Z", "after": "buyer_payment", "seconds": 1})}},
    {"name": "unnamed-record", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW2}},
    {"name": "unknown-member", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "status": "agreed"}},
    {"name": "bad-check", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "check": "maybe"}},
    {"name": "ruling-cannot-tell", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "rulings": [{"final": True, "value": "cannot_tell", "time": "2026-11-01T12:00:00Z"}]}},
]
names = [c["name"] for c in cases] + [c["name"] for c in invalid]
assert len(names) == len(set(names))
doc = {"about": "Row status vectors (evidencebook#461, acceptance 6): each case's expect is the status of one term row as of input.at, written by hand from the rule in rowstatus/rowstatus.go's package comment; Go, Rust and Python must give exactly expect for every case and refuse every invalid input.",
       "cases": cases, "invalid": invalid}
json.dump(doc, open(sys.argv[1], "w"), indent=1, ensure_ascii=False)
open(sys.argv[1], "a").write("\n")
print(len(cases), len(invalid))
