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
	// Context is a sealed request's seal context.
	Context *xdealv1.SealContext `json:"context"`
	// An against request reads record against With: Check is agreement,
	// dispute or ruling, and Held the acceptor agreement's record id.
	Check string `json:"check"`
	With  string `json:"with"`
	Held  string `json:"held"`
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
	case "check", "sealed":
		err := xdealv1.Check([]byte(req.Record))
		if req.Kind == "sealed" && req.Context != nil {
			err = xdealv1.CheckSealed([]byte(req.Record), *req.Context)
		}
		var se *dealprofile.StageError
		switch {
		case err == nil:
			return `"ok"`, nil
		case errors.As(err, &se) && (se.Stage == xdealv1.StageRecord || se.Stage == xdealv1.StageAgreement):
			// The text is the profile's at these stages: compared too.
			return jcsOf([]string{se.Stage, se.Detail})
		case errors.As(err, &se):
			return fmt.Sprintf("%q", se.Stage), nil
		}
		return refused, nil
	case "against":
		// A record that does not decode is reported as its record check
		// refuses it: the checks below run that check first, and the two
		// readers differ only in which of the two refuses a record the
		// schema refuses (a required list written null).
		r, err1 := xdealv1.Decode([]byte(req.Record))
		w, err2 := xdealv1.Decode([]byte(req.With))
		var err error
		switch {
		case err1 != nil:
			err = xdealv1.Check([]byte(req.Record))
		case err2 != nil:
			err = xdealv1.Check([]byte(req.With))
		}
		if err1 != nil || err2 != nil {
			var se *dealprofile.StageError
			if errors.As(err, &se) {
				return fmt.Sprintf("%q", se.Stage), nil
			}
			return refused, nil
		}
		switch req.Check {
		case "agreement":
			err = xdealv1.CheckAgreement(r, w, req.Held)
		case "dispute":
			err = xdealv1.CheckDispute(r, w)
		case "ruling":
			err = xdealv1.CheckRuling(r, w)
		default:
			return "", fmt.Errorf("unknown check %q", req.Check)
		}
		var se *dealprofile.StageError
		switch {
		case err == nil:
			return `"ok"`, nil
		case errors.As(err, &se) && (se.Stage == xdealv1.StageRecord || se.Stage == xdealv1.StageAgreement):
			return jcsOf([]string{se.Stage, se.Detail})
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
