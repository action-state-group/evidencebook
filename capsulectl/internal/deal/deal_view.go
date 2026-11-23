package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"

	"github.com/action-state-group/evidencebook/capsulectl/internal/deal/assets"
)

// The deal view is this plugin's presentation module (capsulectl.deal-view/v0,
// capsule-cli#428 step 4): the page section for deal receipts. deal-view.js
// carries deal-view.css inline and registers itself with the page's runtime;
// deal-view.manifest.json pins both by digest. The plugin's own pages
// (`deal report --html`, the shared page) put it in the emitter's module slot
// from these embedded bytes (dealViewModule).
//
// The handshake also offers it to the core's page builder
// (`capsulectl presentation list|build`). The core reads the files itself from
// capsulectl-deal.d/ beside the launcher, which the release ships as
// deal-view-VERSION.tar.gz, and checks them against the digests stated here.
// Without that directory the core refuses the module and the plugin's
// commands are unaffected.
// The files are held by the assets package, which the report builder shares
// (capsulectl-book's settlement report carries deal receipts).
var (
	DealViewJS                   = assets.JS
	DealViewCSS                  = assets.CSS
	DealViewJSSHA256             = assets.JSSHA256
	DealViewManifestJSON         = assets.ManifestJSON
	DealViewComposedManifestJSON = assets.ComposedManifestJSON
)

// dealViewDir is where the module's files sit, relative to the launcher.
const dealViewDir = "capsulectl-deal.d"

// dealViewPresentations are the deal view's two modules as the handshake
// states them: one script and stylesheet, registered under each manifest.
func dealViewPresentations() []cli.PluginPresentation {
	style := sha256.Sum256([]byte(DealViewCSS))
	files := []cli.PluginPresentationFile{
		{Role: "script", Path: dealViewDir + "/deal-view.js", SHA256: strings.TrimSpace(DealViewJSSHA256)},
		{Role: "style", Path: dealViewDir + "/deal-view.css", SHA256: hex.EncodeToString(style[:])},
	}
	return []cli.PluginPresentation{{Manifest: DealViewManifestJSON, Files: files}, {Manifest: DealViewComposedManifestJSON, Files: files}}
}
