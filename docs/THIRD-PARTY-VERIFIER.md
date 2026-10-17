# Testing evidencebook with your own verifier

This page is for a developer who wants to check evidencebook's answers with a
verifier they wrote themselves. You write the requests, you choose which keys
to trust, and you verify the replies. You need nothing from us beyond this
page and the public documents it links to.

This is the third-party verifier test in §8 of this repository's
[evidence interoperability requirements](PRD-EVIDENCE-INTEROP.md), for the
Minimum Evidence Interoperability Profile, `evidence-interop/0`. You implement
the requester and verifier sides from the profile text, using none of
evidencebook's code and none of the Agent Action Capsule (AAC) project's client
or verifier code. Then you show that your verifier gives the results the
profile's conformance vectors specify for these classes: positive, invariance,
tampered, declined, missing evidence, backdated acceptance and unsupported
version.

§8 also asks for one personal-evidence disclosure to be processed alongside a
service-evidence response, each keeping its own units and source assertions.
Sections 1 to 5 cover the service-evidence response. Section 6 covers the
personal-evidence disclosure: a holder's History at the Basic level of the
Personal-Agent History Profile, with its own conformance vectors, which this
repository's `Respond` produced. Section 6.4 says how to keep the two apart.

The commands below are for a Unix shell (bash or zsh) on Linux or macOS, with
`git` and `curl` installed.

## 1. The normative inputs

All three come from the public
[agent-action-capsule](https://github.com/action-state-group/agent-action-capsule)
repository, pinned at commit `c86a73a3a726dd82dce28cf0e2105c5af008bf42`.
Read them at that commit, not at the tip of `main`, which may have moved on.

| Input | Path at the pinned commit |
|---|---|
| The profile | `spec/interop-profile-v0.md` |
| The request draft the profile builds on | `spec/draft-mih-agent-evidence-request-00.md` |
| The profile's conformance vectors | `vectors/interop-profile-v0/` (`vectors.json`, with `README.md` explaining how to read it) |

The vectors at this commit are byte-for-byte the same as when they were first
published, in commit `5beee5454e54f966e6026e3161d17a577edfd7c2`. The later
commit corrects only the profile text, and none of the corrections touch the
exchanges this page uses.

Download them into a working directory:

```bash
mkdir -p ~/eb-verifier-test && cd ~/eb-verifier-test
AAC=c86a73a3a726dd82dce28cf0e2105c5af008bf42
for f in spec/interop-profile-v0.md spec/draft-mih-agent-evidence-request-00.md \
  vectors/interop-profile-v0/README.md vectors/interop-profile-v0/manifest.json \
  vectors/interop-profile-v0/vectors.json vectors/interop-profile-v0/SHA256SUMS; do
  curl -fsSL --create-dirs -o "aac/$f" \
    "https://raw.githubusercontent.com/action-state-group/agent-action-capsule/$AAC/$f"
done
(cd aac/vectors/interop-profile-v0 && sha256sum -c SHA256SUMS)
```

On macOS, use `shasum -a 256 -c SHA256SUMS` in the last line. All three files
should report `OK`.

## 2. Install Go and get evidencebook

evidencebook needs Go 1.27 or newer (see `go.mod`). If `go version` already
reports 1.27 or newer, skip the first three lines. Otherwise install Go into
your home directory. Use `linux-amd64` as shown, or swap in your platform:
`linux-arm64`, `darwin-arm64` or `darwin-amd64`. The full list is at
<https://go.dev/dl/>.

```bash
mkdir -p "$HOME/sdk"
curl -fsSL https://go.dev/dl/go1.27.0.linux-amd64.tar.gz | tar -C "$HOME/sdk" -xz
export PATH="$HOME/sdk/go/bin:$PATH"
go version
cd ~/eb-verifier-test
git clone https://github.com/action-state-group/evidencebook.git
cd evidencebook
```

Every remaining command runs from this `evidencebook` directory. The first
`go run` downloads evidencebook's Go dependencies and compiles. That takes a
minute or two. Later runs start within a few seconds.

## 3. The test responder

`test/interop/answer` is a test command, not part of evidencebook's API. It
reads one request from a file or from stdin, with its media type, and writes the
signed reply that evidencebook's `Respond` returns for it.

```bash
go run ./test/interop/answer -info
```

`-info` prints what you need to pin and to ask for:

- `responder_key`: the Ed25519 key that signs every envelope and refusal. This
  is the profile's key identifier form (§5.2).
- `checkpoints`: two signed checkpoints. For each, `expected_pin` is the
  digest you put in a request's `coverage`, and `log_key` is the
  `(iss, sub, kid)` of the checkpoint's signer.
- `records`: the digests of the seven records the book holds, in log order.
  The first three are under both checkpoints. The last four are only under
  the second.
- `manifest`: the profile's manifest (§11.4.2).

Each run creates a new book in a temporary directory and seeds it the same way,
using test keys whose seeds are the SHA-256 of public labels. So these values
are the same on every run and on every machine:

| Value | |
|---|---|
| `responder_key` | `e84fed8190a6116016bf68ef2fc1780a919e9ed2d9f88efb895a954ffd7f0117` |
| log key `kid` | `c82acf66ef2bdc106017544b91873dfbdc92f0963b099d49b0f621bedf76e2af` |
| checkpoint 1 `expected_pin` | `07e8e7a0936fdc0d5eda424946ca36576a0bdff8b7602b419daf308d7719afbd` (`iss` `evidence-stream-1`, `sub` `evidence-stream-1#8`) |
| checkpoint 2 `expected_pin` | `a8f66f1f436bf4e2549faf86040a272f98dc7f578cc78fc0459347db3934f25d` (`iss` `evidence-stream-1`, `sub` `evidence-stream-1#19`) |
| record 1 | `78c2628bf823fc958b96c66e47cbaec799194b4da38b5f3ad30eda1404b4a9b0` |
| record 2 | `5053828d77686751cdefd690ff1c8112a3e0f11c038060e69029d300adc412b4` |
| record 3 | `b1c7ffe655132799ca75ec97ecdc47375f77e394eb0ebe07e1d0fab39dad855d` |
| record 4 | `b74c29ca59de0592d4cda8fc8d437412017f3faffc8fac3530c14f70f2188773` |
| record 5 | `4312eabdde48711b4f5645a0f2007934c74ea7a7587695c7e5a796119595e0f2` |
| record 6 | `d70427536a4ac642d4afd56afda4554a7ad402288a57c8a9aee9e5bf809182f7` |
| record 7 | `5347d0817c005f092d346fb23e2aa9ed7fd385331cdf9e817c19b72ad918e91b` |

The responder key and the log key are the vectors' `responder` and `log` test
keys (`test_keys` in `vectors.json`). You can derive both yourself from their
`seed_label`. **They are test keys whose private halves anyone can compute.
Trusting them is your own choice, for this test only. Never use them for
anything else.**

Nothing in the reply depends on how you reach the command. Only two members
change from run to run: `issued_at` is the time of the run, and
`request_digest` is the SHA-256 of your request octets. The canonical Artifact
for a given subject, anchor and derivation is the same whoever asks, whatever
the nonce, encoding or run (profile §6.2).

Options:

| Option | Meaning |
|---|---|
| `-media-type` | `application/json` or `application/cbor`, the encoding of your request. The reply comes back in the same encoding |
| `-in`, `-out` | request file and reply file; `-` (the default) is stdin or stdout |
| `-requester` | stands in for your transport's authentication. It is the identity a transport would have authenticated you as. Without it, only the `checkpoints` subject and the `history_card/1` derivation are answered, and every other subject is refused with `not_authorized`. Use any name |
| `-decline` | answer from a responder whose cost policy declines every request. The reply is a signed `policy_declined` refusal |

The command uses neither a network nor a server. evidencebook also carries this
exchange over the profile's other bindings (A2A and MCP, §11.4). The
profile requires the same Artifact on every binding, so this test needs only
the octets.

## 4. Requests

You build the request octets yourself. Under the profile you MUST encode them
deterministically: JCS (RFC 8785) for JSON, or RFC 8949 §4.2.1 deterministic
CBOR (§5.1). Keep the exact octets you sent, because `request_binding` is
checked against them. Each command below writes one request with `printf`, so
that you can compare your own encoder's output with it, and then gets the reply.

```bash
mkdir -p run
PIN=a8f66f1f436bf4e2549faf86040a272f98dc7f578cc78fc0459347db3934f25d
REC=b1c7ffe655132799ca75ec97ecdc47375f77e394eb0ebe07e1d0fab39dad855d
```

**Positive.** Record 3 under checkpoint 2, as `alice`:

```bash
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-1","profile":"evidence-interop/0","subject":{"record":"'$REC'"}}' > run/positive.req.json
go run ./test/interop/answer -media-type application/json -requester alice -in run/positive.req.json -out run/positive.reply.json
```

**Invariance.** The same question, as `bob` with another nonce, in
deterministic CBOR. Your encoder's output must be these 211 bytes, with SHA-256
`bb1f40c649a44c58bd0ab9da36f90c3a83938ca0ec5933d73f5488c57e8e487f`:

```bash
printf '%b' '\xa4\x65nonce\x65bob-1\x67profile\x72evidence-interop/0\x67subject\xa1\x66record\x78\x40'$REC'\x68coverage\xa1\x6cexpected_pin\x78\x40'$PIN > run/invariance-bob.req.cbor
go run ./test/interop/answer -media-type application/cbor -requester bob -in run/invariance-bob.req.cbor -out run/invariance-bob.reply.cbor
```

Check this pair together with the positive pair (`run/positive.*`).

**Declined.** The positive request, sent to a responder that declines it:

```bash
go run ./test/interop/answer -media-type application/json -requester alice -decline -in run/positive.req.json -out run/declined.reply.json
```

**Missing evidence: no such subject.** A record digest this book does not
hold (the SHA-256 of the empty string):

```bash
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-2","profile":"evidence-interop/0","subject":{"record":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}' > run/missing.req.json
go run ./test/interop/answer -media-type application/json -requester alice -in run/missing.req.json -out run/missing.reply.json
```

**Unsupported version.** A profile identifier this responder does not
implement:

```bash
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-3","profile":"evidence-interop/9","subject":{"record":"'$REC'"}}' > run/unsupported.req.json
go run ./test/interop/answer -media-type application/json -requester alice -in run/unsupported.req.json -out run/unsupported.reply.json
```

**Missing evidence: recorded absence.** An absence is the requester's own
record that no reply came within its window (§6.4), so the responder plays no
part. Write a request, do not send it, and once your window has closed write
your absence record for it. Below is an unsigned example. A signed one adds
`key_id` and `sig` under your own key:

```bash
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-4","profile":"evidence-interop/0","subject":{"record":"'$REC'"}}' > run/absence.req.json
D=$( (sha256sum run/absence.req.json 2>/dev/null || shasum -a 256 run/absence.req.json) | cut -d' ' -f1)
printf '%s' '{"outcome":"recorded_absence","recorded_at":"2026-10-12T00:01:10Z","request_digest":"'$D'","window":{"closed":"2026-10-12T00:01:10Z","opened":"2026-10-12T00:00:10Z"}}' > run/absence.record.json
ls run
```

`ls run` should list 11 files.

## 5. What to verify, and what result is specified

Verify each exchange with your own verifier and the trust policy you chose:

- `responder_keys`: the `responder_key` above;
- `log_keys`: the two `log_key` entries from `-info`;
- offline, with `evaluation_time` the time you verify.

For the classes the responder cannot produce (tampered replies, backdated
acceptance, and the verifier-side unsupported-version cases), run your verifier
over the named vector case's own `inputs`, under that case's own `pins`. Those
cases are signed with the same test keys.

The result specified for each exchange is the `expected` block of the vector
case named in the table: each reply's `message_type`, every per-property status
in `properties` (§7), and for requirement `r-record` the §8.1 `row`, `status`,
`sufficiency` and `verdict`. Where a reply comes from this responder, the
digests differ from the vectors', because this book holds different records.
So compare everything except the digests that name those bytes:
`request_digest` (`request_digests` and `artifact_digest` in the invariance
case) and the digests in `claim`. Check those against your own request octets
and the replies you received instead (§8.3).

| Class | What you run | Vector case whose `expected` is the specified result | In short |
|---|---|---|---|
| Positive | `run/positive.req.json` and its reply | `valid-exchange` | envelope; every required property `PASS`; row `envelope-7`, whose status, sufficiency and verdict stay `null`, because the profile fixes no content rule; `claim.evidence` is the `artifact_digest`, and `claim.proofs` is the `cll-inclusion-proof` entry |
| Invariance | the positive pair and the `bob` CBOR pair, as one set of two exchanges | `invariance-two-nonces-two-transports` | two envelopes, each as for the positive class; the Artifact octets are identical; `caller_invariance` `PASS` |
| Tampered | the vector inputs of `tampered-artifact`, `wrong-request-digest`, `envelope-bad-signature`, `envelope-untrusted-responder`, `envelope-subject-mismatch`, `envelope-anchor-not-the-pin`, `verification-material-tampered`, `inclusion-proof-forged-and-signed`, `forged-refusal-untrusted-key` and `forged-refusal-bad-signature` | each case itself | the property each case breaks is `FAIL`; row `envelope-2` (`INSUFFICIENT`) for the eight envelope cases and `refusal-failed` (`UNKNOWN`) for the two forged refusals. A conforming responder never sends these. To check your verifier on this responder's bytes as well, flip one bit of the decoded `artifact` in `run/positive.reply.json`: `artifact_digest` is then `FAIL`, row `envelope-2` |
| Declined | `run/positive.req.json` and `run/declined.reply.json` | `declined` | refusal `policy_declined`; every refusal property `PASS`; row `refusal-withheld`: `WITHHELD`, `GAP`, `not_evaluable`, with presentation `analysis` or `story` |
| Missing evidence | `run/missing.req.json` and its reply | `missing-evidence-no-such-subject` | refusal `no_such_subject`; row `refusal-no_such_subject`: `NOT_FOUND`, `GAP`, `not_evaluable` |
| Missing evidence | `run/absence.req.json` with `run/absence.record.json` and no reply | `missing-evidence-recorded-absence` | absence form `conforming`; row `absence`: `UNKNOWN`, `UNKNOWN`, `not_evaluable`; presentation `story` |
| Backdated acceptance | the vector inputs of `acceptance-absent` and `acceptance-backdated-A-no-ordering-evidence` to `acceptance-backdated-G-policy-requires-other-party`, including `acceptance-after-action-D` | each case itself | each candidate's §11.1.7 properties and the §11.1.8 finding. These are acceptance records and receipts (§11.1), which no responder sends, so they come only from the vectors |
| Unsupported version | `run/unsupported.req.json` and its reply | `unsupported-version-refused` | refusal `request_malformed`; row `refusal-deadline_or_malformed`: `UNKNOWN`, `UNKNOWN`, `not_evaluable` |
| Unsupported version | the vector inputs of `unsupported-version-mismatch`, `unsupported-version-absent` and `unsupported-version-unimplemented` | each case itself | `profile_version` `FAIL`, `NOT_PRESENT` or `NOT_CHECKED` respectively, under the version rules of §3.3 step 5. A conforming `evidence-interop/0` responder never sends these envelopes |

The vectors also hold cases outside these seven classes: foreign objects (§5.1),
lifecycle (§11.2) and carriage (§11.4). They are welcome but not required.

The Artifact this responder returns is an Agent Action Capsule record, in
JSON. You do not need to interpret it. Every property above treats it as
octets.

## 6. Personal evidence: a holder's History

The personal-evidence part uses the Personal-Agent History Profile v0
(`personal_history_basic/1`, its Basic level), an informational host profile
written against the interop profile of §1. In it, the subject is a holder's
signing key, and the unit is a deal the holder selected for one recipient. A
History is a recipient-specific disclosure: one request and one envelope per
selected deal, all under a definition document that names the recipient and the
selection. The request's `derivation` is the SHA-256 of that document.

### 6.1 The inputs

They come from the same public repository, at commit
`0b7b1825965e7f7845cbc70a490a581cbc70bd37`. That commit is later than the one
in §1. Between the two, the interop profile text changes in one row of its
§11.5 table of vector cases, and the request draft does not change, so the
copies you downloaded in §1 still serve.

| Input | Path at this commit |
|---|---|
| The host profile | `spec/personal-agent-history-profile-v0.md` |
| Its conformance vectors | `vectors/personal-history-basic-v0/` (`vectors.json`, 12 cases, with `README.md` explaining how to read it) |
| The responder run the vectors are built from | `vectors/personal-history-basic-v0/responder-run.json` |
| The program that produced that run | `vectors/personal-history-basic-v0/producer/main.go` |

Download them next to the §1 inputs, in a directory of their own:

```bash
cd ~/eb-verifier-test
AAC_PH=0b7b1825965e7f7845cbc70a490a581cbc70bd37
for f in spec/personal-agent-history-profile-v0.md spec/evidence-result-v0.md \
  vectors/personal-history-basic-v0/README.md vectors/personal-history-basic-v0/manifest.json \
  vectors/personal-history-basic-v0/vectors.json vectors/personal-history-basic-v0/responder-run.json \
  vectors/personal-history-basic-v0/producer/main.go vectors/personal-history-basic-v0/SHA256SUMS; do
  curl -fsSL --create-dirs -o "aac-ph/$f" \
    "https://raw.githubusercontent.com/action-state-group/agent-action-capsule/$AAC_PH/$f"
done
(cd aac-ph/vectors/personal-history-basic-v0 && sha256sum -c SHA256SUMS)
```

On macOS, use `shasum -a 256 -c SHA256SUMS`. All five files should report
`OK`. `vectors.json` has SHA-256
`8369998d0fb0f118a1f4d6c5469cd93d83885fa6ae2c3b39e1267955001cbccc`.
`spec/evidence-result-v0.md` is the Evidence Result document the profile takes
its claim, sufficiency and verdict from.

### 6.2 Reproduce the responder run

The vectors' positive run is not hand-made. It is the octets that
evidencebook's `Respond`, unmodified, sent at evidencebook commit
`43f3cb1b955d647bc92cb4da0d823973d1981cad`. The program that drove it is in
this repository as `test/interop/personal-history`, byte-for-byte the
`producer/main.go` you downloaded. It seeds a holder stream of four deals, with
a checkpoint after the sixth record and another after the eighth, and registers
the Basic level definition for one recipient. Then it asks for each deal as the
requester each exchange names. Its clock is fixed and its keys are test keys,
so every run on every machine gives the same bytes.

In the `evidencebook` clone of §2:

```bash
cd ~/eb-verifier-test/evidencebook
PH=../aac-ph/vectors/personal-history-basic-v0
cmp "$PH/producer/main.go" test/interop/personal-history/main.go
go run ./test/interop/personal-history > run/responder-run.json
cmp "$PH/responder-run.json" run/responder-run.json
(sha256sum run/responder-run.json 2>/dev/null || shasum -a 256 run/responder-run.json) | cut -d' ' -f1
```

Both `cmp` commands print nothing, and the last line prints
`f7d77d56d81ca187cb9a8118f8a15aae5407f5a96aca1c5525a5f5d2e827b848`. That is
`run_sha256` in the corpus `manifest.json`. evidencebook's continuous
integration reproduces the run on every change, so a later commit than
`43f3cb1b…` gives the same bytes. If yours does not, report it as a finding.

The run holds every request and reply as the octets carried, the definitions,
the holder stream (four deals, three selected, eight records) and its two
checkpoints. Your verifier can read the exchanges from your own copy. For the
cases whose `source` is `responder`, the vector inputs are those same octets.

### 6.3 What to verify, and what result is specified

Each case pins its own policy in `pins`. Use it as given:

- `trust_policy`: the holder key
  `27cb07b534232cb4f4b7b269a8436fafb17c19be41fbab20ac948d1c81b0a034` as the
  only accepted responder key (`mutant-second-trusted-key` adds the second
  holder key `020126b36be66edbb505b7c18f5164055d99e457f0cdf42b0430ea0c93c00a1b`),
  and the holder log key
  `74897b69738198f3322a53d7e4ac2f70ffd568e7303f8549655ccffe14d28320` for
  `iss` `holder-stream-1`, `sub` `holder-stream-1#15` and
  `holder-stream-1#22`;
- `verification_policy`: offline, `evidence-interop/0`, the three CLL kinds;
- the contract `H@1`, with SHA-256
  `5d4b7da72c8de9ef089f02b4f39dde25fe06d7659c9b4800e5fe79c6e8b12d34`, given
  in `contract.document`. Its one requirement, `r-deal-steps`, has a Basic
  level answer as its only evidence;
- `evaluation_time` `2026-10-15T01:00:00Z`.

These are test keys, derived from the `seed_label` values in `test_keys` like
the keys of §3. **Trust them for this test only.**

A case's `inputs` is either a History in the profile's one-file form (§8.2:
`history`, `definition`, `exchanges`) or, for the refusal, one `request` and
its `reply`. The requests are the recipient's own (`recipient-1`, `mode`
`asked`), but your verifier did not send them. For every History case:

1. verify each exchange as the interop profile §7.3 describes, and apply the
   host profile's §6.1 to decide whether each envelope is accepted;
2. evaluate the seven History properties of the host profile's §6.2 over the
   accepted envelopes only;
3. for an accepted History, derive each selected deal's state (host profile §5).

The specified result is the case's whole `expected` block: per exchange, its
`properties`, `accepted`, row for `r-deal-steps` and `claim`; then
`history_properties`, `accepted`, and `deal_states` for the accepted History.
`fails` names the property the host profile's §6.4 table names for that
mutant. It is one of those the table below lists. Unlike §5, compare the
digests too: these cases are fixed octets, not replies to requests of your own,
so every value must match.

In every case, an accepted envelope has `profile_version`, `envelope_signature`, `responder_trust`, `issued_at_form`, `request_binding`, `subject_binding`, `artifact_digest` and `anchor_match` `PASS`; `responder_key_current` and `anchor_inclusion` `NOT_CHECKED`; and `verification_binding` `NOT_PRESENT`.
Its row for `r-deal-steps` is `envelope-4`: `UNKNOWN`, `UNKNOWN`, `not_evaluable`, and its `claim.proofs` is empty.
An envelope that is not accepted has the one property the table names as `FAIL`, and its row is `envelope-2`: `INSUFFICIENT`, `INSUFFICIENT`, `not_evaluable`.
`anchor_inclusion` stays `NOT_CHECKED` because the interop profile registers
no verification material for a derivation's answer. So no case yields `met` or
`not_met`. The Basic level makes no outcome claim for a requirement to meet.

| Case | Source | What it holds | Envelopes accepted | Envelope not accepted | History property `FAIL` | History |
|---|---|---|---|---|---|---|
| `history-basic` | responder | the History: three of the four deals, at the second checkpoint | 3 of 3 | none | none | accepted |
| `mutant-step-kind-in-transit` | derived | one step's kind changed in an artifact after signing, not re-signed | 2 of 3 | exchange 1: `artifact_digest` `FAIL` | `selection_coverage` | not accepted |
| `mutant-deal-added-to-definition` | derived | the run's replies, under a definition with one more deal | 3 of 3 | none | `definition_binding`, `selection_coverage` | not accepted |
| `mutant-envelope-for-deal-not-selected` | responder | the History and an envelope, signed by the holder key, for the deal not selected | 4 of 4 | none | `selection_coverage` | not accepted |
| `mutant-selected-deal-not-answered` | derived | the replies for two of the three selected deals | 2 of 2 | none | `selection_coverage` | not accepted |
| `mutant-second-trusted-key` | derived | one envelope re-signed by the second holder key, which the case's pins also trust | 3 of 3 | none | `single_signer` | not accepted |
| `mutant-deal-at-another-anchor` | responder | one deal asked for and answered at the first checkpoint | 3 of 3 | none | `single_anchor` | not accepted |
| `mutant-deal-at-another-anchor-request-at-history-anchor` | derived | that answer, re-signed over a request that names the History's checkpoint | 2 of 3 | exchange 3: `anchor_match` `FAIL` | `selection_coverage` | not accepted |
| `mutant-record-kind-outside-list` | derived | one record's `record_type` changed to a kind outside the host profile's §3, re-signed by the holder key | 3 of 3 | none | `record_kinds` | not accepted |
| `mutant-member-outside-reads` | derived | `counterparty_ref` added to one record, re-signed by the holder key | 3 of 3 | none | `artifact_form` | not accepted |
| `mutant-definition-reads-widened` | responder | answers under a definition whose `reads` adds `statement` and `counterparty_ref` | 3 of 3 | none | `definition_form` | not accepted |
| `refused-other-requester` | responder | a request under the definition from `requester-2`, who is not its recipient | no envelope | refusal `not_authorized`; every refusal property `PASS` except `responder_key_current` `NOT_CHECKED`; row `refusal-withheld`: `WITHHELD`, `GAP`, `not_evaluable` | no History | no History |

For `history-basic`, `deal_states` counts 3 selected deals: 1 with a recorded failed step, 2 with recorded completed steps only, 0 with no recorded step.

The two other-anchor cases are two readings of one row of the host profile's
§6.4 table. That row names both `single_anchor` and `anchor_match`, and the
two cannot fail together: the corpus README explains why. Each case's `expected` is the result for its
own reading.

### 6.4 Keeping the two kinds of evidence apart

Report the service-evidence response of §5 and this History as two separate
results. Each is evaluated under its own fixture's pins, against its own
contract and requirement. Do not combine their counts, keys or requirement
rows, and do not show either as support for the other.

| | Service-evidence response (§3 to §5) | Personal-evidence disclosure (§6) |
|---|---|---|
| Subject | one record, named by its digest (`{"record": …}`), in the book of the responder key `e84fed81…` | the holder key `27cb07b5…`, the `responder` of every envelope in the History. Each request names one deal (`{"correlation": …}`) |
| Unit | records: each exchange answers for one record | selected deals, 3 in `history-basic`. A deal's records are counted separately, as records. No figure adds deals to records |
| Coverage | the record, under the checkpoint you pinned | the holder's selection, with `completeness` `not_asserted`: not a claim that it is all of the holder's deals |
| Source assertion | the responder key's envelope over the record, and an inclusion proof to the pinned checkpoint: `anchor_inclusion` `PASS`, `claim.proofs` the `cll-inclusion-proof` entry | the holder key's envelope over the deal's records. Each record's `epistemic_type` (`producer_claim`) is the holder's agent's own label, never corroboration by anyone else. `event_time_claim` (the agent's claim) and `committed_at` (the holder stream's time) are two separate times. `anchor_inclusion` `NOT_CHECKED`, `claim.proofs` empty |
| Requirement row | `r-record` of the interop vectors' contract, as §5 specifies | `r-deal-steps` of `H@1`: row `envelope-4`, `UNKNOWN`, `UNKNOWN`, `not_evaluable` |
| Not established | what the record's content means: the profile fixes no content rule, so the Artifact is octets to you (§5) | any outcome of a deal (sale, payment, delivery, receipt or return); that anything happened at a claimed time; who holds the holder key |

## 7. What to report back

Open an issue on the
[evidencebook repository](https://github.com/action-state-group/evidencebook/issues)
with:

1. the agent-action-capsule commits you read (they should be the ones in §1
   and §6.1), and the evidencebook commit you ran (`git rev-parse HEAD`);
2. your verifier: language, the libraries it uses for Ed25519, SHA-256, JCS and
   CBOR, and a statement that it uses no code from evidencebook or from
   agent-action-capsule;
3. the key pins you chose, and your `evaluation_time`;
4. for each row of the table in §5: the request and reply octets (JSON as text,
   CBOR as hex) or the vector case id, your verifier's per-property statuses,
   and your `row`, `status`, `sufficiency` and `verdict` for `r-record`, with
   the claim's `evidence` and `proofs`;
5. the SHA-256 of the `run/responder-run.json` you produced in §6.2;
6. for each of the 12 cases of §6.3, by case id: per exchange, your
   per-property statuses, whether you accepted the envelope, your `row`,
   `status`, `sufficiency` and `verdict` for `r-deal-steps`, and the claim;
   then your seven History properties, whether you accepted the History, and,
   for `history-basic`, the number of selected deals and each deal's state;
7. the two results side by side as §6.4 lays them out: for each, its subject,
   its unit and count, its coverage, its source assertion, and what it does not
   establish;
8. every place the profile texts left you unsure, or where you think the text,
   the vectors and this responder disagree.

**Pass** means both of these hold. For every row of the table in §5, your
results equal the named vector case's `expected` block as §5 describes. For
every case of §6.3, your results equal that case's whole `expected` block, and
your report keeps the two results apart as §6.4 describes. Any difference is a
finding. Report it even if you think the mistake is ours.

## Other implementations

evidencebook's continuous integration checks this responder with a Python
reference verifier from the agent-action-capsule project. It also reproduces the
§6.2 run and runs the agent-action-capsule test for the History vectors, which
uses that reference verifier and a History evaluator of its own. Those, and
evidencebook's Go code, are the implementations this test is meant to be
independent of. Do not use any of them for the test. Comparing results with
them afterwards is fine.
