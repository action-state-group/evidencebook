// SPDX-License-Identifier: Apache-2.0

// Package detrand is the deterministic random stream the shelf vectors and
// the storage interlock inject as a shelf's one random source
// (evidencebook#254 §5). Block i (from 0) of the stream for seed is
// SHA-256(seed ‖ uint64be(i)); the stream is the blocks in order. Rust and
// Python build the same stream from the same seed. It is for tests only:
// production uses the system CSPRNG.
package detrand

import (
	"crypto/sha256"
	"encoding/binary"
)

// Reader reads the stream for one seed. It is not safe for concurrent use.
type Reader struct {
	seed  []byte
	block uint64
	buf   []byte
}

// New is the stream for seed.
func New(seed string) *Reader {
	return &Reader{seed: []byte(seed)}
}

// Read fills p from the stream; it never fails.
func (r *Reader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.buf) == 0 {
			var counter [8]byte
			binary.BigEndian.PutUint64(counter[:], r.block)
			sum := sha256.Sum256(append(append([]byte{}, r.seed...), counter[:]...))
			r.buf = sum[:]
			r.block++
		}
		c := copy(p[n:], r.buf)
		r.buf = r.buf[c:]
		n += c
	}
	return n, nil
}
