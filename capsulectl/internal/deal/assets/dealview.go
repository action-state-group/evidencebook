// Package assets holds the deal view's composed presentation module: the
// script, its stylesheet and the manifest that pins them by digest
// (capsulectl.deal-view-composed/v0), which `capsulectl deal compose --html`
// draws with until it is removed (evidencebook#869, EB2b). Every other
// receipt is drawn by capsule-viewer's go/receipt. It links no command, hook
// or wording of either launcher.
package assets

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
)

var (
	//go:embed deal-view.js
	JS string
	//go:embed deal-view.css
	CSS string
	//go:embed deal-view.js.sha256
	JSSHA256 string
	// The script's one module, capsulectl.deal-view-composed/v0: the receipt
	// with the other party's half, read from a deal composition
	// (docs/design/two-sided-receipt.md). A single copy's receipt is drawn by
	// capsule-viewer's x-deal-v0 modules (evidencebook#869).
	//go:embed deal-view-composed.manifest.json
	ComposedManifestJSON []byte
)

// Module is the deal view as the emitter's module slot takes it, checked the
// way the core's builder checks a presentation module: its manifest pins the
// script's digest, the runtime accepts it, and it is ambiguous with none of
// the built-in page modules (presentation_build.go). The manifest rides with
// the module, so the emitter checks that it pins the script and stylesheets.
func Module() (emitter.Module, error) {
	var manifest map[string]interface{}
	if err := json.Unmarshal(ComposedManifestJSON, &manifest); err != nil {
		return emitter.Module{}, fmt.Errorf("the deal view's manifest: %w", err)
	}
	executable := cli.ManifestMap(manifest, "executable")
	pin := strings.TrimSpace(JSSHA256)
	if cli.ManifestString(executable, "script_sha256") != pin {
		return emitter.Module{}, errors.New("the deal view's manifest does not pin its script")
	}
	if r := cli.RuntimeRefusal(manifest); r != nil {
		return emitter.Module{}, r
	}
	builtins, err := cli.BuiltinPresentationManifests()
	if err != nil {
		return emitter.Module{}, err
	}
	if err := cli.StaticPresentationCheck(append(builtins, manifest)); err != nil {
		return emitter.Module{}, err
	}
	var styles []string
	for _, s := range cli.AsList(executable["style_sha256"]) {
		if digest, ok := s.(string); ok {
			styles = append(styles, digest)
		}
	}
	return emitter.Module{Code: []byte(JS), SHA256: pin, StyleSHA256: styles, Manifest: json.RawMessage(ComposedManifestJSON)}, nil
}
