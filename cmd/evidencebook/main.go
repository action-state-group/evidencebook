// SPDX-License-Identifier: Apache-2.0

// Command evidencebook runs evidencebook's foreign-evidence adapters.
//
//	evidencebook adapt <adapter> [-policy trust-policy.json] [-explain] <file | ->
//	evidencebook adapt <adapter> -declare
//
// adapt checks the foreign objects in <file> ("-" reads standard input) under the adapter's native
// method and prints the per-property results as JSON. It makes no network
// calls. Exit status: 0 when no property is FAIL, 1 when any is, 2 on a usage
// or input error. -declare prints the adapter's FR-19 declaration instead.
//
// Adapters: acp, ucp, x402.
//
// x402: <file> is detected as one of: a settlement-records leg
// (JSON Capsule whose settlement.wrapped[] carries x402.payment-payload
// content), the bare payment payload (the base64-decoded PAYMENT-SIGNATURE
// octets), or an HTTP header block carrying PAYMENT-SIGNATURE and optionally
// PAYMENT-RESPONSE, as received.
//
// acp: <file> is a settlement-records leg wrapping one ACP object, or an
// ACP 2026-04-17 checkout session, completed session with
// order, delegate payment response or order event, as a JSON body, an MCP
// JSON-RPC response, or an HTTP message (header fields, empty line, body).
// ACP defines no issuer signature, so its native_check is never PASS.
//
// ucp: <file> is a UCP checkout (JSON; natively checked when it
// carries ap2.merchant_authorization), an order body (JSON with
// checkout_id; possession only), or an Order Event Webhook request held
// whole (HTTP/1.1 request line, fields, body), as received. Keys come from
// the trust policy, never from the network.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/action-state-group/evidencebook/adapter"
	"github.com/action-state-group/evidencebook/adapter/acp"
	"github.com/action-state-group/evidencebook/adapter/ucp"
	"github.com/action-state-group/evidencebook/adapter/x402"
)

var adapters = map[string]adapter.Adapter{
	x402.Adapter{}.Name(): x402.Adapter{},
	acp.Adapter{}.Name():  acp.Adapter{},
	ucp.Adapter{}.Name():  ucp.Adapter{},
}

const usage = `usage:
  evidencebook adapt <adapter> [-policy trust-policy.json] [-explain] <file | ->
  evidencebook adapt <adapter> -declare
adapters: acp, ucp, x402`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
	explain := fs.Bool("explain", false, "add each report's human-readable explanation (not part of the report shape)")
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
	var raw []byte
	var err error
	if fs.Arg(0) == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(fs.Arg(0))
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	results := a.Check(raw, policy)
	var props any = results
	if *explain {
		var ex []json.RawMessage
		for _, r := range results {
			b, err := marshalPlain(r)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			if r.DetailFields != nil && r.Detail != "" {
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				m["explanation"] = r.Detail
				b, _ = marshalPlain(m)
			}
			ex = append(ex, b)
		}
		props = ex
	}
	out := struct {
		Adapter    string `json:"adapter"`
		Properties any    `json:"properties"`
	}{a.Name(), props}
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if adapter.AnyFail(results) {
		return 1
	}
	return 0
}

// marshalPlain is json.Marshal without HTML escaping, as enc writes: a
// method such as "relying party policy <id>" keeps its '<' and '>'.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
