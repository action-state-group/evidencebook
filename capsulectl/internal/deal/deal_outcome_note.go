package deal

// The book owner's own note that an x-deal-v0 deal did not hold (evidencebook#462),
// an outcome_note record after the deal's final close, which it confirms. The
// verb that sealed it (`deal outcome-note seal`) is gone: x-deal-v1's computed
// row status and the adjudicator's ruling replace it (evidencebook#461,
// evidencebook-skills#285). Books sealed by an earlier release may still hold
// one, so the profile still reads it, and no shared copy is made of a deal that
// holds one (u597, evidencebook-deal#218): a record confirming the close would
// say what the note says even with its payload withheld. The x-deal-v0 card rule
// that counts it goes with the x-deal-v0 removal (evidencebook#656).

// dealOutcomeNote is an outcome_note step: why the outcome did not hold.
type dealOutcomeNote struct {
	Reason string `json:"reason"`
}
