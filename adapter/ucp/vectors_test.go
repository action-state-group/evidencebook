// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
	"github.com/action-state-group/evidencebook/adapter/internal/ijson"
)

// The agent-action-capsule vectors for this adapter, vectors/adapters/ucp/,
// copied byte for byte into testdata/aac-vectors/.
//
// Pinned to agent-action-capsule main 75009695 (#300).
const (
	aacUCPCommit     = "750096953995904c997a41ad6baaaf5d75939947"
	aacUCPSumsSHA256 = "43814c37195905953e2aec64d20877ec75264459024095bb162e76503dbc5fca"
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
			v, err := ijson.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if canon, err := ijson.JCS(v); err != nil || !bytes.Equal(canon, content) {
				t.Errorf("wrapped content is not JCS(checkout)")
			}
			// The wrapped octets give the same results as the file.
			policy := adapter.TrustPolicy{
				Pins:    map[string]string{PinKeysSource: c.KeySet},
				KeySets: map[string]json.RawMessage{KeySetName: keys},
			}
			fromWrapped := check(content, policy)

			// The vectors supply a key set and no trust policy.
			got := check(raw, policy)
			want := c.Expected.Reports
			if len(got) != len(want)+1 {
				t.Fatalf("%d reports, want the %d expected plus %s", len(got), len(want), PropBusinessTrust)
			}
			for i, w := range want {
				g := got[i]
				// The whole detail object is compared: {"check", "object"}.
				if g.Property != w.Property || g.Status != w.Status || w.Detail != "" || !maps.Equal(g.DetailFields, w.DetailFields) {
					t.Errorf("report %d: got %s %v %s, want %s %v %s", i, g.Property, g.DetailFields, g.Status, w.Property, w.DetailFields, w.Status)
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
