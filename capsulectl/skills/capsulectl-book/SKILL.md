---
name: capsulectl-book
description: Call the capsulectl-book verbs to ask for, answer, close, reconcile and report on evidence in an evidence book — never to reason about business meaning or invent a workflow beyond the verbs listed below.
---

# capsulectl-book

This skill may call the book verbs below — request and answer evidence, close and reconcile a period, seal a Result and render its report — nothing else. It never reasons about business meaning, and never invents a workflow beyond the verb surface below. It works on a book profile (a jsonl profile with `log_id`, `log: book` (the owner the core requires; `--log book` when you create it), `operator`, a signing key the profile trusts and a checkpoint signing key it trusts) and extends the `capsulectl` skill: the core verbs (`verify`, `disclose`, `get`, `cll`, `publish`, `plugin ls` and the rest) are that skill's, and this one adds only what the `capsulectl-book` plugin provides.

## Guardrails

**May:**

- call only the verbs listed in this file
- request evidence from a counterparty, and answer a request received from one
- close a period and reconcile it against a counterparty's held bundle
- seal a Result a judging step produced, and render its report
- verify a bundle with `capsulectl verify` before treating it as authentic

**Must never:**

- create or modify a scope file, Evidence Contract, or licence
- write, edit or complete an Evidence Result v0 — seal only the document a judging step produced
- invent a relationship between a requirement and an evidence source
- modify, summarize, reinterpret, or restate the output of `verify`
- describe self-attested evidence as witnessed, countersigned, or independently verified
- transmit data off-machine without first showing exactly what will be transmitted; `book request` and `book respond` write the bytes to send and transmit nothing themselves

**Approval points:**

- Stop and ask a human whenever a command fails because of a licence, plugin, or authorization requirement. When a command says the `capsulectl-book` plugin is not installed, show the user the exact command it prints; never run `plugin install` or `plugin remove` yourself.

**Evidence policy:**

Every consequential action taken by this skill must produce an evidence record. If evidence generation fails, report `evidence unavailable`, stop before the next consequential action, and do not infer that the underlying action failed to occur.

## Verb surface

Each verb is run as `capsulectl book VERB ...`, which the core hands to the `capsulectl-book` plugin.

| Verb | Args | When | Emits | How |
|---|---|---|---|---|
| `book request` | `--profile NAME --request FILE [--sent] --responder NAME --output FILE`<br>`--profile NAME --for RECORD_ID --response FILE --responder-key HEX --responder-checkpoint-key HEX [--witness-directory FILE]`<br>`--profile NAME --for RECORD_ID --absent-until TIME` | Ask a party for evidence (the request is recorded before it is sent), then record exactly one outcome for it: the answer that came back, or that nothing came back in the window. The output file is what to transmit; this verb transmits nothing itself. The request is the evidence-interop/0 request map, or with `--sent` a request another tool already sent (such as the capsules plugin's), recorded exactly as given so its answer verifies; the answer is a signed envelope or refusal. A recorded artifact prints `witness`: `witnessed` only when a receipt from a witness in `--witness-directory` covers its anchor checkpoint; `unwitnessed` means it checks only against the holder's own signatures, so never report it as witnessed or fully checked. Run it on a jsonl book profile only: on a remote profile it is the deprecated alias of `book remote request`, which asks another party's book over the profile's transport, so there stop and ask a human. | (each invocation's own book record is the evidence; no wrapper capsule) | primary action (running it *is* the record) |
| `book respond` | `--profile NAME --request FILE --requester ID --output FILE`<br>`--profile NAME --request FILE --requester ID --policy FILE --output FILE` | Answer a received evidence request with a signed artifact or a signed refusal, under the share policy, and record that it was answered. --requester is the identity you authenticated the sender as; without it only public subjects are answered. A malformed request is refused, and an unauthenticated one answered or refused, without a book record. The output file is what to send back; this verb transmits nothing itself. | (the book's record of the answered request is the evidence; no wrapper capsule; an empty `record_id` in the result means the answer is not on record, so show it to the user before it is sent) | primary action (running it *is* the record) |
| `book close` | `--profile NAME --period day\|week\|month --counterparty ID`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty ID`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty ID --since-last`<br>`--profile NAME --period day\|week\|month --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX`<br>`--profile NAME --period day\|week\|month --counterparty ID --capsule-out FILE --bundle-out FILE` | Seal the book's Close for a period that has ended (by default the most recent one; --date names any date inside the period), for one counterparty. The Close seals agent-action-capsule's close/v1 body: the period, and the counts by record type and head of the window it binds, which under --since-last can start before the period. It seals tallies only when the --peer bundle discloses the counterparty's Close for this period, which it then cites; otherwise it states counts only, and the reconciliation it prints (every exchange INSUFFICIENT without a peer bundle) is not sealed, so no reconcile claim can cite it. Repeating it for the same period and counterparty returns the first Close and seals no second one; the peer bundle and --since-last of a repeat are not applied. | (the Close record itself; --capsule-out writes it for `capsulectl verify`) | primary action (running it *is* the record) |
| `book reconcile` | `--profile NAME --period day\|week\|month --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX`<br>`--profile NAME --period day\|week\|month --date YYYY-MM-DD --counterparty BOOK_ID --peer FILE --peer-checkpoint-key HEX` | Compare this book's period (by default the most recent one that has ended; --date names any date inside it) with a counterparty's held bundle, showing each exchange in one of six states, as of this book's latest checkpoint: an own exchange not yet checkpointed reads INSUFFICIENT until `book close` checkpoints it. It never queries the counterparty and adds no record. | (no capsule — comparing held records is local comparison, not a consequential action) | not consequential — no capsule |
| `book result build` | `--profile NAME --result FILE --out FILE`<br>`--profile NAME --result FILE --contract REF --out FILE --capsule-out FILE` | Seal a caller-supplied, schema-valid Evidence Result v0 into the book as the record a report is rooted on, citing every record its claims name. Refuses a document whose headline values do not recompute from its claims, or that cites a record the book does not hold, or a close_state or tallies that differ from what the cited Close's links and statement read. A repeat of the same document seals nothing new. Only UNILATERAL close claims are reachable today (see the reference). | (the Result's evidence_result record; --capsule-out writes it for `capsulectl verify`) | primary action (running it *is* the record) |
| `book report build` | `--bundle FILE --card attribution\|human_role\|obligation\|outcome\|process\|quality\|settlement --out FILE`<br>`--bundle FILE --card CARD --presentation FILE --out FILE --permalink`<br>`--bundle FILE --card CARD --out FILE --dry-run` | Render a held, disclosed Evidence Bundle whose root is a sealed Result v0 into an offline report.html (and, with --permalink, a viewer permalink) after verifying it; never for a bundle that does not verify or whose root is not a Result. --dry-run writes the page marked draft and mints no permalink. A permalink carries the whole disclosed bundle in its URL fragment, so sharing the link transmits the bundle. | (no capsule — rendering a held bundle is local; the disclose act that produced the bundle is already on record) | not consequential — no capsule |

## Capsule emission

**Records are for consequential actions only** (see Evidence policy under Guardrails, above). The `How` column says which of the two cases applies:

- **primary action** — the verb's entire job already is persisting a signed book record (`book request`, `book respond`, `book close`, `book result build`). Running it satisfies the invariant by itself; nothing wraps it, and the record id it returns is what to cite. The exceptions are `book respond` to a malformed request or one with no authenticated requester: its signed answer or refusal may leave no record (`record_id` empty), which is reported, never hidden.
- **not consequential** — `book reconcile` and `book report build` only read, compare or render held records. They run and add no record: **no wrapper seal, no synthetic evidence record.**

See [references/book.md](references/book.md) for the request, Close and Result details and what `book result build` refuses.

## Exercise this skill

[scripts/run-scripted-demo.sh](scripts/run-scripted-demo.sh) runs every verb above against two throwaway jsonl book profiles in a temp directory, with `capsulectl` and `capsulectl-book` built from a source checkout: the consequential verbs (`book request`, `book respond`, `book close`, `book result build`) each return the signed book record that is their evidence, and the script counts them; `book reconcile` and `book report build` run and add no record — the script asserts both halves. It never touches a real profile or a real log. It is a contributor check, run after any change to the verb surface: it needs Go, `jq` and this repository. Using the skill needs neither: install the release binaries of `capsulectl` and `capsulectl-book`.

## Reference

- [Book verbs](references/book.md) — what each book verb does and refuses, in the detail this skill is built against.
- [capsulectl skill](https://github.com/action-state-group/capsule-cli/tree/main/skills/capsulectl) — the core verbs this skill builds on.
