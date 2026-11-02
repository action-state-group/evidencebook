### Changed: Book derives record order from its log (#219)

- `Book` no longer keeps a separate list of record ids beside its log of records; the log is the one source of record order. No behavior change.
