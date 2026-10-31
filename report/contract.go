// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"slices"
)

// Contract is a compiled Evidence Contract v0 as distributed (for example
// evidencebook-skills contracts/<id>/<version>.json): its exact text and
// the lowercase hex SHA-256 of its UTF-8 bytes. Build checks the one against
// the other, the few members the page reads, and that the contract is one
// the Result cites; the contract's own schema is its compiler's to check.
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

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// checkedContract is what checkContract establishes: the contract's
// contract_ref (`<id>@<version>`, as a Result cites it) and each
// requirement's tier by requirement id, "" when it names none.
type checkedContract struct {
	ref   string
	tiers map[string]string
}

// checkContract checks c's digest and the members the page reads: id,
// version, and requirements, each with a unique id and at most one of the
// two tiers. Other members are left to the contract's schema.
func checkContract(c Contract) (checkedContract, error) {
	if !hexSHA256.MatchString(c.SHA256) {
		return checkedContract{}, refusal("contract sha256 is not a lowercase hex SHA-256")
	}
	if sum := sha256.Sum256([]byte(c.Text)); hex.EncodeToString(sum[:]) != c.SHA256 {
		return checkedContract{}, refusal("contract bytes do not hash to its sha256")
	}
	var doc struct {
		ID           string `json:"id"`
		Version      string `json:"version"`
		Requirements []struct {
			ID   string          `json:"id"`
			Tier json.RawMessage `json:"tier"`
		} `json:"requirements"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(c.Text)))
	if err := decoder.Decode(&doc); err != nil {
		return checkedContract{}, refusal("contract is not an Evidence Contract v0 object with a string id, a string version and a requirements array")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return checkedContract{}, refusal("contract input must contain one JSON value")
	}
	if doc.ID == "" || doc.Version == "" || len(doc.Requirements) == 0 {
		return checkedContract{}, refusal("contract names no id, no version or no requirements")
	}
	out := checkedContract{ref: doc.ID + "@" + doc.Version, tiers: make(map[string]string, len(doc.Requirements))}
	for i, requirement := range doc.Requirements {
		if requirement.ID == "" {
			return checkedContract{}, refusef("contract requirements[%d] has no id", i)
		}
		if _, seen := out.tiers[requirement.ID]; seen {
			return checkedContract{}, refusef("contract names requirement %q twice", requirement.ID)
		}
		tier := ""
		if requirement.Tier != nil {
			if json.Unmarshal(requirement.Tier, &tier) != nil || (tier != TierMustHave && tier != TierInformational) {
				return checkedContract{}, refusef("contract requirement %q has tier %s; a tier is %q or %q", requirement.ID, requirement.Tier, TierMustHave, TierInformational)
			}
		}
		out.tiers[requirement.ID] = tier
	}
	return out, nil
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
