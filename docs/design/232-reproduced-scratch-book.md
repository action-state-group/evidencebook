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
(`SOURCE_DATE_EPOCH`, the reproducible-builds convention; an `--at` flag was dropped because `publish` and the checkpoint cut are capsule-cli commands that a plugin flag cannot reach) on all three stamps. Everything
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
   a time earlier than the previous record's (the same time is allowed, since
   one run stamps one time).
4. Default: without `SOURCE_DATE_EPOCH` there is no marker and the
   wall clock is used.

## Where each part lives
- `evidencebook` (this module, Rust and Python mirrors): the `reproduction`
  header member, `Config.Reproduction` (needs `Config.Now`, the caller's time),
  the live/reproduction mix refusal.
- `capsulectl-book`: the input (`SOURCE_DATE_EPOCH`, read when a book opens, so
  it reaches `publish` and the checkpoint cut) and guard 3. The dead end is the
  root book's (a write of the other clock kind is refused), reached through
  every capsulectl verb; reads of a marked book work without the variable.
  `backfill run` and `book migrate` stamp the wall clock and refuse to run with
  the variable set. `SOURCE_DATE_EPOCH` is also set globally by some build
  environments; a book written there is a reproduction.
- capsule-cli: refusing to countersign a bundle whose records carry the marker
  (the `countersign` command works on a bundle, outside any book).

## Out of scope
Caller-given times on a live book. Backdating a live record stays impossible.

## Test
Two runs of loop step 10 over one step-08 manifest with `SOURCE_DATE_EPOCH=<window end>`
give byte-identical bundles; countersign and append on the result fail.
`TestReproductionBooksAreByteIdentical` is the root half.
