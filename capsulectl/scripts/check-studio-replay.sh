#!/usr/bin/env bash
# check-studio-replay.sh -- the Terms Pack Studio replay fixture (testdata/studio/
# sticker-trades.json, scripts/studio-replay.sh) lands in fresh seller and buyer
# stores as ten closed deals each, the buyer's half under the seller's deal id,
# and a recomputed "price as offered" fold over the seller's exported records finds
# nine met and the one trade whose commit moved after the buyer accepted.
#
#   scripts/check-studio-replay.sh            # uses CAPSULECTL if set, else builds
#
# Needs bash, jq and Go (to build capsulectl and the plugin). Touches nothing outside
# its own temp directory.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/home"
if [[ -n "${CAPSULECTL:-}" ]]; then
  ln -s "$CAPSULECTL" "$work/bin/capsulectl"
else
  (cd "$root" && go build -o "$work/bin/capsulectl" github.com/action-state-group/capsule-cli/cmd/capsulectl)
fi
mkdir -m 0700 "$work/plugins"
(cd "$root" && go build -o "$work/plugins/capsulectl-deal" ./cmd/capsulectl-deal)
export PATH="$work/bin:$PATH" HOME="$work/home" CAPSULECTL_PLUGIN_ROOTS="$work/plugins" CAPSULE_DEAL_CHECK_URL=

fixture="$root/testdata/studio/sticker-trades.json"
bash "$root/scripts/studio-replay.sh" "$fixture" "$work/seller" "$work/buyer" >"$work/replay.out" 2>"$work/replay.err" ||
  { echo "FAIL the replay ran" >&2; cat "$work/replay.err" >&2; exit 1; }
echo "ok   the replay ran"

side() { local home=$1; shift; XDG_CONFIG_HOME=$home capsulectl --profile deal deal "$@"; }
fail=0
check() { # check DESCRIPTION WANT GOT
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else echo "FAIL $1: want $2, got $3" >&2; fail=1; fi
}

for who in seller buyer; do
  closed=$(side "$work/$who" list --json | jq '[.[] | select(.kind == "deal" and .state == "closed")] | length')
  check "the $who store holds ten closed deals" 10 "$closed"
done

met=0 missed=0 missed_trade=
while read -r line; do
  deal=$(jq -r .deal_id <<<"$line")
  side "$work/seller" export --deal "$deal" --output "$work/seller-$deal.json" >/dev/null
  side "$work/buyer" export --deal "$deal" --output "$work/buyer-$deal.json" >/dev/null
  # price as offered: the sealed commit is the sealed offer's amount (a commit that moved
  # after the acceptance is sealed only as an unchecked action, with the amount it tried).
  if jq -e '([.[] | select(.type == "action-record/v0" and .body.action == "offer") | .body.amount_minor] | last) as $o
            | [.[] | select(.type == "action-record/v0" and .body.action == "commit") | .body.amount_minor] | . == [$o]' \
    "$work/seller-$deal.json" >/dev/null; then
    met=$((met + 1))
    # A met trade has no unchecked attempt either.
    check "trade $(jq -r .trade <<<"$line"): no unchecked action" 0 \
      "$(jq '[.[] | select(.type == "action-outcome/v0" and .body.status == "unchecked_action")] | length' "$work/seller-$deal.json")"
  else
    missed=$((missed + 1))
    missed_trade=$(jq -r .trade <<<"$line")
    # The miss is exactly: the offer as made, its acceptance, no sealed commit, and a commit
    # attempt at the raised price refused as changed after the acceptance.
    check "the missed trade is sealed as an offer of 150, its acceptance and a refused commit of 175" true \
      "$(jq '([.[] | select(.type == "action-record/v0" and .body.action == "offer") | .body.amount_minor] == [150])
            and ([.[] | select(.type == "action-approval/v0")] | length == 1)
            and ([.[] | select(.type == "action-record/v0" and .body.action == "commit")] | length == 0)
            and ([.[] | select(.type == "action-outcome/v0" and .body.status == "unchecked_action")] as $u
                 | ($u | length) == 1 and ($u[0].body.outcome == "mismatch" and $u[0].body.attempted.action == "commit"
                   and $u[0].body.attempted.amount_minor == 175 and $u[0].body.findings[0].rule == "changed_after_acceptance"))' "$work/seller-$deal.json")"
  fi
  # the evidence the sibling evaluator reads: one payment, one handoff, each with its sealed time,
  # the payment first (these records have x-deal-v0.record_type and x-deal-v0.at, no top-level type).
  check "trade $(jq -r .trade <<<"$line"): payment then handoff, each with a sealed time" true \
    "$(jq '[.[] | select(.["x-deal-v0"].record_type == "evidence")] as $e
          | ($e | map(.body.source)) == ["x402_settle_response", "seller_handoff"]
            and ($e | all(.["x-deal-v0"].at != null)) and $e[0]["x-deal-v0"].at <= $e[1]["x-deal-v0"].at' "$work/seller-$deal.json")"
  check "trade $(jq -r .trade <<<"$line"): the buyer's half pays what the buyer was asked" "$(jq -r .paid_minor <<<"$line")" \
    "$(jq '[.[] | select(.type == "action-record/v0" and .body.action == "pay") | .body.amount_minor] | first' "$work/buyer-$deal.json")"
done < <(grep '"trade"' "$work/replay.out")
check "the seller store holds ten closed sales" 10 \
  "$(side "$work/seller" list --json | jq '[.[] | select(.kind == "sale" and .state == "closed")] | length')"
check "price as offered met" 9 "$met"
check "price as offered missed" 1 "$missed"
check "the missed trade is trade 7" 7 "$missed_trade"
exit "$fail"
