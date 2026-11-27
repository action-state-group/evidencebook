<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: a buyer's x-deal-v1 deal names the seller in a record its agreement cites (evidencebook#1031)

- On a buyer's x-deal-v1 deal, the opening intent (`deal note --kind intent --opening`) carries `seller`: the key the agreement will name for the seller, and commitments of the name and domain the opening's `who` gives. The name and domain stay in your local store with their nonces; the record holds only the commitments. A seller's opening, or one whose `who` gives no name, names no seller.
- `deal agree` with no `--intent` cites the deal's opening intent when it is sealed, so the agreement binds the seller it names. An opening sealed after the agreement is not cited: an agreement is sealed once.
