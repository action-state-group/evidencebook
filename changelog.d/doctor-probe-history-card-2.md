### Changed: `Book.Doctor`'s probe asks `history_card/2` (evidencebook#1011)

- With `DoctorOptions.Probe`, `Doctor` asks the node's own responder for checkpoints, then
  `history_card/2` (the sampled card), and verifies the card as a requester does; it no longer asks
  `history_card/1`, which Steven's simplification ruling retires as an asked form. The
  `probe_history_card` finding keeps its name and names the form it asked. `Respond` still answers
  `history_card/1`.
- The doctor's `checkpoint`, `stranger_view` and `unsupported` findings, the disclosure policy's
  settings rows and the guide now list `history_card/2` among what a stranger is answered, as
  `Respond` already did.
