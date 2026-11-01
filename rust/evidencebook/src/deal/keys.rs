//! Keys, fingerprints and commitments (PROFILE.md §4), as Go's
//! `dealprofile` makes them (`keys.go`).
//!
//! Counterparty identifiers never enter a sealed record raw. A record
//! carries a per-deal HMAC fingerprint of each normalized identifier, and
//! free text that may contain identifiers is carried as a salted commitment.
//! Raw values, the per-deal key and the commitment nonces stay in the
//! producer's local store.
//!
//! Go's string functions are matched rune for rune: `strings.ToLower` is the
//! simple (one-to-one) lowercase mapping, `cases.Fold` full case folding,
//! `unicode.IsPunct` the Unicode punctuation categories, `strings.TrimSpace`
//! and `strings.Fields` the White_Space property, and RE2's `\d` and `\s`
//! are ASCII.

use std::sync::OnceLock;

use hmac::{Hmac, Mac};
use icu_casemap::CaseMapper;
use icu_normalizer::ComposingNormalizerBorrowed;
use icu_properties::props::{GeneralCategory, GeneralCategoryGroup};
use icu_properties::CodePointMapData;
use idna::uts46::{AsciiDenyList, DnsLength, Hyphens, Uts46};
use regex::Regex;
use serde_json::json;
use sha2::Sha256;

use super::publicsuffix::effective_tld_plus_one;
use crate::canonical::json_digest;

/// An identifier that cannot be normalized. Such a value is not
/// fingerprinted and not recorded.
#[derive(Clone, Copy, Debug, PartialEq, Eq, thiserror::Error)]
#[error("identifier cannot be normalized")]
pub struct NotNormalizable;

/// The counterparty identifier kinds a record may carry, a closed set
/// (PROFILE.md §2).
pub fn kinds() -> &'static [&'static str] {
    &[
        "payee",
        "name",
        "domain",
        "phone",
        "email",
        "relay_address",
        "profile_id",
    ]
}

const LEGAL_FORMS: &[&str] = &[
    "llc", "inc", "ltd", "corp", "co", "company", "gmbh", "llp", "plc",
];

struct Patterns {
    phone_extension: Regex,
    payee_is_phone: Regex,
    url_scheme: Regex,
    domain_port: Regex,
}

// Go's RE2 `\s` is [\t\n\f\r ] and `\d` is [0-9].
fn patterns() -> &'static Patterns {
    static P: OnceLock<Patterns> = OnceLock::new();
    P.get_or_init(|| Patterns {
        phone_extension: Regex::new(
            r"(?i)[\t\n\f\r ]*(?:ext\.?|x|#)[\t\n\f\r ]*[0-9]+[\t\n\f\r ]*$",
        )
        .expect("pattern"),
        payee_is_phone: Regex::new(r"^[\t\n\f\r ]*\+?[0-9\t\n\f\r ().-]{7,}[\t\n\f\r ]*$")
            .expect("pattern"),
        url_scheme: Regex::new(r"^[a-z][a-z0-9+.-]*://").expect("pattern"),
        domain_port: Regex::new(r":[0-9]+$").expect("pattern"),
    })
}

fn nfc(s: &str) -> String {
    ComposingNormalizerBorrowed::new_nfc()
        .normalize(s)
        .into_owned()
}

fn nfkc(s: &str) -> String {
    ComposingNormalizerBorrowed::new_nfkc()
        .normalize(s)
        .into_owned()
}

/// Go `strings.ToLower`: the simple lowercase mapping, rune by rune.
pub(crate) fn go_lower(s: &str) -> String {
    let cm = CaseMapper::new();
    s.chars().map(|c| cm.simple_lowercase(c)).collect()
}

/// Go `cases.Fold().String`: full case folding.
pub(crate) fn fold(s: &str) -> String {
    CaseMapper::new().fold_string(s).into_owned()
}

fn is_punct(c: char) -> bool {
    GeneralCategoryGroup::Punctuation.contains(CodePointMapData::<GeneralCategory>::new().get(c))
}

fn norm_phone(v: &str) -> Result<String, NotNormalizable> {
    let v = nfkc(v);
    let mut v = v.trim();
    if let Some(m) = patterns().phone_extension.find(v) {
        v = &v[..m.start()];
    }
    let plus = v.starts_with('+');
    let mut digits: String = v.chars().filter(char::is_ascii_digit).collect();
    if !plus {
        match digits.len() {
            10 => digits.insert(0, '1'), // v0 default region: NANP
            11 if digits.starts_with('1') => {}
            _ => return Err(NotNormalizable),
        }
    }
    if digits.len() < 8 || digits.len() > 15 {
        return Err(NotNormalizable);
    }
    Ok(format!("+{digits}"))
}

fn norm_email(v: &str) -> Result<String, NotNormalizable> {
    let v = go_lower(&nfc(v.trim()));
    if v.matches('@').count() != 1 || v.starts_with('@') || v.ends_with('@') {
        return Err(NotNormalizable);
    }
    Ok(v)
}

fn norm_domain(v: &str) -> Result<String, NotNormalizable> {
    let p = patterns();
    let v = go_lower(&nfc(v.trim()));
    let mut v = p.url_scheme.replace(&v, "").into_owned();
    if let Some(i) = v.find(['/', '?', '#']) {
        v.truncate(i);
    }
    if let Some(i) = v.rfind('@') {
        v = v[i + 1..].to_string();
    }
    let v = p.domain_port.replace(&v, "");
    let v = v.trim_end_matches('.');
    let uts46 = Uts46::new();
    let v = decode_non_ascii_alabels(&uts46, v)?;
    let ascii = uts46
        .to_ascii(
            v.as_bytes(),
            AsciiDenyList::STD3,
            Hyphens::Check,
            DnsLength::Ignore,
        )
        .map_err(|_| NotNormalizable)?;
    let labels: Vec<&str> = ascii.split('.').collect();
    if labels.len() < 2 || labels.iter().any(|l| l.is_empty()) {
        return Err(NotNormalizable);
    }
    let registrable = effective_tld_plus_one(&ascii).ok_or(NotNormalizable)?;
    let (out, ok) = uts46.to_unicode(registrable.as_bytes(), AsciiDenyList::STD3, Hyphens::Check);
    ok.map_err(|_| NotNormalizable)?;
    Ok(out.into_owned())
}

/// RFC 3492 decoding as x/net's `idna` does it: the part before the last
/// hyphen is copied rune for rune, whatever the runes are (the `idna` crate
/// reads only ASCII there and refuses such a label that Go accepts).
fn punycode_decode(encoded: &str) -> Option<String> {
    if encoded.is_empty() {
        return Some(String::new());
    }
    let chars: Vec<char> = encoded.chars().collect();
    let split = chars.iter().rposition(|&c| c == '-')?;
    let mut out: Vec<char> = chars[..split].to_vec();
    let (mut i, mut n, mut bias) = (0u32, 128u32, 72u32);
    let limit = i32::MAX as u32;
    let mut pos = split + 1;
    while pos < chars.len() {
        let (old_i, mut w, mut k) = (i, 1u32, 36u32);
        loop {
            let c = *chars.get(pos)?;
            let digit = match c {
                'a'..='z' => c as u32 - 'a' as u32,
                'A'..='Z' => c as u32 - 'A' as u32,
                '0'..='9' => c as u32 - '0' as u32 + 26,
                _ => return None,
            };
            pos += 1;
            i = i.checked_add(digit.checked_mul(w)?)?;
            if i > limit {
                return None;
            }
            let t = if k <= bias {
                1
            } else if k >= bias + 26 {
                26
            } else {
                k - bias
            };
            if digit < t {
                break;
            }
            w = w.checked_mul(36 - t)?;
            if w > limit {
                return None;
            }
            k += 36;
        }
        if out.len() >= 1024 {
            return None;
        }
        let x = out.len() as u32 + 1;
        let mut delta = i - old_i;
        delta = if old_i == 0 { delta / 700 } else { delta / 2 };
        delta += delta / x;
        let mut kk = 0;
        while delta > 35 * 26 / 2 {
            delta /= 35;
            kk += 36;
        }
        bias = kk + 36 * delta / (delta + 38);
        n = n.checked_add(i / x)?;
        i %= x;
        out.insert(i as usize, char::from_u32(n)?);
        i += 1;
    }
    Some(out.into_iter().collect())
}

/// Go accepts an `xn--` label whose part before the last hyphen holds
/// non-ASCII characters and reads it as the label it decodes to; the `idna`
/// crate refuses it. Such a label is replaced by its decoded form, which must
/// be NFC and survive `to_unicode` unchanged (every character valid as
/// written, none mapped), and which `to_ascii` then encodes as Go does.
fn decode_non_ascii_alabels(uts46: &Uts46, v: &str) -> Result<String, NotNormalizable> {
    if v.is_ascii() || !v.split('.').any(|l| l.starts_with("xn--")) {
        return Ok(v.to_string());
    }
    // UTS46 maps these to a full stop before it splits the labels.
    let v = v.replace(['\u{3002}', '\u{ff0e}', '\u{ff61}'], ".");
    let mut labels = Vec::new();
    for label in v.split('.') {
        match label.strip_prefix("xn--") {
            Some(rest) if !label.is_ascii() => {
                let u = punycode_decode(rest).ok_or(NotNormalizable)?;
                if u.is_empty() || u.is_ascii() || nfc(&u) != u {
                    return Err(NotNormalizable);
                }
                let (back, ok) =
                    uts46.to_unicode(u.as_bytes(), AsciiDenyList::STD3, Hyphens::Check);
                ok.map_err(|_| NotNormalizable)?;
                if back != u {
                    return Err(NotNormalizable);
                }
                labels.push(u);
            }
            _ => labels.push(label.to_string()),
        }
    }
    Ok(labels.join("."))
}

fn norm_name(v: &str) -> Result<String, NotNormalizable> {
    let v: String = fold(&nfc(v))
        .chars()
        .map(|c| if is_punct(c) { ' ' } else { c })
        .collect();
    let mut toks: Vec<&str> = v.split_whitespace().collect();
    if toks.first() == Some(&"the") {
        toks.remove(0);
    }
    while toks.last().is_some_and(|t| LEGAL_FORMS.contains(t)) {
        toks.pop();
    }
    if toks.is_empty() {
        return Err(NotNormalizable);
    }
    Ok(toks.join(" "))
}

/// One raw identifier of `kind` in the form its fingerprint is taken over
/// (PROFILE.md §4).
pub fn normalize(kind: &str, v: &str) -> Result<String, NotNormalizable> {
    match kind {
        "phone" => norm_phone(v),
        "email" => norm_email(v),
        "domain" => norm_domain(v),
        "name" => norm_name(v),
        "payee" => {
            if v.contains('@') {
                norm_email(v)
            } else if patterns().payee_is_phone.is_match(v) {
                norm_phone(v)
            } else {
                norm_name(v)
            }
        }
        "relay_address" => Ok(go_lower(&nfc(v.trim()))),
        "profile_id" => {
            let out = nfc(v.trim());
            if out.is_empty() {
                Err(NotNormalizable)
            } else {
                Ok(out)
            }
        }
        _ => Err(NotNormalizable),
    }
}

fn hmac_sha256(key: &[u8], parts: &[&str]) -> [u8; 32] {
    let mut m = Hmac::<Sha256>::new_from_slice(key).expect("HMAC takes a key of any length");
    for p in parts {
        m.update(p.as_bytes());
    }
    m.finalize().into_bytes().into()
}

/// A deal's key, derived from the store secret and the deal id. A producer
/// stores it with the deal at open, so a later rotation of the store secret
/// cannot change a deal in progress.
pub fn deal_key(store_secret: &[u8], deal_id: &str) -> [u8; 32] {
    hmac_sha256(store_secret, &["x-deal-v0/deal-key\x00", deal_id])
}

/// The key of a profile's own fingerprints
/// ([`PROFILE_FP_ALG`](super::PROFILE_FP_ALG)): one merchant has one such
/// fingerprint across the profile's deals. Like the store secret, it never
/// leaves the device.
pub fn profile_key(store_secret: &[u8]) -> [u8; 32] {
    hmac_sha256(store_secret, &["x-deal-v0/profile-key\x00"])
}

/// The fingerprint of one raw identifier of `kind` under `key` (a deal key,
/// or a profile key), or [`NotNormalizable`], in which case the value is not
/// recorded.
pub fn fingerprint(key: &[u8], kind: &str, raw: &str) -> Result<String, NotNormalizable> {
    let n = normalize(kind, raw)?;
    Ok(hex::encode(hmac_sha256(
        key,
        &["x-deal-v0/fp\x00", kind, "\x00", &n],
    )))
}

/// The salted commitment [`COMMIT_ALG`](super::COMMIT_ALG) names: SHA-256
/// over the JCS bytes of `{"nonce", "text"}`, with `nonce_hex` a fresh random
/// 256-bit value (64 lowercase hex) for each commitment. Whoever holds its
/// opening (nonce and text) recomputes it, and nobody else can test a guess
/// against it.
pub fn commit_text(nonce_hex: &str, text: &str) -> String {
    json_digest(&json!({"nonce": nonce_hex, "text": text})).expect("two strings are JCS")
}
