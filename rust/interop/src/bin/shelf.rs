// SPDX-License-Identifier: Apache-2.0
//! Rust's side of the shelf storage interlock (evidencebook#254 §5 item 2),
//! with the verbs of Go's `test/interop/shelf`:
//!
//! ```text
//! shelf write DIR       the writer phase, on a new DIR/shelf.db
//! shelf continue DIR    the continuer phase, on DIR/shelf.db
//! shelf dump DIR        the file's dump on stdout (it reads a copy)
//! shelf hold DIR SECS   open the shelf, print "held", keep it open SECS seconds
//! shelf try DIR MS      open the shelf, waiting at most MS milliseconds for
//!                       its lock: exit 0 if it opened, 3 if another writer
//!                       holds the lock
//! ```

use evidencebook::shelf::ShelfError;
use evidencebook_interop::shelf;
use std::io::Write as _;
use std::path::Path;
use std::process::ExitCode;
use std::time::Duration;

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    match run(&args) {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("shelf: {e}");
            let held = e
                .downcast_ref::<ShelfError>()
                .is_some_and(|e| matches!(e, ShelfError::LockHeld(_)));
            ExitCode::from(if held { 3 } else { 1 })
        }
    }
}

fn run(args: &[String]) -> Result<(), shelf::Error> {
    let [verb, dir, rest @ ..] = args else {
        return Err("usage: shelf write|continue|dump|hold|try DIR [ARG]".into());
    };
    let dir = Path::new(dir);
    let n = || -> Result<u64, shelf::Error> {
        match rest {
            [n] => Ok(n.parse()?),
            _ => Err(format!("usage: shelf {verb} DIR N").into()),
        }
    };
    match verb.as_str() {
        "write" => {
            std::fs::create_dir_all(dir)?;
            shelf::write(dir)
        }
        "continue" => shelf::continue_(dir),
        "dump" => {
            std::io::stdout().write_all(shelf::dump(dir)?.as_bytes())?;
            Ok(())
        }
        "hold" => {
            let secs = n()?;
            let env = shelf::hold(dir, None)?;
            println!("held");
            std::io::stdout().flush()?;
            std::thread::sleep(Duration::from_secs(secs));
            env.release()?;
            Ok(())
        }
        "try" => {
            let ms = n()?;
            let env = shelf::hold(dir, Some(Duration::from_millis(ms))).map_err(|e| {
                // Report the lock as the shelf's own error, so main exits 3.
                match e.downcast::<ShelfError>() {
                    Ok(shelf_error) => shelf_error as shelf::Error,
                    Err(other) => other,
                }
            })?;
            env.release()?;
            Ok(())
        }
        other => Err(format!("unknown command {other:?}").into()),
    }
}
