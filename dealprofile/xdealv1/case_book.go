// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/action-state-group/evidencebook"
)

// CaseLogPrefix begins the log id of an adjudicator's case book.
const CaseLogPrefix = "case/"

// caseLogTag separates the case log id's hash from any other SHA-256 over
// the same inputs.
const caseLogTag = Profile + "/case-log"

// CaseLogID is the log id of the case book in which the adjudicator whose key
// id is adjudicatorKeyID seals its rulings of the deal whose agreements cite
// the offer with record digest offerDigest (PROFILE.md §4, "The case book").
// It is one book per deal and adjudicator, so both parties compute the same id
// and refuse a ruling on any other log: a second, different ruling at the same
// place is then a fork of this one log, which the status reads
// (conflicting_final_rulings). The id is CaseLogPrefix and the first 16 bytes,
// lowercase hex, of SHA-256 over the tag, the offer digest and the key id,
// each followed by a zero byte. It does not contain the deal id, and an offer
// is of one deal only (CheckAgreement), so two deals never share one.
func CaseLogID(offerDigest, adjudicatorKeyID string) string {
	h := sha256.New()
	for _, part := range []string{caseLogTag, offerDigest, adjudicatorKeyID} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return CaseLogPrefix + hex.EncodeToString(h.Sum(nil)[:16])
}

// HeldRuling is the ruling of the deal dealID whose record digest is digest,
// sealed by adjudicatorKey, among the records the holder holds for that deal:
// a ruling a statement names (StatementBody.Names) is found by the digest the
// statement holds. found false means the ruling is unavailable to this
// holder: it was never received, or it was deleted under retention. A reader
// shows that as "ruling unavailable", never as no ruling.
func HeldRuling(ctx context.Context, shelf *evidencebook.Shelf, dealID, adjudicatorKey, digest string) (read BookRead, found bool, err error) {
	held, err := shelf.HeldByDeal(ctx, dealID)
	if err != nil {
		return BookRead{}, false, err
	}
	for _, h := range held {
		if h.SenderKey != adjudicatorKey || !strings.HasPrefix(h.LogID, CaseLogPrefix) {
			continue
		}
		b, err := VerifyBook(h.Record)
		if err != nil || b.Record.Block.RecordType != TypeRuling || b.Record.Block.DealID != dealID {
			continue
		}
		if d, err := Digest(b.Record); err == nil && d == digest {
			return b, true, nil
		}
	}
	return BookRead{}, false, nil
}
