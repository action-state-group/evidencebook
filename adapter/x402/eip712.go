// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// keccak256 is Ethereum's Keccak-256 (the pre-standard padding, not SHA3-256).
func keccak256(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// address is a 20-byte EVM account or contract address.
type address [20]byte

func (a address) String() string { return "0x" + hex.EncodeToString(a[:]) }

// parseAddress reads 0x followed by exactly 40 hex digits, either case.
// The EIP-55 mixed-case checksum is not enforced: EIP-712 signs the 20
// bytes, not the text.
func parseAddress(s string) (address, error) {
	var a address
	if len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return a, fmt.Errorf("%q is not 0x and 40 hex digits", s)
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return a, fmt.Errorf("%q is not 0x and 40 hex digits", s)
	}
	copy(a[:], b)
	return a, nil
}

var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// parseUint256 reads an unsigned decimal integer string (digits only) that
// fits in 256 bits.
func parseUint256(s string) (*big.Int, error) {
	if s == "" {
		return nil, errors.New("empty integer")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("%q is not an unsigned decimal integer", s)
		}
	}
	n, _ := new(big.Int).SetString(s, 10)
	if n.Cmp(maxUint256) > 0 {
		return nil, fmt.Errorf("%q exceeds uint256", s)
	}
	return n, nil
}

// parseBytes32 reads 0x followed by exactly 64 hex digits.
func parseBytes32(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return out, fmt.Errorf("%q is not 0x and 64 hex digits", s)
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return out, fmt.Errorf("%q is not 0x and 64 hex digits", s)
	}
	copy(out[:], b)
	return out, nil
}

// parseHexBytes reads 0x followed by an even number of hex digits.
func parseHexBytes(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") {
		return nil, fmt.Errorf("signature is not 0x-prefixed hex")
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("signature is not 0x-prefixed hex")
	}
	return b, nil
}

// ABI encodings of EIP-712 atomic values: each is one 32-byte word.

func wordUint(n *big.Int) []byte {
	var w [32]byte
	n.FillBytes(w[:])
	return w[:]
}

func wordAddress(a address) []byte {
	var w [32]byte
	copy(w[12:], a[:])
	return w[:]
}

func wordString(s string) []byte { return keccak256([]byte(s)) }

// typedDataDigest is keccak256(0x19 0x01 || domainSeparator || structHash)
// (EIP-712).
func typedDataDigest(domainSeparator, structHash []byte) []byte {
	return keccak256([]byte{0x19, 0x01}, domainSeparator, structHash)
}

const (
	typeEIP712DomainFull    = "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
	typeEIP712DomainNoVC    = "EIP712Domain(string name,string version,uint256 chainId)"
	typeTransferWithAuth    = "TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"
	typeReceiptV1           = "Receipt(uint256 version,string network,string resourceUrl,string payer,uint256 issuedAt,string transaction)"
	typeReceiptV2           = "Receipt(uint256 version,string network,string resourceUrl,string payer,uint256 issuedAt,string transaction,string responseHash,string responseHashAlg,string responseHashEncoding)"
	receiptDomainName       = "x402 receipt"
	receiptDomainVersion    = "1"
	receiptDomainChainIDInt = 1
)

// tokenDomainSeparator is the EIP-712 domain of an EIP-3009 token: name,
// version, chainId, verifyingContract.
func tokenDomainSeparator(name, version string, chainID *big.Int, contract address) []byte {
	return keccak256(
		keccak256([]byte(typeEIP712DomainFull)),
		wordString(name),
		wordString(version),
		wordUint(chainID),
		wordAddress(contract),
	)
}

// authorization is an EIP-3009 TransferWithAuthorization message, decoded
// from the transmitted strings without loss.
type authorization struct {
	From, To                       address
	Value, ValidAfter, ValidBefore *big.Int
	Nonce                          [32]byte
}

func (m authorization) structHash() []byte {
	return keccak256(
		keccak256([]byte(typeTransferWithAuth)),
		wordAddress(m.From),
		wordAddress(m.To),
		wordUint(m.Value),
		wordUint(m.ValidAfter),
		wordUint(m.ValidBefore),
		m.Nonce[:],
	)
}

// secp256k1HalfOrder is n/2; a signature with s above it is the malleable
// twin and is rejected by the EIP-3009 token contracts' signature recovery.
var secp256k1HalfOrder = new(big.Int).Rsh(secp256k1.S256().N, 1)

// errHighS and errBadV mark signatures that are well formed bytes but that
// an EVM verifier rejects.
var (
	errHighS = errors.New("s is in the upper half of the curve order (malleable form)")
	errBadV  = errors.New("v is not 27, 28, 0 or 1")
)

// recoverAddress recovers the signer's address from a 65-byte r||s||v
// signature over digest. v is 27 or 28 (0 or 1 is accepted and read as 27 or
// 28, as the x402 reference verifier does).
func recoverAddress(digest, sig []byte) (address, error) {
	var a address
	if len(sig) != 65 {
		return a, fmt.Errorf("signature is %d bytes, not 65", len(sig))
	}
	v := sig[64]
	if v < 27 {
		v += 27
	}
	if v != 27 && v != 28 {
		return a, errBadV
	}
	if new(big.Int).SetBytes(sig[32:64]).Cmp(secp256k1HalfOrder) > 0 {
		return a, errHighS
	}
	compact := make([]byte, 65)
	compact[0] = v // 27 + recovery id, uncompressed key
	copy(compact[1:], sig[:64])
	pub, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return a, err
	}
	return pubKeyAddress(pub), nil
}

// pubKeyAddress is the last 20 bytes of keccak256 of the 64-byte
// uncompressed public key.
func pubKeyAddress(pub *secp256k1.PublicKey) address {
	var a address
	copy(a[:], keccak256(pub.SerializeUncompressed()[1:])[12:])
	return a
}
