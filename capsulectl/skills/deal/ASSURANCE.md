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
| What the third party sees | One checkpoint per tick of the cadence: hashes, a size that grows by the same amount every tick, and a time on the cadence. Never content, how many deals there are, or when they happen | The bundle it is asked to sign: digests, plus anything you chose to disclose in it |
| What it covers | Every deal under that checkpoint | That one bundle, through one step; steps after it need a new countersignature |
| Who starts it | `capsulectl deal init` sets it up; opt out with `--no-witness` | You, per deal and per bundle; nothing requests one automatically |
| What the receipt says | "Sealed, witness pending" until the next tick of the cadence, then "Witnessed" (`assurance.rung` is `self_attested` until a receipt verifies, then `witnessed`, or `witnessed_in_part` while steps sealed after the last witnessed tick are uncovered; `unverified` when a receipt held does not verify) | `countersigned` only when the signature verifies and the directory you chose lists the signer; a signer the directory lacks, or the producer's own key, is not a rung |
| Choose it when | You want any deal checked by a third party without doing anything, and are content that it learns only checkpoint hashes | A counterparty or auditor needs a named party to have signed this exact bundle, and you accept that party seeing it |

## Switch a deal to countersign

**Only an x-deal-v0 deal can be countersigned today.** A new deal is x-deal-v1
by default (`deal init`), and for it nothing in capsulectl gets a countersignature
or shows one on its receipt: `deal countersign` answers "not served for
x-deal-v1 deals yet", `deal report` has no `--from-bundle`, `--directory` or
`--html` for it, and `countersign request` refuses the v1 report bundle because
it carries a disclosures member. A v1 deal stays witnessed (default) and
self-attested otherwise; a countersign path for v1 is not built
([evidencebook#862](https://github.com/action-state-group/evidencebook/issues/862)
tracks it). The steps below are for a v0 deal.

Witnessing stays on unless you turn it off. To add a countersignature you ask a
countersigner of your choice, which updates your bundle file, and attach it.
Nothing here is automatic.

```sh
# 1. Write the deal's report bundle.
capsulectl --profile deal deal report --deal DEAL_ID --bundle bundle.json

# 2. Ask the countersigner you chose for a signature over that bundle. It
#    attaches the returned entry to bundle.json, in place. --window is the
#    attestation window or period label the service expects.
capsulectl --profile deal countersign request --service COUNTERSIGNER_URL \
  --window WINDOW --bundle bundle.json

# 3. Verify what is now attached and keep it with this deal. A signature that
#    does not verify is refused.
capsulectl --profile deal deal countersign --deal DEAL_ID --bundle bundle.json \
  --directory DIRECTORY

# 4. Render the receipt from that same file.
capsulectl --profile deal deal report --deal DEAL_ID --from-bundle bundle.json \
  --directory DIRECTORY --html receipt.html
```

`DIRECTORY` lists the countersigners you trust; none is built in. Set it once
with `capsulectl profile update --profile deal --countersign-directory DIRECTORY`.
[COUNTERSIGN.md](COUNTERSIGN.md) says what each rung and finding means.

## Other implementations

The Python library has no default witness and no command that requests a
countersignature: it only checks a witness or countersignature it is given. A
default witness there is each host's own config.
