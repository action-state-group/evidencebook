### Fixed: `capsulectl book card register --out` syncs the statement file before linking it (#697)

- The statement file's contents are flushed to disk before it is linked to `--out`, so a crash or power loss after the link no longer leaves an empty or partial file at `--out`. The directory is not synced, so after a crash the file may still be missing.
