package book

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/action-state-group/capsule-viewer/go/receipt"
	"github.com/action-state-group/evidencebook/report"
)

// receiptEntry is one entry of the --receipts file: the exchange capsule a
// node served the receipt by, and either the receipt bundle, held:true when
// the holder's share policy keeps it off the page, or not_closed:true when the
// deal had not closed when the page was built. The bundle is the JSON the
// node served, written in the file as an object (kept byte for byte, the
// whitespace between its members included) or as a JSON string holding that
// text.
type receiptEntry struct {
	Exchange string          `json:"exchange_capsule_id"`
	Receipt  json.RawMessage `json:"receipt"`
	Held     bool            `json:"held"`
	// NotClosed says the deal had not closed when the page was built, so it
	// has no receipt yet.
	NotClosed bool `json:"not_closed"`
}

// readReceipts reads --receipts, a JSON list of receiptEntry, into the
// report's Receipts with capsule-viewer's receipt modules as the modules that open them. An empty
// path carries nothing (nil: the page is the page built without the flag). The
// builder never decides what the share policy allows: the caller's file does,
// and report.Build maps each entry to the deal whose claims cite its exchange
// capsule.
func readReceipts(path string) (*report.Receipts, error) {
	if path == "" {
		return nil, nil
	}
	// The file holds whole receipt bundles, so it is read with the bundle cap.
	raw, err := cli.ReadBundleInput(path)
	if err != nil {
		return nil, err
	}
	const shape = "--receipts must be a JSON list of {\"exchange_capsule_id\", \"receipt\" | \"held\": true | \"not_closed\": true}: "
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return nil, cli.InputError(shape + "it is not a list")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var items []json.RawMessage
	if err := decoder.Decode(&items); err != nil {
		return nil, cli.InputError(shape + err.Error())
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, cli.InputError("--receipts must contain one JSON list")
	}
	deals := make([]report.Receipt, len(items))
	for i, item := range items {
		// An entry decides whether a receipt is carried, held or not closed, so a field
		// written twice, or under a spelling Go's decoder would fold onto a
		// field, is refused rather than read as its last value.
		if err := entryKeysOnce(item); err != nil {
			return nil, cli.InputError(shape + "entry " + strconv.Itoa(i) + ": " + err.Error())
		}
		var e receiptEntry
		d := json.NewDecoder(bytes.NewReader(item))
		d.DisallowUnknownFields()
		if err := d.Decode(&e); err != nil {
			return nil, cli.InputError(shape + "entry " + strconv.Itoa(i) + ": " + err.Error())
		}
		if e.Exchange == "" {
			return nil, cli.InputError("--receipts: an entry has no exchange_capsule_id")
		}
		deals[i] = report.Receipt{Exchange: e.Exchange, Held: e.Held, NotClosed: e.NotClosed}
		if len(e.Receipt) == 0 || bytes.Equal(e.Receipt, []byte("null")) {
			continue
		}
		var text string
		if json.Unmarshal(e.Receipt, &text) == nil {
			deals[i].Bundle = text
		} else {
			deals[i].Bundle = string(e.Receipt)
		}
	}
	// The page opens each receipt with capsule-viewer's receipt modules, the
	// one receipt renderer (capsule-viewer#383): an owner's or counterparty's
	// copy with the unilateral module, a seller's with the seller module.
	var modules []emitter.Module
	for _, kind := range []string{receipt.Unilateral, receipt.Seller} {
		module, err := receipt.Module(kind)
		if err != nil {
			return nil, err
		}
		modules = append(modules, module)
	}
	return &report.Receipts{Modules: modules, Deals: deals, Gate: dealVerify}, nil
}

// receiptVerifyTimeout bounds one receipt's `capsulectl deal verify`.
const receiptVerifyTimeout = 2 * time.Minute

// dealVerify is each carried receipt's own check: the deal plugin's
// `capsulectl deal verify --bundle` must call it VALID (evidencebook#735),
// or INCOMPLETE only as a producer selection (producerSelection).
// capsule-cli's page gate reads no deal section since rc50 (#609), and this
// plugin links no deal code, so it calls the deal plugin through the command
// line, by the capsulectl that launched it (CAPSULECTL_BIN, else capsulectl
// on PATH). Only --receipts needs the deal plugin installed.
func dealVerify(bundle []byte) error {
	dir, err := os.MkdirTemp("", "capsulectl-book-receipt-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(path, bundle, 0o600); err != nil {
		return err
	}
	bin := os.Getenv("CAPSULECTL_BIN")
	if bin == "" {
		bin = "capsulectl"
	}
	// A verify that hangs must not hang the page build.
	ctx, cancel := context.WithTimeout(context.Background(), receiptVerifyTimeout)
	defer cancel()
	out, runErr := exec.CommandContext(ctx, bin, "deal", "verify", "--bundle", path).Output()
	var result dealVerifyResult
	if json.Unmarshal(out, &result) != nil || result.Verdict == "" {
		why := "it printed no verdict"
		if runErr != nil {
			why = runErr.Error()
		}
		return cli.InputError("capsulectl deal verify --bundle could not check the receipt (" + why + "): --receipts needs the deal plugin installed")
	}
	if result.Verdict != "VALID" && !result.producerSelection() {
		var findings []string
		for _, c := range result.ReaderClaims {
			if c.Status == "fail" {
				findings = append(findings, c.Findings...)
			}
		}
		why := ""
		if len(findings) > 0 {
			why = " (" + strings.Join(findings, ", ") + ")"
		}
		return cli.InputError("capsulectl deal verify --bundle calls it " + strings.ToUpper(result.Verdict) + why + "; check it with that command")
	}
	return nil
}

// dealVerifyResult is the part of `capsulectl deal verify --bundle`'s report
// dealVerify reads.
type dealVerifyResult struct {
	Verdict      string `json:"verdict"`
	ReaderClaims []struct {
		Status   string   `json:"status"`
		Findings []string `json:"findings"`
	} `json:"reader_claims"`
	GraphClosure        claimStatus           `json:"graph_closure"`
	IntervalCoverage    claimStatus           `json:"interval_coverage"`
	PerRecordMembership claimStatus           `json:"per_record_membership"`
	Checkpoint          claimStatus           `json:"checkpoint"`
	ProducerSignatures  claimStatus           `json:"producer_signatures"`
	Extensions          []dealVerifyExtension `json:"extensions"`
}

// dealVerifyExtension is one entry of deal verify's extensions.
type dealVerifyExtension struct {
	Kind     string          `json:"kind"`
	Status   string          `json:"status"`
	Findings []string        `json:"findings"`
	Composed json.RawMessage `json:"composed"`
}

type claimStatus struct {
	Status   string   `json:"status"`
	Findings []string `json:"findings"`
}

// producerSelection says whether an INCOMPLETE verdict is a producer
// selection and nothing else: interval coverage withheld only as
// selection_not_checkable, every other claim passing, every reader claim
// passing with no finding, the deal section read and passing with no
// finding, every other extension passing or uninterpreted with no finding,
// and no composed member (whose own claims could read INCOMPLETE). A Mesh node's receipt is one by design (the log is not
// shown whole); the report admits it on the page's own verdict
// (report.receiptPageGate, evidencebook#870), and this is the deal plugin's
// half of the same narrow exception. Any other INCOMPLETE stays refused.
func (r dealVerifyResult) producerSelection() bool {
	if r.Verdict != "INCOMPLETE" {
		return false
	}
	c := r.IntervalCoverage
	if c.Status != "withheld" || len(c.Findings) != 1 || c.Findings[0] != "selection_not_checkable" {
		return false
	}
	for _, claim := range []claimStatus{r.GraphClosure, r.PerRecordMembership, r.Checkpoint, r.ProducerSignatures} {
		if claim.Status != "pass" {
			return false
		}
	}
	for _, c := range r.ReaderClaims {
		if c.Status != "pass" || len(c.Findings) > 0 {
			return false
		}
	}
	deal := false
	for _, e := range r.Extensions {
		if len(e.Findings) > 0 || len(e.Composed) > 0 {
			return false
		}
		switch {
		case e.Kind == "x-deal-v0" || e.Kind == "x-deal-v1":
			if e.Status != "pass" {
				return false
			}
			deal = true
		case e.Status != "pass" && e.Status != "uninterpreted":
			return false
		}
	}
	return deal
}

// entryKeysOnce refuses a JSON object that is not an object, names a key
// other than the four an entry has (spelled exactly: Go's decoder would fold
// "Held" or a Unicode look-alike of a letter onto a field, and read the last
// of two such keys) or names one of them twice.
func entryKeysOnce(item json.RawMessage) error {
	d := json.NewDecoder(bytes.NewReader(item))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return errors.New("it is not an object")
	}
	seen := make(map[string]bool)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		key, _ := token.(string)
		switch key {
		case "exchange_capsule_id", "receipt", "held", "not_closed":
		default:
			return errors.New("the key " + strconv.Quote(key) + " is not one of exchange_capsule_id, receipt, held and not_closed, spelled exactly")
		}
		if seen[key] {
			return errors.New("the key " + strconv.Quote(key) + " is written twice")
		}
		seen[key] = true
		var skip json.RawMessage
		if err := d.Decode(&skip); err != nil {
			return err
		}
	}
	return nil
}
