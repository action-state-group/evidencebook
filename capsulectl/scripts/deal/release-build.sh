#!/usr/bin/env bash
# release-build.sh VERSION COMMIT OUTDIR -- the one build command for release
# assets, used by .github/workflows/release.yml, CI's dry run and local
# rebuilds.
#
# Reproducible, as capsule-cli's: CGO off, -trimpath, no VCS stamping, empty
# build id, and only the versions injected. The same Go toolchain (go.mod's
# `go` line) and the same inputs give byte-identical assets, so anyone can
# rebuild a tag and compare against SHA256SUMS.
#
# The plugin embeds the capsulectl core it builds against (capsule-cli#326,
# design note section 4.2): cli.CLIVersion and cli.CLICommit are that core's
# release and commit, read from go.mod, and cli.PluginVersion and
# cli.PluginCommit are this release's. The handshake reports the first as
# core_version and the second as version.
#
# Assets: capsulectl-deal-VERSION-OS-ARCH for each target;
# deal-skill-VERSION.tar.gz, the skills/deal tree; and deal-view-VERSION.tar.gz,
# the deal view's files as the core reads them beside the launcher
# (capsulectl-deal.d/deal-view.js and .css, the bytes the binary embeds and
# its handshake states). Both archives are deterministic (sorted entries,
# fixed mtime, owner 0:0, gzip -n). It needs GNU tar: set TAR=gtar on macOS.
set -euo pipefail

VERSION="${1:?version, e.g. v0.1.0}"
COMMIT="${2:?full commit sha}"
OUT="${3:?output directory}"
CORE=github.com/action-state-group/capsule-cli
PKG=$CORE/cli
TARGETS=(linux/amd64 linux/arm64 darwin/arm64)

cd "$(dirname "$0")/.."
core_version=$(go list -m -f '{{.Version}}' "$CORE")
core_commit=$(go mod download -json "$CORE@$core_version" | jq -er '.Origin.Hash')

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
ldflags="-s -w -buildid= -X $PKG.CLIVersion=$core_version -X $PKG.CLICommit=$core_commit -X $PKG.PluginVersion=$VERSION -X $PKG.PluginCommit=$COMMIT"
for target in "${TARGETS[@]}"; do
  os=${target%/*} arch=${target#*/}
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "$ldflags" -o "$OUT/capsulectl-deal-$VERSION-$os-$arch" ./cmd/capsulectl-deal
done

# The skill: the tree as committed, in a fixed order with fixed metadata.
git ls-files -z skills/deal | LC_ALL=C sort -z |
  "${TAR:-tar}" --null -T - --format=gnu --owner=0 --group=0 --numeric-owner \
    --mode='u+rw,go=rX' --mtime='1970-01-01 00:00:00Z' -cf - |
  gzip -n -9 >"$OUT/deal-skill-$VERSION.tar.gz"

# The deal view, unpacked into the plugin root beside capsulectl-deal.
view=$(mktemp -d)
trap 'rm -rf "$view"' EXIT
mkdir "$view/capsulectl-deal.d"
cp internal/deal/assets/deal-view.js internal/deal/assets/deal-view.css "$view/capsulectl-deal.d/"
(cd "$view" && printf '%s\0' capsulectl-deal.d capsulectl-deal.d/deal-view.css capsulectl-deal.d/deal-view.js |
  "${TAR:-tar}" --null --no-recursion -T - --format=gnu --owner=0 --group=0 --numeric-owner \
    --mode='u+rw,go=rX' --mtime='1970-01-01 00:00:00Z' -cf -) |
  gzip -n -9 >"$OUT/deal-view-$VERSION.tar.gz"

cd "$OUT"
if command -v sha256sum >/dev/null; then
  sha256sum capsulectl-deal-"$VERSION"-* deal-skill-"$VERSION".tar.gz deal-view-"$VERSION".tar.gz > SHA256SUMS
else
  shasum -a 256 capsulectl-deal-"$VERSION"-* deal-skill-"$VERSION".tar.gz deal-view-"$VERSION".tar.gz > SHA256SUMS
fi
cat SHA256SUMS
