#!/usr/bin/env bash
set -euo pipefail

# Plays the DEMO desk lamp purchase end to end, both sides, on x-deal-v1:
# the buyer offers -> the seller receives -> each side seals its admission
# decision -> the buyer seals its intent -> the seller agrees -> the buyer
# receives that and agrees -> the seller receives the buyer's agreement ->
# the buyer's check before paying pauses -> the user holds -> the report.
# Prints the card and fails if it is not the expected one.
# Uses CAPSULECTL if set (a downloaded release binary), else builds from
# source. Touches nothing outside its own temp directory. No real people or
# money. The admission decisions (demo/lamp/admission-*.json) are
# capsule-engine's real output, kept so the demo needs no engine install; a
# real deal seals the engine's output for its own counterparty and amount.
# demo/jet-ski and run-demo-x-deal-v0.sh are the legacy x-deal-v0 demo.

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
demo="$skill_dir/demo/lamp"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export XDG_CONFIG_HOME="$work/config"
unset CAPSULE_DEAL_CHECK_URL

bin="${CAPSULECTL:-}"
if [[ -z "$bin" ]]; then
  # The core from the capsule-cli release this repository builds against,
  # and this repository's deal plugin on a plugin root of the run's own.
  bin="$work/capsulectl"
  mkdir -m 0700 "$work/plugins"
  (cd "$skill_dir/../.." && go build -o "$bin" github.com/action-state-group/capsule-cli/cmd/capsulectl &&
    go build -o "$work/plugins/capsulectl-deal" ./cmd/capsulectl-deal)
  export CAPSULECTL_PLUGIN_ROOTS="$work/plugins"
fi
buyer() { "$bin" --profile buyer deal "$@"; }
seller() { "$bin" --profile seller deal "$@"; }
cd "$work"

# Two profiles, one per side. The offer names the seller's key: the buyer
# takes an agreement only from the key its offer names.
seller_key=$("$bin" deal init --profile seller --dir seller --no-witness --materiality builtin:neutral | jq -r .public_key)
# The buyer pins no materiality predicate, so every attribute its agent
# picked (here, the item) pauses the check for the user.
"$bin" deal init --profile buyer --dir buyer --no-witness >/dev/null
sed "s/SELLER_KEY/$seller_key/" "$demo/offer.json" >offer.json

# The buyer offers; the user sends offer.sent.json to the seller. Both
# sides know the deal by the same id.
buyer offer --input offer.json --out offer.sent.json >offered.json
offer=$(jq -r .offer offered.json)
id=$(jq -r .deal_id offered.json)
seller receive offer.sent.json >/dev/null

# Each side seals its own admission decision, then the buyer its intent.
seller admit --deal "$id" --input "$demo/admission-seller.json" >/dev/null
buyer admit --deal "$id" --input "$demo/admission-buyer.json" >/dev/null
buyer note --deal "$id" --kind intent --input "$demo/intent.json" --opening "$demo/opening.json" >/dev/null

# The agreement exchange: the seller (the acceptor) agrees first.
seller agree "$offer" --out seller-agreement.json >/dev/null
buyer receive seller-agreement.json >/dev/null
buyer agree "$offer" --out buyer-agreement.json >/dev/null
seller receive buyer-agreement.json >/dev/null

# The check before paying pauses and shows the user a card.
buyer check --deal "$id" --input "$demo/check-pay.json" >check.json

# Trailing whitespace is not part of the card: a saved file may end in a
# newline, spaces or CRLF. $(...) drops the trailing newlines.
trim_end() { sed -e 's/[[:space:]]*$//'; }
card=$(jq -r .card check.json | trim_end)
expected=$(trim_end <"$demo/expected-card.txt")
printf '%s\n\n' "$card"
if [[ "$card" != "$expected" ]]; then
  printf 'unexpected card; expected:\n%s\n' "$expected" >&2
  exit 1
fi

# The user holds: the answer cites the card exactly as shown.
jq -r .card check.json >card.txt
check_id=$(jq -r .check_id check.json)
buyer note --deal "$id" --kind approval --check "$check_id" --choice hold --said "hold" --shown-card card.txt >/dev/null

# The report: each row's status and check, and the note that the person
# has signed nothing yet. Nothing was paid, so nothing is settled.
buyer report --deal "$id" | jq -r '(.report.rows[] | "\(.id): \(.status), check \(.check)"), "settled: \(.report.settled)", "", .settled_note'
