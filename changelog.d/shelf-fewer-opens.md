### Added: `ReadShelfWindowFrom`; a shelf opens a book's log without a probe of its own (#1040)

- `ReadShelfWindowFrom` reads a shelf file's stored window policy on a handle the caller already has open, so a host that keeps its own handle on the shelf's file opens no extra connection to read the policy. `ReadShelfWindow` is unchanged.
- A shelf asks its own handle whether a book's, cadence or host log is new before opening its CLL store, so a new log costs its CLL store's init and open only (no failed open and no read-only probe), and an existing one costs its open only. What it provisions and refuses is unchanged: a log the file holds any state of is never provisioned over.
