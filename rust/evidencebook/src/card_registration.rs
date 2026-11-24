//! Registering a Standard History card on a SCITT transparency service
//! (agent-action-capsule `spec/standard-history-v0.md` §13, cited as SH).
//!
//! A port of the Go `card_registration.go`, byte for byte: the same card
//! statement octets and the same checks in the same order. The signer is the
//! one the crate's answer path takes (`evidence_request::envelope::Signer`),
//! since the holder key is the envelope's responder. Decoding is by
//! `ciborium`, which is stricter than Go's decoder on a malformed statement (a
//! null `unprotected`, a null `receipts` value, a repeated `receipts` label
//! or a tag inside the statement is refused as not a card statement); a
//! well-formed statement is accepted and re-encoded identically by both.
//!
//! Registration is opt-in and has no default: nothing here names or reaches a
//! service, and the crate has no network client. The host supplies a
//! [`CardStatementRegistrar`] (its own client for the service it chose) and the
//! relying party's transparency policy. What a registration shows is existence
//! at a time and nothing else (SH §13): never that the counts are right, never
//! `anchor_inclusion` (it stays `NOT_CHECKED` for a card), never that the
//! holder registered no other card. A failed registration changes nothing
//! about the card, and carriage of the card statement beside the card is the
//! host's (SH §13.3): a card statement is never an entry of an envelope's
//! verification.
//!
//! A service the holder operates confers nothing (SH §13.2), and a card
//! statement's `iss` and `sub` tell the service, and anyone who reads its log,
//! which holder key registered a card for which anchor and when. The log is
//! append-only, so a registration can never be withdrawn, or the link it makes
//! between the key and the anchor (SH §13.5). The statement carries the card's
//! digest, not the card, but a digest does not hide a card from a party that
//! can guess it: a party that holds the anchor, the policy digest and `since`
//! can test guesses of the counts of a small card against it. A holder
//! registers knowing that, and only on a service whose published policy it
//! accepts. Not registering is no finding against a card or its holder.

use agent_action_capsule::witness::check_card_statement;
use evidence_request::envelope::{Envelope, Signer};
use serde_json::Value;
use sha2::{Digest, Sha256};

pub use agent_action_capsule::witness::{
    CardRegisteredReport, CardStatementReport, CARD_REGISTERED_METHOD, CARD_REGISTERED_PROPERTY,
};

const COSE_SIGN1_TAG: u64 = 18;
const COSE_LABEL_RECEIPTS: i128 = 394;
/// The most Receipts one card statement holds (SH §13.1).
pub const MAX_CARD_RECEIPTS: usize = 16;
/// The most octets a card statement is (SH §13.1).
pub const CARD_STATEMENT_MAX_LEN: usize = 10_485_760;

/// Why a card statement was not built, attached or registered.
#[derive(Debug, thiserror::Error)]
pub enum CardRegistrationError {
    /// The input is not acceptable: the message says what.
    #[error("invalid: {0}")]
    Invalid(String),
    /// The signer failed.
    #[error("signing failed: {0}")]
    Sign(String),
    /// The host's registrar failed (the service is down, say). The card is
    /// exactly as it was; the registrar's error is the source.
    #[error("card registration: {0}")]
    Registrar(#[source] Box<dyn std::error::Error + Send + Sync>),
}

fn invalid<T>(message: impl Into<String>) -> Result<T, CardRegistrationError> {
    Err(CardRegistrationError::Invalid(message.into()))
}

/// The host's client for the one transparency service it chose.
/// `register_statement` posts the card statement's octets and returns the
/// octets of the Receipt the service issued for it. The crate reaches no
/// service itself and has no default one.
pub trait CardStatementRegistrar {
    fn register_statement(
        &self,
        statement: &[u8],
    ) -> Result<Vec<u8>, Box<dyn std::error::Error + Send + Sync>>;
}

// ---- Core deterministic CBOR (RFC 8949 §4.2.1), just what a card statement
// needs: unsigned and negative integers, byte and text strings, definite
// arrays and maps with keys sorted by their encoding. The Go side encodes with
// fxamacker's CoreDetEncOptions; this writes the same octets.

fn head(out: &mut Vec<u8>, major: u8, value: u64) {
    let m = major << 5;
    if value < 24 {
        out.push(m | value as u8);
    } else if value <= 0xff {
        out.extend([m | 24, value as u8]);
    } else if value <= 0xffff {
        out.push(m | 25);
        out.extend((value as u16).to_be_bytes());
    } else if value <= 0xffff_ffff {
        out.push(m | 26);
        out.extend((value as u32).to_be_bytes());
    } else {
        out.push(m | 27);
        out.extend(value.to_be_bytes());
    }
}

fn uint(out: &mut Vec<u8>, v: u64) {
    head(out, 0, v);
}

fn nint(out: &mut Vec<u8>, v: i64) {
    debug_assert!(v < 0);
    head(out, 1, (-1 - v) as u64);
}

fn bstr(out: &mut Vec<u8>, b: &[u8]) {
    head(out, 2, b.len() as u64);
    out.extend(b);
}

fn tstr(out: &mut Vec<u8>, s: &str) {
    head(out, 3, s.len() as u64);
    out.extend(s.as_bytes());
}

/// The protected header: `{1: -8, 15: {1: iss, 2: sub}}`, keys in order.
fn protected_header(iss: &str, sub: &str) -> Vec<u8> {
    let mut out = Vec::new();
    head(&mut out, 5, 2);
    uint(&mut out, 1);
    nint(&mut out, -8);
    uint(&mut out, 15);
    head(&mut out, 5, 2);
    uint(&mut out, 1);
    tstr(&mut out, iss);
    uint(&mut out, 2);
    tstr(&mut out, sub);
    out
}

/// The COSE Sig_structure the signature covers (RFC 9052 §4.4): the octets
/// whose SHA-256 is the statement entry E.
fn sig_structure(protected: &[u8], payload: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    head(&mut out, 4, 4);
    tstr(&mut out, "Signature1");
    bstr(&mut out, protected);
    bstr(&mut out, &[]);
    bstr(&mut out, payload);
    out
}

/// A card statement's parts, as carried.
struct Sign1 {
    protected: Vec<u8>,
    receipts: Vec<Option<Vec<u8>>>,
    payload: Vec<u8>,
    signature: Vec<u8>,
}

/// Tag 18 around the array of four, the unprotected map holding the
/// `receipts` label only when there is a Receipt (an empty map otherwise).
fn encode_statement(s: &Sign1) -> Vec<u8> {
    let mut out = Vec::new();
    head(&mut out, 6, COSE_SIGN1_TAG);
    head(&mut out, 4, 4);
    bstr(&mut out, &s.protected);
    if s.receipts.is_empty() {
        head(&mut out, 5, 0);
    } else {
        head(&mut out, 5, 1);
        uint(&mut out, COSE_LABEL_RECEIPTS as u64);
        head(&mut out, 4, s.receipts.len() as u64);
        for r in &s.receipts {
            match r {
                Some(r) => bstr(&mut out, r),
                // Go decodes a null Receipt to a nil byte string and writes
                // it back as null; the whole-statement check then refuses it.
                None => out.push(0xf6),
            }
        }
    }
    bstr(&mut out, &s.payload);
    bstr(&mut out, &s.signature);
    out
}

fn parse_statement(statement: &[u8]) -> Result<Sign1, CardRegistrationError> {
    use ciborium::value::Value as Cbor;
    let mut rest = statement;
    let value: Cbor = ciborium::de::from_reader(&mut rest).map_err(|_| {
        CardRegistrationError::Invalid("not a card statement (tag 18 around a COSE_Sign1)".into())
    })?;
    if !rest.is_empty() {
        return invalid("not a card statement (tag 18 around a COSE_Sign1)");
    }
    let Cbor::Tag(COSE_SIGN1_TAG, inner) = value else {
        return invalid("not a card statement (tag 18 around a COSE_Sign1)");
    };
    let not = || CardRegistrationError::Invalid("not a card statement".into());
    let Cbor::Array(items) = *inner else {
        return Err(not());
    };
    let [protected, unprotected, payload, signature]: [Cbor; 4] =
        items.try_into().map_err(|_| not())?;
    let (Cbor::Bytes(protected), Cbor::Bytes(payload), Cbor::Bytes(signature)) =
        (protected, payload, signature)
    else {
        return Err(not());
    };
    let Cbor::Map(entries) = unprotected else {
        return Err(not());
    };
    let mut receipts = Vec::new();
    let mut seen = false;
    for (label, value) in entries {
        let Cbor::Integer(label) = label else {
            return Err(not());
        };
        if i128::from(label) != COSE_LABEL_RECEIPTS {
            return invalid("the card statement has an unprotected label besides receipts");
        }
        if seen {
            return Err(not());
        }
        seen = true;
        let Cbor::Array(list) = value else {
            return Err(not());
        };
        for r in list {
            receipts.push(match r {
                Cbor::Bytes(r) => Some(r),
                Cbor::Null => None,
                _ => return Err(not()),
            });
        }
    }
    Ok(Sign1 {
        protected,
        receipts,
        payload,
        signature,
    })
}

fn is_hex64(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f'))
}

/// Signs a card statement (SH §13.1) over `card`, the card's octets, as of
/// `anchor` (the card's anchor digest, which is the envelope's
/// `anchor.digest`). The statement carries no Receipt yet: its unprotected
/// map is empty, which is the form to submit to a service. A Receipt added
/// later changes neither the statement entry nor the signature.
pub fn build_card_statement(
    signer: &dyn Signer,
    card: &[u8],
    anchor: &str,
) -> Result<Vec<u8>, CardRegistrationError> {
    if card.is_empty() {
        return invalid("a card statement needs the card octets");
    }
    if !is_hex64(anchor) {
        return invalid("the anchor is not 64 lowercase hex characters");
    }
    let protected = protected_header(&signer.key_id(), anchor);
    let digest = Sha256::digest(card).to_vec();
    let signature = signer
        .sign(&sig_structure(&protected, &digest))
        .map_err(CardRegistrationError::Sign)?;
    Ok(encode_statement(&Sign1 {
        protected,
        receipts: Vec::new(),
        payload: digest,
        signature: signature.to_vec(),
    }))
}

/// Adds `receipt`, the octets of one service Receipt, to `statement` and
/// returns the statement with it. It first checks the Receipt alone, on the
/// statement, under the relying party's transparency policy with the card,
/// the holder key identifier and the anchor, and refuses a Receipt that does
/// not make that statement PASS, so only a Receipt of a listed service over
/// this statement is ever kept, even beside Receipts that pass. A card
/// statement holds 1 to 16 Receipts (SH §13.1); a seventeenth is refused, and
/// a holder registers a second statement instead.
pub fn attach_card_receipt(
    card: &[u8],
    holder: &str,
    anchor: &str,
    statement: &[u8],
    receipt: &[u8],
    policy: &Value,
) -> Result<Vec<u8>, CardRegistrationError> {
    if statement.len() > CARD_STATEMENT_MAX_LEN {
        return invalid(format!(
            "the card statement is over {CARD_STATEMENT_MAX_LEN} octets"
        ));
    }
    let mut parsed = parse_statement(statement)?;
    if parsed.receipts.len() >= MAX_CARD_RECEIPTS {
        return invalid(format!(
            "a card statement holds at most {MAX_CARD_RECEIPTS} Receipts"
        ));
    }
    // The new Receipt is checked alone: card_registered passes when any one
    // Receipt does, so checking the whole statement would let a Receipt that
    // passes hide one that does not.
    let existing = std::mem::take(&mut parsed.receipts);
    parsed.receipts = vec![Some(receipt.to_vec())];
    let alone = encode_statement(&parsed);
    let report = check_card_statement(card, holder, anchor, &alone, policy);
    if report.status != "PASS" {
        return invalid(format!(
            "the Receipt does not make the card statement pass ({})",
            report.status
        ));
    }
    parsed.receipts = existing;
    parsed.receipts.push(Some(receipt.to_vec()));
    let candidate = encode_statement(&parsed);
    if candidate.len() > CARD_STATEMENT_MAX_LEN {
        return invalid(format!(
            "the card statement would be over {CARD_STATEMENT_MAX_LEN} octets"
        ));
    }
    // The whole statement must still be in the one form of SH §13.1, which an
    // earlier Receipt that is not a byte string, say, would break.
    let report = check_card_statement(card, holder, anchor, &candidate, policy);
    if report.status != "PASS" {
        return invalid(format!(
            "the card statement would not pass ({})",
            report.status
        ));
    }
    Ok(candidate)
}

/// Builds the card statement for `card`, the artifact of `envelope` (the
/// answer it was derived in), has the host's `registrar` post it to the
/// service the host chose, and returns the statement with the service's
/// Receipt attached, checked under `policy`. The signer must be the envelope's
/// responder: a statement under another key would be logged for good, append
/// only, and then fail SH §13.3 step 2 at every reader, so it is refused
/// before anything is posted. It is a call of its own: a card is complete
/// without it, and an error here (the service is down, or its Receipt does not
/// pass) leaves the card exactly as it was. The host keeps the returned octets
/// beside the card (SH §13.3).
///
/// There is no default registrar: a host that configured no service does not
/// call this.
pub fn register_card(
    signer: &dyn Signer,
    card: &[u8],
    envelope: &Envelope,
    registrar: &dyn CardStatementRegistrar,
    policy: &Value,
) -> Result<Vec<u8>, CardRegistrationError> {
    if signer.key_id() != envelope.responder {
        return invalid("the signer is not the envelope's responder, the holder key");
    }
    let statement = build_card_statement(signer, card, &envelope.anchor.digest)?;
    let receipt = registrar
        .register_statement(&statement)
        .map_err(CardRegistrationError::Registrar)?;
    attach_card_receipt(
        card,
        &envelope.responder,
        &envelope.anchor.digest,
        &statement,
        &receipt,
        policy,
    )
}

/// `card_registered` (SH §13.3) over the card statements received beside
/// `card`, the octets of the card's artifact, under the envelope's responder
/// (the holder key) and anchor and the relying party's transparency policy.
/// `NOT_PRESENT` when there is no statement. A verifier may say a `PASS`
/// means the holder registered this card with the service named, as the
/// policy names it, at the registration time (or with no time when the
/// Receipt has none); it must not say the service checked the counts, the
/// deals or the anchor, or that this is the holder's only card (SH §13.4).
/// It is reported beside the card properties, never as one, and never changes
/// a level or `anchor_inclusion`.
pub fn check_card_registered(
    card: &[u8],
    envelope: &Envelope,
    statements: &[Vec<u8>],
    policy: &Value,
) -> CardRegisteredReport {
    agent_action_capsule::witness::check_card_registered(
        card,
        &envelope.responder,
        &envelope.anchor.digest,
        statements,
        policy,
    )
}
