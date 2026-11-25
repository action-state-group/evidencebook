#!/usr/bin/env bash
# release-build.sh VERSION COMMIT OUTDIR -- the one build command for the
# release assets of both launchers, capsulectl-book and capsulectl-deal, used
# by release.yml, CI's dry run and local rebuilds. VERSION is the module
# version (v1.2.3), the tag without its capsulectl/ prefix.
#
# Reproducible, as capsule-cli's: CGO off, -trimpath, no VCS stamping, empty
# build id, and only the versions injected. The same Go toolchain (go.mod's
# `go` line) and the same inputs give byte-identical assets. Each launcher
# embeds the capsulectl core it builds against (capsule-cli#326, design note
# section 4.2): cli.CLIVersion and cli.CLICommit are that core's release and
# commit, read from go.mod, and cli.PluginVersion and cli.PluginCommit are
# this release's. The handshake reports the first as core_version and the
# second as version. Both launchers carry the same version: one tag, one
# release (evidencebook#610).
#
# Assets:
#   capsulectl-book-VERSION-OS-ARCH, capsulectl-deal-VERSION-OS-ARCH, one per target;
#   standard-history-VERSION.tar.gz, the History card module's files, to unpack
#     beside the book launcher;
#   deal-skill-VERSION.tar.gz, the skills/deal tree;
#   deal-view-VERSION.tar.gz, the deal view's files as the core reads them
#     beside the deal launcher (capsulectl-deal.d/deal-view.js and .css, the
#     bytes the binary embeds and its handshake states);
#   SHA256SUMS over all of them.
# Every archive is deterministic (sorted entries, fixed mtime, owner 0:0,
# gzip -n). It needs GNU tar: TAR names it, else `tar` when that is GNU tar,
# else `gtar` (macOS: brew install gnu-tar); anything else is refused before
# the build starts.
set -euo pipefail

VERSION="${1:?version, e.g. v0.1.0}"
COMMIT="${2:?full commit sha}"
OUT="${3:?output directory}"
CORE=github.com/action-state-group/capsule-cli
PKG=$CORE/cli
TARGETS=(linux/amd64 linux/arm64 darwin/arm64)
LAUNCHERS=(capsulectl-book capsulectl-deal)

# The archives need GNU tar's --format=gnu, --mode and --mtime options; macOS's
# tar is bsdtar, which refuses them at the first archive.
is_gnu_tar() { "$1" --version 2> /dev/null | head -n 1 | grep -q 'GNU tar'; }
if [ -n "${TAR:-}" ]; then
  if ! is_gnu_tar "$TAR"; then
    echo "release-build.sh: TAR=$TAR is not GNU tar, which the archives need." >&2
    echo "Unset TAR to let this script find GNU tar (tar, then gtar), or set TAR=gtar after: brew install gnu-tar (macOS)." >&2
    exit 1
  fi
elif is_gnu_tar tar; then TAR=tar
elif command -v gtar > /dev/null && is_gnu_tar gtar; then TAR=gtar
else
  echo "release-build.sh: GNU tar is needed for the archives, and neither tar nor gtar is GNU tar." >&2
  echo "On macOS: brew install gnu-tar (it installs gtar, which this script then finds)." >&2
  echo "On Linux: install your distribution's tar package." >&2
  exit 1
fi
# RELEASE_BUILD_TAR_ONLY=1 prints the tar it would use and builds nothing
# (scripts/test-release-build-tar.sh).
if [ -n "${RELEASE_BUILD_TAR_ONLY:-}" ]; then
  echo "$TAR"
  exit 0
fi

cd "$(dirname "$0")/.."
core_version=$(go list -m -f '{{.Version}}' "$CORE")
core_commit=$(go mod download -json "$CORE@$core_version" | jq -er '.Origin.Hash')

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
ldflags="-s -w -buildid= -X $PKG.CLIVersion=$core_version -X $PKG.CLICommit=$core_commit -X $PKG.PluginVersion=$VERSION -X $PKG.PluginCommit=$COMMIT"
for launcher in "${LAUNCHERS[@]}"; do
  for target in "${TARGETS[@]}"; do
    os=${target%/*} arch=${target#*/}
    GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -ldflags "$ldflags" -o "$OUT/$launcher-$VERSION-$os-$arch" "./cmd/$launcher"
  done
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The History card module (capsuleviewer.standard-history/v0), unpacked into the
# plugin root beside capsulectl-book: standard-history-module/ holds the four
# files the handshake names, the bytes capsule-viewer's release bundle carries
# (internal/book/assets/standard-history, which SOURCE.txt names).
mkdir "$tmp/standard-history-module"
# The files are the ones the module's own handshake entry names.
files=()
while IFS= read -r f; do files+=("$f"); done < <(jq -r '.files[].path | sub("^standard-history-module/"; "")' internal/book/assets/standard-history/presentation.json)
for f in "${files[@]}"; do cp "internal/book/assets/standard-history/$f" "$tmp/standard-history-module/"; done
(cd "$tmp" && { printf '%s\0' standard-history-module; for f in $(printf '%s\n' "${files[@]}" | LC_ALL=C sort); do printf '%s\0' "standard-history-module/$f"; done; } |
  "$TAR" --null --no-recursion -T - --format=gnu --owner=0 --group=0 --numeric-owner \
    --mode='u+rw,go=rX' --mtime='1970-01-01 00:00:00Z' -cf -) |
  gzip -n -9 >"$OUT/standard-history-$VERSION.tar.gz"

# The deal skill: the tree as committed, in a fixed order with fixed metadata.
git ls-files -z skills/deal | LC_ALL=C sort -z |
  "$TAR" --null -T - --format=gnu --owner=0 --group=0 --numeric-owner \
    --mode='u+rw,go=rX' --mtime='1970-01-01 00:00:00Z' -cf - |
  gzip -n -9 >"$OUT/deal-skill-$VERSION.tar.gz"

# The deal view, unpacked into the plugin root beside capsulectl-deal.
mkdir "$tmp/capsulectl-deal.d"
cp internal/deal/assets/deal-view.js internal/deal/assets/deal-view.css "$tmp/capsulectl-deal.d/"
(cd "$tmp" && printf '%s\0' capsulectl-deal.d capsulectl-deal.d/deal-view.css capsulectl-deal.d/deal-view.js |
  "$TAR" --null --no-recursion -T - --format=gnu --owner=0 --group=0 --numeric-owner \
    --mode='u+rw,go=rX' --mtime='1970-01-01 00:00:00Z' -cf -) |
  gzip -n -9 >"$OUT/deal-view-$VERSION.tar.gz"

cd "$OUT"
assets=(capsulectl-book-"$VERSION"-* capsulectl-deal-"$VERSION"-* standard-history-"$VERSION".tar.gz deal-skill-"$VERSION".tar.gz deal-view-"$VERSION".tar.gz)
if command -v sha256sum >/dev/null; then
  sha256sum "${assets[@]}" > SHA256SUMS
else
  shasum -a 256 "${assets[@]}" > SHA256SUMS
fi
cat SHA256SUMS
