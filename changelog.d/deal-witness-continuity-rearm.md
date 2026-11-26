<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: one continuity 409 from the witness no longer stops a deal profile's time-stamps (evidencebook-skills#358)

- `capsulectl/go.mod` requires capsule-cli ce4147ff (capsule-cli#648). `deal tick` re-arms a checkpoint the witness refused with a continuity 409 once, a minute or more after the refusal, and holds later checkpoints behind it, instead of storing the refusal as final. Before, every later checkpoint of that profile chained on the refused one and was refused too, so nothing more was time-stamped and a History card left out the deals agreed after it.
- Its status reads "pending: the witness refused it as not continuing the checkpoint it last accepted (HTTP 409); it is sent once more at a later capsulectl run, a minute or more after the refusal." A second refusal after that retry still reads as not delivered and is not retried.
- `book window publish` shows a first continuity refusal as `pending`, as the core's own status does, including for the predecessor checkpoint `book window convert` queued, and `failed` only once it is refused again or refused for good (`capsulectl/internal/book/window_cmd.go`).
