// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

// randomSource is a shelf's one random source (ShelfConfig.Random). Every
// draw is defined on the bytes it reads, so another implementation fed the
// same stream draws the same values (evidencebook#254 §5):
//
//   - bytes(n) reads n bytes;
//   - uniform(n) reads 8 bytes as a big-endian x, again while x is at or
//     above the largest multiple of n not over 2^64, and returns x mod n
//     (for a power of two, such as a tree position, one read);
//   - a tree position is uniform(2^16), drawn again while it is taken.
type randomSource struct{ r io.Reader }

func newRandomSource(r io.Reader) randomSource {
	if r == nil {
		r = rand.Reader
	}
	return randomSource{r: r}
}

func (s randomSource) bytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.r, b); err != nil {
		return nil, fmt.Errorf("the shelf's random source: %w", err)
	}
	return b, nil
}

// hex is bytes(n) in lowercase hex.
func (s randomSource) hex(n int) (string, error) {
	b, err := s.bytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// uniform is a uniform draw in [0, n), n > 0.
func (s randomSource) uniform(n uint64) (uint64, error) {
	if n == 0 {
		return 0, fmt.Errorf("%w: a uniform draw needs a positive bound", ErrInvalid)
	}
	// 2^64 mod n, computed in 64 bits; zero when n divides 2^64.
	rest := -n % n
	for {
		b, err := s.bytes(8)
		if err != nil {
			return 0, err
		}
		x := binary.BigEndian.Uint64(b)
		if rest == 0 || x < -rest {
			return x % n, nil
		}
	}
}
