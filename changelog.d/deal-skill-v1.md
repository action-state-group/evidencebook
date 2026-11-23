### Changed: the deal skill teaches x-deal-v1 (#626, B6)

- **SKILL.md teaches the x-deal-v1 flow.** A deal between two parties, each with its own book, in order:
  - offer, and `deal receive` on the other side;
  - admission on each side (`deal admit`);
  - the user's intent with the opening;
  - the agreement exchange;
  - the final review (`deal check`), and the user's answer with the card they saw;
  - the payment (`deal settle --sent`, then `--status`) or another act (`deal perform`);
  - statements;
  - reading the deal once a checkpoint covers it.

  Sale, disclosure, cancellation and refund, dispute, later evidence, and what "settled" means without the person's own signature each have their v1 form. The command reference lists the x-deal-v1 verbs.
- **The x-deal-v0 procedures move, unchanged, to `LEGACY-X-DEAL-V0.md`.** It covers finishing an x-deal-v0 deal already open and a counterparty that runs only x-deal-v0. Booking, Signature, Cancel-by date and sale threads are there only.
- **The demo is a two-party x-deal-v1 purchase** (`demo/lamp`, `scripts/run-demo.sh`). It needs no capsule-engine install: its admission decisions are the engine's real output. The x-deal-v0 demo stays as `scripts/run-demo-x-deal-v0.sh`, and the Go tests run both.
- The `--deal` help of `deal check`, `deal note`, `deal report` and `deal settlement` names `deal offer` and `deal receive` as where a deal id comes from.
