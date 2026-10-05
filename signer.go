// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"io"

	emit "github.com/action-state-group/capsule-emit-go"
	"github.com/veraison/go-cose"
)

// Signer is the book's signing seam. A host plugs in any key custodian (a
// local key, an HSM, a KMS) that produces raw Ed25519 signatures; the book
// never holds or requires a particular key. Sign receives the exact bytes to
// be signed (for COSE, the Sig_structure), never a pre-hash.
type Signer interface {
	PublicKey() ed25519.PublicKey
	Sign(ctx context.Context, message []byte) ([]byte, error)
}

// KeyID is the lowercase hex encoding of the signer's raw public key, the
// same key_id convention the record envelopes and checkpoints use.
func KeyID(s Signer) string {
	return hex.EncodeToString(s.PublicKey())
}

// Ed25519Signer is the reference in-process Signer.
type Ed25519Signer struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
}

// NewEd25519Signer copies private so later mutation by the caller has no effect.
func NewEd25519Signer(private ed25519.PrivateKey) (*Ed25519Signer, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: Ed25519 private key has wrong size", ErrInvalid)
	}
	seeded := ed25519.NewKeyFromSeed(private.Seed())
	return &Ed25519Signer{private: seeded, public: append(ed25519.PublicKey(nil), seeded[ed25519.SeedSize:]...)}, nil
}

// PublicKey returns a copy of the verifying key.
func (s *Ed25519Signer) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), s.public...)
}

// Sign returns the Ed25519 signature over message.
func (s *Ed25519Signer) Sign(ctx context.Context, message []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ed25519.Sign(s.private, message), nil
}

// coseSigner adapts a Signer to go-cose so capsule-emit-go can build Producer
// Envelopes without the book ever seeing a private key.
type coseSigner struct {
	ctx    context.Context
	signer Signer
}

func (c coseSigner) Algorithm() cose.Algorithm { return cose.AlgorithmEdDSA }

func (c coseSigner) Sign(_ io.Reader, content []byte) ([]byte, error) {
	return c.signer.Sign(c.ctx, content)
}

func signingIdentity(ctx context.Context, signer Signer) (emit.SigningIdentity, error) {
	return emit.NewSigningIdentity(coseSigner{ctx: ctx, signer: signer}, signer.PublicKey())
}

func verifySignature(publicKeyHex string, message, signature []byte) error {
	public, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: key_id is not an Ed25519 public key", ErrInvalid)
	}
	if !ed25519.Verify(public, message, signature) {
		return fmt.Errorf("%w: signature does not verify", ErrInvalid)
	}
	return nil
}
