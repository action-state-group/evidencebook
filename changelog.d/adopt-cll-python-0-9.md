### Changed: Python package on checkpointed-local-log 0.9.0 and capsule-emit 0.17.0 (#452)

- `python/` requires checkpointed-local-log `>=0.9.0,<0.10` and capsule-emit `>=0.17.0,<0.18` (were `>=0.8.0,<0.9` and `>=0.15.0,<0.16`). Its checkpoint writer now signs the CLL -02 CWT `sub`, the bare log id, instead of `<log id>#<size>`; the reader still accepts both. The interop twins (`responder.py`, `personal_history.py`) write the bare log id in their log keys, and the checkpoint-parity cases keep the signed `sub` when they change a claim.
- `capsulectl`'s Go ↔ Python round trip now resolves evidencebook v0.14.2 and installs Python CLL from `python/v0.9.0` (the `python/` subdirectory), with agent-action-capsule 0.16.0 and capsule-emit 0.17.0 locked with hashes.
