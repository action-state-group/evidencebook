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
| `ported` | A twin exists. Found by name against `rust/evidencebook/src` and `python/evidencebook`; a row the name match missed shows as frozen, so confirm before porting. |
| `frozen #N` | No twin. Issue #N, labelled `frozen-port:rust` or `frozen-port:python`, names the Go entry points, the vectors a port must replay and the frozen status. |
| `needed-by-mesh #N` | No twin, and the Mesh plugin (`Mesh-LLM/capsules-plugin`) calls it. Not frozen. |
| `not needed` | No twin is wanted: a Go sentinel error that each language reports through its own error type, or a Go struct type for data that Python reads as plain dicts (`dealprofile`, `dealprofile/xdealv1`, `rowstatus`). |

`TestPortParity` (`port_parity_test.go`, run by `go test ./...`) fails when an
exported API has no row, a row names an API Go no longer exports, or a status
is not one of the four forms. When you add a Go API, add its row; when you port
one, change the status to `ported` and close its issue's entry.
