// SPDX-License-Identifier: Apache-2.0

// Command verify checks a bundle produced by another implementation with
// evidencebook.VerifyBundle, and requires it to be fully verified: all three
// claims pass, the checkpoint is authenticated, and every record's capsule
// verifies.
//
//	go run ./test/interop/verify <bundle.json>
//
// Exit 0 when it is; 1 (with the reason) when it is not.
package main

import (
	"fmt"
	"os"

	"github.com/action-state-group/evidencebook"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: verify <bundle.json>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	verified, err := evidencebook.VerifyBundle(data)
	if err == nil {
		err = verified.FullyVerified()
	}
	if err == nil && !verified.AnchorAuthenticated {
		err = fmt.Errorf("the checkpoint is not authenticated")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "evidencebook rejected the bundle: %v (claims %+v)\n", err, verified.Claims)
		os.Exit(1)
	}
	fmt.Printf("evidencebook verified the bundle: %d records, claims %+v, anchor %s:%d\n",
		len(verified.Records), verified.Claims, verified.Anchor.Root, verified.Anchor.MMRSize)
}
