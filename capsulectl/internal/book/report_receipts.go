package book

import (
	"bytes"
	"encoding/json"
	"io"

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
	raw, err := cli.ReadInput(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var entries []receiptEntry
	if err := decoder.Decode(&entries); err != nil {
		return nil, cli.InputError("--receipts must be a JSON list of {\"exchange_capsule_id\", \"receipt\" | \"held\": true}: " + err.Error())
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, cli.InputError("--receipts must contain one JSON list")
	}
	deals := make([]report.Receipt, len(entries))
	for i, e := range entries {
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
