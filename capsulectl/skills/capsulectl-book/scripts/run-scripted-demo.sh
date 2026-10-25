#!/usr/bin/env bash
set -euo pipefail

# Exercises every verb in SKILL.md against two throwaway jsonl book profiles
# in a temp directory, running `capsulectl` (the core) with `capsulectl-book`
# (the plugin) built from this checkout:
#
#   - request, respond, close and result build are the CONSEQUENTIAL book
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
  --namespace demo --log-id skill-demo --operator demo-operator \
  --trusted-key "$public_key" --signing-key-file "$seed" \
  --checkpoint-signing-key-file "$checkpoint_seed" --checkpoint-trusted-key "$checkpoint_public_key" >/dev/null
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
  --namespace demo --log-id skill-demo-peer --operator demo-peer-operator \
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

echo "== 1/6 request + respond (each a signed book record; consequential) ==" >&2
cat >"$work/evidence-request.json" <<'EOF'
{"subject":{"full_history":null},"coverage":{"min_freshness":1}}
EOF
"$bin" book request --profile "$profile" --request "$work/evidence-request.json" --responder "$peer" \
  --output "$work/request.sent" >"$work/request-result.json"
record_of "$work/request-result.json"
request_record=$(jq -r .record_id "$work/request-result.json")
"$bin" book respond --profile "$peer" --request "$work/request.sent" --requester "$profile" \
  --output "$work/response.json" >"$work/respond-result.json"
record_of "$work/respond-result.json"
jq -e '.outcome=="artifact"' "$work/respond-result.json" >/dev/null || {
  echo "FAIL: an identified requester asking for full history under the default policy should get an artifact" >&2; exit 1; }
"$bin" book request --profile "$profile" --for "$(jq -r .record_id "$work/request-result.json")" \
  --response "$work/response.json" --responder-key "$peer_public_key" \
  --responder-checkpoint-key "$peer_checkpoint_key" >"$work/response-recorded.json"
record_of "$work/response-recorded.json"
jq -e '.outcome=="artifact"' "$work/response-recorded.json" >/dev/null || {
  echo "FAIL: the requester did not record the verified artifact" >&2; exit 1; }
echo "  asked, answered, and recorded the answer: three book records" >&2

echo "== 2/6 the counterparty closes first, no peer bundle (primary action; signs) ==" >&2
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

echo "== 3/6 close against the counterparty's held bundle (primary action; signs) ==" >&2
"$bin" book close --profile "$profile" --period day --date "$yesterday" --counterparty skill-demo-peer \
  --peer "$work/peer-close-bundle.json" --peer-checkpoint-key "$peer_checkpoint_key" >"$work/close-peer-result.json"
record_of "$work/close-peer-result.json"
jq -e '.reconciliation.peer_complete==true and .reconciliation.states.MATCHED==1 and .reconciliation.states.CONFLICTING==1' \
  "$work/close-peer-result.json" >/dev/null || {
  echo "FAIL: one matching and one differing exchange should reconcile MATCHED + CONFLICTING against a complete peer account" >&2; exit 1; }
echo "  one exchange MATCHED, one CONFLICTING, from the held bundle alone" >&2

echo "== 4/6 reconcile (not-consequential; adds no record) ==" >&2
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
echo "  same states as the sealed Close; a bundle under the wrong key or from another book is refused" >&2

echo "== 5/6 result build (primary action; signs) ==" >&2
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

echo "== 6/6 disclose on the Result + report build (not-consequential; adds no record) ==" >&2
result_record=$(jq -r .record_id "$work/result-build.json")
"$bin" disclose --profile "$profile" --root "$result_record" --payloads selected --out "$work/result-bundle.json" >/dev/null
"$bookdemo" aac-verify "$work/result-bundle.json" >"$work/result-bundle-verify.json"
"$bin" book report build --bundle "$work/result-bundle.json" --card outcome --permalink --out "$work/report.html" >"$work/report-build.json"
jq -e '.verification=="pass" and .form=="book" and .claims==2 and .unsupported_claims==0 and (.permalink|startswith("https://"))' \
  "$work/report-build.json" >/dev/null || {
  echo "FAIL: report build should verify the book-form Result bundle and render both claims supported" >&2; exit 1; }
grep -q 'renderEvidenceGraph(window.__BUNDLE__' "$work/report.html" && ! grep -q 'https\?://' "$work/report.html" || {
  echo "FAIL: report.html must be self-contained (inline runtime, nothing loaded from the network)" >&2; exit 1; }
set +e
"$bin" book report build --bundle "$work/peer-close-bundle.json" --card outcome --out "$work/report-wrong.html" >/dev/null 2>&1
wrong_status=$?
set -e
[[ "$wrong_status" -eq 2 && ! -e "$work/report-wrong.html" ]] || {
  echo "FAIL: a bundle rooted at a Close, not a Result, must be refused (exit 2) with nothing written, got $wrong_status" >&2; exit 1; }
echo "  the Result roots a verified bundle; report.html rendered offline; a non-Result root is refused" >&2

end_epoch=$(date +%s)
elapsed=$((end_epoch - start_epoch))
book_records=$(wc -l <"$book_records_file" | tr -d ' ')

echo >&2
echo "== summary ==" >&2
echo "book records from consequential book verbs (request x2, respond, close x2, result build): $book_records (target: 6)" >&2
echo "not-consequential book verbs (reconcile, report build): ran, added no record" >&2
echo "elapsed: ${elapsed}s from a fresh build through the last verb (target: under 3600s)" >&2

if [[ "$book_records" -ne 6 ]]; then
  echo "FAIL: expected exactly 6 book records from consequential book verbs, got $book_records" >&2
  exit 1
fi
if [[ "$elapsed" -ge 3600 ]]; then
  echo "FAIL: exceeded the one-hour target (${elapsed}s)" >&2
  exit 1
fi
echo "PASS" >&2
