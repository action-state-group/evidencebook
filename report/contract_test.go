// SPDX-License-Identifier: Apache-2.0

package report

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outcomeContract is a compiled contract for the 4-monthly-outcome
// fixture's contract_ref (example-support-outcomes/v1@1), shaped as
// evidencebook-skills compiles one: every requirement the Result cites,
// one of them informational and one with no tier, and one it does not cite.
const outcomeContract = `{"id":"example-support-outcomes/v1","version":"1","requirements":[
{"id":"resolution.done_in_full","profile":"outcome","tier":"must_have"},
{"id":"resolution.right_change","profile":"outcome","tier":"must_have"},
{"id":"policy.confirmed_before_acting","profile":"outcome","tier":"must_have"},
{"id":"communication.no_invented_terms","profile":"outcome","tier":"informational"},
{"id":"communication.plain_words","profile":"outcome"}]}`

func pinned(text string) *Contract {
	return &Contract{Text: text, SHA256: hostWordingDigest(text)}
}

// The page reads each cited requirement's tier from the pinned contract by
// (contract_ref, requirement_ref); the bundle it embeds is unchanged.
func TestBuildReadsTiersFromThePinnedContract(t *testing.T) {
	raw := mustRead(t, filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json"))
	contract := pinned(outcomeContract)
	page, err := Build(raw, View{Card: "outcome", Contract: contract, ContractReport: true})
	require.NoError(t, err)
	assert.Equal(t, "VALID", page.Verdict)
	assert.Equal(t, "example-support-outcomes/v1@1", page.ContractRef)
	assert.Equal(t, contract.SHA256, page.ContractSHA256)
	assert.Equal(t, map[string]string{
		"resolution.done_in_full":         TierMustHave,
		"resolution.right_change":         TierMustHave,
		"policy.confirmed_before_acting":  TierMustHave,
		"communication.no_invented_terms": TierInformational,
	}, page.Tiers, "only the requirements the Result cites, each with its tier")

	// The contract reaches the page's runtime as its contract setting, bound
	// to its digest.
	options, err := canonical.JCS(map[string]interface{}{"audience": "*", "format": "html", "contract": map[string]interface{}{"text": contract.Text, "sha256": contract.SHA256}})
	require.NoError(t, err)
	assert.Contains(t, string(page.HTML), `renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"), undefined, `+strings.NewReplacer("<", `\u003c`, ">", `\u003e`, "&", `\u0026`).Replace(string(options))+`);`)

	plain, err := Build(raw, View{Card: "outcome", ContractReport: true})
	require.NoError(t, err)
	assert.Equal(t, plain.BundleDigest, page.BundleDigest, "the contract is a page input, not part of the bundle")
	assert.Empty(t, plain.ContractRef)
	assert.Nil(t, plain.Tiers, "with no contract every tier is undecided")
	assert.NotContains(t, string(plain.HTML), contract.SHA256)
	_, err = plain.Permalink("")
	require.NoError(t, err)
	_, err = page.Permalink("")
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "a page with a contract gets no permalink")
}

// A requirement that names no tier is undecided, never must-have.
func TestBuildLeavesAnUntieredRequirementUndecided(t *testing.T) {
	raw := mustRead(t, filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json"))
	text := strings.Replace(outcomeContract, `"id":"communication.no_invented_terms","profile":"outcome","tier":"informational"`, `"id":"communication.no_invented_terms","profile":"outcome"`, 1)
	page, err := Build(raw, View{Card: "outcome", Contract: pinned(text)})
	require.NoError(t, err)
	tier, cited := page.Tiers["communication.no_invented_terms"]
	assert.True(t, cited)
	assert.Equal(t, "", tier)
}

func TestBuildRefusesABadOrForeignContract(t *testing.T) {
	raw := mustRead(t, filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json"))
	good := pinned(outcomeContract)
	replaced := func(old, new string) *Contract { return pinned(strings.Replace(outcomeContract, old, new, 1)) }
	for name, c := range map[string]struct {
		contract *Contract
		message  string
	}{
		"a digest of other bytes": {&Contract{Text: good.Text, SHA256: strings.Repeat("0", 64)}, "contract bytes do not hash to its contract_sha256"},
		"not a digest":            {&Contract{Text: good.Text, SHA256: "sha256:" + good.SHA256}, "not a lowercase hex SHA-256"},
		"not JSON":                {pinned(`{"id":`), "contract is not JSON"},
		"two values":              {pinned(outcomeContract + " {}"), "contract is not JSON"},
		`has no requirement "resolution.done_in_full"`: {pinned(`{"id":"example-support-outcomes/v1","version":"1","requirements":[]}`), `has no requirement "resolution.done_in_full"`},
		"a requirement with no id":                     {replaced(`"id":"communication.plain_words",`, ""), "requirement 5 id is not a non-empty string"},
		"a requirement twice":                          {replaced(`"communication.plain_words"`, `"resolution.done_in_full"`), `requirement id "resolution.done_in_full" is listed twice`},
		"an unknown tier":                              {replaced(`"tier":"informational"`, `"tier":"optional"`), `requirement "communication.no_invented_terms" tier is not must_have or informational`},
		"a tier that is not a string":                  {replaced(`"tier":"informational"`, `"tier":1`), `requirement "communication.no_invented_terms" tier is not must_have or informational`},
		"another contract":                             {replaced(`"version":"1"`, `"version":"2"`), "contract example-support-outcomes/v1@2 is not one the Result cites"},
		"a cited requirement it lacks":                 {replaced(`"id":"resolution.right_change"`, `"id":"resolution.other_change"`), `has no requirement "resolution.right_change"`},
	} {
		page, err := Build(raw, View{Card: "outcome", Contract: c.contract})
		require.ErrorIs(t, err, ErrRefused, name)
		assert.Nil(t, page, name)
		assert.Contains(t, err.Error(), c.message, name)
	}
}
