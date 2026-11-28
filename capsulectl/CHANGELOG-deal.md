# Changelog

## Unreleased

- A seller's agent claim is sealed without its verdict, and the verdict is its own `claim_authority` record right after the claim (evidencebook#607, PROFILE.md rule 12; this requires evidencebook v0.24.0). A counterparty's copy of a sale under the representation rules (evidencebook-deal#244: the ruling that an unauthorized claim is never on the counterparty copy) then carries each such claim and its words, so the buyer can still check what it was told against them, and withholds every `claim_authority`, the represent trail (snapshot, check, approval), the seller's check of any step and the answers to checks, every act sealed unchecked, every disclosure (the approved ones too, so the buyer cannot tell which telling was not approved) and every claim sealed with its verdict on it (a claim sealed before this change, whose words are then not opened either). Its merchant email rows carry no "approved" amount or basis, which are read from the withheld checks. Its report derives no kind, line, result or count from what it withholds and says "Not shared: how the seller's agent was authorized for what it said and did", never "none"; the copy's list of what it leaves out names it too. The default adjudicator copy also withholds every `claim_authority` (#231). A step that stopped between a claim and its verdict is finished by the next `note`, `check`, `close` or `rules-verdict seal`, or by `deal tick` before it seals a deferred email, from the verdict the claim was noted with. Known limits: in a rule 12 sale under x-deal-v0 the buyer's copy no longer names the seller's terms pack (the "under the seller's terms, pack X" line, which lives in the withheld check), while the adjudicator copy still does, and carrying that line into x-deal-v1, apart from the check, is tracked with evidencebook#626; copies shared before this change still carry the verdict fields and cannot be recalled; a withheld record keeps its place in the log, so a buyer who counts withheld steps before a claim can guess, without proof, that one with none before it had no check (evidencebook#607 note section 7); the close record and the position of an unchecked act are for evidencebook#613.
- `deal report --share adjudicator` and `disclose --deal ID --share adjudicator` on a sale no longer show the seller's agent's claims by default (Steven's ruling 2026-11-21, "yes, uniformly"): every claim record the agent made, authorized or not, is withheld (so the sealed `unchecked`, `rule` and `source_kind` fields and the committed words stay out; the copy still verifies), its step reads "Claim recorded (withheld)", and the records that lead up to a represent check (the snapshot, the check, its approval) are withheld and listed as "A record kept for the user only (withheld)", so the copy does not say which claims had a check before them. The copy's `claim_openings`, `representations` and "Unverified: ..." line leave out the agent's claims; a claim that is not the agent's is unchanged. The seller's choice is now `--include-agent-claims`, which shows all of it (`--include-unauthorized-claims` still works as its earlier name); its help and the "Not shared" / "Shared by the seller's choice" lines say so, and the copy's list of what it leaves out (also sealed in the disclosure record) names "the claims your agent made (their words and whether you authorized them)" wherever the claims are not shown. A counterparty's copy is unchanged here (evidencebook-deal#231; the counterparty side is #244, evidencebook#607).
- `capsulectl-deal verify --bundle` now checks an x-deal-v1 copy's standalone report with evidencebook's report rule (`xdealv1.VerifyReportBundle`, evidencebook v0.22.0): the report against the seq 1 agreement and the records its rows rely on, which the copy's `x-deal-v1` extension carries (evidencebook#588, #597). It is registered through capsule-cli's `SetDealV1ReportVerifier` (capsule-cli#587, #596; go.mod requires capsule-cli v0.1.0-rc46, the first release that hands the hook the extension). A report that passes the rule is no longer `report_rule_not_checked`; one that fails reads `report_rule_failed` and the bundle is invalid. evidencebook-deal#233.

### Added: capsulectl-deal answers an adjudicator in full and rules over four books (#861)

- `deal respond` answers an adjudicator's request for an x-deal-v1 deal (derivation `deal_adjudicator_copy/1`, subject the deal's correlation) with the full adjudicator copy: the whole deal book, the copy of the other side's book the party holds, and the book's facts trimmed to the copy (`xdealv1.TrimBookEvidence`), in one answer file `{"request", "bundle", "facts"}`. It has no selective form. A request for a deal the profile does not hold is refused `no_such_subject`, one for an x-deal-v0 deal `derivation_unsupported`, one with an `expected_pin` `coverage_unsatisfiable`, and one whose `--requester` is not the adjudicator the agreement names `not_authorized`. `deal respond --help` says so. Each answer and each refusal of a held deal is linked by an x-deal-v1 `answer` record in the party's journal.
- `deal ruling --answer FILE` (at most one per party, beside the case-opening `--copy` files) reads the case with `xdealv1.GatherCase`: the ruling carries `sources`, and the output lists each side's book read with each record's time and what timed it (`own_stamp` or `holder_link`). A ruling against a side none of whose records was read is refused (Steven's ruling 2 of 2026-11-24, docs/design/861-four-books.md §6). Without `--answer`, `deal ruling` reads the case as before and writes no `sources`.
- `deal receive` prints a ruling's `sources`, a source the adjudicator did not read as "not shown to the adjudicator", and a fork as "the signer's book forked at N". A capsulectl-deal release before this one refuses a ruling that carries `sources`.
- `capsulectl/go.mod` requires evidencebook at 341af3ed, the library with the full adjudicator copy and `GatherCase` (#877).

### Added: a buyer's deal that allows pay seals what the user confirmed (evidencebook#839)

- `deal open --records typed --authority-line` (seals nothing) prints the line to show the user before a buyer's deal that allows `pay` opens: "I'll record that you authorize: pay up to <max> for <item>. You chose <seller>; the rules check the amount, not the seller. Is that right? (yes/no)", or "You haven't picked a seller, so this doesn't limit who gets paid." when none was named. The amount is in the user's own unit.
- Such a deal now opens only with `intent.rendered`, that line as shown (refused if the open's own terms render another), and `intent.confirmed`, the user's own words answering it, and says "Recorded." once sealed. Its `task-authority/v0` carries `asked`: the item, the seller the user picked (a keyed fingerprint, or `unconstrained`), and commitments to the line shown and to the yes. Everyday Rule 26 can then cite a record of what the person said; rule 26 still reads only `allowed` and `max_total_minor`.
- Deals sealed before keep their records unchanged. The Rust crate and the Python Goose reference are not changed here.

- `deal canary watch` signs its read of the witness with the log's checkpoint key, as the witness now requires to show a log's size, root and time (capsule-anchor#110, ruling u226). It takes the key from `--profile` (whose cadence log `--log-id` now defaults to and must name) or, off the canary's host, from `--read-key-file` or `--read-key-env` with the profile's checkpoint seed. A witness that refuses the key for the log raises the alarm, as a key change did; a refusal for a skewed clock or a malformed read, and an answer without the checkpoint, leave the log unread (exit 1). It follows no redirect now, so the signed read reaches only the witness named. `examples/canary-watch.yml` and `examples/deal-harness-watch.yml` pass the seed from a repository secret. Upgrade the watcher before the witness is redeployed: an older one reads the gated answer as a rewritten log.

### Changed: a `standard_history/3` or `/4` card is refused when a deal's `agreed_at` is later than its first witnessed time (agent-action-capsule#696)

- `deal standard-history card --derivation standard_history/3` (or `/4`) refuses the card, naming the deal, when a deal's `agreed_at` is later than the record time of the holder's own agreement from a witness the agreement names by more than 300 seconds (`xdealv1.CheckAgreedAt`; agent-action-capsule `spec/standard-history-v0.md` §11.2 "Agreed no later than witnessed"). A deal no named witness timed, or whose agreement names none, is not checked.

### Added: `deal checkpoint proof` (x-deal-v1, #788)

- `deal checkpoint proof --deal ID --copy FILE --out FILE` writes the stamp proof of your deal book and the witness receipts your shelf holds, for the checkpoint your copy rests on (`deal report --share ... --bundle`). Send it to the adjudicator with your copy: `deal ruling entries --proof` reads it to say "entered" rather than "entered, not witnessed", when a witness your agreement names has a receipt for that checkpoint. With no such receipt yet, the file changes nothing: the entry stays "entered, not witnessed", and the output says so. A later run replaces the file.
- It carries no record content, only checkpoints, proofs and witness receipts, so it is not a disclose act.
- It is read-only: it never seals, stamps or contacts the witness. When no stamp covers that checkpoint yet, run `deal tick` once one is due.

### Added: a receipt says when the checkpoints stopped (capsule-cli#600)

- `capsulectl deal report` adds "Checkpoints stopped at <time>: ..." to the receipt's assurance text (and `assurance.checkpoints_stopped` in its JSON) and to the receipt email when the newest checkpoint tick is more than two cadence intervals old, so a stalled schedule does not read as a clean history. It says nothing while the schedule is on time or before the first tick, and it is not on the sealed page, which checks offline. The line points at the capsule-cli README's "Keeping the poll running"; capsulectl still has no scheduler or daemon.

### Fixed: `deal compose` seals only what a refusal's responder signed (#872)

- A composed refusal member now carries only `request_digest`, `reason`, `issued_at`, `key_id` and `sig`, and its digest is over those. Before, compose copied the whole recorded refusal, so a `detail` or a `policy_digest` the responder's signature does not cover was sealed under your composition as if it were the responder's evidence. The refusal still verifies, and recording a response (`deal request --response`) is unchanged.

### Added: the other party's half in your own x-deal-v1 copy (#850)

- `deal report --bundle` (your own copy, no `--share`) carries the other party's half when your store holds it: their signed agreement with the stamp proof that covers it, the same thing the History card reads for "stamped by both". A shared copy never carries it.
- `deal verify --bundle` checks it from the file alone and reports `counterparty_half` on the x-deal-v1 entry: `verified` with their key's first 16 hex digits, or `failed`, which makes the copy INVALID. A copy without it has no `counterparty_half` member, as before. It shows their signed, stamped agreement, not that they confirmed a later record.

### Added: `deal ruling entries`, and `deal receive` of the other side's copy (x-deal-v1, #773)

- `deal ruling entries --deal ID --ruling DIGEST --copy FILE [--copy FILE] [--proof FILE ...]` (seals nothing): the adjudicator sees, from each party's copy (`deal report --share adjudicator --bundle`), whether that party entered its ruling in its deal book. The answer is one of:
  - "entered";
  - "entered, not witnessed";
  - "ruling not entered by the <party> as of its checkpoint at <time>";
  - "not shown entered".

  A party shows as not having entered only by a checkpoint after the ruling's witnessed time. The ruling's witnessed time is the time a witness the agreement names gave the adjudicator's stamp; with none, a copy without the entry shows nothing. The ruling's own time shows as "sent <time> (adjudicator's statement)", never as proof of delivery. `--proof` takes a party's stamp proof file with its witness receipts. It never says a party refused.
- `deal receive` takes the other side's copy (`deal report --share counterparty --bundle`): it takes each record the copy carries for the rows, rulings included, so a ruling the other side received reaches you too, and an adjudicator that gave each side another ruling shows as a fork.
- The share gate no longer refuses an x-deal-v1 copy for a random identifier that happens to look like a card number (#787). It takes the deal's own ids out first: the deal id, and the shelf log ids the copy's agreements name. It does so in the copy's text and in the record payloads the copy carries as base64. A log id whose last segment is 32 hex characters (`deal/…`, `case/…`) is an id too. A card number written in a copy is still refused.

### Fixed: the share gate reads a card number by its network prefix (#787)

- The share gate refused a copy for any 13 to 19 digit run that passed the card checksum (Luhn), so an honest merchant order number such as `1002003004005005` blocked `deal report --share`. It now reads a card number as a payment reference's check does (#772): a card network's prefix and the checksum. Both checks now also read Diners Club 30x, Troy, BORICA, China T-Union, GPN, Uzcard, Napas and HUMO. Card numbers of the major networks are still refused, alone, with separators, or with a code after them; UATP airline cards, which start with 1, are left out on purpose.

### Added: a ruling's stamped time, and "not independent (shared operator)" (#819)

- `deal receive` of a ruling says when it was stamped (`stamped_at`): the earliest time a witness your agreement names registered the adjudicator's stamp, from the receipts the file carries, or "unknown (no receipt from a witness the agreement names)" when none does. It is never the ruling's own time, which is the adjudicator's word.
- When the adjudicator's key is a party's key, or both are bound to one controller at that time, it says `"independence": "not independent (shared operator)"`. It never says a ruling is independent. The bindings checked are those the file carries and those you give with `--binding`, such as ones the adjudicator's operator publishes. A revocation counts only when you give it with `--revocation`: its time is one key's word, so it never comes from the adjudicator's file.
- `deal ruling` writes the adjudicator's witness receipts for the stamp into the file, and takes `--binding` to deliver bindings with it. Run again once the witness's receipt is back, it writes the file again with it.

### Added: an adjudicator can check whether a dispute is decided by the disputed side's own word, and rule only on that

- `deal ruling check --deal D --row R --dispute F --answer A [--answer B] [--copy C] [--ruling RF]` reads the case as `deal ruling --answer` does and prints `xdealv1.DecideCase`'s result. That is `decisive` (against, value, and the deciding record with its time) or `needs_person` with the reason. It seals nothing. With `--ruling`, a ruling you sealed on the row, the side the dispute is against is read only as far as that ruling read, from the same sources (other answers are refused), so the check gives the answer the ruling was sealed on. An empty `--ruling` or `--decided-by` is refused.
- `deal ruling --decided-by RECORD_ID` (with `--answer`) seals the ruling only if the check decides that row on that record, with that value and against that side; otherwise it refuses and says why. The ruling record does not carry the decision.
- `capsulectl/go.mod` requires evidencebook at c4ac3579, the library with `DecideCase` (#943).

### Fixed: an adjudicator's profile key (#814)

- `deal ruling`'s help used to say to switch an existing profile's checkpoint key to the record key with `profile update`. A profile's store has checkpointed from the start, so after that switch the stamp failed, behind a generic "operation failed". The help and the refusal now say to make the adjudicator's profile with one key for both: `capsulectl --profile NAME deal init --dir DIR --signing-key-file KEY --checkpoint-key-file KEY`. KEY is the key file you sign records with, so the key id your agreements name stays. A profile that has checkpointed with another key cannot switch: make a new one, with another name and `--dir`, leave the old store as it is, and seal nothing more with it (one key, one live store).
- A profile whose store has stamped with another key is refused before anything is sealed, with that advice, including after a stamp that failed on the key.
- If you followed the old help: switch the profile's checkpoint key back to its earlier file (`capsulectl --profile NAME profile update --checkpoint-signing-key-file DIR/checkpoint.seed`), and the store is readable again. If you also changed `--checkpoint-trusted-key`, as the old refusal said, set it back to the earlier checkpoint key's public key in the same command. A ruling the failed run sealed stays in that store, unstamped and never sent: make the ruling again from the new profile.

### Added: `deal ruling`, the adjudicator's verb, and `deal dispute --challenge` (x-deal-v1, #773)

- `deal ruling --deal ID --row ROW --value met|not_met --against buyer|seller|none [--final] [--forum F] [--remedy-kind K --remedy-due T [--amount-minor N --currency C]] --dispute FILE --copy FILE [--copy FILE] [--out FILE]`: the adjudicator an agreement names seals its ruling on a disputed row in the deal's case book (evidencebook#615), stamps it (a new stamp when none covers it yet), and writes the file both parties take with `deal receive`, which names the ruling in each party's book. The ruling is checked against the dispute it was sent (it must name the adjudicator and the row) and one or both parties' adjudicator copies (`deal report --share adjudicator`), each verified; a side without a copy is recorded as not read. The adjudicator's profile must checkpoint with its record key. Run again, it writes the same ruling and seals nothing; a second final ruling on a row that differs from the first is refused. A ruling counts only from the deal's case log.
- `deal dispute --challenge RULING --grounds FILE`: seals a dispute of kind challenge naming a ruling of the deal you hold and the SHA-256 of your grounds. The grounds stay with you; only their SHA-256 is sealed. A challenge changes no row status: the first final ruling still applies and is never replaced; the adjudicator may issue a later ruling that is not final (#773). A ruling you do not hold is refused as unavailable.
- `deal receive` of a ruling prints its digest (`ruling`), the one `--challenge` takes, and `--out FILE` writes the statement your book sealed to name it: send that file to the other side, so the other side holds it and counts your later records.

### Changed: the deal skill teaches receiving a ruling and challenging it

- The skill's dispute section says how a party takes a ruling: `deal receive ruling.json --out naming.json`, sending the naming statement before anything later; reading `stamped_at` and the "not independent (shared operator)" line as they are, never as "independent"; `deal dispute --challenge`; and keeping the other side's copy with `deal receive`. The referee's side stays out of the skill, as other audiences are for disputes a person runs; `deal ruling --help` covers it.
- `deal ruling --help` says how a new adjudicator makes its key (`capsulectl key generate --output KEY`, whose printed public key the parties name) and that its stamps go to the witness as hashes only.

### Changed: `deal tick` bounds the time it spends delivering pending checkpoints (evidencebook#937)

- A `deal tick` run (and `deal tick --now`) no longer takes one witness round trip per pending checkpoint. Deliveries go oldest first and stop at the first checkpoint that gets no receipt and has not failed for good, since the witness holds every later one until it has a receipt; one failed for good is passed over. A run without `--wait-up-to` also stops once it has spent `--deliver-for` (default 5s, `0` for no limit) on witness requests: a request still running then is ended and recorded as `witness_timeout` (no answer within this run's budget; its text, capsule-cli's, still calls the witness or the network slow) and tried again at a later tick, so a witness that accepts the request and never answers costs the run its budget, not the witness client's 30s (evidencebook#960; `capsulectl/go.mod` requires capsule-cli at 16ec5bc9, capsule-cli#646). No request starts with under 2s of the budget left, so a budget under 2s sends nothing. A `--wait-up-to` run has no limit unless `--deliver-for` is given, and the budget is per poll.
- A checkpoint whose request was ended is listed in `pending` as `witness_timeout`; each one the run did not send is listed with its stored reason and `not_tried_this_run: true`. The next tick resumes from the oldest.

### Fixed: the x-deal-v1 receipt page shows local times and one witness line (capsule-viewer#423, #424)

- The page `deal report --html` writes for an x-deal-v1 deal shows its times in the reader's own zone. Due times and the window end carry UTC beside them, for example "Dec 2, 2026, 12:56 PM CST (18:56 UTC)". The page's data keeps UTC.
- With `--witness-directory`, when capsulectl verify reports your book's witness time verified, the page says it in one line: "Time-stamped by <witness> at <local time> (<UTC time>), as capsulectl verify reported on your own book. This file carries no witness receipt, so this page did not check it itself." The header line that said only "This file carries no witness receipt…" is left out on that page, so it no longer reads as "not stamped".
- capsule-viewer go is pinned by commit to main bb8169bb (capsule-viewer#427), with no release.

### Added: `deal report --html` writes an x-deal-v1 deal's receipt page (capsule-viewer#405)

- `deal report --deal D --html FILE` on an x-deal-v1 deal writes your own copy's receipt page. Before, it refused with "has no page yet".
- capsule-viewer's x-deal-v1 receipt module draws it from the same bundle `--bundle` writes.
- The page carries this binary's own `deal verify --bundle` report of that bundle. It says "capsulectl verify reported its signature check as: pass. This page did not check the signature itself."; a report that does not pass refuses the copy on the page.
- It takes no `--out`, `--bundle` or `--withheld`, and no `--share` but keep.
- capsule-viewer go is pinned by commit to main 130877b1, with no release. That commit carries #417, #420 (the other party's half), #421 (the witness time and the file-scoped line) and #422 (the runtime).
- `deal report --deal D --html FILE --witness-directory W` also checks your own book's witness time. It finds the stamp proof and receipts as `deal checkpoint proof` does and passes them to its own `verify`, beside the copy. The page carries only what verify reported, never the proof (no copy carries witness evidence, since a stamp proof shows your shelf's checkpoint and position).
- `deal verify --bundle F --stamp-proof P --witness-directory W` reports, in the copy's x-deal-v1 entry, `witnessed: {status, witness, at}`:
  - `verified`, with the directory's name for the witness and the earliest verified receipt's own time;
  - `withheld`, with no directory, no receipt held, or no receipt it could check under the directory;
  - `failed`, when the proof is not of this copy's own signed checkpoint or does not verify, or a receipt fails and none verifies.
  It never changes the verdict.

### Added: a History card carries a cll witness's receipt for its anchor, and `deal verify` checks it (evidencebook#891)

- **What changed.** `deal standard-history card`, in both copies, now carries the receipt a `cll` witness gave for the checkpoint the card is as of. The default hosted witness `https://witness.agentactioncapsule.org` is such a witness. Until now its card carried no receipt, and `deal verify` reported the card's witness as `withheld`.
- **Where the receipt goes.** It rides in a new extension, `x-standard-history-anchor-witness/v0`: `{"witnesses": [one checkpoint.witnesses entry]}`.
  - The card carries exactly that one receipt, over that one checkpoint. There is no stamp proof and nothing about any other checkpoint (Sherlock2's ruling u220, ops evidence-network/inbox.md:632, 2026-11-25).
  - It does not go in `as_of.receipts`. That member holds only receipts whose leaf is the anchor itself (interop profile v1 §6.2.2), and a `cll` receipt's leaf is not.
  - It is interim: a witness that answers SCRAPI gives the anchor receipt `as_of.receipts` already carries.
- **The writer checks it first.** The receipt is checked again under the profile's pinned witness before it is written. One that does not check is left out, the verb prints `witness receipt left out: ...` on stderr, and the card is still written.
- **What `deal verify --bundle CARD --witness-directory DIR` checks.** Before it reports `reader_witnesses: pass`, it requires that:
  - the card Capsule is the root, signed, and its disclosed payload matches its seal;
  - the payload's `anchor` is `as_of.anchor`, and `as_of.checkpoint` hashes to it;
  - the key that signed the card's own log also signed that checkpoint;
  - the one receipt verifies against that checkpoint.

  Each failure is a named finding, and the verdict is then no longer VALID: `card_anchor_witness_in_deal_copy`, `card_checkpoint_malformed`, `card_unverified`, `card_anchor_mismatch`, `card_anchor_checkpoint_mismatch`, `card_anchor_checkpoint_signature_invalid`, `card_anchor_checkpoint_malformed`, `card_anchor_checkpoint_signer_differs` and `card_anchor_witness_malformed`, or AAC's own finding for a receipt that does not verify. An unverified card checkpoint reads `withheld` (`checkpoint_unverified`). With no directory row for the witness it reads `withheld`. The `witnesses` row stays about the card bundle's own log, which no witness sees.
- **Deal copies.** No deal copy carries the extension (u219). A bundle carrying it beside a deal section or a cadence chain fails with `card_anchor_witness_in_deal_copy`.
- **The staple.** The `history_card/2` staple is unchanged. Its receipt is a different form (ER -03 §8.1.2), which the hosted witness does not issue.

### Added: `deal check --history`, the counterparty's verified stapled `history_card/2` in the rules check (x-deal-v1, #666)

- `deal check --history FILE --counterparty-key HEX --checkpoint-key HEX` hands the pinned rules checker, on an x-deal-v1 deal, the counterparty's stapled `history_card/2`, exactly as received, as the input member `received_history`. The file may be a `{history, request, reply}` file, an `evidence-commitment/1`, or a printed staple.
- Before anything runs or is sealed, it is verified with capsule-cli's own staple verifier (`cli.StapleVerdict`, capsule-cli#567) under exactly one `--counterparty-key` and the `--checkpoint-key` its checkpoints are signed by. Both keys come from your deployment policy, never from the History or anything the counterparty sent, and the checkpoint key need not be the counterparty's. The member names the counterparty only (`about: "counterparty"`), the keys it was verified under (`counterparty_keys`, `checkpoint_keys`), and the document exactly as received, with its SHA-256 and `request_binding`.
- A staple bound to our request reaches the checker with `request_binding: "BOUND"`. A staple that checks out but answers no request of ours, such as a commitment, which staples the reply alone, reaches it with `"NOT_PRESENT"`: it was not made in answer to this deal's request and can be replayed, so a ruleset should weigh it accordingly.
- The check is refused, and nothing runs or is sealed, when:
  - the document is not a stapled `history_card/2` (a `personal_history_basic/1` History is not read: the personal-history forms are frozen, #962);
  - the staple is not accepted under the keys;
  - a check is left unrun, such as no `--checkpoint-key`, which the refusal names;
  - more than one `--history` or more than one counterparty key is given;
  - it staples a `history_card/1`, or more than one card;
  - the deal is x-deal-v0, or no rules checker is pinned.
- The verdict binds what the rules read, by digest only: its `received_history` holds the document's SHA-256 and `input_sha256`, the SHA-256 of the JCS of the member as the checker read it, so it says which keys vouched for the card. It is bound whenever the checker started with the History, whatever it answered. No History content or key is sealed or kept, and no shared copy carries the member.
- History alone never makes the verdict NO (D1 condition 1). When the checker denies given the History, `deal check` runs it again without the History; if that run does not deny, or cannot answer, the check asks you (pause) instead of refusing, says why, and seals the checker's own deny unchanged in its rules. `deal settle --sent` checks a pay that read the History again on the same History: give it the same `--history`, `--counterparty-key` and `--checkpoint-key`, or the payment is refused and nothing is sent. Any other document or key is refused too, because its digests differ from the ones the verdict sealed.
- When the checker is given the History and refuses, reports another ruleset, or answers with something unreadable, the check reports the cause alone, never the checker's own words. `--received` cards are still read only by the notice run, whose input does not hold the History.

- `deal init --signing-key-file F --checkpoint-key-file F` keeps an existing key instead of making one (evidencebook#624): a PKCS#8 PEM (the form a Python Goose node's `producer.key` and `checkpoint.key` are in) or a 64-hex Ed25519 seed, so the key id a peer knows the node by stays. The key is copied into `--dir` as a hex seed and the source file is not changed; every key is read and checked before anything is created, so a key that is neither (or an encrypted PEM) is refused with nothing left behind and a corrected retry works, and a key not given is made fresh as before. The result names each kept key's two places: the source file and the copied seed. This starts a new store: the history of an earlier home is not carried into the new profile or its History card, and stays readable where it is. A peer that knows the key id sees a new log from it that starts empty. Retire the earlier home (stop it sealing and checkpointing) before the new store goes live: one key, one live store; until then both answer under one key with different histories. The source key file stays yours to keep or delete.

### Added: `deal list --json` lists an x-deal-v1 deal's own payment legs (evidencebook#920)

- Each x-deal-v1 deal with a book carries `own_legs`: the latest own leg of each payment (`leg`, `status`, `payment_ref_type`), read from the deal book without a checkpoint; an empty list when the deal has a book and no leg, and absent when it has no book. A leg a later own leg supersedes, and a refund or remedy leg, are left out; no reference value, amount or counterparty is listed. The Goose loop node counts its settlements on a rail from it (`loop_settlement_disclosure`, for evidence-network/loop step 06), so legs sealed before that tool existed are counted too. The plain table is unchanged.

- `deal node append` and `deal node records` keep a node's own book on the deal profile's shelf (evidencebook#624): `append --book ID --type T --statement FILE [--subject-ref S] [--cite ID]... [--payload FILE]... [--once MEMBER]` seals one record as a producer claim under the profile's key, and `records --book ID [--type T]` lists a book's records oldest first with their statements. The record types are the node's own names. `--once MEMBER` seals nothing when the book already holds a record of that type with the same value for that statement member, so a repeat after an interrupted run neither doubles nor loses a record. A deal's book (a sale's and a results book too, all under `deal/`), the cadence and the journal are refused, as is a `--type` the evidencebook library writes itself (such as `index_root`, which would make the book unopenable): only the deal verbs and the library write those. The records are the node's own, read with `deal node records`; the one verb that can answer a requester from a node book is `deal node respond`, which serves only what the share policy it is given allows.

- `deal node checkpoint` and `deal node bundle` give a node's own book on the deal profile's shelf its newest checkpoint and Evidence Bundles (evidencebook#624). `deal node checkpoint --book ID [--out FILE]` prints the book's newest checkpoint (its id, the records it covers, its root and signing key), cut now when the book grew, `--out` writes the signed statement (COSE) to a new file for a host to submit to its witness, and `--list` prints every checkpoint the book holds with whether a witnessed cadence tick covers it and how (`cadence_extent`). `deal node bundle --book ID --record ID --out FILE [--at CHECKPOINT_ID] [--disclose] [--witness-receipt FILE --witness-directory FILE]` writes an `evidence-bundle/v2` that proves the record is in the book under a checkpoint (the newest by default), and verifies it before writing it to a new file. By default every record in the bundle travels as its capsule only, its header and payloads withheld and each named by its digest; `--disclose` shows everything the record and the records it links to keep, their headers and payloads (one hop, whatever the link type, which can reach another party's records held in the book, such as a dispute's two half Capsules), and their payloads, and the book commits a permanent disclosure record naming what was shown (not who it was shown to), so its own history says so; the book has then grown, so the next default checkpoint is new and a witnessed tick carries it only as a later checkpoint of the book (the cadence chain travels as `part`), and `--at` with a checkpoint `--list` shows as `all` gives the tick's own; with `part` the witness saw only the earlier checkpoint, so records after it are in the book by the consistency proof but were not seen by the witness. The interval runs from the earliest record the bundle selects to the checkpoint, and the capsules of every record in it travel with the bundle (their headers and payloads withheld by digest unless disclosed), later records included; each capsule in the interval still shows its type, time and digests (for a model call, its provider, model id and the digests of its prompt and completion, which do not hide a short or guessable text). A `--book` the shelf does not hold is an input error, not a new empty log. A witness receipt for the `--at` checkpoint must verify under the witness directory named, or no bundle is written; when a witnessed cadence tick carries the checkpoint, the cadence chain travels in the `x-cadence-witness/v0` extension. The witness itself is the host's transport: the host submits the statement and gives the receipt back. A bundle shows that the record is in the book at that position, not that its content is true. A deal's book, the cadence and the journal are refused, as for `deal node append`.

- `deal node referee receive` checks a referee's Result against both parties' halves and seals it into a node book as received, or seals why it was refused (evidencebook#624): `--book ID --result FILE --parties FILE --received-type T --refused-type T --half-type T`. `--result` is `{"capsule": <the referee's sealed Result Capsule>, "document": <base64 of the Result document exactly as written>}` and `--parties` is `{"buyer": {"half", "action", "terms"}, "seller": {...}}`. The check is capsule-engine's Go referee package (`go/referee`, `BindOpenings` then `VerifyResult`, required at v0.6.0), plus this node's own: its key must have signed one of the halves (`not_a_party`) and the document must be the referee's JCS bytes (`result_mismatch`). A Result that fails is refused and the refusal sealed as a record holding the Result's digest and the code, never silence, and the verb exits with the code; so every Result delivered to the node leaves a permanent record in its book, a refused one too, even when it is junk or about a deal the node is not in. Receiving is safe to repeat: a Result already received and a half already held seal nothing again, so a retry after an interruption resumes. What it shows is what the Result and the halves say, not that the referee's judgment is right, and received means checked and held, not that the node accepts the ruling. The record of a received Result holds its digest, Capsule id and the referee's key, not the document. Both halves, the other party's included, are kept in full in the book and go with it to anyone it is shared or exported to. The record type names are the node's own. `capsulectl/go.mod` requires `github.com/action-state-group/capsule-engine/go` v0.6.0 (was v0.3.0).

- Removed `deal node referee receive` (evidencebook#624). It took a referee's Result against two parties' x-deal-v0 halves, and its only caller was the Goose reference node, which no longer has halves. The Result's path on x-deal-v1 is `deal dispute`, the offer's adjudicator's `deal ruling` and `deal receive`; carrying it between a node and its adjudicator is tracked in evidencebook#830.

- `deal node request`, `deal node respond` and `deal node reply-check` give a node's own book on the deal profile's shelf the evidence-request exchange (evidencebook#624). `deal node request --book ID` is the core's `request` verb on a node book: `--request FILE --responder KEY --output FILE` asks and records the request, `--response FILE --for RECORD --responder-key KEY --responder-checkpoint-key KEY` records the answer after verifying it under the pinned keys (an answer under other keys is refused and never recorded, so it cannot foreclose the request), and `--absent-until TIME --for RECORD` records that nothing came back once the waiting window has closed. `deal node respond --book ID --request FILE --output FILE [--requester ID] [--policy FILE]` answers a request addressed to the node with a signed reply and records it; with no `--requester` the `history_card/1` and `history_card/2` derivations are answered (checkpoint commitments only) and the checkpoints subject, `checkpoint_range` and everything else get a signed refusal; an authenticated requester is also answered the checkpoints subject, and `standard_history` is not served from a node book at all (`derivation_unsupported`: a node book counts no deals); with the default policy a record is served only to the counterparty it names, and the records `deal node append` and `seal-model-call` seal name none, so by default they go to no one; a policy with `history_segments` `peers` serves any record of the node book by its subject to any requester the operator authenticated, with the record's Capsule and statement members and not its payload originals (a sealed prompt's text does not leave, its digest does, and a short text's digest does not hide it; a held half names the other party's key id). `--requester` is for an identity the transport authenticated, never a name the requester declared. `deal node reply-check --reply FILE --request-digest D --signer KEY` says whether a reply parses and carries the responder's signature for that request, records nothing, and is the check a reply gets that arrives after its request's absence was recorded; it does not check the answer under the pinned checkpoint key or the witness receipts, as recording an answer does, so a late reply that passes it is not a recorded, verified answer. The request, the recorded answer and the recorded absence are signed records of the node book, and so is an answer or refusal to a well-formed request from an authenticated requester; a malformed request or one with no `--requester` is answered without a record. The transport (a relay, a pipe) is the host's. A deal's book, the cadence and the journal are refused, as for `deal node append`.

- `deal node seal-model-call` seals a model call the agent reports as an effect-boundary record of a node book (evidencebook#624): `--book ID --type T --provider P --model-id M --developer D --prompt FILE --completion FILE [--out FILE]`. The record is a Capsule signed in the process under the profile's key. Everything it says about the call is the node's own statement: the effect is an inference completion that the runtime claims (`effect_attestation` `runtime_claimed`, nothing stronger), its status `confirmed` means only that the runtime reports the call finished, and its `model_attestation` names the provider and model id as the node states them; nothing checks that they answered. The prompt is kept in full as the record's payload and goes with the record to anyone it is shared or exported to, so do not seal a prompt you would not show them. The completion is not stored, only its digest, which hides a long completion and not a short or guessable one. `--out` writes the Capsule, with its `signature` and `key_id`, to a new file (never over an existing one); it carries digests, not the prompt or the completion. `--prompt` and `--completion` are one JSON value each, not null, with nothing after it; `--developer` is required.

- capsulectl-deal writes a bundle's witnessed chain as `parent-log-witness/v1` (Evidence Bundle -03), which AAC's verifier checks, in place of `x-cadence-witness/v0` (evidencebook#737, Sherlock2's ruling on capsule-cli#428): the five bundle builders (a deal report, a History's deal members, a sale's bundle, a terms result's share and `deal node bundle`) and the deal page's witnessed flag. The block is the chain with its `cadence` member named `parent`; members -03 does not define (`state`, `extent`, `checkpoint_at`, `steps_witnessed`, `steps`, `rest`) stay in it, which a verifier ignores. A bundle whose checkpoint is not witnessed yet carries no witness extension at all, under no name (it used to carry a state note under `x-cadence-witness/v0`, which core rc50 lists as "not checked here": a chain to check elsewhere where there is none). The words for that state ("witness pending", "the witness signature did not verify") come from the producer's own state when `deal report` makes the report; `deal report --from-bundle` on such a file says `not_carried`, since a file does not say. `capsulectl-deal` still reads `x-cadence-witness/v0` and the names before it, and a v1 block's `extent` and `state` are taken from the block's shape (`earlier` present or not), as AAC's verifier does. **Readers:** a capsulectl whose core is v0.1.0-rc47 or earlier checks only `x-cadence-witness/v0`, so it reads a new bundle's witness as not checked; use capsule-cli v0.1.0-rc50 or later, which verifies `parent-log-witness/v1`. **The deal page's witness line** reads a witnessed `parent-log-witness/v1` bundle through the page's embedded runtime, capsule-viewer/go v0.23.1 (capsule-viewer#362), now pinned (and in `report`, whose page goldens moved with it): with no witness list of the reader's own the line reads "not checked", as it did for v0. A page built with v0.23.0 or earlier reads a witnessed v1 bundle as "missing". A deal with no witness has no chain to check, so its line reads "missing" instead of "not checked".

### Added: an x-deal-v1 deal can be countersigned (evidencebook#865)

- `deal report --deal ID --bundle FILE --withheld` writes the withheld form of your deal book's bundle at the report's checkpoint: your deal book's records as signed Capsules, including your profile's operator name, the book's log id and each record's place in it, its time, signing key and digests (COUNTERSIGN.md lists every member; a Capsule with any other member is refused), the checkpoint and its proofs, and the profile's signing key as its producer key; no record's contents, no report and none of the other side's records. It is the bundle `capsulectl countersign request --bundle FILE` submits. The exact bytes pass the share gate before they are written; nothing is sealed or recorded.
- `deal countersign` now serves x-deal-v1 deals: the bundle must verify, be of the deal's own book, carry a checkpoint that book holds (the same signed statement) and have a record of the book as its root. A countersignature that verifies is kept with that bundle, as for an x-deal-v0 deal.
- `deal report` on an x-deal-v1 deal prints `countersign` beside the report (not in the sealed report): `rung` `countersigned` only when a stored countersignature verifies and the profile's countersigner directory lists its signer, `countersigned_in_part` once the deal book has records after the signed one, and `covers`, which says it covers neither the report nor the other side's records; otherwise the finding (`not_countersigned`, `self_countersigned`, `unresolved_signer`, `unverified`, `unchecked`).
- Countersign is served for x-deal-v1 deals only: an x-deal-v0 report bundle carries its records' contents, which `countersign request` refuses, and `deal report --withheld` is refused for it. ASSURANCE.md and COUNTERSIGN.md give the x-deal-v1 sequence.

### Added: a deal store whose journal is split by time window (#797)

- `deal init --window day|week|month|quarter|year|none` (default `none`) creates a store whose journal is one book per calendar window in UTC. Each deal's book stays one book for the whole deal, placed in the window its agreement is sealed in, so a deal that runs past its window stays in it. The window, or its absence, is fixed when the store is created; `deal init` prints which on stderr and returns it as `window` (the period, or `none`) with that sentence as `notice`. A windowed store holds x-deal-v1 deals only: an x-deal-v0 verb is refused there.
- On a windowed store, every journal record lands in the window of its time, and the verbs that read the journal (checks, admissions, records sent whole, `deal report`) read it across windows in order.
- `deal window list` shows the windows and the deals placed in each; `deal window roll` closes ended windows; `deal window chain --from --to --out` writes the chain a requester's report over windows is checked against, carrying each placed deal's proof that its book extends what the window's close recorded; `deal window respond --pack FILE...` answers a Terms Pack request from the window or placed deal book its pin names and records the answer in the current window. A report over those windows is built and checked with capsulectl-book's `book report derive --chain` and `book report coverage`, which name a window or a placed deal with no answer.

### Fixed: a slow witness reads as slow, never as a consent ask

- `capsulectl/go.mod` requires capsule-cli at dcecbf5 (capsule-cli#644): a witness delivery that reached the witness and then timed out is pending with reason `witness_timeout` and the words "the witness was reached but its answer did not come in time", where it read "network consent needed, or no network".
- `deal report`'s assurance passes `witness_timeout` on in `witness_reason`, beside `network_consent_needed` and `refused`, so an agent can tell a slow witness from a missing grant.
- The deal skill says what `witness_timeout` means: the witness or the network to it is slow, it is tried again on a later run, and the user is never asked for a network grant for it. The 403 paragraph says it is tried again on a later run with a growing wait, not at every tick.
- With the same capsule-cli, `capsulectl doctor --check-witness` reports `timed_out: true` when its own probe reaches the witness and gets no answer in time, with no consent text.

### Fixed: an x-deal-v1 payment counts as an earlier payment to its merchant in the rules history (evidencebook#890)

- The history `deal check` gives a pinned rules checker now carries, for each earlier x-deal-v1 act, the payee keyed per profile (`counterparty_profile`) and the counterparty its check sealed, as it already did for x-deal-v0 acts. Before, a rule keyed on an earlier payment to this merchant (Everyday Rules r02 and r06) read every x-deal-v1 merchant as new.
- Each earlier x-deal-v1 act is now read under its own deal's key, so its per-deal fingerprints are the ones that deal sealed; before, they were rebuilt under the key of the deal being checked.
- An earlier x-deal-v1 act whose check cannot be read, or whose link to it is broken, marks the history incomplete, so a limit that needs every earlier payment, such as the 7-day limit (r05), reports that it could not be checked. With Everyday Rules 0.3.10, r02 and r06 also say they could not be checked when they find no earlier payment to that merchant, instead of telling the person there was none (capsule-engine#327).
- Tested end to end with capsule-engine's `guard check --limits` (go/v0.8.0, now 4a490697) pinned as a buyer's rules checker on x-deal-v1 deals: within a person's per-order and weekly limits, over each, a limits file that is gone, and a payment checked on the buyer's offer before it is sent (declined and cancelled unsent, or approved, agreed and paid). capsulectl/go.mod names capsule-engine's command as a `tool` for these tests, which moves the indirect capsule-engine/go from v0.6.0 to v0.8.0; its guard, pack and canon packages differ only by the added person-limits support, which capsulectl does not call. It now requires capsule-engine at 4a490697 (capsule-engine#329), whose `guard check` defaults a buyer to Everyday Rules 0.3.10 (was 0.3.8).

### Changed: x-deal-v0 copies and node bundles carry no witness chain (u225, u230, evidencebook#958)

The witnessed cadence chain (`parent-log-witness/v1`) shows the holder's shelf position and cadence, which no copy or published bundle discloses (Sherlock2's rulings u219, u225 and u230, ops evidence-network/inbox.md, 2026-11-25).

- **Copies no longer carry the chain.** A new deal report (own, and the `--share counterparty` and `--share adjudicator` copies), a History's deal members, a sale's bundle and a terms result's share no longer carry the chain. Nor does `deal node bundle` (u230): a node may still attach a receipt over its own `--at` checkpoint with `--witness-receipt`, and `deal node checkpoint --list` still says which checkpoints a tick carries, as the node's own reading. This retires the earlier "five bundle builders" entry: no builder writes the chain.
- **Copies no longer carry `witness_coverage`.** Its "steps 1 to k witnessed" came from that chain.
- **What still travels.** A witness receipt held on the deal's own checkpoint (`checkpoint.witnesses`) still travels and still verifies.
- **A copy's rung, grade and `as_of`** come only from what the copy carries. A deal witnessed only through the cadence therefore reads `self_attested`, with grade witness `not_carried`.
- **The holder's own witnessing.** `deal report`, and `deal report --from-bundle`, which now reads this profile's own state for the file's checkpoint, say what the holder's profile reports in attributed voice, in `assurance.text` and `assurance.holder_witnessed`.
  - The line reads: "Your own profile reports this checkpoint witnessed by ..., as capsulectl reported from your own book".
  - For a deal witnessed through an earlier checkpoint, it names the steps covered (no longer the acts in them, which came from `witness_coverage`) and never says this checkpoint is witnessed.
  - `holder_witnessed` gives the witness, the extent, the witnessed checkpoint's time (`at`) and, in part, `steps_witnessed` of `steps`.
  - That line is in the holder's own output only, never in a copy, its page or its email.
- **The deal page's witness line** reads "missing" for a new copy, as for any copy without a chain.
- **Old copies.** Copies written before this change still verify, their chain checked as before. A report sealed before it that states `witness_coverage` is not shown in a new copy:
  - a History is refused with a hint: seal a current report with `deal report`, then use that one;
  - an evidence request pinned to it is answered with a signed refusal (`coverage_unsatisfiable`).

### Fixed: an x-deal-v0 act with a broken link to its check marks the rules history incomplete (evidencebook#955)

- In the history `deal check` gives a pinned rules checker, an earlier x-deal-v0 act that rests on an approval or a check that cannot be found, or whose check cannot be read, now marks the history incomplete, as an x-deal-v1 act already does. Before, the act was kept without who it was with and the history was called complete.
- What the person sees: a limit that needs every earlier payment, such as Everyday Rules' 7-day limit (r05), now says it could not be checked because the history is incomplete, and the check asks the person instead of allowing the payment. Before, that limit was checked as if the history were complete.
- On that incomplete history, Everyday Rules r02 and r06 now also say they could not be checked, beside r05, instead of telling the person there was no earlier payment to that merchant: the history may be missing that payment. The check still asks the person, as it did when they read the merchant as new (capsule-engine#327, Everyday Rules 0.3.10).
- An act recorded with no check at all is unchanged and leaves the history complete: it cites no check, so there is no link to break.

### Changed: the receipt page is drawn by capsule-viewer at 8fe749b (capsule-viewer#437, #391)

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer/go at 8fe749b, the receipt-page fold from Steven's 2026-11-25 run (capsule-viewer#424, #429-#432): term names in words ("Payment as agreed", "Delivery", "Item as described"), the terms' tally beside where the deal stands, local times in the records table, one plain line on why no payment record is shown, and the time and named-witness columns only on the holder's own keep page.
- The same range brings capsule-viewer#391: service and rental deals are worded ("Service", "Rental"), and a receipt follows the host page's colour scheme, so it is dark on a dark page.
- Each receipt manifest now pins three stylesheets: the kit's, its dark overrides and the new receipt.css (which keeps words in table cells whole), and each script hash changes.

### Changed: the receipt page names its wording pack capsuleviewer.receipt.wording.en/v1 (capsule-viewer#438, #956)

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer/go at 2aa43cf. Its receipt wording pack is `capsuleviewer.receipt.wording.en/v1`, with the same words as the old example pack (Sherlock2 on #956: a person's receipt page does not run on a pack named "example"). Each receipt script hash changes with it; pages written before keep their own scripts and pack.

- `capsulectl deal` docs: `skills/deal/ASSURANCE.md` writes down the difference between the witness (on by default, `deal init --no-witness` to opt out) and a countersignature (manual, per deal, per bundle), with the command sequence to add one. No behavior changes: `deal init` already points a new profile at Action State's hosted witness (evidencebook#862).

## v0.1.0-rc45

This release pairs with capsulectl `v0.1.0-rc45` (go.mod requires it), and is the first release with the x-deal-v1 write path: `deal offer` (and `offer cancel`), `receive`, `agree` and `dispute` on the new store layout (`deal set-version x-deal-v1 --migrate`). `deal agree` cites the holder's admission decision, the x-deal-v1 `admit` that capsule-engine `admission evaluate --admit` (go/v0.5.0) emits for the host to seal, and refuses one that is ASK or NO.

Every pull request merged since `v0.1.0-rc42`:

- [#215](https://github.com/action-state-group/evidencebook-deal/pull/215): docs(design): the plugin's x-deal-v1 write path (#212)
- [#219](https://github.com/action-state-group/evidencebook-deal/pull/219): fix(deal): no shared copy of a deal that holds the outcome note (u597, #218)
- [#220](https://github.com/action-state-group/evidencebook-deal/pull/220): build(deps): cll v0.4.0 and capsule-viewer v0.19.1 (verify --bundle, no code change)
- [#217](https://github.com/action-state-group/evidencebook-deal/pull/217): feat(deal): x-deal-v1 version setting, store move, offer and cancellation (#212, slice 1)
- [#221](https://github.com/action-state-group/evidencebook-deal/pull/221): feat(deal): x-deal-v1 slice 2: receive, agree, the deal-book writer and the first-act gate (#212)
- [#223](https://github.com/action-state-group/evidencebook-deal/pull/223): chore(deps): evidencebook v0.17.0, the nonce on admit, check, verdict and approval (#212)
- [#224](https://github.com/action-state-group/evidencebook-deal/pull/224): feat(deal): deal dispute, a dispute or its withdrawal sealed in the x-deal-v1 deal book (#95)
- [#225](https://github.com/action-state-group/evidencebook-deal/pull/225): fix(deal): the deal-book writer appends with AppendAt and rebuilds on ErrBusy (evidencebook v0.18.0)
- [#228](https://github.com/action-state-group/evidencebook-deal/pull/228): test(deal): a v1-only bundle never selects the deal view (#214)
- [#226](https://github.com/action-state-group/evidencebook-deal/pull/226): feat(deal): the standard_history/3 card counted from x-deal-v1 deal books (#213)
- [#232](https://github.com/action-state-group/evidencebook-deal/pull/232): feat(deal): the seller may show an adjudicator the claims their agent made without authority
- [#234](https://github.com/action-state-group/evidencebook-deal/pull/234): test(deal): fix the post-without-ask mutation so CI is green with jsonschema (#229)
- [#236](https://github.com/action-state-group/evidencebook-deal/pull/236): fix(deal): keep a received offer's pair so the acceptor's payment check runs
- [#235](https://github.com/action-state-group/evidencebook-deal/pull/235): feat(deal): the standard_history/4 card counts a deal with no witnessed time (#230)
- [#238](https://github.com/action-state-group/evidencebook-deal/pull/238): fix(deal): the offer verb seals the terms leg so an offer with a payment row is received
- [#241](https://github.com/action-state-group/evidencebook-deal/pull/241): feat(deal): deal agree refuses an admit that is ASK or NO

- `go.mod` requires capsule-cli `v0.1.0-rc45` (was rc42: the witness fix, capsule-cli#590, and the releases between) and, with it, capsule-viewer go v0.19.2.
- `deal agree --admit DIGEST` refuses an admission decision whose outcome is ASK or NO: an agreement cites a GO, or a LIMIT whose tier holds the deal (x-deal-v1 PROFILE.md, `admit`; evidencebook#590). Whether a LIMIT's tier holds the deal is the evaluator's (capsule-engine `admission evaluate --admit` prints `admits_deal`); the plugin holds no admission pack, so it does not decide it, and a LIMIT cited above its tier stays detectable offline by a reader holding the pack whose `definition_digest` is the admit's `pack_digest`: its tiers bound the offer's value. The body capsule-engine emits passes the profile check when sealed (a test seals capsule-engine's `admit.json` vector). evidencebook-deal#240.
- go.mod requires evidencebook v0.21.0 (was v0.19.0): x-deal-v1 `admit` rows take admission pack row ids (evidencebook#590), so an `admit` capsule-engine emits passes the profile check, and from v0.20.0 a v1 status value carries `start.first_act` (evidencebook#583), which changes a status value's commitment.
- `deal offer` now seals the offer's terms leg as its Capsule's settlement member when the terms have exactly one payment row (x-deal-v1 §6.3, §7), so the offer is no longer refused when the other side receives it and the acceptor's payment check runs. An offer with no payment row or several carries none, as before. Offers already sent without it stay refused; send them again. evidencebook-deal#237.
- `deal standard-history card --derivation standard_history/4`: the History card of x-deal-v1 deals that leaves no deal out for want of a witness time (AAC `spec/standard-history-v0.md` §12; agent-action-capsule go v0.23.0). A started deal whose agreement names no witness, or whose named witnesses timed none of its records, is counted and carries `no_witness_time: true`; `local_times` counts it, and the card has no `left_out` or `not_counted` tally. `standard_history/3` is unchanged. Steven's ruling u616; evidencebook-deal#230.
- The acceptor's payment check now runs for a deal whose offer it received: `deal receive` keeps the offer's sent pair as received (a `pair` column on `deal_v1_offers`, byte for byte) and the status provider reads it, so the acceptor's row and its `standard_history/3` card no longer read `agreed`/`met` or `held` where the settlement legs disagree. An offer received before this change keeps no pair: those deals' rows do not get the check until the offer is received again. evidencebook-deal#227.
- `--include-unauthorized-claims` on `deal report --share adjudicator` and `disclose --deal ID --share adjudicator`: the seller may choose, per adjudicator copy, to show the claims their agent made without their authority (all of them, never some). Off by default; an adjudicator's copy of a sale then says "Not shared: ...", and when shown, "Shared by the seller's choice: ..."; a counterparty's copy never carries them and the flag is refused there. Steven's ruling u615; evidencebook-deal#207.
- `deal standard-history card --derivation standard_history/3 --out FILE` (x-deal-v1 deals, new store layout): writes the History card counted from each v1 deal book's latest fact on the shelf log as of the newest stamp (AAC `spec/standard-history-v0.md` §11). Each row's result is computed as of the anchor's time from the status value's inputs with evidencebook's row rule; a stamp-only record time is resolved from a witness the agreement names (`Shelf.StampTimeByKey`, evidencebook v0.19.0), else left untimed. The default derivation stays `standard_history/1`, which counts x-deal-v0 deals and is unchanged; `deal respond` and `deal standard-history staple` still serve `/1` only. A stamp now commits a v1 book's status value (the status provider is installed on the cadence). A deal's value band reaches the card as an amount in that band, since a status value carries no amount (evidencebook design #482 Q15). evidencebook-deal#213.
- `deal dispute --deal ID --row ROW [--forum F] [--withdraw] [--out FILE]` (x-deal-v1 deals): seals a `dispute` record of kind dispute, or of kind withdrawal with `--withdraw`, in the holder's deal book and writes the file to send; the other side counts it with `deal receive`. The record is the one place a dispute lives, and a row's status reads it. The dispute names the agreement's adjudicator and one of its forums (`--forum` may be left out when there is one), cites the holder's own statements on the row, and is checked against the holder's agreement before it is stored; an agreement with no adjudicator allows none. Run again with the same `--out`, it rewrites the same file and seals nothing new; an `--out` file that already exists is refused for a new dispute or withdrawal, before anything is sealed. A challenge of a ruling is not here (evidencebook-deal#95).

## v0.1.0-rc42

This release pairs with capsulectl `v0.1.0-rc42` (go.mod requires it).

This release carries what merged since `v0.1.0-rc41`: `deal outcome-note seal`, the temporary note that a finished deal did not hold (#210, evidencebook#462; evidencebook v0.14.2), `deal sale unpost` (#209), the answers to a pause as the user's own journey row (#208), the failed-limit card (#205), the seller's view of an unauthorized claim (#206), the seller's offer card wording (#199) and the saved-receipt line (#197).

- TEMPORARY: `deal outcome-note seal --deal ID --reason dispute_lost|settled_against|not_delivered|other` seals the holder's own `outcome_note` record after a deal's final close (evidencebook#462; the target design's computed row status, evidencebook#461, supersedes it). The History card counts a deal holding one `did_not_hold`; it is in no shared copy and a deal takes one. A verifier built before the evidencebook release that carries `outcome_note` refuses such a deal. Per Ethan and Steven's phone agreement (relayed by Ethan, evidencebook#462) for the 2026-11-19 Goose ARD demo.

- `deal sale unpost --sale ID --platform SITE` records that a sale's live listing was taken down: a sealed `sale_unpost` (evidencebook v0.14.1) on the sale's log. It needs no check or approval and is refused for a site with no live listing; the site no longer counts toward a thread's `listing_classes` (absent when no listing is live), and a later post on it makes it live again. `deal sale close` now requires each live listing's takedown first and names the sites still up, per Steven's ruling "requires, not seals": it never seals a takedown itself, since that would record one that may not have happened. A sold thread's close leaves the sale open while a listing is up and prints `sale_open_listings`. `deal open --sale` refuses a new buyer on a sale whose item is already sold, closed or not. A verifier or capsulectl built before evidencebook v0.14.1 refuses a sale that holds a `sale_unpost`. Sales sealed before this verify unchanged. Door: Steven, non-stop decision of 2026-10-24 (company-internal-all#13); ruling (Buzz 2497bf0b; evidencebook-deal#201, capsule-cli#384).

- The seller's own receipt lists each claim their agent made to the buyer with no authority (sealed unchecked, `deal_representations.go`) in its anomalies, agent side: "Your agent said this without your authority: “<words>” (<why>)", with the reason for each of the eight rules in plain words ("you hadn't approved it", "your approval had expired", …). Shared copies carry no such item (evidencebook-deal#200, capsule-cli#333).

- Deal card: when a limit in the user's own rules fails (a failed finding that carries both its `limit` and `value`), the proceed button reads "Pay {amount} anyway", the facts line reads "over your {limit} per-purchase limit" (the kind from the finding's `check`, left out when it is not a plain identifier) instead of "within your {cap} limit", and a new reason `card.why.limit_failed` says it, beside "first time dealing with this seller" on a first payment. No rule id is read. A limit and value count only when the rules asked or denied and the value is over the limit, both as bare integers in minor units (a limit sent as a string, or a floor, gets the old card); the profile gives these members no unit yet, so the checker's side of this is an interim contract. Per Sherlock2's confirmation on evidencebook-deal#203 (moved from capsule-cli#425).

- Wording: the user's applied answer to a pause that is neither an OK nor a hold (for example "verify the contact first") is now the user's own journey row, "Chose to call them first" (`journey.user.chose.verify_contact`; any other choice reads "Chose a different option", `journey.user.chose.other`), instead of showing only the agent's next move. The facts carry the choice token only (evidencebook-deal#202, from capsule-cli#415).

- Wording: a seller's offer card has its own words: the button reads "Send offer" ("Send offer anyway" when a limit is broken) and the question reads "Send {buyer} an offer of {amount}?", instead of the raw id "proceed" and "Go ahead with {buyer}?". A test fails if any card button shows its option id. Per Sherlock2's ruling (evidencebook-deal#198, capsule-cli#368).

- Wording: the saved-receipt line says the receipt holds "what you asked, what was checked and what I did" (`say.receipt.saved`), and the first receipt's keep lines (`say.receipt.first.keep`, `.sold`) end "to check I did only what you asked", so no line claims a yes that was never sealed. Per Sherlock2's ruling and review (evidencebook-deal#196, capsule-cli#365).

## v0.1.0-rc41

This release pairs with capsulectl `v0.1.0-rc41` (go.mod requires it).

This release carries what merged since `v0.1.0-rc40`: the party's own rules verdict (#194), the wording fixes (#186, #188), the `agent-action-capsule/go` v0.21.0 pin (#192) and a longer timeout for the nightly race run (#189).

- `deal rules-verdict seal --word met|not_met|unknown|out_of_scope` seals the party's own overall rules word as a `rules_verdict` record (evidencebook v0.14.0) after the final close, bound to the deal and to the digest of the pack its checks ran under; the pack is named by its digest alone, never by a name. `deal rules-verdict policy [--share|--no-share]` is the holder's choice of whether a counterparty copy (`deal report --share counterparty`, `deal respond`) carries it: a node shares by default, a person does not, a profile with no declared holder kind shares none; otherwise the copy lists the record as withheld, says nothing of what it is, and the other side's receipt has no verdict to read and says "not shared" (the receipt wording is the reader's, actionstate-muse#192). Never on a History card or an adjudicator's copy. This repository derives, re-runs, counts and combines nothing. Carries Steven's ruling (Buzz 8b6aaecf; evidencebook-deal#191); the module now requires evidencebook v0.14.0, which also makes `uniform` the default refusal policy (evidencebook#433).
- Wording: the witness's record is called the witness signature, not a receipt, in the email text (`Witnessed: … signed this receipt's checkpoint`, "no witness signature has come back yet") and in the countersignature line ("Its log signature verifies"); "receipt" no longer stands for the exchange in `page.card.with_unchecked`, `page.close.not_closed`, `page.own.note`, `page.same.deal`, `page.detail.points_exchange`, the obligation state label (`OPEN`), the closed-exchange and calendar lines, the skill-instructions lines, the changed-rules lines and the opened-exchange lines. Per Sherlock2's re-review of #179 (#187).
- Wording: `say.close.open` no longer says a receipt "stays open until it arrives" (now "this stays open until it arrives"); `page.asked.exact` says the words were "sealed when the exchange began"; `page.detail.same_deal` says "the same exchange". Per Sherlock2's review of actionstate-muse#371 (#185).

## v0.1.0-rc40

This release pairs with capsulectl `v0.1.0-rc40` (go.mod requires it).

This release carries what merged since `v0.1.0-rc39`: the held-request tier in `deal respond` (#176), `deal held` and `deal decline` (#180), the stable `reason_code` on a deferred merchant email (#177), the wording change from "deal" to "receipt" (#179) and a design note for dropping a held object or email (#174).

- A deferred merchant email's result and each waiting entry carry a stable `reason_code` beside the raw `reason`, and the key check carries `key_check_code`. Branch on the code, never on the reason; codes are never renamed and new ones may be added, so treat an unknown code like `dns_unreachable`. The codes are listed in `skills/deal/README.md` (evidencebook-deal#21, #177).

- `deal respond` holds a request for a derivation it does not serve automatically (anything but `standard_history/1`) instead of refusing it with `derivation_unsupported`: the F4.10 "ask me" tier, per Steven's 2026-11-01 ruling. It records one pending `evidence_request_held` item (the same request twice is one item), writes no reply file, and prints outcome `held`. No wire change: the requester sees pending until its window closes, then a recorded absence. Contract scope stays deferred (evidencebook-deal#96).
- `deal held` lists the held requests you have not decided (request digest, requester, derivation, subject kind, receipt, time held, state `held, can be declined`). `deal decline --request-digest D --output FILE` answers one with a signed `policy_declined` refusal (a deal-shaped request gets the deal reply, an evidence-interop one its own reply) and records it as the request's answer, so it leaves the list; `deal respond` on a declined request says so instead of holding it again. No wire change. Serving a held request is not built: a held item keeps no request octets and no held derivation is servable (evidencebook-deal#178).
- The wording pack no longer shows the word "deal" to a person: report and receipt pages say "receipt" (side unknown), per Sherlock2's ruling (actionstate-muse#363). Message ids and the `deal` verb, `--profile deal` and `x-deal-v0` are unchanged (evidencebook-deal#175, #179).

## v0.1.0-rc39

This release pairs with capsulectl `v0.1.0-rc39` (go.mod requires it).

This release carries what merged since `v0.1.0-rc38`: `reconcile` in `deal list` and the ruling time in `deal report` (#172).

- `deal list --json` states, per deal, `reconcile`: `null` until the deal has a verified sealed terms Result, then `counterparty_half_held` (any verified Result cites two distinct halves, as the History card reads it), `halves_conflict` (from the newest verified Result that cites both halves: true on a conflicting pairing, false when every claim matched, null when none exists or a claim could not be compared, so a later one-half Result never erases a conflict) and `at` (when the newest verified Result was sealed). `null` is "not reconciled" or "unknown", not false, so a plugin counting held halves and conflicts per `counterparty_fingerprint` never reads a zero it was not given; a ranged listing reads only Results sealed before `--until`. `deal report`'s `ruling` member also states `at`, when the ruling step was sealed. Read-only fields over sealed records; nothing sealed or shared changes (evidencebook-deal#171).

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
