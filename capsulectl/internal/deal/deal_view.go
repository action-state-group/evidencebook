package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/receipt"

	"github.com/action-state-group/evidencebook/capsulectl/internal/deal/assets"
)

// The presentation modules this plugin's handshake offers the core's page
// builder (`capsulectl presentation list|build`, capsule-cli#428 step 4).
// The core reads each module's files itself from capsulectl-deal.d/ beside
// the launcher, which the release ships as deal-view-VERSION.tar.gz, and
// checks them against the digests stated here. Without that directory the
// core refuses the modules and the plugin's commands are unaffected.
//
//   - capsule-viewer's x-deal-v0 receipt modules (go/receipt's unilateral and
//     seller, the one receipt renderer, capsule-viewer#383): a single copy's
//     receipt, as the plugin's own pages draw it (dealReportHTML). Each is a
//     script and the three stylesheets its manifest pins (receipt.Styles).
//   - The deal view's composed module (capsulectl.deal-view-composed/v0):
//     the receipt with the other party's half, which `deal compose --html`
//     still draws (docs/design/two-sided-receipt.md) until it is removed
//     (evidencebook#869, EB2b). deal-view.js carries deal-view.css inline.
//
// The files are held by the assets package and go/receipt.
var (
	DealViewJS                   = assets.JS
	DealViewCSS                  = assets.CSS
	DealViewJSSHA256             = assets.JSSHA256
	DealViewComposedManifestJSON = assets.ComposedManifestJSON
)

// dealViewDir is where the modules' files sit, relative to the launcher.
const dealViewDir = "capsulectl-deal.d"

// receiptStyleNames name go/receipt's stylesheets in capsulectl-deal.d/, in
// receipt.Styles's order (the manifest's style_sha256 order).
var receiptStyleNames = []string{"receipt-kit.css", "receipt-kit-dark.css", "receipt.css"}

// presentationFile is one file the handshake states, with its bytes.
type presentationFile struct {
	role, name string
	data       []byte
}

// presentationModules are the modules the handshake offers, each with its
// manifest and files.
func presentationModules() ([]struct {
	manifest []byte
	files    []presentationFile
}, error) {
	type module = struct {
		manifest []byte
		files    []presentationFile
	}
	var out []module
	for _, kind := range []string{receipt.Unilateral, receipt.Seller} {
		m, err := receipt.Module(kind)
		if err != nil {
			return nil, err
		}
		styles, err := receipt.Styles(kind)
		if err != nil {
			return nil, err
		}
		if len(styles) != len(receiptStyleNames) {
			return nil, fmt.Errorf("receipt %s: %d stylesheets, want %d", kind, len(styles), len(receiptStyleNames))
		}
		files := []presentationFile{{role: "script", name: "receipt_" + kind + ".js", data: m.Code}}
		for i, s := range styles {
			files = append(files, presentationFile{role: "style", name: receiptStyleNames[i], data: s})
		}
		out = append(out, module{manifest: m.Manifest, files: files})
	}
	composed := []presentationFile{{role: "script", name: "deal-view.js", data: []byte(DealViewJS)}, {role: "style", name: "deal-view.css", data: []byte(DealViewCSS)}}
	return append(out, module{manifest: DealViewComposedManifestJSON, files: composed}), nil
}

// dealViewPresentations are the modules as the handshake states them.
// Modules that share a file (the receipt modules' stylesheets) name it
// once in capsulectl-deal.d/ with one digest.
func dealViewPresentations() []cli.PluginPresentation {
	modules, err := presentationModules()
	if err != nil {
		// The bytes are embedded: a failure here is a build defect, which
		// the handshake test catches.
		panic(err)
	}
	var out []cli.PluginPresentation
	for _, m := range modules {
		var files []cli.PluginPresentationFile
		for _, f := range m.files {
			sum := sha256.Sum256(f.data)
			files = append(files, cli.PluginPresentationFile{Role: f.role, Path: dealViewDir + "/" + f.name, SHA256: hex.EncodeToString(sum[:])})
		}
		out = append(out, cli.PluginPresentation{Manifest: m.manifest, Files: files})
	}
	return out
}

// PresentationFiles are the files capsulectl-deal.d/ holds, by name, as the
// handshake states them: what the release writes into deal-view-VERSION.tar.gz
// (scripts/release-build.sh, through scripts/dealviewfiles). A name two
// modules share must hold the same bytes.
func PresentationFiles() (map[string][]byte, error) {
	modules, err := presentationModules()
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, m := range modules {
		for _, f := range m.files {
			if prev, ok := out[f.name]; ok && string(prev) != string(f.data) {
				return nil, fmt.Errorf("%s: two modules state different bytes", f.name)
			}
			out[f.name] = f.data
		}
	}
	return out, nil
}
