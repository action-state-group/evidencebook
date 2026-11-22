# Port parity

Go is the full implementation. The Rust crate follows the Go APIs the Mesh
plugin calls, and the Python package is frozen (no new verbs except the
shelf's; see [#191](https://github.com/action-state-group/evidencebook/issues/191)).
[PORT-PARITY.tsv](PORT-PARITY.tsv) lists, for every exported top-level Go func,
type and var of the root package, `dealprofile`, `dealprofile/xdealv1` and
`rowstatus`, where its Rust and Python twin stands. Constants and methods
belong to their type's or feature's row. The `adapter` packages are Go-only
carriage run by `cmd/evidencebook` and are not listed.

| Status | Meaning |
|---|---|
| `ported` | A public twin exists, possibly under another name (an associated constructor, a constant, an enum variant, one trait standing for several Go interfaces, or a Python dict form). A private or crate-internal item is not a twin. The first pass was a name match, then checked by hand; it can still be wrong in either direction, so confirm before relying on a row. |
| `frozen #N` | No twin. Issue #N, labelled `frozen-port:rust` or `frozen-port:python`, (or its "Port" comment, for the issues that predate this table: #131, #140, #192, #618, #619) names the Go entry points, the vectors a port must replay and the frozen status. |
| `needed-by-mesh #N` | No twin, and the Mesh plugin (`Mesh-LLM/capsules-plugin`) calls it. Not frozen. |
| `not needed` | No twin is wanted: a Go sentinel error that each language reports through its own error type, or a Go struct type for data that Python reads as plain dicts (`dealprofile`, `dealprofile/xdealv1`, `rowstatus`). |

`TestPortParity` (`port_parity_test.go`, run by `go test ./...`) fails when an
exported API has no row, a row names an API Go no longer exports, or a status
is not one of the four forms. When you add a Go API, add its row; when you port
one, change the status to `ported` and close its issue's entry.
