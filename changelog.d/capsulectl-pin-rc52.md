### Changed: `capsulectl` pins capsule-cli v0.1.0-rc52, and a wording pack that lacks keys is pinned with a notice (capsule-cli#627)

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc52` (was rc51). `profile update --wording` no longer refuses a pack that lacks keys of the core's or of a plugin's built-in pack (an older plugin on a newer core): it pins the pack, prints a `wording_notice`, and the whole built-in pack is used until the pack is updated; `doctor --install-check` adds a `warnings` entry. A pack whose `card.terms.*` entries could hide a term is still refused (evidencebook#834).
