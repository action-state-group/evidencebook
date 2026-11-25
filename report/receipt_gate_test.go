// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/receipt"
)

// testdata/viewer-receipts holds two receipts from capsule-viewer's
// tests/testdata/receipt (README there): a Mesh node's payer copy, which is a
// producer selection (strict verify: INCOMPLETE, interval coverage withheld as
// selection_not_checkable), and a seller's keep copy (VALID).
const (
	meshPayer  = "testdata/viewer-receipts/mesh-payer.bundle.json"
	sellerKeep = "testdata/viewer-receipts/seller-keep.bundle.json"
)

// mutated is the fixture with change applied to its decoded JSON.
func mutated(t *testing.T, path string, change func(map[string]interface{})) map[string]interface{} {
	t.Helper()
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(mustRead(t, path), &raw))
	change(raw)
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	value, err := cli.DecodeBundleJSON(b)
	require.NoError(t, err)
	return value
}

func decoded(t *testing.T, path string) map[string]interface{} {
	return mutated(t, path, func(map[string]interface{}) {})
}

// A carried receipt is held to the verdict of the page that opens it: a
// producer selection passes on its other checks, as the page runtime and the
// receipt modules read it (capsule-viewer#386). The core page gate alone
// refuses it.
func TestAProducerSelectedReceiptPassesThePagesVerdict(t *testing.T) {
	payer := decoded(t, meshPayer)
	assert.ErrorContains(t, cli.PageGate(payer), "INCOMPLETE")
	assert.NoError(t, receiptPageGate(payer))
	assert.NoError(t, receiptPageGate(decoded(t, sellerKeep)))
}

// The exemption is the producer selection's alone: a receipt with anything
// else unmet stays refused, with the page gate's reason.
func TestAReceiptWithAnythingElseUnmetIsStillRefused(t *testing.T) {
	cases := map[string]func(map[string]interface{}){
		// Not a producer selection: its coverage is withheld for another reason.
		"not a selection": func(b map[string]interface{}) {
			b["completeness"].(map[string]interface{})["selection"] = "complete"
		},
		// A record's signature no longer verifies.
		"signature": func(b map[string]interface{}) {
			r := b["records"].([]interface{})[0].(map[string]interface{})
			sig := r["signature"].(string)
			r["signature"] = strings.Repeat("A", len(sig))
		},
		// A record changed after it was sealed.
		"tampered record": func(b map[string]interface{}) {
			b["records"].([]interface{})[0].(map[string]interface{})["timestamp"] = "2001-01-01T00:00:00Z"
		},
		// A record's membership proof is gone.
		"membership": func(b map[string]interface{}) {
			b["completeness_certificate"].(map[string]interface{})["memberships"] = []interface{}{}
		},
		// The checkpoint's signature no longer verifies.
		"checkpoint": func(b map[string]interface{}) {
			cp := b["checkpoint"].(map[string]interface{})
			cp["root"] = strings.Repeat("0", len(cp["root"].(string)))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, receiptPageGate(mutated(t, meshPayer, change)))
		})
	}
}

// The real receipt modules take the receipts they are for: a Mesh payer's
// copy is the unilateral module's, a seller's copy the seller module's, and
// a copy neither takes is refused.
func TestTheRealReceiptModulesTakeTheirReceipts(t *testing.T) {
	manifests := map[string]moduleManifest{}
	var all []moduleManifest
	for _, kind := range []string{receipt.Unilateral, receipt.Seller} {
		module, err := receipt.Module(kind)
		require.NoError(t, err)
		var m moduleManifest
		require.NoError(t, json.Unmarshal(module.Manifest, &m))
		manifests[kind] = m
		all = append(all, m)
	}
	r := func(path string) Receipt { return Receipt{Exchange: "ex", Bundle: string(mustRead(t, path))} }
	assert.NoError(t, checkReceiptBundle("d1", r(meshPayer), []moduleManifest{manifests[receipt.Unilateral]}))
	assert.Error(t, checkReceiptBundle("d1", r(meshPayer), []moduleManifest{manifests[receipt.Seller]}))
	assert.NoError(t, checkReceiptBundle("d1", r(sellerKeep), []moduleManifest{manifests[receipt.Seller]}))
	assert.Error(t, checkReceiptBundle("d1", r(sellerKeep), []moduleManifest{manifests[receipt.Unilateral]}))
	assert.NoError(t, checkReceiptBundle("d1", r(meshPayer), all))
	assert.NoError(t, checkReceiptBundle("d1", r(sellerKeep), all))
}
