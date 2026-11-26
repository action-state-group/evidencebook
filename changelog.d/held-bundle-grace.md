### Fixed: a Close citing a peer Close seals in a window's grace again (#116)

- A window book in its grace now takes the `held_bundle` a Close citing a peer Close appends just before itself (`relied_by` `close`), and `VerifyWindowChain` accepts such a record in a boundary's tail when a late close/v1 Close in the tail cites it. Since #1009, `Book.Close` with a `PeerClose` over a period in a window in its grace failed with `ErrWindowClosed` before sealing. A `held_bundle` for anything but a Close is still refused in grace, and a late one no Close cites is a named finding.
- `Book.Acknowledge`'s comment no longer says the book holds the counterparty's Close: it names the Close by its record id and keeps no copy, the custody profile's "by digest otherwise".
