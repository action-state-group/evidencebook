### Added: `report.Page.IgnoredReceipts` lists the receipts no deal cites (#764)

- A `View.Receipts` entry whose exchange capsule no deal's claims cite is neither carried nor held (a node hands over every exchange it holds, and the report's window decides which became deals), and `Page.IgnoredReceipts` now lists its exchange capsule ids, in the order given, so a caller that meant to carry one can tell a mis-keyed id from a deal it handed nothing for. A capsule cited by the claims of two deals is still refused.
