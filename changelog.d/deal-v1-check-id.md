### Fixed: `deal settle` and `deal perform` take the check id `deal check` prints (#626)

On an x-deal-v1 deal, `deal check` prints a `check_id`, but `--check` on `deal settle` and `deal perform` took only the check record's digest, which no command prints. So a person could not pay or act on a v1 deal. `--check` now takes the printed `check_id`, or the digest as before. Found in the hand run before the default flip (#749).
