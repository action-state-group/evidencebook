# evidencebook#232: a reproduced scratch book takes a caller-given time

Status: ruled (Steven, 2026-10-31, option a).

## Problem
capsulectl stamps the wall clock in three places: each record's
`evidence_record_header.committed_at` (publish), each Result's `timestamp`
(and so `agent_input_digest`) in `book result build`, and the checkpoint
`timestamp`. Two runs over the same inputs therefore never give the same
digests, so neither we nor a third party can re-run a judge and compare by
digest (evidence-network/loop#177).

## Ruling
A scratch book built to reproduce results may take one caller-given time
(`--at <RFC 3339>`, or `SOURCE_DATE_EPOCH`) on all three stamps. Everything
else keeps the wall clock.

## What `committed_at` means
In a normal book, `committed_at` is when the record was committed. In a
reproduction book it is the time the caller asserts the original run used.
The marker below is what tells a reader which meaning applies.

## Guards
1. Marker: a reproduction book carries the header member
   `reproduction: {"clock": "caller"}` in the digested header of every record,
   so it cannot be stripped without changing the record's identity. A reader
   that sees it must not read any stamp as a real commit time. The checkpoint
   is a signed statement of the log's own, which has no header; it covers
   only marked records, so the marker travels with the book, not the
   statement (the checkpoint time is the same caller time).
2. Dead end: a book is reproduction or live for its whole history. The root
   `Book` refuses to write a record of the other kind into it (`ErrInvalid`),
   and capsulectl refuses to countersign a marked book. A reproduction can be
   compared, never extended as a live book or vouched.
3. Monotonic: caller times are non-decreasing with `seq`; capsulectl rejects
   a time earlier than the previous record's.
4. Default: without `--at` (or `SOURCE_DATE_EPOCH`) there is no marker and the
   wall clock is used.

## Where each part lives
- `evidencebook` (this module, Rust and Python mirrors): the `reproduction`
  header member, `Config.Reproduction` (needs `Config.Now`, the caller's time),
  the live/reproduction mix refusal.
- `capsulectl-book`: the input (`--at` on its own verbs, `SOURCE_DATE_EPOCH`
  for the core's `publish` and checkpoint paths, which are capsule-cli's
  commands), guards 2 and 3.

## Out of scope
Caller-given times on a live book. Backdating a live record stays impossible.

## Test
Two runs of loop step 10 over one step-08 manifest with `--at <window end>`
give byte-identical bundles; countersign and append on the result fail.
`TestReproductionBooksAreByteIdentical` is the root half.
