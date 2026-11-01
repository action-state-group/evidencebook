//! UTC instants as the shelf stores and compares them.
//!
//! The Go shelf works in `time.Time`; this is the part of it the shelf's
//! bytes depend on: nanoseconds since the Unix epoch, written in Go's
//! `time.RFC3339` (whole seconds) and `time.RFC3339Nano` (the fraction with
//! trailing zeros dropped) forms, and parsed from either.

use evidence_request::time::parse_utc;
use std::fmt;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

const NANOS: i128 = 1_000_000_000;

/// A UTC instant: nanoseconds since the Unix epoch.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Time(i128);

impl Time {
    /// The instant `nanos` nanoseconds after the Unix epoch.
    pub const fn from_unix_nanos(nanos: i128) -> Self {
        Self(nanos)
    }

    /// The instant `seconds` seconds after the Unix epoch.
    pub const fn from_unix_seconds(seconds: i64) -> Self {
        Self(seconds as i128 * NANOS)
    }

    /// Nanoseconds since the Unix epoch.
    pub const fn unix_nanos(self) -> i128 {
        self.0
    }

    /// The system clock now.
    pub fn now() -> Self {
        Self::from(SystemTime::now())
    }

    /// Parse an RFC 3339 UTC time (`YYYY-MM-DDTHH:MM:SS[.fraction]Z`).
    pub fn parse(text: &str) -> Option<Self> {
        let t = parse_utc(text)?;
        Some(Self(i128::from(t.seconds) * NANOS + i128::from(t.nanos)))
    }

    /// This instant plus `d`.
    pub fn add(self, d: Duration) -> Self {
        Self(self.0 + d.as_nanos() as i128)
    }

    /// This instant plus `nanos` (which may be negative).
    pub fn add_nanos(self, nanos: i128) -> Self {
        Self(self.0 + nanos)
    }

    /// Go's `Truncate(d)`: rounded down to a multiple of `d` since the
    /// zero time, which for the units the shelf uses is the epoch too.
    pub fn truncate(self, d: Duration) -> Self {
        let unit = d.as_nanos() as i128;
        if unit <= 0 {
            return self;
        }
        Self(self.0 - self.0.rem_euclid(unit))
    }

    /// Go's `time.RFC3339`: whole seconds, `Z`.
    pub fn rfc3339(self) -> String {
        let (date, _) = self.civil();
        date
    }

    /// Go's `time.RFC3339Nano`: the fraction with its trailing zeros
    /// dropped, none when it is zero.
    pub fn rfc3339_nano(self) -> String {
        let (date, nanos) = self.civil();
        if nanos == 0 {
            return date;
        }
        let fraction = format!("{nanos:09}");
        format!(
            "{}.{}Z",
            &date[..date.len() - 1],
            fraction.trim_end_matches('0')
        )
    }

    /// `YYYY-MM-DDTHH:MM:SSZ` and the nanoseconds within the second.
    fn civil(self) -> (String, u32) {
        let seconds = self.0.div_euclid(NANOS) as i64;
        let nanos = self.0.rem_euclid(NANOS) as u32;
        let days = seconds.div_euclid(86_400);
        let rest = seconds.rem_euclid(86_400);
        let (year, month, day) = civil_from_days(days);
        (
            format!(
                "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}Z",
                rest / 3600,
                rest / 60 % 60,
                rest % 60
            ),
            nanos,
        )
    }
}

impl From<SystemTime> for Time {
    fn from(t: SystemTime) -> Self {
        match t.duration_since(UNIX_EPOCH) {
            Ok(after) => Self(after.as_nanos() as i128),
            Err(before) => Self(-(before.duration().as_nanos() as i128)),
        }
    }
}

impl fmt::Display for Time {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.rfc3339_nano())
    }
}

/// Howard Hinnant's `civil_from_days`: the proleptic Gregorian date of a day
/// count from 1970-01-01.
fn civil_from_days(z: i64) -> (i64, u32, u32) {
    let z = z + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (if m <= 2 { y + 1 } else { y }, m, d)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn writes_and_reads_go_forms() {
        let t = Time::parse("2026-10-31T12:05:36Z").unwrap();
        assert_eq!(t.rfc3339(), "2026-10-31T12:05:36Z");
        assert_eq!(t.rfc3339_nano(), "2026-10-31T12:05:36Z");
        let t = t.add_nanos(120_000_000);
        assert_eq!(t.rfc3339_nano(), "2026-10-31T12:05:36.12Z");
        assert_eq!(t.rfc3339(), "2026-10-31T12:05:36Z");
        assert_eq!(
            t.truncate(Duration::from_secs(60)).rfc3339(),
            "2026-10-31T12:05:00Z"
        );
        assert_eq!(Time::from_unix_seconds(0).rfc3339(), "1970-01-01T00:00:00Z");
        assert_eq!(
            Time::parse("2000-02-29T23:59:59.000000001Z")
                .unwrap()
                .rfc3339_nano(),
            "2000-02-29T23:59:59.000000001Z"
        );
        assert!(Time::parse("2026-10-31 12:00:00").is_none());
    }
}
