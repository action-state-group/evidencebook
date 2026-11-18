- **`history_card/2`** (Evidence Request -03 §8.1.2, evidencebook#507): the default first question for
  a node with many checkpoints. A card lists the first checkpoint, the checkpoints 1, 2, 4, 8 and so
  on positions back from the anchor and the latest `window` checkpoints in full (agent-action-capsule's `HistoryPositions`), each
  with its receipts and a consistency proof from the entry listed before it. Its size grows with
  the logarithm of the number of checkpoints, and `MaxAnswerItems` does not cap it; `history_card/1`
  lists every checkpoint and is refused past `MaxAnswerItems`. A book answers it to any requester over `checkpoints` or `full_history`, and
  `ResponderPolicy.HistoryWindow` (1 to 64, default 16) is the window it states. `CheckHistoryCard2`
  runs the spec's seven ordered checks, a requester verifies a /2 answer by them, and `Staple` and
  `CheckStaple` carry the card. A `checkpoints` subject with a range `[a, b]` (§8.1.3) is answered with
  the entries from a through b and verified by a requester. The manifest lists the derivation and
  draft -03. `history_card/1` is unchanged. Needs agent-action-capsule go v0.22.0.
