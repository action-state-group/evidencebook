package deal

import (
	"encoding/json"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/action-state-group/capsule-viewer/go/pageruntime"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// The composer's receipt with the other party's half (evidencebook-deal#31,
// #79; docs/design/two-sided-receipt.md). A composed/v1 bundle carries the
// answers the composer recorded; the composer's own keep copy (item, amounts
// and story held back from a shared copy) is never a member. On the
// composer's own page only, `deal compose --html --keep` carries that keep
// bundle in the bootstrap beside the composition, the way the countersign
// rung rides, so the composed bundle, its digest and anything shared are
// unchanged. The deal view's composed module (capsulectl.deal-view-composed/v0)
// verifies the keep bundle in the page and reads it as your half.

// bundleRootKey is the key_id of a bundle's root record, or "".
func bundleRootKey(bundle map[string]interface{}) string {
	root, _ := bundle["root"].(string)
	records, _ := bundle["records"].([]interface{})
	for _, raw := range records {
		if record, ok := raw.(map[string]interface{}); ok && record["capsule_id"] == root {
			key, _ := record["key_id"].(string)
			return key
		}
	}
	return ""
}

// keepCopyOf reads the keep bundle at path and joins it to the
// composition: it must verify VALID, be the composer's own copy (audience
// keep), and name the same deal id and the same composer (the key that
// sealed the composition) as the composition, or the page is refused. The
// page checks the same again.
func keepCopyOf(path, dealID string, composition map[string]interface{}) (map[string]interface{}, error) {
	raw, err := cli.ReadBundleInput(path)
	if err != nil {
		return nil, err
	}
	keep, err := cli.DecodeBundleJSON(raw)
	if err != nil {
		return nil, cli.InputError("--keep is not a bundle: " + err.Error())
	}
	if verdict, _ := cli.BundleVerdict(keep, nil); verdict != "VALID" {
		return nil, cli.InputError("--keep: the keep bundle verifies " + verdict + ", not VALID; the page is not written")
	}
	report, _, _ := cli.DealSealedReport(keep)
	if audience, _ := report["audience"].(string); audience != cli.DealAudienceKeep {
		return nil, cli.InputError("--keep must be your own copy (`deal report --bundle`, audience keep); a shared copy is not your private half")
	}
	if id, _ := report[dealprofile.DealIDMember].(string); id != dealID {
		return nil, cli.InputError(fmt.Sprintf("--keep is a copy of deal %q, not of the composed deal %q; the page is not written", id, dealID))
	}
	if keepKey, composer := bundleRootKey(keep), bundleRootKey(composition); keepKey == "" || keepKey != composer {
		return nil, cli.InputError("--keep was not sealed by the composer of this composition; the page is not written")
	}
	return keep, nil
}

// composedPageHTML renders the composed bundle as one local page through the
// deal view's composed module, with the keep copy in the bootstrap when
// there is one, and beside it what the deal view reads from the bootstrap
// for that copy and cannot recompute (dealReportHTML's openings,
// countersign and words). The page gate is the same as every page's: the
// composition must verify VALID.
func composedPageHTML(composition, keep map[string]interface{}, verifyCommand string, w cli.Wording) (string, error) {
	if err := cli.PageGate(composition); err != nil {
		return "", err
	}
	return composedPage(composition, keep, verifyCommand, w)
}

// composedPage is the page composedPageHTML writes once the gate passed. The
// deal view checks the composition again in the page, since a viewer that
// opens the bundle itself runs no gate.
func composedPage(composition, keep map[string]interface{}, verifyCommand string, w cli.Wording) (string, error) {
	module, err := dealViewModule()
	if err != nil {
		return "", err
	}
	boot := map[string]interface{}{"words": dealPageWords(w)}
	if keep != nil {
		if boot, _, err = dealPageBoot(keep, dealNotCountersigned(), w); err != nil {
			return "", err
		}
		boot["keep"] = keep
	}
	boot["verify_command"] = verifyCommand
	// json.Marshal escapes <, > and &, so the data cannot close its element.
	data, err := json.Marshal(boot)
	if err != nil {
		return "", err
	}
	bootstrap := `globalThis.capsulectlDealPage = Object.freeze(` + string(data) + `);
renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"), undefined, { verifyCommand: capsulectlDealPage.verify_command });`
	return emitter.EmitEvidenceGraphHTMLWithOptions(composition, pageruntime.IIFE, emitter.Options{
		Title:             w.Line("report.both_halves_title", nil),
		ThemeCSS:          dealThemeCSS,
		CoreRuntimeSHA256: pageruntime.SHA256,
		Modules:           []emitter.Module{module},
		Bootstrap:         bootstrap,
	})
}
