package evidencebook

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/action-state-group/cll-go/witness"
)

// WitnessRow is one row of a witness directory, in capsule-emit's
// witnesses.json format: the witness's endpoint, its binding (cll when
// absent) and its keys. A key id is a raw Ed25519 key in hex, or the SHA-256
// of a DER key listed in public_keys. Other row fields are read past.
type WitnessRow struct {
	Name       string   `json:"name"`
	Endpoint   string   `json:"endpoint"`
	Binding    string   `json:"binding"`
	KeyIDs     []string `json:"key_ids"`
	PublicKeys []string `json:"public_keys"`
}

// ParseWitnessDirectory reads a witness directory: {"witnesses": [rows]} or
// a bare array of rows.
func ParseWitnessDirectory(raw []byte) ([]WitnessRow, error) {
	var rows []WitnessRow
	if trimmed := bytes.TrimLeft(raw, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("%w: witness directory: %v", ErrInvalid, err)
		}
		return rows, nil
	}
	var wrapped struct {
		Witnesses *[]WitnessRow `json:"witnesses"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("%w: witness directory: %v", ErrInvalid, err)
	}
	if wrapped.Witnesses == nil {
		return nil, fmt.Errorf("%w: the witness directory has no witnesses array", ErrInvalid)
	}
	return *wrapped.Witnesses, nil
}

// witnessBinding and witnessEndpoint read a receipt's witness URL the way
// the directory names it: "rekor+https://..." and "scrapi+https://..." name
// those bindings, anything else is cll; the endpoint drops the binding prefix
// and any trailing slash.
func witnessBinding(url string) string {
	scheme, _, found := strings.Cut(url, "://")
	if !found {
		return "cll"
	}
	scheme = strings.ToLower(scheme)
	for _, binding := range []string{"rekor", "scrapi"} {
		if strings.HasPrefix(scheme, binding+"+") {
			return binding
		}
	}
	return "cll"
}

func witnessEndpoint(url string) string {
	if witnessBinding(url) != "cll" {
		_, url, _ = strings.Cut(url, "+")
	}
	return strings.TrimRight(url, "/")
}

func (r WitnessRow) binding() string {
	if r.Binding == "" {
		return "cll"
	}
	return r.Binding
}

// keys returns the row's Ed25519 keys in key_ids order; a key id that names
// no usable Ed25519 key is skipped.
func (r WitnessRow) keys() []ed25519.PublicKey {
	byHash := map[string]ed25519.PublicKey{}
	for _, b64 := range r.PublicKeys {
		der, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		parsed, err := x509.ParsePKIXPublicKey(der)
		if key, ok := parsed.(ed25519.PublicKey); ok && err == nil {
			sum := sha256.Sum256(der)
			byHash[hex.EncodeToString(sum[:])] = key
		}
	}
	var keys []ed25519.PublicKey
	for _, id := range r.KeyIDs {
		if key, ok := byHash[id]; ok {
			keys = append(keys, key)
		} else if raw, err := hex.DecodeString(id); err == nil && len(raw) == ed25519.PublicKeySize {
			keys = append(keys, ed25519.PublicKey(raw))
		}
	}
	return keys
}

// verifyCarriedReceipts checks every witness receipt a bundle carries
// (checkpoint.witnesses) against the signed checkpoint statement it anchors
// to, under the keys of directory's row for the receipt's witness. Each
// must verify: a receipt that is malformed, names a witness the directory
// does not list (or lists with no usable key, or under a binding this
// package does not check), or does not cover this checkpoint under that
// key, is an error. How many receipts a requester needs is its own policy;
// a bundle that carries none passes.
func verifyCarriedReceipts(statement []byte, carried []json.RawMessage, directory []WitnessRow) error {
	if len(carried) == 0 {
		return nil
	}
	if len(directory) == 0 {
		return fmt.Errorf("%w: the bundle carries %d witness receipt(s) and no witness directory was given to verify them", ErrInvalid, len(carried))
	}
	for i, raw := range carried {
		receipt, url, err := decodeWitnessReceipt(raw)
		if err != nil {
			return fmt.Errorf("%w: witness receipt %d: %v", ErrInvalid, i, err)
		}
		if err := checkReceipt(statement, receipt, url, directory); err != nil {
			return fmt.Errorf("%w: witness receipt %d (%s): %v", ErrInvalid, i, url, err)
		}
	}
	return nil
}

func checkReceipt(statement []byte, receipt witness.Receipt, url string, directory []WitnessRow) error {
	binding, endpoint := witnessBinding(url), witnessEndpoint(url)
	var row *WitnessRow
	for i := range directory {
		if directory[i].binding() == binding && strings.TrimRight(directory[i].Endpoint, "/") == endpoint {
			row = &directory[i]
			break
		}
	}
	switch {
	case row == nil:
		return fmt.Errorf("no directory row for this witness")
	case binding != "cll":
		return fmt.Errorf("a %s receipt is not checked here, only cll", binding)
	}
	keys := row.keys()
	if len(keys) == 0 {
		return fmt.Errorf("no usable key in the directory row")
	}
	var last error
	for _, key := range keys {
		verifier, err := witness.NewReceiptVerifier(key)
		if err != nil {
			last = err
			continue
		}
		if last = verifier.Verify(statement, receipt); last == nil {
			return nil
		}
	}
	return last
}

// decodeWitnessReceipt reads one checkpoint.witnesses entry: ts_url,
// entry_hash, receipt_b64 (standard base64), and leaf_index and tree_size
// (numbers, or numbers as strings, as producers write them).
func decodeWitnessReceipt(raw json.RawMessage) (witness.Receipt, string, error) {
	var entry struct {
		URL       string          `json:"ts_url"`
		EntryHash string          `json:"entry_hash"`
		Receipt   string          `json:"receipt_b64"`
		LeafIndex json.RawMessage `json:"leaf_index"`
		TreeSize  json.RawMessage `json:"tree_size"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return witness.Receipt{}, "", err
	}
	if entry.URL == "" || entry.EntryHash == "" || entry.Receipt == "" {
		return witness.Receipt{}, "", fmt.Errorf("ts_url, entry_hash and receipt_b64 are required")
	}
	receiptBytes, err := base64.StdEncoding.DecodeString(entry.Receipt)
	if err != nil {
		return witness.Receipt{}, "", fmt.Errorf("receipt_b64 is not base64")
	}
	leaf, err := receiptInt(entry.LeafIndex)
	if err != nil {
		return witness.Receipt{}, "", fmt.Errorf("leaf_index: %v", err)
	}
	size, err := receiptInt(entry.TreeSize)
	if err != nil {
		return witness.Receipt{}, "", fmt.Errorf("tree_size: %v", err)
	}
	return witness.Receipt{Bytes: receiptBytes, EntryHash: entry.EntryHash, EntryHashScheme: witness.EntryHashSchemeCheckpointDigest, LeafIndex: leaf, TreeSize: size}, entry.URL, nil
}

func receiptInt(raw json.RawMessage) (int64, error) {
	text := strings.Trim(string(raw), `"`)
	if text == "" {
		return 0, fmt.Errorf("missing")
	}
	return strconv.ParseInt(text, 10, 64)
}
