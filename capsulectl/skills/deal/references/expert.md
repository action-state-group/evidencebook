# Expert commands

The [deal skill](../SKILL.md) teaches five verbs: check, record, ask, verify
and show. The commands here sit outside the five; their instructions are kept as
they were taught before, and SKILL.md points here where a step needs one.
The setup commands are in [setup.md](setup.md), the book's own expert commands
(`book close`, `book result derived-inputs`, the window commands) in
[book.md](book.md), and the x-deal-v0 sale (`deal sale ...`) in
[LEGACY-X-DEAL-V0.md](../LEGACY-X-DEAL-V0.md).

## Reading at the last checkpoint

This one is yours to run when Purchase step 11
([references/purchase.md](purchase.md#purchase)) sends you here.

`deal settlement --deal ID` and `deal report --deal ID` read your book only
up to its last checkpoint, and refuse a book with records after it. Run
`deal tick`: if it prints `state: not_due`, wait until the `due` time it
prints and run it again (the scheduled cadence does this by itself every 5
minutes). Then run the report again.

`deal settlement --deal ID` gives each payment row's check on both sides'
legs you hold. On a deal whose other side keeps no signed records, it says
the record is one-sided, as `deal report` does.

## Command reference

All commands take `--profile deal` and print one JSON object. `--help` on
each gives every flag.

| Command | What it does |
|---|---|
| `deal init --dir DIR [--no-witness] [--materiality FILE\|builtin:neutral] [--window day\|week\|month\|quarter\|year\|none]` | Creates the profile: its store, signing and checkpoint seeds (mode 0600) and its cadence log. Prints `public_key`, the key the other side names in an offer's `to`, and the store's `window` (its period, or `none`) with a `notice`. `--window` (default `none`) splits the store's journal into calendar windows in UTC; each deal's book stays whole, placed in the window its agreement is sealed in. The window, or its absence, is fixed when the store is created. |
| `deal window list` / `deal window roll` / `deal window chain --from LABEL --to LABEL --out FILE` | On a store created with `--window`: lists the windows and the deals placed in each; closes ended windows; writes the chain a requester's report over windows is checked against, with each placed deal's proof. |
| `deal window respond --request FILE --requester ID --output FILE [--pack FILE]...` | On a store created with `--window`: answers a Terms Pack request from the window or placed deal book its pin names, and records the answer in the current window. Each `--pack` is a pack file (capsulectl-book's format) this answer may serve; without `--requester`, the requester you authenticated, a Terms Pack is not answered. |
| `deal tick` | The checkpoint cadence's poll. Prints `state` (`ticked` or `not_due`) and `due`. Schedule `cll checkpoint cadence --wait-up-to 5m` every 5 minutes ([setup.md](setup.md)). |
| `deal settlement --deal ID` | Each payment row's check over both sides' legs you hold, as your records read it now. |
| `deal audit-host --executions FILE` | The daily check of host actions (below). |
| `deal set-version x-deal-v1 --migrate` | Moves a store made before x-deal-v1 was the default to the new layout, once, keeping a backup. The user's step: never run it yourself. |

## Daily check of host actions

Once a day (a scheduled task on the host is enough), run:

```sh
capsulectl --profile deal deal audit-host --executions executions.jsonl
```

`executions.jsonl` is the host's own record of the tool calls that ran in
the period, those of the agent and of every sub-task it started, written in
the format in [AUDIT-HOST.md](../AUDIT-HOST.md). Read the execution records, not the
conversation: the payment is usually a sub-task's action, not one you can see
in the chat. The pass lists each consequential action with no deal record
(`unrecorded`), and prints on its face what it cannot see. It exits 3 when
anything is unrecorded. Show the user the `summary` and the `unrecorded` list
as they are. It can list what is missing from the records it read; it cannot
prove that nothing else happened.
