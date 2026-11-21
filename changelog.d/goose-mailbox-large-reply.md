<!-- changelog: python/CHANGELOG.md -->
- The Goose reference's mailbox receives a large reply (evidencebook#589): the relay connection accepts a message of the
  largest reply a book accepts (`MAX_REPLY_BYTES`) in base64, where websockets' 1 MiB default closed it with 1009 on a long
  history's `history_card/1` and the poll recorded an absence. A poll that could not read any relay returns
  `transport_error` (`loop_ask_mailbox_poll`, `loop_dispute_poll`) and records nothing, instead of reading as no reply.
  `loop_ask_mailbox` asks for `history_card/2` on `checkpoints` when the asked node's commitment offers it, else `history_card/1`.
