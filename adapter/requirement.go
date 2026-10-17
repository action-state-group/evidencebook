// SPDX-License-Identifier: Apache-2.0

package adapter

import "fmt"

// Relation says how a requirement relates to a native check
// (PRD-EVIDENCE-INTEROP FR-28; interop-profile-v0 §8.1 rows 5 and 6).
type Relation int

const (
	// IsTheCheck: the requirement asks exactly what the native check decides
	// ("is this payment authorization signed by the payer?").
	IsTheCheck Relation = iota + 1
	// SupportedByTheCheck: the check is evidence toward the requirement but
	// does not decide it ("was the resource paid for?").
	SupportedByTheCheck
)

func (r Relation) String() string {
	switch r {
	case IsTheCheck:
		return "is the check"
	case SupportedByTheCheck:
		return "supported by the check"
	}
	return fmt.Sprintf("Relation(%d)", int(r))
}

// Requirement is one requirement evaluated against a foreign object, with the
// native properties it depends on.
type Requirement struct {
	ID        string
	Relation  Relation
	DependsOn []string
}

// Evaluation is the sufficiency and verdict one foreign object's native check
// contributes to a requirement. It assumes every envelope property of the
// Artifact carrying the object passed: a failed envelope makes the object
// unusable for any requirement, and that mapping belongs to the envelope
// verifier, not to an adapter (interop-profile-v0 §7.2, §8.1).
type Evaluation struct {
	Requirement string `json:"requirement"`
	// Row is the interop-profile-v0 §8.1 row applied, or the adapter mapping
	// used for a native status the table does not name.
	Row string `json:"row"`
	// Sufficiency and Verdict are empty when the native check does not decide
	// them (row 7: the contract evaluates the Artifact's content).
	Sufficiency string `json:"sufficiency,omitempty"`
	Verdict     string `json:"verdict,omitempty"`
	// Basis names the properties the mapping turned on.
	Basis []string `json:"basis"`
}

// Evaluate maps the native results a requirement depends on to that
// requirement's sufficiency and verdict, and nothing more (FR-28):
//
//   - a FAIL decides a requirement that is the check: SATISFIED, not_met
//     (row 5);
//   - a FAIL removes the evidence for a requirement it only supports:
//     INSUFFICIENT, not_evaluable (row 6), unless other evidence settles it,
//     which is the contract's evaluation, not this function's;
//   - an absent input is INSUFFICIENT, and a check not run or undecided is
//     UNKNOWN, both not_evaluable (the profile's rows 3 and 4, applied to the
//     native properties);
//   - every property PASS decides a requirement that is the check: SATISFIED,
//     met. For a requirement it only supports, the contract evaluates the
//     content (row 7) and Evaluate leaves sufficiency and verdict empty.
//
// A property the requirement depends on that was not reported counts as
// NOT_PRESENT. A FAIL never yields met.
func Evaluate(results []PropertyResult, req Requirement) Evaluation {
	ev := Evaluation{Requirement: req.ID}
	byStatus := map[Status][]string{}
	for _, name := range req.DependsOn {
		st := NotPresent
		if r, ok := Find(results, name); ok {
			st = r.Status
		}
		byStatus[st] = append(byStatus[st], name)
	}
	switch {
	case len(byStatus[Fail]) > 0:
		ev.Basis = byStatus[Fail]
		if req.Relation == IsTheCheck {
			ev.Row, ev.Sufficiency, ev.Verdict = "5", "SATISFIED", "not_met"
		} else {
			ev.Row, ev.Sufficiency, ev.Verdict = "6", "INSUFFICIENT", "not_evaluable"
		}
	case len(byStatus[NotPresent]) > 0:
		ev.Basis = byStatus[NotPresent]
		ev.Row, ev.Sufficiency, ev.Verdict = "native NOT_PRESENT (as row 3)", "INSUFFICIENT", "not_evaluable"
	case len(byStatus[NotChecked])+len(byStatus[Inconclusive]) > 0:
		ev.Basis = append(append([]string{}, byStatus[NotChecked]...), byStatus[Inconclusive]...)
		ev.Row, ev.Sufficiency, ev.Verdict = "native NOT_CHECKED or INCONCLUSIVE (as row 4)", "UNKNOWN", "not_evaluable"
	default:
		ev.Basis = byStatus[Pass]
		if req.Relation == IsTheCheck {
			ev.Row, ev.Sufficiency, ev.Verdict = "7 (the requirement is the check, and it passed)", "SATISFIED", "met"
		} else {
			ev.Row = "7 (the contract evaluates the content)"
		}
	}
	return ev
}
