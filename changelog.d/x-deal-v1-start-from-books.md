- **`xdealv1.StartFromBooks`** (evidencebook#461 step 5, PR 3b) derives `Start`'s input from the two deal
  books and computes it. It reads the holder's own book, the counted prefix of the counterparty's book,
  and the offer, each record with its record id and the times witnesses gave it. The offerer's agreement
  has a time only from a witness the agreements name. A record of the acceptor's book names the offerer's
  agreement when its `seen` is that agreement or a later record of the offerer's book; a cancellation
  named before that means not started. With no layer, the holder's first act starts the deal.
  `testdata/deal-v1/start/books.json` holds 21 book-level cases, including a time from an unnamed
  witness and an empty witness list. Reading the books from a shelf comes with the ReceiveProfile (PR 4).
