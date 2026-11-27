<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: a claim with no source note names whose it is, and your own side's statements are not listed as the other party's unverified claims (#680, #1036)

- A claim noted with `source_kind` and no `source` note said `Claim recorded ():` on the Receipt and in an adjudicator's copy, and `(from )` in its unverified line. These lines now name whose it is: "your agent", "you", "the other party", "the platform" or "an outside source". A source note reads with spaces in claim and evidence lines: `buyer_refund_request` reads "buyer refund request".
- The user's own claims are no longer listed as the other party's unverified claims (#1036). On a deal where the user sells, neither are the agent's own claims, which are the seller's representations. This holds on the Receipt and in an adjudicator's copy.
