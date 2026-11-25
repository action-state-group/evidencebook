### Fixed: `capsulectl/scripts/release-build.sh` checks for GNU tar before it builds (#895)

- The archives need GNU tar. The script now uses `TAR` when it is set, then `tar` if that is GNU tar, then `gtar`. If `TAR` names a tar that is not GNU tar, or neither `tar` nor `gtar` is GNU tar, it refuses before the build and says what to do (macOS: `brew install gnu-tar`, and unset `TAR` or set `TAR=gtar`). Before, macOS's tar failed at the first archive, after the launchers were built, unless `TAR=gtar` was set, which was mentioned only in a comment.
