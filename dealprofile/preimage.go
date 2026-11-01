// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// PreimageMatches reports whether preimage, the body_evidence preimage of a
// Lightning payment the user received, is the preimage of paymentHash, the
// step's settlement payment_ref value (ln.payment_hash): SHA-256 over the 32
// octets the hex names. Both are 64 lowercase hex characters, or it is false.
func PreimageMatches(preimage, paymentHash string) bool {
	if !hex64.MatchString(preimage) || !hex64.MatchString(paymentHash) {
		return false
	}
	raw, _ := hex.DecodeString(preimage)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]) == paymentHash
}
