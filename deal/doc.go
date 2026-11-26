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
// Once both sides agree, the deal's acts and what each side says of them go
// in the holder's deal book:
//
//   - [Store.Perform] is the design's deal.perform: an act the holder did,
//     resting on its passing commit check, sealed only when x-deal-v1's
//     authorization accepts it over the deal's journal ([Store.Authorize],
//     [Store.DealJournal]) and, for the first act, once the other side's
//     stamp proof covers the holder's agreement;
//   - [Store.Statement] is deal.statement: how rows went;
//   - [Store.Settle] is deal.settle for what the holder received, a payee
//     leg;
//   - [Store.Dispute], [Store.WithdrawDispute] and [Store.Challenge] are
//     deal.dispute: a dispute of a row before the agreement's adjudicator,
//     its withdrawal, and a challenge of a ruling;
//   - [Store.Status] is deal.status: the deal book's status value now.
//
// Each of these called again with the same input seals nothing and returns
// the record already sealed (Acted.Again), so a caller that stopped can
// make the same call again. A caller adds its own check of a record before
// it is sealed with [BeforeSeal].
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
// (deal_v1_admits). Two more tables are created for capsulectl-deal, which
// writes them: each deal's local step list (deal_v1_events), from which
// [Store.DealJournal] reads the journal records a deal's steps bind, and
// each payer leg's send (deal_v1_sends). capsulectl-deal keeps its deals in the same tables, so a
// deal store written by either opens in the other.
//
// Every call can be run again after a failure. A record sent whole is
// written to deal_sent before it is sealed, keyed by the caller's request
// key, so a run that stopped finds its record and never seals it twice; a
// deal book's log id is stored before its first record is appended, and an
// agreement a stopped run appended is read back from the book. These depend
// on the order of the writes, not on a transaction across the shelf and the
// tables, which sit on separate connections.
package deal
