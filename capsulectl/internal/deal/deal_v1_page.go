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
	"slices"
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
//
// With proof and dirPath (deal report --witness-directory), verify also gets
// the stamp proof and the directory, and its x-deal-v1 entry reports the
// book's witness time (witnessed); the proof stays beside the copy, never in
// it or on the page (Sherlock2).
//
// With countersigned (v1CountersignedCopy), the page also carries the stored
// countersigned copy behind the report's countersign rung and the directory
// rows that named its signers; the page fills its COUNTERSIGNED stamp only
// from its own check of that copy (capsule-viewer#456, evidencebook#1015).
func v1ReportPage(ctx context.Context, bundle []byte, verifyCommand string, proof *entryProof, dirPath string, countersigned *v1PageCountersignedCopy) ([]byte, error) {
	report, err := selfVerifyReport(ctx, bundle, proof, dirPath)
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
	if countersigned != nil {
		boot["countersigned_copy"] = countersigned
	}
	// json.Marshal escapes <, > and &, so the data cannot close its element.
	data, err := json.Marshal(boot)
	if err != nil {
		return nil, err
	}
	bootstrap := `globalThis.capsulectlDealPage = Object.freeze(` + string(data) + `);
renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"), undefined, { verifierReport: capsulectlDealPage.verifier_report, verifyCommand: capsulectlDealPage.verify_command, countersignedCopy: capsulectlDealPage.countersigned_copy });`
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

// v1PageCountersignedCopy is what a v1 receipt page carries for its own
// check of the deal's countersign rung: the stored countersigned bundle, as
// stored, and the countersigner rows ({name, key_ids}, the shape of
// capsule-cli's directory rows and of agent-action-capsule's
// CountersignerSource) that named its signers.
type v1PageCountersignedCopy struct {
	Bundle         json.RawMessage          `json:"bundle"`
	Countersigners []v1PageCountersignerRow `json:"countersigners"`
}

type v1PageCountersignerRow struct {
	Name   string   `json:"name"`
	KeyIDs []string `json:"key_ids"`
}

// v1CountersignedCopy is the copy a page carries for stamp, or nil when it
// has no rung: the stored bundle of the entry that decides the rung
// (storedCountersignOf), with a row for each signer resolved on that bundle,
// named as the profile's countersigner directory names it.
func v1CountersignedCopy(stamp dealGradeCountersign) *v1PageCountersignedCopy {
	if stamp.Rung == "" || stamp.copy == nil {
		return nil
	}
	var rows []v1PageCountersignerRow
	at := map[string]int{}
	for _, e := range stamp.Entries {
		if e.State != "resolved" || e.BundleDigest != stamp.copyDigest {
			continue
		}
		i, ok := at[e.Signer]
		if !ok {
			i = len(rows)
			at[e.Signer] = i
			rows = append(rows, v1PageCountersignerRow{Name: e.Signer})
		}
		if !slices.Contains(rows[i].KeyIDs, e.KeyID) {
			rows[i].KeyIDs = append(rows[i].KeyIDs, e.KeyID)
		}
	}
	return &v1PageCountersignedCopy{Bundle: stamp.copy, Countersigners: rows}
}

// selfVerifyReport runs this binary's `verify --bundle` on bundle and returns
// its JSON report (capsule-cli-result/v1). verify exits non-zero for a
// verdict other than VALID; that report is still the one to carry, so only
// output that is not such a report is an error.
func selfVerifyReport(ctx context.Context, bundle []byte, proof *entryProof, dirPath string) ([]byte, error) {
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
	args := []string{"verify", "--bundle", path}
	if proof != nil && dirPath != "" {
		raw, err := json.Marshal(proof)
		if err != nil {
			return nil, err
		}
		proofPath := filepath.Join(dir, "proof.json")
		if err := os.WriteFile(proofPath, raw, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "--"+stampProofFlag, proofPath, "--witness-directory", dirPath)
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("verify --bundle for the page: %w", ctx.Err())
	}
	// A report has a verdict and the bundle's digest; capsulectl's error
	// envelope shares the spec_version but has neither, and is no report.
	var probe struct {
		SpecVersion  string          `json:"spec_version"`
		Verdict      string          `json:"verdict"`
		BundleDigest string          `json:"bundle_digest"`
		Error        json.RawMessage `json:"error"`
	}
	if json.Unmarshal(stdout.Bytes(), &probe) != nil || probe.SpecVersion != "capsule-cli-result/v1" ||
		probe.Verdict == "" || probe.BundleDigest == "" || probe.Error != nil {
		var exitErr *exec.ExitError
		if runErr != nil && !errors.As(runErr, &exitErr) {
			return nil, fmt.Errorf("verify --bundle for the page: %w", runErr)
		}
		return nil, fmt.Errorf("verify --bundle for the page gave no report: %s", bytes.TrimSpace(stderr.Bytes()))
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}
