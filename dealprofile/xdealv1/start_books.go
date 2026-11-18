// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"fmt"
	"slices"
)

// BookRecord is one record of a deal book as the holder holds it: the
// record, its record id (the Capsule id of the sealed book record), and the
// registration time each witness gave a shelf checkpoint covering it, keyed
// by the witness's key id (the shelf's RecordTimeByKey and
// ReceivedRecordTimeByKey give these, one key at a time).
type BookRecord struct {
	Record   Record            `json:"record"`
	RecordID string            `json:"record_id"`
	Times    map[string]string `json:"times,omitempty"`
}

// StartBooks is what StartFromBooks reads: the holder's own deal book, the
// counted prefix of the counterparty's deal book the holder holds, and the
// offer both agreements cite. Each book is in order from its agreement.
type StartBooks struct {
	At          string       `json:"at"`
	Offer       Record       `json:"offer"`
	Own         []BookRecord `json:"own"`
	Counterpart []BookRecord `json:"counterpart"`
}

// StartFromBooks derives Start's input from the two books (design §5.2) and
// computes it. The offerer's agreement's record time is the earliest time a
// witness the agreements name gave it; a time from any other witness is no
// time (§5.2, "offerer_agreement_time"). It refuses books out of order, of
// another deal, a holder's book not opened by an agreement citing the offer,
// and a counterpart's book not opened by an agreement.
//
// Reading the books from a shelf, the holder's own book records and the
// counterparty's received records, is the profile's ReceiveProfile's
// (design §8.3), which knows how a received record is read.
func StartFromBooks(b StartBooks) (StartInput, StartResult, error) {
	in, err := startInput(b)
	if err != nil {
		return StartInput{}, StartResult{}, err
	}
	res, err := Start(in)
	return in, res, err
}

func startInput(b StartBooks) (StartInput, error) {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("xdealv1: start from books: "+format, args...)
	}
	of, ok := asPropose(b.Offer.Body)
	if !ok {
		return StartInput{}, bad("the offer is not a propose but a %s record", b.Offer.Block.RecordType)
	}
	offerDigest, err := Digest(b.Offer)
	if err != nil {
		return StartInput{}, err
	}
	for name, book := range map[string][]BookRecord{"own": b.Own, "counterpart": b.Counterpart} {
		for i, r := range book {
			if r.Record.Block.Seq != int64(i+1) {
				return StartInput{}, bad("the %s book's record %d has seq %d", name, i+1, r.Record.Block.Seq)
			}
			if r.Record.Block.DealID != b.Offer.Block.DealID {
				return StartInput{}, bad("the %s book's record %d is of another deal", name, i+1)
			}
		}
	}
	if len(b.Own) == 0 {
		return StartInput{}, bad("the holder's book is empty")
	}
	own, ok := asAgree(b.Own[0].Record.Body)
	if !ok || own.Kind != KindAgreement {
		return StartInput{}, bad("the holder's book does not open with its agreement")
	}
	if own.Offer.Digest != offerDigest {
		return StartInput{}, bad("the holder's agreement does not cite the offer")
	}
	in := StartInput{At: b.At, Side: own.Side, AgreeBy: of.AgreeBy,
		CounterpartyLayer: own.CounterpartyLayer != nil && *own.CounterpartyLayer}
	if !in.CounterpartyLayer {
		for _, r := range b.Own {
			if _, _, isAct := actCheck(r.Record); isAct {
				t, err := recordTime(r, own.Witnesses)
				if err != nil {
					return StartInput{}, err
				}
				in.FirstAct = &FirstAct{Time: t}
				break
			}
		}
		return in, nil
	}
	offererBook, acceptorBook := b.Own, b.Counterpart
	if own.Side == SideAcceptor {
		offererBook, acceptorBook = b.Counterpart, b.Own
	}
	// The acceptor's book names the offerer's agreement when a record's seen
	// is that agreement's record id or a later one of the offerer's book; a
	// cancellation it named before that settles the deal as never started.
	// An acceptor reads its own book for both even before it holds any of
	// the offerer's: a cancellation it named then still counts.
	offererIDs := make([]string, len(offererBook))
	for i, r := range offererBook {
		offererIDs[i] = r.RecordID
	}
	for _, r := range acceptorBook {
		if st, ok := bodyOf[StatementBody](r.Record.Body); ok && st.Names != nil && st.Names.Cancellation != nil && !in.AcceptorNamesOfferer {
			in.CancellationNamedFirst = true
		}
		if s := r.Record.Block.Seen; s.V != nil && slices.Contains(offererIDs, *s.V) {
			in.AcceptorNamesOfferer = true
		}
	}
	if len(b.Counterpart) == 0 {
		return in, nil
	}
	cp, ok := asAgree(b.Counterpart[0].Record.Body)
	if !ok || cp.Kind != KindAgreement {
		return StartInput{}, bad("the counterpart's book does not open with its agreement")
	}
	in.CounterpartAgreementHeld = true
	same := cp.Offer.Digest == offerDigest && cp.RowsDigest == own.RowsDigest
	in.SameOfferAndRows = &same
	seen := offererBook[0].Record.Block.Seen
	names := seen.V != nil && *seen.V == acceptorBook[0].RecordID
	in.OffererNamesAcceptor = &names
	// The offerer's agreement's time comes from the witnesses it names.
	offerer, _ := asAgree(offererBook[0].Record.Body)
	if in.OffererAgreementTime, err = recordTime(offererBook[0], offerer.Witnesses); err != nil {
		return StartInput{}, err
	}
	return in, nil
}

// recordTime is a record's record time from the witnesses named: the
// earliest time one of them gave it, or nil for none. Times are UTC to the
// second, so their text orders as their instants do.
// A time from a witness not named is no time, and is not read.
func recordTime(r BookRecord, witnesses []Witness) (*string, error) {
	var earliest *string
	for _, w := range witnesses {
		t, ok := r.Times[w.KeyID]
		if !ok {
			continue
		}
		if _, err := startTime("the time witness "+w.KeyID+" gave record "+r.RecordID, t); err != nil {
			return nil, err
		}
		if earliest == nil || t < *earliest {
			earliest = &t
		}
	}
	return earliest, nil
}
