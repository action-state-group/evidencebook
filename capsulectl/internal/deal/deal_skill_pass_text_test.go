package deal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A passing `deal check` is `voice: silent`, which an agent once read as
// "say nothing of the check" and asked the user to press "Place order" with
// its own summary (evidencebook#1030). The skill keeps, in SKILL.md's Rules
// and next to the `silent` voice, that a pass the user acts on is asked with
// the check's approval_text, and each scenario step where the user presses
// the button says the same.
func TestDealSkillShowsApprovalTextOnAPassTheUserActsOn(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	skill := readSkillText(t, "SKILL.md")

	rules := flat(section(t, skill, "Rules (never break these)"))
	for _, want := range []string{
		"**A pass the user acts on shows its text.** When `deal check` passes and the user presses the final button themselves",
		"ask them with the check's `approval_text`, shown as the rule above says. One sentence of your own may lead in and name the button;",
		"never your own summary of the amount, the payee or the terms.",
		"When the check you act on paused instead, its card was already shown and the user's answer sealed: do not show it again, just name the button.",
	} {
		assert.Contains(t, rules, want)
	}

	voice := flat(section(t, skill, "What you say: `voice` and `say`"))
	assert.Contains(t, voice, "`silent`: the step is on the record; say nothing about it. A passing `deal check` is silent, but when the act is the user's own (they press the final button themselves), your request to them is the check's `approval_text`, as the Rules say: `silent` drops your narration, never that text.")

	purchase := flat(section(t, readSkillText(t, "references/purchase.md"), "Purchase"))
	assert.Contains(t, purchase, "When the user presses it themselves after a check that passed, ask them with the check's `approval_text`, as it is; after a pause, its card was shown in step 8, so just name the button.")

	cancel := flat(readSkillText(t, "references/cancellation.md"))
	assert.Contains(t, cancel, "When the check passed and the user presses the cancel button themselves, ask them with the check's `approval_text`, as it is; after a pause, its card was already shown, so just name the button.")
}
