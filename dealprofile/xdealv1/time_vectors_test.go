// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// timeVectors is what parseAt's time.Parse(time.RFC3339, at) gives on times
// a record that was never checked may carry (testdata/deal-v1/time): the
// instant, or Go's error text. Python's port of the parser
// (python/evidencebook/deal/v1/gotime.py) is held to it.
const timeVectors = "../../testdata/deal-v1/time/vectors.json"

// TestTimeVectors writes and checks testdata/deal-v1/time/vectors.json
// (EB_WRITE_DEAL_V1_VECTORS=1): hand-picked times, then times mutated from
// them by a fixed seed.
func TestTimeVectors(t *testing.T) {
	type tcase struct {
		In         string `json:"in"`
		OK         bool   `json:"ok"`
		Unix       int64  `json:"unix,omitempty"`
		Nanos      int    `json:"nanos,omitempty"`
		BeforeZero bool   `json:"before_zero,omitempty"`
		Error      string `json:"error,omitempty"`
	}
	inputs := []string{
		"2026-11-01T10:00:00Z", "2026-11-01T1:00:00Z", "2026-11-01T10:00:00,5Z", "2026-11-01T10:00:00.1234567891Z",
		"2026-11-01T10:00:00+24:00", "2026-11-01T10:00:00+00:60", "2026-11-01T10:00:00+25:00", "2026-11-01T10:00:00+00:61",
		"2026-11-01T10:00:00-26:0z", "bad", "", "0000-01-01T00:00:00Z", "0001-01-01T00:00:00Z", "2026-02-30T00:00:00Z",
		"2024-02-29T00:00:00Z", "2026-11-01T10:00:00", "2026-11-01t10:00:00Z", "2026-11-01T10:00:00z", "2026-13-01T00:00:00Z",
		"2026-11-01T24:00:00Z", "2026-11-01T10:60:00Z", "2026-11-01T10:00:60Z", "2026-11-01T10:00:00Zx",
		"2026-11-01T10:00:00.Z", "2026-11-01T10:00:00 05:00", "é2026-11-01T10:00:00Z", "2026-11-01T10:00:00\"Z",
		"9999-12-31T23:59:59-24:00",
	}
	alphabet := []rune("0123456789-:T.,Z+zt é\"\\\n ")
	r := rand.New(rand.NewPCG(461, 7))
	for i := 0; i < 600; i++ {
		s := []rune(inputs[r.IntN(len(inputs))])
		for n := r.IntN(4); n > 0; n-- {
			j := r.IntN(len(s) + 1)
			switch op := r.Float64(); {
			case op < 0.4 && len(s) > 0:
				s[min(j, len(s)-1)] = alphabet[r.IntN(len(alphabet))]
			case op < 0.7:
				s = append(s[:j], append([]rune{alphabet[r.IntN(len(alphabet))]}, s[j:]...)...)
			case len(s) > 0:
				k := min(j, len(s)-1)
				s = append(s[:k], s[k+1:]...)
			}
		}
		inputs = append(inputs, string(s))
	}
	var cases []tcase
	for _, in := range inputs {
		c := tcase{In: in}
		if at, err := time.Parse(time.RFC3339, in); err != nil {
			c.Error = err.Error()
		} else {
			c.OK, c.Unix, c.Nanos, c.BeforeZero = true, at.Unix(), at.Nanosecond(), at.Before(time.Time{})
		}
		cases = append(cases, c)
	}
	out, err := json.MarshalIndent(map[string]any{
		"description": "time.Parse(time.RFC3339, in), as parseAt reads a record's at in CheckAuthorizations (evidencebook#461): the instant (Unix seconds and nanoseconds, and whether it is before Go's zero Time, which at never decreasing also refuses), or Go's error text. Hand-picked times, then times mutated from them by a fixed seed. Written by dealprofile/xdealv1's TestTimeVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	out = append(out, '\n')
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll("../../testdata/deal-v1/time", 0o755))
		require.NoError(t, os.WriteFile(timeVectors, out, 0o644))
	}
	want, err := os.ReadFile(timeVectors)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, out), "%s differs; rerun with EB_WRITE_DEAL_V1_VECTORS=1", timeVectors)
}
