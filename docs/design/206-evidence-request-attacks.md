# evidencebook#206: attacks on the evidence request, what holds and what does not

Status: the answer doc for #206. Written 2026-10-30 from the attack discussion of 2026-10-29 and the 2026-10-24 security review of the ARD / AI Catalog proposal; the answer table was added 2026-11-01. Code references in the sections below are to this repository at `5cf7016` and to Mesh-LLM/capsules-plugin at `86c6062` unless stated; the table's are to both repositories' `main` on 2026-11-01.

## Answers, one row per attack

Each attack #206 lists, and each row added to it since, has one row: what answers it (a test, or a decision with who made it) and where that stands. "Plugin" is the capsules-plugin mesh responder (`crates/evidence-plugin/src/`), which checks signed requests; "book" is this repository's responder (`Book.Respond`, with Rust and Python parity), whose caller names the asker. Plugin tests are unit tests in the file named.

| # | Attack | Answer | Status | Applies to |
|---|---|---|---|---|
| K1 | Replay of a signed request inside its window | A per-key nonce cache held until the request's deadline, `MAX_WINDOW_SECS` 300 (`requester_auth.rs` `ReplayCache`). Test: `a_nonce_is_used_once_until_its_deadline` (`requester_auth.rs`). | answered | plugin |
| K2 | A stranger learns whether a record exists | `policy_declined` is decided before the subject is resolved, so it is the same whether or not the record exists. Test: `an_unsigned_record_request_is_refused_alike_for_any_subject_before_the_ledger_is_read` (`evidence_answer.rs`). | answered | plugin |
| K3 | Oversized answers | Refused over `MAX_ANSWER_BYTES`, never truncated. Test: `an_answer_over_the_stream_cap_is_declined` (`evidence_answer.rs`). The book caps the request (`MaxRequestBytes`) and the items per artifact (`MaxAnswerItems`). | answered | both |
| K4 | An unsigned or unbound asker gets identity-dependent records | Refused; a valid signature under a key no host binds reads as unsigned only where the answer needs no identity. Tests: `an_unbound_key_reads_as_unsigned_only_where_no_identity_is_needed`, `a_failed_request_is_never_answered_as_unsigned` (`evidence_answer.rs`). | answered | plugin |
| 1 | Replay across a responder restart (the nonce cache is in memory) | The cache keeps one number on disk, the latest deadline it may hold for a bound key (`replay-horizon` in the data directory), written and synced before a bound key's nonce with a later deadline is held. After a restart it holds no nonce whose deadline is at or before that horizon: a bound key's request is refused as when its nonce cache is full (`capacity_exceeded` under the v1 profile), retryable with a later deadline, and an unbound key's reads as unsigned, replay-unchecked. Test: `a_nonce_held_before_a_restart_is_refused_after_it` (`requester_auth.rs`), failing before the fix. Residual: a node upgraded from a version that wrote no horizon is unprotected for that one restart. See R3. | answered: Mesh-LLM/capsules-plugin#237 | plugin |
| 2 | The unbound-key nonce pool fills up | While it is full, a new unbound nonce is not held and the request is answered as unsigned, labelled replay-unchecked (ruling u290 §2), so a replay gets only what any asker gets. Tests: `a_full_unbound_pool_answers_as_unsigned_and_still_refuses_a_replay` (`evidence_answer.rs`), `a_full_unbound_pool_leaves_new_nonces_unchecked_and_bound_keys_alone` (`requester_auth.rs`). | answered | plugin |
| 3 | Request flooding and amplification | Plugin: bounded in-flight pools, 8 for proven principals and 2 for strangers (`strangers_cannot_exhaust_the_pool_a_proven_principal_draws_from`), and one key holds at most half the proven pool (`one_key_cannot_hold_the_whole_proven_pool`, `mesh_evidence_bridge.rs`, failing before the fix). Book: `Respond` answers from a snapshot and does not stall sealing (`TestRespondFloodDoesNotStallSealing`, #215). Accepted, Sherlock2 confirming: no per-second rate limit (the book's `ResponderPolicy.Limiter` stays off by default); each request is bounded in bytes and records; a refusal costs one Ed25519 signature; no remote request cuts a checkpoint. See R1 and R2. | answered: Mesh-LLM/capsules-plugin#239, #215 | both |
| 4 | A withheld record located through whole-or-refused answers | A `never_leaves` record is dropped from every remote asker's matched set before counting and gating, so the answer is the same with or without it (Steven's ruling: fix). Test: `a_record_that_never_leaves_changes_no_keyed_answer` (`evidence_answer.rs`, Mesh-LLM/capsules-plugin#168). `range` is refused to every asker, so bisection is not open. The book has no `never_leaves` equivalent. See R4. | answered: Mesh-LLM/capsules-plugin#168 | plugin |
| 5 | A counterparty reads the log's size and cadence | Accepted in writing, Sherlock2 confirming: signed checkpoints, with their sizes and times, are stranger-safe by design (PRD R-8), so anyone reads size and cadence from the checkpoint list. What only a counterparty gains is the refusal-reason difference, which is row 8. | accepted | both |
| 6 | Requester key compromise or rotation | Rotation: a newly announced key replaces the old one at the next poll of the host's keys (every 30 s, `peer_keys.rs` `EVERY`), and a peer the host lists without a key loses it then. Gap: a peer the host stops listing keeps its last key. Decided (Sherlock2 confirming): an unlisted peer's key expires after a stated TTL, and the host can withdraw a key explicitly over `plugin_keys.v1`; the worst case to stop honouring a key is the poll interval plus the TTL. See R5. | decided; not built: Mesh-LLM/capsules-plugin#232 | plugin |
| 7 | The responder picks which records to answer with | Interval coverage is withheld, so a producer-selected answer verifies only as INCOMPLETE (capsule-viewer#159). Decided (Sherlock2 confirming): one vector per answer kind shows no producer-selected answer verifies as COMPLETE; any kind that does is a finding of its own. | decided; vectors not built: Mesh-LLM/capsules-plugin#233 | plugin |
| 8 | The unheld-key oracle under the `specific` refusal policy | `uniform` folds `no_such_subject` and `not_authorized` into `policy_declined` (`TestRefusalsDoNotEnumerateRecords`, uniform case). Steven ruled that `uniform` becomes the default and `specific` an operator opt-in. See R6. | decided; not built: #225 | book |
| 9 | Left out before sealing, and the answer still looks complete | The requester reconciles its own sealed halves against the provider's answers (`ReconcileAsked`). Tests: `TestReconcileAskedNamesExactlyTheUnsealed`, `TestReconcileAskedVectors`. See R7. | answered: #229 | book |

## The frame

The evidence request (`draft-mih-agent-evidence-request` -01, served here by `Book.Respond` and read by `request.go`) was designed as a scoped ask between two parties: one subject, one checkpoint to verify against, one of three outcomes. It was never designed as a public polling surface. Most of the attack surface discussed below comes from treating it as one, and most of the answer is the move already under way in the loop: the stranger's question is answered by a published staple (the node's commitment with its `history_card/1`), collected off the mesh channel and the loop repository and verified offline, so a node need answer no stranger at all.

Three parties can be attacked: the responder (the holder's node), the asker (the relying party's verifier), and the network report that counts what nodes publish. #206 lists the responder-side attacks. This note places those, adds the asker-side and report-side ones, and names the issue that owns each open item.

## What holds up, by construction

| Property | Why it holds | Where |
|---|---|---|
| The stranger's question is cheap and static | `checkpoints` and `history_card/1` carry no records, only the checkpoint chain, which grows with the log; they are cacheable per epoch, and the plugin publishes them as the staple inside its commitment. Both responders cap the chain (Go `MaxAnswerItems`, default 10,000; the plugin's `MAX_ANSWER_CHECKPOINTS`, 1024) and decline a longer one, so past the plugin's cap the staple stops building. A node can serve strangers from a static object, or not serve them at all. | capsules-plugin `commitment.rs` (`STRANGER_SAFE`), `evidence_answer.rs` (`MAX_ANSWER_CHECKPOINTS`); `interop_artifact.go`; ER -01 §8.1 |
| Expensive forms are default-denied | `range`, `correlation` and `full_history` are refused under every `SharePolicy` setting; the only grant is a recipient-specific derivation, answered to the one requester it names. A stranger cannot force a bundle to be built, so amplification is bounded by the size of a card. (After 5cf7016, #207 gates `correlation` like `exchange`, and #213 answers `full_history` under `standard_history/1`.) | `interop_respond.go` (`SharePolicy`, `Authorize`, `mayHave`); ER -01 §8 |
| A refusal leaks nothing about existence | To an asker no record names, `policy_declined` is decided before the subject is resolved, so it is the same whether or not the record exists (byte-identical for the same request in the same second; it carries `issued_at`). | capsules-plugin `evidence_answer.rs` module doc; #206 "already answered" |
| Replay inside the window is refused for a bound key | A per-key nonce cache over the request's signed deadline. An unbound key's nonce goes to a shared pool; when it is full, the request is answered as unsigned and labelled replay-unchecked, so a replay gets only what any asker gets. | capsules-plugin `requester_auth.rs` (`ReplayCache`, 300 s, `MAX_UNBOUND_NONCES`); #206 |
| Oversized answers are refused, never truncated | A hard byte cap on the plugin's answer path; in Go, a request-size cap before parsing and an item cap per artifact. | capsules-plugin `MAX_ANSWER_BYTES`; `interop_respond.go` (`MaxRequestBytes` 64 KiB, `MaxAnswerItems` 10,000); #206 |
| Dispute-time requests are bound to one exchange and one party | Under the default `counterparties` setting, an `exchange` subject is answerable only to the party named in the record (`peers` answers any authenticated peer); the reply is bound to the request by digest and nonce, retained as evidence, never asked twice. | `interop_respond.go` (`mayHave`); ER -01 §5 |
| A flood costs the attacker, not the holder's record | Nothing on the wire is a judgment about the holder. A refused or absent answer is a fact about one attempt, never a verdict. | ER -01 §Discipline |
| The three outcomes never convert | A timeout is not a refusal; pending is not an outcome; a bundle that fails verification is received-and-failed, not a fourth state. | `request.go` (`AbsenceRecord`, received-and-failed); ER -01 §Discipline |

## What does not hold up yet

### Responder side

**R1. The responder holds the book lock while answering.** `Book.Respond` takes `b.mu` for the whole answer (`interop_respond.go` lines 225 to 226), and the rate limiter (R2) runs under it too, so even a rate-limited refusal takes the lock. Under a flood, even of cheap refused requests, the lock is held continuously and the node's own `Seal`, `Checkpoint` and receipt of a pushed half wait behind it. A denial of service against the endpoint becomes a denial of service against the node's own evidence. The security review marked it "load test before publishing". Owner: **#208**. Answer shape: a short-lock snapshot for reads, or separate locks with sealing taking precedence, and a load test with a stated sealing-latency bound.

**Answer.** `Respond` answers from a snapshot of the log taken under a short lock (#215, merged as `0dcf547`). It takes the lock again only to issue an anchor for an authenticated freshness floor and to seal `evidence_request_answered`. Two tests cover it:

- `TestRespondDoesNotHoldTheLockWhileAnswering` shows that an answer in progress never blocks a seal.
- `TestRespondFloodDoesNotStallSealing` (`respond_lock_test.go`) is the load test. Flooders keep requests in flight, every answer costs 1 s, and the book seals at a fixed rate. Every seal must land, and in order. With an authenticated requester flooding, 95% of seals finish within 500 ms; with strangers flooding (`checkpoints`, `history_card/1` and a refused `full_history`), every seal finishes within 2 s. Before #215, seals took seconds.

**Residual.**

- An authenticated flood seals one `evidence_request_answered` record per answer under the lock, so a seal can queue behind those, and the log grows by one record per answer.
- The rate limiter and the snapshot still share the short lock, so a rate-limited refusal takes it briefly.
- A seal can wait for one CLL tree read already in progress, because the log store reads under the same lock its appends take.
- capsules-plugin's answer path has its own bounded in-flight pools (`mesh_evidence_bridge.rs`: 8 permits for proven principals, 2 for strangers); the book's responder has none.

Status: answered (#208).

**R2. Per-asker rate limiting is off by default.** #206 item 3. The Go responder has a per-requester limiter (`ResponderPolicy.Limiter`, `WindowLimiter`), applied before parsing, but it is nil by default and every unauthenticated caller shares one `""` bucket. Every refusal is signed. The receipt GET that cuts a checkpoint since capsules-plugin#145 is the owner's own local route, so a remote asker cannot make the log write. Owner: **#206**. The staple removes the stranger case; the per-asker limit still matters for authenticated peers.

**Answer.** capsules-plugin caps what one proven key holds of its proven pool at half (`MAX_IN_FLIGHT_PER_KEY`, 4 of 8), so one counterparty flooding a node leaves room for the others; past its share a request is declined as when the pool is full (`capacity_exceeded`, `policy_declined` under `evidence-interop/0`) before the ledger is read (Mesh-LLM/capsules-plugin#239, test `one_key_cannot_hold_the_whole_proven_pool`). Keys a controller declaration binds share their anchor's share, as they share its nonce budget. The rest is accepted, as Sherlock2 confirmed on #206: no per-second rate limit is added, and the book's limiter stays off by default, because each request is already bounded in bytes and records, a refusal costs one signature, and no remote request makes the log write.

**Residual.** Several colluding proven keys together can still fill the proven pool; strangers keep their own pool.

Status: answered.

**R3. Replay across a restart, and the unbound-key nonce pool.** #206 items 1 and 2. Replay state is in memory, so a restart forgets it; that is open. The shared unbound-key pool can fill, but a request it cannot hold is answered as unsigned (ruling u290 §2), so item 2 needs only its citation. Owner: **#206**.

**Answer.** The replay cache keeps the latest deadline it may hold for a bound key in the data directory (`replay-horizon`), written and synced, directory entry included, before any bound key's nonce with a later deadline is held, at most about once per 30 s of deadlines. An unbound key never moves it: its request reads as unsigned, so its replay check stays in memory, and a key anyone can make cannot lengthen what a restart refuses. After a restart it holds no nonce whose deadline is at or before that horizon, since the run before may have held it: a bound key's request is refused as when its nonce cache is full (`capacity_exceeded` under the v1 profile), which the asker can retry with a later deadline, and an unbound key's reads as unsigned, replay-unchecked, as when its pool is full. A horizon that cannot be read counts as the latest any held nonce could have (300 s plus one step from the start); if the horizon cannot be written, a bound key's nonce is not held and its request is refused (fail closed), and the node logs the failure once as an error. A new request is refused only while its deadline is at or before the horizon, which is the latest deadline held before the restart plus at most 30 s: with this plugin's askers, whose deadlines are 120 s ahead, that ends about 30 s after the last request the node answered before the restart, and a stop longer than the horizon refuses nothing (Mesh-LLM/capsules-plugin#237, test `a_nonce_held_before_a_restart_is_refused_after_it`). The How ack proposed refusing every short deadline for 300 s after each start with nothing on disk; one stored number avoids refusing every signed request after every restart. Item 2 is answered by its citations in the table.

**Residual.** A node upgraded from a version that wrote no horizon is unprotected for that one restart.

Status: answered.

**R4. Locating a withheld record, and cadence leakage.** #206 items 4 and 5. `range` is refused to every asker, so bisection is not open. But a counterparty asking `correlation`, `exchange` or `exchange_corroboration/1` over a key gets a refusal of the whole answer when a matched record is `never_leaves`, which locates a withheld record. Log size and cadence are public in the checkpoint list; what a counterparty gains beyond that is only which refusal it gets. Owner: **#206**; item 5 may be accepted in writing.

**Decision, item 4.** Fix (Steven, 2026-10-30, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=79b46dfbf4f038b19623ec4c593c5eb44260a78c1907c19c5e8b245efc52dfd9). A `never_leaves` record leaves no one's answer, so it drops out of the matched set for every remote asker before counting and gating, and the answer is the same whether or not one exists. Every remote answer stays INCOMPLETE, so dropping a record never yields a false COMPLETE. It lands after the vectors of #206 row 7. Implementation: Mesh-LLM/capsules-plugin#161. The book's Go responder has no `never_leaves` equivalent.

Status: answered (Mesh-LLM/capsules-plugin#168, test `a_record_that_never_leaves_changes_no_keyed_answer`). Item 5 is accepted in writing (the table's row 5).

**R5. Requester key rotation and compromise.** #206 item 6. Owner: **#206**.

**Decision.** Rotation works today: the host's newer key replaces the old one at the next poll, every 30 s. Withdrawal does not when the host stops listing the peer, since the registry keeps that peer's last key. Fix: an unlisted peer's key expires after a stated TTL, and the host can withdraw a key explicitly over `plugin_keys.v1`; the worst case to stop honouring a key is the poll interval plus the TTL (Sherlock2 confirmed). The TTL can refuse a returning peer until it re-announces.

Status: decided; not built (Mesh-LLM/capsules-plugin#232).

**R6. The unheld-key oracle under `specific`.** #206 row 8, from #207. Under `RefusalPolicySpecific`, the reason a counterparty gets for a key it doesn't hold (`not_authorized` against `no_such_subject` or `retention_expired`) tells it whether the holder has the record. `RefusalPolicyUniform` folds them into one answer (`TestRefusalsDoNotEnumerateRecords`, uniform case). Owner: **#206**.

**Decision.** `uniform` becomes the default refusal policy, with `specific` an operator opt-in (Steven, 2026-10-30, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=79b46dfbf4f038b19623ec4c593c5eb44260a78c1907c19c5e8b245efc52dfd9), reversing his Q3 on #114. Implementation: #225, in Go, Rust and Python; a breaking change to the default.

Status: decided; not built.

**R7. Left out before sealing, and the answer still looks complete.** #206 row 9 (Steven, 2026-10-31). A provider serves an exchange and never seals it. When the counterparty later asks for the pair's records (`correlation`, `exchange` or `exchange_corroboration/1`), every answer verifies, and the records it does hold number without a gap in their per-pair `seq`/`prev_seq`, because the provider numbers only what it seals. Sequence numbers catch deleting a sealed record; a record never sealed leaves no gap and shows only in the counterparty's half (Mesh-LLM/mesh-llm#1965 §3, as corrected on 2026-10-31). Row 7 does not cover it: row 7 is a responder choosing among records it sealed. Owner: **#206**.

**Answer.** The requester reconciles. `ReconcileAsked` (`reconcile_asked.go`) takes the requester's own sealed halves of one pair and, for each, what came back when it asked the provider for that exchange by the exchange's two digests. It names every half the provider confirmed it holds no record of: a verified answer that carries no record of the exchange, or a signed `no_such_subject`, each only at an anchor the caller states covers the exchange (`Covers`: the anchor postdates the exchange and every record the provider sealed for it). Any other verified refusal (`policy_declined`, `not_authorized`) is REFUSED: never omitted, since a uniform refusal reads the same whether the record exists or not, but reported on its own, because an honest provider does not refuse its counterparty about their own exchange. An answer at an older anchor, a half with no digests to ask by, a matching record with no record id, an absence or an answer that failed verification is UNRESOLVED. The verdict is OMISSIONS_FOUND, NO_OMISSION_FOUND or UNRESOLVED (when nothing is omitted and something is REFUSED or UNRESOLVED), never COMPLETE (row 7). It also reports the provider's numbering across the records that matched, as a separate fact. Halves match records by request and response digest, one record to at most one half, as a maximum matching, so the order of the input never changes how many are omitted; an `exchange_id` is not a join key here, because each side's host assigns its own.

The reconcile trusts the caller's verification and does not repeat it: the caller checks each answer's signature, anchor and inclusion proofs under the provider's key, and states coverage. In S08 that caller is the asker's capsules plugin (`mesh_evidence_request`, which verifies under the provider's announced key), and the reconcile runs on what it verified; step 08 states coverage because the liar cuts a checkpoint at every record it pushes and 08 asks only after step 07 saw no push for 30 s, so the newest anchor, which a `min_freshness` 1 answer uses, postdates every record it sealed for those exchanges. A book-side re-verification of the plugin's reply is not built.

Tests: `TestReconcileAskedNamesExactlyTheUnsealed` (a provider seals 4 of 10 exchanges; the reconcile names exactly the other 6, whether the provider refuses `no_such_subject` or answers without the record), `TestReconcileAskedMatchingIgnoresOrder` and `TestReconcileAskedVectors` over `testdata/reconcile-asked-v0/vectors.json`, whose first case is the S08 dry run's capture (6 exchanges, 3 sealed, numbered 1 to 3 with no gap, the other 3 named). Shown live in S08 (company-internal-all `devcon8/s08-liar/`, step 08).

**Residual.**

- Bilateral traffic only: an exchange the requester never sealed cannot be asked about, so one both sides left out stays invisible (Mesh-LLM/mesh-llm#1965 §6).
- OMITTED sets the provider's signed "no record" against the requester's own half. To a third party that is a contradiction between two parties, not proof that the exchange happened, unless the half carries something only the provider could have made, such as a provider-signed response. In S08 the asker's halves are its own sealed records of the digests it sent and received; they carry no provider signature, so S08 shows the contradiction, signed by both sides.
- A provider that refuses uniformly (`policy_declined`) leaves the halves it left out REFUSED, not OMITTED, and the pair's verdict UNRESOLVED: the requester's own half still shows the exchange happened, and the refusal is a finding of its own, but the provider's answer does not confirm it holds none.
- Failed exchanges, which need a half on each side to count, are #205.
- Go only; Rust and Python parity is not built.

Status: answered (row 9).

### Rule side

**P1. Recorded absence as a weapon.** The discipline that makes operators answer, "not answering is worse than answering", is also an attack surface: flood a holder during a dispute window and every legitimate asker records an absence against it. Two limits already exist: an absence is never a refusal, and it is chargeable only against a retention commitment. Nothing says what a reader does with an absence recorded while the holder was under attack. Owner: **agent-action-capsule#473**, which proposes a holder-sealed, witnessed "unavailable window" record that a reader shows beside the absence as the holder's statement, never as proof. Steven approved it on 2026-10-30, amended so the window's witness time is shown relative to the absence and the reason class is a closed list (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=c2a3350f17c9dc910f8bc618b2c606cd0e899042ee35a4b2729fdfd358497b7f). This is a ruling and spec text, not a code change.

**Answer.** Evidence Request -02 §4.7, "Unavailable-Window Statements" (agent-action-capsule#473; spec PR agent-action-capsule#476). The holder may seal a signed statement giving a start, an end and a `reason_class` from a closed list (`flood`, `maintenance`, `outage`, `other_declared`). Only a receipt from a transparency service the reader accepts as independent of the holder registers and dates it. A reader that holds an absence and a covering statement shows "no answer; holder states unavailability" with the class and the stated period, never as a refusal or as proof of an attack, and labels it "stated afterwards" when it was registered after the asker's window closed. The absence and the retention commitment are unchanged; how much the statement weighs is relying-party policy. Vectors: `vectors/evidence-request/unavailable-window.json` in agent-action-capsule (35 cases).

Status: decided in spec; ER -02 §4.7 merged in agent-action-capsule#476 (`d3bd61b`), and -02 is posted as `draft-mih-agent-evidence-request-02` (dated 30 October 2026; the IETF archive copy's SHA-256 is `ee5e2b90bda08fd956c271f512b52f4a834bcff3a1cae0144f644f5955bef948`). Implementation, not built: #223 (seal and read statements), capsule-viewer#164 (draw it), agent-action-capsule#477 (the Go and Rust readers run the vectors).

### Asker side

**A1. A hostile answer built to exhaust the verifier.** The plugin caps answer bytes; the Go reader in `request.go` has no stated caps on records, inclusion proofs, receipts or JSON depth before cryptography runs (the only size checks found at `5cf7016` are the MCP and A2A "message too large" guards on the request path). agent-action-capsule#456 records that Rust and Python readers already diverge past 128 nesting levels. Owner: **#209**. Answer shape: stated limits applied before any signature or proof check; exceeding one is received-and-failed with the limit named.

**Answer.** The requester states its limits and checks each one before the work it bounds (#216, merged as `3234231`). The limits are exported constants, with the same values in Python (`python/evidencebook/_limits.py`):

| Limit | Go | Value |
|---|---|---|
| Reply or bundle size | `MaxReplyBytes` | 8 MiB |
| JSON nesting | `MaxReplyDepth` | 32 |
| Verification entries, checkpoints, history entries, records | `MaxReplyItems` | 10,000 |
| Carried witness receipts | `MaxReplyReceipts` | 64 |

What happens over a limit depends on what it falls on:

- Unauthenticated octets (the reply's size, its outer JSON nesting, the carried receipts): the reply is refused unrecorded, and the request stays open for the genuine answer. Recording octets nobody signed would let anyone on the path decide the outcome of the real answer.
- What the responder signed (the counts, and the artifact's own JSON nesting; a bare `record` artifact is hashed and never decoded, so it has no depth check): checked once the artifact hashes to the signed digest, and before any checkpoint signature or proof check. Over a limit, the reply is received and failed, as `artifact_verification_failed` with the limit named.
- Bundles: `VerifyBundle` refuses a bundle over any limit before the AAC verifier runs.

Vectors are in `testdata/asker-limits/limits.json`, and Go (`TestLimitVectors`) and Python (`test_limits.py`) agree on them. `TestAReplyOverAnOctetLimitIsRefusedAndTheRequestStaysOpen` shows each octet refusal and the genuine answer recording after it; `TestASignedEnvelopeOverACountLimitIsReceivedAndFailed` and `TestBundleLimits` cover the other two cases. Each refusal has a stated time bound, and the tests fail past it: every limit vector is refused within 1 s, and a hostile reply through `RecordResponse` or a bundle over a limit within 2 s.

Status: answered (#209).

**A2. A fork that verifies.** A holder can answer `checkpoints` with a chain that is internally consistent, every consistency proof holding, but was never registered with a witness the asker trusts, or only with one the holder controls. Every per-property check passes. Only the witness receipt against the asker's trust set distinguishes it, and today the requester is silent: for `checkpoints` and `history_card/1` it checks signatures and consistency only and reports no finding (the witness directory is read only by `VerifyBundleAnswer`). Owner: **#209**. Answer shape: an `unwitnessed` finding distinct from `NOT_CHECKED`; a page never says "checked" of a chain that only self-verifies.

**Answer.** `ResponseStatement.witness` reads `witnessed` only when the material carried the anchor's signed checkpoint and a receipt from a witness in the requester's `ResponderKeys.Witnesses` verifies over that checkpoint (#216). Anything else that verifies reads `unwitnessed`: a chain that verifies only against its holder's signatures, a fork witnessed only by a holder-run witness the requester doesn't list, and an answer kind with no anchor checkpoint in hand. A receipt that fails to verify counts for nothing and never fails the answer: nothing signs the receipt carrier, so a forged receipt cannot decide a genuine answer's outcome. `VerifyBundleAnswer` adds the finding `unwitnessed`. Vectors: `testdata/asker-limits/witness.json` (`TestWitnessVectors`, `test_limits.py`). Tests: `TestRecordResponseWitnessStanding`, `TestBundleWitnessStanding`.

The pages say it too: `capsulectl book request --response` prints the standing (#221), capsule-viewer's evidence report and banner show an unwitnessed chain as unwitnessed (capsule-viewer#163, capsule-viewer#165), capsule-cli documents the flag (capsule-cli#496), and the capsules-plugin Evidence tab marks only an anchor with a trusted receipt as witnessed (Mesh-LLM/capsules-plugin#156).

Status: answered (#209).

### Report side

**N1. A staple costs one key.** The growth report counts staples that verify. Anyone can mint nodes with signed, self-consistent, even witnessed checkpoints, so "nodes with a verifying staple" and "checkpoints cut per week" are inflatable at near zero cost. Witnessed checkpoints, key age and a controller declaration raise the cost but do not remove it. Owner: **evidence-network/loop#170**. Answer shape: a caveat line on the inflatable rows ("a staple costs one key; these rows measure evidence activity, not nodes or deals"), "distinct controllers" shown beside "nodes" with no ranking, and the existence-proof rows (decisions limited or refused; lab deals with outside nodes, settled, disputed) sourced only from the lab's own participation with real settlement. Lab-to-lab never counts.

## The one design move that removes most of this

Stop answering strangers over the request path. The commitment already carries the staple and travels on two carriers, the `commitment/1` mesh channel and the loop's `nodes/`. A relying party that holds the staple can admit at micro value with no round trip, and the network report can be built from collected staples with no request to any node. What remains on the request path is authenticated: a counterparty's `exchange` ask at dispute time, and an identified asker's `range` under a disclosure profile. That is a smaller, better-bounded surface, and R1, R2 and A1 are the items that still bite on it.

## Issue map

| Item | Issue | Repo | Kind | Status |
|---|---|---|---|---|
| R1 responder lock | #208 | evidencebook | code, load test | answered: #215 |
| R7 left out before sealing | #206 row 9 | evidencebook | code, vectors, answer doc | answered: `ReconcileAsked` |
| R2–R6 flooding, replay, range, keys, refusal oracle | #206 | evidencebook | answer doc, tests, decisions | answered: R2 (Mesh-LLM/capsules-plugin#239), R3 (Mesh-LLM/capsules-plugin#237), R4 (Mesh-LLM/capsules-plugin#168; item 5 accepted); decided, not built: R5 (Mesh-LLM/capsules-plugin#232), R6 (#225); row 7's vectors: Mesh-LLM/capsules-plugin#233 |
| P1 absence under attack | #473 | agent-action-capsule | ruling, spec text | decided: ER -02 §4.7, merged in agent-action-capsule#476; implementation open: #223, capsule-viewer#164, agent-action-capsule#477 |
| A1, A2 asker-side limits and forks | #209 | evidencebook | code, vectors | answered: #216, #221, capsule-viewer#163 and #165, Mesh-LLM/capsules-plugin#156 |
| N1 Sybil staples | #170 | evidence-network/loop | report rule, wording | open |

Each of #208, #209, #473 and loop#170 asks for its row in #206's answer doc, so #206 stays the one list.

## Not attacks, but easy to mistake for them

- **Sniffing the mesh relay for admission decisions.** Not possible and not wanted: iroh traffic is end-to-end encrypted, a relay sees only metadata, and admission decisions never go on the wire. They are local sealed records whose only outward sign is an accepted or refused request.
- **"Share your anonymised data to improve the service."** Admission records name peers by salted commitment, which the operator can reverse, and timing leaks a lot. Collecting them is telemetry and casts the network as a collector. The lab participating as a real counterparty is the honest source for step 11.

## Words

Counts carry their denominators. "Records sealed", never "deals", for anything a log counts. "Exchanges served, self-reported", for the service history's period count. "Unwitnessed", never "checked", for a chain with no trusted receipt. No score anywhere.
