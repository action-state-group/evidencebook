package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The assurance doc names the default witness and the manual countersign
// sequence; it must follow the constant and the commands it describes
// (evidencebook#862).
func TestAssuranceDocNamesTheDefaultWitnessAndTheCountersignSequence(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "skills", "deal", "ASSURANCE.md"))
	require.NoError(t, err)
	text := string(doc)
	assert.Contains(t, text, DealDefaultWitness)
	assert.Contains(t, text, "--no-witness")
	for _, step := range []string{"countersign request --service", "--window WINDOW", "deal countersign --deal DEAL_ID --bundle bundle.json", "--countersign-directory"} {
		assert.Contains(t, text, step)
	}
	assert.Contains(t, text, "not independent of it")
}

// attachInPlace does what `countersign request` does to its --bundle file when
// the service answers: it attaches the returned entry to the bundle, in place.
func attachInPlace(t *testing.T, bundlePath string, entry cli.CountersignatureEntry) {
	t.Helper()
	b := readBundleFile(t, bundlePath)
	require.NoError(t, cli.AttachCountersignatures(b, []cli.CountersignatureEntry{entry}))
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bundlePath, raw, 0o600))
}

// ASSURANCE.md's sequence on an x-deal-v0 deal: the bundle, then the entry
// `countersign request` leaves in that same file (`request` itself needs a
// live HTTPS countersigner, so a fixture signs the entry), then `deal
// countersign` with no --entry, then the receipt from that file.
func TestAssuranceDocSequenceRunsOnAV0Deal(t *testing.T) {
	dealFixture(t)
	dealID := openJetSki(t)
	_, bundlePath, _, _ := dealReportFiles(t, dealID)
	digest, err := aacbundle.BundleDigest(readBundleFile(t, bundlePath))
	require.NoError(t, err)
	entry, signerKey := countersignerEntry(t, digest, []cli.CountersignCheck{{Name: "range membership", Result: "established"}})
	directory := countersignerDirectoryFile(t, "Example Countersigner", signerKey)

	attachInPlace(t, bundlePath, entry)
	attached := dealRun(t, "countersign", "--deal", dealID, "--bundle", bundlePath, "--directory", directory)
	assert.Equal(t, "countersigned", asView(t, attached["countersign"]).Rung)
	out, _, html, _ := dealReportFiles(t, dealID, "--from-bundle", bundlePath, "--directory", directory)
	assert.Equal(t, "countersigned", asView(t, out["countersign"]).Rung)
	assert.Equal(t, "countersigned", pageCountersign(t, html).Rung)
}

// An x-deal-v1 deal (the default for a new deal) has no countersign path:
// `deal countersign` is refused, and the generic `countersign request` refuses
// the v1 report bundle, which carries a disclosures member. ASSURANCE.md says
// so; this fails when a v1 path lands, so the doc is changed with it.
func TestAssuranceDocSaysAV1DealHasNoCountersignPath(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "bundle.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", file)
	directory := filepath.Join(t.TempDir(), "none.json")
	assert.Contains(t, v1Err(t, "countersign", "--deal", h.dealID, "--bundle", file, "--directory", directory), "not served for x-deal-v1 deals yet")
	out, err := invoke(t, "", "countersign", "request", "--profile", "deal", "--service", "https://countersigner.example", "--window", "30d", "--bundle", file)
	require.Error(t, err, out)
	assert.Contains(t, err.Error(), "refuses a bundle carrying disclosures")

	doc, err := os.ReadFile(filepath.Join("..", "..", "skills", "deal", "ASSURANCE.md"))
	require.NoError(t, err)
	assert.Contains(t, string(doc), "x-deal-v1")
	assert.Contains(t, string(doc), "x-deal-v0")
}
