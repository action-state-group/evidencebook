### Added: a deal with a party that keeps no records of its own, on x-deal-v1 (#762)

- **An offer whose `to` is null** is to a party that runs no deal tool, such as a shop. It stays with you, and there is nothing to send. You agree to it yourself with `deal agree`, at once, once your admission decision and intent are sealed.
  - The agreement says the other side runs no layer: `counterparty_layer: false`, with no counterparty key, and `seen` null on every record of your book (x-deal-v1 PROFILE §4.7).
  - Your first payment or act goes ahead without the other side's stamp proof, and is marked so.
  - The deal reads as any other: settlement (the payment check says why it cannot run: there is no key for the other side), report, bundle, `deal list` ("keeps no records of its own") and the Standard History card, which counts the deal from its first act.
- **`deal receive` refuses an offer whose `to` is null,** because no one can answer it.
- **`deal agree` on an offer you received** now refuses one that names another key, or no key, instead of sealing an agreement the other side would refuse.
