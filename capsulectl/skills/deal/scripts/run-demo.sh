#!/usr/bin/env bash
set -euo pipefail

# Plays the DEMO jet ski rental end to end against a throwaway deal profile:
# open -> two seller messages -> the payee switch -> a domain lookup -> the
# check before paying the deposit -> the user holds -> the report.
# Prints the difference card and fails if it is not the expected one.
# Writes the report page to $1 when given. Uses CAPSULECTL if set (a
# downloaded release binary), else builds from source.
# Touches nothing outside its own temp directory. No real people or money.

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
demo="$skill_dir/demo/jet-ski"
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
deal() { "$bin" --profile demo deal "$@"; }

# The x-deal-v0 profile ships with EvidenceBook (dealprofile/x-deal-v0):
# DEAL_PROFILE_DIR names a copy of it, else the one this repository builds
# against when the source is here. Without one, the demo pins capsulectl's
# built-in predicate, whose attributes are the same, and skips the checker.
profile_dir="${DEAL_PROFILE_DIR:-}"
if [[ -z "$profile_dir" && -f "$skill_dir/../../go.mod" ]] && command -v go >/dev/null; then
  # Offline or with no module cache, this finds nothing and the demo falls back.
  if dir="$(cd "$skill_dir/../.." && go mod download -json github.com/action-state-group/evidencebook 2>/dev/null | jq -er .Dir)"; then
    profile_dir="$dir/dealprofile/x-deal-v0"
  fi
fi
materiality=builtin:neutral
if [[ -n "$profile_dir" ]]; then
  materiality="$profile_dir/materiality-predicate/neutral.json"
fi

deal init --dir "$work/store" --materiality "$materiality" >/dev/null
id=$(deal open --input "$demo/01-open.json" | jq -r .deal_id)
deal note --deal "$id" --kind message --input "$demo/02-message-quote.json" >/dev/null
deal note --deal "$id" --kind message --input "$demo/03-message-switch.json" >/dev/null
deal note --deal "$id" --kind change --input "$demo/04-change.json" >/dev/null
deal note --deal "$id" --kind evidence --input "$demo/05-evidence-domain.json" >/dev/null
deal check --deal "$id" --input "$demo/06-check-pay.json" >"$work/check.json"

# Trailing whitespace is not part of the card: a saved file may end in a
# newline, spaces or CRLF. $(...) drops the trailing newlines.
trim_end() { sed -e 's/[[:space:]]*$//'; }
card=$(jq -r .card "$work/check.json" | trim_end)
expected=$(trim_end <"$demo/expected-card.txt")
printf '%s\n\n' "$card"
if [[ "$card" != "$expected" ]]; then
  printf 'unexpected card; expected:\n%s\n' "$expected" >&2
  exit 1
fi

check_id=$(jq -r .check_id "$work/check.json")
deal note --deal "$id" --kind approval --check "$check_id" --choice hold --said "hold" >/dev/null
# The sealed records carry no raw values; check them against the profile.
deal export --deal "$id" --output "$work/records.json" >/dev/null
if [[ -n "$profile_dir" ]] && command -v python3 >/dev/null; then
  python3 "$profile_dir/check_profile.py" "$work/records.json" | tail -1
fi

if [[ -n "${1:-}" ]]; then
  deal report --deal "$id" --html "$1" | jq -r .trail
  printf '\nreport: %s\n' "$1"
else
  deal report --deal "$id" | jq -r .trail
fi
