### Changed: a report page files each claim's receipt under the subject of the records it cites (#116)

- `report.Build` with `Receipts` now finds each claim's deal from the records the claim cites. It reads them with `SubjectsOfRecord`, using only the headers that the bundle's evidence-book verifier checked. When none of the cited records is a member of a subject, the deal is the part of the claim id before `::`, as before.
- The page receives each claim's deal through capsule-viewer's `receiptDeals` option, so `report/` owns the rule and the page keeps no copy of it.
- `Receipts` are refused in three cases: a claim cites members of two subjects, a claim's deal is `__proto__`, or the evidence-book verifier refuses the bundle.
- A receipt for a record that the claims of two deals cite is still refused. Two subjects whose claims share a supporting record each keep their own receipt.
