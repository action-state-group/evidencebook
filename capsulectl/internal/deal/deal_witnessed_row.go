// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// The witnessed member names the directory row of the witness it reports on,
// with that row's independent_of as the directory states it
// (evidencebook#1073, the producer half of capsule-viewer#508; u652): the
// page, not capsulectl, decides from the list whether the witness is
// independent of the parties, as it does for a countersigner's row
// (evidencebook#1019). AAC's witness.Row keeps a row's other members
// private, so verify reads the directory's rows once more and matches them
// with AAC's own RowFor, one row at a time.

// witnessRows is the rows of verify's --witness-directory as decoded, nil
// when none was given or it does not decode.
type witnessRows []interface{}

// snapshotWitnessDirectory reads verify's --witness-directory once, points
// the flag at a private copy of those bytes, which the core's verify then
// loads (and validates) as W, and returns the rows decoded from the same
// bytes, so the row a member names is always the row the receipt was checked
// under, even if W changes meanwhile. cleanup removes the copy. Without the
// flag it does nothing.
func snapshotWitnessDirectory(c *cobra.Command) (rows witnessRows, cleanup func(), err error) {
	cleanup = func() {}
	path, _ := c.Flags().GetString("witness-directory")
	if path == "" {
		return nil, cleanup, nil
	}
	raw, err := cli.ReadInput(path)
	if err != nil {
		return nil, cleanup, err
	}
	dir, err := os.MkdirTemp("", "capsulectl-deal-witnesses-")
	if err != nil {
		return nil, cleanup, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	copyPath := filepath.Join(dir, "witnesses.json")
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if err := c.Flags().Set("witness-directory", copyPath); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return decodeWitnessRows(raw), cleanup, nil
}

// decodeWitnessRows decodes a directory as witness.ParseDirectory does: an
// object's witnesses list or a bare list, numbers as json.Number. The core
// refuses a file that is no directory, so what does not decode here only
// gives no rows.
func decodeWitnessRows(raw []byte) witnessRows {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value interface{}
	if dec.Decode(&value) != nil {
		return nil
	}
	switch value := value.(type) {
	case map[string]interface{}:
		rows, _ := value["witnesses"].([]interface{})
		return rows
	case []interface{}:
		return value
	}
	return nil
}

// rowMembers are the witnessed member's members for the receipt of witness
// URL tsURL: witness, the name of the row dir gives for it, and
// independent_of, that row's list ([] when the row has none). ok is false
// when dir lists no such witness. independent_of is left out when the row's
// list is not a list of strings: it is never guessed.
func (rows witnessRows) rowMembers(dir *witness.Directory, tsURL string) (map[string]any, bool) {
	row, ok := listedRow(dir, tsURL)
	if !ok {
		return nil, false
	}
	members := map[string]any{"witness": row.Name}
	for _, raw := range rows {
		// RowFor takes the first matching row in order, so the first raw row
		// it matches alone is the row dir gave.
		if _, match := witness.DirectoryFromValue([]interface{}{raw}).RowFor(tsURL); !match {
			continue
		}
		fields, _ := raw.(map[string]interface{})
		if list, ok := independentOfList(fields); ok {
			members["independent_of"] = list
		}
		break
	}
	return members, true
}

// independentOfList is a row's independent_of as it states it, [] when the
// row has none; ok is false when it is present but not a list of strings.
func independentOfList(row map[string]interface{}) ([]string, bool) {
	value, present := row["independent_of"]
	if !present {
		return []string{}, true
	}
	items, isList := value.([]interface{})
	if !isList {
		return nil, false
	}
	list := make([]string, 0, len(items))
	for _, item := range items {
		text, isString := item.(string)
		if !isString {
			return nil, false
		}
		list = append(list, text)
	}
	return list, true
}

// withRow adds the members of the row for tsURL, when dir lists it, to a
// failed or withheld member.
func (rows witnessRows) withRow(member map[string]any, dir *witness.Directory, tsURL string) map[string]any {
	if members, ok := rows.rowMembers(dir, tsURL); ok {
		for k, v := range members {
			member[k] = v
		}
	}
	return member
}

// firstListed is first, or tsURL when first is "" and dir lists tsURL's
// witness: the first receipt, in order, of a witness dir lists.
func firstListed(first, tsURL string, dir *witness.Directory) string {
	if first != "" {
		return first
	}
	if _, ok := listedRow(dir, tsURL); ok {
		return tsURL
	}
	return ""
}

// listedRow is dir's row for a receipt's witness URL. An empty URL names no
// witness: "" stands for no receipt here, and RowFor would match it to a row
// with no endpoint.
func listedRow(dir *witness.Directory, tsURL string) (witness.Row, bool) {
	if dir == nil || tsURL == "" {
		return witness.Row{}, false
	}
	return dir.RowFor(tsURL)
}
