//! The public suffix list Go's `dealprofile` reads, by the same lookup.
//!
//! A domain's fingerprint is taken over its registrable domain (eTLD+1), so
//! the Go and Rust implementations must agree on every public suffix. The
//! tables here are golang.org/x/net v0.59.0's `publicsuffix/data` files, byte
//! for byte (the version the Go module requires; they hold
//! publicsuffix.org's list at git revision
//! d6c92f1bbb7433e5db7b8405c25d4035fb8ff376), and the lookup is a port of
//! its `list.go`. Both are under the Go authors' BSD-3-Clause license,
//! `vendor/x-net-publicsuffix/LICENSE`. Moving the Go module to another
//! x/net version means replacing these files with that version's.

const TEXT: &str = include_str!("../../vendor/x-net-publicsuffix/text");
const NODES: &[u8] = include_bytes!("../../vendor/x-net-publicsuffix/nodes");
const CHILDREN: &[u8] = include_bytes!("../../vendor/x-net-publicsuffix/children");

// x/net v0.59.0 publicsuffix/table.go.
const NODES_BITS_CHILDREN: u32 = 10;
const NODES_BITS_ICANN: u32 = 1;
const NODES_BITS_TEXT_OFFSET: u32 = 16;
const NODES_BITS_TEXT_LENGTH: u32 = 6;
const CHILDREN_BITS_WILDCARD: u32 = 1;
const CHILDREN_BITS_NODE_TYPE: u32 = 2;
const CHILDREN_BITS_HI: u32 = 14;
const CHILDREN_BITS_LO: u32 = 14;
const NODE_TYPE_NORMAL: u32 = 0;
const NODE_TYPE_EXCEPTION: u32 = 1;
const NUM_TLD: u32 = 1450;

fn children(i: u32) -> u32 {
    let o = i as usize * 4;
    u32::from_be_bytes([
        CHILDREN[o],
        CHILDREN[o + 1],
        CHILDREN[o + 2],
        CHILDREN[o + 3],
    ])
}

fn node(i: u32) -> u64 {
    let o = i as usize * 5;
    NODES[o..o + 5]
        .iter()
        .fold(0u64, |acc, b| (acc << 8) | u64::from(*b))
}

fn node_label(i: u32) -> &'static str {
    let x = node(i);
    let length = (x & ((1 << NODES_BITS_TEXT_LENGTH) - 1)) as usize;
    let offset = ((x >> NODES_BITS_TEXT_LENGTH) & ((1 << NODES_BITS_TEXT_OFFSET) - 1)) as usize;
    &TEXT[offset..offset + length]
}

fn find(label: &str, mut lo: u32, mut hi: u32) -> Option<u32> {
    while lo < hi {
        let mid = lo + (hi - lo) / 2;
        match node_label(mid).cmp(label) {
            std::cmp::Ordering::Less => lo = mid + 1,
            std::cmp::Ordering::Equal => return Some(mid),
            std::cmp::Ordering::Greater => hi = mid,
        }
    }
    None
}

/// The public suffix of `domain` (Go `publicsuffix.PublicSuffix`, without
/// its ICANN flag, which nothing here reads).
pub(crate) fn public_suffix(domain: &str) -> &str {
    if domain.parse::<std::net::IpAddr>().is_ok() {
        return domain;
    }
    let (mut lo, mut hi) = (0u32, NUM_TLD);
    let mut s = domain;
    let mut suffix = domain.len();
    let mut wildcard = false;
    loop {
        let dot = s.rfind('.');
        let after = dot.map_or(0, |d| d + 1);
        if wildcard {
            suffix = after;
        }
        if lo == hi {
            break;
        }
        let Some(f) = find(&s[after..], lo, hi) else {
            break;
        };
        let mut u = (node(f) >> (NODES_BITS_TEXT_OFFSET + NODES_BITS_TEXT_LENGTH)) as u32;
        u >>= NODES_BITS_ICANN;
        u = children(u & ((1 << NODES_BITS_CHILDREN) - 1));
        lo = u & ((1 << CHILDREN_BITS_LO) - 1);
        u >>= CHILDREN_BITS_LO;
        hi = u & ((1 << CHILDREN_BITS_HI) - 1);
        u >>= CHILDREN_BITS_HI;
        match u & ((1 << CHILDREN_BITS_NODE_TYPE) - 1) {
            NODE_TYPE_NORMAL => suffix = after,
            NODE_TYPE_EXCEPTION => {
                suffix = 1 + s.len();
                break;
            }
            _ => {}
        }
        u >>= CHILDREN_BITS_NODE_TYPE;
        wildcard = u & ((1 << CHILDREN_BITS_WILDCARD) - 1) != 0;
        match dot {
            None => break,
            Some(d) => s = &s[..d],
        }
    }
    if suffix == domain.len() {
        // If no rules match, the prevailing rule is "*".
        return &domain[domain.rfind('.').map_or(0, |d| d + 1)..];
    }
    &domain[suffix..]
}

/// The effective top-level domain plus one more label (Go
/// `publicsuffix.EffectiveTLDPlusOne`), or `None` where Go returns an error.
pub(crate) fn effective_tld_plus_one(domain: &str) -> Option<&str> {
    if domain.starts_with('.') || domain.ends_with('.') || domain.contains("..") {
        return None;
    }
    let suffix = public_suffix(domain);
    if domain.len() <= suffix.len() {
        return None;
    }
    let i = domain.len() - suffix.len() - 1;
    if domain.as_bytes()[i] != b'.' {
        return None;
    }
    Some(&domain[domain[..i].rfind('.').map_or(0, |d| d + 1)..])
}

#[cfg(test)]
mod tests {
    use super::*;

    // Cases from x/net v0.59.0 publicsuffix/list_test.go (eTLD+1).
    #[test]
    fn effective_tld_plus_one_as_x_net() {
        for (domain, want) in [
            ("com", None),
            ("example.com", Some("example.com")),
            ("b.example.com", Some("example.com")),
            ("uk.com", None),
            ("example.uk.com", Some("example.uk.com")),
            ("a.b.example.uk.com", Some("example.uk.com")),
            ("jp", None),
            ("kyoto.jp", None),
            ("test.kyoto.jp", Some("test.kyoto.jp")),
            ("ide.kyoto.jp", None),
            ("b.ide.kyoto.jp", Some("b.ide.kyoto.jp")),
            ("c.kobe.jp", None),
            ("b.c.kobe.jp", Some("b.c.kobe.jp")),
            ("city.kobe.jp", Some("city.kobe.jp")),
            ("www.city.kobe.jp", Some("city.kobe.jp")),
            ("cromulent", None),
            ("a.cromulent", Some("a.cromulent")),
            ("b.a.cromulent", Some("a.cromulent")),
            ("co.uk", None),
            ("example.co.uk", Some("example.co.uk")),
            ("www.example.co.uk", Some("example.co.uk")),
            (".example.com", None),
            ("example.com.", None),
            ("a..example.com", None),
            ("192.0.2.1", None),
        ] {
            assert_eq!(effective_tld_plus_one(domain), want, "{domain}");
        }
    }
}
