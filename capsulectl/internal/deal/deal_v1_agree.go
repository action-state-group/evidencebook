package deal

import (
	"context"
	"encoding/json"
	"os"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/spf13/cobra"
)

// `deal agree` seals the holder's agreement, which opens its deal book, and
// `deal receive` takes what the other side sent (docs/design/
// xdealv1-write-path.md §4.3, §5, slice 2). The plugin has no transport:
// what leaves is a file, and what comes in is a file.

// v1Push is the file a stamped record travels in: the book record as a pair
// and the stamp proof that covers it (design §7.2). Either part may be absent.
type v1Push struct {
	Record     json.RawMessage          `json:"record,omitempty"`
	StampProof *evidencebook.StampProof `json:"stamp_proof,omitempty"`
	// A ruling's push also carries the adjudicator's witness receipts for
	// the stamp's shelf checkpoint, which time the ruling, and the
	// controller bindings it delivers (#780, #819). Never revocations: those
	// come only from the party that receives it.
	WitnessReceipts []evidencebook.WitnessReceipt `json:"witness_receipts,omitempty"`
	Bindings        []json.RawMessage             `json:"bindings,omitempty"`
}

func dealAgreeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agree OFFER_DIGEST",
		Short: "Agree to an offer: seal your agreement, which opens your deal book, and write the file to send",
		Long: `Agree to an offer you sent or received (OFFER_DIGEST is the offer's record
digest): seal your agreement as the first record of your deal book and write
it to --out (standard output with none). --admit is the digest of your own
admission decision in your journal, which an agreement cites: a GO, or a
LIMIT whose tier holds the deal; an ASK or a NO is refused. ` + "`deal admit`" + `
seals capsule-engine's decision for the deal, and with no --admit the
agreement cites that one. --admit naming an admit ` + "`deal admit`" + ` sealed for
another deal is refused, and so is one other than the deal's own when it has
one. --intent is the
digest of your journal's intent for the task; with none, the agreement cites
the deal's opening intent (deal note --kind intent --opening) when it is
sealed, which on a buyer's deal names the seller by commitment, so seal the
opening before you agree. The
agreement takes the offer's rows, witnesses, adjudicator and forums as they
are.

Each side stamps right after its agreement and the file carries its stamp
proof: neither side's first act is sealed until it holds the other's. The
offerer agrees only once it holds the acceptor's agreement (deal receive),
and you can agree to a received offer only when it names your key.

An offer of yours whose to is null is to a party that keeps no signed records of
its own (a shop, say): you agree to it alone, at once, and your first act
goes ahead without a stamp proof from them, marked so.
Needs the deal store in the new layout (deal set-version x-deal-v1
--migrate).`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			offerDigest := args[0]
			admit, _ := c.Flags().GetString("admit")
			intent, _ := c.Flags().GetString("intent")
			out, _ := c.Flags().GetString("out")
			v1ReceiveContext(c)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				cad, err := s.requireMigrated(ctx)
				if err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				if intent == "" {
					if intent, err = s.openingIntent(ctx, offerDigest); err != nil {
						return err
					}
				}
				ag, err := s.store.Agree(ctx, cad, offerDigest, dealapi.AgreeOptions{Admit: admit, Intent: intent})
				if err != nil {
					return libErr(err)
				}
				return writeAgreement(c, ag, out)
			})
		},
	}
	cmd.Flags().String("admit", "", "The digest of your journal's admission decision for this deal (default: the one `deal admit` sealed for it)")
	cmd.Flags().String("intent", "", "The digest of your journal's intent for the task (default: the deal's opening intent, when sealed)")
	cmd.Flags().String("out", "", "Where to write the file to send (default: standard output)")
	return cmd
}

// openingIntent is the record digest of the opening intent of the deal the
// offer is of, "" when none is sealed yet: the intent an agreement cites by
// default, which names the seller on a buyer's deal (evidencebook#1031). An
// opening sealed after the agreement is not cited, since an agreement is
// sealed once.
func (s *dealSession) openingIntent(ctx context.Context, offerDigest string) (string, error) {
	offer, err := s.offerByDigest(ctx, offerDigest)
	if err != nil || offer == nil {
		// Store.Agree names an offer it does not hold.
		return "", err
	}
	events, err := s.v1StoredEvents(ctx, offer.Record.Block.DealID)
	if err != nil {
		return "", err
	}
	if len(events) < 2 || events[0].Event.Kind != "open" || events[1].Event.Kind != "intent" {
		return "", nil
	}
	return events[1].Digest, nil
}

// writeAgreement writes the holder's agreement as the file to send, with its
// stamp proof, taken right after the agreement (design §7.2).
func writeAgreement(c *cobra.Command, ag dealapi.Agreement, out string) error {
	proof := ag.StampProof
	push := v1Push{Record: ag.Pair, StampProof: &proof}
	data, err := json.Marshal(push)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if out == "" {
		_, err = c.OutOrStdout().Write(data)
		return err
	}
	if err := cli.AtomicFile(out, data, false); err != nil {
		// A file already there is the earlier run's only if it holds the same record.
		prev, rerr := os.ReadFile(out)
		var was v1Push
		if rerr != nil || json.Unmarshal(prev, &was) != nil || string(was.Record) != string(ag.Pair) {
			return err
		}
		// An earlier version wrote the offerer's file with no stamp proof: the
		// same record with no proof is completed with it, so the file the
		// output describes is the file on disk.
		if was.StampProof == nil && push.StampProof != nil {
			if err := cli.AtomicFile(out, data, true); err != nil {
				return err
			}
		}
	}
	res := map[string]any{"deal_id": ag.DealID, "agreement": ag.Digest, "side": ag.Side, "file": out, "stamp_proof": push.StampProof != nil}
	if ag.NoLayer {
		res["note"] = "the other side keeps no signed records of its own: your agreement opens your deal book, the file written is your own copy with no one to send it to, and your first act goes ahead without their stamp proof, marked so"
	}
	return cli.Output(c, res)
}
