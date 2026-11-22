package book

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The card registration vectors AAC defines (SH §13.3), shared with the
// root package's tests: the holder key is derived from a public label, so the
// profile signs the same statement the vector holds, byte for byte.
const cardVectorsPath = "../../../testdata/card-registration/vectors.json"

type cardVector struct {
	Card       string   `json:"card"`
	Holder     string   `json:"holder"`
	Anchor     string   `json:"anchor"`
	Statements []string `json:"statements"`
}

func loadCardVector(t *testing.T) (cardVector, cardService) {
	t.Helper()
	raw, err := os.ReadFile(cardVectorsPath)
	require.NoError(t, err)
	var file struct {
		Policies map[string]struct {
			Witnesses []struct {
				Name string   `json:"name"`
				Keys []string `json:"keys"`
			} `json:"witnesses"`
		} `json:"policies"`
		Cases []struct {
			cardVector
			Name string `json:"name"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	for _, c := range file.Cases {
		if c.Name == "pass-eddsa-time" {
			a := file.Policies["default"].Witnesses[0]
			return c.cardVector, cardService{Name: a.Name, Operator: "test operator", Keys: a.Keys}
		}
	}
	require.Fail(t, "no pass-eddsa-time vector")
	return cardVector{}, cardService{}
}

// receiptOf is the one Receipt a vector statement carries (COSE unprotected
// header 394).
func receiptOf(t *testing.T, statement []byte) []byte {
	t.Helper()
	var tagged cbor.RawTag
	require.NoError(t, cbor.Unmarshal(statement, &tagged))
	var parts []cbor.RawMessage
	require.NoError(t, cbor.Unmarshal(tagged.Content, &parts))
	var unprotected map[int64][][]byte
	require.NoError(t, cbor.Unmarshal(parts[1], &unprotected))
	require.Len(t, unprotected[394], 1)
	return unprotected[394][0]
}

type fakeAnchor struct {
	*httptest.Server
	posts atomic.Int32
}

// newFakeAnchor answers capsule-anchor's POST /transparency/register-statement
// with handle's result.
func newFakeAnchor(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *fakeAnchor {
	t.Helper()
	f := &fakeAnchor{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.posts.Add(1)
		if r.URL.Path != "/transparency/register-statement" {
			http.NotFound(w, r)
			return
		}
		handle(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func receiptAnswer(receipt []byte) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"receipt_b64": base64.StdEncoding.EncodeToString(receipt)})
	}
}

func serviceFile(t *testing.T, s cardService) string {
	t.Helper()
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	return writeJSON(t, string(raw))
}

// holderProfile is a profile signing under the vector's holder key.
func holderProfile(t *testing.T, name string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seed := sha256.Sum256([]byte("card-registration-v0 key holder"))
	key := ed25519.NewKeyFromSeed(seed[:])
	p := cli.Profile{Name: name, Type: "jsonl", LogID: name, Log: Name, Namespace: "capsule", Operator: name}
	p.Connection.Database = filepath.Join(t.TempDir(), name)
	p.Signing.Value = hex.EncodeToString(key.Seed())
	p.TrustedKeys = []string{hex.EncodeToString(key.Public().(ed25519.PublicKey))}
	require.NoError(t, cli.SaveProfile(p, false))
}

func cardFile(t *testing.T, v cardVector) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "card.json")
	card, err := hex.DecodeString(v.Card)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, card, 0o600))
	return path
}

func TestCardRegisterWritesTheStatementAndCheckPasses(t *testing.T) {
	v, service := loadCardVector(t)
	holderProfile(t, "holder")
	vector, err := hex.DecodeString(v.Statements[0])
	require.NoError(t, err)
	var posted []byte
	anchor := newFakeAnchor(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Statement string `json:"signed_statement_b64"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		posted, err = base64.StdEncoding.DecodeString(body.Statement)
		require.NoError(t, err)
		receiptAnswer(receiptOf(t, vector))(w, r)
	})
	service.URL = anchor.URL
	card, svc, out := cardFile(t, v), serviceFile(t, service), filepath.Join(t.TempDir(), "statement.json")

	_, err = invoke(t, "", "--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc, "--out", out)
	require.NoError(t, err)
	require.EqualValues(t, 1, anchor.posts.Load())
	assert.NotEqual(t, vector, posted, "the service is sent the statement with no Receipt")

	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var written cardStatementFile
	require.NoError(t, json.Unmarshal(raw, &written))
	assert.Equal(t, v.Statements[0], written.Statement, "the statement with its Receipt is the vector's, byte for byte")

	got, err := invoke(t, "", "card", "check", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc, "--statement", out)
	require.NoError(t, err, got)
	var report struct {
		Property string  `json:"property"`
		Status   string  `json:"status"`
		Service  *string `json:"service"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &report), got)
	assert.Equal(t, "card_registered", report.Property)
	assert.Equal(t, "PASS", report.Status)
	require.NotNil(t, report.Service)
	assert.Equal(t, "service-a", *report.Service)
}

func TestCardRegisterPrintsTheStatementWithNoOut(t *testing.T) {
	v, service := loadCardVector(t)
	holderProfile(t, "holder")
	vector, _ := hex.DecodeString(v.Statements[0])
	anchor := newFakeAnchor(t, receiptAnswer(receiptOf(t, vector)))
	service.URL = anchor.URL
	got, err := invoke(t, "", "--profile", "holder", "card", "register", "--card", cardFile(t, v), "--holder", v.Holder, "--anchor", v.Anchor, "--service", serviceFile(t, service))
	require.NoError(t, err)
	var written cardStatementFile
	require.NoError(t, json.Unmarshal([]byte(got), &written), got)
	assert.Equal(t, v.Statements[0], written.Statement)
}

func TestCardRegisterPostsNothingWhenRefused(t *testing.T) {
	v, service := loadCardVector(t)
	holderProfile(t, "holder")
	vector, _ := hex.DecodeString(v.Statements[0])
	anchor := newFakeAnchor(t, receiptAnswer(receiptOf(t, vector)))
	service.URL = anchor.URL
	card, svc := cardFile(t, v), serviceFile(t, service)
	base := []string{"--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor}

	t.Run("no --service: opt-in, no default", func(t *testing.T) {
		_, err := invoke(t, "", base...)
		require.ErrorIs(t, err, cli.ErrInput)
		assert.Contains(t, err.Error(), "no default")
	})
	t.Run("a holder the profile does not sign as", func(t *testing.T) {
		other := hex.EncodeToString(make([]byte, 32))
		args := []string{"--profile", "holder", "card", "register", "--card", card, "--holder", other, "--anchor", v.Anchor, "--service", svc}
		_, err := invoke(t, "", args...)
		require.ErrorIs(t, err, cli.ErrInput)
		assert.Contains(t, err.Error(), "nothing was posted")
	})
	t.Run("a malformed anchor", func(t *testing.T) {
		_, err := invoke(t, "", "--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", "zz", "--service", svc)
		require.Error(t, err)
	})
	t.Run("missing flags", func(t *testing.T) {
		_, err := invoke(t, "", "--profile", "holder", "card", "register", "--service", svc)
		require.ErrorIs(t, err, cli.ErrInput)
	})
	assert.Zero(t, anchor.posts.Load(), "nothing reached the service")
}

func TestCardRegisterFailuresLeaveNothing(t *testing.T) {
	v, service := loadCardVector(t)
	holderProfile(t, "holder")
	vector, _ := hex.DecodeString(v.Statements[0])
	card := cardFile(t, v)
	cases := map[string]func(w http.ResponseWriter, r *http.Request){
		"server error": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusInternalServerError) },
		"redirect is not followed": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusFound)
		},
		"not json":       func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
		"no receipt":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) },
		"over the bound": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, cardServiceMaxResponse+2)) },
		"junk receipt":   receiptAnswer([]byte("junk")),
		"receipt of another statement": func(w http.ResponseWriter, r *http.Request) {
			other := append([]byte{}, receiptOf(t, vector)...)
			other[len(other)-1] ^= 0xff
			receiptAnswer(other)(w, r)
		},
	}
	for name, handle := range cases {
		t.Run(name, func(t *testing.T) {
			anchor := newFakeAnchor(t, handle)
			service.URL = anchor.URL
			out := filepath.Join(t.TempDir(), "statement.json")
			got, err := invoke(t, "", "--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", serviceFile(t, service), "--out", out)
			require.Error(t, err, got)
			assert.Contains(t, err.Error(), "may have logged it", "after the post, the error does not say nothing was posted")
			assert.NotContains(t, err.Error(), "nothing was posted")
			assert.EqualValues(t, 1, anchor.posts.Load())
			_, statErr := os.Stat(out)
			assert.True(t, os.IsNotExist(statErr), "no statement file is kept on a failed registration")
			assert.NotContains(t, got, v.Statements[0])
		})
	}
}

func TestCardCheck(t *testing.T) {
	v, service := loadCardVector(t)
	service.URL = "https://anchor.example"
	svc, card := serviceFile(t, service), cardFile(t, v)

	t.Run("no statement is NOT_PRESENT, not a finding", func(t *testing.T) {
		got, err := invoke(t, "", "card", "check", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc)
		require.NoError(t, err, got)
		assert.Contains(t, got, `"NOT_PRESENT"`)
		assert.NotContains(t, got, "anchor_inclusion")
	})
	t.Run("another card does not pass", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "other.json")
		require.NoError(t, os.WriteFile(other, []byte(`{"not":"the card"}`), 0o600))
		statement := writeJSON(t, `{"statement":"`+v.Statements[0]+`"}`)
		got, err := invoke(t, "", "card", "check", "--card", other, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc, "--statement", statement)
		require.NoError(t, err, got)
		assert.Contains(t, got, `"FAIL"`)
	})
	t.Run("a statement file that is not hex", func(t *testing.T) {
		statement := writeJSON(t, `{"statement":"nothex"}`)
		_, err := invoke(t, "", "card", "check", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc, "--statement", statement)
		require.ErrorIs(t, err, cli.ErrInput)
	})
}

func TestLoadCardServiceRefuses(t *testing.T) {
	good := `{"name":"a","operator":"o","url":"https://a.example","keys":["00"]}`
	_, err := loadCardService(writeJSON(t, good))
	require.NoError(t, err)
	for name, body := range map[string]string{
		"no name":       `{"operator":"o","url":"https://a.example","keys":["00"]}`,
		"no operator":   `{"name":"a","url":"https://a.example","keys":["00"]}`,
		"no keys":       `{"name":"a","operator":"o","url":"https://a.example","keys":[]}`,
		"empty key":     `{"name":"a","operator":"o","url":"https://a.example","keys":[""]}`,
		"ftp url":       `{"name":"a","operator":"o","url":"ftp://a.example","keys":["00"]}`,
		"no host":       `{"name":"a","operator":"o","url":"https://","keys":["00"]}`,
		"unknown key":   `{"name":"a","operator":"o","url":"https://a.example","keys":["00"],"default":true}`,
		"not an object": `[]`,
	} {
		_, err := loadCardService(writeJSON(t, body))
		assert.ErrorIs(t, err, cli.ErrInput, name)
	}
	_, err = loadCardService("")
	require.ErrorIs(t, err, cli.ErrInput)
}
