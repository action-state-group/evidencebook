//! The shelf's own writer lock (Go `shelf_lock_unix.go`): an exclusive
//! advisory `flock(LOCK_EX|LOCK_NB)` on a lock file, retried every 50 ms
//! until the caller's deadline, so a Go, a Rust and a Python writer of one
//! shelf exclude each other (evidencebook#254 §2.4). Off unix the shelf's
//! own lock does not exist; a shelf on a host-held lock takes none.

use super::ShelfError;
use std::fs::File;
use std::path::Path;
use std::time::{Duration, Instant};

/// How often a waiting shelf tries the lock again.
const RETRY: Duration = Duration::from_millis(50);

/// A held lock: the open lock file. Dropping it releases the lock.
pub(crate) struct FileLock {
    #[cfg_attr(not(unix), allow(dead_code))]
    file: File,
}

impl FileLock {
    /// Take the lock on `path`, creating the file (owner-only) when absent,
    /// waiting for another holder until `deadline` (forever when `None`).
    #[cfg(unix)]
    pub(crate) fn acquire(path: &Path, deadline: Option<Instant>) -> Result<Self, ShelfError> {
        use std::os::unix::fs::OpenOptionsExt;
        use std::os::unix::io::AsRawFd;
        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .mode(0o600)
            .open(path)?;
        loop {
            // SAFETY: flock on a file descriptor this function owns.
            if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0 {
                return Ok(Self { file });
            }
            let err = std::io::Error::last_os_error();
            if err.raw_os_error() != Some(libc::EWOULDBLOCK) {
                return Err(err.into());
            }
            let wait = match deadline {
                None => RETRY,
                Some(d) => {
                    let left = d.saturating_duration_since(Instant::now());
                    if left.is_zero() {
                        return Err(ShelfError::LockHeld(format!(
                            "waiting for the shelf's writer lock {}",
                            path.display()
                        )));
                    }
                    left.min(RETRY)
                }
            };
            std::thread::sleep(wait);
        }
    }

    /// Off unix a shelf's own lock does not exist.
    #[cfg(not(unix))]
    pub(crate) fn acquire(_path: &Path, _deadline: Option<Instant>) -> Result<Self, ShelfError> {
        Err(ShelfError::Unsupported(
            "a shelf's writer lock needs a unix advisory file lock".into(),
        ))
    }

    /// Give up the lock and close the file.
    pub(crate) fn release(self) -> Result<(), ShelfError> {
        #[cfg(unix)]
        {
            use std::os::unix::io::AsRawFd;
            // SAFETY: flock on the file descriptor this lock owns.
            if unsafe { libc::flock(self.file.as_raw_fd(), libc::LOCK_UN) } != 0 {
                return Err(std::io::Error::last_os_error().into());
            }
        }
        Ok(())
    }
}
