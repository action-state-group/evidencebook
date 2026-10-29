#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# run.sh runs every command of docs/THIRD-PARTY-VERIFIER.md from public URLs
# only, and checks every value that page states. It is the page's own test:
# when it passes, a tester who follows the page gets the inputs the page
# describes. It verifies nothing for the tester; that is their verifier's job.
#
#   test/cleanroom/run.sh
#
# Environment:
#   WORK      the working directory the page uses (default ~/eb-verifier-test)
#   EB_REF    the evidencebook commit or branch to clone (default main)
#   EB_DIR    use this evidencebook checkout instead of cloning (CI does)
#   VERIFIER  a command to run once all inputs are in place, as
#             `$VERIFIER "$WORK"`; its output is saved to $WORK/verifier.out
#
# It needs bash, git, curl and Go 1.27 or newer, and nothing else. The
# Dockerfile next to it builds a container with exactly those, and nothing
# from this repository: the container clones it like any tester would.
set -euo pipefail

AAC=9d653f41419d2d2bac4714137b763315947ec754
CLL=0f74297761f3a0b81d27a116f32ac556c0ecb59d
LOOP=cfeaf098bde73d31ed9b08a0ba38a56cd8c333f8
WORK=${WORK:-$HOME/eb-verifier-test}
EB_REF=${EB_REF:-main}

fail() { echo "run.sh: FAIL: $*" >&2; exit 1; }
step() { echo "== $*"; }
sha256() { (sha256sum "$1" 2>/dev/null || shasum -a 256 "$1") | cut -d' ' -f1; }
checksums() { (cd "$1" && { sha256sum -c SHA256SUMS 2>/dev/null || shasum -a 256 -c SHA256SUMS; }); }
fetch() { # repository commit path destination
  curl -fsSL --create-dirs -o "$4" "https://raw.githubusercontent.com/$1/$2/$3"
}
want_sha() { # file sha
  got=$(sha256 "$1")
  [ "$got" = "$2" ] || fail "$1: SHA-256 $got, the page says $2"
}

step "§1 the normative inputs (agent-action-capsule $AAC)"
mkdir -p "$WORK"
cd "$WORK"
WORK=$(pwd)
for f in spec/interop-profile-v0.md spec/draft-mih-agent-evidence-request-00.md \
  vectors/interop-profile-v0/README.md vectors/interop-profile-v0/manifest.json \
  vectors/interop-profile-v0/vectors.json vectors/interop-profile-v0/SHA256SUMS; do
  fetch action-state-group/agent-action-capsule "$AAC" "$f" "aac/$f"
done
checksums aac/vectors/interop-profile-v0 || fail "§1 checksums"

step "§2 Go and evidencebook"
go version
v=$(go env GOVERSION | sed 's/^go//')
case "$v" in 1.2[7-9]* | 1.[3-9]* | [2-9]*) ;; *) fail "Go $v; the page needs 1.27 or newer" ;; esac
if [ -n "${EB_DIR:-}" ]; then
  EB=$(cd "$EB_DIR" && pwd)
else
  [ -d evidencebook ] || git clone -q https://github.com/action-state-group/evidencebook.git
  git -C evidencebook checkout -q "$EB_REF"
  EB=$WORK/evidencebook
fi
cd "$EB"
echo "evidencebook $(git rev-parse HEAD)"
# The page writes its requests and replies to run/. A run/ from an earlier
# attempt may hold a tester's own files, so move it aside, never delete it.
[ -e run ] && mv run "run.$(date +%Y%m%dT%H%M%S)"

step "§3 the test responder"
info=$(go run ./test/interop/answer -info)
for v in e84fed8190a6116016bf68ef2fc1780a919e9ed2d9f88efb895a954ffd7f0117 \
  c82acf66ef2bdc106017544b91873dfbdc92f0963b099d49b0f621bedf76e2af \
  07e8e7a0936fdc0d5eda424946ca36576a0bdff8b7602b419daf308d7719afbd \
  a8f66f1f436bf4e2549faf86040a272f98dc7f578cc78fc0459347db3934f25d \
  78c2628bf823fc958b96c66e47cbaec799194b4da38b5f3ad30eda1404b4a9b0 \
  5053828d77686751cdefd690ff1c8112a3e0f11c038060e69029d300adc412b4 \
  b1c7ffe655132799ca75ec97ecdc47375f77e394eb0ebe07e1d0fab39dad855d \
  b74c29ca59de0592d4cda8fc8d437412017f3faffc8fac3530c14f70f2188773 \
  4312eabdde48711b4f5645a0f2007934c74ea7a7587695c7e5a796119595e0f2 \
  d70427536a4ac642d4afd56afda4554a7ad402288a57c8a9aee9e5bf809182f7 \
  5347d0817c005f092d346fb23e2aa9ed7fd385331cdf9e817c19b72ad918e91b; do
  grep -q "\"$v\"" <<<"$info" || fail "-info does not print $v"
done

step "§4 requests"
mkdir -p run
PIN=a8f66f1f436bf4e2549faf86040a272f98dc7f578cc78fc0459347db3934f25d
REC=b1c7ffe655132799ca75ec97ecdc47375f77e394eb0ebe07e1d0fab39dad855d
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-1","profile":"evidence-interop/0","subject":{"record":"'$REC'"}}' > run/positive.req.json
go run ./test/interop/answer -media-type application/json -requester alice -in run/positive.req.json -out run/positive.reply.json
printf '%b' '\xa4\x65nonce\x65bob-1\x67profile\x72evidence-interop/0\x67subject\xa1\x66record\x78\x40'$REC'\x68coverage\xa1\x6cexpected_pin\x78\x40'$PIN > run/invariance-bob.req.cbor
n=$(wc -c < run/invariance-bob.req.cbor | tr -d ' ')
[ "$n" = 211 ] || fail "the CBOR request is $n bytes, the page says 211"
want_sha run/invariance-bob.req.cbor bb1f40c649a44c58bd0ab9da36f90c3a83938ca0ec5933d73f5488c57e8e487f
go run ./test/interop/answer -media-type application/cbor -requester bob -in run/invariance-bob.req.cbor -out run/invariance-bob.reply.cbor
go run ./test/interop/answer -media-type application/json -requester alice -decline -in run/positive.req.json -out run/declined.reply.json
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-2","profile":"evidence-interop/0","subject":{"record":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}' > run/missing.req.json
go run ./test/interop/answer -media-type application/json -requester alice -in run/missing.req.json -out run/missing.reply.json
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-3","profile":"evidence-interop/9","subject":{"record":"'$REC'"}}' > run/unsupported.req.json
go run ./test/interop/answer -media-type application/json -requester alice -in run/unsupported.req.json -out run/unsupported.reply.json
printf '%s' '{"coverage":{"expected_pin":"'$PIN'"},"nonce":"alice-4","profile":"evidence-interop/0","subject":{"record":"'$REC'"}}' > run/absence.req.json
D=$(sha256 run/absence.req.json)
printf '%s' '{"outcome":"recorded_absence","recorded_at":"2026-10-12T00:01:10Z","request_digest":"'$D'","window":{"closed":"2026-10-12T00:01:10Z","opened":"2026-10-12T00:00:10Z"}}' > run/absence.record.json
n=$(ls run | wc -l | tr -d ' ')
[ "$n" = 11 ] || fail "run/ holds $n files, the page says 11"
for f in positive declined missing unsupported; do
  grep -q '"request_digest"' "run/$f.reply.json" || fail "run/$f.reply.json is not a reply"
done

step "§6.1 the personal-evidence inputs"
cd "$WORK"
for f in spec/personal-agent-history-profile-v0.md spec/evidence-result-v0.md \
  vectors/personal-history-basic-v0/README.md vectors/personal-history-basic-v0/manifest.json \
  vectors/personal-history-basic-v0/vectors.json vectors/personal-history-basic-v0/responder-run.json \
  vectors/personal-history-basic-v0/producer/main.go vectors/personal-history-basic-v0/SHA256SUMS; do
  fetch action-state-group/agent-action-capsule "$AAC" "$f" "aac/$f"
done
checksums aac/vectors/personal-history-basic-v0 || fail "§6.1 checksums"
want_sha aac/vectors/personal-history-basic-v0/vectors.json f1f0e1b95c7dd32f2ddc584d4c55bc9a643a58f550a941651bfd5d3e4c59a4b9

step "§6.2 reproduce the responder run"
cd "$EB"
PH=$WORK/aac/vectors/personal-history-basic-v0
diff "$PH/producer/main.go" test/interop/personal-history/main.go > run/producer.diff || true
cmp test/interop/personal-history/producer.diff run/producer.diff ||
  fail "main.go differs from the corpus producer by more than producer.diff"
go run ./test/interop/personal-history > run/responder-run.json
cmp "$PH/responder-run.json" run/responder-run.json || fail "the responder run differs from the corpus"
want_sha run/responder-run.json f7d77d56d81ca187cb9a8118f8a15aae5407f5a96aca1c5525a5f5d2e827b848

step "§7 a node's bundle (evidence-network/loop $LOOP)"
cd "$WORK"
for f in spec/draft-mih-zhang-agent-disclosure-bundle-01.md spec/draft-mih-scitt-agent-action-capsule-05.md \
  spec/draft-mih-agent-disclosure-envelope-00.md; do
  fetch action-state-group/agent-action-capsule "$AAC" "$f" "aac/$f"
done
for f in spec/draft-mih-scitt-checkpointed-local-log-01.md mmr-conformance-vectors/README.md \
  mmr-conformance-vectors/vectors.json checkpoint-conformance-vectors/README.md; do
  fetch action-state-group/checkpointed-local-log "$CLL" "$f" "cll/$f"
done
NODE=findings/2026-10-28-live/02-book/stevenmih-1
for f in "$NODE/bundle.json" "$NODE/verify.json" \
  nodes/16a607c33006dd4243cb2347c4bdbaa5129a590b5257eea62ac5047abcf87864.json \
  lab/runs/2026-10-28/join/stevenmih-1/book.json \
  nodes/91abddbcdf24bfa5920edb467fa125a4506f8a7bad204880b68f92675f62e2d9.json \
  findings/2026-10-29-live/05-deal/stevenmih-{1,2}/{held-1,held-1-inclusion,held-1-citing,checks}.json; do
  fetch evidence-network/loop "$LOOP" "$f" "loop/$f"
done
want_sha "loop/$NODE/bundle.json" aba8bc0f6792a355f1fc8c34d8994b5c204a18d1fa978b40e26f5a7d0f14dbb0
want_sha "loop/$NODE/verify.json" 9a1ad3b2c39c7d7862d259cce52dab46df9eb686b1a6f915816f9d6597b5e244
grep -q '"key_id": "1dd25d9938ba4841f5e127e39e8dcc71df82b2072d9186ece75e431063ab26d9"' \
  loop/nodes/16a607c33006dd4243cb2347c4bdbaa5129a590b5257eea62ac5047abcf87864.json ||
  fail "the node listing does not name the node key"
grep -q '"checkpoint_key": "dbcc770a60a673668470b1a8083d88f00fd290b92234f9fbb610eec52a689922"' \
  loop/lab/runs/2026-10-28/join/stevenmih-1/book.json || fail "book.json does not name the checkpoint key"

# Every §7 value the page tells a tester to expect is the value the
# downloaded bundle and the published key files carry.
GUIDE=$EB/docs/THIRD-PARTY-VERIFIER.md
B="loop/$NODE/bundle.json"
for v in "$(sha256 "$B")" \
  "$(grep -o '"range_root":"[0-9a-f]*"' "$B" | cut -d'"' -f4)" \
  "$(grep -o '"public_key":"[0-9a-f]*"' "$B" | cut -d'"' -f4)" \
  "$(grep -o '"checkpoint_key": "[0-9a-f]*"' loop/lab/runs/2026-10-28/join/stevenmih-1/book.json | cut -d'"' -f4)"; do
  [ -n "$v" ] && grep -qF "$v" "$GUIDE" || fail "§7 does not state the bundle's value '$v'"
done
# The bundle's own root (its keys are sorted, so the last "root" is the
# top-level one, after checkpoint.root), named like the records below.
r=$(grep -o '"root":"[0-9a-f]*"' "$B" | tail -1 | cut -d'"' -f4)
grep -qF "\`root\` is \`${r:0:8}…\`" "$GUIDE" || fail "§7 does not name the bundle's root $r"
# The page names the three records by their first eight hex digits.
for id in $(grep -o '"body_digests":\[[^]]*\]' "$B" | grep -o '[0-9a-f]\{64\}'); do
  grep -qF "\`${id:0:8}…\`" "$GUIDE" || fail "§7 does not name record $id"
done
grep -q '"missing":\[\]' "$B" || fail "the bundle declares missing records; §7 says none"
grep -q '"countersignatures"' "$B" && fail "the bundle carries countersignatures; §7 says none"
for f in first_seq last_seq mmr_size; do
  v=$(grep -o "\"$f\":[0-9]*" "$B" | head -1 | cut -d: -f2)
  echo "  bundle $f $v"
done
grep -q 'sequences 4 to 6' "$GUIDE" && grep -q '"first_seq":4' "$B" && grep -q '"last_seq":6' "$B" ||
  fail "§7's sequence range is not the bundle's"
grep -q '`log_size` is 10' "$GUIDE" && grep -q '"mmr_size":10' "$B" || fail "§7's log size is not the bundle's"

step "§7.4 two nodes, one exchange"
X=loop/findings/2026-10-29-live/05-deal
want_sha $X/stevenmih-1/held-1.json 07c26ab9a3e56ad8edac043faebae092ac32d5c98d73c4569d77d67c8730def9
want_sha $X/stevenmih-1/held-1-inclusion.json f252e1720b5a51516ba7ec8e69cb9605628a493f61cc83296aa8a102a1c8b811
want_sha $X/stevenmih-1/held-1-citing.json 314d54d90dc2d8e313acb0c57d5494b6b3574d8bf4eedbf8a7c56def64bccaa4
want_sha $X/stevenmih-1/checks.json b96e991fa50f4c4b2a6196ffc418d6484558ce851f0138e9b8787e27105cf0cb
want_sha $X/stevenmih-2/held-1.json 3124d7f54ecf4102c1ffc6ca08a81f08411430ef83c5ad1119ce250905905694
want_sha $X/stevenmih-2/held-1-inclusion.json 07b6a1bec0545ea5be8dccffacb8999c7aad1479b650d3b48e699e9d0a42d451
want_sha $X/stevenmih-2/held-1-citing.json 2fa153cfc0549864cbb9cb8420447cfcc3ea58dff9e5f99a21b23253aface154
want_sha $X/stevenmih-2/checks.json 7f02f94ec73ffa92b0c47ba99f4f516ba3bb93dd8144a22ccea39e70f0b76896
grep -q '"key_id": "6216dcc48158cdb961a544f30201796aa3401ea58a295f359fc2000c040b9e26"' \
  loop/nodes/91abddbcdf24bfa5920edb467fa125a4506f8a7bad204880b68f92675f62e2d9.json ||
  fail "stevenmih-2's listing does not name its key"
# Every value §7.4 states is the files' own.
for n in 1 2; do
  # Full values for the checkpoint and the nonce; the page names records by their first eight hex digits.
  for v in $(grep -o '"\(checkpoint_digest\|log_id\)": *"[^"]*"' $X/stevenmih-$n/held-1-inclusion.json | cut -d'"' -f4) \
    $(grep -o '"half_capsule_id": *"[0-9a-f]*"' $X/stevenmih-$n/held-1-inclusion.json | cut -d'"' -f4 | cut -c1-8) \
    $(grep -o '"capsule_id": *"[0-9a-f]*"' $X/stevenmih-$n/held-1-citing.json | head -1 | cut -d'"' -f4 | cut -c1-8) \
    $(grep -o '"client_nonce": *"[^"]*"' $X/stevenmih-$n/held-1.json | cut -d'"' -f4); do
    grep -qF "$v" "$GUIDE" || fail "§7.4 does not state $v (stevenmih-$n)"
  done
  grep -q '"leaf_index": *32' $X/stevenmih-$n/held-1-inclusion.json || fail "§7.4's leaf_index 32 is not stevenmih-$n's"
  grep -q '"mmr_size": *127' $X/stevenmih-$n/held-1-inclusion.json || fail "§7.4's mmr_size 127 is not stevenmih-$n's"
  grep -q '"citation_purpose": *"counterparty_half"' $X/stevenmih-$n/held-1-citing.json || fail "stevenmih-$n's record does not cite counterparty_half"
  grep -q '"accepted_via": *"host_announced"' $X/stevenmih-$n/held-1-citing.json || fail "stevenmih-$n's acceptance is not host_announced"
done
# Every `leaf_index` and `mmr_size` the page states is the files' value.
[ "$(grep -o '`leaf_index` [0-9]*' "$GUIDE" | sort -u)" = '`leaf_index` 32' ] || fail "§7.4 states a leaf_index other than 32"
[ "$(grep -o '`mmr_size` [0-9]*' "$GUIDE" | sort -u)" = '`mmr_size` 127' ] || fail "§7.4 states an mmr_size other than 127"
# Each side's values sit in its own column: on one table row, stevenmih-1's
# held value comes before stevenmih-2's. And each checkpoint is the other side's.
side() { grep -o "\"$2\": *\"[0-9a-f]*\"" "$1" | head -1 | cut -d'"' -f4; }
for pair in "held-1-inclusion.json checkpoint_digest 64" "held-1-inclusion.json half_capsule_id 8" "held-1-citing.json capsule_id 8"; do
  set -- $pair
  a=$(side $X/stevenmih-1/$1 $2 | cut -c1-$3) b=$(side $X/stevenmih-2/$1 $2 | cut -c1-$3)
  grep -F "$a" "$GUIDE" | grep -q "$a.*$b" || fail "§7.4 does not put $2 $a (stevenmih-1) before $b (stevenmih-2) on one row"
done
K1=1dd25d9938ba4841f5e127e39e8dcc71df82b2072d9186ece75e431063ab26d9
K2=6216dcc48158cdb961a544f30201796aa3401ea58a295f359fc2000c040b9e26
[ "$(side $X/stevenmih-1/held-1-inclusion.json key_id)" = "$K2" ] || fail "stevenmih-1 holds a checkpoint not signed by stevenmih-2's key"
[ "$(side $X/stevenmih-2/held-1-inclusion.json key_id)" = "$K1" ] || fail "stevenmih-2 holds a checkpoint not signed by stevenmih-1's key"
grep -q '"received_from": *"91abddbcdf24bfa5920edb467fa125a4506f8a7bad204880b68f92675f62e2d9"' $X/stevenmih-1/held-1-inclusion.json ||
  fail "stevenmih-1's half did not come from stevenmih-2"
grep -q '"received_from": *"16a607c33006dd4243cb2347c4bdbaa5129a590b5257eea62ac5047abcf87864"' $X/stevenmih-2/held-1-inclusion.json ||
  fail "stevenmih-2's half did not come from stevenmih-1"

echo "run.sh: every input the page names is in place under $WORK"
if [ -n "${VERIFIER:-}" ]; then
  step "your verifier: $VERIFIER $WORK"
  $VERIFIER "$WORK" | tee "$WORK/verifier.out"
fi
