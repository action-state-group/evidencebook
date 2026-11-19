// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"fmt"
	"time"
)

// When a deal has started (design §5). A deal between two sides with the
// layer has started when the two agreements cite the same offer and rows
// digest; the offerer's agreement names the acceptor's in seen, and the
// acceptor's book names no cancellation of that offer unless it named the
// offerer's agreement first; and the offerer's agreement has a record time
// not after agree_by, or the acceptor's book names it, or has no record time
// at all (then started locally, also after agree_by: the deal is counted, and
// a later time after agree_by makes it not started). Until then the deal's
// rows have no status, no dispute on it counts and no card counts it.

// Start states (StartResult.State).
const (
	StateStarted           = "started"
	StateAwaitingAgreement = "awaiting_agreement"
	StateNotStarted        = "not_started"
)

// What started a deal (StartResult.By).
const (
	ByOffererInTime = "offerer_in_time"
	ByAcceptorNamed = "acceptor_named"
	ByFirstAct      = "first_act"
)

// FirstAct is the holder's first perform or settle record, for a deal whose
// counterparty runs no layer: Time is its record time, or nil when it has
// none yet.
type FirstAct struct {
	Time *string `json:"time"`
}

// StartInput is what Start reads (design §5.2). Every member but At is the
// status value's start member (§8.1), so a reader of facts computes the same
// result. A boolean held as a pointer is null when it does not apply: the
// agreements' match when the counterpart's agreement is not held.
type StartInput struct {
	// At is the time the start is computed at, RFC 3339 UTC to the second.
	At string `json:"at"`
	// Side is the holder's: offerer or acceptor.
	Side string `json:"side"`
	// CounterpartyLayer is the agreement's counterparty_layer.
	CounterpartyLayer bool `json:"counterparty_layer"`
	// AgreeBy is the offer's agree_by.
	AgreeBy string `json:"agree_by"`
	// CounterpartAgreementHeld says the holder holds the other side's
	// agreement for this deal.
	CounterpartAgreementHeld bool `json:"counterpart_agreement_held"`
	// SameOfferAndRows says the two agreements cite the same offer record and
	// rows digest; null when the counterpart's agreement is not held.
	SameOfferAndRows *bool `json:"same_offer_and_rows"`
	// OffererNamesAcceptor says the offerer's agreement names the acceptor's
	// agreement in seen; null when the counterpart's agreement is not held.
	OffererNamesAcceptor *bool `json:"offerer_names_acceptor"`
	// OffererAgreementTime is the offerer's agreement's record time from a
	// witness the agreement names, or null.
	OffererAgreementTime *string `json:"offerer_agreement_time"`
	// AcceptorNamesOfferer says a record of the acceptor's book names the
	// offerer's agreement: its seen is that agreement's record id or a later
	// one of the offerer's book.
	AcceptorNamesOfferer bool `json:"acceptor_names_offerer"`
	// CancellationNamedFirst says the acceptor's book named a cancellation of
	// the offer before it named the offerer's agreement.
	CancellationNamedFirst bool `json:"cancellation_named_first"`
	// FirstAct is the holder's first act with a counterparty that runs no
	// layer; null before it, and when the counterparty runs the layer.
	FirstAct *FirstAct `json:"first_act"`
}

// StartResult is whether the deal has started (design §5.2). Local says the
// result rests on what the holder holds and no witnessed time: another
// reader may compute otherwise until a time or a naming fixes it. By names
// what started it, and is null when nothing has yet: a deal started locally
// on an offerer's agreement with no time yet may still end named by the
// acceptor or not started.
type StartResult struct {
	State string  `json:"state"`
	Local bool    `json:"local"`
	By    *string `json:"by"`
}

func started(local bool, by string) StartResult {
	return StartResult{State: StateStarted, Local: local, By: &by}
}

// Start computes whether a deal has started from the values in, in design
// §5.2's order. It refuses a time that is not RFC 3339 UTC to the second and
// an unknown side. With a counterparty that runs the layer it also refuses:
// agree_by that is not such a time (without the layer agree_by plays no
// part); a held counterpart agreement whose same_offer_and_rows or
// offerer_names_acceptor is null, which would skip a condition of §5.1; and
// an offerer that holds no acceptor's agreement, or one citing another offer
// or rows, since an offerer writes its agreement after the acceptor's it
// holds, so its own records are inconsistent.
func Start(in StartInput) (StartResult, error) {
	at, err := startTime("at", in.At)
	if err != nil {
		return StartResult{}, err
	}
	if in.Side != SideOfferer && in.Side != SideAcceptor {
		return StartResult{}, fmt.Errorf("xdealv1: start: side %q is neither offerer nor acceptor", in.Side)
	}
	notStarted := StartResult{State: StateNotStarted}
	// 1. A counterparty with no layer: the holder's first act starts it.
	if !in.CounterpartyLayer {
		if in.FirstAct == nil {
			return notStarted, nil
		}
		if in.FirstAct.Time != nil {
			if _, err := startTime("first_act.time", *in.FirstAct.Time); err != nil {
				return StartResult{}, err
			}
		}
		return started(in.FirstAct.Time == nil, ByFirstAct), nil
	}
	agreeBy, err := startTime("agree_by", in.AgreeBy)
	if err != nil {
		return StartResult{}, err
	}
	late := at.After(agreeBy)
	awaiting := func() StartResult {
		if late {
			return notStarted
		}
		return StartResult{State: StateAwaitingAgreement}
	}
	// 2. The acceptor named a cancellation first: never, whatever comes later.
	if in.CancellationNamedFirst {
		return notStarted, nil
	}
	if in.CounterpartAgreementHeld && (in.SameOfferAndRows == nil || in.OffererNamesAcceptor == nil) {
		return StartResult{}, fmt.Errorf("xdealv1: start: a held counterpart agreement gives same_offer_and_rows and offerer_names_acceptor")
	}
	// 3. No counterpart agreement, or one about another deal.
	if !in.CounterpartAgreementHeld || in.SameOfferAndRows != nil && !*in.SameOfferAndRows {
		if in.Side == SideOfferer {
			return StartResult{}, fmt.Errorf("xdealv1: start: an offerer writes its agreement after the acceptor's it holds")
		}
		return awaiting(), nil
	}
	// 4. The offerer's agreement does not join the acceptor's.
	if !*in.OffererNamesAcceptor {
		return awaiting(), nil
	}
	// 5. The acceptor's book names the offerer's agreement.
	if in.AcceptorNamesOfferer {
		return started(false, ByAcceptorNamed), nil
	}
	// 6. The offerer's agreement has a time in time.
	if in.OffererAgreementTime != nil {
		t, err := startTime("offerer_agreement_time", *in.OffererAgreementTime)
		if err != nil {
			return StartResult{}, err
		}
		if !t.After(agreeBy) {
			return started(false, ByOffererInTime), nil
		}
		return notStarted, nil
	}
	// 7. No time and never named: started, locally, by nothing yet. Before
	// agree_by that is the offerer still in time; after it the acceptor agreed
	// and the offerer's agreement shows no lateness, so the deal counts (Steven,
	// u608: an agreement then silence is counted), and a witnessed time after
	// agree_by, or a naming, still settles it either way.
	return StartResult{State: StateStarted, Local: true}, nil
}

func startTime(name, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.UTC().Format(time.RFC3339) != s {
		return time.Time{}, fmt.Errorf("xdealv1: start: %s %q is not a UTC time to the second", name, s)
	}
	return t, nil
}
