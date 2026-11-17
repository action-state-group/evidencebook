// SPDX-License-Identifier: Apache-2.0

// Package xdealv1 is the x-deal-v1 deal record profile
// (evidencebook#461, docs/design/461-deal-profile.md): its names, its record
// types and bodies, a record's strict encoding and digest, and the checks a
// single record must pass. The record schema is x-deal-v1/x-deal-v1.schema.json
// beside the v0 profile.
//
// v1 keeps v0's canonicalization, digest, keys and fingerprints
// (dealprofile.DealKey, Fingerprint, CommitText and their frozen labels), and
// reuses v0's body types for the journal overlay and the journey overlay. It
// adds the ten types of design §7: propose, agree (agreement, decline,
// cancellation), perform, settle, statement, dispute, ruling, ask, answer
// and admit. A deal book opens at its agreement; there is no close and no
// results log.
//
// Like dealprofile, the package seals nothing and holds no deal logic: it
// returns bytes and verdicts, and the producer seals them with its own key on
// its own book.
package xdealv1
