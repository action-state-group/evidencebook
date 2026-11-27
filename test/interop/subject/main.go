// SPDX-License-Identifier: Apache-2.0

// Command subject is the Go side of the subject bundle differential check
// (test/interop/subject/diff.py, evidencebook#1012 and #1013): one JSON
// request per line on stdin, {"kind": "verify", "bundle": <the bundle as
// text>}, and one line out for each: the JCS of what VerifySubjectBundle
// makes of it (Verdict), or {"valid":false} when it is refused. Refusal
// texts differ between languages and are not compared.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	evidencebook "github.com/action-state-group/evidencebook"
)

type request struct {
	Kind   string `json:"kind"`
	Bundle string `json:"bundle"`
}

// Verdict is what a verifier makes of a subject bundle that passes. Every
// implementation answers with the same members, so the check compares the
// JCS text.
type Verdict struct {
	Valid        bool                `json:"valid"`
	Form         string              `json:"form"`
	Level        string              `json:"level"`
	SubjectKind  string              `json:"subject_kind"`
	SubjectID    string              `json:"subject_id"`
	Membership   string              `json:"membership"`
	Root         string              `json:"root"`
	Members      []string            `json:"members"`
	MemberCount  int                 `json:"member_count"`
	Close        string              `json:"close"`
	Closed       bool                `json:"closed"`
	HeldByDigest []string            `json:"held_by_digest"`
	Withheld     []string            `json:"withheld"`
	SubjectsOf   map[string][]string `json:"subjects_of"`
}

func verify(data []byte) Verdict {
	got, err := evidencebook.VerifySubjectBundle(data)
	if err != nil {
		return Verdict{}
	}
	v := Verdict{Valid: true, Form: got.Form, Level: got.Level, SubjectKind: got.Subject.SubjectKind, SubjectID: got.Subject.SubjectID,
		Membership: got.Subject.Membership, Root: got.Root, Members: got.Members, MemberCount: got.MemberCount, Close: got.Close,
		Closed: got.Closed, HeldByDigest: got.HeldByDigest, Withheld: []string{}, SubjectsOf: map[string][]string{}}
	if v.Members == nil {
		v.Members = []string{}
	}
	if v.HeldByDigest == nil {
		v.HeldByDigest = []string{}
	}
	for _, w := range got.Withheld {
		v.Withheld = append(v.Withheld, w.RecordID+" "+w.Digest)
	}
	sort.Strings(v.Withheld)
	if got.Bundle != nil {
		for _, r := range got.Bundle.Records {
			if ids := evidencebook.SubjectsOfRecord(got.Bundle.Records, r.RecordID); len(ids) > 0 {
				v.SubjectsOf[r.RecordID] = ids
			}
		}
	}
	return v
}

func answer(line []byte) (string, error) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return "", err
	}
	if req.Kind != "verify" {
		return "", fmt.Errorf("unknown kind %q", req.Kind)
	}
	v := verify([]byte(req.Bundle))
	var out any = v
	if !v.Valid {
		out = map[string]bool{"valid": false}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", err
	}
	text, err := canonical.JCS(doc)
	return string(text), err
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 64<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for in.Scan() {
		text, err := answer(in.Bytes())
		if err != nil {
			fmt.Fprintln(os.Stderr, "subject:", err)
			os.Exit(1)
		}
		fmt.Fprintln(out, text)
		out.Flush()
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "subject:", err)
		os.Exit(1)
	}
}
