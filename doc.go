// SPDX-License-Identifier: Apache-2.0

// Package evidencebook is a reference implementation of the Evidence Layer:
// a local evidence store that records evidence and the typed links between
// records, keeps payload digests committed while payload bytes are resolved
// separately, maintains three distinct classes of index, and answers requests
// for evidence with portable, independently verifiable bundles.
//
// A Book embeds a commitment substrate. The Checkpointed Local Log binding
// (OpenCLL) is the reference; a SCITT receipt-holding binding
// (NewSCITTReceiptSubstrate) shows that conformance does not depend on it.
// The substrate never appears in the Book's API: callers append records and
// ask for bundles, requests, reconciliations and closes, and the book
// composes every proof itself.
//
// Every record is sealed as an Agent Action Capsule whose agent input digest
// commits the record header, so a bundle this package produces is checked by
// the neutral Evidence Bundle verifier with nothing from this package. Over
// the SCITT stub, which keeps no ordered log, that verifier reports interval
// coverage as withheld.
package evidencebook

// Compile-time checks that each reference implementation satisfies its seam.
var (
	_ Substrate          = (*CLLSubstrate)(nil)
	_ Substrate          = (*SCITTReceiptSubstrate)(nil)
	_ Store              = (*FileStore)(nil)
	_ PayloadResolver    = (*PayloadDir)(nil)
	_ OperationalIndex   = (*SQLiteIndex)(nil)
	_ AuthenticatedIndex = (*SortedMerkleIndex)(nil)
	_ DiscoveryIndex     = (*TokenIndex)(nil)
	_ Signer             = (*Ed25519Signer)(nil)
)
