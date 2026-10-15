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
This page does not cover that part. The responder here serves service evidence
only.

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
| checkpoint 1 `expected_pin` | `2faed55bcd9bb04cfa753df224879148c6f53608769aefde68ddc8a91ae9f05d` (`iss` `evidence-stream-1`, `sub` `evidence-stream-1#8`) |
| checkpoint 2 `expected_pin` | `cd9648a8c81b15841f41d97fbade236bc6f10ceabed2259745594638ebfbfc64` (`iss` `evidence-stream-1`, `sub` `evidence-stream-1#19`) |
| record 1 | `4fdcd0414e3a1a1b5a63ad135a463a6d46a7623d4973e2dd53e6c0377c2a6beb` |
| record 2 | `17ea4a57e9ec7b5dfebb96bf8e9bc1f64e6d1acdee51395e43fa54f285708938` |
| record 3 | `6cf41cfa1eaccf3965950228cfafba099495b80c4bae35d83b6843ec65a9f5f0` |
| record 4 | `21039e6c0bd9151a96aa143b0534f7b9180ad8de2804f7bea11d73d46b6b5a1f` |
| record 5 | `15171dfcbe39459228d82f52b61823e6fbd927e61424fb2c936de44eeeef022d` |
| record 6 | `67f36d5ef16cc916969b6c1caa30708b87f4731bcfb5ab1479f50095333fc317` |
| record 7 | `d1246d27c620e1d970e12a953218a8b9b5468a536bcff51f450d7dfa2b472c46` |

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
PIN=cd9648a8c81b15841f41d97fbade236bc6f10ceabed2259745594638ebfbfc64
REC=6cf41cfa1eaccf3965950228cfafba099495b80c4bae35d83b6843ec65a9f5f0
```

**Positive.** Record 3 under checkpoint 2, as `alice`:

```bash
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-1","profile":"evidence-interop/0","subject":{"record":"'$REC'"}}' > run/positive.req.json
go run ./test/interop/answer -media-type application/json -requester alice -in run/positive.req.json -out run/positive.reply.json
```

**Invariance.** The same question, as `bob` with another nonce, in
deterministic CBOR. Your encoder's output must be these 211 bytes, with SHA-256
`5d19f563c9ed541f41a3be2e682c41526fd06f44eb3af530e1f06e1c4de4dabb`:

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

## 6. What to report back

Open an issue on the
[evidencebook repository](https://github.com/action-state-group/evidencebook/issues)
with:

1. the agent-action-capsule commit you read (it should be the one in §1), and
   the evidencebook commit you ran (`git rev-parse HEAD`);
2. your verifier: language, the libraries it uses for Ed25519, SHA-256, JCS and
   CBOR, and a statement that it uses no code from evidencebook or from
   agent-action-capsule;
3. the key pins you chose, and your `evaluation_time`;
4. for each row of the table in §5: the request and reply octets (JSON as text,
   CBOR as hex) or the vector case id, your verifier's per-property statuses,
   and your `row`, `status`, `sufficiency` and `verdict` for `r-record`, with
   the claim's `evidence` and `proofs`;
5. every place the profile text left you unsure, or where you think the text,
   the vectors and this responder disagree.

**Pass** means that for every row of the table in §5, your results equal the
named vector case's `expected` block as §5 describes. Any difference is a
finding. Report it even if you think the mistake is ours.

## Other implementations

evidencebook's continuous integration checks this responder with a Python
reference verifier from the agent-action-capsule project. That verifier and
evidencebook's Go code are the implementations this test is meant to be
independent of. Do not use either for the test. Comparing results with them
afterwards is fine.
