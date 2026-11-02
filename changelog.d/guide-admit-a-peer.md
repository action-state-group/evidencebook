- docs/GUIDE.md gains Path 5, admit a peer: verify the staple, evaluate the admission pack with capsule-engine's
  `admission.Evaluate` (go/v0.3.0) and seal the decision's block with `Book.Append`. `Example (AdmitAPeer)` runs
  `admission-baseline@1`'s fixtures (`testdata/admission-baseline/`, listed in VECTORS.md) for a GO, an ASK with its
  recorded request and a LIMIT after a refusal. The root module requires capsule-engine/go v0.3.0, for that Example
  only (evidencebook#189).
