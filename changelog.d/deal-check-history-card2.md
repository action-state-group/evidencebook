<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal check --history` reads a stapled `history_card/2` (#666)

- `--history` now also takes the counterparty's stapled `history_card/2`, exactly as received. It may be a `{history, request, reply}` file, an `evidence-commitment/1`, or a printed staple.
- It is verified with capsule-cli's own staple verifier (`cli.StapleVerdict`, capsule-cli#567) before anything runs or is sealed, under exactly one `--counterparty-key` and the new `--checkpoint-key`. Both keys come from your deployment policy, and the checkpoint key need not be the counterparty's.
- A staple bound to our request reaches the checker with `request_binding: "BOUND"`. A staple that checks out but answers no request of ours, such as a commitment, which staples the reply alone, reaches it with `"NOT_PRESENT"`.
- The check is refused, and nothing runs or is sealed, when:
  - the staple is not accepted under that key;
  - a check is left unrun, such as no `--checkpoint-key`, which the refusal names;
  - more than one counterparty key is given for it;
  - it staples a `history_card/1`;
  - it staples more than one card.
- The checker input binds the checkpoint keys too (`checkpoint_keys`, beside `counterparty_keys`), so the verdict's `input_sha256` says which key vouched for the card. `deal settle --sent` takes the same `--checkpoint-key` and refuses any other.
- A `NOT_PRESENT` History was not made in answer to this deal's request, and it can be replayed, so a ruleset should weigh it accordingly.
