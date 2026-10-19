# Deal reconcile contract v0: conformance vectors

These vectors exercise `spec/deal-reconcile-contract-v0.md` (cited as DR): the Evidence Contract
`ec:deal-reconcile@1` that a `type: reconcile` claim of an Evidence Result
(`spec/evidence-result-v0.md` §4.1) cites when two books reconcile the two halves of a deal. They
cover:

- halves read through a profile's half mapping (DR §4.2), here the example mapping;
- the field commitments and their openings (DR §4, §5), built as the deal commitment of
  `spec/personal-agent-history-outcome-v0.md` §7;
- whether a side's set is complete for the period (DR §6.1);
- the per-pairing states (DR §6);
- the six tallies, sufficiency, verdict and deal state, and the Evidence Result that carries them
  with no field opening and no cited Close (DR §7).

| Case | Expected |
|---|---|
| `pair-matched` | both requirements `MATCHED`; tallies `matched` 1; `SATISFIED`, `met`; deal `reconciled` |
| `amount-conflicting` | amount `CONFLICTING`, `SATISFIED`, `not_met`; currency `MATCHED` |
| `amount-opening-fails` | amount `UNRESOLVED` (not `MATCHED`, not `CONFLICTING`), `UNKNOWN`, `not_evaluable`; currency `MATCHED`; deal `not_reconciled` |
| `amount-opening-missing` | amount `INSUFFICIENT`, `GAP`; currency `MATCHED` |
| `currency-texts-empty` | currency `INSUFFICIENT` (two empty texts are not `MATCHED`), `GAP`, `not_evaluable`; amount `MATCHED`; deal `not_reconciled` |
| `currency-one-text-empty` | currency `INSUFFICIENT`; amount `MATCHED` |
| `a-only-peer-complete` | side B holds no half, its `source_authoritative` set `COMPLETE`: both `A_ONLY`; `SATISFIED`, `not_met` |
| `b-only-side-a-complete` | side A holds no half, its set `COMPLETE`: both `B_ONLY` |
| `a-only-peer-commitment-not-checked` | side B's `stream_interval` set `UNDECIDED` (commitment `NOT_CHECKED`): both `UNRESOLVED`; `UNKNOWN`, `not_evaluable` |
| `a-only-peer-not-declared` | side B holds no half, no coverage given: both `INSUFFICIENT`; `GAP`, `not_evaluable` |
| `a-only-peer-holder-selected` | side B's coverage `holder_selected`: both `INSUFFICIENT` |
| `a-only-peer-coverage-fails` | side B's coverage fails `coverage_declaration_population`: both `INSUFFICIENT` |
| `a-only-peer-property-not-reported` | side B's report leaves out `coverage_declaration_source`: both `INSUFFICIENT` |
| `a-only-peer-other-period` | side B's coverage is given for another period: both `INSUFFICIENT` |

Every case carries the six tallies per requirement and cites the sections its expected values
come from. `vectors.json` also carries the contract document (`contract.document`,
`contract.document_jcs`), its `contract_ref`, its digest, the SHA-256 of the Evidence Contract v0
schema it validates against, and the example half mapping (`half_mapping_example`). The half
layout and the profile name `example-half/0` are for these vectors only. Coverage
properties are given as a verifier reports them under `spec/interop-profile-v0.md` §6.7.3; the
coverage declaration vectors check how they are computed.

## Derivation

`python/scripts/generate_deal_reconcile_v0_vectors.py` writes every file except this README. It
uses no repository implementation. It states each per-pairing state by hand from DR §6 and derives
the tallies, sufficiency, verdict and Result from them by DR §7. Every nonce is a label digest for
these vectors only. The amounts and currency are placeholders; `XTS` is the code reserved for
testing.

`python/tests/test_deal_reconcile_v0_vectors.py` checks the corpus from the case inputs alone, with
an evaluator written from DR §4 to §7:

- It compares the evaluator's result with the case's whole `expected` block.
- It runs defective evaluators, each of which must disagree with at least one case.
- It validates each expected Result against `schemas/evidence-result-v0.json` where `jsonschema`
  is installed.
- It regenerates the directory twice and requires both runs to be byte-identical to the committed
  files.
