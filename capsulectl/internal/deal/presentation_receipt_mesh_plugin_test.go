package deal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// meshCompositionDir holds compositions the Mesh capsules plugin built,
// unedited (testdata/mesh-composition/README.md says from which commit and
// how to build them again).
var meshCompositionDir = filepath.Join("testdata", "mesh-composition")

// The receipt drawn from real Mesh output (evidencebook-deal#78): each
// composition goes through the host's own presentation build, which selects
// the composed deal view, and the page draws the receipt's cases in Chrome
// at 390px. Their bundle answer is stamped by both; a refusal or an absence
// is your receipt only, the absence's box reading "No record". A
// composition with no member of theirs is not Mesh output: the plugin
// builds none without a request record (deal_receipt/composition.rs).
func TestPresentationMeshReceiptFromThePlugin(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	installDealView(t)
	chrome := startHeadlessChrome(t, binary)
	for _, c := range []struct {
		file, banner, their, box string
	}{
		{"artifact.json", "Stamped by both sides", "verified", "Their record is about the same exchange as yours"},
		{"refusal.json", "Your receipt only", "refused_unchecked", "A refusal in their name came back on Oct 29; its signature was not checked here."},
		{"absence.json", "Your receipt only", "absent", "No record. Your agent asked and nothing came back by"},
	} {
		t.Run(c.file, func(t *testing.T) {
			list, err := passList(t, filepath.Join(meshCompositionDir, c.file), "*", "html")
			require.NoError(t, err)
			assert.Equal(t, "capsulectl.deal-view-composed/v0", list)
			b, page, err := passBuild(t, filepath.Join(meshCompositionDir, c.file))
			require.NoError(t, err)
			require.NotEmpty(t, b.Modules)
			path := filepath.Join(t.TempDir(), "page.html")
			require.NoError(t, os.WriteFile(path, []byte(page), 0o600))
			var got struct{ Banner, Their, Refusal, Text string }
			chrome.open(t, "file://"+path, presentationViews[0]).eval(t, `(() => {
  const q = (s) => document.querySelector("#deal " + s);
  return {
    Banner: (q(".deal-banner") || {}).textContent || "",
    Their: (q("[data-their]") || { dataset: {} }).dataset.their || "",
    Refusal: (q("p.deal-bad") || {}).textContent || "",
    Text: (document.querySelector("#deal") || {}).innerText || "",
  };
})()`, &got)
			require.Empty(t, got.Refusal, "the deal view draws the composition")
			assert.Contains(t, got.Banner, c.banner)
			assert.Equal(t, c.their, got.Their)
			assert.Contains(t, got.Text, c.box)
			assert.NotContains(t, got.Text, "Mesh", "no Mesh wording (two-sided-receipt.md C1)")
		})
	}
}
