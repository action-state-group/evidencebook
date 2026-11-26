<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: x-deal-v0 copies and node bundles carry no witness chain (u225, u230, evidencebook#958)

The witnessed cadence chain (`parent-log-witness/v1`) shows the holder's shelf position and cadence, which no copy or published bundle discloses (Sherlock2's rulings u219, u225 and u230, ops evidence-network/inbox.md, 2026-11-25).

- **Copies no longer carry the chain.** A new deal report (own, and the `--share counterparty` and `--share adjudicator` copies), a History's deal members, a sale's bundle and a terms result's share no longer carry the chain. Nor does `deal node bundle` (u230): a node may still attach a receipt over its own `--at` checkpoint with `--witness-receipt`, and `deal node checkpoint --list` still says which checkpoints a tick carries, as the node's own reading. This retires the earlier "five bundle builders" entry: no builder writes the chain.
- **Copies no longer carry `witness_coverage`.** Its "steps 1 to k witnessed" came from that chain.
- **What still travels.** A witness receipt held on the deal's own checkpoint (`checkpoint.witnesses`) still travels and still verifies.
- **A copy's rung, grade and `as_of`** come only from what the copy carries. A deal witnessed only through the cadence therefore reads `self_attested`, with grade witness `not_carried`.
- **The holder's own witnessing.** `deal report`, and `deal report --from-bundle`, which now reads this profile's own state for the file's checkpoint, say what the holder's profile reports in attributed voice, in `assurance.text` and `assurance.holder_witnessed`.
  - The line reads: "Your own profile reports this checkpoint witnessed by ..., as capsulectl reported from your own book".
  - For a deal witnessed through an earlier checkpoint, it names the steps covered (no longer the acts in them, which came from `witness_coverage`) and never says this checkpoint is witnessed.
  - `holder_witnessed` gives the witness, the extent, the witnessed checkpoint's time (`at`) and, in part, `steps_witnessed` of `steps`.
  - That line is in the holder's own output only, never in a copy, its page or its email.
- **The deal page's witness line** reads "missing" for a new copy, as for any copy without a chain.
- **Old copies.** Copies written before this change still verify, their chain checked as before. A report sealed before it that states `witness_coverage` is not shown in a new copy:
  - a History is refused with a hint: seal a current report with `deal report`, then use that one;
  - an evidence request pinned to it is answered with a signed refusal (`coverage_unsatisfiable`).
