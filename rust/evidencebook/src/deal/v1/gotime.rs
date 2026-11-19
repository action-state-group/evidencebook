//! Go's `time.Parse(time.RFC3339, s)`, its result and its error text, for
//! the times the x-deal-v1 authorization reads from records that were never
//! checked (Go `xdealv1.parseAt`; evidencebook#551).
//!
//! Go parses this layout with its general parser when the strict RFC 3339
//! path fails, so it also takes a one-digit hour, a comma before a fraction
//! of any length, and a zone offset of up to 24 hours and 60 minutes; it
//! refuses anything else with the error [`parse`] returns. The crate replays
//! `testdata/deal-v1/time/vectors.json`, which Go's `TestTimeVectors`
//! writes, as Python's `gotime.py` does.

const LAYOUT: &str = "2006-01-02T15:04:05Z07:00";

/// Go's zero Time, 0001-01-01T00:00:00Z, as Unix seconds.
pub(crate) const ZERO: i64 = -62_135_596_800;

/// Go's `time.quote`: `"` and `\` escaped, each byte of a non-ASCII or
/// control character written as `\x` hex.
fn quote(s: &[u8]) -> String {
    let mut out = String::from("\"");
    for &b in s {
        if !(0x20..0x80).contains(&b) {
            out.push_str(&format!("\\x{b:02x}"));
        } else {
            if b == b'"' || b == b'\\' {
                out.push('\\');
            }
            out.push(b as char);
        }
    }
    out.push('"');
    out
}

fn cannot(value: &[u8], elem_value: &[u8], elem_layout: &str) -> String {
    format!(
        "parsing time {} as {}: cannot parse {} as {}",
        quote(value),
        quote(LAYOUT.as_bytes()),
        quote(elem_value),
        quote(elem_layout.as_bytes())
    )
}

fn range(value: &[u8], what: &str) -> String {
    format!("parsing time {}: {what} out of range", quote(value))
}

fn digit(s: &[u8], i: usize) -> bool {
    s.get(i).is_some_and(u8::is_ascii_digit)
}

/// Go's `getnum`: one or two digits (exactly two when `fixed`).
fn getnum(s: &[u8], fixed: bool) -> Option<(i64, &[u8])> {
    if !digit(s, 0) {
        return None;
    }
    if !digit(s, 1) {
        if fixed {
            return None;
        }
        return Some((i64::from(s[0] - b'0'), &s[1..]));
    }
    Some((
        i64::from(s[0] - b'0') * 10 + i64::from(s[1] - b'0'),
        &s[2..],
    ))
}

/// Days from 1970-01-01 to y-m-d in the proleptic Gregorian calendar.
fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = if y >= 0 { y } else { y - 399 } / 400;
    let yoe = y - era * 400;
    let doy = (153 * (m + if m > 2 { -3 } else { 9 }) + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

fn days_in(month: i64, year: i64) -> i64 {
    match month {
        2 if year % 4 == 0 && (year % 100 != 0 || year % 400 == 0) => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

/// `value` as Go's `time.Parse(time.RFC3339, value)` reads it: the instant
/// as (Unix seconds, nanoseconds), or Go's error text where Go refuses it.
pub(crate) fn parse(value: &str) -> Result<(i64, u32), String> {
    let value = value.as_bytes();
    let mut v = value;
    // 2006: four characters, the first a digit, all digits.
    if v.len() < 4 || !(0..4).all(|i| digit(v, i)) {
        return Err(cannot(value, v, "2006"));
    }
    let year = v[..4]
        .iter()
        .fold(0i64, |a, &b| a * 10 + i64::from(b - b'0'));
    v = &v[4..];
    let mut fields = [0i64; 5];
    let parts: [(&[u8], &str, bool, &str); 5] = [
        (b"-", "01", true, "month"),
        (b"-", "02", true, ""),
        (b"T", "15", false, "hour"),
        (b":", "04", true, "minute"),
        (b":", "05", true, "second"),
    ];
    for (k, (prefix, std, fixed, name)) in parts.iter().enumerate() {
        if !v.starts_with(prefix) {
            let p = std::str::from_utf8(prefix).unwrap_or_default();
            return Err(cannot(value, v, p));
        }
        v = &v[prefix.len()..];
        let Some((n, rest)) = getnum(v, *fixed) else {
            return Err(cannot(value, v, std));
        };
        v = rest;
        let out = match *name {
            "month" => !(1..=12).contains(&n),
            "hour" => n >= 24,
            "minute" | "second" => n >= 60,
            _ => false,
        };
        if out {
            return Err(range(value, name));
        }
        fields[k] = n;
    }
    let [month, day, hour, minute, sec] = fields;
    let mut nanos = 0u32;
    if v.len() >= 2 && (v[0] == b'.' || v[0] == b',') && digit(v, 1) {
        let mut n = 2;
        while n < v.len() && digit(v, n) {
            n += 1;
        }
        let digits = &v[1..n.min(10)];
        let read = digits
            .iter()
            .fold(0u32, |a, &b| a * 10 + u32::from(b - b'0'));
        nanos = read * 10u32.pow(9 - digits.len() as u32);
        v = &v[n..];
    }
    let offset;
    if let Some(rest) = v.strip_prefix(b"Z") {
        offset = 0;
        v = rest;
    } else {
        if v.len() < 6 || v[3] != b':' {
            return Err(cannot(value, v, "Z07:00"));
        }
        let (sign, hh, mm, rest) = (v[0], &v[1..3], &v[4..6], &v[6..]);
        // Go reads the minutes only when the hour reads, and checks the
        // ranges before it reports a part that did not read.
        let h = getnum(hh, true);
        let m = if h.is_some() { getnum(mm, true) } else { None };
        let (hr, mi) = (h.map_or(0, |x| x.0), m.map_or(0, |x| x.0));
        // Go sets each range error in turn, so the minute's overrides the hour's.
        if mi > 60 {
            return Err(range(value, "time zone offset minute"));
        }
        if hr > 24 {
            return Err(range(value, "time zone offset hour"));
        }
        if h.is_none() || m.is_none() || (sign != b'+' && sign != b'-') {
            return Err(cannot(value, v, "Z07:00"));
        }
        offset = (hr * 60 + mi) * 60 * if sign == b'+' { 1 } else { -1 };
        v = rest;
    }
    if !v.is_empty() {
        return Err(format!(
            "parsing time {}: extra text: {}",
            quote(value),
            quote(v)
        ));
    }
    if !(1..=days_in(month, year)).contains(&day) {
        return Err(range(value, "day"));
    }
    let seconds =
        days_from_civil(year, month, day) * 86_400 + hour * 3600 + minute * 60 + sec - offset;
    Ok((seconds, nanos))
}

#[cfg(test)]
mod tests {
    use super::{parse, ZERO};
    use serde_json::Value;

    /// testdata/deal-v1/time/vectors.json: each case Go's `time.Parse`
    /// answer, the instant or the error text, and whether it is before Go's
    /// zero Time.
    #[test]
    fn go_time_vectors() {
        let p = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../testdata/deal-v1/time/vectors.json");
        let v: Value = serde_json::from_slice(&std::fs::read(&p).unwrap()).unwrap();
        let cases = v["cases"].as_array().unwrap();
        assert!(cases.len() > 600);
        for c in cases {
            let input = c["in"].as_str().unwrap();
            match parse(input) {
                Ok((secs, nanos)) => {
                    assert!(
                        c["ok"].as_bool().unwrap(),
                        "{input:?}: Go refuses it: {}",
                        c["error"]
                    );
                    assert_eq!(Some(secs), c["unix"].as_i64(), "{input:?}");
                    assert_eq!(
                        u64::from(nanos),
                        c["nanos"].as_u64().unwrap_or(0),
                        "{input:?}"
                    );
                    assert_eq!(
                        secs < ZERO,
                        c["before_zero"].as_bool().unwrap_or(false),
                        "{input:?}"
                    );
                }
                Err(e) => {
                    assert!(!c["ok"].as_bool().unwrap(), "{input:?}: Go takes it: {e}");
                    assert_eq!(Some(e.as_str()), c["error"].as_str(), "{input:?}");
                }
            }
        }
    }
}
