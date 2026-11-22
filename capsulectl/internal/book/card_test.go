package book

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	postedCh := make(chan []byte, 1)
	receipt := receiptOf(t, vector)
	anchor := newFakeAnchor(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Statement string `json:"signed_statement_b64"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		octets, decodeErr := base64.StdEncoding.DecodeString(body.Statement)
		if decodeErr != nil {
			http.Error(w, "bad base64", http.StatusBadRequest)
			return
		}
		postedCh <- octets
		receiptAnswer(receipt)(w, r)
	})
	service.URL = anchor.URL
	card, svc, out := cardFile(t, v), serviceFile(t, service), filepath.Join(t.TempDir(), "statement.json")

	_, err = invoke(t, "", "--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc, "--out", out)
	require.NoError(t, err)
	require.EqualValues(t, 1, anchor.posts.Load())
	assert.NotEqual(t, vector, <-postedCh, "the service is sent the statement with no Receipt")

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
	existing := filepath.Join(t.TempDir(), "earlier.json")
	require.NoError(t, os.WriteFile(existing, []byte("earlier statement"), 0o600))
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
	t.Run("--out is an existing file, such as the card itself", func(t *testing.T) {
		for _, out := range []string{card, existing} {
			_, err := invoke(t, "", "--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc, "--out", out)
			require.ErrorIs(t, err, cli.ErrInput)
			assert.Contains(t, err.Error(), "exists")
		}
		kept, err := os.ReadFile(existing)
		require.NoError(t, err)
		assert.Equal(t, "earlier statement", string(kept))
	})
	t.Run("--out in a directory that does not exist", func(t *testing.T) {
		_, err := invoke(t, "", "--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", svc, "--out", filepath.Join(t.TempDir(), "missing", "s.json"))
		require.ErrorIs(t, err, cli.ErrInput)
		assert.Contains(t, err.Error(), "nothing was posted")
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
	goodReceipt := receiptOf(t, vector)
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
			other := append([]byte{}, goodReceipt...)
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
	key := "302a300506032b6570032100c3e0c07b69a2d7a85de9a29c2bb013418a99d632b582c81517504a5d8efd52ef"
	good := `{"name":"a","operator":"o","url":"https://a.example","keys":["` + key + `"]}`
	_, err := loadCardService(writeJSON(t, good))
	require.NoError(t, err)
	k := `"keys":["` + key + `"]`
	for name, body := range map[string]string{
		"no name":         `{"operator":"o","url":"https://a.example",` + k + `}`,
		"no operator":     `{"name":"a","url":"https://a.example",` + k + `}`,
		"no keys":         `{"name":"a","operator":"o","url":"https://a.example","keys":[]}`,
		"empty key":       `{"name":"a","operator":"o","url":"https://a.example","keys":[""]}`,
		"short key":       `{"name":"a","operator":"o","url":"https://a.example","keys":["00"]}`,
		"uppercase key":   `{"name":"a","operator":"o","url":"https://a.example","keys":["` + strings.ToUpper(key) + `"]}`,
		"raw 32-byte key": `{"name":"a","operator":"o","url":"https://a.example","keys":["` + key[24:] + `"]}`,
		"ftp url":         `{"name":"a","operator":"o","url":"ftp://a.example",` + k + `}`,
		"no host":         `{"name":"a","operator":"o","url":"https://",` + k + `}`,
		"credentials":     `{"name":"a","operator":"o","url":"https://user:secret@a.example",` + k + `}`,
		"query":           `{"name":"a","operator":"o","url":"https://a.example/?x=1",` + k + `}`,
		"fragment":        `{"name":"a","operator":"o","url":"https://a.example/#f",` + k + `}`,
		"unknown member":  `{"name":"a","operator":"o","url":"https://a.example",` + k + `,"default":true}`,
		"not an object":   `[]`,
	} {
		_, err := loadCardService(writeJSON(t, body))
		assert.ErrorIs(t, err, cli.ErrInput, name)
	}
	_, err = loadCardService("")
	require.ErrorIs(t, err, cli.ErrInput)
}

// failingWriter is a stdout that takes no output.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

// A statement that cannot be saved after the post is not lost: the error says
// the card is registered and carries the statement to keep beside the card.
func TestCardRegisterKeepsTheStatementWhenSavingFails(t *testing.T) {
	v, service := loadCardVector(t)
	holderProfile(t, "holder")
	vector, _ := hex.DecodeString(v.Statements[0])
	receipt := receiptOf(t, vector)
	card := cardFile(t, v)

	t.Run("the --out path appeared during the post", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "statement.json")
		anchor := newFakeAnchor(t, func(w http.ResponseWriter, r *http.Request) {
			_ = os.WriteFile(out, []byte("someone else's file"), 0o600)
			receiptAnswer(receipt)(w, r)
		})
		service.URL = anchor.URL
		_, err := invoke(t, "", "--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", serviceFile(t, service), "--out", out)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "registered, but its statement was not saved")
		assert.Contains(t, err.Error(), v.Statements[0])
		kept, readErr := os.ReadFile(out)
		require.NoError(t, readErr)
		assert.Equal(t, "someone else's file", string(kept), "the file that appeared is left as it was")
		left, globErr := filepath.Glob(filepath.Join(filepath.Dir(out), ".card-*"))
		require.NoError(t, globErr)
		assert.Empty(t, left, "no temp file is left")
	})
	t.Run("standard output cannot be written", func(t *testing.T) {
		anchor := newFakeAnchor(t, receiptAnswer(receipt))
		service.URL = anchor.URL
		c := testCommand()
		c.SetIn(strings.NewReader(""))
		c.SetOut(failingWriter{})
		c.SetErr(failingWriter{})
		c.SetArgs([]string{"--profile", "holder", "card", "register", "--card", card, "--holder", v.Holder, "--anchor", v.Anchor, "--service", serviceFile(t, service)})
		err := c.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "registered, but its statement was not saved")
		assert.Contains(t, err.Error(), v.Statements[0])
	})
}
