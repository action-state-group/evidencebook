package deal

import (
	"encoding/json"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/action-state-group/capsule-viewer/go/pageruntime"
)

// The composer's both-halves page (evidencebook-deal#31, option b). A
// composed/v1 bundle carries the copy the composer was served and the copy it
// cut for the other party; the composer's own keep copy (item, amounts and
// story held back from a shared copy) is never a member. On the composer's
// own page only, `deal compose --html --keep` joins that keep bundle to the
// composition at the page: it rides in the bootstrap beside the bundle, the
// way the countersign rung does, so the composed bundle, its digest and
// anything shared are unchanged. The join states stay what the served
// members derive; the private half is shown, never counted.

// dealPrivateHalf is the keep copy as the page shows it: what the composer
// asked (only when its words recompute to their sealed commitment) and what
// the agent did. Nothing here is a bundle member.
type dealPrivateHalf struct {
	Label  string   `json:"label"`
	Note   string   `json:"note"`
	DealID string   `json:"deal_id"`
	Asked  string   `json:"asked,omitempty"`
	Did    []string `json:"did"`
}

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

// privateHalfOf reads the keep bundle at path and joins it to the
// composition: it must verify VALID, be the composer's own copy (audience
// keep), and name the same deal id and the same composer (the key that
// sealed the composition) as the composition, or the page is refused.
func privateHalfOf(path, dealID string, composition map[string]interface{}, w cli.Wording) (*dealPrivateHalf, error) {
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
	if id, _ := report[dealIDMember].(string); id != dealID {
		return nil, cli.InputError(fmt.Sprintf("--keep is a copy of deal %q, not of the composed deal %q; the page is not written", id, dealID))
	}
	if keepKey, composer := bundleRootKey(keep), bundleRootKey(composition); keepKey == "" || keepKey != composer {
		return nil, cli.InputError("--keep was not sealed by the composer of this composition; the page is not written")
	}
	return privateHalfFrom(keep, dealID, w)
}

// privateHalfFrom is the half a verified keep bundle makes on the page.
func privateHalfFrom(keep map[string]interface{}, dealID string, w cli.Wording) (*dealPrivateHalf, error) {
	report, _, _ := cli.DealSealedReport(keep)
	openings, err := dealPageOpenings(keep)
	if err != nil {
		return nil, err
	}
	half := &dealPrivateHalf{Label: w.Line("report.private_half", nil), Note: w.Line("report.private_half_note", nil), DealID: dealID, Did: []string{}}
	if openings["asked"] == true {
		opening, _ := report["asked_opening"].(map[string]interface{})
		half.Asked, _ = opening["text"].(string)
	}
	did, _ := report["did"].([]interface{})
	for _, raw := range did {
		if step, ok := raw.(map[string]interface{}); ok {
			if text, _ := step["text"].(string); text != "" {
				half.Did = append(half.Did, text)
			}
		}
	}
	return half, nil
}

// composedPageHTML renders the composed bundle as one local page, with the
// private half beside it when there is one. The page gate is the same as
// every page's: the composition must verify VALID.
func composedPageHTML(composition map[string]interface{}, half *dealPrivateHalf, verifyCommand string, w cli.Wording) (string, error) {
	if err := cli.PageGate(composition); err != nil {
		return "", err
	}
	boot := map[string]interface{}{"verify_command": verifyCommand}
	if half != nil {
		boot["private_half"] = half
	}
	data, err := json.Marshal(boot)
	if err != nil {
		return "", err
	}
	bootstrap := `globalThis.capsulectlDealPage = Object.freeze(` + string(data) + `);
renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"), undefined, { verifyCommand: capsulectlDealPage.verify_command });
(function () {
  var half = capsulectlDealPage.private_half;
  if (!half) { return; }
  var el = function (tag, text) { var e = document.createElement(tag); if (text) { e.textContent = text; } return e; };
  var section = el("section");
  section.setAttribute("data-private-half", "keep");
  section.style.margin = "1.5rem auto";
  section.style.maxWidth = "60rem";
  section.style.padding = "0 1rem";
  section.appendChild(el("h2", half.label));
  section.appendChild(el("p", half.note));
  if (half.asked) { section.appendChild(el("p", half.asked)); }
  var list = el("ul");
  half.did.forEach(function (line) { list.appendChild(el("li", line)); });
  section.appendChild(list);
  document.getElementById("app").after(section);
})();`
	return emitter.EmitEvidenceGraphHTMLWithOptions(composition, pageruntime.IIFE, emitter.Options{
		Title:             w.Line("report.both_halves_title", nil),
		ThemeCSS:          dealThemeCSS,
		CoreRuntimeSHA256: pageruntime.SHA256,
		Bootstrap:         bootstrap,
	})
}
