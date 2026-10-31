package book

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
)

// The History card is a presentation module of capsule-viewer
// (capsuleviewer.standard-history/v0, capsule-viewer#134 and #149): the page
// for a standard_history/1 card this book serves (evidencebook#157). The book
// offers it to the core's page builder (`capsulectl presentation list|build`)
// in the handshake, as capsulectl-deal offers the deal view.
//
// presentation.json is capsule-viewer's own handshake entry for the module,
// copied from its release bundle (standard-history-module-vVERSION.tar.gz):
// the manifest, and each file's role, path and sha256. The plugin states
// nothing of its own, so the book and the viewer cannot drift. The files it
// names are beside it in assets/standard-history/ and ship in the release as
// standard-history-VERSION.tar.gz, unpacked beside the launcher, where the
// core reads them and checks them against these digests. Without them the
// core refuses the module and the plugin's commands are unaffected.
//
// assets/standard-history/SOURCE.txt names the capsule-viewer release the
// files came from.
//
//go:embed assets/standard-history/presentation.json
var standardHistoryPresentationJSON []byte

// standardHistoryPresentation is the History card module as the handshake states it.
func standardHistoryPresentation() cli.PluginPresentation {
	var p cli.PluginPresentation
	if err := json.Unmarshal(standardHistoryPresentationJSON, &p); err != nil {
		panic(fmt.Sprintf("book: assets/standard-history/presentation.json: %v", err))
	}
	return p
}
