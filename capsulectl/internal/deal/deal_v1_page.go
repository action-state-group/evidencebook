package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/action-state-group/capsule-viewer/go/pageruntime"
	"github.com/action-state-group/capsule-viewer/go/receipt"
)

// selfVerifyBinary is the program whose `verify --bundle` the v1 page carries:
// this binary itself, so the page shows the one Go verifier's own report of
// the copy (capsule-viewer#405, route 1). Tests point it at a built plugin.
var selfVerifyBinary = os.Executable

// v1PageVerifyTimeout bounds the page's one `verify --bundle` run.
const v1PageVerifyTimeout = 2 * time.Minute

// v1ReportPage is the receipt page of an x-deal-v1 deal's own copy: the
// bundle `deal report --bundle` writes, drawn by capsule-viewer's x-deal-v1
// receipt module, with this binary's own `verify --bundle` report of that
// bundle carried beside it (`verifierReport`). No page can run the x-deal-v1
// report rule itself, so the page shows what capsulectl reported and says so
// ("capsulectl verify reported its signature check as: pass. This page did
// not check the signature itself."); a report that fails refuses the copy on
// the page, and the page draws nothing capsulectl did not report.
func v1ReportPage(ctx context.Context, bundle []byte, verifyCommand string) ([]byte, error) {
	report, err := selfVerifyReport(ctx, bundle)
	if err != nil {
		return nil, err
	}
	// Numbers stay json.Number: the emitter canonicalizes the bundle (JCS).
	var b map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(bundle))
	dec.UseNumber()
	if err := dec.Decode(&b); err != nil {
		return nil, err
	}
	module, err := receipt.Module(receipt.Deal)
	if err != nil {
		return nil, err
	}
	boot := map[string]any{"verifier_report": json.RawMessage(report), "verify_command": verifyCommand}
	// json.Marshal escapes <, > and &, so the data cannot close its element.
	data, err := json.Marshal(boot)
	if err != nil {
		return nil, err
	}
	bootstrap := `globalThis.capsulectlDealPage = Object.freeze(` + string(data) + `);
renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"), undefined, { verifierReport: capsulectlDealPage.verifier_report, verifyCommand: capsulectlDealPage.verify_command });`
	page, err := emitter.EmitEvidenceGraphHTMLWithOptions(b, pageruntime.IIFE, emitter.Options{
		Title:             "Deal report",
		CoreRuntimeSHA256: pageruntime.SHA256,
		Modules:           []emitter.Module{module},
		Bootstrap:         bootstrap,
	})
	if err != nil {
		return nil, err
	}
	return []byte(page), nil
}

// selfVerifyReport runs this binary's `verify --bundle` on bundle and returns
// its JSON report (capsule-cli-result/v1). verify exits non-zero for a
// verdict other than VALID; that report is still the one to carry, so only
// output that is not such a report is an error.
func selfVerifyReport(ctx context.Context, bundle []byte) ([]byte, error) {
	exe, err := selfVerifyBinary()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "capsulectl-deal-page-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(path, bundle, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, v1PageVerifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "verify", "--bundle", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var probe struct {
		SpecVersion string `json:"spec_version"`
	}
	if json.Unmarshal(stdout.Bytes(), &probe) != nil || probe.SpecVersion != "capsule-cli-result/v1" {
		var exitErr *exec.ExitError
		if runErr != nil && !errors.As(runErr, &exitErr) {
			return nil, fmt.Errorf("verify --bundle for the page: %w", runErr)
		}
		return nil, fmt.Errorf("verify --bundle for the page gave no report: %s", bytes.TrimSpace(stderr.Bytes()))
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}
