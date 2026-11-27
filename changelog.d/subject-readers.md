### Added: `SubjectsOfRecord`, the subject a verified bundle record is a member of (#116)

- `SubjectsOfRecord(records, id)` returns the ids of the subjects that the record `id` of a verified bundle (`VerifiedBundle.Records`) is a member of. It reads only verified headers, uses the book's own membership rule, and needs the subject's `subject_root` in the bundle: a record of the same book with a lower `seq`. The result is empty for a record that is no member. `report/` uses it to give each claim of a Result its subject (design note section 4, Readers).
