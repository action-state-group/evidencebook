### Added: `capsulectl book bundle-diff` renders one history at two disclosure levels (#403)

- `capsulectl book bundle-diff --higher A.json --lower B.json [--checkpoint-key HEX]` reads two held bundles of the same log and lists the records and payloads the lower one drops (`DiffBundles`). It reads files only: no profile, nothing is sealed.
- It refuses a bundle that does not verify, bundles of different logs or signed under different checkpoint keys, a `--checkpoint-key` that did not sign them, and a `--lower` that discloses something `--higher` does not (it is then not a lower level).
- Only records and payloads are compared: an interval-coverage leaf whose header a bundle does not carry is not, and the verb does not detect a fork.
