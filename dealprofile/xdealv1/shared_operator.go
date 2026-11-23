package xdealv1

import (
	"time"

	"github.com/action-state-group/agent-action-capsule/go/controllerbinding"
)

// NotIndependentSharedOperator is the only label SharedOperator returns. There
// is no positive counterpart: a reader shows this text or nothing, never
// "independent", because a binding can show that two keys share a controller
// and the absence of one shows nothing about independence (u120, u534).
const NotIndependentSharedOperator = "not independent (shared operator)"

// SharedOperator returns NotIndependentSharedOperator when the ruling's
// adjudicator key and either party's key are bound to one controller, and ""
// otherwise.
//
// The adjudicator key is the agreement's adjudicator.key_id; the party keys
// are the ruling's keys.buyer and keys.seller. A binding counts only when it
// is a controller-binding record with claim same_controller_asserted that
// verifies, window and signatures included, at the ruling's stamped time
// (block.at) and names exactly the adjudicator key and one party key.
// revocations are the revocation statements delivered with the bindings;
// judged at the ruling's time, one revoked after the ruling does not remove
// the label. A bad signature, a missing or unreadable time and any
// INCONCLUSIVE result give "". The verifier reads no member of the record for
// this and holds no list of any operator's keys: the bindings are the proof.
func SharedOperator(ruling Record, agreement AgreeBody, bindings, revocations [][]byte) string {
	var body RulingBody
	switch b := ruling.Body.(type) {
	case RulingBody:
		body = b
	case *RulingBody:
		if b == nil {
			return ""
		}
		body = *b
	default:
		return ""
	}
	if agreement.Adjudicator.V == nil {
		return ""
	}
	adjudicator := agreement.Adjudicator.V.KeyID
	at, err := time.Parse(time.RFC3339, ruling.Block.At)
	if adjudicator == "" || err != nil {
		return ""
	}
	opts := controllerbinding.Options{Revocations: revocations}
	for _, binding := range bindings {
		report := controllerbinding.Verify(binding, []time.Time{at}, opts)
		if report.Claim != controllerbinding.ClaimSameController {
			continue
		}
		for _, party := range []string{body.Keys.Buyer, body.Keys.Seller} {
			if party != adjudicator && report.AppliesTo(adjudicator, party) {
				return NotIndependentSharedOperator
			}
		}
	}
	return ""
}
