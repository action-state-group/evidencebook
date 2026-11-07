# Design note: dropping a held object or email while the deal step stays loadable

Issue: evidencebook-deal#18 (moved from capsule-cli, where the held object is capsule-cli#250).
Status: round 1, for review by Codex, Kimi and Sherlock2 (the drop verb is user-facing).
Checked on main at ed6e003 (capsule-cli rc39, evidencebook v0.13.9, capsule-emit/go
v0.3.1-0.20261027234055-9defe98af14b).

## Problem

A book deal's step is one artifact-store record. Retention acts on a whole record:
`Book.SetRetention(DELETED)` appends a lifecycle record and then calls `Purge` on the record's
originals (evidencebook `retention.go:124`, `purgeLocked`). Two things follow.

1. **No way to drop the bytes alone.** A held object's octets and carrier headers (deal_wrapped.go
   `dealWrapped.Object`, `.Headers`) and a merchant email's `Raw` (deal_merchant_email.go:62)
   live in the step's `deal_local` opening, which is read back on every load
   (`bookStep`, deal_books.go:415, and `loadBook`, deal_books.go:837). Purging the step takes the
   opening, and with it the deal's ability to load. `bookStep` also refuses a step whose retention
   is WITHHELD or DELETED (deal_books.go:411-414: "No deal verb sets a retention").
2. **The size cap.** `artifact.Prepare` rejects a record whose Capsule, envelope and artifact
   contents add up to more than 8 MiB (capsule-emit `artifact/record.go:297`). `deal note --email`
   accepts 10 MB (`maxEmail`, deal_merchant_email.go:57), and the email sits in the step's opening
   as base64 JSON, so a step with a large email cannot be stored. The held object is capped at
   4 MiB for the same reason (`maxWrappedObject`, deal_wrapped.go:63). I did not run an oversized
   email through `deal note --email`; by the code above it fails in `Prepare`.

## What the sealed step already says

The step's record carries digests and flags, not the bytes: `wrapped[{type, digest_alg, digest}]`,
`wrapped_octets`, `wrapped_held`, `carrier_headers_commitment` (`wrappedBody`, deal_wrapped.go:309)
and `merchant_email.message_digest` (`merchantEmailBody`, deal_profile.go:530). `loadBook`
re-derives the record from the local event and requires it to equal the sealed payload byte for
byte (deal_books.go:863). So a step stays loadable after a drop exactly when its local event
still yields the same record without the bytes.

## Options

**A. A held record in a book of its own (recommended if built).** A step sealed after the change
keeps in its local event a reference (the held record's id) and the digests, and not the bytes.
The bytes go into one record of a sibling book `deal/<id>/held`, sealed by the book like any
record. Dropping is `SetRetention(held_record, DELETED)`: the existing record-level purge, applied
to the held record alone. The sibling book follows `deal/<id>/results` (deal_books.go:303-306):
"a book of its own, never the deal's book, so no copy of the deal carries one". Nothing about a
held record or its lifecycle record reaches a copy of the deal or the counterparty.

**B. Per-original purge in the artifact store.** A change to the donated store API in four
languages plus vectors, a public contract. Evidencebook and the boundary docs keep retention per
record (the issue thread, 2026-10-24), so this adds a second granularity nobody asked for. Not
recommended.

**C. Do nothing for retention; fix only the cap.** The sealed step is a digest, so the user's
alternative to a drop is not recording the bytes (`deal note --object-digest` already seals a
digest without possession, `wrappedHeldDigest`). Lower `maxEmail` to what a step can hold and fail
with a clear message.

## Design of A

- **Local event, not the wire.** The step's record bytes do not change, so this is not a
  `dealprofile` (wire) change and needs no evidencebook-side schema edit. What changes is
  `deal_local`: `dealWrapped` gains `Ref` (held record id) and a stored held flag in place of
  `held()` deriving it from `Object != nil`; `merchantEmail` gains `Ref` and `RawDigest` in place of
  `Raw`. A drop leaves the step's `wrapped_held: octets` as sealed: it says what the sealer held
  at seal time, never what remains. `verify-object` reads presence from the held record.
- **Verification after a drop.** `verifyWrapped` (deal_wrapped.go:508) reports
  `object_digest: NOT_PRESENT` and `carrier_headers: NOT_PRESENT` when the held record is
  DELETED, as it does for a digest-only step; a copy given with `--object` still verifies against
  the sealed digest. `verify-email` and the other `Raw` readers (below) report the email as
  dropped and, where a copy is given, check it against `RawDigest`.
- **Size.** The held record holds raw bytes (`Prepare` counts `len(Content)`), so its limit is
  8 MiB less the Capsule and envelope. With the bytes in a held record the email cap can be
  7 MiB; the object cap rises from 4 MiB only if wanted. 10 MB cannot fit in any single record.
- **New steps only.** A step already sealed carries its bytes inside `deal_local`, and the store
  cannot purge one original. It stays droppable only together with the deal, as today.
- **Verb.** `deal drop --deal <id> --step <n>` (Sherlock2 keeps "drop" and avoids "purge" and "delete"
  as the user word), asking the user to confirm first and then running `SetRetention(DELETED, reason)`
  on the held record. The confirmation names what goes and what stays: "This deletes the held
  {object|email} bytes for step <n> of deal <id>. The step, its digests and the sealed facts stay.
  The order id stays in the clear. A copy you kept still verifies against the digest. This cannot
  be undone." `verify-object` prints `NOT_PRESENT (dropped <date>)`, so a reader can tell dropped
  from digest-only.

## What the issue's framing leaves open (found in the code)

1. **Headers and order id sit in `deal_local`, and the record needs their commitments.** The step's
   `Entry.Fields` are only `deal_local`, `amount` and `currency` (evidencebook `dealprofile/step.go:123`);
   `carrier_headers` (deal_profile.go:67) and the email's `order_id` (deal_profile.go:61) are not
   fields but texts the record commits with salted nonces held in the local event
   (`buildDealRecord`'s `commit`, deal_profile.go:615-621), so their text lives only inside the
   `deal_local` opening. A held object's headers can be the personal part (request ids, tokens;
   the share gate treats them as private values, deal_wrapped.go:347). To drop them the re-derivation
   must stop needing the text: the local event keeps the commitment values and a presence flag
   (`Headers != nil` decides whether `carrier_headers_commitment` is emitted, deal_wrapped.go:313)
   and the held record keeps the text and its nonce. That is a change inside this plugin, no
   evidencebook change.
2. **`Raw` has many readers.** `e.Raw`/`m.Raw` is read in deal_merchant_email.go:974-978 and
   :1218, deal_share.go:260,277, deal_share_gate.go:500, deal_deferred_email.go:265,
   deal_verifier_statement.go:111,134,148 and deal_profile.go:536. The held object has readers
   too: both share checks (deal_share.go:218, deal_share_gate.go:520) scan its octets and headers as
   private values, and `verify-object --out` exports them (`writeHeld`, deal_wrapped.go:582).
   Each must resolve the held record (and handle a dropped one) on its own path; a new step
   that skipped the share scan would leak what the scan exists to catch. A dropped email
   reads its digest from `RawDigest` and cannot re-check DKIM, which needs the bytes. The share
   gate loses the email text as a private-value source after a drop, which is correct because the
   bytes are gone, but means a later share cannot be checked against them.
3. **Parsed values stay.** For an email the re-derivation also reads the parsed totals, currency,
   dates and item count (deal_profile.go:552-577), which are in the sealed payload, so a drop
   cannot remove `Parsed` without breaking the byte-equal check (deal_books.go:863). What a drop
   removes is the raw message, the object octets and the carrier headers. The order id text, the
   parsed values and the sealed digests stay: Sherlock2 (Buzz, 2026-11-07, reply to this note) rules
   the headers are the personal part and the rest are deal facts the counterparty holds anyway, so
   a drop that left header tokens in the clear must not ship as a drop.

## Is the build wanted?

My recommendation: split it.

- **Cap fix (S), do now, separate task.** Lower `maxEmail` to about 5 MiB (the email is base64 in the step's
  opening, so 7 MiB is already 9.8 MB against the 8 MiB limit; Codex's arithmetic) and check the
  whole record's size before sealing, with a clear input error that names the limit. That removes the "cannot be stored" case without any retention change.
- **Retention build (L), not yet.** The issue's own wording is "a likely ask" (the held object
  and the user who wants to keep the deal and drop the body); I found no user request. The cost
  is a new sibling book, a changed `deal_local` shape, about a dozen `Raw` call sites, a new verb, and
  moving the header and order id commitments out of the text-derived path (open point 1). Build it when a user asks for
  a drop; the scope and the mechanism are settled above. Until then the note is the design of record.

Sherlock2 also notes that "new steps only" leaves every step sealed before the build undroppable,
and that landing the held-book storage shape early (with any other `deal_local` shape change)
makes the later verb retroactive from that point. That is a schedule trade-off for the build, not
a reason to build now.

## Doors and routing

- Option A: no store API, no wire change, no release. The `deal_local` shape is a local, versionless
  detail of this plugin (new steps only; old steps read as today).
- Needs routing if built: the final confirmation wording (Sherlock2 has given the text above) and the irreversible
  deletion of a user's evidence (Steven, as a user-visible disclosure and deletion rule), which
  Sherlock2 places at the point a drop ships, not before.
- `deal verify-object` keeps `object_digest: NOT_PRESENT` after a drop, per the issue.

## Tests the build would need

A held object and an email sealed on a book deal; drop; the deal still loads and `deal report`
reproduces; `verify-object` reports NOT_PRESENT for object and headers and still PASS for a copy;
`verify-email` reports dropped; a second drop is idempotent (`SetRetention` retries a failed
purge); a drop on an old step refuses with a clear message; a large email over the cap is refused
with the limit named; the held book is absent from a copy of the deal.
