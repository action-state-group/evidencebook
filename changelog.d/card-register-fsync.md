### Fixed: `capsulectl book card register --out` syncs the statement file before linking it (#697)

- The statement file is flushed to disk before it is linked to `--out`, so a crash or power loss after the link can no longer leave an empty or partial statement at `--out`. The registration is permanent on the service, so the statement could not be recreated by registering again.
