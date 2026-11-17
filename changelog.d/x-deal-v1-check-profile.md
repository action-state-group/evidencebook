- **x-deal-v1's Python reference checker** (evidencebook#461 step 5, PR 2e). `dealprofile/x-deal-v1/check_profile.py`
  checks every x-deal-v1 fixture on its own, independent of the Go package: the canonicalization, schema
  and record stages, the agreements against the offer, the dispute and rulings against the agreement, each
  book's links, and that every negative is refused at its stage. CI's `deal-profile` job requires "ALL OK".
