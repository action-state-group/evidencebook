#!/usr/bin/env bash
# check-standard-history-archive.sh BINARY ARCHIVE -- the History card archive
# holds exactly the files the binary's handshake states, at the paths it
# states, with the digests it states: what capsulectl checks when it loads the
# module beside the launcher. Run by CI's release dry run and the release.
set -euo pipefail

BIN="${1:?plugin binary}"
ARCHIVE="${2:?standard-history-VERSION.tar.gz}"
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT

tar -xzf "$ARCHIVE" -C "$dir"
meta=$("$BIN" cli-plugin-metadata)
stated=$(jq -r '.presentations[].files[] | "\(.sha256)  \(.path)"' <<<"$meta" | LC_ALL=C sort -k2)
[ -n "$stated" ] || { echo "the handshake states no presentation files: $meta"; exit 1; }
if command -v sha256sum >/dev/null; then sum=(sha256sum); else sum=(shasum -a 256); fi
held=$(cd "$dir" && find . -type f | sed 's|^\./||' | LC_ALL=C sort | xargs "${sum[@]}" | LC_ALL=C sort -k2)
if [ "$stated" != "$held" ]; then
  echo "the archive does not hold what the handshake states"
  diff <(echo "$stated") <(echo "$held") || true
  exit 1
fi
echo "$ARCHIVE holds the handshake's presentation files"
