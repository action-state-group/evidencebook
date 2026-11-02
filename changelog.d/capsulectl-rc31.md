### Changed: capsulectl-book `v0.1.0-rc31` pairs with capsulectl `v0.1.0-rc31`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc31`; capsulectl-book
  `v0.1.0-rc31` pairs with capsulectl `v0.1.0-rc31` and carries `book report check` without the signing
  secret (#349), `SOURCE_DATE_EPOCH` for scratch books (#330) and `book report build --contract` (#317).
