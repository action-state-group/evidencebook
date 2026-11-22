### Added: `xdealv1.StatusProvider.ReportSnapshot` (#626)

- `ReportSnapshot` returns a v1 report, the book pairs it rests on, and each payment row's payment check, all from one read of the shelf. A host that shows the checks beside the report never shows a check the report did not read.
- Rows whose owed is an amount but whose check was not run are listed with why: `not_recomputed`, `no_offer_pair` or `no_acceptor_key`.
- `ReportWithBook` is unchanged and now calls it.
