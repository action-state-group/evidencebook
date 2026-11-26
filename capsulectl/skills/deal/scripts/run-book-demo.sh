#!/usr/bin/env bash
set -euo pipefail

# Exercises every book verb in references/book.md against two throwaway jsonl book profiles
# in a temp directory, running `capsulectl` (the core) with `capsulectl-book`
# (the plugin) built from this checkout:
#
#   - request, respond, close, acknowledge, rebut and result build are the CONSEQUENTIAL book
#     verbs: each returns the id of the signed book record that is its
#     evidence, and the script asserts the count;
#   - reconcile and report build are NOT consequential: they run and add no
#     record. The counterparty's Close verifies with `verify` and its bundle
#     with the neutral AAC bundle verifier; the sealed Result roots a disclosed
#     bundle that `report build` verifies and renders to an offline report.html;
#   - a malformed or mismatched input is refused (exit 2) with nothing written.
#
# A contributor check: it builds from a source checkout, so it needs Go, jq
# and this repository. It is not an install test; a user installs the release
# binaries and the skill from the same tags. It never touches a real profile
# or a real log, or any path outside its own temp directory.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)" # capsulectl/
work="${KEEP:-$(mktemp -d)}"
trap '[[ -n "${KEEP:-}" ]] || rm -rf "$work"' EXIT

export XDG_CONFIG_HOME="$work/config"
mkdir -p "$XDG_CONFIG_HOME"

start_epoch=$(date +%s)

# The core at the version capsulectl/go.mod pins, and the plugin from this
# checkout, in one directory the core finds plugins in.
bin="$work/plugins/capsulectl"
export CAPSULECTL_PLUGIN_ROOTS="$work/plugins"
mkdir -p "$work/plugins"
(cd "$repo_root" && go build -o "$bin" github.com/action-state-group/capsule-cli/cmd/capsulectl \
  && go build -o "$work/plugins/capsulectl-book" ./cmd/capsulectl-book)

profile=demo
seed="$work/signing.hex"
"$bin" key generate --output "$seed" >"$work/keygen.json"
public_key=$(jq -r .public_key "$work/keygen.json")

store="$work/store"
checkpoint_seed="$work/checkpoint-signing.hex"
"$bin" key generate --output "$checkpoint_seed" >"$work/checkpoint-keygen.json"
checkpoint_public_key=$(jq -r .public_key "$work/checkpoint-keygen.json")
"$bin" profile create --name "$profile" --type jsonl --jsonl-path "$store" \
  --namespace demo --log-id skill-demo --log book --operator demo-operator \
  --trusted-key "$public_key" --signing-key-file "$seed" \
  --checkpoint-signing-key-file "$checkpoint_seed" --checkpoint-trusted-key "$checkpoint_public_key" >/dev/null
# A new store is split by month windows; the demo seeds yesterday's records
# into one JSONL book, so it creates that store explicitly.
"$bin" book window init --profile "$profile" --period none >/dev/null
"$bin" store init --profile "$profile" >/dev/null
# This profile's one log is its evidence book. Yesterday's exchanges go in
# first, before anything today: `close` refuses a period that has not ended,
# and a log whose commit times go backwards cannot be closed at all.
# bookdemo is a fixture helper, not part of capsulectl.
bookdemo="$work/bookdemo"
(cd "$repo_root" && go build -o "$bookdemo" ./scripts/bookdemo)
yesterday=$(date -u -v-1d +%Y-%m-%d 2>/dev/null || date -u -d yesterday +%Y-%m-%d)
"$bookdemo" seed --store "$store" --log-id skill-demo --operator demo-operator \
  --signing-key-file "$seed" --checkpoint-key-file "$checkpoint_seed" --at "${yesterday}T12:00:00Z" \
  booking-1:req-1:confirmed booking-2:req-2:refunded

# The steps run the book verbs over two local books: this profile's and a
# second profile standing in for the counterparty. bookdemo seeds both books
# with yesterday's exchanges first.
peer=demo-peer
peer_seed="$work/peer-signing.hex"
peer_checkpoint_seed="$work/peer-checkpoint-signing.hex"
"$bin" key generate --output "$peer_seed" >"$work/peer-keygen.json"
"$bin" key generate --output "$peer_checkpoint_seed" >"$work/peer-checkpoint-keygen.json"
peer_public_key=$(jq -r .public_key "$work/peer-keygen.json")
peer_checkpoint_key=$(jq -r .public_key "$work/peer-checkpoint-keygen.json")
peer_store="$work/peer-store"
"$bin" profile create --name "$peer" --type jsonl --jsonl-path "$peer_store" \
  --namespace demo --log-id skill-demo-peer --log book --operator demo-peer-operator \
  --trusted-key "$peer_public_key" --signing-key-file "$peer_seed" \
  --checkpoint-signing-key-file "$peer_checkpoint_seed" --checkpoint-trusted-key "$peer_checkpoint_key" >/dev/null
"$bookdemo" seed --store "$peer_store" --log-id skill-demo-peer --operator demo-peer-operator \
  --signing-key-file "$peer_seed" --checkpoint-key-file "$peer_checkpoint_seed" --at "${yesterday}T12:00:00Z" \
  booking-1:req-1:confirmed booking-2:req-2:cancelled

book_records_file="$work/book-records.txt"
: >"$book_records_file"
record_of() { # FILE: log the record_id a consequential book verb returned, or fail closed
  local id
  id=$(jq -r .record_id "$1")
  [[ "$id" =~ ^[0-9a-f]{64}$ ]] || { echo "FAIL: evidence unavailable -- no record_id in $1" >&2; exit 1; }
  echo "$id" >>"$book_records_file"
}

# The books' sizes: a not-consequential verb must leave both logs as they were.
book_size() { wc -l <"$1/book/log.jsonl" | tr -d ' '; }
assert_no_record() { # BEFORE_OWN BEFORE_PEER VERB
  [[ "$(book_size "$store")" == "$1" && "$(book_size "$peer_store")" == "$2" ]] || {
    echo "FAIL: $3 added a record to a book" >&2; exit 1; }
}

echo "== 1/7 request + respond (each a signed book record; consequential) ==" >&2
cat >"$work/evidence-request.json" <<'EOF'
{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}
EOF
"$bin" book request --profile "$profile" --request "$work/evidence-request.json" --responder "$peer" \
  --output "$work/request.sent" >"$work/request-result.json"
record_of "$work/request-result.json"
request_record=$(jq -r .record_id "$work/request-result.json")
"$bin" book respond --profile "$peer" --request "$work/request.sent" --requester "$profile" \
  --output "$work/response.json" >"$work/respond-result.json"
record_of "$work/respond-result.json"
jq -e '.outcome=="artifact"' "$work/respond-result.json" >/dev/null || {
  echo "FAIL: an identified requester asking for the checkpoints should get an artifact" >&2; exit 1; }
"$bin" book request --profile "$profile" --for "$(jq -r .record_id "$work/request-result.json")" \
  --response "$work/response.json" --responder-key "$peer_public_key" \
  --responder-checkpoint-key "$peer_checkpoint_key" >"$work/response-recorded.json"
record_of "$work/response-recorded.json"
jq -e '.outcome=="artifact"' "$work/response-recorded.json" >/dev/null || {
  echo "FAIL: the requester did not record the verified artifact" >&2; exit 1; }
echo "  asked, answered, and recorded the answer: three book records" >&2

echo "== 2/7 the counterparty closes first, no peer bundle (primary action; signs) ==" >&2
# A book's counterparty is named by its book id (the other profile's
# log_id). The peer holds only its own account, so every exchange reads
# INSUFFICIENT; its bundle is what it hands over.
"$bin" book close --profile "$peer" --period day --date "$yesterday" --counterparty skill-demo \
  --capsule-out "$work/peer-close.json" --bundle-out "$work/peer-close-bundle.json" >"$work/peer-close-result.json"
record_of "$work/peer-close-result.json"
jq -e '.reconciliation.states.INSUFFICIENT==2 and .reconciliation.states.CONFLICTING==0 and .reconciliation.peer_complete==false' \
  "$work/peer-close-result.json" >/dev/null || {
  echo "FAIL: with no peer bundle each exchange must read INSUFFICIENT, never CONFLICTING" >&2; exit 1; }
"$bin" verify --profile "$peer" --capsule "$work/peer-close.json" >"$work/peer-close-verify.json"
"$bookdemo" aac-verify "$work/peer-close-bundle.json" >"$work/peer-close-bundle-verify.json"
"$bin" book close --profile "$peer" --period day --date "$yesterday" --counterparty skill-demo >"$work/peer-close-again.json"
jq -e --arg id "$(jq -r .record_id "$work/peer-close-result.json")" '.already_closed==true and .record_id==$id' \
  "$work/peer-close-again.json" >/dev/null || {
  echo "FAIL: a second close of the same period and counterparty must return the first, not seal another" >&2; exit 1; }
echo "  sealed; verify accepts the Close; the AAC bundle verifier passes its bundle; a repeat seals no second Close" >&2

echo "== 3/7 close against the counterparty's held bundle (primary action; signs) ==" >&2
"$bin" book close --profile "$profile" --period day --date "$yesterday" --counterparty skill-demo-peer \
  --peer "$work/peer-close-bundle.json" --peer-checkpoint-key "$peer_checkpoint_key" >"$work/close-peer-result.json"
record_of "$work/close-peer-result.json"
jq -e '.reconciliation.peer_complete==true and .reconciliation.states.MATCHED==1 and .reconciliation.states.CONFLICTING==1' \
  "$work/close-peer-result.json" >/dev/null || {
  echo "FAIL: one matching and one differing exchange should reconcile MATCHED + CONFLICTING against a complete peer account" >&2; exit 1; }
jq -e '(.peer_close|length)==64 and (.held_bundle|length)==64' "$work/close-peer-result.json" >/dev/null || {
  echo "FAIL: a Close citing the counterparty's Close must hold the bundle it relies on (held_bundle)" >&2; exit 1; }
echo "  one exchange MATCHED, one CONFLICTING, from the held bundle alone, which the Close holds and cites" >&2
# Acknowledging the counterparty's Close is the person's statement: run only
# on their word (it makes that Close AGREED when the counterparty reads it).
"$bin" book acknowledge --profile "$profile" --period day --date "$yesterday" --counterparty skill-demo-peer \
  --peer "$work/peer-close-bundle.json" --peer-checkpoint-key "$peer_checkpoint_key" >"$work/acknowledge-result.json"
record_of "$work/acknowledge-result.json"
jq -e --arg id "$(jq -r .record_id "$work/peer-close-result.json")" '.peer_close==$id and .record_type=="acknowledgement" and .already_recorded==false' \
  "$work/acknowledge-result.json" >/dev/null || {
  echo "FAIL: acknowledge must answer the counterparty's Close for the period" >&2; exit 1; }
"$bin" book acknowledge --profile "$profile" --period day --date "$yesterday" --counterparty skill-demo-peer \
  --peer "$work/peer-close-bundle.json" --peer-checkpoint-key "$peer_checkpoint_key" >"$work/acknowledge-again.json"
jq -e --arg id "$(jq -r .record_id "$work/acknowledge-result.json")" '.already_recorded==true and .record_id==$id' \
  "$work/acknowledge-again.json" >/dev/null || {
  echo "FAIL: a second acknowledge of the same Close must return the first" >&2; exit 1; }
echo "  acknowledged the counterparty's Close; a repeat adds nothing" >&2
# A later dispute, in the person's words: a rebuttal outranks the
# acknowledgement, and acknowledging again is then refused.
"$bin" book rebut --profile "$profile" --period day --date "$yesterday" --counterparty skill-demo-peer \
  --peer "$work/peer-close-bundle.json" --peer-checkpoint-key "$peer_checkpoint_key" \
  --reason "the second exchange differs from ours" >"$work/rebut-result.json"
record_of "$work/rebut-result.json"
jq -e '.record_type=="rebuttal"' "$work/rebut-result.json" >/dev/null || {
  echo "FAIL: rebut must record a rebuttal" >&2; exit 1; }
if "$bin" book acknowledge --profile "$profile" --period day --date "$yesterday" --counterparty skill-demo-peer \
  --peer "$work/peer-close-bundle.json" --peer-checkpoint-key "$peer_checkpoint_key" >/dev/null 2>"$work/ack-after-rebut.err"; then
  echo "FAIL: acknowledging a Close this book rebutted must be refused" >&2; exit 1
fi
echo "  rebutted it with a reason; acknowledging again is refused" >&2

echo "== 4/7 reconcile (not-consequential; adds no record) ==" >&2
sizes=("$(book_size "$store")" "$(book_size "$peer_store")")
"$bin" book reconcile --profile "$profile" --period day --date "$yesterday" --counterparty skill-demo-peer \
  --peer "$work/peer-close-bundle.json" --peer-checkpoint-key "$peer_checkpoint_key" >"$work/reconcile-result.json"
jq -e --slurpfile closed "$work/close-peer-result.json" '.reconciliation.states==$closed[0].reconciliation.states' \
  "$work/reconcile-result.json" >/dev/null || {
  echo "FAIL: reconcile after the Close must compute what the Close sealed" >&2; exit 1; }
for wrong in "--peer-checkpoint-key $checkpoint_public_key --counterparty skill-demo-peer" \
  "--peer-checkpoint-key $peer_checkpoint_key --counterparty someone-else"; do
  set +e
  # shellcheck disable=SC2086 # $wrong is two flag pairs by design
  "$bin" book reconcile --profile "$profile" --period day --date "$yesterday" \
    --peer "$work/peer-close-bundle.json" $wrong >/dev/null 2>&1
  wrong_status=$?
  set -e
  [[ "$wrong_status" -eq 2 ]] || {
    echo "FAIL: a peer bundle under another key, or from another book, must be refused (exit 2), got $wrong_status" >&2; exit 1; }
done
assert_no_record "${sizes[@]}" "book reconcile"
echo "  same states as the sealed Close; a bundle under the wrong key or from another book is refused" >&2

echo "== 5/7 result build (primary action; signs) ==" >&2
# A Result the skill did not write: one requirement claim citing the
# request record, one close claim citing the Close sealed in 4/6 by
# close_ref (REQUIRED, and among the claim's evidence) -- UNILATERAL, since
# no record of the named peer's book acknowledges or rebuts that Close.
close_record=$(jq -r .record_id "$work/close-peer-result.json")
today=$(date -u +%Y-%m-%d)
jq -n --arg request "$request_record" --arg close "$close_record" --arg start "${yesterday}T00:00:00Z" --arg end "${today}T00:00:00Z" '
  def ref($d): {digest_alg:"SHA-256", digest:$d};
  {result_version:"evidence-result-v0", generated_at:($end),
   claims:[
     {id:"req-1", contract_ref:"ec:skill-demo:2026@1", requirement_ref:"asked-once", tier:"recomputed", grade:"self-attested",
      sufficiency:"SATISFIED", verdict:"met", evidence:[ref($request)], proofs:[],
      presentation:{kind:"disclosure", status:"SATISFIED", evidence:[ref($request)]}},
     {id:"close-1", type:"close", contract_ref:"ec:skill-demo:2026@1", requirement_ref:"close", tier:"recomputed", grade:"self-attested",
      sufficiency:"SATISFIED", verdict:"met", evidence:[ref($close)], proofs:[],
      presentation:{kind:"disclosure", status:"SATISFIED", evidence:[ref($close)]},
      close:{period:{start:$start, end:$end}, close_state:"UNILATERAL", close_ref:ref($close), peer:"skill-demo-peer"}}],
   aggregate:{coverage:{evaluated_population:2, excluded_not_applicable:0, unknown_count:0},
              buckets:{met:["req-1","close-1"], not_met:[], not_evaluable:[]}}}' >"$work/result.json"
"$bin" book result build --profile "$profile" --result "$work/result.json" --contract "ec:skill-demo:2026@1" \
  --out "$work/result.sealed.json" --capsule-out "$work/result-record.json" >"$work/result-build.json"
record_of "$work/result-build.json"
"$bin" verify --profile "$profile" --capsule "$work/result-record.json" >"$work/result-verify.json"
jq -e '.already_built==false and .claims==2 and (.cites|length)==2' "$work/result-build.json" >/dev/null || {
  echo "FAIL: result build should seal a two-claim Result citing two book records" >&2; exit 1; }
set +e
jq '.aggregate.buckets.met=["req-1"] | .aggregate.buckets.not_met=["close-1"]' "$work/result.json" >"$work/result-bad.json"
"$bin" book result build --profile "$profile" --result "$work/result-bad.json" --out "$work/result-bad.sealed.json" >/dev/null 2>"$work/result-bad.err"
bad_status=$?
set -e
[[ "$bad_status" -eq 2 ]] && grep -q 'do not cross-check' "$work/result-bad.err" || {
  echo "FAIL: a Result whose buckets do not match its verdicts must be refused (exit 2), got $bad_status" >&2; exit 1; }
echo "  sealed; verify accepts the record; a Result whose headline values do not recompute is refused" >&2

echo "== 6/7 disclose on the Result + report build (not-consequential; adds no record) ==" >&2
result_record=$(jq -r .record_id "$work/result-build.json")
"$bin" disclose --profile "$profile" --root "$result_record" --payloads selected --out "$work/result-bundle.json" >/dev/null
"$bookdemo" aac-verify "$work/result-bundle.json" >"$work/result-bundle-verify.json"
sizes=("$(book_size "$store")" "$(book_size "$peer_store")") # after disclose, which commits its own record
"$bin" book report build --bundle "$work/result-bundle.json" --card outcome --permalink --out "$work/report.html" >"$work/report-build.json"
jq -e '.verification=="pass" and .form=="book" and .claims==2 and .unsupported_claims==0 and (.permalink|startswith("https://"))' \
  "$work/report-build.json" >/dev/null || {
  echo "FAIL: report build should verify the book-form Result bundle and render both claims supported" >&2; exit 1; }
# Nothing is loaded from the network: no src/href/action/url()/@import/fetch()/
# import()/socket names a URL. The vendored runtime's comments may mention one.
remote_load='(src|href|action)[[:space:]]*=[[:space:]]*["'"'"']?(https?:)?//|url\([[:space:]]*["'"'"']?(https?:)?//|@import[[:space:]]+(url\(|["'"'"'])|(fetch|import)[[:space:]]*\([[:space:]]*["'"'"'`]?(https?:)?//|new[[:space:]]+(WebSocket|EventSource)[[:space:]]*\([[:space:]]*["'"'"'`]wss?:'
grep -q 'renderEvidenceGraph(window.__BUNDLE__' "$work/report.html" && ! grep -Eiq "$remote_load" "$work/report.html" || {
  echo "FAIL: report.html must be self-contained (inline runtime, nothing loaded from the network)" >&2; exit 1; }
set +e
"$bin" book report build --bundle "$work/peer-close-bundle.json" --card outcome --out "$work/report-wrong.html" >/dev/null 2>&1
wrong_status=$?
set -e
[[ "$wrong_status" -eq 2 && ! -e "$work/report-wrong.html" ]] || {
  echo "FAIL: a bundle rooted at a Close, not a Result, must be refused (exit 2) with nothing written, got $wrong_status" >&2; exit 1; }
assert_no_record "${sizes[@]}" "book report build"
echo "  the Result roots a verified bundle; report.html rendered offline; a non-Result root is refused" >&2

echo "== 7/7 a Terms Pack by digest: respond + request + derived-inputs + derive (derive is a primary action; signs) ==" >&2
# The counterparty (holder) opts a pack in by listing it in its profile; the
# requester names the pack by the digest of its definition, runs it (here: a
# one-row Result written by hand, as an evaluator would), and derive writes the
# one bundle that verify checks and report build renders.
mkdir -p "$peer_store/book/derivations"
cat >"$peer_store/book/derivations/skill-demo-pack.json" <<PACK
{"document":{"pack":"skill-demo-pack","version":1},"recipients":["$profile"],"forms":["full_history"],"fields":["record_type","committed_at"]}
PACK
cat >"$work/pack-request.json" <<REQ
{"subject":{"full_history":null},"coverage":{"min_freshness":1},"derivation":"$(printf '%s' '{"pack":"skill-demo-pack","version":1}' | jq -cS . | tr -d '\n' | shasum -a 256 | cut -d' ' -f1)"}
REQ
pack_digest=$(jq -r .derivation "$work/pack-request.json")
"$bin" book request --profile "$profile" --request "$work/pack-request.json" --responder "$peer" \
  --output "$work/pack.sent" >"$work/pack-request-result.json"
record_of "$work/pack-request-result.json"
"$bin" book respond --profile "$peer" --request "$work/pack.sent" --requester "$profile" \
  --output "$work/pack-response.json" >"$work/pack-respond-result.json"
record_of "$work/pack-respond-result.json"
jq -e '.outcome=="artifact"' "$work/pack-respond-result.json" >/dev/null || {
  echo "FAIL: a pack the holder listed and granted to this requester should be answered" >&2; exit 1; }
"$bin" book request --profile "$profile" --for "$(jq -r .record_id "$work/pack-request-result.json")" \
  --response "$work/pack-response.json" --responder-key "$peer_public_key" \
  --responder-checkpoint-key "$peer_checkpoint_key" >"$work/pack-recorded.json"
record_of "$work/pack-recorded.json"
pack_response=$(jq -r .record_id "$work/pack-recorded.json")
"$bin" book result derived-inputs --profile "$profile" --response "$pack_response" >"$work/derived-inputs.json"
jq -e --arg d "$pack_digest" '.proven==true and .derivation==$d and (.records|length)>0' "$work/derived-inputs.json" >/dev/null || {
  echo "FAIL: derived-inputs should print the proven rows of the pack the holder answered" >&2; exit 1; }
jq -n --arg r "$pack_response" '{result_version:"evidence-result-v0",generated_at:"2026-11-22T00:00:00Z",
  claims:[{id:"has-record",contract_ref:"ec:skill-demo-pack@1",requirement_ref:"has-record",tier:"recomputed",grade:"self-attested",
    sufficiency:"SATISFIED",verdict:"met",proofs:[],evidence:[{digest:$r,digest_alg:"SHA-256"}],
    presentation:{kind:"disclosure",status:"SATISFIED",evidence:[{digest:$r,digest_alg:"SHA-256"}]}}],
  aggregate:{buckets:{met:["has-record"],not_met:[],not_evaluable:[]},coverage:{evaluated_population:1,excluded_not_applicable:0,unknown_count:0}}}' >"$work/pack-result.json"
"$bin" book report derive --profile "$profile" --response "$pack_response" --pack "$pack_digest" \
  --result "$work/pack-result.json" --out "$work/pack-bundle.json" >"$work/derive-result.json"
jq '{record_id: .result_record_id}' "$work/derive-result.json" >"$work/derive-record.json"
record_of "$work/derive-record.json"
"$bin" verify --bundle "$work/pack-bundle.json" | jq -e '.verdict=="VALID"' >/dev/null || {
  echo "FAIL: the derived bundle must verify VALID" >&2; exit 1; }
"$bin" book report build --bundle "$work/pack-bundle.json" --card outcome --out "$work/pack-report.html" >/dev/null
[[ -s "$work/pack-report.html" ]] || { echo "FAIL: no report page for the derived bundle" >&2; exit 1; }
echo "  the pack was served by digest, the answer recorded, the Result sealed and the one bundle verified and rendered" >&2

end_epoch=$(date +%s)
elapsed=$((end_epoch - start_epoch))
book_records=$(wc -l <"$book_records_file" | tr -d ' ')

echo >&2
echo "== summary ==" >&2
echo "book records from consequential book verbs (request x3, respond x2, response x1, close x2, acknowledge, rebut, result build, report derive): $book_records (target: 12)" >&2
echo "not-consequential book verbs (reconcile, report build): ran, added no record" >&2
echo "elapsed: ${elapsed}s from a fresh build through the last verb (target: under 3600s)" >&2

if [[ "$book_records" -ne 12 ]]; then
  echo "FAIL: expected exactly 12 book records from consequential book verbs, got $book_records" >&2
  exit 1
fi
if [[ "$elapsed" -ge 3600 ]]; then
  echo "FAIL: exceeded the one-hour target (${elapsed}s)" >&2
  exit 1
fi
echo "PASS" >&2
