<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: a passing check the user acts on is asked with its approval text (evidencebook#1030)

- `skills/deal/SKILL.md` Rules add "A pass the user acts on shows its text": when `deal check` passes and the final button is the user's to press, the agent asks with the check's `approval_text`, shown as the card rule says, never its own summary; after a paused check whose card was already shown, it only names the button. The `silent` voice now says it drops the agent's narration, never that text, and `references/purchase.md` step 9 and `references/cancellation.md` say the same where the button is the user's to press.
