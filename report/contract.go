// SPDX-License-Identifier: Apache-2.0

package report

import (
	"slices"

	"github.com/action-state-group/capsule-viewer/go/emitter"
)

// Contract is a compiled Evidence Contract v0 as distributed (for example
// evidencebook-skills contracts/<id>/<version>.json): its exact text and
// the lowercase hex SHA-256 of its UTF-8 bytes. Build checks the one against
// the other and the few members the page reads (emitter.CheckContract), and
// that the contract is one the Result cites; the contract's own schema is
// its compiler's to check.
type Contract struct {
	Text   string
	SHA256 string
}

// The requirement tiers of Evidence Contract v0 (`tier`): a must-have
// requirement gates the overall judgment, an informational one is reported
// without gating. A requirement that names no tier, or a page built with no
// contract, has an undecided tier, which is never read as must-have.
const (
	TierMustHave      = "must_have"
	TierInformational = "informational"
)

// checkedContract is what the viewer's emitter.CheckContract establishes:
// the contract's contract_ref (`<id>@<version>`, as a Result cites it) and
// each requirement's tier by requirement id, "" when it names none.
type checkedContract struct {
	ref   string
	tiers map[string]string
}

// checkContract checks c as the page's runtime does (its digest, and the
// members the page reads), so Build refuses exactly the contracts the
// emitter would.
func checkContract(c Contract) (checkedContract, error) {
	checked, err := emitter.CheckContract(emitter.Contract{Text: c.Text, SHA256: c.SHA256})
	if err != nil {
		return checkedContract{}, refusal(err.Error())
	}
	return checkedContract{ref: checked.ID + "@" + checked.Version, tiers: checked.Tiers}, nil
}

// against checks that the Result is one this contract describes: it cites
// the contract's contract_ref, and each requirement_ref its claims name
// under that ref is a requirement of the contract. It returns the tiers of
// the requirements the Result cites under that ref, by requirement_ref.
func (c checkedContract) against(contracts []string, result map[string]interface{}) (map[string]string, error) {
	if !slices.Contains(contracts, c.ref) {
		return nil, refusef("contract %s is not one the Result cites (%v); build the page with the contract its claims name", c.ref, contracts)
	}
	tiers := make(map[string]string)
	for _, raw := range asSlice(result["claims"]) {
		claim, _ := raw.(map[string]interface{})
		if ref, _ := claim["contract_ref"].(string); ref != c.ref {
			continue
		}
		requirement, ok := claim["requirement_ref"].(string)
		if !ok {
			continue
		}
		tier, known := c.tiers[requirement]
		if !known {
			return nil, refusef("contract %s has no requirement %q, which the Result's claim %v cites", c.ref, requirement, claim["id"])
		}
		tiers[requirement] = tier
	}
	return tiers, nil
}
