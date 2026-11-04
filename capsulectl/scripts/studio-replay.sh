#!/usr/bin/env bash
# studio-replay.sh FIXTURE SELLER_CONFIG_HOME BUYER_CONFIG_HOME
#
# Replays the ten sticker trades of testdata/studio/sticker-trades.json into a seller's and a
# buyer's deal store, through capsulectl and the deal plugin only (no Go, no network), so the
# Terms Pack Studio demo has past trades to rehearse its terms over. Each trade is one sale
# (a sale closes with its one completed thread) with its buyer thread; the buyer's store holds
# the other half of the same deal under the seller's thread id.
#
# Each config home is an XDG_CONFIG_HOME holding the profile `deal`. A home with no `deal`
# profile gets one, with its store under <home>/deal-store (materiality builtin:neutral, no
# witness); a home that already has one is used as it is. Nothing already in a store is touched,
# so replay into fresh homes: running it twice adds twenty trades.
#
# A trade with a price_change_minor is the seller raising the price after the buyer accepted and
# the seller committed: a detail_change record states the new price, the buyer pays it and the
# seller delivers. (A commit at a changed price is refused by the deal itself, as
# changed_after_acceptance, so the change comes after the commit and the miss is the sealed
# amounts: the offer and the commit at 150, the change and the payment at 175.)
#
# Sealed times are the replay's own clock (the CLI has no way to backdate a record), so every
# trade is paid and delivered within minutes of each other; the fixture carries no times.
#
# Prints one JSON object per trade, then a summary object; exit 1 on the first failed step.
set -euo pipefail

usage() { echo "usage: studio-replay.sh FIXTURE SELLER_CONFIG_HOME BUYER_CONFIG_HOME" >&2; exit 2; }
[ $# -eq 3 ] || usage
fixture=$1
seller_home=$2
buyer_home=$3
[ -s "$fixture" ] || { echo "no fixture at $fixture" >&2; exit 2; }
command -v capsulectl >/dev/null || { echo "capsulectl is not on PATH" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is not on PATH" >&2; exit 2; }
[ "$(jq -r .schema "$fixture")" = "studio-sticker-trades/v0" ] || { echo "$fixture is not studio-sticker-trades/v0" >&2; exit 2; }
mkdir -p "$seller_home" "$buyer_home"
seller_home=$(cd "$seller_home" && pwd)
buyer_home=$(cd "$buyer_home" && pwd)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# side HOME VERB...: capsulectl deal VERB... on the profile `deal` under that config home.
side() {
  local home=$1
  shift
  XDG_CONFIG_HOME=$home capsulectl --profile deal deal "$@"
}
ensure_store() {
  local home=$1
  if ! side "$home" list >/dev/null 2>&1; then
    side "$home" init --dir "$home/deal-store" --materiality builtin:neutral --no-witness >/dev/null
  fi
}
ensure_store "$seller_home"
ensure_store "$buyer_home"

item=$(jq -r .item "$fixture")
currency=$(jq -r .currency "$fixture")
# One payee account for the whole seller store: the store refuses a later payment to another.
pay_to=0x1111111111111111111111111111111111111111
payer=0x2222222222222222222222222222222222222222
network=eip155:8453
asset=0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913
printf '%s sticker bytes\n' "$item" >"$work/sticker.bin"
digest=$(shasum -a 256 "$work/sticker.bin" | cut -d' ' -f1)
price_check='{"item":"'"$item"'","quantity":1}'

count=$(jq '.trades | length' "$fixture")
for i in $(seq 0 $((count - 1))); do
  t=$(jq -c ".trades[$i]" "$fixture")
  n=$(jq -r .n <<<"$t")
  offer=$(jq -r .offer_minor <<<"$t")
  commit=$(jq -r .commit_minor <<<"$t")
  paid=$(jq -r .paid_minor <<<"$t")
  changed=$(jq -r '.price_change_minor // empty' <<<"$t")
  domain=$(jq -r .buyer_domain <<<"$t")
  name=$(jq -r .buyer_name <<<"$t")
  f="$work/t$n"
  mkdir -p "$f"

  # Seller: list the sticker, open the buyer's thread, offer, record the acceptance, commit.
  jq -n --arg item "$item" --arg cur "$currency" --argjson p "$offer" '{type:"purchase",demo:true,
    intent:{verbatim:("Sell my "+$item+" for "+($p|tostring)),min_total_minor:100,asked:{item:$item,quantity:1},allowed:["offer","commit"]},
    terms:{item:$item,quantity:1,price_minor:$p,currency:$cur},recourse:{rail:"x402",refundable:false}}' >"$f/sale.json"
  sale=$(side "$seller_home" sale new --input "$f/sale.json" | jq -r .sale_id)
  jq -n --arg name "$name" --arg d "$domain" --argjson p "$offer" \
    '{channel:"marketplace",who:{name:$name,domain:$d},terms:{price_minor:$p}}' >"$f/thread.json"
  thread=$(side "$seller_home" open --sale "$sale" --input "$f/thread.json" | jq -r .deal_id)

  jq -n --argjson p "$offer" --argjson t "$price_check" \
    '{action:"offer",amount_minor:$p,terms:($t+{handover:"shipped",price_minor:$p}),recourse:{rail:"x402",refundable:false}}' >"$f/offer-check.json"
  offer_check=$(side "$seller_home" check --deal "$thread" --input "$f/offer-check.json" | jq -r .check_id)
  jq -n --argjson p "$offer" '{action:"offer",amount_minor:$p}' >"$f/offer-act.json"
  side "$seller_home" note --deal "$thread" --kind act --input "$f/offer-act.json" >/dev/null
  side "$seller_home" note --deal "$thread" --kind acceptance --check "$offer_check" \
    --words "yes, $offer works for me" --channel marketplace >/dev/null

  jq -n --argjson p "$commit" --argjson t "$price_check" \
    '{action:"commit",amount_minor:$p,terms:($t+{handover:"shipped",price_minor:$p}),recourse:{rail:"x402",refundable:false}}' >"$f/commit-check.json"
  commit_verdict=$(side "$seller_home" check --deal "$thread" --input "$f/commit-check.json" | jq -r .verdict)
  jq -n --argjson p "$commit" '{action:"commit",amount_minor:$p}' >"$f/commit-act.json"
  side "$seller_home" note --deal "$thread" --kind act --input "$f/commit-act.json" >/dev/null
  if [ -n "$changed" ]; then
    jq -n --argjson p "$changed" '{source:"seller message",terms:{price_minor:$p}}' >"$f/change.json"
    side "$seller_home" note --deal "$thread" --kind change --input "$f/change.json" >/dev/null
  fi

  # Buyer: the other half of the same deal, under the seller's thread id; checks and pays.
  jq -n --arg item "$item" --arg cur "$currency" --argjson p "$paid" --arg d "$domain" '{type:"purchase",demo:true,channel:"marketplace",
    intent:{verbatim:("Buy a "+$item+" for up to "+($p|tostring)),party_role:"buyer",asked:{item:$item,quantity:1},max_total_minor:$p,allowed:["pay"]},
    who:{name:"Sticker Shop",domain:"seller.example",relay_address:"shop@seller.example"},
    terms:{item:$item,quantity:1,price_minor:$p,currency:$cur},recourse:{rail:"x402",refundable:false}}' >"$f/buyer-open.json"
  side "$buyer_home" open --deal-id "$thread" --records typed --input "$f/buyer-open.json" >/dev/null
  jq -n --argjson p "$paid" --argjson t "$price_check" \
    '{action:"pay",amount_minor:$p,authorized_max_minor:$p,terms:($t+{price_minor:$p}),recourse:{rail:"x402",refundable:false}}' >"$f/pay-check.json"
  side "$buyer_home" check --deal "$thread" --input "$f/pay-check.json" >/dev/null
  jq -n --argjson p "$paid" '{action:"pay",amount_minor:$p,payee:"Sticker Shop",rail:"x402",reference:("order-"+($p|tostring))}' >"$f/pay-act.json"
  side "$buyer_home" note --deal "$thread" --kind act --input "$f/pay-act.json" >/dev/null

  # Seller: the buyer's terms leg, the payment received, the handoff; then both sides close.
  atomic=$((paid * 10000))
  XDG_CONFIG_HOME=$buyer_home capsulectl deal terms-leg --ephemeral --amount "$atomic" --content-digest "$digest" --out "$f/terms.json" >/dev/null
  tx=$(printf '0x%064X' "$n")
  jq -n --arg net "$network" --arg a "$asset" --arg to "$pay_to" --arg amt "$atomic" \
    '{scheme:"exact",network:$net,amount:$amt,asset:$a,payTo:$to,maxTimeoutSeconds:60,extra:{name:"USD Coin",version:"2"}}' >"$f/requirements.json"
  jq -n --arg tx "$tx" --arg net "$network" --arg p "$payer" '{success:true,transaction:$tx,network:$net,payer:$p}' >"$f/settle.json"
  side "$seller_home" note --deal "$thread" --kind payment_received --terms "$f/terms.json" \
    --settle-response "$f/settle.json" --requirements "$f/requirements.json" >/dev/null
  side "$seller_home" note --deal "$thread" --kind delivered --terms "$f/terms.json" --content "$work/sticker.bin" >/dev/null
  echo '{"status":"received"}' >"$f/close.json"
  side "$buyer_home" close --deal "$thread" --input "$f/close.json" >/dev/null
  side "$seller_home" close --deal "$thread" --input "$f/close.json" >/dev/null

  jq -nc --argjson n "$n" --arg sale "$sale" --arg deal "$thread" --argjson offer "$offer" --argjson commit "$commit" \
    --argjson paid "$paid" --arg v "$commit_verdict" '{trade:$n,sale_id:$sale,deal_id:$deal,offer_minor:$offer,commit_minor:$commit,paid_minor:$paid,commit_check:$v}'
done
jq -nc --argjson n "$count" '{replayed:$n}'
