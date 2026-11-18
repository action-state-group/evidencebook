# x-deal-v1 fixtures

The deal of the evidence layer's integrated design, Appendix A: a 60 dollar lamp with three rows
(paid as agreed, delivered, as described), where the lamp arrives cracked, the seller does not
give way and the buyer goes to the adjudicator. Each positive fixture is one record of that deal;
`appendix_a_line` is the line of Appendix A's step-by-step table it is written at.

| Directory | Records |
|---|---|
| `appendix-a/buyer-journal/` | the owner's intent, the two admission decisions, the evidence request, the offer, and the check, verdict and approval the payment rests on |
| `appendix-a/buyer-book/` | the buyer's deal book, eight records: its agreement, the payment, the receipt, the not-as-described statement, the dispute, the two ruling received records and the refund received record |
| `appendix-a/seller-journal/` | the seller's answer to the buyer's evidence request, its admission decision, and the check, verdict and approval its refund rests on |
| `appendix-a/seller-book/` | the seller's deal book, seven records: its agreement, the payment received record, the delivery, the reply, the two ruling received records and the refund |
| `appendix-a/case-book/` | the adjudicator's case book: the ruling, then the same ruling sealed again as final |
| `settled/` | the same deal settled without a ruling (Appendix A's last table, lines s1 to s12): it leaves the main path after the buyer's dispute and the seller's reply, so its books continue from those records at `seq` 5 and 6 |
| `not-started/` | an offer declined by the seller, and an offer cancelled by the buyer (Appendix A's last paragraph) |
| `negative/` | records that MUST be refused, each one positive record (`base`) with one change |

Each positive file holds the record, its `placement` (`journal`, `deal_book` or `case_book`), its
`expected_digest`, the `seal_context` a journal record is checked with when its `deal_id` depends
on it (section 3 of PROFILE.md), and, for a book record, a `record_id`. `expected-digests.txt`
lists every positive digest.

Each negative file names the `check` that refuses it, the `expected_stage` and, where the stage is
the record checks or later, text the refusal contains:

- `record`: the record alone (`xdealv1.Check`, or `CheckSealed` with `seal_context`);
- `agreement`: the agreement read against the offer named in `with`, and, for an offerer's
  agreement, the acceptor's agreement record id in `held_acceptor_agreement`
  (`xdealv1.CheckAgreement`);
- `dispute`, `ruling`: the record read against the agreement named in `with`
  (`xdealv1.CheckDispute`, `CheckRuling`).

A negative whose change no JSON value can show (a member given twice) carries `record_text`
instead of `record`.

**Stand-ins.** The fixtures seal no Capsules, so a book record has no real record id. A record id
here (`record_id`, `seen`, a ruling's `read`) is `SHA-256("x-deal-v1 fixtures\0record id of " +
record digest)`, in lowercase hex, and every key, pack, card and commitment is
`SHA-256("x-deal-v1 fixtures\0" + name)`. These are fixed values to check against, not a rule of
the profile.

**How they are built.** `dealprofile/xdealv1`'s `TestFixtures` builds every file here, runs each
positive through every check it must pass (the record checks, the agreement against the offer, the
dispute and the rulings against the agreement, each book's `seq`, `prev` and `agreement_ref`, and
each side's acts against its journal), runs each negative through its check, and compares the
files byte for byte. To write them after an intended change:

```bash
EB_WRITE_DEAL_V1_FIXTURES=1 go test ./dealprofile/xdealv1 -run TestFixtures
```
