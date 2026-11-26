// SPDX-License-Identifier: Apache-2.0

package deal

import "github.com/action-state-group/evidencebook/dealprofile/xdealv1"

// openDisputeOn finds the holder's own dispute on a row that no later
// withdrawal of the same row has taken back, as package deal reads the book
// for a dispute: its place in the book (seq) and body. ok is false when none is open. last is the latest dispute-type
// record on the row (a dispute or a withdrawal), seq 0 when there is none.
func openDisputeOn(book []xdealv1.Record, row string) (seq int, open bool, last *xdealv1.DisputeBody, lastSeq int) {
	for i, r := range book {
		b, ok := disputeOf(r)
		if !ok || b.Row != row {
			continue
		}
		last, lastSeq = b, i+1
		switch b.Kind {
		case xdealv1.KindDispute:
			seq, open = i+1, true
		case xdealv1.KindWithdrawal:
			seq, open = 0, false
		}
	}
	return seq, open, last, lastSeq
}
