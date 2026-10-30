# evidencebook#206: attacks on the evidence request, what holds and what does not

Status: draft for review. Design note only; no code changes here. Written 2026-10-30 from the attack discussion of 2026-10-29 and the 2026-10-24 security review of the ARD / AI Catalog proposal. Code references are to this repository at `5cf7016` and to Mesh-LLM/capsules-plugin at `86c6062` unless stated.

## The frame

The evidence request (`draft-mih-agent-evidence-request` -01, served here by `Book.Respond` and read by `request.go`) was designed as a scoped ask between two parties: one subject, one checkpoint to verify against, one of three outcomes. It was never designed as a public polling surface. Most of the attack surface discussed below comes from treating it as one, and most of the answer is the move already under way in the loop: the stranger's question is answered by a published staple (the node's commitment with its `history_card/1`), collected off the mesh channel and the loop repository and verified offline, so a node need answer no stranger at all.

Three parties can be attacked: the responder (the holder's node), the asker (the relying party's verifier), and the network report that counts what nodes publish. #206 lists the responder-side attacks. This note places those, adds the asker-side and report-side ones, and names the issue that owns each open item.

## What holds up, by construction

| Property | Why it holds | Where |
|---|---|---|
| The stranger's question is cheap and static | `checkpoints` and `history_card/1` are constant size in records, cacheable per epoch, and the plugin publishes them as the staple inside its commitment. A node can serve strangers from a static object, or not serve them at all. | capsules-plugin `commitment.rs` (`STRANGER_SAFE`); ER -01 §8.1 |
| Expensive forms are default-denied | `range`, `correlation` and `full_history` are answered only to a requester the disclosure profile grants. A stranger cannot force a bundle to be built, so amplification is bounded by the size of a card. | `interop_respond.go` (`DefaultSharePolicy`, `allows`); ER -01 §8 |
| A refusal leaks nothing about existence | `policy_declined` is decided before the subject is resolved and is byte-identical whether or not the record exists. | capsules-plugin `evidence_answer.rs` module doc; #206 "already answered" |
| Replay inside the window is refused | A per-key nonce cache over the request's signed deadline. | capsules-plugin `requester_auth.rs` (`ReplayCache`, 300 s); #206 |
| Oversized answers are refused, never truncated | A hard byte cap on the plugin's answer path. | capsules-plugin `MAX_ANSWER_BYTES`; #206 |
| Dispute-time requests are bound to one exchange and one party | An `exchange` subject is answerable only to the party named in the record; the reply is bound to the request by digest and nonce, retained as evidence, never asked twice. | `interop_respond.go`; ER -01 §5 |
| A flood costs the attacker, not the holder's record | Nothing on the wire is a judgment about the holder. A refused or absent answer is a fact about one attempt, never a verdict. | ER -01 §Discipline |
| The three outcomes never convert | A timeout is not a refusal; pending is not an outcome; a bundle that fails verification is received-and-failed, not a fourth state. | `request.go` (`AbsenceRecord`, received-and-failed); ER -01 §Discipline |

## What does not hold up yet

### Responder side

**R1. The responder holds the book lock while answering.** `Book.Respond` takes `b.mu` for the whole answer (`interop_respond.go` lines 225 to 226). Under a flood, even of cheap refused requests, the lock is held continuously and the node's own `Seal`, `Checkpoint` and receipt of a pushed half wait behind it. A denial of service against the endpoint becomes a denial of service against the node's own evidence. The security review marked it "load test before publishing". Owner: **#208**. Answer shape: a short-lock snapshot for reads, or separate locks with sealing taking precedence, and a load test with a stated sealing-latency bound.

**R2. No per-asker rate limit, and a GET can write.** #206 item 3. Every refusal is signed, and since capsules-plugin#145 a receipt GET can cut a checkpoint, which writes the log and may contact witnesses. Owner: **#206**. The staple removes the stranger case; the per-asker limit still matters for authenticated peers.

**R3. Replay across a restart, and the unbound-key nonce pool.** #206 items 1 and 2. In-memory replay state and a shared pool that can fill. Owner: **#206**.

**R4. Range bisection and cadence leakage.** #206 items 4 and 5. A counterparty can bisect ranges to locate a withheld record, and can read log size and cadence beyond its own records. Owner: **#206**; item 5 may be accepted in writing.

**R5. Requester key rotation and compromise.** #206 item 6. Owner: **#206**.

### Rule side

**P1. Recorded absence as a weapon.** The discipline that makes operators answer, "not answering is worse than answering", is also an attack surface: flood a holder during a dispute window and every legitimate asker records an absence against it. Two limits already exist: an absence is never a refusal, and it is chargeable only against a retention commitment. Nothing says what a reader does with an absence recorded while the holder was under attack. Owner: **agent-action-capsule#473**, which proposes a holder-sealed, witnessed "unavailable window" record that a reader shows beside the absence as the holder's statement, never as proof. This is a ruling, not a code change.

### Asker side

**A1. A hostile answer built to exhaust the verifier.** The plugin caps answer bytes; the Go reader in `request.go` has no stated caps on records, inclusion proofs, receipts or JSON depth before cryptography runs (the only size checks found at `5cf7016` are the MCP and A2A "message too large" guards on the request path). agent-action-capsule#456 records that Rust and Python readers already diverge past 128 nesting levels. Owner: **#209**. Answer shape: stated limits applied before any signature or proof check; exceeding one is received-and-failed with the limit named.

**A2. A fork that verifies.** A holder can answer `checkpoints` with a chain that is internally consistent, every consistency proof holding, but was never registered with a witness the asker trusts, or only with one the holder controls. Every per-property check passes. Only the witness receipt against the asker's trust set distinguishes it, and today the finding is folded into `not_checked` rather than named. Owner: **#209**. Answer shape: an `unwitnessed` finding distinct from `NOT_CHECKED`; a page never says "checked" of a chain that only self-verifies.

### Report side

**N1. A staple costs one key.** The growth report counts staples that verify. Anyone can mint nodes with signed, self-consistent, even witnessed checkpoints, so "nodes with a verifying staple" and "checkpoints cut per week" are inflatable at near zero cost. Witnessed checkpoints, key age and a controller declaration raise the cost but do not remove it. Owner: **evidence-network/loop#170**. Answer shape: a caveat line on the inflatable rows ("a staple costs one key; these rows measure evidence activity, not nodes or deals"), "distinct controllers" shown beside "nodes" with no ranking, and the existence-proof rows (decisions limited or refused; lab deals with outside nodes, settled, disputed) sourced only from the lab's own participation with real settlement. Lab-to-lab never counts.

## The one design move that removes most of this

Stop answering strangers over the request path. The commitment already carries the staple and travels on two carriers, the `commitment/1` mesh channel and the loop's `nodes/`. A relying party that holds the staple can admit at micro value with no round trip, and the network report can be built from collected staples with no request to any node. What remains on the request path is authenticated: a counterparty's `exchange` ask at dispute time, and an identified asker's `range` under a disclosure profile. That is a smaller, better-bounded surface, and R1, R2 and A1 are the items that still bite on it.

## Issue map

| Item | Issue | Repo | Kind |
|---|---|---|---|
| R1 responder lock | #208 | evidencebook | code, load test |
| R2–R5 flooding, replay, range, keys | #206 | evidencebook | answer doc, tests, decisions |
| P1 absence under attack | #473 | agent-action-capsule | ruling, spec text |
| A1, A2 asker-side limits and forks | #209 | evidencebook | code, vectors |
| N1 Sybil staples | #170 | evidence-network/loop | report rule, wording |

Each of #208, #209, #473 and loop#170 asks for its row in #206's answer doc, so #206 stays the one list.

## Not attacks, but easy to mistake for them

- **Sniffing the mesh relay for admission decisions.** Not possible and not wanted: iroh traffic is end-to-end encrypted, a relay sees only metadata, and admission decisions never go on the wire. They are local sealed records whose only outward sign is an accepted or refused request.
- **"Share your anonymised data to improve the service."** Admission records name peers by salted commitment, which the operator can reverse, and timing leaks a lot. Collecting them is telemetry and casts the network as a collector. The lab participating as a real counterparty is the honest source for step 11.

## Words

Counts carry their denominators. "Records sealed", never "deals", for anything a log counts. "Exchanges served, self-reported", for the service history's period count. "Unwitnessed", never "checked", for a chain with no trusted receipt. No score anywhere.
