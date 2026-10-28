//! Ed25519 verification, strict, for every signature this crate checks.
//!
//! ed25519-dalek's `verify` accepts a signature by a small-order public key,
//! which anyone can make without the private key (checkpointed-local-log#108),
//! and checkpointed-local-log 0.3.1 verifies checkpoints that way. Every check
//! here refuses a non-canonical A or R (y >= p), then calls `verify_strict`,
//! which refuses small-order A and R and S >= L and checks the cofactorless
//! equation. That is the rule of checkpointed-local-log's shared
//! `checkpoint-conformance-vectors/ed25519-vectors.json`, which the tests run.

use ciborium::Value as Cbor;
use ed25519_dalek::{Signature, VerifyingKey};

/// The COSE_Sign1 tag (RFC 9052 §4.2).
pub(crate) const COSE_SIGN1_TAG: u64 = 18;
/// The COSE header label of the key id.
const COSE_KID: i64 = 4;

/// Whether a 32-byte point encoding has y < p (RFC 8032 §5.1.3).
fn canonical_point(encoded: &[u8; 32]) -> bool {
    !(encoded[0] >= 0xed && encoded[1..31].iter().all(|&b| b == 0xff) && encoded[31] & 0x7f == 0x7f)
}

/// Whether `signature` is `public_key`'s signature over exactly `message`.
pub(crate) fn verify(public_key: &[u8; 32], message: &[u8], signature: &[u8; 64]) -> bool {
    let r: &[u8; 32] = signature[..32].try_into().expect("64 bytes hold 32");
    if !canonical_point(public_key) || !canonical_point(r) {
        return false;
    }
    let Ok(key) = VerifyingKey::from_bytes(public_key) else {
        return false;
    };
    key.verify_strict(message, &Signature::from_bytes(signature))
        .is_ok()
}

/// The four members of a tagged COSE_Sign1 message: the protected header's
/// octets, the unprotected header, the payload and the signature.
pub(crate) struct Sign1 {
    pub protected: Vec<u8>,
    pub unprotected: Vec<(Cbor, Cbor)>,
    pub payload: Vec<u8>,
    pub signature: [u8; 64],
}

impl Sign1 {
    /// Read a tagged COSE_Sign1 message with an attached payload and a
    /// 64-byte signature, and nothing after it; anything else is `None`.
    pub fn decode(octets: &[u8]) -> Option<Sign1> {
        let mut rest = octets;
        let Ok(Cbor::Tag(COSE_SIGN1_TAG, inner)) = ciborium::from_reader::<Cbor, _>(&mut rest)
        else {
            return None;
        };
        if !rest.is_empty() {
            return None;
        }
        let Cbor::Array(parts) = *inner else {
            return None;
        };
        let [Cbor::Bytes(protected), Cbor::Map(unprotected), Cbor::Bytes(payload), Cbor::Bytes(signature)] =
            <[Cbor; 4]>::try_from(parts).ok()?
        else {
            return None;
        };
        Some(Sign1 {
            protected,
            unprotected,
            payload,
            signature: signature.try_into().ok()?,
        })
    }

    /// The `Sig_structure` this message's signature covers (RFC 9052 §4.4),
    /// with no external data.
    pub fn to_be_signed(&self) -> Vec<u8> {
        to_be_signed(&self.protected, &self.payload)
    }

    /// The protected header's `kid` (label 4), when it is 32 bytes.
    pub fn kid(&self) -> Option<[u8; 32]> {
        let Ok(Cbor::Map(entries)) = ciborium::from_reader::<Cbor, _>(self.protected.as_slice())
        else {
            return None;
        };
        entries
            .into_iter()
            .find_map(|(label, value)| match (label, value) {
                (Cbor::Integer(l), Cbor::Bytes(kid)) if i128::from(l) == i128::from(COSE_KID) => {
                    kid.try_into().ok()
                }
                _ => None,
            })
    }

    /// Whether the signature verifies, strictly, under `public_key`.
    pub fn verifies_under(&self, public_key: &[u8; 32]) -> bool {
        verify(public_key, &self.to_be_signed(), &self.signature)
    }
}

/// `["Signature1", protected, h'', payload]` in CBOR.
pub(crate) fn to_be_signed(protected: &[u8], payload: &[u8]) -> Vec<u8> {
    encode(&Cbor::Array(vec![
        Cbor::Text("Signature1".into()),
        Cbor::Bytes(protected.to_vec()),
        Cbor::Bytes(Vec::new()),
        Cbor::Bytes(payload.to_vec()),
    ]))
}

/// CBOR octets of `value`: definite lengths, shortest heads, entries in the
/// order given.
pub(crate) fn encode(value: &Cbor) -> Vec<u8> {
    let mut out = Vec::new();
    ciborium::into_writer(value, &mut out).expect("writing to a Vec never fails");
    out
}

/// Whether a COSE_Sign1 checkpoint statement verifies, strictly, under the
/// key its own protected `kid` names.
pub(crate) fn statement_verifies(statement: &[u8]) -> bool {
    Sign1::decode(statement).is_some_and(|m| m.kid().is_some_and(|kid| m.verifies_under(&kid)))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::Value;

    /// checkpointed-local-log `checkpoint-conformance-vectors/ed25519-vectors.json`
    /// as of c45422a (unchanged through 7d71a1c), vendored with its SHA-256.
    const VECTORS: &str = include_str!("../tests/ed25519_vectors_c45422a.json");
    const VECTORS_SHA256: &str = "2394c8ab93291f3945166624181ba5be34df3d89c80768b8b5f15604692503f3";

    fn bytes<const N: usize>(case: &Value, field: &str) -> [u8; N] {
        hex::decode(case[field].as_str().unwrap())
            .unwrap()
            .try_into()
            .unwrap()
    }

    #[test]
    fn the_vendored_vectors_are_upstreams() {
        use sha2::{Digest, Sha256};
        assert_eq!(hex::encode(Sha256::digest(VECTORS)), VECTORS_SHA256);
    }

    #[test]
    fn decides_every_shared_raw_case() {
        let vectors: Value = serde_json::from_str(VECTORS).unwrap();
        let cases = vectors["ed25519_cases"].as_array().unwrap();
        assert_eq!(
            cases.len() as u64,
            vectors["ed25519_count"].as_u64().unwrap()
        );
        for case in cases {
            let message = hex::decode(case["message_hex"].as_str().unwrap()).unwrap();
            assert_eq!(
                verify(
                    &bytes(case, "public_key_hex"),
                    &message,
                    &bytes(case, "signature_hex")
                ),
                case["valid"].as_bool().unwrap(),
                "{}",
                case["name"]
            );
        }
    }

    #[test]
    fn decides_every_shared_checkpoint_statement() {
        let vectors: Value = serde_json::from_str(VECTORS).unwrap();
        let cases = vectors["cose_cases"].as_array().unwrap();
        assert_eq!(cases.len() as u64, vectors["cose_count"].as_u64().unwrap());
        assert!(!cases.is_empty());
        for case in cases {
            let statement = hex::decode(case["cose_hex"].as_str().unwrap()).unwrap();
            assert_eq!(
                statement_verifies(&statement),
                case["valid"].as_bool().unwrap(),
                "{}",
                case["name"]
            );
        }
    }
}
