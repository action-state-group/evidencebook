<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: an audit copy of an x-deal-v1 deal for an outside reader (#1058)

- `deal report --share audit --to WHO --bundle FILE` (or `--out FILE`) writes a copy for a reader who is not a party, such as an external audit. It keeps the rows, statements, disputes, rulings, outcome and the signed records they rest on, and `deal verify --bundle` passes it. It carries no personal detail, no witness time and no witness receipt, and the offer only when it names no way to reach the offerer.
- Like every shared copy it is put on record first. Its share gate reads the exact file, payment references included, and refuses it if any personal value the holder's store holds would be in it; the spending limit is kept from it as from the counterparty.
- `--to` names the reader on the holder's disclosure record only. The parties' keys stay in the signed records, so a reader can link a party across copies.
