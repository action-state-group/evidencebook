# The countersign rung on a deal receipt

Every deal receipt names how far it can be trusted, in rungs. The receipt,
the report JSON and the page each carry a `countersign` field. With no
countersignature it says:

> Not countersigned: no other party has signed this record. It stands on the
> agent's own seal and any witness signature this file carries.

That is the honest value for every receipt today.

## What a countersignature is here

A countersignature is a signature by another party over the digest of one
exact Evidence Bundle file, in the countersign/v1 shape that `capsulectl
countersign request` and `capsulectl countersign verify` already handle. The
bundle digest leaves out only the `countersignatures` member itself, so the
signature covers everything else in that file.

No deal command asks for a countersignature or sends a bundle anywhere: the
deal commands only check one you already hold. You get one by asking the
countersigner you chose with `capsulectl countersign request` over the
withheld bundle `deal report --bundle FILE --withheld` writes;
[ASSURANCE.md](ASSURANCE.md) gives the whole sequence and says how it differs
from the witness that is on by default. Countersign is served for x-deal-v1
deals only: an x-deal-v0 deal's report bundle carries its records' contents,
which `countersign request` refuses. With a signature you hold:

```sh
# 1. Write the withheld form of your deal book's bundle (an x-deal-v1 deal).
capsulectl --profile deal deal report --deal DEAL_ID --bundle bundle.json --withheld

# 2. Attach a countersignature you hold. It is verified before it is written;
#    one that does not verify is refused, and the file is left unchanged.
capsulectl --profile deal deal countersign --deal DEAL_ID --bundle bundle.json \
  --entry countersignature.json --directory DIRECTORY

# 3. The report states it.
capsulectl --profile deal deal report --deal DEAL_ID
```

`--directory` is a countersigner directory (an HTTPS URL or a file, as for
`countersign verify`) that you choose. It resolves which party a signing key
belongs to. No list is built in. Without `--directory`, `deal countersign`
uses the profile's own, `countersign.directory`, set once with `capsulectl
profile update --profile deal --countersign-directory DIRECTORY`; the report
uses only that one.

`deal countersign` refuses a bundle that is not this deal's own. The bundle
must verify, its log must be the deal's own deal book, its checkpoint must be
one that book holds (the same signed statement), and its root must be a record
of the book under that checkpoint. Without `--entry`, `deal countersign` only
verifies the countersignatures the file already carries. On an x-deal-v0 deal
it checks the report bundle instead (its checkpoint signed by this profile's
checkpoint key, its root one of the deal's steps, its deal extension naming
this deal), and `deal report --from-bundle FILE --directory DIRECTORY` renders
the receipt from it.

The withheld bundle carries each record of your deal book as its signed
Capsule, the checkpoint and its proofs, and the profile's signing key as its
producer key, so a countersignature by that key reads `self_countersigned`.
Each record Capsule carries exactly these members, and `deal report
--withheld` refuses to write one that carries any other:

| Member | What it shows |
|---|---|
| `operator` | your profile's operator name (the profile name) |
| `developer` | `capsulectl-deal` |
| `action_id` | the deal book's log id and the record's place in it (`deal/<log>#<n>`) |
| `action_type` | `fyi` |
| `timestamp` | when the record was sealed |
| `key_id`, `signature` | the signing key and its signature over the Capsule |
| `capsule_id` | the record's digest |
| `model_attestation` | only `compute_attestation.agent_input_digest`, the digest of the record's header |
| `references` | the digest of the record before it in the book (absent on the first) |
| `settlement` | on a payment record only, the payment as you recorded it: whether you paid or received it, the amount (or the amount received and the receive fee), any routing fee, the payment reference (for Lightning, the payment hash; never the preimage), its status, when you observed it, and the digest of the terms it pays |
| `assurance`, `canonicalization_id`, `format_version`, `spec_version` | fixed values |

It carries none of the records' contents except each payment's `settlement`
member, which is part of the signed Capsule, no report and none of the other
side's records, so a countersignature over it covers your deal book up to and
including the record it is rooted at, your payments with it, and not the report: the report's
`countersign.covers` says so.

## Kept with the bundle it covers

Every countersignature `deal countersign` verifies is kept in the deal store
with the exact bundle it covers, so a later report still has it whichever
file you keep. A countersignature covers that one bundle and never a later
one: a later report never attaches it to its own bundle. It states it in
`grade.countersign` instead (on an x-deal-v1 deal, in the report's
`countersign`), verified again against the stored bundle, with
the steps it covers (`through_step` of `steps`), the signer resolved against
the profile's `countersign.directory` only (never a `--directory` given to
the report, so who asks never changes it), its `recomputed_at` (the signer's
own clock) and the stored bundle's digest. When the deal has steps after
every resolved countersignature's bundle, the rung is `countersigned_in_part`,
as `witnessed_in_part` is for a witness. An entry that could not be checked
is `unchecked` and fills no rung, with `unchecked_reason`: `no_directory`
(the profile names none, or names a file by a relative path),
`directory_unreadable`, or `bundle_does_not_check`. Covering the steps after
it needs a new countersignature over a new bundle. With a rung,
`grade.countersign` also names the entry that decides it: the resolved one
reaching furthest into the deal (the latest `recomputed_at` among equals),
as `signer`, `recomputed_at`, `through_step` and `steps`.

A countersignature `deal countersign` showed as resolved under a
`--directory` you passed can read `unresolved_signer` or `unchecked` in the
next report: the grade resolves signers against the profile's directory only.
Set the profile's directory to the one you trust.

## What each value says

The evidence grade ladder is self-attested → witnessed → countersigned.
`countersigned` is its top rung. `self_countersigned` and
`unresolved_signer` are annotations, not rungs: they add nothing, and the
receipt's grade stays where witnessing put it (`assurance.rung`:
`self_attested`, `witnessed` or `witnessed_in_part`), so a self-countersigned
receipt with no witness signature is self-attested. `not_countersigned` and
`unverified` name no rung.

The report's `countersign` object carries a rung only when one is reached:
`rung` is `countersigned`, or `countersigned_in_part` (a stored countersignature
whose bundle ends before the deal's last step, below), or absent. Everything
else is a `finding`:
`self_countersigned`, `unresolved_signer`, `not_countersigned`,
`unverified`, or `unchecked` (the file carries a countersignature that was
not checked when the page was written). The page shows the `text` and
`note` capsulectl wrote; it names no rung itself.

| `rung` or `finding` | When | What the receipt says |
|---|---|---|
| `not_countersigned` | no countersignature | Not countersigned (above) |
| `countersigned` | the signature verifies and the directory lists the key | Countersigned by NAME, a signer listed in the directory you chose, over this exact bundle, with its own check results. It vouches only as far as you trust that directory. |
| `self_countersigned` | the key is the producer's own (a key in the profile's `trusted_keys`, or the one the bundle's `producer-key/v1` extension declares) | **NOT INDEPENDENT**: countersigned by the producer's own key. A self-countersignature is not a check by anyone else. A directory listing that key does not change this. |
| `unresolved_signer` | the signature verifies, but the directory does not list the key | Countersigned by a key the directory does not list: its signer is unknown. |
| `unverified` | a countersignature type capsulectl does not check | Attached, not counted. |

The user's own report also carries `grade`: `rung`, the highest rung the
deal reached (`countersigned` or `countersigned_in_part` when a stored
countersignature resolves, else the witness's); `witness`, where the copy's witnessing stands (`state`, and once
witnessed the `witness`, `at`, the time of the checkpoint the witness signed,
and in part `steps_witnessed` of `steps`); and `countersign`, above. The grade
reflects only what the copy carries: since u225 a copy carries no cadence
chain, so a deal witnessed only through the cadence reads `not_carried`, and
the report's `assurance.holder_witnessed` says what your own profile reports. A shared
copy carries no grade.

The page shows what capsulectl found when it wrote the page: a page cannot
resolve a signer against a directory itself. Anyone can check the same file
with `capsulectl countersign verify bundle.json --directory DIRECTORY`.

## The optional second reader

`CAPSULE_DEAL_CHECK_URL` is a different, separate hook. It names an optional
second reader of a `deal check`:

- It is off unless set.
- It receives only the minimal fields (no message text, names, contact
  details or card numbers).
- It can only add differences: its "pass" never clears a local pause.
- It gets 2 seconds. When it is slow or unreachable, the local rules decide
  alone.
