# capsule-viewer's x-deal-v0 verify-report fixtures

Copied byte for byte from capsule-viewer `tests/testdata/deal-v0-report/` at
83b2c4857d8d09a60ad9426b2560a44b0a808064 (capsule-viewer#449, CV3b), the
field contract capsulectl's `deal verify --bundle` output is held to
(`TestV0VerifyCountersignMatchesTheViewerContract`). Refresh them from the
capsule-viewer commit `capsulectl/go.mod` pins when the contract changes.

- `countersigned-keep.bundle.json`: a keep copy carrying three countersign/v1
  entries signed by TEST-ONLY keys.
- `countersigned-keep.verify.json`: its report, with the `countersign` member
  the page reads (the `22…` key trusted as the producer's).
- `countersigners.json`: the countersigner directory, which lists the `11…` key.
