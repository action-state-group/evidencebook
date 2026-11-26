// SPDX-License-Identifier: Apache-2.0

// Command dealviewfiles writes the files capsulectl-deal's handshake states
// into DIR (capsulectl-deal.d/ in the release's deal-view-VERSION.tar.gz):
// capsule-viewer's x-deal-v0 receipt modules and stylesheets, and the deal
// view's composed module. The bytes are the ones the binary embeds, so the
// archive and the handshake have one source (scripts/release-build.sh;
// scripts/check-deal-view-archive.sh checks the archive against a built
// binary's handshake).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/action-state-group/evidencebook/capsulectl/internal/deal"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dealviewfiles DIR")
		os.Exit(2)
	}
	files, err := deal.PresentationFiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(os.Args[1], name), data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
