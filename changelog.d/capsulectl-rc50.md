### Released: capsulectl-book and capsulectl-deal `capsulectl/v0.1.0-rc50` (#802)

This release pairs with capsule-cli `v0.1.0-rc50` (`capsulectl/go.mod` requires it, with `report` v0.7.0 and capsule-viewer go v0.23.0; `report/go.mod` moves to capsule-cli rc50 too) and is cut from main at f31db8ca. capsule-cli rc50 removed the deal-format verify code from its core, so this is the first capsulectl built on it: capsulectl-deal carries its own copies (#809). x-deal-v1 is the default deal version (#749).

Every pull request merged since `capsulectl/v0.1.0-rc49` that changes capsulectl, `report` or the deal skill:

- [#749](https://github.com/action-state-group/evidencebook/pull/749): feat(deal): x-deal-v1 is the default deal version (#626)
- [#759](https://github.com/action-state-group/evidencebook/pull/759): feat(deal): the deal skill teaches x-deal-v1; the x-deal-v0 procedures move to a legacy file (#626)
- [#760](https://github.com/action-state-group/evidencebook/pull/760): fix(deal): deal audit-host reads x-deal-v1 deals (#758)
- [#766](https://github.com/action-state-group/evidencebook/pull/766): feat(deal): agree to your own x-deal-v1 offer with a party that keeps no records (#762)
- [#770](https://github.com/action-state-group/evidencebook/pull/770): feat(capsulectl): book report build --receipts carries deal receipts into a settlement report (#764, #767)
- [#772](https://github.com/action-state-group/evidencebook/pull/772): feat(deal): a card checkout's payment on x-deal-v1, under the merchant's order number (#771)
- [#776](https://github.com/action-state-group/evidencebook/pull/776): test(deal): the receipt page check takes the deal-format verdicts from the plugin (#735)
- [#784](https://github.com/action-state-group/evidencebook/pull/784): feat(report): Receipt.NotClosed marks a deal that had not closed when the page was built (#778)
- [#786](https://github.com/action-state-group/evidencebook/pull/786): feat(capsulectl): book report build --receipts takes not_closed (#778)
- [#792](https://github.com/action-state-group/evidencebook/pull/792): fix(deal): on x-deal-v1, an answer that was not applied authorizes nothing (#790)
- [#796](https://github.com/action-state-group/evidencebook/pull/796): feat(deal): deal ruling, the adjudicator's verb, and deal dispute --challenge (#773)
- [#801](https://github.com/action-state-group/evidencebook/pull/801): feat(deal): deal ruling entries, deal receive of a copy, and share gate fixes (#773, #787)
- [#806](https://github.com/action-state-group/evidencebook/pull/806): feat(report): View.Coverage states the range a report over time windows covers (#689)
- [#809](https://github.com/action-state-group/evidencebook/pull/809): feat(deal): capsulectl-deal reads its own deal formats on capsule-cli rc50 (#735)
- [#810](https://github.com/action-state-group/evidencebook/pull/810): feat(deal): deal checkpoint proof, a deal book's stamp proof with its witness receipts (#788)
- [#813](https://github.com/action-state-group/evidencebook/pull/813): feat(report): Receipts.Gate, the caller's own check of each carried receipt (#735)
