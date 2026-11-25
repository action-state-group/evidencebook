package deal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// countersignRequest runs capsule-cli's `countersign request` on file
// against a local HTTPS service whose certificate the client does not trust:
// a bundle the request takes fails only there, after every check of the
// bundle and the profile, and nothing leaves the machine.
func countersignRequest(t *testing.T, file string) error {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the countersign request reached the service")
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	out, err := invoke(t, "", "countersign", "request", "--profile", "deal", "--service", srv.URL, "--window", "30d", "--bundle", file)
	require.Error(t, err, out)
	return err
}

// An x-deal-v0 deal has no countersign path (evidencebook#865: x-deal-v0 is
// frozen): its report bundle carries a disclosures member, so step 2 of the
// sequence, the real `countersign request`, refuses it, and --withheld is
// refused for it. ASSURANCE.md says countersign is served for x-deal-v1
// deals only.
func TestAssuranceDocSequenceIsRefusedOnAV0Deal(t *testing.T) {
	dealFixture(t)
	dealID := openJetSki(t)
	_, bundlePath, _, _ := dealReportFiles(t, dealID)
	assert.Contains(t, countersignRequest(t, bundlePath).Error(), "refuses a bundle carrying disclosures")
	_, err := invoke(t, "", "--profile", "deal", "deal", "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "w.json"), "--withheld")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "countersign is served for x-deal-v1 deals only")

	doc, err := os.ReadFile(filepath.Join("..", "..", "skills", "deal", "ASSURANCE.md"))
	require.NoError(t, err)
	assert.Contains(t, string(doc), "Countersign is served for x-deal-v1 deals only")
}

// ASSURANCE.md's sequence on an x-deal-v1 deal (the default): the withheld
// bundle, which `countersign request` takes (it fails only at the untrusted
// test service); the entry the service would return, signed by a fixture and
// attached as the request attaches it; `deal countersign`; then the report,
// which says countersigned, and countersigned in part once the book has a
// record after the signed one.
func TestAssuranceDocSequenceRunsOnAV1Deal(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "bundle.json")
	out := dealRun(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld")
	assert.Equal(t, true, out["withheld"])
	assert.Nil(t, out["sealed_report"], "the withheld file carries no report, so none is sealed")
	b := readBundleFile(t, file)
	for _, member := range []string{"disclosures", "disclosure_record"} {
		assert.NotContains(t, b, member)
	}
	assert.Equal(t, []string{producerKeyExtension}, mapKeys(b["extensions"].(map[string]interface{})), "only the producer key: no payload, book or report")
	assert.Equal(t, "selected", b["completeness"].(map[string]interface{})["payloads_mode"])
	err := countersignRequest(t, file)
	assert.NotContains(t, err.Error(), "disclosures")
	assert.Contains(t, err.Error(), "certificate", "the request took the bundle and failed only at the untrusted service")

	before := dealRun(t, "report", "--deal", h.dealID)
	assert.Equal(t, "not_countersigned", asStamp(t, before["countersign"]).Finding)

	entry, signerKey := countersignerEntry(t, mustDigest(t, b), []cli.CountersignCheck{{Name: "range membership", Result: "established"}})
	directory := countersignerDirectoryFile(t, "Example Countersigner", signerKey)
	attachInPlace(t, file, entry)
	attached := dealRun(t, "countersign", "--deal", h.dealID, "--bundle", file, "--directory", directory)
	assert.Equal(t, "countersigned", asView(t, attached["countersign"]).Rung)

	// The report states the stored countersignature, resolved against the
	// profile's countersigner directory.
	_, err = invoke(t, "", "profile", "update", "--profile", "deal", "--countersign-directory", directory)
	require.NoError(t, err)
	stamp := asStamp(t, dealRun(t, "report", "--deal", h.dealID)["countersign"])
	assert.Equal(t, "countersigned", stamp.Rung)
	assert.Equal(t, "Example Countersigner", stamp.Signer)
	assert.Equal(t, stamp.ThroughStep, stamp.Steps)
	assert.Contains(t, stamp.Covers, "not this report, and not the other side's records")

	// A record after the signed one: countersigned in part.
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	later := asStamp(t, dealRun(t, "report", "--deal", h.dealID)["countersign"])
	assert.Equal(t, "countersigned_in_part", later.Rung)
	assert.Less(t, later.ThroughStep, later.Steps)
}

func asStamp(t *testing.T, v any) dealGradeCountersign {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var g dealGradeCountersign
	require.NoError(t, json.Unmarshal(raw, &g))
	return g
}
