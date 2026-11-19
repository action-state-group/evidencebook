// SPDX-License-Identifier: Apache-2.0

// Command deal-v1 is the Go side of the x-deal-v1 differential check
// (test/interop/deal-v1/diff.py): one JSON request per line on stdin,
// {"kind": "check", "record": <text>}, {"kind": "status", "books": ...} or
// {"kind": "report", "books": ..., "copy": ...} or {"kind": "verify_report",
// "payload": ..., "first": <hex>}, and one line out for each: "ok" or the
// refusal's stage for a check, the JCS of the status value or the report, the
// JCS of {"key_id", "report"} for a verified report and {"stage": ...} for one
// refused, or {"error":true} when it is refused. A verify_report payload is sealed as a report Capsule under the
// test seller's record key (dealprofile/xdealv1's receive tests), and read
// against first, the seller's agreement book pair.
package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	emit "github.com/action-state-group/capsule-emit/go"
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
	// A verify_report request: the report payload and the first record.
	Payload json.RawMessage `json:"payload"`
	First   string          `json:"first"`
}

// sellerKey is the record key of the seller in dealprofile/xdealv1's receive
// tests, which signed the first record of testdata/deal-v1/report-verify.
func sellerKey() ed25519.PrivateKey {
	s := sha256.Sum256([]byte("xdealv1 receive tests\x00seller record key"))
	return ed25519.NewKeyFromSeed(s[:])
}

// verifyReport seals payload as a report Capsule under the seller's key and
// reads it against first.
func verifyReport(payload json.RawMessage, first string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return refusedBy(err), nil
	}
	jcs, err := canonical.JCS(doc)
	if err != nil {
		return refusedBy(err), nil
	}
	identity, err := emit.NewEd25519SigningIdentity(sellerKey())
	if err != nil {
		return "", err
	}
	in, err := dealprofile.ReportCapsule("seller", "xdealv1-test", time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		return "", err
	}
	sealed, err := emit.Seal(emit.SealInput{Capsule: in, Payload: json.RawMessage(jcs), Identity: identity})
	if err != nil {
		return "", err
	}
	pair, err := xdealv1.ReportPair(sealed.Payload, sealed.Envelope, jcs)
	if err != nil {
		return "", err
	}
	firstPair, err := hex.DecodeString(first)
	if err != nil {
		return "", err
	}
	r, err := xdealv1.VerifyReport(pair, firstPair)
	var se *xdealv1.StageError
	if errors.As(err, &se) && os.Getenv("DEAL_V1_DEBUG") == "" {
		return jcsOf(map[string]string{"stage": se.Stage})
	}
	if err != nil {
		return refusedBy(err), nil
	}
	return jcsOf(map[string]any{"key_id": r.KeyID, "report": r.Report})
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
	case "verify_report":
		return verifyReport(req.Payload, req.First)
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
