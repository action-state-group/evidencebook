<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: a claim with no source note names whose it is, and a seller's own statements are not the buyer's unverified claims (#680)

- A claim noted with `source_kind` and no `source` note said `Claim recorded ():` on the Receipt and in an adjudicator's copy, and `(from )` in its unverified line. These lines now name whose it is: "your agent", "you", "the other party", "the platform" or "an outside source". A source note reads with spaces in claim and evidence lines: `buyer_refund_request` reads "buyer refund request".
- On a deal where the user sells, the agent's own claims are the seller's representations and are no longer listed as the other party's unverified claims, on the Receipt or in an adjudicator's copy.
