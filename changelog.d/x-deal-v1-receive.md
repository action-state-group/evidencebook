- **x-deal-v1's receive profile and the coverage gate** (evidencebook#461 step 5, PR 4b).
  - `xdealv1.BookPair` and `VerifyBook`: a deal-book or case-book record travels as its Capsule with its
    signature and key, the header the Capsule commits, and the record the header commits. The Capsule
    comes from `Book.Sealed`.
  - `xdealv1.ReceiveProfile` implements `evidencebook.ReceiveProfile`:
    - `Read` places a book record at its book log and seq, and a sent record in its own one-record log;
    - `Admit` applies the push rule of design note §8.3;
    - `Answered` says whether our agreement, decline or counter-offer cites a held offer;
    - `ProofSender` relates a stamp proof to the counterparty whose agreement opens the log, refusing a
      fork at position 1 and anything but exactly one candidate.
  - `ReceiveProfile.CanAct` is the gate before a first act of a deal. The counterparty's agreement must be
    of that deal, signed by the key it expects and citing its offer, and counted now. A held stamp proof
    bound to it must cover it: its book id, its checkpoint key, and its shelf log, which is also the
    log the counterparty's evidence commitment names when one is given.
  - A v1 deal book needs no authenticated indexes, so its log holds the deal's records only.
