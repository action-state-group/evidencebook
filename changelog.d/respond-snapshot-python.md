<!-- changelog: python/CHANGELOG.md -->
- `Book.respond` answers from a snapshot of the log and holds the book's lock only to read the clock, call the limiter, issue an anchor a freshness floor needs and seal `evidence_request_answered`, so a flood of requests no longer blocks appends and checkpoints, as Go's `Book.Respond` does (evidencebook#217).
