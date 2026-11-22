# capsule-engine admit outputs

Real output of `capsule-engine admission evaluate --admit FILE`, the input `deal admit` reads
(evidencebook#626). Each file is the command's stdout, unchanged.

- Producer: capsule-engine 9c018cc (`go run ./cmd/capsule-engine` in `go/`).
- Pack: `conformance-vectors/admission/v0/admission-baseline-1.0.0/pack-definition.json`.
- Input: the `input` of that directory's `cases.json`, case index 0 (`go.json`, GO, `admits_deal`
  true), 10 (`ask.json`, ASK, false) and 1 (`no.json`, NO, `admit` null).
- `--admit` file: `{"asks":[],"answers":[],"card_digest":"b…b"}` (64 `b`). The command draws each
  admit's nonce, so a re-run makes other bytes; replace the three files together.
