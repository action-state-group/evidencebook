package book

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/capsulectl/internal/deal/assets"
	"github.com/action-state-group/evidencebook/report"
)

// receiptEntry is one entry of the --receipts file: the exchange capsule a
// node served the receipt by, and either the receipt bundle or held:true when
// the holder's share policy keeps it off the page. The bundle is the JSON the
// node served, written in the file as an object (kept byte for byte, the
// whitespace between its members included) or as a JSON string holding that
// text.
type receiptEntry struct {
	Exchange string          `json:"exchange_capsule_id"`
	Receipt  json.RawMessage `json:"receipt"`
	Held     bool            `json:"held"`
}

// readReceipts reads --receipts, a JSON list of receiptEntry, into the
// report's Receipts with the deal view as the module that opens them. An empty
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
	const shape = "--receipts must be a JSON list of {\"exchange_capsule_id\", \"receipt\" | \"held\": true}: "
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
		// An entry decides whether a receipt is carried or held, so a field
		// written twice (or twice by case, which Go's decoder folds) is
		// refused rather than read as its last value.
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
		deals[i] = report.Receipt{Exchange: e.Exchange, Held: e.Held}
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
	module, err := assets.Module()
	if err != nil {
		return nil, err
	}
	return &report.Receipts{Module: module, Deals: deals}, nil
}

// entryKeysOnce refuses a JSON object that is not an object or names a key
// twice, ignoring case.
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
		folded := strings.ToLower(key)
		if seen[folded] {
			return errors.New("the key " + strconv.Quote(key) + " is written twice")
		}
		seen[folded] = true
		var skip json.RawMessage
		if err := d.Decode(&skip); err != nil {
			return err
		}
	}
	return nil
}
