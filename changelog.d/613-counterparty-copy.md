### Added: what a rule 12 counterparty copy withholds, as vectors (#613)

- `dealprofile`: PROFILE.md section 6 rule 12 "Copies" now lists every record a counterparty copy
  of a seller's deal withholds so that nothing in it says which of the seller's agent claims were
  authorized (evidencebook#613, following #607): besides the `claim_authority`, the represent
  checks, the evaluations and the unchecked outcomes, every answer to a check (`user_approval`,
  `card_answer`, whatever the answer), every telling (a `disclosure`, or an `action-record/v0`
  with `disclosed`) and every `close` (its `unchecked_actions` counts the unchecked acts). The
  counterparty's acceptance and platform approval observations stay. The copy says how the deal
  ended from the outcome the close refs, or "not shared" when that outcome is an unchecked one.
  New `CounterpartyWithheld` and `RequiredDisclosed` give, for a deal's records, the positions such
  a copy withholds under rule 12 and the ones it must disclose; `check_profile.py` computes the same
  and generates `x-deal-v0/fixtures/counterparty-copy-vectors.json` (twin deals that differ only
  in whether the ordered claim was authorized), which both are checked against. Known limit: an
  act the gate held that the agent did anyway is not in force, so the buyer's copy lacks the steps
  that rest on it (the offer, the buyer's acceptance, a commit) and its end outcome compares the
  delivery with the terms without that offer; a buyer who knows those steps happened can tell they
  were not authorized, never why.
