# Setup (once)

Read this before the first deal on a profile, as the
[deal skill](../SKILL.md) says. Its commands sit outside the skill's five
verbs and are kept here as they were taught before; each step that is the
user's, not yours, says so.

`capsulectl deal` runs in the `capsulectl-deal` plugin. If a `capsulectl deal`
command says the plugin is not installed, stop and show the user what it
prints: it says to download the plugin file built for their capsulectl from
the plugin's release and install it with `capsulectl plugin install <file>`.
Installing it is the user's step: never run `plugin install` yourself.

```sh
capsulectl --profile deal deal init --dir ~/.local/share/capsule-deal
```

This creates a local SQLite store and two signing seeds (mode 0600), and
turns on the public witness by default (`--no-witness` turns it off; [ASSURANCE.md](../ASSURANCE.md) compares it with a countersignature). It
prints `public_key`: the other side names it in an offer's `to`, so give it
to them when they make the offer. Raw names, numbers, addresses and message
text stay in this local store: each sealed step is an x-deal-v1 record (see
EvidenceBook's
[`dealprofile/x-deal-v1/PROFILE.md`](https://github.com/action-state-group/evidencebook/tree/main/dealprofile/x-deal-v1/PROFILE.md)).
Your journal stays on this device, and only what the deal needs (the offer,
your agreement, your acts and statements) goes to the other side, as files
the user sends.

A profile made before x-deal-v1 was the default refuses x-deal-v1 commands
until its store is moved, once: `capsulectl --profile deal deal set-version
x-deal-v1 --migrate` (a backup is kept beside the store). That is the user's
step: show them the message and never run it yourself.

**Admission.** Before agreeing, each side seals capsule-engine's admission
decision on the other ([SKILL.md](../SKILL.md), Purchase, step 4). capsule-engine is its own program:
if `capsule-engine` is not installed, stop at that step and tell the user.

Which of your own picks (a size, a delivery option the user never named)
pause a check is set by a materiality predicate the profile pins:
`deal init --materiality builtin:neutral` (the predicate built into
capsulectl: a quantity of 1 does not pause, a size, variant, delivery or other
quantity does) or a FILE of the user's own, or later `profile update
--materiality` (see EvidenceBook's
[`dealprofile/x-deal-v0/materiality-predicate/`](https://github.com/action-state-group/evidencebook/tree/v0.10.0/dealprofile/x-deal-v0/materiality-predicate)). With none configured, every attribute you
picked pauses the check and the user is asked about it. **Setting or changing
the predicate is the user's policy: never do it yourself.** If a check refuses
because the predicate changed since it was pinned, tell the user; re-pinning is
theirs to run.

Then set up the **checkpoint cadence**: schedule
`capsulectl --profile deal cll checkpoint cadence --wait-up-to 5m` **every 5
minutes** on this machine (a cron line `*/5 * * * *`, or the host's scheduler
at a five-minute interval). Each run is a poll: it waits for a tick due within
its 5 minutes and publishes it on time, on the profile's own clock (every 5m,
give or take 1m, by default), and a run with no tick due publishes nothing. A
run stays alive up to 5 minutes; if the host caps how long a scheduled job may
run, schedule more often with a matching smaller `--wait-up-to`. Never drop
`--wait-up-to`: without it a scheduled run publishes on the scheduler's grid,
which hides the cadence's jitter. Deal steps never publish anything
themselves. If your agent host asks before a program reaches a website, the
first tick raises that question for the witness's site: tell the user to
choose **"Always allow this site"**, not "allow once", because later ticks
run on the schedule, when nobody is there to answer, and an allow-once grant
leaves every later tick pending. Say plainly that the grant covers the
witness's whole site: for the default witness, witness.agentactioncapsule.org,
that is agentactioncapsule.org and all its subdomains.
A tick that could not reach the witness says so: "pending: network consent
needed, or no network". A tick that reached the witness but got no answer in
time says "pending: the witness was reached but its answer did not come in
time" (reason `witness_timeout`): the witness or the network to it is slow,
and it is tried again on a later run; never ask the user for a network grant
for it. A
tick refused with HTTP 403 says "pending: the
request was refused (HTTP 403), by the witness or by a network gate on this
host": on an agent host that asks before network access, that too is most
likely a missing standing grant, and the tick names the site. It is tried
again on a later capsulectl run, after a wait that grows up to an hour.

Tell the user, in one message, exactly what this does: their deal steps are
sealed on this device; about every 5 minutes, whether or not anything happened, one
checkpoint of hashes goes to the witness; the witness never sees content, how
many deals there are, or when they happen. A remote checker (minimal deal
fields, never message text) stays off unless they turn it on. Ask once, up
front, before turning the remote checker on, or if they would rather have no
witness.
