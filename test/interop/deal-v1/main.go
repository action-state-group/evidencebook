// SPDX-License-Identifier: Apache-2.0

// Command deal-v1 is the Go side of the x-deal-v1 differential check
// (test/interop/deal-v1/diff.py): one JSON request per line on stdin,
// {"kind": "check", "record": <text>}, {"kind": "status", "books": ...} or
// {"kind": "report", "books": ..., "copy": ...}, and one line out for each:
// "ok" or the refusal's stage for a check, the JCS of the status value or the
// report, or {"error":true} when it is refused.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

type request struct {
	Kind   string          `json:"kind"`
	Record string          `json:"record"`
	Books  json.RawMessage `json:"books"`
	Copy   *reportCopy     `json:"copy"`
}

type reportCopy struct {
	Audience string `json:"audience"`
	Nonce    string `json:"nonce"`
	Referee  string `json:"referee"`
	AsOf     string `json:"as_of"`
}

func jcsOf(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", err
	}
	b, err := canonical.JCS(doc)
	return string(b), err
}

// refusedBy is the refusal line, with the refusal's text when
// DEAL_V1_DEBUG is set.
func refusedBy(err error) string {
	if os.Getenv("DEAL_V1_DEBUG") != "" && err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(b)
	}
	return `{"error":true}`
}

func answer(line []byte) (string, error) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return "", err
	}
	refused := `{"error":true}`
	switch req.Kind {
	case "check":
		err := xdealv1.Check([]byte(req.Record))
		var se *dealprofile.StageError
		switch {
		case err == nil:
			return `"ok"`, nil
		case errors.As(err, &se):
			return fmt.Sprintf("%q", se.Stage), nil
		}
		return refused, nil
	case "status":
		var b xdealv1.StatusBooks
		if err := json.Unmarshal(req.Books, &b); err != nil {
			return refusedBy(err), nil
		}
		v, err := xdealv1.BuildStatus(b)
		if err != nil {
			return refusedBy(err), nil
		}
		return jcsOf(v)
	case "report":
		var b xdealv1.ReportBooks
		if err := json.Unmarshal(req.Books, &b); err != nil || req.Copy == nil {
			return refusedBy(err), nil
		}
		r, err := xdealv1.BuildReport(b, xdealv1.ReportCopy{Audience: req.Copy.Audience, Nonce: req.Copy.Nonce,
			Referee: req.Copy.Referee, AsOf: req.Copy.AsOf})
		if err != nil {
			return refusedBy(err), nil
		}
		return jcsOf(r)
	}
	return "", fmt.Errorf("unknown kind %q", req.Kind)
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<26)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for in.Scan() {
		line, err := answer(in.Bytes())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(out, line)
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
