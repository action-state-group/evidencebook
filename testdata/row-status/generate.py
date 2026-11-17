# SPDX-License-Identifier: Apache-2.0
# Usage: python3 testdata/row-status/generate.py testdata/row-status/vectors.json
# Writes testdata/row-status/vectors.json and, beside it, late.json. Every "expect" below is written by hand from the rule
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

# Boundaries: each rule at the due date and at the end of the contest window (acceptance 6).
R1END, R1DUE = "2026-11-04T23:59:59Z", "2026-11-01T23:59:59Z"
DIS0 = [st("buyer", "met", "2026-11-01T12:00:00Z"), st("seller", "not_met", "2026-11-01T13:00:00Z")]
case("b-r3-at-due", "in disagreement at T = the due date", R1DUE, ROW1, res("in_disagreement", R1D), statements=DIS0)
case("b-r3-at-window-end", "in disagreement at T = the window end", R1END, ROW1, res("in_disagreement", R1D), statements=DIS0)
case("b-r4-at-due", "agreed so far at T = the due date", R1DUE, ROW1, res("agreed_so_far", R1D, "met"), statements=AGR1)
case("b-r4-at-window-end", "still agreed so far at T = the window end (inside)", R1END, ROW1, res("agreed_so_far", R1D, "met"), statements=AGR1)
case("b-r5-at-due", "waiting at T = the due date", R1DUE, ROW1, res("waiting", R1D, "met"), statements=[st("buyer", "met", "2026-11-01T12:00:00Z")])
RUL1 = [{"final": True, "value": "not_met", "time": "2026-11-01T20:00:00Z"}]
case("b-r1-at-due", "ruled at T = the due date", R1DUE, ROW1, res("ruled", R1D, "not_met", "none"), statements=AGR1, rulings=RUL1)
case("b-r1-at-window-end", "ruled at T = the window end", R1END, ROW1, res("ruled", R1D, "not_met", "none"), statements=AGR1, rulings=RUL1)
case("b-r2-at-due", "in dispute at T = the due date", R1DUE, ROW1, res("in_dispute", R1D), statements=AGR1,
     disputes=[{"side": "seller", "time": "2026-11-01T20:00:00Z", "seen_in_time": True}])
case("b-r2-at-window-end", "in dispute at T = the window end", R1END, ROW1, res("in_dispute", R1D), statements=AGR1,
     disputes=[{"side": "seller", "time": "2026-11-01T20:00:00Z", "seen_in_time": True}])
case("b-dispute-at-window-end-not-late", "a dispute dated exactly at the window end is not after it: not late", "2026-11-06T00:00:00Z", ROW1, res("in_dispute", R1D),
     statements=AGR1, disputes=[{"side": "seller", "time": R1END, "seen_in_time": True}])
case("b-dispute-one-second-after-late", "one second after the window end: late", "2026-11-06T00:00:00Z", ROW1, res("agreed", R1D, "met"),
     statements=AGR1, disputes=[{"side": "seller", "time": "2026-11-05T00:00:00Z", "seen_in_time": True}])
case("b-statement-at-window-end-in-time", "a statement dated exactly at the window end is in time, not a concession (shown with concessions off)", "2026-11-06T00:00:00Z", ROW1,
     res("agreed", R1D, "met"), no_concession=True, statements=DIS1 + [st("seller", "met", R1END)])
case("b-statement-one-second-after-late", "one second after the window end the same statement is late, and with concessions off it does not count", "2026-11-06T00:00:00Z", ROW1,
     res("in_disagreement", R1D), no_concession=True, statements=DIS1 + [st("seller", "met", "2026-11-05T00:00:00Z")])
RD = dict(FR, remedy_due="2026-11-27T23:59:59Z")
case("b-remedy-at-its-date", "a remedy dated exactly at the remedy's date is made", "2026-11-28T00:00:00Z", ROW3, res("ruled", R3D, "not_met", "made"),
     named=RCPT, statements=R3ST, rulings=[RD], remedies=[{"time": "2026-11-27T23:59:59Z"}])
case("b-remedy-due-at-its-date", "T = the remedy's date with no remedy: still due", "2026-11-27T23:59:59Z", ROW3, res("ruled", R3D, "not_met", "due"),
     named=RCPT, statements=R3ST, rulings=[RD])
# Records with no time that a result rests on.
case("l-untimed-cannot-tell", "an untimed cannot_tell that clears the side's earlier value makes the result local", "2026-11-02T00:00:00Z", ROW1, res("waiting", R1D, "met", local=True),
     statements=[st("buyer", "met", "2026-11-01T12:00:00Z"), st("buyer", "cannot_tell", None), st("seller", "met", "2026-11-01T12:30:00Z")])
case("l-untimed-withdrawal", "an untimed withdrawal that removes a dispute makes the result local", "2026-11-04T00:00:00Z", ROW1, res("agreed_so_far", R1D, "met", local=True),
     statements=AGR1, disputes=[{"side": "seller", "time": "2026-11-03T10:00:00Z", "seen_in_time": True}], withdrawals=[{"side": "seller", "time": None}])
WD_D = [{"side": "seller", "time": "2026-11-03T10:00:00Z", "seen_in_time": True}, {"side": "buyer", "time": "2026-11-03T11:00:00Z", "seen_in_time": True}]
case("l-untimed-withdrawal-not-relied-on", "another dispute counts on its own, so the untimed withdrawal of the first is not relied on", "2026-11-04T00:00:00Z", ROW1, res("in_dispute", R1D),
     statements=AGR1, disputes=WD_D, withdrawals=[{"side": "seller", "time": None}])
case("l-untimed-withdrawal-not-relied-on-reordered", "the same with the disputes in the other order", "2026-11-04T00:00:00Z", ROW1, res("in_dispute", R1D),
     statements=AGR1, disputes=list(reversed(WD_D)), withdrawals=[{"side": "seller", "time": None}])
case("l-untimed-is-never-a-concession", "an untimed statement after the window cannot be shown late, so it is the side's latest in-time statement (local), never a concession", "2026-11-07T00:00:00Z", ROW1,
     res("agreed", R1D, "met", local=True), statements=DIS1 + [st("seller", "met", None)])
case("l-concession-to-untimed-value", "a timed concession to the other side's untimed in-time value counts, and the result rests on that untimed statement: local", "2026-11-07T00:00:00Z", ROW1,
     res("agreed", R1D, "met", local=True), statements=[st("buyer", "met", None), st("seller", "not_met", "2026-11-02T10:00:00Z"), st("seller", "met", "2026-11-06T10:00:00Z")])
case("l-untimed-after-future", "an untimed statement after one of the same side dated after T is not held as of T", "2026-11-05T05:00:00Z", ROW1, res("uncontested", R1D, "met"),
     statements=[st("seller", "met", "2026-11-01T12:30:00Z"), st("buyer", "not_met", "2026-11-05T10:00:00Z"), st("buyer", "met", None)])
# A final ruling shows while a date it does not need is unset (Sherlock2's recommendation; a product call recorded on #466).
case("d-ruling-over-unset-date", "a final ruling decides the row even before the receipt record has a time", "2026-11-20T00:00:00Z", ROW3, res("ruled", {}, "not_met", "none"),
     named=NORCPT, rulings=[{"final": True, "value": "not_met", "time": "2026-11-19T10:00:00Z"}])
case("d-not-reached-over-ruling", "not reached is checked first, before any ruling", "2026-12-02T00:00:00Z", ROW3, res("not_reached", {}),
     named=NORCPT, rulings=[{"final": True, "value": "not_met", "time": "2026-11-19T10:00:00Z"}])

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
    {"name": "check-not-recomputed", "input": {"at": "2026-11-02T00:00:00Z", "parties": P, "row": ROW1, "check": "fail"}},
    {"name": "year-zero", "input": {"at": "0000-01-01T00:00:00Z", "parties": P, "row": ROW1}},
    {"name": "date-past-9999", "input": {"at": "9999-12-31T23:59:59Z", "parties": P, "named": {"buyer_payment": {"time": "9999-12-31T23:59:59Z", "latest": "9999-12-31T23:59:59Z"}}, "row": ROW2}},
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

# Late statements and late disputes (evidencebook#461, Q9; docs/design/461-deal-profile.md section 9):
# an output beside the status, written to late.json next to vectors.json. Each expect lists indices
# into the input's statements and disputes, in input order, by hand from the rule: a statement held
# as of T is late when its time is after the window end, or it has none and an earlier timed
# statement of its side is (a concession is late too); a dispute held as of T is late as rule 2
# says. Both are empty while the row is not reached or a date is unset.
byname = {c["name"]: c["input"] for c in cases}
late = []
def late_case(name, why, expect_statements, expect_disputes, inp=None):
    late.append({"name": name, "why": why, "input": copy.deepcopy(inp if inp is not None else byname[name]),
                 "expect": {"late_statements": expect_statements, "late_disputes": expect_disputes}})
late_case("r5-late-statement-ignored", "the buyer's statement after the window end is late", [1], [])
late_case("cn-concession-counts", "a concession is late, though it counts", [2], [])
late_case("cn-only-first-counts", "both concessions are late, the counted one and the other", [2, 3], [])
late_case("l-untimed-after-late", "an untimed statement after a late one of its side is late too", [1, 2], [])
late_case("l-untimed-is-never-a-concession", "an untimed statement whose side has no late timed one is not late", [], [])
late_case("l-untimed-after-future", "statements not held as of T are never listed", [], [])
late_case("b-statement-at-window-end-in-time", "a statement at the window end itself is in time", [], [])
late_case("b-statement-one-second-after-late", "one second after the window end: late", [2], [])
late_case("r2-late-dispute", "a late dispute on an agreed row", [], [0])
late_case("r2-late-dispute-unseen", "a dispute whose claimant had not seen the statement in time is not late", [], [])
late_case("r2-late-dispute-failed-check", "a dispute on a row whose check failed is not late", [], [])
late_case("r2-late-dispute-on-disagreement", "a dispute on a row whose in-time statements differ is not late", [], [])
late_case("r2-late-dispute-uncontested", "a late dispute on an uncontested row", [], [0])
late_case("r2-late-dispute-ignores-concession", "the concession is late; the dispute, read against the in-time statements, is not", [2], [])
late_case("b-dispute-at-window-end-not-late", "a dispute at the window end itself is not late", [], [])
late_case("b-dispute-one-second-after-late", "one second after the window end: late", [], [0])
late_case("l-untimed-dispute", "a dispute with no time is never late", [], [])
late_case("d-not-reached", "a row not reached lists nothing", [], [])
late_case("d-unset-up-to-latest", "a row whose dates are unset lists nothing", [], [])
LATE_D = {"side": "seller", "time": "2026-11-06T10:00:00Z", "seen_in_time": True}
late_case("late-dispute-ruled", "a late dispute is listed on a ruled row too", [], [0],
          {"at": "2026-11-07T00:00:00Z", "parties": P, "row": ROW1, "statements": AGR1, "disputes": [LATE_D],
           "rulings": [{"final": True, "value": "met", "time": "2026-11-06T12:00:00Z"}]})
late_case("late-dispute-withdrawn", "a late dispute is listed though withdrawn", [], [0],
          {"at": "2026-11-07T00:00:00Z", "parties": P, "row": ROW1, "statements": AGR1, "disputes": [LATE_D],
           "withdrawals": [{"side": "seller", "time": "2026-11-06T11:00:00Z"}]})
late_case("late-index-order", "indices are into the input: the second dispute is the late one", [], [1],
          {"at": "2026-11-07T00:00:00Z", "parties": P, "row": ROW1, "statements": AGR1,
           "disputes": [{"side": "buyer", "time": "2026-11-06T09:00:00Z", "seen_in_time": False}, LATE_D]})
late_case("late-after-T-not-held", "a statement and a dispute dated after T are not held, so not listed", [], [],
          {"at": "2026-11-05T12:00:00Z", "parties": P, "row": ROW1,
           "statements": AGR1 + [st("buyer", "not_met", "2026-11-05T13:00:00Z")],
           "disputes": [dict(LATE_D, time="2026-11-05T14:00:00Z")]})
assert len({c["name"] for c in late}) == len(late)
late_path = sys.argv[1].rsplit("/", 1)[0] + "/late.json" if "/" in sys.argv[1] else "late.json"
json.dump({"about": "Late statement and late dispute vectors (evidencebook#461, Q9): each expect lists, by index into the input, the statements and disputes the rule sets aside as late as of input.at, written by hand from the rule in testdata/row-status/generate.py; each implementation's late output must give exactly expect.",
           "cases": late}, open(late_path, "w"), indent=1, ensure_ascii=False)
open(late_path, "a").write("\n")
print(len(late), "late")
