// SPDX-License-Identifier: Apache-2.0

// Command verify checks a bundle produced by another implementation with
// evidencebook.VerifyBundle, and requires it to be fully verified: all three
// claims pass, the checkpoint is authenticated, and every record's capsule
// verifies.
//
//	go run ./test/interop/verify <bundle.json> [<checkpoint key hex> <witnesses.json>]
//
// With a checkpoint key and a witness directory it checks the bundle as a
// holder's answer (VerifyBundleAnswer): anchored to that key, and a receipt
// carried on the checkpoint must verify under the directory, else the
// bundle is not witnessed.
//
// Exit 0 when it is; 1 (with the reason) when it is not.
package main

import (
	"fmt"
	"os"

	"github.com/action-state-group/evidencebook"
)

func main() {
	if len(os.Args) != 2 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: verify <bundle.json> [<checkpoint key hex> <witnesses.json>]")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 4 {
		directory, err := os.ReadFile(os.Args[3])
		var rows []evidencebook.WitnessRow
		if err == nil {
			rows, err = evidencebook.ParseWitnessDirectory(directory)
		}
		var answer evidencebook.VerifiedBundle
		if err == nil {
			answer, err = evidencebook.VerifyBundleAnswer(data, evidencebook.ResponderKeys{Checkpoint: os.Args[2], Witnesses: rows})
		}
		if err == nil && !answer.Witnessed {
			err = fmt.Errorf("no carried receipt verified under the directory")
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "evidencebook rejected the bundle as an answer: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("evidencebook verified the bundle as an answer: witnessed, anchor %s\n", answer.AnchorKeyID)
		return
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
