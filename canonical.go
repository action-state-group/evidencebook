// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
)

// canonicalJSON returns UTF8(JCS(v)) for any value that marshals to JSON.
// CONTRACT: encoding/json.Marshal, which this wraps, sets this parameter.
func canonicalJSON(v any) ([]byte, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeJSON(encoded)
	if err != nil {
		return nil, err
	}
	return canonical.JCS(decoded)
}

// decodeJSON decodes with json.Number so JCS sees integers, not floats.
// CONTRACT: agent-action-capsule canonical.JCS and bundle.VerifyBundle take decoded JSON as interface{}.
func decodeJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return value, nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
