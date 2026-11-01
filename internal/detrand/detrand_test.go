// SPDX-License-Identifier: Apache-2.0

package detrand

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheStreamIsItsBlocksInOrder(t *testing.T) {
	var want []byte
	for i := uint64(0); i < 3; i++ {
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], i)
		sum := sha256.Sum256(append([]byte("seed"), counter[:]...))
		want = append(want, sum[:]...)
	}
	got := make([]byte, len(want))
	_, err := io.ReadFull(New("seed"), got)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// Reads of any size see the same stream.
	r := New("seed")
	var pieces []byte
	for _, size := range []int{1, 7, 31, 33, 24} {
		b := make([]byte, size)
		_, err := io.ReadFull(r, b)
		require.NoError(t, err)
		pieces = append(pieces, b...)
	}
	require.True(t, bytes.Equal(want, pieces))
	require.NotEqual(t, want[:32], func() []byte { b := make([]byte, 32); _, _ = New("other").Read(b); return b }())
}
