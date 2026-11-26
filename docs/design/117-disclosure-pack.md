# evidencebook#117: the disclosure pack profile v0

Status: converged with Codex (plan review and three PR rounds; Kimi was unavailable, every call
ending "Prompt turn ended with reason: failed"). Sherlock2's PM review (2026-11-26, evidencebook#1005)
is folded in, with its answers in §10, as is the Sherlock2 twin's point on u631 and u632. Builds
nothing. Written 2026-11-26 CST against evidencebook main `df54098c`, Mesh-LLM/capsules-plugin
main `251f6eb`, capsule-cli main `052a8a6a`, evidence-network/loop main `b96e67c2` and
agent-action-capsule main at the same date.

## The ask and what is already ruled

Roadmap 4.4 asks for `disclosure-baseline@1`, "private classes (fixed by the profile; a holder may
only narrow), tiers, budgets, judged rows; its digest in the holder's commitment", with the spec
"**Disclosure pack profile v0**: private classes, tiers, budgets, the policy digest in the manifest
and commitment, `policy_mismatch` as a finding" (company-internal-all
`architecture/Action_State_Evidence_Roadmap_2026-10-25.md`, row 4.4). Row 4.8 asks for the two L-cap
fields in the commitment. Reference v3 §3A.6 describes the pack, §3A.8 the two commitment fields,
§3A.9 the judged rows (`Action_State_Evidence_Architecture_Reference_v3_2026-10-24.md:240-298`).

Ruled (`architecture/ARCHITECTURE.md` §13.1, recorded 2026-10-25, and confirmed on this issue by
Sherlock2 on 2026-10-29):

- **Item 19.** evidencebook owns the profile; the private classes are fixed by it and a holder may
  only narrow them; budgets are placeholders. At stage 1 the Mesh plugin's share policy adopts the
  profile. Sherlock2 added one acceptance line: the profile marks every budget value as a
  placeholder.
- **Item 20.** Both commitment fields stay in the manifest only until the first end-to-end
  admission; the ARD #110 and ai-catalog asks are not widened now.

Later rulings this profile must hold (ops `company-oss/inbox.md`):

- **u590** (Steven 2026-11-06): scoped bundles ship four levels, anyone, counterparty, adjudicator
  and owner; "ask me" and "under contract" are request-time choices, not levels. The adjudicator
  gets the counterparty's records plus the referee material.
- **u593** (Steven 2026-11-16): a countersigner may see a `declared_count` record's payload, by the
  holder's opt-in per countersign and per record kind, never by default.
- **u640** (Steven 2026-11-26, evidencebook#998, ops commit `7cb1ace0`): the countersigner sees the
  asking party's own payment legs (side, amount sent or received with the receive fee, routing fee,
  payment reference such as a Lightning hash, status, observed time, the terms leg paid), never a
  preimage, never the counterparty's legs. The signed message:
  buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=f7c948e3b379990d3b64e66e8b75d61d2925a261dc35b6d5803e4e2b52d81929&thread=ef685ec74988588082dd26f1ce927be64f50b1bc831290d7e8e2964f15a1a7f4

## 1. What exists today

A disclosure policy is not new. Several producers already sign one, but no profile fixes what is
in it.

**The file.** Every producer signs a `disclosure-policy/0` file: `{profile, key_id, policy, sig}`,
Ed25519 over the JCS form without `sig`. The commitment names it by `lcap.policy_uri` and
`lcap.policy_digest`, the SHA-256 of the file's bytes (loop `scripts/check_commitment.py:95`,
`:599-601`; plugin `commitment.rs` module doc). The commitment itself is the AAC Evidence Commitment
Carriers binding's (`spec/bindings/evidence-commitment-carriers-v0.md`, EXPERIMENTAL), which says a
commitment "MUST NOT be placed inside a discovery object: the manifest" (§1).

**The `policy` member, five shapes.**

| Producer | `policy` members | Source |
|---|---|---|
| Mesh plugin | `advertise_evidence_requests`, `share_adjudications`, `share_exchange_corroboration`, `share_history_segments`, `share_record_at_completion`, `share_standard_history`, `stranger_safe` | `crates/evidence-plugin/src/commitment.rs`, `Policy::body` (`:215-230`) |
| capsule-cli `commitment publish` | `stranger_safe`, optional `share_deal_half` | `cli/commitment_publish.go:292-296` |
| evidencebook Goose example (also the ARD demo's, evidencebook-skills#299) | `stranger_safe`, `share_deal_file` | `python/examples/goose/commitment.py:53` |
| loop `join.sh` | the node's `SharePolicy` JSON as is | `join.sh:253-266` |
| evidencebook `DisclosurePolicy` | `name`, `holder`, `round`, Muse's eight settings, `never` | `disclosure_policy.go:86-114`; its doc comment calls it the file's `policy` member (`:18-22`), but nothing on main signs it into a file: `capsulectl book policy` seals it as `disclosure_policy` records (`capsulectl/internal/book/policy.go`) |

**What the responder enforces.** evidencebook's gate reads `SharePolicy`
(`interop_respond.go:135-144`): `record_at_completion`, `history_segments`, `adjudications`,
`witness` (shown only, `doctor.go:403`) and `standard_history`. Relationships are `stranger` (no
authenticated requester), `counterparty` (some record of the book names it) and `identified` (any
other authenticated requester) (`interop_respond.go:179-181`, `:680-690`). A stranger gets only the
public questions, `checkpoints` and `history_card/1` (`RespondOptions` doc). `DisclosurePolicy`
overlays two switches on it (`disclosure_policy.go:197-204`) and limits unsigned askers to one shared
weekly bucket (`:209-214`). The Mesh plugin enforces the same switches from its settings
(`share_policy.rs:28-54`).

**The never-list.** `NeverList` (`disclosure_policy.go:62-66`) is fixed text in every
`DisclosurePolicy`; `Validate` refuses any other list (`:158-160`) and a change naming one of its
items is `ErrNeverList` (`:346-349`). It is Muse's list for a person.

**Two digests named "the policy digest".** `lcap.policy_digest` covers the signed file's bytes.
`DisclosurePolicy.Digest` is the SHA-256 of the `policy` member's JCS (`disclosure_policy.go:164-172`),
and that is what a `standard_history/1` card names; the History Frame registers that column as
`policy_octets`, "SHA-256 of the policy's octets as held" (AAC `spec/history-frame-v0.md:110`,
`:330`). `book doctor` checks the card's digest against the policy in force (`doctor.go:615-622`).

**The refusal check.** The plugin checks a signed refusal against the policy its peer
advertised: `peer_commitments::policy_check` returns `MATCH`, `MISMATCH` or `NOT_CHECKED`
(`peer_commitments.rs:17-30`, `:117-190`). Its rules, all of which the profile keeps:

- no commitment held at ask time: `NOT_CHECKED`;
- `capacity_exceeded`: `NOT_CHECKED`, a limit of the responder's own;
- `policy_declined` and `not_authorized` under `evidence-interop/0`, or on a request over the size
  limit: `NOT_CHECKED`, because an honest responder also sends them for load and size limits
  (`:43-58`, `:122-140`);
- a reason that blames the request: `MATCH`;
- a subject form the commitment does not list: `MATCH`;
- what `stranger_safe` lists, withheld: `MISMATCH`;
- a policy refusal of record bodies the policy gives no one (`range`, `full_history`, or any record
  body while `share_history_segments` is `off`): `MATCH` (`:176-208`);
- anything that depends on who asks: `NOT_CHECKED`.

Loop step 07 reads the plugin's result (`steps/07-ask:1078-1080`) and runs its own
`policy_answers` (`:279-288`), which also answers authenticated askers under
`history_segments: peers`. It reads `history_segments`, `SharePolicy`'s key, while the plugin signs
`share_history_segments`.

So the problem the profile solves is not "make `SharePolicy` a pack" from nothing. It is five
unrelated `policy` members, two digests with one name, and a refusal check that can say `MISMATCH`
only where the answer does not depend on who asks.

## 2. The document: a `disclosure-pack/0` member in the `disclosure-policy/0` file

The profile fixes the `policy` member of the file every reader already fetches. It adds no second
document and does not change the envelope.

- The envelope stays `disclosure-policy/0`, with the same signature, `lcap.policy_uri` and digest
  rule. Both readers accept any object as its `policy` member (loop
  `scripts/check_commitment.py:604-607`; plugin `commitment.rs:745-756`) and refuse any other
  envelope token, so a new envelope token would make every commitment a new writer signs fail
  verification until both readers moved. Keeping `/0` means no reader breaks.
- A `policy` member that follows this profile says so with `"profile": "disclosure-pack/0"`. A
  member without it is one of today's five shapes, read as today (§7).

```json
{
  "profile": "disclosure-pack/0",
  "baseline": "disclosure-baseline@1",
  "private": ["amounts", "counterparty_identities", "exact_timestamps", "free_text",
              "other_customers", "payload_content", "reasoning"],
  "tiers": {
    "stranger":       {"answers": ["checkpoints", "checkpoint_range", "history_card/1", "history_card/2"],
                       "budget": {"asks": 3, "per": "P7D", "shared": true, "placeholder": true}},
    "identified":     {"answers": ["standard_history/5"],
                       "budget": {"asks": null, "per": "P7D", "placeholder": true}},
    "counterparty":   {"answers": ["record", "exchange", "correlation", "deal_half", "adjudication"],
                       "budget": {"asks": null, "per": "P7D", "placeholder": true}},
    "under_contract": {"answers": []}
  },
  "judged": [],
  "max_request_bytes": 65536
}
```

The values above are a person's defaults (§5); §4 gives each tier's ceiling and §5 the budget rules.

**Holder settings that are not disclosure.** Muse's `name`, `holder`, `round`, `window`, `staple`,
`more_than_standard`, `receipt` and `asked_log_retention_days` (`disclosure_policy.go:86-112`) go in
an optional `settings` object of the member. The profile does not interpret it and a checker
ignores it, but it stays inside the member, so the policy digest still covers the window a
`standard_history/1` card counts and the round a `policy-change/v1` is composed against.

**Two digests, kept apart on purpose.** Each has consumers that need it unchanged:

- The **file digest**, `lcap.policy_digest`: the SHA-256 of the signed file's bytes. It is what a
  commitment pins and what the plugin records in an `evidence_request` (`peer_commitments.rs:12-15`).
  A new signature, key or serialization changes it.
- The **policy digest**: the SHA-256 of the `policy` member's JCS (`disclosure_policy.go:164-172`).
  A `standard_history/1` card names it (History Frame `policy_octets`), `book doctor` compares the
  card with it (`doctor.go:615-622`), and a `policy-change/v1` names it as `base_policy` to detect a
  stale round (`disclosure_policy.go:339-344`). It moves only when the policy says something
  different.

A reader that holds the file gets the policy digest by recomputing the member's JCS. The vectors
include a re-signed file with an unchanged member (file digest moves, policy digest does not) and a
one-member change (both move).

## 3. Private classes, and who is entitled to each

**The fixed list.** The seven classes of Reference v3 §3A.6 are `disclosure-baseline@1`'s
private list: `payload_content`, `amounts`, `counterparty_identities`, `free_text`, `reasoning`,
`other_customers` and `exact_timestamps`. A pack member's `private` MUST contain all seven and MAY
add more; a policy missing one is refused, as `Validate` refuses a changed never-list today. A
holder narrows in two ways only: by adding a class to `private`, or by answering less within a tier
(§4). No member, request, catalog entry, contract or pack removes a class.

Muse's never-list becomes the person default's additions, so nothing it promises is lost:

| `NeverList` item | pack class |
|---|---|
| `items` | `payload_content` |
| `amounts_beyond_bands` | `amounts` (a band is not an amount; roadmap 4.9) |
| `counterparties_except_to_themselves` | `counterparty_identities` |
| `messages` | `free_text` |
| `rules` | `reasoning` |
| `name`, `contact`, `asked_of_you_log`, `shop_purchases`, `pre_book_history` | added to the person default's `private` under these names |
| `forum_exception_holders_own_copy_only` | not a class: the entitlement table's last row |

**Entitled parties.** Reference v3 says a private class is "never disclosed to anyone but the
party entitled". The profile fixes who that is. The table is part of the profile, not of any
holder's document, and no tier, setting or request adds a row:

| Class | Entitled party | Under |
|---|---|---|
| `amounts`, `exact_timestamps`, `payload_content`, `free_text` of one deal | the counterparty of that deal, for that deal only | Reference v3 §3A.6 (L-content); u590 counterparty level |
| `counterparty_identities`: the counterparty's own identity, never another counterparty's | that counterparty | `NeverList` `counterparties_except_to_themselves`; u590 (the counterparty copy as is) |
| `amounts`, `exact_timestamps`, `payload_content` and `free_text` of that deal's records, `counterparty_identities` of that deal's two parties only, plus the referee material | the adjudicator that deal names | u590 adjudicator level |
| `amounts` and `exact_timestamps` of the holder's own payment legs: side, amount sent or received with the receive fee, routing fee, payment reference, status, observed time, the terms leg paid; never a preimage, never the counterparty's legs | a countersigner the holder engages, after the countersign sign-up step has said so plainly before the holder agreed, and while the countersigner's directory row says the same | u640 (conditions: evidencebook#1001 acceptance 2) |
| `payload_content` of a `declared_count` record | a countersigner, only when the holder opts in for that countersign and that record kind; never by default and never as a standing setting; the countersigner refuses a payload whose digest does not match the sealed record | u593 |
| any class, for the holder's own copy | the holder | `NeverList` `forum_exception_holders_own_copy_only` |

The two countersigner rows are separate consents: agreeing to countersigning shows the payment
legs, and shows no `declared_count` payload unless that countersign also carries the per-kind
opt-in.

This is how u640 and "amounts are private" agree: the countersigner is not an asker of any tier.
The holder sends its own withheld bundle to a countersigner it chose (`capsulectl/internal/deal/deal_v1_countersign.go`,
evidencebook#998), and the profile names that act as an entitlement. A stranger, an identified
requester or a counterparty asking about another deal never reaches a private class through it.

## 4. Tiers and their ceilings

Four tiers, each a relationship the responder decides from its own records and the transport's
authentication (`interop_respond.go:179-181`, `:680-690`), never from what a requester declares.

| Tier | Ceiling: the most a pack member may list | Default (node and person) |
|---|---|---|
| `stranger` | `checkpoints`, `checkpoint_range`, `history_card/1`, `history_card/2` | all four (plugin `STRANGER_SAFE`, `commitment.rs:126-131`); the Goose example lists `history_card/2` only (`commitment.py:53`) |
| `identified` | the stranger ceiling; `standard_history/5`, the one commerce card (u632 Q2); Mesh's Service History derivations and `exchange_corroboration/1` (half digests, never record bodies) | node: stranger's plus `exchange_corroboration/1` (plugin defaults: `share_standard_history` off, `share_exchange_corroboration` on, `share_policy.rs:44-52`, `:150-175`); person: stranger's plus `standard_history/5` |
| `counterparty` | the identified ceiling; Mesh's Service History derivations and `exchange_corroboration/1` whatever the identified tier lists, as the plugin answers a counterparty today (`commitment.rs:83-93`), while every other derivation, `standard_history/1` included, follows the holder's list (with `standard_history: off` it is refused to everyone, `interop_respond.go:146-149`); `record`, `exchange`, `correlation` when every record the subject names names the requester (`mayHave`, `interop_respond.go:196-209`); `deal_half`; `adjudication` to its subjects | all of it |
| `under_contract` | `range`, `full_history`, only when a signed contract's `evidence_obligations` names them | empty: `range` and `full_history` are refused under every setting today (evidencebook#114), and `evidence_obligations` is not specified yet (roadmap 3.5) |

Retired and frozen forms are not in the ceilings. `standard_history/1` retires once no demo asks
for it (u632 Q2): a responder that serves it today keeps answering it under its `standard_history`
switch and its cards keep verifying, but a new policy does not list it. The personal-history forms
(`personal_history_basic/1`, `personal_history_outcome/1`) are frozen (u631), so no tier lists them,
and the declared-purpose input of §7 has no derivation to apply to until one needs it.
`history_card/1` stays in the stranger ceiling: u632 Q3 retires it as an asked form, and a responder
may still answer it.

**Tiers are who may ask; u590's levels are what a holder ships.** A tier is decided per request
from who asks and, for `under_contract`, the contract the request presents: a request-time choice,
as u590 says "under contract" is. u590's levels map here as follows: anyone to the stranger tier's
answers, counterparty to the counterparty tier and its entitlement row, adjudicator to its
entitlement row (§3), and owner to the holder's own-copy row.

`answers` lists each derivation by its name on the wire, as the commitment's `answers.derivations`
does: a registered token, or the digest of the derivation's definition document for those named
that way (`exchange_corroboration/1`, `service_history/1`; `commitment.rs:83-106`). A policy lists a
subset of each ceiling; a token outside its tier's ceiling is refused. Each tier
also answers everything a lower tier lists, so narrowing a lower tier never widens a higher one.
Reference v3's stranger example names only `checkpoints` and `history_card/1`; the ceiling adds the
bounded forms the shipped producers answer, `checkpoint_range` and `history_card/2` (ER -03).

**A list that shrinks as the log grows.** The plugin stops answering the unranged `checkpoints`
and `history_card/1` once their full answers no longer fit (`commitment.rs:140-153`, ER -03
§8.1.3). The policy lists what the responder answers when it signs, so the plugin re-signs the
policy without them and both digests move once, as they do today. The vectors cover a small book
and one past the size limit.

## 5. Budgets

- Each tier has `budget: {asks, per, placeholder}`: at most `asks` requests in any `per` (an ISO
  8601 duration), counted per authenticated requester, or in one shared bucket for strangers
  (`shared: true`), as `unsignedAllowLocked` counts them today (`disclosure_policy.go:206-214`).
  `asks: null` sets no limit.
- `max_request_bytes` caps one request.
- Every budget value carries `"placeholder": true`, and the profile's text says each default is a
  placeholder to be calibrated from the stage-1 health report (item 19; Sherlock2's acceptance
  line). A pack member without the marker on every budget is refused while the profile is v0.
- The stranger tier's own answers are never counted: a budget counts a requester's asks for
  anything its tier does not list, so it bounds probing, not discovery. That keeps today's rule,
  under which `unsigned_asks_per_week` exempts `checkpoints`, `checkpoint_range`, `history_card/1`
  and `history_card/2` (`interop_respond.go:558`). For the tiers above, a budget counts every ask.
- The responder counts asks as it admits them, on its own clock, and an ask it refuses at the limit
  is not counted, as `WindowLimiter.allow` does (`interop_respond.go:100-117`).
- Past a budget, the responder sends a signed `policy_declined`, under `evidence-interop/0` and
  `/1` alike. `capacity_exceeded` stays the responder's own load, which no policy promises past
  (plugin `peer_commitments.rs:130-135`).
- The defaults carry today's limits over (Sherlock2, §10 answer 4): the person's
  `unsigned_asks_per_week: 3` becomes `stranger: {asks: 3, per: "P7D", shared: true}`, with the
  exemption above; the node's `0` (no limit) becomes `asks: null`; `identified` and `counterparty`
  are `asks: null`, since nothing limits them today. Reference v3's "strangers two a day,
  identified ten" stay examples until the stage-1 health report.

## 6. Judged rows

`judged` lists rows by `{id, pack, version, digest}`, such as `purpose-fits-ask`. A judged row
reads typed fields only, never free text from the requester, and its only effect is to refuse one
request or to cap a tier for it; it never widens a tier and never produces NO alone (Reference v3
§3A.9). v0 defines the member and these limits; running a row needs a pinned judge in a published
pack, which waits on pack format v1 and its verbs (roadmap 3.1, 3.2; evidencebook-skills#56).
Until a responder can run a row, it fails closed: its `Validate` refuses a member whose `judged` is
not empty, so no holder signs or adopts a policy whose restrictions nothing enforces. A reader
still reads such a member and checks against it, with every answer a row could change
`NOT_CHECKED`.

## 7. `policy_mismatch`

**The check.** It keeps every rule of the plugin's check (§1) and adds the tiers above `stranger`.
A tier is never taken from a label: the asker raises `MISMATCH` for a tier only from evidence it
holds itself, and otherwise the answer stays `NOT_CHECKED`. Its inputs:

| Input | Where the asker gets it | Without it |
|---|---|---|
| the policy in force when it asked | what its sealed `evidence_request` retains: the commitment digest, the file digest and the `policy` body, verified at fetch time (plugin `peer_commitments.rs:8-15`, `:97-112`). The body is enough to decide; the signed file bytes need not be kept, and a later fetch that replaces the cached frame changes nothing | `NOT_CHECKED` |
| that the request was authenticated | the asker's own signature on the request it sealed (ER -03 signed requests) | the stranger tier only |
| that it is the counterparty of every record the subject names, and that those records exist | a record signed by the responder that names the asker as its counterparty, for each record the subject names: the responder's half the asker holds (`counterparty_half`, roadmap 3.7). The responder's gate reads its own records (`mayHave`, `interop_respond.go:196-209`), so a record the asker sealed alone proves neither | `identified` for that subject, and `NOT_CHECKED` for `no_such_subject` |
| a declared purpose, where the tier's entry requires one (no entry does in v0, §4) | the purpose member of its own request | `MATCH` for a refusal of that derivation |
| that a contract names the subject | the signed contract's `evidence_obligations` (not specified yet) | `NOT_CHECKED` for `under_contract` |
| that no judged row applies | the policy's `judged` is empty, or the rows cannot cap this request | `NOT_CHECKED` |

With those inputs:

An asker cannot know the responder's count: an ask may be lost or arrive in another window, and
one refused at the limit is not counted (§5). So a budget never decides the check. A refusal that a
budget could explain is `NOT_CHECKED`: a `policy_declined` of a request that a finite budget of
its tier counts. A tier with `asks: null`, and the stranger tier's own answers, which no budget
counts (§5), keep their `MISMATCH`.

- `MISMATCH` when the policy lists the subject or derivation for a tier the asker has shown it
  holds, every prerequisite above is met, and the refusal withholds it: `derivation_unsupported`;
  `no_such_subject` for a subject the responder's own signed record proves; or, under
  `evidence-interop/1`, `not_authorized`, and `policy_declined` of a request no finite budget counts.
- `MATCH` when the policy does not list it for that tier and no lower tier lists it, when a
  required purpose is missing, or by the plugin's `MATCH` rules.
- `NOT_CHECKED` in every other case, including each plugin `NOT_CHECKED` rule.

The vectors carry the plugin's existing cases unchanged and add: a counterparty of one deal asking
about another deal; a subject naming records of two counterparties; a record only the asker sealed;
a subject the responder does not hold; a missing and a present purpose; a `policy_declined` under a
finite budget (lost, delayed and refused-at-limit asks), under `asks: null`, and of an exempt
stranger answer; a counterparty asking `standard_history/1` with `standard_history: off`; a policy changed between two asks (the retained
body decides); a nonempty `judged`; and a member in one of today's shapes, which keeps the plugin's
rules and reads `NOT_CHECKED` above the stranger tier.

**The finding.** A `MISMATCH` is the finding `policy_mismatch`, naming the policy file digest, the
request digest and the refusal's digest. The asker's admission pack maps it to NO or a lower tier,
as Reference v3 §3A.6 says; this profile only defines when it is raised.

**Who owns what.** The decision, a pure function of the inputs above, lives in evidencebook (Go,
the Rust crate and Python) with shared vectors; the plugin's `policy_check` becomes a call into the
crate with the same results on its existing tests (the plugin builds on the evidencebook crate, ops
u462). Gathering the inputs stays with each asker's host: the plugin reads its own ledger, loop
step 07 its fixtures, capsulectl its book.

## 8. The commitment and the manifest

No new field. The commitment already carries `lcap.policy_digest`, `lcap.policy_uri` and
`lcap.predicate_classes` (loop `scripts/check_commitment.py:95`; plugin `commitment.rs`; capsule-cli
`commitment_publish.go:323-324`). The evidence-request manifest carries no digest
(`interop_manifest.go:32-37`, IP §11.4.3) and a commitment must not sit in it (ECC §1), so "the
manifest" in item 20 is read as the commitment, which is the holder's own published document and no
discovery object. Nothing goes to ARD #110 or ai-catalog (item 20). Sherlock2 confirmed this
reading (§10 answer 3). Roadmap 4.8's other two members, `human_gated` and `answers_within` (D-35,
still Proposed), are not added by this profile; "no new field" does not close 4.8.

## 9. Migration from `SharePolicy` and today's members

| Today | pack member |
|---|---|
| `history_segments: counterparties` (and `prospective`) | `counterparty` lists `record`, `exchange`, `correlation` |
| `history_segments: off` | `counterparty` lists none of them |
| `history_segments: peers` | narrowed to `counterparties`: record bodies are above the `identified` ceiling (§10 answer 2), and `book doctor` says plainly that the setting was narrowed. Loop step 07 checks `peers` today (`steps/07-ask:279-288`) and moves in build item 5 |
| `record_at_completion: counterparty` / `off`; Muse `receipt` | the counterparty push is the holder's act, not an answer; it stays a holder setting, and `counterparty` lists `deal_half` when on |
| `adjudications: deliver_to_subjects` / `off` | `counterparty` lists `adjudication` or not |
| evidencebook `standard_history: on` / `off` | `identified` lists the commerce card (`standard_history/5`) or not; `standard_history/1` is answered under the switch until it retires (§4) |
| plugin `share_standard_history` | `identified` lists the Service History derivations (by definition digest) or not; a different switch from evidencebook's, of the same name |
| plugin `share_exchange_corroboration` | `identified` lists `exchange_corroboration/1` or not; `counterparty` gets it either way, and it never releases `record` or `exchange` bodies |
| plugin `advertise_evidence_requests` | not disclosure: stays a plugin setting |
| `witness` | not disclosure: stays a setting |
| `stranger_safe` (plugin, capsule-cli, Goose) | `stranger.answers` |
| `share_deal_half` (capsule-cli), `share_deal_file` (Goose) | `counterparty` lists `deal_half` |
| Muse `under_contract: named_by_contract` | `under_contract` lists its ceiling once `evidence_obligations` exists |
| Muse `unsigned_asks_per_week` | the stranger budget (§5) |
| `never` | `private` (§3) |

`SharePolicy` stays as an input for one release: `DisclosurePolicy` is built from it by this table,
so a caller that passes one gets the same answers.

## 10. What-questions, answered

Asked of the Sherlock2 twin first, then of Sherlock2 (evidencebook#1005, PM review at `f5b57223`,
https://github.com/action-state-group/evidencebook/pull/1005#issuecomment-7202292872; Buzz
buzz://message?channel=ef9c2983-4bef-4df0-96a9-a34748edfe2b&id=9a81d719709cbacca6cb1be15febb97d3d154db97d19168c5b0da1e9ec826bd8&thread=095a449da655820998644c35b891ccb31ff5e7bbbbdd4428d72637ba45f84f0e).
Sherlock2's answers decide; none leaves a decision for Steven.

1. **The entitlement table (§3).** An entitlement, not a widening: both countersigner rows add
   nothing beyond u640 and u593 as ruled.
2. **`history_segments: peers` (§9).** Drop it: a node set to `peers` answers counterparties only,
   and `book doctor` says the setting was narrowed. Item 19 covers it as a narrowing; a fifth tier
   would be a widening, for Steven, and is not recommended. No shipped default uses `peers`
   (`interop_respond.go:153-155`; plugin `share_policy.rs:121`).
3. **Item 20's "manifest" (§8).** The commitment; the ER manifest stays digest-free.
4. **Budget defaults (§5).** Today's limits, carried over.
5. **u590 and `under_contract` (§4).** No conflict: u590's levels are what a holder ships, tiers are
   who may ask, decided per request.

## 11. Build PRs, in order

Each item starts only after Sherlock2's PM review of this note and the answers its question needs.

Readers move before writers: a `disclosure-pack/0` member breaks no signature check (§2), but a
reader that does not know it loses the stranger `MISMATCH` it has today.

| # | Repository | What | Needs |
|---|---|---|---|
| 1 | evidencebook | The profile text (`docs/disclosure-pack-profile-v0.md`), the member's JSON Schema, and shared vectors under `testdata/disclosure-pack/`: valid and refused members, both digests, each of today's five shapes read under §9, and the check cases of §7 | questions 1 to 5 |
| 2 | evidencebook | Go: `DisclosurePolicy` reads and writes the member; the gate reads tiers and budgets from it; `SharePolicy` mapped by §9; `PolicyCheck` and the `policy_mismatch` finding | 1 |
| 3 | evidencebook | The Rust crate and Python on the same vectors (`rust.yml`, the python job) | 1, 2 |
| 4 | Mesh-LLM/capsules-plugin | reader: `policy_check` calls the crate and reads both member shapes | 3 (upstream review) |
| 5 | evidence-network/loop | `site/watch.py` and step 07 read both shapes and the finding for every tier | 3 |
| 6 | capsule-viewer | the History view's `sharing` reads both shapes | 1 |
| 7 | Mesh-LLM/capsules-plugin; capsule-cli; evidencebook Goose example; loop `join.sh` | writers: the plugin's share policy (item 19, stage 1), `commitment publish`, `python/examples/goose/commitment.py` and `join.sh` sign the member; vectors check an old reader against a new member | 4, 5, 6 |

The judged rows' runtime is not on this list: it waits on pack format v1 (evidencebook-skills#56).
