# The countersign rung on a deal receipt

Every deal receipt names how far it can be trusted, in rungs. The receipt,
the report JSON and the page each carry a `countersign` field. With no
countersignature it says:

> Not countersigned: no other party has signed this record. It stands on the
> agent's own seal and any witness receipt this file carries.

That is the honest value for every receipt today.

## What a countersignature is here

A countersignature is a signature by another party over the digest of one
exact Evidence Bundle file, in the countersign/v1 shape that `capsulectl
countersign request` and `capsulectl countersign verify` already handle. The
bundle digest leaves out only the `countersignatures` member itself, so the
signature covers everything else in that file.

capsulectl sends nothing to get one. How a countersigner came to sign a
bundle is outside capsulectl. The deal commands only check a countersignature
you already hold:

```sh
# 1. Write the deal's report bundle.
capsulectl --profile deal deal report --deal DEAL_ID --bundle bundle.json

# 2. Attach a countersignature you hold. It is verified before it is written;
#    one that does not verify is refused, and the file is left unchanged.
capsulectl --profile deal deal countersign --deal DEAL_ID --bundle bundle.json \
  --entry countersignature.json --directory DIRECTORY

# 3. Render the receipt from that same file, so the signature covers what it shows.
capsulectl --profile deal deal report --deal DEAL_ID --from-bundle bundle.json \
  --directory DIRECTORY --html receipt.html --email receipt.eml
```

`--directory` is a countersigner directory (an HTTPS URL or a file, as for
`countersign verify`) that you choose. It resolves which party a signing key
belongs to. No list is built in. Without `--directory`, both commands use
the profile's own, `countersign.directory`, set once with `capsulectl profile
update --profile deal --countersign-directory DIRECTORY`.

`deal countersign` refuses a bundle that is not this deal's own. The bundle
must verify, its log must be the deal's, its checkpoint must be signed by
this profile's checkpoint key, its root must be one of the deal's steps, and
its deal extension must name this deal. Without `--entry`,
`deal countersign` only verifies the countersignatures the file already
carries.

## Kept with the bundle it covers

Every countersignature `deal countersign` verifies is kept in the deal store
with the exact bundle it covers, so a later report still has it whichever
file you keep. A countersignature covers that one bundle and never a later
one: a later report never attaches it to its own bundle. It states it in
`grade.countersign` instead, verified again against the stored bundle, with
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
receipt with no witness receipt is self-attested. `not_countersigned` and
`unverified` name no rung.

The report's `countersign` object carries a rung only when one is reached:
`rung` is `countersigned`, or absent. Everything else is a `finding`:
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
countersignature resolves, else the witness's); `witness`, where its witnessing stands (`state`, and once
witnessed the `witness`, `at`, the time of the checkpoint the witness signed,
and in part `steps_witnessed` of `steps`); and `countersign`, above. A shared
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
