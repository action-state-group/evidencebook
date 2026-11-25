# Witness and countersign: which assurance a deal has

A deal can stand on two kinds of outside check. **Witnessed** is on by default.
**Countersigned** is a manual step you take per deal.

Action State runs the default witness (`https://witness.agentactioncapsule.org`).
Any countersigner Action State hosts is run by Action State too. Both are
run by Action State and are not independent of it: a deal checked by a
service we run shows that service's check, and a relying party decides
whether that counts for it.

| | Witness (default) | Countersign (manual) |
|---|---|---|
| What it proves | The deal's checkpoint, and so the records it covers, existed by the time the witness signed it; a later rewrite of the chain is detectable | A named party signed the digest of one exact Evidence Bundle file |
| What the third party sees | One checkpoint per tick of the cadence: hashes, a size that grows by the same amount every tick, and a time on the cadence (on-demand stamps aside). Never content, how many deals there are, or when they happen. One exception: a stamp made on demand (`deal agree`, `deal ruling`, `deal tick --now`) comes off the cadence, so the witness sees the minute it was made, and for agree or ruling, that some deal reached that step then (not which deal) | The withheld bundle it is asked to sign: each record of your deal book as its signed Capsule, including your profile's operator name, the book's log id and the record's place in it, its time, the signing key and digests ([COUNTERSIGN.md](COUNTERSIGN.md) lists every member), the checkpoint and its proofs. None of the records' contents, no report, none of the other side's records. A countersigner that guesses a record's exact contents can check the guess against its digest |
| What it covers | Every deal under that checkpoint | Your deal book up to and including the record the bundle is rooted at, as digests; records after it need a new countersignature. Not the report, and not the other side's records |
| Who starts it | `capsulectl deal init` sets it up; opt out with `--no-witness` | You, per deal and per bundle; nothing requests one automatically |
| What the receipt says | "Sealed, witness pending" until the next tick of the cadence, then "Witnessed" (`assurance.rung` is `self_attested` until a receipt verifies, then `witnessed`, or `witnessed_in_part` while steps sealed after the last witnessed tick are uncovered; `witness_state` is `unverified` and the rung stays `self_attested` when a receipt held does not verify) | `deal report` prints `countersign.rung` `countersigned` only when a stored signature verifies and the profile's countersigner directory lists the signer, and `countersigned_in_part` once your deal book has records after the signed one; a signer the directory lacks, or the producer's own key, is not a rung |
| Choose it when | You want any deal checked by a third party without doing anything, and are content that it learns only checkpoint hashes | A counterparty or auditor needs a named party to have signed this exact bundle, and you accept that party seeing it |

## Switch a deal to countersign

**Countersign is served for x-deal-v1 deals only**, the default for a new deal
(`deal init`). An x-deal-v0 deal cannot get a countersignature: its report
bundle carries its records' contents, and `countersign request` refuses a bundle
that does (`deal countersign` on it only checks one you already hold).

Witnessing stays on unless you turn it off. To add a countersignature you ask a
countersigner of your choice, which updates your bundle file, and keep it with
the deal. Nothing here is automatic.

```sh
# 1. Write the withheld form of your deal book's bundle at its last
#    checkpoint: the file a countersigner is shown, with no record's contents.
capsulectl --profile deal deal report --deal DEAL_ID --bundle bundle.json --withheld

# 2. Ask the countersigner you chose for a signature over that bundle. It
#    attaches the returned entry to bundle.json, in place. --window is the
#    attestation window or period label the service expects.
capsulectl --profile deal countersign request --service COUNTERSIGNER_URL \
  --window WINDOW --bundle bundle.json

# 3. Verify what is now attached and keep it with this deal. A signature that
#    does not verify is refused.
capsulectl --profile deal deal countersign --deal DEAL_ID --bundle bundle.json \
  --directory DIRECTORY

# 4. The report's countersign states the rung and what it covers.
capsulectl --profile deal deal report --deal DEAL_ID
```

`DIRECTORY` lists the countersigners you trust; none is built in. Set it once
with `capsulectl profile update --profile deal --countersign-directory DIRECTORY`:
the report resolves signers against that directory only. The report's
`countersign` says how far the signature reaches (`through_step` of `steps`
records of your deal book) and, in `covers`, that it covers neither the report
nor the other side's records. The receipt page `deal report --html` writes for
an x-deal-v1 deal is a fresh copy, so it carries no countersignature, and a
History card does not yet say countersigned. [COUNTERSIGN.md](COUNTERSIGN.md) says what
each rung and finding means.

## Other implementations

The Python library has no default witness and no command that requests a
countersignature: it only checks a witness or countersignature it is given. A
default witness there is each host's own config.
