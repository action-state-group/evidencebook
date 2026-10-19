// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A method template's '<', '>' and '&' are written as themselves, and the
// report reads back the same.
func TestPropertyResultJSONIsPlain(t *testing.T) {
	for _, r := range []PropertyResult{
		{Property: "p", Status: Pass, Method: "relying party policy <id>", Detail: "a < b && c > d"},
		{Property: NativeCheck, Status: Fail, Method: "<alg> over JCS(x)", DetailFields: map[string]string{"check": "c<1>", "object": "o&o"}},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		// json.Marshal HTML-escapes a Marshaler's output; an encoder without
		// HTML escaping, as the command line uses, shows what MarshalJSON wrote.
		plain, err := r.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		want := `{"property":"p","status":"PASS","method":"relying party policy <id>","detail":"a < b && c > d"}`
		if r.DetailFields != nil {
			want = `{"property":"native_check","status":"FAIL","method":"<alg> over JCS(x)","detail":{"check":"c<1>","object":"o&o"}}`
		}
		if string(plain) != want {
			t.Errorf("got  %s\nwant %s", plain, want)
		}
		for _, in := range [][]byte{plain, b} {
			var back PropertyResult
			if err := json.Unmarshal(in, &back); err != nil {
				t.Fatal(err)
			}
			if back.Method != r.Method || (r.DetailFields == nil && back.Detail != r.Detail) || (r.DetailFields != nil && back.DetailFields["check"] != r.DetailFields["check"]) {
				t.Errorf("round trip: %+v, want %+v", back, r)
			}
		}
	}
}

// LoadTrustPolicy keeps a key set's octets as written, so a duplicate member
// still reaches the adapter's strict reader.
func TestLoadTrustPolicyKeySetVerbatim(t *testing.T) {
	ks := `{"keys":[{"kid":"a","kid":"b"}]}`
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"key_sets":{"ucp.keys":`+ks+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadTrustPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(p.KeySets["ucp.keys"]); got != ks {
		t.Errorf("key set %s, want %s", got, ks)
	}
}
