### Added: `xdealv1.CheckAgreedAt`, agreed no later than witnessed (agent-action-capsule#696)

- A deal's `agreed_at` may be later than its first witnessed time, the record time of the holder's own agreement from a witness the agreement names, by at most `xdealv1.AgreedAtSkew` (300 seconds), as agent-action-capsule `spec/standard-history-v0.md` §11.2 "Agreed no later than witnessed" says. A later one returns `xdealv1.ErrAgreedAfterWitnessed`: the deal entry is not well formed. Vectors: `testdata/deal-v1/agreed-at/vectors.json`.
