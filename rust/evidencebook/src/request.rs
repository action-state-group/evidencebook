//! Request subjects, the three outcomes of a request, and the three kinds of
//! "no".
//!
//! I-D sections: *Answering a Request*, *Three Kinds of "No"*. Subject forms,
//! outcomes and refusal reasons are the tokens of the companion request draft
//! (`draft-mih-agent-evidence-request-00`); this module does not restate that
//! draft's rules about coverage anchors. The draft's registry and refusal
//! profile come from the `evidence-request` crate (its protocol core); this
//! module keeps evidencebook's own names and the evidence layer's concepts
//! ([`Outcome`], [`NoKind`]) over it.

use ed25519_dalek::VerifyingKey;
use evidence_request::registry::Reason;
use serde::{Deserialize, Serialize};

/// The six subject forms a request can name, with the store capability that
/// answers each (I-D *Answering a Request*).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SubjectKind {
    /// One record by id or digest: operational or authenticated index.
    Record,
    /// Records at stated positions, with inclusion or range proof.
    Range,
    /// Every record whose correlation field matches: operational index.
    Correlation,
    /// Every record citing the named interaction half's digest. The wire
    /// token is fixed by the request draft's subject registry and is part of
    /// the public serialized form, so it cannot be renamed here; the variant
    /// carries this crate's own name, and the vocabulary gate exempts exactly
    /// this token on the lines marked below.
    #[serde(rename = "exchange")] // vocabulary-gate: request-draft wire token
    CitingRecords,
    /// The store's whole evidence body, subject to disclosure policy.
    FullHistory,
    /// The substrate's checkpoint identity and receipts; no record lookup.
    Checkpoints,
}

impl SubjectKind {
    pub const ALL: [SubjectKind; 6] = [
        SubjectKind::Record,
        SubjectKind::Range,
        SubjectKind::Correlation,
        SubjectKind::CitingRecords,
        SubjectKind::FullHistory,
        SubjectKind::Checkpoints,
    ];

    /// Whether answering this subject resolves records (every form except
    /// `checkpoints`).
    pub fn resolves_records(self) -> bool {
        self != SubjectKind::Checkpoints
    }
}

/// The three mutually exclusive outcomes of one request. A responder grants
/// or refuses; recorded absence is the requester's own record that nothing
/// arrived in its window, never something a responder sends.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    /// A signed artifact response.
    Artifact,
    /// A signed refusal naming a reason token.
    Refusal,
    /// The requester's own signed record of non-arrival.
    RecordedAbsence,
}

/// The three different things "no evidence for this subject" can mean (I-D
/// *Three Kinds of "No"*). Only [`NoKind::NonMembership`] is checkable by a
/// party other than the store.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum NoKind {
    /// An authenticated-index proof that nothing occupies the position or
    /// carries the identity.
    NonMembership,
    /// It exists and is not disclosed under current policy. Not an absence.
    Withheld,
    /// The store's own unproven claim that it holds nothing. A
    /// `producer_claim`, never proof.
    AssertedAbsence,
}

impl NoKind {
    /// Whether a party that does not trust the store can check this "no".
    pub fn is_checkable(self) -> bool {
        self == NoKind::NonMembership
    }
}

/// Refusal reason tokens (the request draft's registry).
pub mod reasons {
    pub const NOT_AUTHORIZED: &str = "not_authorized";
    pub const NO_SUCH_SUBJECT: &str = "no_such_subject";
    pub const COVERAGE_UNSATISFIABLE: &str = "coverage_unsatisfiable";
    pub const DERIVATION_UNSUPPORTED: &str = "derivation_unsupported";
    pub const POLICY_DECLINED: &str = "policy_declined";
    pub const DEADLINE_UNMET: &str = "deadline_unmet";
    pub const REQUEST_MALFORMED: &str = "request_malformed";
    pub const RETENTION_EXPIRED: &str = "retention_expired";
    /// -02: a limit of the responder's own (load, size, a transient fault).
    pub const CAPACITY_EXCEEDED: &str = "capacity_exceeded";
    /// -02: a requester authentication a deployment profile defines does not verify.
    pub const REQUEST_UNAUTHENTICATED: &str = "request_unauthenticated";

    /// The registry's tokens: the eight of -00 and the two -02 adds.
    pub const ALL: [&str; 10] = [
        NOT_AUTHORIZED,
        NO_SUCH_SUBJECT,
        COVERAGE_UNSATISFIABLE,
        DERIVATION_UNSUPPORTED,
        POLICY_DECLINED,
        DEADLINE_UNMET,
        REQUEST_MALFORMED,
        RETENTION_EXPIRED,
        CAPACITY_EXCEEDED,
        REQUEST_UNAUTHENTICATED,
    ];
}

/// A signed refusal of one request. The signature covers the sorted, compact
/// JSON of `issued_at`, `reason` and `request_digest` -- the body the Python
/// and Go implementations sign (`tests/refusal_interop.rs` verifies a refusal
/// signed by the Python one).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Refusal {
    pub request_digest: String,
    pub reason: String,
    pub issued_at: String,
    /// Raw Ed25519 public key, hex.
    pub key_id: String,
    /// Ed25519 signature over [`Refusal::signing_body`], hex.
    pub sig: String,
}

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum RefusalError {
    #[error("refusal key_id is not a raw Ed25519 public key in hex")]
    BadKey,
    #[error("refusal signature is not 64 bytes of hex")]
    BadSignature,
    #[error("refusal signature does not verify under its key_id")]
    Unverified,
}

impl Refusal {
    fn object(&self) -> serde_json::Map<String, serde_json::Value> {
        match serde_json::to_value(self).expect("five string members serialize") {
            serde_json::Value::Object(map) => map,
            _ => unreachable!("a struct serializes to an object"),
        }
    }

    /// The exact bytes a refusal signature covers: RFC 8785 of `issued_at`,
    /// `reason` and `request_digest` (`evidence_request::refusal::signing_body`).
    pub fn signing_body(&self) -> Vec<u8> {
        evidence_request::refusal::signing_body(&self.object())
            .expect("three string members always canonicalize")
            .into_bytes()
    }

    /// Whether `reason` is a registered token under `evidence-interop/0`, the
    /// profile this crate asks under: the eight tokens of
    /// ER -01 (`envelope::refusal_reasons`), not the ten of ER -02, which
    /// only `evidence-interop/1` registers. Separate from
    /// [`Refusal::verify`], which, like the Go and Python verifiers, checks
    /// only the signature.
    pub fn reason_is_registered(&self) -> bool {
        self.reason_is_registered_under(evidence_request::envelope::PROFILE)
    }

    /// Whether `reason` is a registered token under `profile`, the profile
    /// the refused request asked for (v1 §6.3): the eight of
    /// `evidence-interop/0`, the ten of `evidence-interop/1`, and for a
    /// profile `evidence-request` does not implement, the ten
    /// (`envelope::refusal_reasons`). The book's responder refuses a /1
    /// request `capacity_exceeded`, which only this reads as registered.
    pub fn reason_is_registered_under(&self, profile: &str) -> bool {
        Reason::from_token(&self.reason)
            .is_some_and(|r| evidence_request::envelope::refusal_reasons(profile).contains(&r))
    }

    /// Check the signature, offline, against the refusal's own `key_id`
    /// (`evidence_request::refusal::check`: Ed25519, strict verification).
    pub fn verify(&self) -> Result<(), RefusalError> {
        let key: [u8; 32] = hex::decode(&self.key_id)
            .ok()
            .and_then(|b| b.try_into().ok())
            .ok_or(RefusalError::BadKey)?;
        VerifyingKey::from_bytes(&key).map_err(|_| RefusalError::BadKey)?;
        hex::decode(&self.sig)
            .ok()
            .filter(|b| b.len() == 64)
            .ok_or(RefusalError::BadSignature)?;
        let check = evidence_request::refusal::check(&serde_json::Value::Object(self.object()));
        if check.signature_valid {
            Ok(())
        } else {
            Err(RefusalError::Unverified)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{Signer, SigningKey};

    fn signed(reason: &str) -> Refusal {
        let key = SigningKey::from_bytes(&[3u8; 32]);
        let mut r = Refusal {
            request_digest: "ab".repeat(32),
            reason: reason.into(),
            issued_at: "2026-09-27T00:00:00Z".into(),
            key_id: hex::encode(key.verifying_key().to_bytes()),
            sig: String::new(),
        };
        r.sig = hex::encode(key.sign(&r.signing_body()).to_bytes());
        r
    }

    #[test]
    fn subject_tokens_are_the_request_draft_tokens() {
        let tokens: Vec<String> = SubjectKind::ALL
            .iter()
            .map(|s| {
                serde_json::to_value(s)
                    .unwrap()
                    .as_str()
                    .unwrap()
                    .to_string()
            })
            .collect();
        assert_eq!(
            tokens,
            [
                "record",
                "range",
                "correlation",
                "exchange", // vocabulary-gate: request-draft wire token
                "full_history",
                "checkpoints"
            ]
        );
        assert!(!SubjectKind::Checkpoints.resolves_records());
        assert!(SubjectKind::ALL[..5].iter().all(|s| s.resolves_records()));
    }

    #[test]
    fn subject_kinds_are_the_registry_forms() {
        use evidence_request::registry::SubjectForm;
        let mut ours: Vec<String> = SubjectKind::ALL
            .iter()
            .map(|s| {
                serde_json::to_value(s)
                    .unwrap()
                    .as_str()
                    .unwrap()
                    .to_string()
            })
            .collect();
        assert!(ours.iter().all(|t| SubjectForm::from_token(t).is_some()));
        let mut registry: Vec<String> = SubjectForm::ALL
            .iter()
            .map(|f| f.token().to_string())
            .collect();
        ours.sort_unstable();
        registry.sort_unstable();
        assert_eq!(ours, registry);
    }

    #[test]
    fn reason_tokens_are_the_registry_tokens() {
        let mut registry: Vec<&str> = Reason::ALL.iter().map(|r| r.token()).collect();
        let mut ours = reasons::ALL.to_vec();
        registry.sort_unstable();
        ours.sort_unstable();
        assert_eq!(ours, registry);
    }

    #[test]
    fn only_non_membership_is_checkable() {
        assert!(NoKind::NonMembership.is_checkable());
        assert!(!NoKind::Withheld.is_checkable());
        assert!(!NoKind::AssertedAbsence.is_checkable());
    }

    #[test]
    fn signing_body_is_sorted_compact_json() {
        let r = signed(reasons::NO_SUCH_SUBJECT);
        assert_eq!(
            String::from_utf8(r.signing_body()).unwrap(),
            format!(
                "{{\"issued_at\":\"2026-09-27T00:00:00Z\",\"reason\":\"no_such_subject\",\"request_digest\":\"{}\"}}",
                "ab".repeat(32)
            )
        );
    }

    #[test]
    fn verify_accepts_a_good_refusal_and_rejects_each_tamper() {
        let good = signed(reasons::POLICY_DECLINED);
        good.verify().unwrap();

        let mut t = good.clone();
        t.reason = reasons::NOT_AUTHORIZED.into();
        assert_eq!(t.verify(), Err(RefusalError::Unverified));

        let mut t = good.clone();
        t.issued_at = "2026-09-27T00:01:00Z".into();
        assert_eq!(t.verify(), Err(RefusalError::Unverified));

        let mut t = good.clone();
        t.request_digest = "cd".repeat(32);
        assert_eq!(t.verify(), Err(RefusalError::Unverified));

        let unregistered = signed("no_such_record");
        unregistered.verify().unwrap();
        assert!(!unregistered.reason_is_registered());
        assert!(good.reason_is_registered());
        // ER -02's tokens are registered under evidence-interop/1 only.
        for token in [reasons::CAPACITY_EXCEEDED, reasons::REQUEST_UNAUTHENTICATED] {
            let r = signed(token);
            r.verify().unwrap();
            assert!(!r.reason_is_registered(), "{token}");
            assert!(
                !r.reason_is_registered_under("evidence-interop/0"),
                "{token}"
            );
            assert!(
                r.reason_is_registered_under("evidence-interop/1"),
                "{token}"
            );
            assert!(
                r.reason_is_registered_under("evidence-interop/9"),
                "{token}"
            );
        }

        let mut t = good.clone();
        t.key_id = "zz".into();
        assert_eq!(t.verify(), Err(RefusalError::BadKey));

        let mut t = good.clone();
        t.sig = "00".into();
        assert_eq!(t.verify(), Err(RefusalError::BadSignature));

        // Strict verification: the identity point as key with R = identity
        // and s = 0 passes a non-strict Ed25519 check for any message; it is
        // refused here.
        let mut t = good;
        t.key_id = format!("01{}", "00".repeat(31));
        t.sig = format!("01{}{}", "00".repeat(31), "00".repeat(32));
        assert_eq!(t.verify(), Err(RefusalError::Unverified));
    }
}
