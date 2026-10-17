// SPDX-License-Identifier: Apache-2.0

// Command evidencebook runs evidencebook's foreign-evidence adapters.
//
//	evidencebook adapt <adapter> [-policy trust-policy.json] <file>
//	evidencebook adapt <adapter> -declare
//
// adapt checks the foreign objects in <file> under the adapter's native
// method and prints the per-property results as JSON. It makes no network
// calls. Exit status: 0 when no property is FAIL, 1 when any is, 2 on a usage
// or input error. -declare prints the adapter's FR-19 declaration instead.
//
// Adapters: x402 (<file> is an HTTP header block carrying PAYMENT-SIGNATURE
// and optionally PAYMENT-RESPONSE, as received).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/action-state-group/evidencebook/adapter"
	"github.com/action-state-group/evidencebook/adapter/x402"
)

var adapters = map[string]adapter.Adapter{
	x402.Adapter{}.Name(): x402.Adapter{},
}

const usage = `usage:
  evidencebook adapt <adapter> [-policy trust-policy.json] <file>
  evidencebook adapt <adapter> -declare
adapters: x402`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "adapt" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	a, ok := adapters[args[1]]
	if !ok {
		fmt.Fprintf(stderr, "unknown adapter %q\n%s\n", args[1], usage)
		return 2
	}
	fs := flag.NewFlagSet("adapt "+args[1], flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyPath := fs.String("policy", "", "trust policy JSON file (pins, keys, clock)")
	declare := fs.Bool("declare", false, "print the adapter's FR-19 declaration")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if *declare {
		if fs.NArg() != 0 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		if err := enc.Encode(a.NativeObject()); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	var policy adapter.TrustPolicy
	if *policyPath != "" {
		var err error
		if policy, err = adapter.LoadTrustPolicy(*policyPath); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	results := a.Check(raw, policy)
	out := struct {
		Adapter    string                   `json:"adapter"`
		Properties []adapter.PropertyResult `json:"properties"`
	}{a.Name(), results}
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if adapter.AnyFail(results) {
		return 1
	}
	return 0
}
