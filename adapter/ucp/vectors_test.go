// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

// The agent-action-capsule vectors for this adapter, vectors/adapters/ucp/,
// copied byte for byte into testdata/aac-vectors/.
//
// PINNED TO AN UNMERGED HEAD: 9e65d793 is em-spec's branch head, not a merge
// commit. Re-pin to the merge SHA when the vectors merge: copy the directory
// from the merge commit, then update aacUCPCommit and aacUCPSumsSHA256.
const (
	aacUCPCommit     = "9e65d7930fb85a30e6c206be35f0b287ed1657e6" // re-pin to the merge SHA
	aacUCPSumsSHA256 = "d55ee23bfbaea618be73851fd28d51d9efc6096ed1a4b852353eb8a42ba11a9b"
)

type aacCase struct {
	Name     string `json:"name"`
	Checkout string `json:"checkout"`
	KeySet   string `json:"key_set"`
	Wrapped  struct {
		Type      string `json:"type"`
		DigestAlg string `json:"digest_alg"`
		Digest    string `json:"digest"`
		Content   string `json:"content"`
	} `json:"wrapped"`
	Expected struct {
		Reports       []adapter.PropertyResult `json:"reports"`
		BusinessTrust adapter.Status           `json:"business_trust"`
		Mapping       map[string]struct {
			Row         int    `json:"row"`
			Status      string `json:"status"`
			Sufficiency string `json:"sufficiency"`
			Verdict     string `json:"verdict"`
		} `json:"mapping"`
	} `json:"expected"`
}

func TestAACVectors(t *testing.T) {
	dir := filepath.Join("testdata", "aac-vectors")
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if s := sha256.Sum256(sums); hex.EncodeToString(s[:]) != aacUCPSumsSHA256 {
		t.Fatalf("testdata/aac-vectors/SHA256SUMS is not the one pinned at %s", aacUCPCommit)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		want, name, _ := strings.Cut(line, "  ")
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if s := sha256.Sum256(data); hex.EncodeToString(s[:]) != want {
			t.Fatalf("%s does not match SHA256SUMS", name)
		}
	}
	var doc struct {
		UCPCommit string    `json:"ucp_commit"`
		Cases     []aacCase `json:"cases"`
	}
	data, _ := os.ReadFile(filepath.Join(dir, "cases.json"))
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.UCPCommit != UCPCommit || len(doc.Cases) != 2 {
		t.Fatalf("cases.json: UCP commit %s, %d cases", doc.UCPCommit, len(doc.Cases))
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, c.Checkout))
			if err != nil {
				t.Fatal(err)
			}
			keys, err := os.ReadFile(filepath.Join(dir, c.KeySet))
			if err != nil {
				t.Fatal(err)
			}

			// The wrapped entry's octets are the RFC 8785 form of the whole
			// checkout, ap2 included (§3(a), §6), and its digest is theirs.
			content, err := base64.RawURLEncoding.DecodeString(c.Wrapped.Content)
			if err != nil {
				t.Fatal(err)
			}
			if s := sha256.Sum256(content); c.Wrapped.Type != "ucp.checkout" || c.Wrapped.DigestAlg != "SHA-256" || hex.EncodeToString(s[:]) != c.Wrapped.Digest {
				t.Errorf("wrapped entry: type %s, digest does not match content", c.Wrapped.Type)
			}
			v, err := parseIJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			if canon, err := jcs(v); err != nil || !bytes.Equal(canon, content) {
				t.Errorf("wrapped content is not JCS(checkout)")
			}
			// The wrapped octets give the same results as the file.
			policy := adapter.TrustPolicy{Pins: map[string]string{PinKeys: string(keys), PinKeysSource: c.KeySet}}
			fromWrapped := check(content, policy)

			// The vectors supply a key set and no trust policy.
			got := check(raw, policy)
			want := c.Expected.Reports
			if len(got) != len(want)+1 {
				t.Fatalf("%d reports, want the %d expected plus %s", len(got), len(want), PropBusinessTrust)
			}
			for i, w := range want {
				g := got[i]
				// Detail shape: the vectors at 9e65d793 (and ucp-v0.md at
				// 39bf254a, §3(e)) write detail as the text "<name>". This
				// adapter writes {"check": <name>, "object": "ucp.checkout"}
				// (adapter.NativeCheckReport), the shape the other adapter
				// declarations use. The check name is compared; the shape
				// difference is reported upstream, not hidden: if the
				// vectors move to the object form, this assertion changes.
				if w.DetailFields != nil {
					t.Errorf("report %d: vector detail is now an object %v; compare it whole", i, w.DetailFields)
				}
				if g.Property != w.Property || adapter.CheckName(g) != w.Detail || g.Status != w.Status || g.DetailFields["object"] != CheckCheckout {
					t.Errorf("report %d: got %s %v %s, want %s %s %s", i, g.Property, g.DetailFields, g.Status, w.Property, w.Detail, w.Status)
				}
				if g.Method != w.Method {
					t.Errorf("%s: method %q, want %q", w.Detail, g.Method, w.Method)
				}
				if fromWrapped[i].Status != g.Status {
					t.Errorf("%s: %s from the wrapped octets, %s from the file", w.Detail, fromWrapped[i].Status, g.Status)
				}
			}
			if bt := got[len(want)]; bt.Property != PropBusinessTrust || bt.Status != c.Expected.BusinessTrust {
				t.Errorf("business_trust: %s %s, want %s", bt.Property, bt.Status, c.Expected.BusinessTrust)
			}

			// §8.1 mapping recorded by the failing case.
			for name, m := range c.Expected.Mapping {
				rel := adapter.IsTheCheck
				if name == "requirement_rests_on_the_check" {
					rel = adapter.SupportedByTheCheck
				}
				ev := adapter.Evaluate(got, adapter.Requirement{ID: name, Relation: rel, DependsOn: []string{PropMASignature}})
				if ev.Row != strconv.Itoa(m.Row) || ev.Status != m.Status || ev.Sufficiency != m.Sufficiency || ev.Verdict != m.Verdict {
					t.Errorf("%s: %+v, want %+v", name, ev, m)
				}
			}
		})
	}
}
