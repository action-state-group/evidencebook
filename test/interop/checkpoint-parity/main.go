// SPDX-License-Identifier: Apache-2.0

// Command checkpoint-parity reads CLL checkpoint statements and says, for
// each, whether this module accepts it: the checks checkpointFromStatement
// makes (checkpoint.ParseRecord, VerifySignature, a complete MMR size).
//
//	go run ./test/interop/checkpoint-parity < cases.json > verdicts.json
//
// cases.json is a JSON array of {"name", "statement_hex"}; the output maps
// each name to "accept" or the error. test/interop/python-evidencebook/
// checkpoint_parity.py writes the cases and compares the Python package's
// verdicts with these.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

type statementCase struct {
	Name         string `json:"name"`
	StatementHex string `json:"statement_hex"`
}

func verdict(statement []byte) string {
	record, err := checkpoint.ParseRecord(statement)
	if err != nil {
		return err.Error()
	}
	if err := record.VerifySignature(); err != nil {
		return err.Error()
	}
	if _, ok := mmr.LeafCount(record.MMRSize); !ok {
		return fmt.Sprintf("checkpoint size %d is not a complete MMR", record.MMRSize)
	}
	return "accept"
}

func main() {
	var cases []statementCase
	if err := json.NewDecoder(os.Stdin).Decode(&cases); err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint-parity:", err)
		os.Exit(1)
	}
	out := map[string]string{}
	for _, c := range cases {
		statement, err := hex.DecodeString(c.StatementHex)
		if err != nil {
			fmt.Fprintln(os.Stderr, "checkpoint-parity:", c.Name, err)
			os.Exit(1)
		}
		out[c.Name] = verdict(statement)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", " ")
	if err := encoder.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint-parity:", err)
		os.Exit(1)
	}
}
