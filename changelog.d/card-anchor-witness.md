<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: a History card carries a cll witness's receipt for its anchor, and `deal verify` checks it (evidencebook#891)

- **What changed.** `deal standard-history card`, in both copies, now carries the receipt a `cll` witness gave for the checkpoint the card is as of. The default hosted witness `https://witness.agentactioncapsule.org` is such a witness. Until now its card carried no receipt, and `deal verify` reported the card's witness as `withheld`.
- **Where the receipt goes.** It rides in a new extension, `x-standard-history-anchor-witness/v0`: `{"witnesses": [one checkpoint.witnesses entry]}`.
  - The card carries exactly that one receipt, over that one checkpoint. There is no stamp proof and nothing about any other checkpoint (Sherlock2's confirmation, ops 92151a94).
  - It does not go in `as_of.receipts`. That member holds only receipts whose leaf is the anchor itself (interop profile v1 §6.2.2), and a `cll` receipt's leaf is not.
  - It is interim: a witness that answers SCRAPI gives the anchor receipt `as_of.receipts` already carries.
- **The writer checks it first.** The receipt is checked again under the profile's pinned witness before it is written. One that does not check is left out, and the card is still written.
- **What `deal verify --bundle CARD --witness-directory DIR` checks.** Before it reports `reader_witnesses: pass`, it requires that:
  - the card Capsule is the root, signed, and its disclosed payload matches its seal;
  - the payload's `anchor` is `as_of.anchor`, and `as_of.checkpoint` hashes to it;
  - the key that signed the card's own log also signed that checkpoint;
  - the one receipt verifies against that checkpoint.

  Each failure is a named finding (`card_unverified`, `card_anchor_mismatch`, `card_anchor_checkpoint_mismatch`, `card_anchor_checkpoint_signer_differs`, `card_anchor_witness_malformed`), and the verdict is then no longer VALID. With no directory row for the witness it reads `withheld`. The `witnesses` row stays about the card bundle's own log, which no witness sees.
- **Deal copies.** No deal copy carries the extension (u219). A bundle carrying it beside a deal section or a cadence chain fails with `card_anchor_witness_in_deal_copy`.
- **The staple.** The `history_card/2` staple is unchanged. Its receipt is a different form (ER -03 §8.1.2), which the hosted witness does not issue.
