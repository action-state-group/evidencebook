// SPDX-License-Identifier: Apache-2.0

// Command row-status is the Go side of the row status differential check
// (test/interop/row-status/diff.py): one JSON input per line on stdin, one
// line out for each, the result or {"error":true}.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/action-state-group/evidencebook/rowstatus"
)

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<24)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for in.Scan() {
		line := "{\"error\":true}"
		if input, err := rowstatus.ParseInput(in.Bytes()); err == nil {
			if res, err := rowstatus.Compute(input); err == nil {
				b, err := json.Marshal(res)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				line = string(b)
			}
		}
		fmt.Fprintln(out, line)
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
