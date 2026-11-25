# Received History fixtures

One-file Histories (`personal_history_basic/1`, PH §8.2) for `deal check --history`, written from
agent-action-capsule's `vectors/personal-history-basic-v0/vectors.json` as capsule-cli v0.1.0-rc53
vendors it (`cli/testdata/personal-history-basic-v0/vectors.json`, SHA-256
`f1f0e1b95c7dd32f2ddc584d4c55bc9a643a58f550a941651bfd5d3e4c59a4b9`), the way capsule-cli's
`historyFileOf` writes a case: `{history, definition, exchanges}`. The trusted holder key of every
case is `27cb07b534232cb4f4b7b269a8436fafb17c19be41fbab20ac948d1c81b0a034`.

| File | Case | Verdict |
|---|---|---|
| `accepted.json` | `history-basic` | ACCEPTED |
| `accepted-2.json` | `multi-stream-one-tick` | ACCEPTED |
| `not-accepted.json` | `mutant-deal-added-to-definition` | NOT_ACCEPTED |
