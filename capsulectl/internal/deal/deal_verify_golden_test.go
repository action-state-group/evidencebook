package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"os"
	"os/exec"
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
// PR 2 compares the plugin's reader with these goldens, unchanged; the one
// expected difference is named there.
//
// EB_WRITE_DEAL_VERIFY_GOLDENS=1 builds the copies and writes both files; it
// must run on capsule-cli rc47 with the plugin's v1 report rule registered,
// as `capsulectl verify --bundle` runs it.

const dealVerifyDir = "testdata/deal-verify"

// dealVerifyResult is what a golden records of one copy: the verdict, the
// page gate, the extension entries of the kinds the plugin will read or
// recurse through (composed/v1 included, with each member's own report), and
// the witness entry. PageGateNoDirectory is cli.PageGate's result, which
// takes no witness directory: a copy can read INVALID under its directory
// and still pass it. rc47 exports no page verdict string, so the gate's
// result is what stands for it.
type dealVerifyResult struct {
	Verdict             string           `json:"verdict"`
	PageGateNoDirectory string           `json:"page_gate_no_directory"`
	Extensions          []map[string]any `json:"extensions"`
	Witnesses           map[string]any   `json:"witnesses"`
}

// dealVerifyKinds are the extension kinds the plugin will read.
var dealVerifyKinds = map[string]bool{
	dealprofile.Profile: true, "x-deal-v1": true, cli.DealSaleExtension: true, cli.HistorySelectionExtension: true,
	dealprofile.ReportExtension: true, cli.DealCadenceExtension: true, "composed/v1": true,
}

// rc47Result is rc47's `capsulectl verify --bundle` result for the copy at
// path, with the witness directory at dir when one is given, and its page
// gate.
func rc47Result(t *testing.T, path, dir string) dealVerifyResult {
	t.Helper()
	report, _ := verifyWithDirectory(t, path, dir)
	verdict, _ := report["verdict"].(string)
	r := dealVerifyResult{Verdict: verdict, PageGateNoDirectory: "pass", Extensions: []map[string]any{}}
	value, err := cli.DecodeBundleJSON(mustRead(t, path))
	require.NoError(t, err)
	if err := cli.PageGate(value); err != nil {
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

// mutate returns raw with f applied to its decoded value.
func mutate(t *testing.T, raw []byte, f func(map[string]any)) []byte {
	t.Helper()
	var v map[string]any
	require.NoError(t, json.Unmarshal(raw, &v))
	f(v)
	out, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return out
}

func extensionsOf(v map[string]any) map[string]any { return v["extensions"].(map[string]any) }

// dealVerifyCopy is one copy the goldens cover: its bytes, and the witness
// directory it is verified under, if any.
type dealVerifyCopy struct {
	raw []byte
	dir []byte
}

// dropOne deletes the first key, in order, of the map at m[key].
func dropOne(m map[string]any, key string) {
	inner, _ := m[key].(map[string]any)
	keys := make([]string, 0, len(inner))
	for k := range inner {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 1 {
		delete(inner, keys[0])
	}
}

// flipHex flips one digit of the first 64-hex string in v (JSON-encoded).
func flipHex(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	s := string(b)
	for i := 0; i+64 <= len(s); i++ {
		if isHex64(s[i : i+64]) {
			c := "0"
			if s[i] == '0' {
				c = "1"
			}
			s = s[:i] + c + s[i+1:]
			break
		}
	}
	var back any
	require.NoError(t, json.Unmarshal([]byte(s), &back))
	return back
}

// buildDealVerifyCopies builds every copy the goldens cover, by name.
func buildDealVerifyCopies(t *testing.T) map[string]dealVerifyCopy {
	t.Helper()
	copies := map[string]dealVerifyCopy{}
	plain := func(raw []byte) dealVerifyCopy { return dealVerifyCopy{raw: raw} }

	// x-deal-v0 under a witnessed cadence: a deal's own bundle and its
	// shared copies, verified under the witness's directory.
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	id := retailDeal(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	dirRaw, err := json.Marshal(map[string]any{"directory_version": "1", "witnesses": []any{rawKeyRow(endpoint, public)}})
	require.NoError(t, err)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	otherDir, err := json.Marshal(map[string]any{"directory_version": "1", "witnesses": []any{rawKeyRow(endpoint, other)}})
	require.NoError(t, err)
	witnessed := func(raw []byte) dealVerifyCopy { return dealVerifyCopy{raw: raw, dir: dirRaw} }

	own := []byte(dealOwnBundle(t, id))
	copies["v0-own"] = witnessed(own)
	_, cp := sharedCopy(t, id, cli.DealAudienceCounterparty, "the shop")
	copies["v0-counterparty"] = witnessed([]byte(cp))
	copies["v0-counterparty-no-directory"] = plain([]byte(cp))
	copies["v0-counterparty-untrusted-witness"] = dealVerifyCopy{raw: []byte(cp), dir: otherDir}
	_, adj := sharedCopy(t, id, cli.DealAudienceAdjudicator, "Lamp Court")
	copies["v0-adjudicator"] = witnessed([]byte(adj))
	copies["v0-no-section"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) { delete(extensionsOf(v), dealprofile.Profile) }))
	copies["v0-no-report-carrier"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) { delete(extensionsOf(v), dealprofile.ReportExtension) }))
	copies["v0-report-only"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) {
		delete(extensionsOf(v), dealprofile.Profile)
		delete(extensionsOf(v), cli.DealCadenceExtension)
	}))
	copies["v0-no-cadence"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) { delete(extensionsOf(v), cli.DealCadenceExtension) }))
	copies["v0-cadence-path-tampered"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) {
		chain := extensionsOf(v)[cli.DealCadenceExtension].(map[string]any)
		chain["path"] = flipHex(t, chain["path"])
	}))
	// A valid cadence chain beside a failing direct receipt: the chain's
	// receipts carried on the bundle's own checkpoint, which they are not
	// over. rc47 lets the cadence claim replace the direct one; PR 2 names
	// the difference.
	copies["v0-cadence-with-failing-direct-receipt"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) {
		chain := extensionsOf(v)[cli.DealCadenceExtension].(map[string]any)
		cpv, _ := v["checkpoint"].(map[string]any)
		cadence, _ := chain["cadence"].(map[string]any)
		receipts, ok := cadence["witnesses"]
		require.True(t, ok && cpv != nil, "the cadence checkpoint carries its receipts")
		cpv["witnesses"] = receipts
	}))

	// A History selection over two deals.
	second := retailDeal(t)
	path, _ := writeHistory(t, []string{id, second}, []string{"--audience", cli.DealAudienceKeep}, id, second)
	history := mustRead(t, path)
	copies["history"] = witnessed(history)
	copies["history-one-deal-dropped"] = witnessed(mutate(t, history, func(v map[string]any) {
		dropOne(extensionsOf(v)[cli.HistorySelectionExtension].(map[string]any), "deals")
	}))

	// A sale and its threads.
	saleID, _ := newSale(t)
	buyerThread(t, saleID, "buyer-a.example")
	buyerThread(t, saleID, "buyer-b.example")
	sale, _ := saleBundle(t, saleID)
	saleRaw, err := json.MarshalIndent(sale, "", "  ")
	require.NoError(t, err)
	copies["sale"] = witnessed(saleRaw)
	copies["sale-thread-dropped"] = witnessed(mutate(t, saleRaw, func(v map[string]any) {
		dropOne(extensionsOf(v)[cli.DealSaleExtension].(map[string]any), "threads")
	}))

	// The failing direct receipt on its own (no cadence chain), which shows
	// the receipt in the case above does fail.
	copies["v0-failing-direct-receipt-alone"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) {
		chain := extensionsOf(v)[cli.DealCadenceExtension].(map[string]any)
		cadence, _ := chain["cadence"].(map[string]any)
		v["checkpoint"].(map[string]any)["witnesses"] = cadence["witnesses"]
		delete(extensionsOf(v), cli.DealCadenceExtension)
	}))

	// A composed page of two parties' answers (the buyer's own and the
	// seller's copy for the buyer): its golden holds each member's own
	// report, deal entries included, which a reader that is not passed into
	// the members cannot reproduce. The same page with a member's report
	// pointer changed breaks the member's binding (its digest), so the core
	// refuses it whatever the members' deal checks say: a binding failure,
	// not a nested deal failure.
	dealFixture(t)
	cid := dealRun(t, "open", "--input", writeJSON(t, crossPartyBuyerOpen))["deal_id"].(string)
	buyer := thisDealParty(t)
	dealFixture(t)
	dealRun(t, "open", "--records", "typed", "--deal-id", cid, "--input", writeJSON(t, sellerTyped))
	dealRun(t, "check", "--deal", cid, "--input", writeJSON(t,
		`{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`))
	seller := thisDealParty(t)
	buyerOwn, _ := pinnedCopy(t, buyer, buyer, cid)
	sellerCopyForBuyer, _ := pinnedCopy(t, buyer, seller, cid)
	party(t, buyer.config)
	composedPath, err := composeMembers(t, cid, buyerOwn, sellerCopyForBuyer)
	require.NoError(t, err, composedPath)
	composed := mustRead(t, composedPath)
	copies["composed"] = plain(composed)
	copies["composed-member-binding-broken"] = plain(mutate(t, composed, func(v map[string]any) {
		b, _ := json.Marshal(v)
		s := strings.Replace(string(b), `"sealed_report":"`, `"sealed_report":"0`, 1)
		var back map[string]any
		require.NoError(t, json.Unmarshal([]byte(s), &back))
		for k := range v {
			delete(v, k)
		}
		for k, x := range back {
			v[k] = x
		}
	}))

	// capsule-cli's own served-refusal sample (cli/testdata at rc47): a
	// composed page whose top-level refusal is served.
	sample, err := os.ReadFile(filepath.Join(rc47Dir(t), "testdata", "composed-refusal-sample.json"))
	require.NoError(t, err)
	copies["composed-served-refusal"] = plain(sample)
	// The served-refusal variants of capsule-cli's own test
	// (TestComposedRefusalMemberPageByItsSignature at rc47): the refusal
	// edited, and its member and composed digests recomputed, so only its
	// signature state differs.
	refusal := func(edit func(block, refusal map[string]any)) []byte {
		return mutate(t, sample, func(v map[string]any) {
			block := extensionsOf(v)["composed/v1"].(map[string]any)
			member := block["members"].([]any)[0].(map[string]any)
			r := member["refusal"].(map[string]any)
			edit(block, r)
			var err error
			member["digest"], err = canonical.JSONDigest(r)
			require.NoError(t, err)
			block["composed_digest"], err = aacbundle.ComposedDigest(block)
			require.NoError(t, err)
		})
	}
	copies["composed-refusal-unsigned"] = plain(refusal(func(_, r map[string]any) { delete(r, "key_id"); delete(r, "sig") }))
	copies["composed-refusal-signed-by-another-key"] = plain(refusal(func(_, r map[string]any) {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		message, err := canonical.JCS(map[string]any{"request_digest": r["request_digest"], "reason": r["reason"], "issued_at": r["issued_at"]})
		require.NoError(t, err)
		r["key_id"], r["sig"] = hex.EncodeToString(public), hex.EncodeToString(ed25519.Sign(private, message))
	}))
	copies["composed-refusal-beside-an-unsigned-one"] = plain(refusal(func(block, _ map[string]any) {
		member := block["members"].([]any)[1].(map[string]any)
		other := map[string]any{"issued_at": "2026-10-29T12:02:00Z", "reason": "not_authorized", "request_digest": member["request_digest"]}
		delete(member, "absence")
		member["outcome"], member["refusal"] = "refusal", other
		var err error
		member["digest"], err = canonical.JSONDigest(other)
		require.NoError(t, err)
	}))
	// A served refusal beside another failure: the page is refused.
	copies["composed-served-refusal-and-a-broken-checkpoint"] = plain(mutate(t, sample, func(v map[string]any) {
		cpv := v["checkpoint"].(map[string]any)
		cpv["root"] = flipHex(t, cpv["root"])
	}))

	// x-deal-v1: a deal book's bundle, and three refused forms.
	h := agreedBoth(t, oneForum) // its agreement names an adjudicator
	h.buyer.act(t)
	v1 := filepath.Join(t.TempDir(), "v1.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", v1)
	v1Raw := mustRead(t, v1)
	copies["v1-keep"] = plain(v1Raw)
	for _, share := range []struct{ audience, to string }{{cli.DealAudienceCounterparty, "Kettle Shop"}, {cli.DealAudienceAdjudicator, "Lamp Court"}} {
		path := filepath.Join(t.TempDir(), share.audience+".json")
		dealRun(t, "report", "--deal", h.dealID, "--share", share.audience, "--to", share.to, "--bundle", path)
		copies["v1-"+share.audience] = plain(mustRead(t, path))
	}
	// Both deal versions, each a real section: rc47 judges the deal section
	// once, the first kind it lists.
	copies["v1-with-a-v0-section-too"] = plain(mutate(t, v1Raw, func(v map[string]any) {
		var c map[string]any
		require.NoError(t, json.Unmarshal([]byte(cp), &c))
		extensionsOf(v)[dealprofile.Profile] = extensionsOf(c)[dealprofile.Profile]
	}))
	copies["v0-with-a-v1-section-too"] = witnessed(mutate(t, []byte(cp), func(v map[string]any) {
		var c map[string]any
		require.NoError(t, json.Unmarshal(v1Raw, &c))
		extensionsOf(v)["x-deal-v1"] = extensionsOf(c)["x-deal-v1"]
	}))
	copies["v1-no-section"] = plain(mutate(t, v1Raw, func(v map[string]any) { delete(extensionsOf(v), "x-deal-v1") }))
	copies["v1-wrong-sealed-report"] = plain(mutate(t, v1Raw, func(v map[string]any) {
		extensionsOf(v)["x-deal-v1"].(map[string]any)["sealed_report"] = strings.Repeat("0", 64)
	}))
	copies["v1-book-dropped"] = plain(mutate(t, v1Raw, func(v map[string]any) { delete(extensionsOf(v)["x-deal-v1"].(map[string]any), "book") }))
	return copies
}

func isHex64(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return len(s) == 64
}

// The goldens are rc47's results for their copies.
func TestDealVerifyGoldensAreRC47s(t *testing.T) {
	if os.Getenv("EB_WRITE_DEAL_VERIFY_GOLDENS") == "1" {
		copies := buildDealVerifyCopies(t)
		// A copy no longer built leaves no golden behind.
		require.NoError(t, os.RemoveAll(dealVerifyDir))
		require.NoError(t, os.MkdirAll(dealVerifyDir, 0o755))
		for name, c := range copies {
			path := filepath.Join(dealVerifyDir, name+".json")
			require.NoError(t, os.WriteFile(path, c.raw, 0o644))
			dir := ""
			if c.dir != nil {
				dir = filepath.Join(dealVerifyDir, name+".witnesses.json")
				require.NoError(t, os.WriteFile(dir, c.dir, 0o644))
			}
			b, err := json.MarshalIndent(rc47Result(t, path, dir), "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dealVerifyDir, name+".rc47.json"), append(b, '\n'), 0o644))
		}
	}
	matches, err := filepath.Glob(filepath.Join(dealVerifyDir, "*.rc47.json"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "no goldens: run with EB_WRITE_DEAL_VERIFY_GOLDENS=1 on capsule-cli rc47")
	for _, golden := range matches {
		name := strings.TrimSuffix(filepath.Base(golden), ".rc47.json")
		t.Run(name, func(t *testing.T) {
			var want dealVerifyResult
			require.NoError(t, json.Unmarshal(mustRead(t, golden), &want))
			dir := filepath.Join(dealVerifyDir, name+".witnesses.json")
			if _, err := os.Stat(dir); err != nil {
				dir = ""
			}
			got := rc47Result(t, filepath.Join(dealVerifyDir, name+".json"), dir)
			b, err := json.Marshal(got)
			require.NoError(t, err)
			var norm dealVerifyResult
			require.NoError(t, json.Unmarshal(b, &norm))
			require.Equal(t, want, norm)
		})
	}
}

// rc47Dir is capsule-cli's cli package directory in the module cache.
func rc47Dir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/action-state-group/capsule-cli").Output()
	require.NoError(t, err)
	return filepath.Join(strings.TrimSpace(string(out)), "cli")
}
