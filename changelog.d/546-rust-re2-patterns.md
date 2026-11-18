<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- **Fixed (`deal`):** the x-deal-v0 and x-deal-v1 schema stages read a schema pattern's `\s` as Go's validator (RE2) does: the tab, newline, form feed, carriage return and space only (evidencebook#546). A platform written with a no-break space or a vertical tab was refused here and passed in Go. The parity vectors carry the cases.
