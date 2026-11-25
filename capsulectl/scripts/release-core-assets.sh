#!/usr/bin/env bash
# release-core-assets.sh VERSION DIST -- add the capsulectl core to the one
# release (evidencebook#895 item 4).
#
# A person installs one capsulectl release: the core and the book and deal
# plugins, one version number, one SHA256SUMS. The core is not rebuilt here.
# Its published binaries are taken from the capsule-cli release that
# capsulectl/go.mod requires, checked, and carried unchanged, so their digests
# are the ones capsule-cli published and its own attestation still verifies
# them. The launchers stay a plugin; nothing is compiled into the core.
#
# VERSION is the release's version (the launchers in DIST are named with it);
# release.yml passes the tag's, which it has already checked equals the core's.
# DIST holds release-build.sh's output. The script:
#   1. downloads the core release into a folder of its own and checks it
#      against the core's SHA256SUMS (the core's list never touches DIST's);
#   2. verifies each binary's build attestation with the core's bundle,
#      bound to capsule-cli's release workflow, the tag and the commit go.mod
#      resolves;
#   3. runs the binary for this machine: it must report that version and commit,
#      and install both launchers in DIST with no version mismatch;
#   4. copies the three binaries and the core's bundle into DIST and adds their
#      lines to DIST/SHA256SUMS.
# Run it after the plugins' own attestation, whose subjects name only the
# plugins' assets. Needs gh (GH_TOKEN for the download) and jq.
set -euo pipefail

VERSION="${1:?release version, e.g. v0.1.0}"
DIST="${2:?release-build.sh output directory}"
CORE=github.com/action-state-group/capsule-cli
REPO=action-state-group/capsule-cli
TARGETS=(linux-amd64 linux-arm64 darwin-arm64)
sha256() { if command -v sha256sum > /dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

cd "$(dirname "$0")/.."
DIST=$(cd "$DIST" && pwd)
core_version=$(go list -m -f '{{.Version}}' "$CORE")
core_commit=$(go mod download -json "$CORE@$core_version" | jq -er '.Origin.Hash')
test -n "$core_commit" || { echo "no commit for $CORE@$core_version" >&2; exit 1; }

draft=$(gh release view "$core_version" --repo "$REPO" --json isDraft --jq .isDraft) \
  || { echo "capsule-cli has no release $core_version" >&2; exit 1; }
test "$draft" = false || { echo "capsule-cli $core_version is not published" >&2; exit 1; }

core_dir=$(mktemp -d)
trap 'rm -rf "$core_dir"' EXIT
bundle="capsulectl-$core_version.sigstore.json"
gh release download "$core_version" --repo "$REPO" --dir "$core_dir" \
  --pattern SHA256SUMS --pattern "$bundle" --pattern "capsulectl-$core_version-*"

for target in "${TARGETS[@]}"; do
  test -f "$core_dir/capsulectl-$core_version-$target" \
    || { echo "capsule-cli $core_version has no capsulectl-$core_version-$target" >&2; exit 1; }
done
test -f "$core_dir/$bundle" || { echo "capsule-cli $core_version has no $bundle" >&2; exit 1; }
(cd "$core_dir" && sha256 -c SHA256SUMS) \
  || { echo "capsule-cli $core_version's files do not match its SHA256SUMS" >&2; exit 1; }

for target in "${TARGETS[@]}"; do
  gh attestation verify "$core_dir/capsulectl-$core_version-$target" \
    --bundle "$core_dir/$bundle" --repo "$REPO" \
    --signer-workflow "$REPO/.github/workflows/release.yml" \
    --source-ref "refs/tags/$core_version" --source-digest "$core_commit" > /dev/null \
    || { echo "capsulectl-$core_version-$target: no build attestation from $REPO at $core_version ($core_commit)" >&2; exit 1; }
done

host="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
core_bin="$core_dir/capsulectl-$core_version-$host"
if [ -f "$core_bin" ]; then
  chmod +x "$core_bin"
  got=$("$core_bin" --version)
  want="capsulectl $core_version (commit $core_commit)"
  test "$got" = "$want" || { echo "the core reports \"$got\", not \"$want\"" >&2; exit 1; }
  home=$(mktemp -d)
  for launcher in capsulectl-book capsulectl-deal; do
    HOME="$home" XDG_CONFIG_HOME="$home/.config" XDG_DATA_HOME="$home/.local/share" \
      "$core_bin" plugin install "$DIST/$launcher-$VERSION-$host" > /dev/null \
      || { rm -rf "$home"; echo "the core $core_version refused $launcher-$VERSION-$host" >&2; exit 1; }
  done
  rm -rf "$home"
else
  echo "no core build for $host: the version and install checks ran on none" >&2
  exit 1
fi

for target in "${TARGETS[@]}"; do
  cp "$core_dir/capsulectl-$core_version-$target" "$DIST/"
  chmod 0755 "$DIST/capsulectl-$core_version-$target"
done
cp "$core_dir/$bundle" "$DIST/"
(cd "$DIST" && sha256 "${TARGETS[@]/#/capsulectl-$core_version-}" "$bundle" >> SHA256SUMS)
echo "added capsulectl $core_version ($core_commit) to $DIST"
