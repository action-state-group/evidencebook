// SPDX-License-Identifier: Apache-2.0

package deal

import (
	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// The deal formats' reader (evidencebook#735). capsule-cli#609 removed the
// core's deal-format checks, and the core reads only neutral formats; the
// plugin reads its own through the core's seam (cli.ExtensionReader), with
// the code rc47's core ran (deal_verify_*.go, copied and credited):
//
//   - Witnesses: the cadence chain (parent-log-witness/v1) and its receipts.
//     rc47 let that claim replace the bundle's direct witness claim; the seam
//     reports it beside the core's (reader_witnesses), so a failing direct
//     receipt stays visible. That is the one difference from rc47, by the
//     seam's design (a reader adds and never weakens).
//   - Read: the deal section (x-deal-v0 or x-deal-v1, once, the first kind
//     listed), with a v0 standalone report's as_of; a sale's threads
//     (x-deal-sale/v0); a History selection (x-history-selection/v0). Nested
//     bundles go back through cli.AssessBundle with this reader.
//   - Finish: a copy that discloses a sealed report but lost its deal section
//     (the stripped copy).
//
// The core calls a reader only inside the plugin's own verbs and gates (it
// passes none itself), so every check of a deal copy goes through
// dealBundleVerdict, nestedBundleVerdict, dealCopyPageGate,
// dealDisclosureRecord, CheckDealStandaloneReport or the `verify` verb
// (dealAssess is their shared core, which tests also call).

// dealReaderName is the reader's name, reported as read_by.
const dealReaderName = "capsulectl-deal"

// dealReader is one bundle's reader: the core builds one per bundle and per
// composed member, so its state is that bundle's.
type dealReader struct {
	// anchored and cadence are the Witnesses result: the cadence claim is the
	// witness claim a v0 standalone report's as_of reads, as in rc47.
	anchored bool
	cadence  aacbundle.ClaimResult
	dealSeen bool
}

// newDealReader is the factory the plugin passes the core.
func newDealReader() cli.ExtensionReader { return &dealReader{} }

func (r *dealReader) Name() string { return dealReaderName }

func (r *dealReader) Witnesses(ctx *cli.ExtensionContext) (aacbundle.ClaimResult, []map[string]any, bool) {
	claim, receipts, anchored := cadenceClaim(ctx.Value, ctx.Statement, ctx.Directory)
	r.anchored, r.cadence = anchored, claim
	return claim, receipts, anchored
}

func (r *dealReader) Read(ctx *cli.ExtensionContext, kind string) (map[string]any, []aacbundle.ClaimResult, bool) {
	switch kind {
	case DealProfile, DealProfileV1:
		// One verdict for the deal section: with both extensions the first
		// names it and the other stays the core's uninterpreted entry, so the
		// failing claim is not counted twice.
		if r.dealSeen {
			return nil, nil, false
		}
		r.dealSeen = true
		entry := DealReportEntry(ctx.Value, ctx.Result)
		// A v1 report words its own as_of: its time is the holder's
		// computation time, not the checkpoint's.
		if entry["report"] == "standalone" && kind == DealProfile {
			witnesses := ctx.Witnesses
			if r.anchored {
				witnesses = r.cadence
			}
			entry["as_of"] = dealReportAsOf(ctx.Value, witnesses, r.anchored)
		}
		var claims []aacbundle.ClaimResult
		if findings, ok := entry["findings"].([]string); ok && (entry["status"] == "fail" || entry["status"] == "pass") {
			// A pass that names an unchecked finding keeps the verdict from
			// reading VALID (INCOMPLETE).
			claims = append(claims, aacbundle.ClaimResult{Status: entry["status"].(string), Findings: findings})
		}
		return entry, claims, true
	case DealSaleExtension:
		entry, claim := saleBundleEntry(ctx.Value, ctx.Result, ctx.Directory)
		return entry, []aacbundle.ClaimResult{claim}, true
	case HistorySelectionExtension:
		entry, claim := historyEntry(ctx.Value, ctx.Result, ctx.SignatureStates, ctx.Directory)
		return entry, []aacbundle.ClaimResult{claim}, true
	}
	return nil, nil, false
}

func (r *dealReader) Finish(ctx *cli.ExtensionContext) ([]map[string]any, []aacbundle.ClaimResult) {
	// A deal bundle that discloses a sealed report but carries no deal
	// section at all had it removed. A copy whose report is standalone and
	// that has lost both sections is its steps only: it shows no report text,
	// and nothing here can tell it from a book's own bundle, so it is judged
	// as one (a stated limit).
	if r.dealSeen {
		return nil, nil
	}
	if entry := DealReportEntry(ctx.Value, ctx.Result); entry["status"] == "fail" {
		return []map[string]any{entry}, []aacbundle.ClaimResult{{Status: "fail", Findings: entry["findings"].([]string)}}
	}
	return nil, nil
}

// dealAssess is `verify --bundle`'s verdict, page verdict and report for a
// deal copy, with the plugin's reader. A reader bug is returned as an error.
func dealAssess(value map[string]interface{}) (verdict, pageVerdict string, report map[string]any, err error) {
	return cli.AssessBundle(value, nil, newDealReader)
}

// dealBundleVerdict is rc47's cli.BundleVerdict for a deal copy: the verdict
// and report with the plugin's reader. A reader bug reads INVALID.
func dealBundleVerdict(value map[string]interface{}) (string, map[string]any) {
	return nestedBundleVerdict(value, nil)
}

// dealCopyPageGate is rc47's cli.PageGate for a deal page: the core's gate with
// the plugin's reader.
func dealCopyPageGate(value map[string]interface{}) error {
	return cli.PageGateWith(value, newDealReader)
}

// WithDealVerify makes the tree's `verify --bundle` the plugin's reader verb
// (evidencebook#735): `capsulectl deal verify --bundle FILE
// [--witness-directory FILE]` reaches the plugin's tree, where it is the
// core's verify with the deal reader (cli.VerifyBundleFileWith), so a deal
// copy's report, cadence chain, sale threads and History are read as rc47's
// core read them. Every other use of verify (--capsule, --history, the store,
// and any input error) is the core's own. It returns root.
func WithDealVerify(root *cobra.Command) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() != "verify" || c.RunE == nil {
			continue
		}
		core := c.RunE
		c.RunE = func(c *cobra.Command, args []string) error {
			bundle, _ := c.Flags().GetString("bundle")
			if bundle == "" || otherVerifyInput(c) {
				return core(c, args)
			}
			return cli.VerifyBundleFileWith(c, bundle, newDealReader)
		}
	}
	return root
}

// otherVerifyInput says whether verify was given another input or a flag of
// another input beside --bundle: the core's verb then answers (with its input
// error).
func otherVerifyInput(c *cobra.Command) bool {
	for _, name := range []string{"capsule", "history"} {
		if v, _ := c.Flags().GetString(name); v != "" {
			return true
		}
	}
	for _, name := range []string{"holder-key", "receipt", "anchor-witness-policy", "counterparty-key", "checkpoint-key", "also-served", "log-key", "witness-key", "peer-key", "outcome-policy"} {
		if f := c.Flags().Lookup(name); f != nil && f.Changed {
			return true
		}
	}
	return false
}
