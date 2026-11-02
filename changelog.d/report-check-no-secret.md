### Fixed: capsulectl-book `book report check` no longer needs the profile's signing secret (#324)

- `book report check` opens the book read-only: it no longer reads the profile's signing or checkpoint signing key, so a
  profile with no key file, a missing one or a key no `trusted_keys` holds answers `match` or `mismatch`. It needs only
  `trusted_keys` and `checkpoint.trusted_keys`.
