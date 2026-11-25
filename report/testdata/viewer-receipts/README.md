# Receipts from capsule-viewer

Copied unchanged from capsule-viewer `tests/testdata/receipt/` at main `158405be` (capsule-viewer 0.26.0). Its README says how each was made.

- `mesh-payer.bundle.json` is `mesh-payer-selected.bundle.json`: a Mesh node's payer copy, cut by the capsules-plugin `deal-receipt-bundle` route. It is a producer selection, so strict verify calls it INCOMPLETE (interval coverage withheld as `selection_not_checkable`), and the page verdict accepts it.
- `seller-keep.bundle.json` is `seller-keep.bundle.json`: a seller's keep copy, VALID.

`receipt_gate_test.go` uses them to hold the report's receipt gate and the real receipt modules (capsule-viewer `go/receipt`).
