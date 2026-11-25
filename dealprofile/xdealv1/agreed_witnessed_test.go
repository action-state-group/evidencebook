// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The agreed_at check's vectors (testdata/deal-v1/agreed-at), hand-written
// from AAC spec/standard-history-v0.md §11.2 "Agreed no later than witnessed".
func TestCheckAgreedAtVectors(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/deal-v1/agreed-at/vectors.json")
	require.NoError(t, err)
	var file struct {
		Cases []struct {
			Name           string `json:"name"`
			AgreedAt       string `json:"agreed_at"`
			FirstWitnessed string `json:"first_witnessed"`
			Want           string `json:"want"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Cases)
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			witnessed, err := time.Parse(time.RFC3339, c.FirstWitnessed)
			require.NoError(t, err)
			err = CheckAgreedAt(c.AgreedAt, witnessed)
			switch c.Want {
			case "ok":
				assert.NoError(t, err)
			case "agreed_after_witnessed":
				assert.ErrorIs(t, err, ErrAgreedAfterWitnessed)
			case "malformed":
				require.Error(t, err)
				assert.False(t, errors.Is(err, ErrAgreedAfterWitnessed))
			default:
				t.Fatalf("unknown want %q", c.Want)
			}
		})
	}
}
