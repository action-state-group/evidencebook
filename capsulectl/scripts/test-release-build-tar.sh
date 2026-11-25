#!/usr/bin/env bash
# test-release-build-tar.sh -- checks how release-build.sh picks GNU tar, with
# stub tar commands (evidencebook#939). Each case runs the script with
# RELEASE_BUILD_TAR_ONLY=1, which stops right after the choice, so a refusal is
# shown to come before anything is built.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

stub() {  # stub DIR NAME VERSION-LINE
  mkdir -p "$1"
  # shellcheck disable=SC2016 # the stub's own $1, written literally
  printf '#!/bin/sh\n[ "$1" = --version ] && echo "%s"\n' "$3" > "$1/$2"
  chmod +x "$1/$2"
}
stub "$work/bsd" tar 'bsdtar 3.5.3 - libarchive 3.7.4'
stub "$work/gnu" tar 'tar (GNU tar) 1.35'
stub "$work/bsd+gtar" tar 'bsdtar 3.5.3 - libarchive 3.7.4'
stub "$work/bsd+gtar" gtar 'tar (GNU tar) 1.35'

fails=0
check() {  # check NAME WANT-EXIT WANT-OUTPUT-SUBSTRING env...
  local name=$1 want=$2 text=$3; shift 3
  local out=$work/out-$name got=0
  env -u TAR "$@" RELEASE_BUILD_TAR_ONLY=1 "$here/release-build.sh" v0.0.0-test 0000000 "$out" > "$work/$name.log" 2>&1 || got=$?
  if [ "$got" != "$want" ] || ! grep -q -- "$text" "$work/$name.log" || [ -e "$out" ]; then
    echo "FAIL $name: exit $got (want $want), output:"; sed 's/^/  /' "$work/$name.log"
    fails=$((fails + 1))
  else
    echo "ok   $name"
  fi
}
sys=/usr/bin:/bin
check gnu-tar-on-path        0 '^tar$'          PATH="$work/gnu:$sys"
check bsd-tar-falls-to-gtar  0 '^gtar$'         PATH="$work/bsd+gtar:$sys"
check no-gnu-tar-refused     1 'neither tar nor gtar is GNU tar' PATH="$work/bsd:$sys"
check bsd-TAR-refused        1 "TAR=$work/bsd/tar is not GNU tar" PATH="$work/bsd+gtar:$sys" TAR="$work/bsd/tar"
check gnu-TAR-used           0 "^$work/gnu/tar\$" PATH="$work/bsd:$sys" TAR="$work/gnu/tar"
[ "$fails" = 0 ]
