//! The shelf's one random source (evidencebook#254 §5).
//!
//! Every salt, tree position, filler nonce and seed, jitter and padding
//! nonce a cadence draws comes from one [`RandomSource`], in the order
//! `testdata/shelf/README.md` states, and every draw is defined on the bytes
//! it reads, so another implementation fed the same stream draws the same
//! values (Go `shelf_random.go`):
//!
//! - `bytes(n)` reads `n` bytes;
//! - `uniform(n)` reads 8 bytes as a big-endian `x`, again while `x` is at
//!   or above the largest multiple of `n` not over 2^64, and returns
//!   `x mod n`;
//! - a tree position is `uniform(2^16)`, drawn again while it is taken.
//!
//! Production uses the system CSPRNG ([`SystemRandom`]); the vectors and the
//! storage interlock inject [`SeededStream`].

use super::ShelfError;
use rand_core::{OsRng, RngCore};
use sha2::{Digest, Sha256};

/// A source of random bytes for a shelf.
pub trait RandomSource: Send {
    /// Fill `buf` with the next bytes of the source.
    fn fill(&mut self, buf: &mut [u8]) -> Result<(), String>;
}

/// The system CSPRNG.
#[derive(Clone, Copy, Debug, Default)]
pub struct SystemRandom;

impl RandomSource for SystemRandom {
    fn fill(&mut self, buf: &mut [u8]) -> Result<(), String> {
        OsRng.try_fill_bytes(buf).map_err(|e| e.to_string())
    }
}

/// The deterministic stream the shelf vectors and the storage interlock use
/// (Go `internal/detrand`): block `i` (from 0) of the stream for `seed` is
/// `SHA-256(UTF8(seed) ‖ uint64be(i))`, and the stream is the blocks in
/// order. It is for tests only.
#[derive(Clone, Debug)]
pub struct SeededStream {
    seed: Vec<u8>,
    block: u64,
    buf: Vec<u8>,
}

impl SeededStream {
    /// The stream for `seed`.
    pub fn new(seed: &str) -> Self {
        Self {
            seed: seed.as_bytes().to_vec(),
            block: 0,
            buf: Vec::new(),
        }
    }
}

impl RandomSource for SeededStream {
    fn fill(&mut self, out: &mut [u8]) -> Result<(), String> {
        let mut n = 0;
        while n < out.len() {
            if self.buf.is_empty() {
                let mut h = Sha256::new();
                h.update(&self.seed);
                h.update(self.block.to_be_bytes());
                self.buf = h.finalize().to_vec();
                self.block += 1;
            }
            let c = self.buf.len().min(out.len() - n);
            out[n..n + c].copy_from_slice(&self.buf[..c]);
            self.buf.drain(..c);
            n += c;
        }
        Ok(())
    }
}

/// The draws a shelf makes from its source.
pub(crate) struct Draws(pub(crate) Box<dyn RandomSource>);

impl Draws {
    pub(crate) fn bytes(&mut self, n: usize) -> Result<Vec<u8>, ShelfError> {
        let mut b = vec![0; n];
        self.0
            .fill(&mut b)
            .map_err(|e| ShelfError::Random(format!("the shelf's random source: {e}")))?;
        Ok(b)
    }

    /// `bytes(n)` in lowercase hex.
    pub(crate) fn hex(&mut self, n: usize) -> Result<String, ShelfError> {
        Ok(hex::encode(self.bytes(n)?))
    }

    /// A uniform draw in `[0, n)`, `n > 0`.
    pub(crate) fn uniform(&mut self, n: u64) -> Result<u64, ShelfError> {
        if n == 0 {
            return Err(ShelfError::Invalid(
                "a uniform draw needs a positive bound".into(),
            ));
        }
        // 2^64 mod n; zero when n divides 2^64.
        let rest = n.wrapping_neg() % n;
        loop {
            let b = self.bytes(8)?;
            let x = u64::from_be_bytes(b.try_into().expect("eight bytes"));
            if rest == 0 || x < rest.wrapping_neg() {
                return Ok(x % n);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_seeded_stream_is_its_blocks_in_order() {
        let mut s = SeededStream::new("x");
        let mut a = [0u8; 40];
        s.fill(&mut a).unwrap();
        let block = |i: u64| {
            let mut h = Sha256::new();
            h.update(b"x");
            h.update(i.to_be_bytes());
            h.finalize().to_vec()
        };
        assert_eq!(&a[..32], block(0).as_slice());
        assert_eq!(&a[32..], &block(1)[..8]);
    }

    #[test]
    fn uniform_refuses_a_zero_bound_and_stays_under_the_bound() {
        let mut d = Draws(Box::new(SeededStream::new("u")));
        assert!(d.uniform(0).is_err());
        for _ in 0..100 {
            assert!(d.uniform(7).unwrap() < 7);
        }
        assert!(d.uniform(1 << 16).unwrap() < 1 << 16);
    }
}
