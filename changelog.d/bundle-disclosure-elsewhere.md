### Added: `BundleRequest.DisclosureRecordedElsewhere` (#626)

- A bundle built with `DisclosureRecordedElsewhere` (with `At`, a checkpoint the book already has) leaves the book unchanged: no disclosure record and no new checkpoint.
- It is for a host that records the disclosure outside the book, as an x-deal-v1 deal book must, since a disclosure record in it is not an x-deal-v1 record and every later report of the book would refuse it.
- Without `At` it is refused.
