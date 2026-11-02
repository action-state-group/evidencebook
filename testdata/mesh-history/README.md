# The Mesh node's Service History definition

`service-history-1.json` is the definition document of the Mesh node's Service History
(`service_history/1`, an `inference-service-history/0` Standard definition), copied byte for byte
from `derivations/service-history-1.json` in
[Mesh-LLM/capsules-plugin](https://github.com/Mesh-LLM/capsules-plugin). A request names it on the
wire by the SHA-256 of its RFC 8785 bytes, `cbc96d800ad206791db7e0793b80a2720c1908468ba99692b739aa8e74aab2d8`.
Its `period_policy` is one week from Monday 2026-01-05, a floor of 5, complementary suppression.

`rust/interop/tests/mesh_history.rs` derives and checks a History under it with
agent-action-capsule's `inference_history`; docs/GUIDE.md path 4 describes it.
