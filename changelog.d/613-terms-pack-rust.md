<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `deal`: the x-deal-v0 record type `terms_pack` (`TYPE_TERMS_PACK`, `TermsPackBody`, `Body::TermsPack`):
  the deal-terms pack a check ran under, named apart from the check's evaluation (evidencebook#613).
  The chain check holds it right after that evaluation, never on a represent check, with a digest
  the evaluation ran, one per pack. The vendored schema follows.
