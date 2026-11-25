<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Changed: `deal::v1::start` reads an offerer that counts no acceptor agreement as awaiting (#878)

- An offerer that holds no acceptor agreement it counts now reads as awaiting before agree_by and not started after, as an acceptor does, instead of an error (Steven's ruling u217: the acceptor forked at its first record and the offerer names neither branch). One holding an acceptor agreement on another offer or rows is still refused. The shared vectors `offerer-holds-none` in `testdata/deal-v1/start/vectors.json` cover it.
