package xdealv1

import (
	"time"

	"github.com/action-state-group/agent-action-capsule/go/controllerbinding"
)

// NotIndependentSharedOperator is the only label SharedOperator returns. There
// is no positive counterpart: a reader shows this text or nothing, never
// "independent", because a binding can show that two keys share a controller
// and the absence of one shows nothing about independence (#780).
const NotIndependentSharedOperator = "not independent (shared operator)"

// SharedOperator returns NotIndependentSharedOperator when the ruling's
// adjudicator key and either party's key are bound to one controller, and ""
// otherwise. An adjudicator key equal to a party key is the same operator
// with no binding needed.
//
// The adjudicator key is the agreement's adjudicator.key_id; the party keys
// are the ruling's keys.buyer and keys.seller. A binding counts only when it
// is a controller-binding record with claim same_controller_asserted that
// verifies, window and signatures included, at stampedAt and names exactly
// the adjudicator key and one party key.
//
// stampedAt is the ruling's stamped time: the time of the witnessed
// checkpoint that covers the ruling, which the caller takes from the case
// book's stamp. It is never the ruling's own block.at, which is the
// adjudicator's word (PROFILE §2): an adjudicator sharing an operator could
// set it outside the binding's window to escape the label. A zero stampedAt,
// a ruling not yet covered by a stamp, gives "" for a binding; an
// adjudicator key equal to a party key is labeled whatever the time.
//
// revocations are the revocation statements delivered with the bindings;
// judged at stampedAt, one revoked after it does not remove the label. A bad
// signature and any INCONCLUSIVE result give "". It adds no record member and holds no list of
// any operator's keys: the bindings are the proof, read beside the keys the
// ruling and the agreement already carry.
func SharedOperator(ruling Record, agreement AgreeBody, stampedAt time.Time, bindings, revocations [][]byte) string {
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
	if adjudicator == "" {
		return ""
	}
	// One key is one operator at every time: no stamped time is needed.
	if adjudicator == body.Keys.Buyer || adjudicator == body.Keys.Seller {
		return NotIndependentSharedOperator
	}
	if stampedAt.IsZero() {
		return ""
	}
	opts := controllerbinding.Options{Revocations: revocations}
	for _, binding := range bindings {
		report := controllerbinding.Verify(binding, []time.Time{stampedAt}, opts)
		if report.Claim != controllerbinding.ClaimSameController {
			continue
		}
		for _, party := range []string{body.Keys.Buyer, body.Keys.Seller} {
			if report.AppliesTo(adjudicator, party) {
				return NotIndependentSharedOperator
			}
		}
	}
	return ""
}
