# Changelog

## Unreleased

## v0.1.0-rc38

This release pairs with capsulectl `v0.1.0-rc38` (go.mod requires it).

This release carries what merged since `v0.1.0-rc37`: `deal disclosures` (#161), the owner's copy opening the counterparty and the buyer's words (#160, #165), `deal check --received` (#167) and the sealed dispute, ruling and side in `deal report` (#169).

- `deal report` (`--read-only` included) carries the holder's `side` (`bought` or `provided`) and, when the store holds them, a structured `dispute` (`row`, and `raised`: true only for the buyer who disputed) and `ruling` (`row`, `finding` `for_buyer` or `for_seller`, `digest`), read from the sealed evidence steps rather than the trail text a claim can imitate. The ruling stands without the dispute (a seller's store holds none); the buyer's words are not in either. `--read-only` keeps its guarantees: nothing is sealed, cut or migrated, and it takes the same shelf lock and checkpoint key as before. No record or schema changes (evidencebook-deal#168).
- `deal check --received FILE` lets a host hand the profile's rules checker the cards it received about the deal's counterparty (a JSON array, at most 64 cards and 1 MiB; the profile must pin a rules checker). The sealed rules block still comes from a run given no cards, so nothing the cards could change reaches it, whatever the checker does with them; when that run was evaluated, a second run given the cards as the optional `received_cards` member of `external-check-input/v0` is read for the checker's optional one-line `notice` alone, printed as `rules_notice` (or `rules_notice_unavailable`, with the reason). Cards and notice are not stored, sealed or shared, and a sale's post check refuses the flag because it runs no checker. A checker must accept `received_cards` before a host sends cards; needs evidencebook with the `received_cards` and `notice` schema members (evidencebook v0.13.9, #427) (evidencebook-deal#166).
- `deal disclosures --deal ID [--record DIGEST]`: a read-only verb that returns the sealed disclosure record of each share of a deal, with its digest and its sequence on the disclosure log; each stored record's JCS digest is recomputed and must equal the digest stored with it (the one its share sealed); the log itself is not re-read. It reads the holder's own store and feeds no share, page or bundle. Lets a script drop its sqlite read of `deal_disclosures` (evidencebook-deal#27).
- The user's own deal report opens who the deal was with and what the buyer wrote. `counterparty_openings` holds the
  deal's key and the payee, name and domain of the counterparty the deal ends with (the one the OK card names), each at
  a step whose sealed record carries the keyed fingerprint it recomputes to; `acceptance_openings` holds each
  acceptance's step, nonce and words. The page recomputes each against that step's sealed `counterparty.ids` or
  `content_commitment` when it is written: the receipt names a checked counterparty as the OK card does (the payee, else
  the name, with the site) and shows a buyer's acceptance words as checked, and a value that does not recompute is
  reported as not verified and not shown. Shared copies carry none of it, nor the deal key, and a shared copy that did
  refuses the page. A deal with nothing to open reports as before. No sealed record or schema changes
  (evidencebook-deal#56).
- A counterparty known only by phone or email is opened the same way in the user's own report, as the OK card names
  them: the phone, else the email, and only when the deal has no payee, name or site to name them by. The receipt shows
  it as "With <phone or email>" once it recomputes against the sealed fingerprint; shared copies are unchanged
  (evidencebook-deal#162).

## v0.1.0-rc37

This release pairs with capsulectl `v0.1.0-rc37` (go.mod requires it).

This release carries what merged since `v0.1.0-rc36`: the card names a terms pack's row in the pack's words (#158).

- For a check whose pinned pack is of kind `deal` (the user's own terms), the seller's card now names the checker's reasons instead of saying "your rules don't allow this" or "I couldn't check this against your rules". Other rulesets, and findings with no reason, keep the card's own wording. No schema change (`reason` is an existing member) (evidencebook-deal#157, #158).

## v0.1.0-rc36

This release pairs with capsulectl `v0.1.0-rc36` (go.mod requires it).

This release carries what merged since `v0.1.0-rc35`: the sealed check's pack kind (#149), dispute and ruling notes with the card's disputes tally (#151, #154) and the History card right after declare and tick (#155).

- `deal note --kind dispute` and `--kind ruling` (evidencebook-deal#151, Terms Pack Studio step 6). They stand in for
  the dispute and ruling record kinds until those land and add no record family and no schema: each seals an evidence
  step whose `source` is `dispute` (the buyer's, once per deal), `ruling_for_buyer` or `ruling_for_seller` (the
  referee's one ruling, final once sealed). The ruling digest is the SHA-256 of the ruling body, which names both
  parties' copies, so the same body written to both stores gives the same digest; `--ruling-digest` makes the second
  store refuse a ruling that differs from the first. A buyer's store checks the ruling against its dispute. The receipt reads
  `Ruling <12 hex> on "price as offered": found for the buyer` on both parties' own copies and on the counterparty's
  shared copy. The standard history card's `disputes` tally is filled from it: raised for a buyer, received for a
  seller, `for` or `against` the holder, forum `external`; a dispute with no ruling yet counts as open.
- `deal standard-history card` works right after `declare` and `deal tick` on a new profile (evidencebook-deal#153).
  A tick's stored time is rounded down to the minute, so a declaration made in the same minute read as newer than
  the tick and the card was refused. `deal tick` now keeps the tick's real cut time, and the card is still refused
  for a declaration or policy change made after the tick. That refusal now says when the next tick is due, is
  reported as waiting rather than as a device fault, and exits 4 instead of 1.
- The sealed check now records the terms pack's `kind` (evidencebook-deal#146). A counterparty's or adjudicator's
  copy names the pack only when the kind sealed with the check is exactly `deal` and the profile still pins that
  pack for the party's role. Sharing follows the sealed kind, never the profile's current pin. Checks sealed
  before this release carry no `kind` and never name their pack on those copies. The user's own copy is unchanged.
  Requires evidencebook `v0.13.6`.
## v0.1.0-rc35

This release pairs with capsulectl `v0.1.0-rc35` (go.mod requires it).

This release carries what merged since `v0.1.0-rc34`: the pack digest on the receipt (#138, #145) and the Terms Pack Studio replay fixture (#136).

- The receipt names the pack that judged a check (evidencebook-deal#138): the user's own copy reads
  "Checked before paying: no differences, under <ruleset id>, pack <12 hex>". A counterparty's copy reads
  "..., under the seller's terms, pack <12 hex>" (the buyer's terms on a buyer's deal) only when the profile's
  rules-checker pin states `"kind": "deal"` (capsule-cli v0.1.0-rc35) and the check ran the pinned digest. An
  adjudicator's copy names a deal-kind pack the same way (evidencebook-deal#145), since the referee rules by the deal's
  terms. A conduct pack, or a pin that states no kind, is never named to a counterparty or an adjudicator, and the
  ruleset id never is. Whether a copy names the pack is decided from the profile's current pin, not the pin the check
  ran under (evidencebook-deal#146).

- A replay fixture for the Terms Pack Studio first demo (evidencebook-deal#136): ten sticker trades,
  nine clean and one where the seller raised the price after the buyer accepted, and
  `scripts/studio-replay.sh` to replay them into a seller's and a buyer's deal store;
  `testdata/studio/README.md` lists the records a store then holds.

## v0.1.0-rc34

This release pairs with capsulectl `v0.1.0-rc34` (go.mod requires it).

This release carries what merged since `v0.1.0-rc33`: the History card errors that say why (#140), and the evidencebook `v0.13.5` pin, whose standard-history entry prices a Lightning (BTC) deal in msat at `assetScale` 11 instead of refusing the card as unpriced (evidencebook#388). A Lightning deal can now make a `deal standard-history card`.

- `deal standard-history card` and `staple` say why a deal cannot be on the History card (a deal with no recorded amount or a currency the card can't count, no finished checkpoint, a declaration or policy changed after the last checkpoint, a deal recorded in an older format) instead of the generic masked "operation failed". The line reads "the History card was not built: <reason>" and names the deal id and nothing else from the records; every other failure stays masked. Exit codes are unchanged (#139).

## v0.1.0-rc33

This release pairs with capsulectl `v0.1.0-rc33` (go.mod requires it).

This release carries what merged since `v0.1.0-rc32`: a Lightning deal priced and reported in msat (#134), a runtime FAIL of the settlement or witness check reading failed (#132), and the refreshed monthly fixtures (#126).

- Builds against capsule-viewer `v0.18.1` (rc32 shipped `v0.10.0`), which reads a BTC amount as "N msat". The presentation goldens are rebuilt on it; the two monthly pages are written by capsulectl-book built from evidencebook#387 (the source of capsulectl-book `v0.1.0-rc33`), so they match what that release writes.

- A Lightning deal is priced and reported in msat (evidencebook-deal#133): terms currency `BTC`
  with `amount_minor` counting millisatoshi (10^-11 BTC, the scale of the settlement legs' asset).
  The receipt's money line reads "Paid 500 msat, returned 0 msat: net 500 msat." and a payee's
  report states its sealed settlement leg ("Received 400 msat, receive fee 100 msat."); the
  payment-received line reads in msat. Fiat wording is unchanged.

- The `4-monthly-outcome` and `5-monthly-compliance` presentation fixtures are refreshed from
  evidencebook (#375): each monthly Result now cites its weeks' calibration summaries and human
  ratings. capsule-viewer/go moves to v0.12.0, the version those pages embed
  (evidencebook-deal#124).

- A runtime answer of `FAIL` from the settlement or witness check now reads **failed**, not
  "can't check" (evidencebook-deal#130): the settlement line says "its proof did not verify" and
  the witness line "the witness's signature did not verify" (on the receipt with the other
  party's half, "the other side's witness signature did not verify" when only theirs fails).
  `PASS`, no receipt, not checked and a runtime without the checks read as before.

## v0.1.0-rc32

Pairs with capsulectl `v0.1.0-rc32`. This section also lists what `v0.1.0-rc31` carried: that tag
was cut before this CHANGELOG had a section for it. New since rc31: evidencebook-deal#121 (r21's
channel), #119, #125 and #123 (witness `unverified`), #105 (the receipt's settlement and witness lines)
and #128 (the sale close, fixes #127).

- A buyer thread closed `received` (outcome completed or mismatch) now closes its sale too, so a completed
  sale leaves no open `sale-…` deal in the seller's store (evidencebook-deal#127); `deal close`
  prints `sale_closed` when it closed the sale, and a retried close finishes a sale a crash left open.
  A thread that did not end with the item handed over leaves the sale open for the next buyer.

- The rules checker's `channel` input comes from the latest counterparty message only
  (evidencebook-deal#24; needs an evidencebook that accepts `rule_inputs_version` 4,
  evidencebook#363): the user's and the agent's messages no longer move it, so r21 no longer
  fires when the user notes their own route (for example "I'll sign in myself"). Rule inputs
  version is now "4"; a check sealed under "3" or before re-derives as before.

- `deal standard-history staple --derivation history_card/1|standard_history/1 [--deal ID]`
  (evidencebook-deal#115; needs evidencebook v0.13.2): prints the holder's request and its
  signed reply as one JSON object, for the host to attach to its message; evidencebook's
  `CheckStaple` accepts both unchanged. `history_card/1` is the cadence's history card and
  seals nothing; `standard_history/1` needs a declared holder that answers the card and seals
  one `evidence_request_answered` record naming `--deal`. No default: the caller decides when
  to staple. Exit 2 for refused input, 1 for any other failure (for example no tick yet).

- The rules checker is sent the acting role's pin (evidencebook-deal#66 part 2; needs
  evidencebook v0.13.1): on a profile whose `rules_checker.roles` is set, the
  `external-check-input/v0` gains `rules_pin` ({`pack`, `definition_digests`}), the acting
  role's pin alone, never the other role's. A role entry that names a pack alone keeps
  the flat digests; a role with no entry sends the flat pin with no pack; a pin that names
  nothing sends no `rules_pin`. A profile without roles sends what it sent before.

- A sealed terms Result whose witness receipt is held but did not verify reports the
  state `unverified` from `deal terms-result`, not `pending_witness`
  (evidencebook-deal#122): a failed check is not a witness still to come.

- A witness check that failed reads as such (evidencebook-deal#113,
  actionstate-muse#311 §3.3): a held witness receipt that does not verify under the
  profile's witness (a tampered receipt, one signed by another key, a rotated witness key or a changed
  endpoint) gives the witness state `unverified` in `assurance.witness_state`,
  `grade.witness.state` and `deal checkpoint status`, with the assurance line saying
  the receipt did not verify, instead of `scheduled` or `pending`. A deal
  never sent to the witness still reads
  scheduled/pending; the rung stays
  `self_attested`.

- An adjudicator's copy seals its referee (F8.3, evidencebook-deal#107; needs
  evidencebook v0.13.1): `disclose --deal ID --share adjudicator --to WHO` and
  `deal report --share adjudicator --to WHO` seal `WHO` as the report's `referee`
  (NFC, trimmed), which evidencebook's `dealprofile.ReportPayload` now requires.
  `deal terms-result --check` with two `--copy` files reads
  `dealprofile.SharedReferee` and adds `referee` to its output: "both sides named the
  same referee: X (the same name as written, not a verified person)", "the sides named
  different referees: A named X, B named Y" (A and B are the `--copy` files), or "A names
  no referee". The check itself still runs. One adjudicator copy's deal view shows "The
  referee this side named: X", and its "What it doesn't" box says only a check of both
  copies compares names and a match is not any particular person.

- The receipt checks that their half points at yours (F4.15b, capsule-viewer#222): on a
  receipt that holds the other side's verified deal half, a new stamp line "Their record
  points at yours" passes when a record of their half, in their book, links to one of your
  records, and names the link: "acknowledges", "disputes" (a `rebuts` link; the grade says
  the deal took place, not that they agree) or any other. No such link reads "missing", never
  failed; an exchange half reads "not checked". The same-deal lines no longer say "neither
  points at the other". Sherlock2's ruling: Buzz 3224c6af.

- The deal view's words are a wording pack (F8.10, evidencebook-deal#97): the English
  `deal-view.js` hardcoded is now the `page.*` entries of
  `assets/wording/capsulectl-deal-en.json`. The module holds them built in (a table the
  pack generates and a test holds to it), so a viewer that opens a bundle shows them; a
  page capsulectl writes carries only the `page.*` words a pinned pack changes. The
  page's text is unchanged (the presentation snapshots are identical); a pack pinned on
  a deal profile must now hold the `page.*` keys too.

- The History card bundle carries its anchor (F4.7, evidencebook-deal#100):
  `deal standard-history card` writes `as_of.checkpoint` in both copies (the
  anchor's `cll-checkpoint` octets as hex; their SHA-256 is `as_of.anchor`), and
  `as_of.receipts` when the cadence tick the card is as of holds a witness
  Receipt for that anchor (interop profile v1 §6.2.2), so capsule-viewer's History
  card can say who recorded the snapshot (capsule-viewer#183). Only a
  `scrapi+https://` witness's Receipt covers the anchor; a `cll` witness's receipt
  covers the checkpoint payload's digest and is never written. A deal profile
  whose `checkpoint.endpoint` is a `scrapi+https://` Transparency Service now
  gets its cadence ticks witnessed: the cadence checks the Receipt under that
  binding, not always under `cll`. The default witness is unchanged.

- The History card's `happened` reads `stamped_by_both` (evidencebook-deal#85): a deal
  whose sealed `deal terms-result` Result, under the card's anchor, names the
  counterparty's admitted half beside your own in the Result's period (the half `terms-result` checked: signed
  under the key the response pinned, a step of this deal, under the copy's verified
  checkpoint, its final close). A deal without one stays `receipt_only` or `no_record`.
  Run `deal terms-result` and then `deal tick` before the card.

- Builds against capsulectl `v0.1.0-rc32` (its profile names the countersigner directory,
  capsule-cli#522; rc32 changes nothing in this plugin's go.mod beyond the require), which pins capsule-viewer `v0.10.0`.

- The owner's report states its grade, keeps countersignatures, and has a read that
  writes nothing (evidencebook-deal#64, actionstate-muse#311 §3.1, §3.3, §4):
  - `deal report` (the user's own copy) carries `grade`: the rung reached
    (`self_attested`, `witnessed`, `witnessed_in_part`, `countersigned` or
    `countersigned_in_part`), the
    witness's state with the time of the checkpoint it signed (`witness.at`) and, in
    part, the steps it covers, and the stored countersignatures. It is what capsulectl
    checked, beside the bundle; nothing sealed changes. A shared copy carries none.
  - `deal countersign` keeps every countersignature it verifies in the deal store
    (`deal_countersignatures`) with the exact bundle it covers. A later report states
    each against that stored bundle: through step k of n, the signer, its
    `recomputed_at`; it never attaches one to its own bundle. Signers resolve against
    the profile's `countersign.directory` only (capsule-cli#522), which both commands'
    `--directory` now defaults to; one not checked says why (`unchecked_reason`).
  - `deal report --read-only` reads a deal and writes nothing: no checkpoint is cut,
    nothing sealed, no file written, the store opened as `deal checkpoint status`
    opens it. A deal with no checkpoint over its last step states its witness
    `unchecked`.

- `counterparty_class` on a pay check (F4.5, evidencebook-deal#93, evidencebook#273): a
  `deal check` input for a `pay` may state `"counterparty_class"` (`person_agent`,
  `business_agent` or `shop_preauth`, SH §7), which is sealed as the pay check's
  top-level member and read by the History card. A `shop_preauth` deal is no
  longer counted (it stays in `in_window.not_counted`); a deal that states none is
  counted and in `class_missing`, as before. It is the caller's own statement,
  never inferred from the rail, and is refused on any action but `pay`, on another
  value and on a deal in the typed action records. Requires evidencebook
  `v0.12.2` (evidencebook#288).

- `TestADealPageWithoutWebCrypto` runs a built deal page with no `crypto` global
  again, against capsule-viewer/go v0.9.0 (evidencebook-deal#54): the runtime
  verifies every record, the graph closure and the coverage with its plain
  SHA-256, and the deal view says it cannot check the report's signature there
  and shows none of the report; with WebCrypto the page shows it. v0.9.0 has no
  switch to turn the SHA-256 fallback off, so the "unable to verify in this
  viewer" page is not covered end to end here.

- The receipt's settlement and witness lines are drawn from the viewer runtime's checks
  (`capsulectl.deal-view/v0`; evidencebook-deal#94, #104). The settlement line reads
  **passed**, "the payment's proof matches its invoice", only when the runtime's
  `paymentPreimageCheck` answers `PASS` for every settled Lightning leg. The witness line
  reads **passed**, "recorded by <witness> at <time>", only when `readerCadenceWitness`
  answers `PASS` for your book (on the receipt with the other party's half, for both books);
  an answer of no receipt reads **missing**. Any other answer, and a page whose runtime
  predates the checks, leaves the lines as they were. Builds against capsule-viewer `v0.10.0`
  (capsule-viewer#215); the presentation goldens are rebuilt (the two monthly-report pages, which capsulectl-book writes, are rebuilt with the book at `v0.12.2` on viewer `v0.10.0`, since evidencebook still pins `v0.9.0`; their bundles are unchanged): a book whose cadence witness is
  `not_configured` now reads **missing** on the witness line, where it said "not checked".

## v0.1.0-rc29

This section also lists what `v0.1.0-rc28` carried: that tag was cut before this CHANGELOG
was, and the History card's 3-month default and the Lightning payment (evidencebook-deal#90,
#99) and the receipt's frame 8 record lines (#101) are new in rc29.

- Builds against capsulectl `v0.1.0-rc29`, evidencebook `v0.12.1`, capsule-viewer `v0.9.0`
  (capsule-viewer#196, #197, #200, #201 since rc27, and #205, #206, #207), agent-action-capsule `v0.18.0` and checkpointed-local-log
  `v0.3.0`. The presentation goldens are rebuilt (`build-fixtures.sh` with capsulectl-book
  `v0.1.0-rc29`, then the snapshots and pages). The share test for the customer's name leaves
  the viewer's runtime scripts out of its check for the bare word "customer": that runtime says
  "customer" since viewer 0.8.0, whoever the customer is.

- The receipt's stamps ladder draws 9b frame 8's lines from their records in the deal view
  (`capsulectl.deal-view/v0`): ON THE RECEIPT, THE RECORD and the consumer words for approval, order,
  settlement and close, and an `agreed` stamp needs the other party's record pointing at yours
  (evidencebook-deal#94, #101). The Lightning preimage check and the witness check stay open on #94.
- `deal note --kind payment_received` also seals a Lightning payment, from
  the payee's own wallet: `--ln-invoice` (the invoice the wallet issued) and
  `--ln-transaction` (its inbound transaction). The payee_observed leg names
  the payment by `ln.payment_hash`, with what the wallet credited as
  `received` and what it deducted as `receive_fee`. The wallet's preimage,
  once its SHA-256 is the payment hash, is sealed on the step's record
  (body_evidence `preimage`, evidencebook#260; Steven, Buzz 79b46dfb
  item 5); the BOLT11 string and the receiving node id are never sealed
  (devcon-workshop#12). Lightning is a rail without recourse: a `pay` check
  on it pauses, as Zelle or crypto does.
- The History card (`standard_history/1`, evidencebook-deal#85, evidencebook#237):
  `deal standard-history declare --holder-kind person|node` (declared once, and
  declaring again never turns answering back on; with no declaration there is
  no card), `policy [--months N | --all]` (its own file
  beside the deal store, default the last 3 months as 9b frame 4 states it, never
  part of the share policy) and `card --out FILE [--shared]`, which seals the card as a Capsule
  and writes the bundle with the `x-standard-history/v0` block (policy and
  `as_of`; the deal entries only in your own copy). The card is as of the
  newest finished cadence tick, never one deal book's checkpoint, and counts
  deals from their sealed records (evidencebook v0.11.0, x-deal-v0 PROFILE.md
  section 11). `deal respond` answers a `standard_history/1` request over
  `full_history` with evidence-interop's signed envelope: a node answers by
  default (declaring it prints a notice; `--no-answer` opts out), a person
  does not (`--answer`). A holder that does not answer is `policy_declined`,
  an undeclared one `derivation_unsupported`, and no finished tick, or a deal
  with no amount, `coverage_unsatisfiable`. `happened` reads `receipt_only` or
  `no_record`: the card does not yet read the other side's half from the shelf
  (the composed page of evidencebook-deal#86 checks it, the card does not).
  A card is as of a tick cut after the last change of declaration or policy:
  run `deal tick` after a change. The settings files must be owner-protected
  regular files. Required evidencebook `v0.11.0` when it shipped; this build requires `v0.12.1`.
  rc28 shipped the card with a 12-month default. A holder who set no policy
  (the default is never written to the file) moves to the 3-month window and a
  new policy digest; `policy --months 12` keeps the old window. A card built
  before the first `deal tick` after the upgrade applies the new window to an
  anchor cut under the old default, so run `deal tick` first.

- Requires capsule-viewer `v0.7.0` (the frames merged since 0.6.0:
  capsule-viewer#186, #187, #188, #189, #190, #192, #193). The presentation
  goldens are rebuilt with `build-fixtures.sh` against it (capsulectl-book from
  evidencebook#244), their snapshots regenerated with Chrome.
- The receipt with the other party's half (the 9b frames 6 and 7;
  evidencebook-deal#78, #79, #81; design note `docs/design/two-sided-receipt.md`).
  The deal view gains a second module, `capsulectl.deal-view-composed/v0`, for a
  deal composition: it checks the composition and each of the other party's
  answers itself, and says "Stamped by both sides", "Your receipt only" or "No
  record". The other party's side shows only their key's first digits, never a
  name. A record that is in the file but did not check gets its own box,
  never one that reads as missing. The single-book deal page no longer reads a
  composition.
- `deal compose --html --keep` builds that page and carries your keep copy in it
  whole, so the page checks your side too. The page says on its face that it is
  your private copy and that the composed bundle is the file to share. The
  composed bundle is unchanged.
- `deal compose` takes a single answer with `--keep`, and refuses an answer
  recorded as not holding up.
- A Mesh payer's receipt is read the same way: both halves must name the same
  exchange by the client's nonce and request. Tested on a synthetic composition
  until the plugin builds the real one (Mesh-LLM/capsules-plugin#196).
- A bundle with two rounds reads its latest round (#81).

## v0.1.0-rc26

Built against capsulectl v0.1.0-rc26 and capsule-viewer v0.6.0 (RELEASING.md).

- The deal view draws the receipt as Steven's 9b frames do (evidencebook-deal#82,
  #80): a verdict banner, the receipt card in the user's own words, "Your side
  · in your book" with a time on every line, the other side marked as not in
  this file, a closed "The stamps" list in the five words (passed, failed,
  missing, not checked, unclear), "What this receipt proves" and "What it
  doesn't", and "Tell your agent". The steps, authority, what was told and the
  anomalies sit under "More on this deal". A shared copy speaks of the holder
  in the third person. The other side's half, and a Mesh node's copy
  (evidencebook-deal#78, #79), are not drawn yet.

- The presentation fixtures are rebuilt with capsule-viewer 0.6.0's page runtime and
  capsulectl-book rc26 (`BOOK_REF=v0.1.0-rc26`; the outcome report's new headline and tile wording); the
  fixture builder no longer fails on a second run (it removes the `10-composed-private-half` directory it
  writes `--bundle` into).

## v0.1.0-rc25

Built against capsulectl v0.1.0-rc25 and capsule-viewer v0.5.0 (RELEASING.md).

- Pages carry their bundle in a JSON data element (capsule-viewer#109, #167):
  the share gate reads that element as the bundle, and still reads a page
  built before it by the `window.__BUNDLE__ =` prefix (#41, #48).

- The user's own receipt line for a flagged check gives what caused the flag
  first, in the rules' own words (a rule the checker could not evaluate is
  said so), and then, after what the user and the agent chose, the rules the
  ruleset declares but the checker does not measure, as "not yet checked", so
  the unmeasured rules no longer read as the reason for the flag. With no flag
  the not-yet-checked note stands on its own. A shared copy names none of the
  user's rules, and an adjudicator's copy keeps the not-yet-checked note and
  who chose what, without the reasons (#55).

- The rules checker is held to the acting role's pin: `deal check` checks the
  checker's answer against that role's definition digests
  (`rules_checker.roles`, capsule-cli#492), so swapping one role's pin by
  `profile update` changes only that role's check; a role with no entry, or
  one that names a pack alone, is held to the flat digests, and a profile
  without roles checks as before. Sending the checker the role's pack is a
  separate change that needs the `external-check-input/v0` schema to carry it
  (#66). The two witness-delivery wording assertions follow
  capsule-cli#489 (#68).

- `deal terms-leg` writes the buyer's signed terms leg, the file
  `deal note --kind payment_received|delivered --terms` answers, so scripts
  and outside developers share one public command. It signs with the profile's key, or
  with `--ephemeral` a key made for the run, and touches no deal store (#70).

- The spending-limit gate masks a random id by the copy's JSON structure: only
  the value of a `cose` member found by walking the copy's real JSON regions,
  not any quoted id-shaped string found by a pattern. Prose that imitates
  JSON (`My "limit": "8USD-Per-Month-Plan-2026".`) and a field value shaped
  like an id (a seller's agent claim) no longer hide a bound from the gate
  (#62; design note `docs/design/limit-gate-structural-id-mask.md`).

- The x-deal-v0 profile is EvidenceBook's `dealprofile` (evidencebook v0.10.0,
  evidencebook#93). The plugin seals records, steps and reports through it, and
  `skills/deal/profile/` and the plugin's copies of the schemas, sentence marks
  and baseline vectors are gone: the skill points to EvidenceBook's
  `dealprofile/x-deal-v0/`. No sealed byte changes; the baseline vectors from
  #65 still pass. `scripts/run-demo.sh` reads the profile from
  `DEAL_PROFILE_DIR`, or from the evidencebook module when run from source.

- The deal skill's "Points of no return" table lists a seller's `offer` and
  `represent`, and a test fails when the table and the plugin's action list
  differ (#23).

- The presentation goldens' tests run on capsule-viewer/go v0.3.0
  (`pageruntime`, `presentation`) in place of the names capsule-cli removed in
  capsule-cli#486, and `sameViewer` accepts that one runtime only. `page.html`
  and the snapshots under `testdata/presentation` are refreshed in Chrome; their
  summary line reads "checkpoint signature not checked here". The Node check
  that ran a deal page with no `crypto` global (`TestADealPageWithoutWebCrypto`,
  a script capsule-cli deleted with its vendored runtime) is gone, and no
  test now runs a built deal page without `crypto` (#54) (#51).

- `deal compose --html FILE --keep KEEP_BUNDLE` writes the composer's
  both-halves page: the composed bundle with the composer's own keep copy
  (`deal report --bundle`) shown as "Your half (private)", joined at the page
  (evidencebook-deal#31, option b). composed/v1 is unchanged: the keep copy
  rides in the page's bootstrap, never in the composed bundle, so share and
  export carry served copies only. The page is refused, and no bundle is
  written, unless the keep bundle verifies VALID, is audience keep, and names
  the composition's deal id and its composer. Join states still come from the
  served members alone. Adds the wording key `report.private_half`.

- The `deal report --html` page runs capsule-viewer's own page runtime
  (`go/pageruntime` in capsule-viewer/go v0.3.0), pinned by its digest, in
  place of capsule-cli's vendored build. Its bootstrap renders through the
  viewer's `renderEvidenceGraph`. The summary line now names the one signature
  check the page didn't run ("checkpoint signature not checked here", from
  capsule-viewer#118). Prerequisite for capsule-cli#472 (#49).

- The deal words move out of capsulectl's built-in wording pack into this
  plugin's own pack, `capsulectl.deal-wording/v0`
  (`internal/deal/assets/wording/capsulectl-deal-en.json`, 268 keys, names
  unchanged; capsule-cli#428 step 3). The plugin registers that pack with
  `cli.RegisterWording`, and its handshake carries it, so `capsulectl profile
  update --wording` and `doctor` check a pack pinned on a deal profile against
  the deal keys as well as the core's. A pack whose `card.terms.*` entries could
  hide a term is still refused. A pack made from the core's pack alone is now
  refused with a line naming this plugin. A pack pinned earlier holds every key
  and is still used. Builds against capsule-cli `5745be57` (unreleased;
  capsule-cli#480 merged).
- Builds against capsule-cli `ed9c4db4` (unreleased; capsule-cli#479 merged), whose core
  has no deal view of its own: `capsulectl presentation list` and `build` use the
  deal view only from this plugin, with its files unpacked beside the launcher,
  and otherwise resolve a deal receipt to the fallback page.
- The deal view (`capsulectl.deal-view/v0`, the deal section of a receipt's
  page) is this plugin's own presentation module now (capsule-cli#428 step 4):
  its files are in `internal/deal/assets/`, the handshake offers it, and a
  release ships `deal-view-VERSION.tar.gz` to unpack beside the launcher
  (README). `deal report --html` pages are unchanged; with the files unpacked,
  `capsulectl presentation build` also carries the deal view. Builds against
  capsule-cli `d5f42c06` (unreleased; capsule-cli#478 merged), whose handshake can
  state a plugin's presentations.
- Builds against capsule-cli `8a856b75` (unreleased; capsule-cli#428 PR B), whose core names no company
  plugin. The deal plugin now says the words the core no longer does: `bundle`,
  `disclose` and `permalink` on a deal profile without `--deal` name the deal to
  pass (`name the deal with --deal DEAL_ID`, `--log-id` does not apply), and the
  canary's alarm still reads `canary alarm: ...` and exits 6. `doctor
  --install-check` reports the deal skill under `skills` (it was `deal_skills`),
  and the installed plugin must declare `core_version` to be checked against the
  core. `cll list` on a deal profile with no `log_id` now gets the core's generic
  message (the deal plugin keeps several logs in its store; name one with `--log-id`).
- `canary` is a deal verb now: `capsulectl deal canary run|watch` (it was
  `capsulectl canary run|watch` in capsulectl, which drops its own with
  capsule-cli#428). `--expect-version` takes this binary's release tag, and
  the sealed note and `binary_version`/`binary_commit` name that tag and
  commit, with the capsulectl core's version beside them (`core_version`). Behavior, flags, the exit code 6 on an alarm and the
  alarm's words are unchanged. Its guide (`docs/CANARY.md`) and the two GitHub
  Actions templates (`examples/canary-watch.yml`,
  `examples/deal-harness-watch.yml`) moved here from capsule-cli; the
  templates install this plugin's binary and run `capsulectl-deal canary
  watch`, which needs no capsulectl beside it.
- Builds against an unreleased capsulectl (capsule-cli `005e7e97`, capsule-viewer `389c1bac` (its Go module pinned by pseudo-version) and agent-action-capsule `1d1752bf`, all unreleased; capsule-cli#428) and
  names no deal-specific export of its host package where a generic one
  exists: the plugin owns its store through `cli.OpenOwnedTarget` and marks it
  (`plugin_owned`, written under the writer lock by every write session and by
  `deal init`, never on a read), `bundle|disclose|permalink --deal` and `cll checkpoint cadence`
  come from `cli.ExtendBundle` and `cli.GraftCommand`, and `deal init` now
  writes `log: deal` into the profile. Deals behave as before; a deal profile
  saved without `log:` is still read as the deal plugin's until the core drops
  that default.
- A deal page opened by the host's viewer (`presentation build`, a shared copy) now
  reads "None of the bundle's checks failed, but some were not run" where it read
  "Contents checked", and its status is `incomplete`, not `verified`. The
  browser has no checkpoint verifier, so under agent-action-capsule 0.8.0's strict
  mode the page does not check the checkpoint signature
  (`checkpoint_verifier_unavailable`); it does check every record's producer
  signature, and a forged signer now reads `failed`. The page scripts assert this
  is the only check not run. No capsulectl-deal release can be cut from this
  until capsule-cli, capsule-viewer and agent-action-capsule are tagged.
- Builds against evidencebook v0.8.0. The deal cadence verifies witness receipts under a directory built from the profile's pinned witness (endpoint and key), so an unpinned profile trusts no witness and no receipt reads as witnessed; a receipt that cannot be checked leaves its tick unwitnessed and pending (evidencebook-deal#32).

## v0.1.0-rc23

The first release of capsulectl-deal and the deal skill from this repository
(capsule-cli#326), built against capsulectl v0.1.0-rc23. A capsulectl-deal
release takes the version of the capsulectl release it pairs with
(RELEASING.md), and requires that capsulectl: any other capsulectl reports a
version mismatch.
capsulectl no longer installs it by name (`plugin install deal`); download
`capsulectl-deal-<version>-<os>-<arch>` from the release, check it against
`SHA256SUMS` and its attestation, then run
`capsulectl plugin install ./capsulectl-deal-<version>-<os>-<arch>`.

### Known issues

Still open as of capsule-cli v0.1.0-rc23, where they are tracked (first
listed in its rc22 release notes):

- The deal journey shows only an OK or a no as your row (capsule-cli#415).
  Another answer to a pause, such as asking to verify the contact first, has
  no row of its own.
- A taken-down listing still counts (capsule-cli#384). There is no way yet to
  record that a listing came down, so its classes still count in
  `listing_classes`.
- The deal profile's checker needs `jsonschema` (capsule-cli#422). Install it
  before you run `skills/deal/profile/check_profile.py`.
- The deal page's close date is in UTC (capsule-cli#426) while the page's
  other times are local.

### Changed

- A release of `capsulectl-deal` seals its own build as each record's
  producer, `capsulectl-deal <tag> (<commit>)`, instead of the capsulectl core
  it embeds, so a record names the deal build and commit that sealed it
  (capsule-cli#326, design note section 4.2). A build without a release still
  seals the core's `capsulectl <version> (<commit>)`. Shared copies keep the
  new stamp. Records already sealed are unchanged (#3).

### Added

- The deal skill has a Sale procedure (`skills/deal/SKILL.md`, "Sale"): what
  the agent does, step by step, when the user sells, starting from
  `deal sale new` (#34).
- A nightly workflow, `.github/workflows/core-main.yml`, builds capsulectl
  from capsule-cli main and runs the deal page and portability checks against
  it, so a core change that breaks deal pages shows before a core release; a
  failed run comments on one open issue (#4).
- The deal report's page data carries `page_producer` (the build that made the
  page: name, version, commit) and, on a release, `release_notes` (this
  repository's release). With capsule-cli#448 the page compares builds only by
  name, marks a step capsulectl sealed as an earlier build, and links this
  plugin's release notes (#3).
- The release workflow, `.github/workflows/release.yml`: a pushed `v*` tag is
  built, attested and published once a release reviewer approves the run. It
  publishes only a tag a shared copy keeps as a producer version, and only
  against a published capsule-cli release (#3).
- The user's own deal report carries `card_openings`: for each answer given on a
  card, the answer's step and the card's opening `{nonce, text}`, which
  recomputes to the approval's sealed `card_commitment` (`rendering_commitment`
  in a typed record). A page can then show the card the user answered on,
  checked when the page is written. No shared copy
  carries it. It rides in the user's own sealed report; no approval or other
  step record changes. The page check refuses to write a page whose card
  opening does not recompute, names no approval, repeats a step or rides on a
  shared copy (evidencebook-deal#1).

### Moved here from capsule-cli (capsule-cli#326)

- `capsulectl-deal` and the `deal` skill now live in this repository, imported
  from capsule-cli at commit e0511c4 (capsule-cli#446). Their history before that is
  capsule-cli's. The import itself changed no command, flag, output or
  record; the entries above came after it.
