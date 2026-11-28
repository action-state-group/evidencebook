### Fixed: capsulectl-book's History card page draws `standard_history/3` and `/4` cards (#1075)

- capsulectl-book ships the History card module of capsule-viewer go/v0.28.0 (it shipped go/v0.18.3's). The old module read only `standard_history/1` and called every `/3` and `/4` card unreadable, though `/3` is `deal standard-history card`'s default. The module's files in `standard-history-VERSION.tar.gz` and their digests in the handshake change; unpack the new archive beside `capsulectl-book`.
