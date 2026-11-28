### Changed: `capsulectl` v0.1.2 pins capsule-cli v0.1.2 (evidencebook-skills#444)

- `capsulectl/go.mod` requires capsule-cli `v0.1.2` (was `v0.1.1`; v0.1.2 changes no command or library, capsule-cli#663 and #664) and the `evidencebook` library at 93cd4a2a, main when this release was prepared (was 0024847b), so the plugins carry the library side of #1066, and capsule-viewer/go at main 7bb5b559 (was 335d67ab; `report/go.mod` too), which contains go/v0.28.0 with its page runtime that no longer prints "self-witnessed" under a witness's time-stamp on a receipt page (capsule-viewer#489; closes #1082), and capsule-viewer#513, whose receipt says when the witness's directory entry does not list it as independent and marks the reader's own witness; its page runtime is rebuilt in-tree at 645ce92d. The presentation fixtures' pages are rewritten from the new viewer. The pull requests below reached `capsulectl/` or the library it builds since `capsulectl/v0.1.1`, by merge title:
- Added:
  - feat(deal): an x-deal-v1 buyer's intent names the seller by commitment (#1031) (#1060)
  - feat(capsulectl): a buyer's x-deal-v1 opening names the seller, and its agreement cites it (#1031) (#1063)
  - feat(capsulectl): capsulectl-deal writes a recorded absence as recorded_absence (#116 PR 2) (#1037)
  - feat(deal): an x-deal-v1 copy carries the offer it rests on (#1065) (#1066)
  - feat(capsulectl): the witnessed member names the witness row's independent_of (#1073) (#1078)
  - feat(capsulectl): deal tick names each delivered and pending checkpoint's tick (#1083) (#1091)
- Fixed:
  - fix(deal-skill): ask with approval_text when the user presses the button after a pass (#1030) (#1039)
- Tests and CI:
  - test(capsulectl): delete five internal/deal tests another test already covers (#1048)
  - test: delete seven duplicate tests and run the holder scenarios once (#1062)
