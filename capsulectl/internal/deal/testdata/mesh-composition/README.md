# Mesh compositions

Deal compositions (`composed/v1`) that the Mesh capsules plugin built, kept
unedited. `TestPresentationMeshReceiptFromThePlugin` renders each through the
host's presentation build.

- `artifact.json`: their half came back as a bundle answer (an `artifact`
  member), beside an absence.
- `refusal.json`: they refused the ask (a signed `refusal` member), beside an
  absence.
- `absence.json`: nothing came back (an `absence` member).

Built from Mesh-LLM/capsules-plugin at `46a688b` (main, 2026-11-01) by the
plugin's own tests in `crates/evidence-plugin/src/deal_receipt/composition/tests.rs`:

```bash
cd crates/evidence-plugin
DEAL_COMPOSITION_ARTIFACT_OUT=artifact.json \
DEAL_COMPOSITION_REFUSAL_OUT=refusal.json \
DEAL_COMPOSITION_BUNDLE_OUT=absence.json \
  cargo test --locked --bins deal_receipt::composition
```

The keys are the tests' own; nothing here is a real account's.
