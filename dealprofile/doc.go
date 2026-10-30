// SPDX-License-Identifier: Apache-2.0

// Package dealprofile is the x-deal-v0 deal record profile: its names, the
// keys and fingerprints that keep raw identifiers out of a record, a record's
// payload and digest, the record-level checks a sealed record must pass, the
// sealed deal_report's payload, and the members of a step's Capsule. The
// normative text is x-deal-v0/PROFILE.md beside this package; its schemas,
// fixtures and reference checker (check_profile.py) ship with it.
//
// The package holds no deal logic: what a step says, what it is approved by
// and what a report's lines read are the producer's (the capsulectl deal
// plugin, or a node that seals deals). It seals nothing either: it returns
// the bytes and Capsule members, and the producer seals them with its own
// key, on its own book. It imports neither the root evidencebook package nor
// any CLI, so a reader of deal bundles can use it alone.
//
// It moved here from the deal plugin (evidencebook#93). Every byte it
// produces is the plugin's: testdata/baseline-vectors.json, frozen from the
// plugin's builders before the move, is checked by this package's tests.
package dealprofile
