#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# The evidencebook crate carries no deployment vocabulary: record kinds,
# correlation key names and content tests belong to the calling profile. This
# gate fails if a word that names one deployment's concepts appears anywhere in
# the crate (source, tests, manifest, docs, vendored data).
#
# A word is a hit wherever it stands between non-alphanumeric characters,
# and an underscore counts as a separator, so compound identifiers
# (`some_word_id`, `word_attestation`) are hits too.
#
# Two exemptions exist, and each is per line: a request-draft wire token or an
# Agent Action Capsule member name that happens to contain one of these words,
# marked on that line with
#   vocabulary-gate: request-draft wire token     (the token "exchange")
#   vocabulary-gate: AAC capsule member           (the member "model_attestation")
#   vocabulary-gate: x-deal-v1 forum value        (the forum "auto:twin")
# A mark is only honoured on a line that also carries its token as a quoted
# string, so it cannot silently exempt prose. The x-deal-v1 record schema,
# vendored byte for byte from dealprofile/x-deal-v1 (a test holds it equal),
# names the same forum in its pattern and description, which a JSON file can
# carry no mark for: its two lines are exempt by file and content.
set -euo pipefail

crate_dir="$(cd "$(dirname "$0")/.." && pwd)"
words='mesh|exchange|exchanges|twin|twins|provider|providers|model|models'
mark='vocabulary-gate: request-draft wire token'
aac_mark='vocabulary-gate: AAC capsule member'
forum_mark='vocabulary-gate: x-deal-v1 forum value'
v1_schema='/schemas/vendor/x-deal-v1/x-deal-v1.schema.json:'

hits="$(grep -RIniE "(^|[^[:alnum:]])($words)($|[^[:alnum:]])" "$crate_dir" \
  --exclude-dir=target --exclude=Cargo.lock --exclude="$(basename "$0")" || true)"

violations="$(printf '%s\n' "$hits" | grep -v '^$' | while IFS= read -r line; do
  if [[ "$line" == *"$mark"* && "$line" == *'"exchange"'* ]]; then
    continue
  fi
  if [[ "$line" == *"$aac_mark"* && "$line" == *'"model_attestation"'* ]]; then
    continue
  fi
  if [[ "$line" == *"$forum_mark"* && "$line" == *'"auto:twin"'* ]]; then
    continue
  fi
  if [[ "$line" == *"$v1_schema"* && ( "$line" == *'auto:(twin|ref)'* || "$line" == *'auto:twin, auto:ref'* ) ]]; then
    continue
  fi
  printf '%s\n' "$line"
done)"

if [[ -n "$violations" ]]; then
  echo "deployment vocabulary in the evidencebook crate:" >&2
  printf '%s\n' "$violations" >&2
  exit 1
fi
echo "vocabulary gate: clean"
