<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: the x-deal-v1 receipt page draws Steven's receipt design (capsule-viewer#442)

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer/go at 1b91f61 (`go/receipt` and `go/pageruntime`). The page `capsulectl deal report --html` writes now opens with the design: the header with the holder's role, the top line with its seal, the cards, the stamps, the steps one per record, "Is this record intact?", "Check it yourself" and "What this does not prove". Every sealed record, the log, the fingerprint, the profile and the producer version move under "For checkers"; the ladder of rungs, the verifier's checks and what the report rests on stay under "Technical details".
- The seal shows "Done" only when the deal is settled, every term is met and both sides say the same, and "Didn't go as agreed" only on a final ruling of not met; otherwise no seal (capsule-viewer#382).
