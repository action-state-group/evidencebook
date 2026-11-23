<!-- changelog: rust/evidencebook/CHANGELOG.md -->
A date inside a check's text ("2 nights from 2026-12-10") is no longer refused as a raw phone number: a phone-shaped run that is only ISO dates set apart by white space passes the personal-data scan, and a real number beside a date, or a date glued to digits or phone punctuation, is still refused (evidencebook#791).
