package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

func dealReceiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "receive FILE",
		Short: "Take a file the other side sent: an offer, a decline, a cancellation, an agreement or a record, with its stamp proof",
		Long: `Take a file the other side sent and keep it in your deal store. FILE holds one
of: an offer, a decline or a cancellation (what 'deal offer' wrote), a deal-book
record such as an agreement, or a push {record, stamp_proof} (what 'deal agree'
writes for the acceptor), or a stamp proof alone, or the other side's copy
(deal report --share counterparty --bundle), whose records for the rows,
rulings included, are each taken in turn. Each record is checked on its
own before it is kept, and a stamp proof against the records you hold.

It says in plain words what it did: which records are kept, whether one is
still missing before a counted one ("counted" means every record before it is
here, so it counts toward the deal), and whether two different records came
for one place (it does not say which is right). It seals nothing and appends
nothing to a book of yours, except that a ruling of one of your deals is entered
in your deal book by a statement naming it. --out then writes that statement as
the file to send to the other side, so the other side holds it and counts your
later records.

For a ruling it also says when it was stamped (stamped_at): the earliest time a
witness your agreement names registered the adjudicator's stamp, from the
receipts the file carries, or "unknown" when none does. When the adjudicator's
key is a party's key, or both are bound to one controller at that time, it says
"independence": "not independent (shared operator)". The bindings checked are
those the file carries and those you give (--binding); revocations count only
when you give them (--revocation). It never says a ruling is independent.

Needs the deal store in the new layout (deal set-version x-deal-v1 --migrate).`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			raw, err := cli.ReadInput(args[0])
			if err != nil {
				return err
			}
			var push v1Push
			var members map[string]json.RawMessage
			if err := json.Unmarshal(raw, &members); err != nil {
				return cli.InputError("the file is not a JSON object: " + err.Error())
			}
			if members["bundle_version"] != nil && members["extensions"] != nil {
				if b, _ := c.Flags().GetStringArray("binding"); len(b) > 0 {
					return cli.InputError("--binding and --revocation go with a ruling file, not a copy")
				}
				if r, _ := c.Flags().GetStringArray("revocation"); len(r) > 0 {
					return cli.InputError("--binding and --revocation go with a ruling file, not a copy")
				}
				if out, _ := c.Flags().GetString("out"); out != "" {
					return cli.InputError("--out goes with a ruling file, not a copy: receive the ruling itself with --out to write the statement naming it")
				}
				// A copy (deal report --share ... --bundle): take the
				// records it carries for the rows, rulings included, so a
				// ruling the other side received reaches you too (PROFILE §5).
				v1ReceiveContext(c)
				return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
					if _, err := s.requireMigrated(ctx); err != nil {
						return err
					}
					if err := s.ensureV1Tables(ctx); err != nil {
						return err
					}
					return s.receiveCopy(ctx, c, raw)
				})
			}
			_, hasProof := members["stamp_proof"]
			_, hasRecord := members["record"]
			bareBookPair := members["payload"] != nil && members["header"] != nil
			if !bareBookPair && (hasProof || (hasRecord && members["agent_input"] == nil)) {
				if err := cli.DecodeJSONAs("the file", raw, &push); err != nil {
					return err
				}
			} else {
				push.Record = raw
			}
			v1ReceiveContext(c)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				return s.receiveV1(ctx, c, push)
			})
		},
	}
	cmd.Flags().String("out", "", "With a ruling: write the statement your book seals to name it, the file to send to the other side")
	cmd.Flags().StringArray("binding", nil, "With a ruling: a controller-binding record to check the adjudicator's key against, such as one its operator publishes (repeatable)")
	cmd.Flags().StringArray("revocation", nil, "With a ruling: a revocation of such a binding (repeatable)")
	return cmd
}

// receivedInfo is what the plugin keeps of a record it verified before the
// store took it.
type receivedInfo struct {
	kind    string
	dealID  string
	sent    *xdealv1.Sent
	book    *xdealv1.BookRead
	payload []byte
	// raw is the file as received; for a sent record it is the pair.
	raw []byte
}

// readReceived tells which form a record file is in and verifies it on its
// own: a sent journal record (an offer, a decline, a cancellation) or a
// deal-book record.
func (s *dealSession) readReceived(ctx context.Context, raw []byte) (*receivedInfo, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, cli.InputError("the record is not a JSON object: " + err.Error())
	}
	switch {
	case members["agent_input"] != nil:
		first, err := xdealv1.VerifySent(raw, nil)
		if err != nil {
			return nil, cli.InputError("the sent record does not verify: " + err.Error())
		}
		// A decline or a cancellation is read against the offer it names when
		// the holder has it: its signer and its digest.
		if ag, ok := agreeOf(first.Record); ok {
			held, err := s.offerByDigest(ctx, ag.Offer.Digest)
			if err != nil {
				return nil, err
			}
			if held != nil {
				if first, err = xdealv1.VerifySent(raw, &xdealv1.OfferContext{Offer: held.Record, Signer: held.Signer}); err != nil {
					return nil, cli.InputError("the sent record does not verify against the offer it answers: " + err.Error())
				}
			}
		}
		return &receivedInfo{kind: first.Record.Block.RecordType, dealID: first.Record.Block.DealID, sent: &first, raw: raw}, nil
	case members["payload"] != nil:
		read, err := xdealv1.VerifyBook(raw)
		if err != nil {
			return nil, cli.InputError("the deal-book record does not verify: " + err.Error())
		}
		return &receivedInfo{kind: read.Record.Block.RecordType, dealID: read.Record.Block.DealID, book: &read}, nil
	}
	return nil, cli.InputError("the file holds no record this profile reads: expected a sent record (agent_input) or a deal-book record (payload)")
}

func (s *dealSession) receiveV1(ctx context.Context, c *cobra.Command, push v1Push) error {
	var info *receivedInfo
	if len(push.Record) > 0 {
		var err error
		if info, err = s.readReceived(ctx, push.Record); err != nil {
			return err
		}
		if err = s.refuseOwnDeal(ctx, info); err != nil {
			return err
		}
		// An offer to a counterparty with no layer (to null) stays with its
		// offerer and is never sent (PROFILE §4.7): no one can answer it.
		if info.sent != nil && info.kind == xdealv1.TypePropose {
			if prop, ok := proposeOf(info.sent.Record); ok && prop.To.V == nil {
				return cli.InputError("this offer names no key (to is null): it is an offer to a party that keeps no signed records of its own, and it stays with whoever made it, so it is not kept: ask them for an offer to your key")
			}
		}
	}
	if info == nil && push.StampProof == nil {
		return cli.InputError("the file holds neither a record nor a stamp proof")
	}
	res, rerr := receiveFromStore(ctx, s.shelf, push.Record, push.StampProof)
	out := map[string]any{}
	if info != nil {
		out["record_type"], out["deal_id"] = info.kind, info.dealID
		// A ruling's record digest is what a statement names it by and what
		// deal dispute --challenge takes.
		if info.book != nil && info.kind == xdealv1.TypeRuling {
			if d, err := xdealv1.Digest(info.book.Record); err == nil {
				out["ruling"] = d
			}
			if rb, ok := info.book.Record.Body.(*xdealv1.RulingBody); ok && rb.Sources != nil {
				out["sources"] = rulingSourcesWords(*rb.Sources)
			}
		}
	}
	if res.Record != nil {
		out["record"] = recordWords(res.Record)
		if res.Record.Stored || res.Record.Duplicate {
			if err := s.noteReceived(ctx, info); err != nil {
				return errors.Join(rerr, err)
			}
		}
	}
	if res.Proof != nil {
		out["stamp_proof"] = proofWords(res.Proof)
	}
	if out["record"] == storeFault || out["stamp_proof"] == storeFault {
		_ = cli.Output(c, out)
		return errors.Join(rerr, fmt.Errorf("%w: %s", cli.ErrConflict, storeFault))
	}
	if rerr != nil {
		out["refused"] = rerr.Error()
		_ = cli.Output(c, out)
		return rerr
	}
	bindingPaths, _ := c.Flags().GetStringArray("binding")
	revocationPaths, _ := c.Flags().GetStringArray("revocation")
	if info != nil && info.kind == xdealv1.TypeRuling && info.book != nil {
		own, err := readJSONFiles("--binding", bindingPaths)
		if err != nil {
			return err
		}
		revocations, err := readJSONFiles("--revocation", revocationPaths)
		if err != nil {
			return err
		}
		if err := s.independence(ctx, info, push, own, revocations, out); err != nil {
			return err
		}
	} else if len(bindingPaths) > 0 || len(revocationPaths) > 0 {
		return cli.InputError("--binding and --revocation go with a ruling: they are checked against its adjudicator's key")
	}
	if path, _ := c.Flags().GetString("out"); path != "" {
		if info == nil || info.kind != xdealv1.TypeRuling {
			return cli.InputError("--out goes with a ruling: it writes the statement that names it")
		}
		if err := s.writeNaming(ctx, c, info, path, out); err != nil {
			return err
		}
	}
	return cli.Output(c, out)
}

// writeNaming writes the statement this holder's book holds naming the
// received ruling, as the file to send to the other side: without it the
// other side holds a gap at that place and counts none of the holder's
// later records.
func (s *dealSession) writeNaming(ctx context.Context, c *cobra.Command, info *receivedInfo, path string, out map[string]any) error {
	ruling, err := xdealv1.Digest(info.book.Record)
	if err != nil {
		return err
	}
	d, err := s.v1DealOf(ctx, info.dealID)
	if err != nil {
		return err
	}
	if d == nil || d.OwnAgreementID == "" {
		return cli.InputError("deal " + info.dealID + " has no book of yours, so the ruling is entered in none")
	}
	book, err := s.ownBookV1(ctx, d)
	if err != nil {
		return err
	}
	for i := len(book) - 1; i >= 0; i-- {
		st, ok := book[i].Body.(*xdealv1.StatementBody)
		if !ok || st.Names == nil || st.Names.Ruling == nil || st.Names.Ruling.Digest != ruling {
			continue
		}
		pair, err := s.bookPairAt(ctx, d, i+1)
		if err != nil {
			return err
		}
		data, err := json.Marshal(v1Push{Record: pair})
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := cli.AtomicFile(path, data, false); err != nil {
			prev, rerr := os.ReadFile(path)
			if rerr != nil || !bytes.Equal(prev, data) {
				return err
			}
		}
		out["naming_statement"], out["file"] = i+1, path
		return nil
	}
	return cli.InputError("your book holds no statement naming this ruling yet; run deal receive on it again")
}

// refuseOwnDeal refuses a record that claims a deal this profile made the
// offer of, from anyone but the other side's own book: an offer for a deal id
// this profile already holds is never taken over.
func (s *dealSession) refuseOwnDeal(ctx context.Context, info *receivedInfo) error {
	if info.sent == nil || info.kind != xdealv1.TypePropose {
		return nil
	}
	d, err := s.v1DealOf(ctx, info.dealID)
	if err != nil {
		return err
	}
	if d != nil {
		held, err := s.offerByDigest(ctx, info.sent.Digest)
		if err != nil {
			return err
		}
		if held == nil || held.Own {
			return cli.InputError("this profile already holds deal " + info.dealID + ": an offer for a deal id it holds is refused")
		}
	}
	return nil
}

// storeFault is what is said when the store returns an outcome that is none of
// the outcomes it defines: a fault, never a quiet "not kept" (Sherlock2, buzz
// 694e4902). The verb exits non-zero on it.
const storeFault = "not kept: the store returned no outcome, which is a fault; report it"

// receiveFromStore is Shelf.Receive; a test replaces it to make the store
// return an outcome with nothing set.
var receiveFromStore = func(ctx context.Context, shelf *evidencebook.Shelf, record []byte, proof *evidencebook.StampProof) (evidencebook.ReceiveResult, error) {
	return shelf.Receive(ctx, record, proof)
}

func recordWords(o *evidencebook.RecordOutcome) string {
	switch {
	case o.Refused != nil:
		return "refused: " + o.Refused.Error()
	case o.Duplicate:
		return "already kept"
	case o.Deleted:
		return "kept once, since deleted under your retention"
	case o.Stored && o.Counted:
		return "kept, and counted"
	case o.Stored && o.AskForBook:
		return "kept, but an earlier record from the other side is missing: ask them to send it again"
	case o.Stored:
		return "kept"
	}
	return storeFault
}

func proofWords(o *evidencebook.ProofOutcome) string {
	switch {
	case o.Refused != nil:
		return "refused: " + o.Refused.Error()
	case o.Duplicate:
		return "already kept"
	case o.Accepted:
		return "kept"
	}
	return storeFault
}

// receiveCopy takes the records a copy's x-deal-v1 extension carries: the
// agreement and every record its rows rely on, each a book pair, in their
// books' order, each through the same receive as a single file. A ruling
// among them reaches this holder as the other side received it, so an adjudicator
// that gave each side another ruling shows as a fork (PROFILE.md §5).
func (s *dealSession) receiveCopy(ctx context.Context, c *cobra.Command, raw []byte) error {
	vb, err := evidencebook.VerifyBundle(raw)
	if err != nil {
		return cli.InputError("the copy does not verify: " + err.Error())
	}
	ext := vb.Extensions[xdealv1.Profile]
	var x xdealv1.ReportBundle
	if ext == nil || json.Unmarshal(ext, &x) != nil {
		return cli.InputError("the bundle is not an x-deal-v1 copy (deal report --share counterparty --bundle)")
	}
	type held struct {
		pair []byte
		read xdealv1.BookRead
	}
	agreement, err := xdealv1.VerifyBook(x.Book.Agreement)
	if err != nil {
		return cli.InputError("the copy's agreement does not verify: " + err.Error())
	}
	// The other side's agreement first, so its book is the one recorded as
	// theirs; then the records its rows rely on, in their books' order.
	pairs := []held{{pair: x.Book.Agreement, read: agreement}}
	var rest []held
	for _, raw := range mapValues(x.Book.ReliedOn) {
		read, err := xdealv1.VerifyBook(raw)
		if err != nil {
			return cli.InputError("a record of the copy does not verify: " + err.Error())
		}
		rest = append(rest, held{pair: raw, read: read})
	}
	slices.SortFunc(rest, func(a, b held) int {
		if c := strings.Compare(a.read.LogID, b.read.LogID); c != 0 {
			return c
		}
		return int(a.read.Seq) - int(b.read.Seq)
	})
	pairs = append(pairs, rest...)
	var out []map[string]any
	var fault, refusedRuling bool
	for _, h := range pairs {
		row := map[string]any{"record_type": h.read.Record.Block.RecordType, "seq": h.read.Seq}
		res, rerr := receiveFromStore(ctx, s.shelf, h.pair, nil)
		isRuling := h.read.Record.Block.RecordType == xdealv1.TypeRuling
		switch {
		case rerr != nil:
			row["refused"] = rerr.Error()
			refusedRuling = refusedRuling || isRuling
		case res.Record != nil:
			row["record"] = recordWords(res.Record)
			fault = fault || row["record"] == storeFault
			// Only the other side's own book is noted as theirs: a ruling of
			// the case book, or a record of this holder's book, is not.
			if (res.Record.Stored || res.Record.Duplicate) && h.read.LogID == agreement.LogID {
				info := &receivedInfo{kind: h.read.Record.Block.RecordType, dealID: h.read.Record.Block.DealID, book: &h.read}
				if err := s.noteReceived(ctx, info); err != nil {
					return err
				}
			}
		}
		if isRuling {
			if d, err := xdealv1.Digest(h.read.Record); err == nil {
				row["ruling"] = d
			}
		}
		out = append(out, row)
	}
	switch {
	case fault:
		_ = cli.Output(c, map[string]any{"records": out})
		return fmt.Errorf("%w: %s", cli.ErrConflict, storeFault)
	case refusedRuling:
		// A ruling the copy carries that this holder refuses is what the
		// copy is for (an adjudicator that gave each side another ruling): it is
		// not a quiet success.
		_ = cli.Output(c, map[string]any{"records": out})
		return fmt.Errorf("%w: a ruling the copy carries was refused; see its row", cli.ErrConflict)
	}
	return cli.Output(c, map[string]any{"records": out})
}

// mapValues are m's values in key order.
func mapValues(m map[string]json.RawMessage) []json.RawMessage {
	keys := slices.Sorted(maps.Keys(m))
	out := make([]json.RawMessage, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}

// noteReceived records what the deal store needs of a record the shelf took
// (Store.NoteReceived): the offer's payload to answer it, the deal's row, the
// other side's book and its agreement.
func (s *dealSession) noteReceived(ctx context.Context, info *receivedInfo) error {
	if info == nil {
		return nil
	}
	st, err := s.dealStore(ctx)
	if err != nil {
		return err
	}
	return libErr(st.NoteReceived(ctx, dealapi.Received{Sent: info.sent, Book: info.book, Pair: info.raw}))
}
