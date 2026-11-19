### Changed: an external-check finding's `limit` and `value` are money-cap amounts (#456)

- The x-deal-v0 external-check result schema and PROFILE now state that a finding's `limit` and `value` are sent only for a money cap the value went over, both in minor units of the deal's currency, with `value` greater than `limit`. A count, floor or window rule omits them. The deal card already read them this way; no member is added and no record changes validity.
