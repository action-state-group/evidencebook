// SPDX-License-Identifier: Apache-2.0

// Package deal is the Deal API over an evidence-book shelf: the calls that
// seal an x-deal-v1 deal's records (evidencebook#899; the Integrated Design's
// section 5, "The Deal API"). dealprofile/xdealv1 holds the profile's rules,
// its record types and checks; this package seals records under them, on the
// holder's own shelf and key, and keeps the local state a deal needs between
// calls.
//
// The design's deal.open is four calls here, one per record, since each takes
// different inputs:
//
//   - [Store.Offer] seals an offer or a counter-offer in the journal, to be
//     sent whole;
//   - [Store.CancelOffer] seals the offerer's cancellation of its own offer;
//   - [Store.DeclineOffer] seals the addressee's decline of an offer;
//   - [Store.Agree] seals the agreement, the first record of a new deal book,
//     and returns it with the stamp proof the other side's first act waits on.
//
// [Store.Admit] seals the result of book.admit, capsule-engine's admission
// decision, which an agreement cites; it evaluates nothing.
// [Store.NoteReceived] records what [evidencebook.Shelf.Receive] kept of the
// other side's records, so the next call knows the deal.
//
// # The store
//
// A Store keeps its state in tables of the shelf's own SQLite file, next to
// the books: which deals it holds and in what state (deal_versions, deal_v1),
// the offers it received (deal_v1_offers), each record sent whole
// (deal_sent) and each admission decision bound to its deal
// (deal_v1_admits). capsulectl-deal keeps its deals in the same tables, so a
// store either writes opens in the other.
//
// Every call can be run again after a failure. A record sent whole is
// written to deal_sent before it is sealed, keyed by the caller's request
// key, so a run that stopped finds its record and never seals it twice; a
// deal book's log id is stored before its first record is appended, and an
// agreement a stopped run appended is read back from the book. These depend
// on the order of the writes, not on a transaction across the shelf and the
// tables, which sit on separate connections.
package deal
