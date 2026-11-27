#!/usr/bin/env bash
# Prints the path of Chrome for Testing at version $1, installing it once into
# ${EB_CI_CACHE:-~/Library/Caches/evidencebook-ci}/chrome (scripts/ci-local.sh
# and its setup-chrome stand-in). One install at a time per version (a lock
# directory), unpacked into a staging directory and moved into place only once
# it runs, so a concurrent check never sees a half-installed or removed copy.
# macOS: ditto keeps the app's framework symlinks, which a plain unzip loses
# (the app then aborts at start).
set -euo pipefail
version=${1:?usage: chrome.sh <version>}
case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) plat=mac-arm64 ;;
  Darwin/x86_64) plat=mac-x64 ;;
  *) echo "chrome.sh: no Chrome for Testing build named for $(uname -s)/$(uname -m)" >&2; exit 2 ;;
esac
cache="${EB_CI_CACHE:-$HOME/Library/Caches/evidencebook-ci}/chrome"
dir="$cache/$version-$plat"
bin="$dir/chrome-$plat/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"
works() { "$bin" --version > /dev/null 2>&1; }
if ! works; then
  mkdir -p "$cache"
  lock="$dir.lock"
  owned=0
  for ((i = 0; i < ${CHROME_LOCK_WAIT:-600}; i++)); do
    if mkdir "$lock" 2> /dev/null; then owned=1; break; fi
    sleep 1
  done
  # Another install holds the lock: leave it and its install alone.
  ((owned)) || { echo "chrome.sh: $lock is held by another install; try again" >&2; exit 2; }
  stage=""
  trap 'rm -rf "$stage"; rmdir "$lock"' EXIT
  stage=$(mktemp -d "$cache/.stage-XXXXXX")
  if ! works; then
    curl -fsSL -o "$stage/chrome.zip" "https://storage.googleapis.com/chrome-for-testing-public/$version/$plat/chrome-$plat.zip"
    mkdir "$stage/unpacked"
    ditto -x -k "$stage/chrome.zip" "$stage/unpacked"
    "$stage/unpacked/chrome-$plat/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing" --version > /dev/null
    rm -rf "$dir"
    mv "$stage/unpacked" "$dir"
  fi
fi
printf '%s\n' "$bin"
