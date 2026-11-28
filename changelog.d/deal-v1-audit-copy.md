- x-deal-v1 report copies take a fourth audience, `audit`, for an outside reader who is not a
  party (evidencebook#1058): the counterparty copy's rows with no witness evidence (no
  witnesses, no referee, every relied-on time `null`), and the offer only when it names no
  `ask`. `xdealv1.VerifyReport` refuses an audit report that carries a witness, a referee or a
  witnessed time; `VerifyReportBundle` accepts audit copies and refuses a full one.
