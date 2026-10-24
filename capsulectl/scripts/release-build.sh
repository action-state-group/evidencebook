#!/usr/bin/env bash
# release-build.sh VERSION COMMIT OUTDIR -- the one build command for
# capsulectl-book's release assets, used by publish-capsulectl.yml and for
# local rebuilds. VERSION is the module version (v1.2.3), the tag without its
# capsulectl/ prefix.
#
# Reproducible, as capsule-cli's: CGO off, -trimpath, no VCS stamping, empty
# build id, and only the versions injected. The plugin embeds the capsulectl
# core it builds against (capsule-cli#326, design note section 4.2):
# cli.CLIVersion and cli.CLICommit are that core's release and commit, read
# from go.mod, and cli.PluginVersion and cli.PluginCommit are this release's.
# The handshake reports the first as core_version and the second as version.
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
    -ldflags "$ldflags" -o "$OUT/capsulectl-book-$VERSION-$os-$arch" ./cmd/capsulectl-book
done

cd "$OUT"
if command -v sha256sum >/dev/null; then
  sha256sum capsulectl-book-"$VERSION"-* > SHA256SUMS
else
  shasum -a 256 capsulectl-book-"$VERSION"-* > SHA256SUMS
fi
cat SHA256SUMS
