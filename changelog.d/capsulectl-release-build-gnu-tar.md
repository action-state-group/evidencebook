### Fixed: `capsulectl/scripts/release-build.sh` checks for GNU tar before it builds (#895)

- The archives need GNU tar. The script now uses `TAR` when it is set, then `tar` if that is GNU tar, then `gtar`. If none of them is GNU tar, it refuses before the build and names what to install (macOS: `brew install gnu-tar`). Before, macOS's tar failed partway through, at the deal-view archive, unless `TAR=gtar` was set, which was mentioned only in a comment.
