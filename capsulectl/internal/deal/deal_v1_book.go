package deal

import (
	"context"
	"errors"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// The x-deal-v1 deal book and the records that come from the other side
// (docs/design/xdealv1-write-path.md, slice 2). A v1 deal book opens at the
// holder's agreement and holds the deal's records only; its log id is random,
// never derived from the deal id (§3). Its writer and the tables that say
// which deal is which are evidencebook's package deal (deal_v1_store.go);
// this file holds the context that opens the shelf's store of received
// records for the verbs that need it, and the first-act gate.

// v1ReceiveKey marks a context whose session opens the shelf with the x-deal-v1
// receive profile: `deal receive`, `deal agree` and the first-act gate read the
// store of received records. No other verb opens it.
type v1ReceiveKey struct{}

func v1ReceiveContext(c *cobra.Command) {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	c.SetContext(context.WithValue(ctx, v1ReceiveKey{}, true))
}

// gateFirstAct applies Steven's first-act gate (u610, u611; evidencebook
// GateAct) to an act the holder is about to seal in its deal book: a first
// perform or payer settle is refused until the counterparty's stamp proof
// covers its agreement, on both sides; a counterparty with no layer is not
// refused, and the answer says so (Unguarded), for the receipt to state. A
// payee leg, a statement and every later act are never gated. The act is the
// record the writer will seal, with its body and type; commitmentShelfLog is
// the shelf log the counterparty's published evidence commitment names, passed
// whenever the holder holds that commitment and "" when it holds none.
func (s *dealSession) gateFirstAct(ctx context.Context, dealID string, act xdealv1.Record, commitmentShelfLog string) (xdealv1.ActGate, error) {
	if s.v1Profile == nil {
		return xdealv1.ActGate{}, errors.New("the first-act gate needs the shelf opened for x-deal-v1 records (v1ReceiveContext)")
	}
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return xdealv1.ActGate{}, err
	}
	if d == nil || d.OwnAgreementID == "" {
		return xdealv1.ActGate{}, cli.InputError("deal " + dealID + " has no agreement of yours yet: `deal agree` opens your deal book")
	}
	own, err := s.ownBookV1(ctx, d)
	if err != nil {
		return xdealv1.ActGate{}, err
	}
	act.Block.DealID = dealID
	return s.v1Profile.GateAct(ctx, xdealv1.ActGateInput{Own: own, Act: act, CounterpartAgreement: d.CounterpartyAgreement, CommitmentShelfLog: commitmentShelfLog})
}
