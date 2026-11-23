package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/require"
)

// The rc47 goldens (evidencebook#735, PR 1). capsule-cli#609 deletes the
// core's deal verify code and adds a seam (cli.AssessBundle with an
// extension reader); the plugin then checks deal copies itself. Before that
// lands, these files fix what capsule-cli v0.1.0-rc47's `verify --bundle`
// says of each deal copy: testdata/deal-verify holds the copies (committed
// bytes, so no clock or key is regenerated) and, beside each, rc47's result.
// PR 2 (on capsule-cli rc50, whose core reads no deal format) compares the
// plugin's reader with these goldens, unchanged, with one expected
// difference (dealVerifyExpectedDifference).
//
// The goldens were written by EB_WRITE_DEAL_VERIFY_GOLDENS=1 on capsule-cli
// rc47 with the plugin's v1 report rule registered (the builder,
// buildDealVerifyCopies, is in the history of #738). That live rc47 oracle
// went with the pin bump: the goldens are now fixed, and writing them again
// is refused.

const dealVerifyDir = "testdata/deal-verify"

// dealVerifyResult is what a golden records of one copy: the verdict, the
// page gate, the extension entries of the kinds the plugin will read or
// recurse through (composed/v1 included, with each member's own report), and
// the witness entry. PageGateNoDirectory is cli.PageGate's result, which
// takes no witness directory: a copy can read INVALID under its directory
// and still pass it. rc47 exports no page verdict string, so the gate's
// result is what stands for it (now dealCopyPageGate, the core's gate with
// the plugin's reader).
type dealVerifyResult struct {
	Verdict             string           `json:"verdict"`
	PageGateNoDirectory string           `json:"page_gate_no_directory"`
	Extensions          []map[string]any `json:"extensions"`
	Witnesses           map[string]any   `json:"witnesses"`
}

// dealVerifyKinds are the extension kinds the plugin will read.
var dealVerifyKinds = map[string]bool{
	dealprofile.Profile: true, "x-deal-v1": true, DealSaleExtension: true, HistorySelectionExtension: true,
	dealprofile.ReportExtension: true, DealCadenceExtension: true, "composed/v1": true,
}

// readerResult is the plugin's `capsulectl deal verify --bundle` result for
// the copy at path (the core's verify with the deal reader), with the witness
// directory at dir when one is given, and its page gate, in the goldens'
// form: what the seam adds beside rc47's output (read_by and claims on an
// entry the reader supplied, reader_claims, and the reader's witness claim
// reported as reader_witnesses beside the core's) is folded back the way
// rc47 reported it (readerAsRC47).
func readerResult(t *testing.T, path, dir string) dealVerifyResult {
	t.Helper()
	report, _ := verifyWithDirectory(t, path, dir)
	report = readerAsRC47(report).(map[string]any)
	verdict, _ := report["verdict"].(string)
	r := dealVerifyResult{Verdict: verdict, PageGateNoDirectory: "pass", Extensions: []map[string]any{}}
	value, err := cli.DecodeBundleJSON(mustRead(t, path))
	require.NoError(t, err)
	if err := dealCopyPageGate(value); err != nil {
		r.PageGateNoDirectory = "refused: " + err.Error()
	}
	exts, _ := report["extensions"].([]any)
	for _, raw := range exts {
		e, _ := raw.(map[string]any)
		if kind, _ := e["kind"].(string); dealVerifyKinds[kind] {
			r.Extensions = append(r.Extensions, e)
		}
	}
	sort.SliceStable(r.Extensions, func(i, j int) bool { return r.Extensions[i]["kind"].(string) < r.Extensions[j]["kind"].(string) })
	r.Witnesses, _ = report["witnesses"].(map[string]any)
	return r
}

// readerAsRC47 folds a report the seam wrote back to rc47's form, at every
// depth (a composed member's, a sale thread's and a History deal's report
// too): an entry's read_by and claims and a report's reader_claims are
// dropped, and a report's reader_witnesses, less its read_by, takes the place
// of its witnesses, as rc47's cadence claim replaced the direct one.
func readerAsRC47(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			switch k {
			case "read_by", "reader_claims", "reader_witnesses":
				continue
			case "claims":
				if _, isEntry := x["kind"]; isEntry {
					continue
				}
			}
			out[k] = readerAsRC47(val)
		}
		if w, ok := x["reader_witnesses"].(map[string]any); ok {
			folded := readerAsRC47(w).(map[string]any)
			out["witnesses"] = folded
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = readerAsRC47(val)
		}
		return out
	}
	return v
}

// gateOutcome is a page gate's result less its reason: "pass", or "refused:
// <verdict>". rc47's refusal gave a fixed reason in parentheses; rc50's names
// the failing claim there (capsule-cli's wording, not a verdict).
func gateOutcome(s string) string {
	if s == "pass" {
		return s
	}
	for _, v := range []string{"INVALID", "INCOMPLETE"} {
		if strings.Contains(s, "calls this bundle "+v) {
			return "refused: " + v
		}
	}
	return s
}

// dealVerifyExpectedDifference is the one copy whose result differs from
// rc47's, by the seam's design (a reader adds and never weakens; plan
// revision 2 on evidencebook#735, accepted by Sherlock2): a valid cadence
// chain beside a failing direct receipt. rc47 let the cadence claim replace
// the direct one and read VALID; the core now keeps its own failing claim
// beside the reader's, so the copy reads INVALID and its page is refused.
const dealVerifyExpectedDifference = "v0-cadence-with-failing-direct-receipt"

// The plugin's reader gives rc47's results for the goldens' copies, but for
// the one expected difference.
func TestDealVerifyReaderMatchesRC47Goldens(t *testing.T) {
	require.NotEqual(t, "1", os.Getenv("EB_WRITE_DEAL_VERIFY_GOLDENS"), "the goldens are capsule-cli rc47's; its live oracle went with the pin bump (evidencebook#735), so they are not written again")
	matches, err := filepath.Glob(filepath.Join(dealVerifyDir, "*.rc47.json"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)
	for _, golden := range matches {
		name := strings.TrimSuffix(filepath.Base(golden), ".rc47.json")
		t.Run(name, func(t *testing.T) {
			var want dealVerifyResult
			require.NoError(t, json.Unmarshal(mustRead(t, golden), &want))
			dir := filepath.Join(dealVerifyDir, name+".witnesses.json")
			if _, err := os.Stat(dir); err != nil {
				dir = ""
			}
			got := readerResult(t, filepath.Join(dealVerifyDir, name+".json"), dir)
			b, err := json.Marshal(got)
			require.NoError(t, err)
			var norm dealVerifyResult
			require.NoError(t, json.Unmarshal(b, &norm))
			if name == dealVerifyExpectedDifference {
				require.Equal(t, "VALID", want.Verdict, "rc47 read it VALID")
				require.Equal(t, "INVALID", norm.Verdict, "the failing direct receipt stays visible")
				report, _ := verifyWithDirectory(t, filepath.Join(dealVerifyDir, name+".json"), dir)
				require.Equal(t, "fail", report["witnesses"].(map[string]any)["status"], "the core's own claim is the failing direct receipt")
				require.Equal(t, "pass", report["reader_witnesses"].(map[string]any)["status"], "the cadence chain still passes")
				norm.Verdict = want.Verdict
			}
			// rc50's page gate names the failing claim where rc47's gave a
			// fixed reason; the gate's outcome and verdict are compared.
			want.PageGateNoDirectory, norm.PageGateNoDirectory = gateOutcome(want.PageGateNoDirectory), gateOutcome(norm.PageGateNoDirectory)
			require.Equal(t, want, norm)
		})
	}
}
