### Added: `VerifyWitnessReceiptTime` (#819)

- `VerifyWitnessReceiptTime` is `VerifyWitnessReceipt` with the receipt's registration time (its `iat`), when the receipt verifies and carries one. A receipt with no time gives none. A reader uses it to take the time a witness registered another party's stamp, such as an adjudicator's stamp covering a ruling.
