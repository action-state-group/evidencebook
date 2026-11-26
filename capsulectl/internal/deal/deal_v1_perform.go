// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal perform` and `deal statement` on an x-deal-v1 deal
// (xdealv1-write-path §13, evidencebook#626 B2). A perform is an act: it
// rests on the holder's passing commit check, the first one is gated until
// the counterparty's stamp proof covers its agreement (u610, u611), and its
// authorization (xdealv1.CheckActs) is checked on the finished record. A
// statement says how a row went; it is never gated and cites no check. Both
// are package deal's Store.Perform and Store.Statement (evidencebook#899),
// which seal one record in the holder's deal book; this file reads their
// flags and writes the file that travels to the other side.
//
// The deal book is the source of truth for acts. After the append, an act
// writes v0's act event to the deal's local list, so the next card's spend so
// far and its used approvals read it unchanged; a run that stops in between is
// repaired by reconcileV1Acts on the next command.

// v1ActDeal is what a payer leg reads first: the deal, the holder's book
// (its agreement first) and the offer's body (#899 phase 3 moves the payer
// leg to package deal with the rest).
type v1ActDeal struct {
	d     *v1Deal
	book  []xdealv1.Record
	offer *xdealv1.ProposeBody
}

func (s *dealSession) loadV1ActDeal(ctx context.Context, dealID string) (*v1ActDeal, error) {
	if _, err := s.requireMigrated(ctx); err != nil {
		return nil, err
	}
	if err := s.ensureV1Tables(ctx); err != nil {
		return nil, err
	}
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, cli.InputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
	}
	if d.OwnLogID != "" {
		if err := s.repairIfStopped(ctx, d); err != nil {
			return nil, err
		}
	}
	if d.OwnAgreementID == "" {
		return nil, cli.InputError("deal " + dealID + " has no agreement of yours yet: `deal agree` opens your deal book")
	}
	book, err := s.ownBookV1(ctx, d)
	if err != nil {
		return nil, err
	}
	held, err := s.offerByDigest(ctx, d.OfferDigest)
	if err != nil {
		return nil, err
	}
	if held == nil {
		return nil, cli.InputError("this profile no longer holds the offer of deal " + dealID)
	}
	prop, ok := proposeOf(held.Record)
	if !ok {
		return nil, fmt.Errorf("%w: the offer of deal %s is not an offer", cli.ErrConflict, dealID)
	}
	return &v1ActDeal{d: d, book: book, offer: prop}, nil
}

// parseStatementFlags reads --statement row=value[:remedy]. The value is
// met, not_met or cannot_tell (PROFILE §3.3); the rows and remedies are held
// to the offer by package deal.
func parseStatementFlags(flags []string) ([]xdealv1.StatementItem, error) {
	out := []xdealv1.StatementItem{}
	for _, f := range flags {
		row, rest, ok := strings.Cut(f, "=")
		if !ok || row == "" || rest == "" {
			return nil, cli.InputError("--statement " + f + ": write row=value, or row=value:remedy, where value is met, not_met or cannot_tell")
		}
		value, remedy, _ := strings.Cut(rest, ":")
		switch value {
		case "met", "not_met", "cannot_tell":
		default:
			return nil, cli.InputError("--statement " + f + ": the value is met, not_met or cannot_tell")
		}
		out = append(out, xdealv1.StatementItem{Row: row, Value: value, Remedy: remedy})
	}
	return out, nil
}

// parseStatements reads --statement row=value[:remedy] against the offer's
// rows: a remedy must be one the row allows, and a row is stated once. A
// payer leg still reads its flags here; every other record's go to package
// deal, which holds them to the offer.
func (a *v1ActDeal) parseStatements(flags []string) ([]xdealv1.StatementItem, error) {
	items, err := parseStatementFlags(flags)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, it := range items {
		r, err := a.row(it.Row)
		if err != nil {
			return nil, err
		}
		if it.Remedy != "" && !slices.ContainsFunc(r.Remedies, func(rr xdealv1.RowRemedy) bool { return rr.ID == it.Remedy }) {
			return nil, cli.InputError("--statement " + flags[i] + ": row " + it.Row + " allows no remedy " + it.Remedy)
		}
		if seen[it.Row] {
			return nil, cli.InputError("--statement names row " + it.Row + " twice: one statement per row in a record")
		}
		seen[it.Row] = true
	}
	return items, nil
}

// v1Store readies a session for a deal-book verb: the store in the new
// layout, its tables and the plugin's own.
func (s *dealSession) v1Store(ctx context.Context) (*dealapi.Store, error) {
	if _, err := s.requireMigrated(ctx); err != nil {
		return nil, err
	}
	if err := s.ensureV1Tables(ctx); err != nil {
		return nil, err
	}
	return s.dealStore(ctx)
}

// v1ActSession is v1Store for an act: an act a stopped run appended to the
// holder's book but did not record locally is recorded first, so the act's
// authorization and the next card read every act (xdealv1-write-path §13
// item 8). It returns the deal's local events and the holder's book, both
// empty for a deal the holder has not agreed, which package deal refuses.
func (s *dealSession) v1ActSession(ctx context.Context, dealID string) (*dealapi.Store, []sealedEvent, []xdealv1.Record, error) {
	st, err := s.v1Store(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	d, err := st.Repair(ctx, dealID)
	if err != nil {
		return nil, nil, nil, libErr(err)
	}
	if d == nil || d.OwnAgreementID == "" {
		return st, nil, nil, nil
	}
	book, err := st.OwnBook(ctx, d)
	if err != nil {
		return nil, nil, nil, libErr(err)
	}
	events, err := s.reconcileV1Acts(ctx, dealID, book)
	if err != nil {
		return nil, nil, nil, err
	}
	return st, events, book, nil
}

func (a *v1ActDeal) row(id string) (*xdealv1.Row, error) {
	for i := range a.offer.TermsRows.Rows {
		if a.offer.TermsRows.Rows[i].ID == id {
			return &a.offer.TermsRows.Rows[i], nil
		}
	}
	return nil, cli.InputError("deal " + a.d.DealID + " has no row " + id + ": the rows are the ones its offer names")
}

// parseMarks reads --mark against the offer's named records: a record marks
// only a name the holder's own party writes (the named record's side).
func (a *v1ActDeal) parseMarks(flags []string) ([]string, error) {
	var out []string
	for _, m := range flags {
		n, ok := a.offer.TermsRows.Named[m]
		if !ok {
			return nil, cli.InputError("--mark " + m + ": the offer of deal " + a.d.DealID + " names no record " + m)
		}
		if n.Side != a.d.Role {
			return nil, cli.InputError("--mark " + m + ": the offer says the " + n.Side + " writes it, and you are the " + a.d.Role)
		}
		if slices.Contains(out, m) {
			return nil, cli.InputError("--mark names " + m + " twice")
		}
		out = append(out, m)
	}
	return out, nil
}

// refuseExistingOut refuses an --out that is already there for a new record:
// an old command run again must not seal a record only to fail on its file.
func refuseExistingOut(out string) error {
	if out == "" {
		return nil
	}
	// Lstat, so a dangling link at the path counts as there too.
	if _, err := os.Lstat(out); err == nil {
		return cli.InputError(out + " exists: --out names a new file, and this seals a new record")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// v1Acts are the holder's own acts of a deal, in book order: its perform and
// settle records.
func v1Acts(book []xdealv1.Record) []xdealv1.Record {
	var out []xdealv1.Record
	for _, r := range book {
		if r.Block.RecordType == xdealv1.TypePerform || r.Block.RecordType == xdealv1.TypeSettle {
			out = append(out, r)
		}
	}
	return out
}

// authorizeV1Act runs the library's authorization (x-deal-v1 §8, with
// x-deal-v0's rule 5 on a settled amount) over the deal's journal records and
// its acts, the candidate last, and returns the candidate's.
func (s *dealSession) authorizeV1Act(ctx context.Context, dealID string, earlier []xdealv1.Record, act xdealv1.Record) (*xdealv1.ActAuthorization, error) {
	return s.authorizeV1ActWith(ctx, dealID, earlier, act, nil)
}

// authorizeV1ActWith is authorizeV1Act for an act whose Capsule carries the
// settlement member given, which rule 5 holds to the checked amount. It is
// package deal's Store.Authorize, its refusal told apart as
// notAuthorizedError.
func (s *dealSession) authorizeV1ActWith(ctx context.Context, dealID string, earlier []xdealv1.Record, act xdealv1.Record, member json.RawMessage) (*xdealv1.ActAuthorization, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	auth, err := st.Authorize(ctx, dealID, earlier, act, member)
	var refused *dealapi.NotAuthorizedError
	if errors.As(err, &refused) {
		return nil, notAuthorizedError{refused.Err}
	}
	return auth, err
}

// refuseStaleApproval refuses an act whose authorizing approval deal note
// marked not applied: answered after a later check or a changed detail, or a
// second answer (evidencebook#790). The library reads the sealed approval,
// which says what the user chose; the reason lives on the local event, as
// x-deal-v0's action rule reads it (Approval.Reason == "").
func refuseStaleApproval(events []sealedEvent, auth *xdealv1.ActAuthorization) error {
	if auth == nil {
		return nil
	}
	ad, err := xdealv1.Digest(auth.Approval)
	if err != nil {
		return err
	}
	for _, se := range events {
		if se.Digest == ad && se.Event.Approval != nil && se.Event.Approval.Reason != "" {
			return cli.InputError("the act is not authorized: the user's answer to this check was not applied (" + se.Event.Approval.Reason + "). Run deal check again, show its card, and seal the user's answer to the new check")
		}
	}
	return nil
}

// notAuthorizedError is the library's refusal of an act's authorization, told
// apart from a failure to read the journal.
type notAuthorizedError struct{ err error }

func (e notAuthorizedError) Error() string { return "the act is not authorized: " + e.err.Error() }
func (e notAuthorizedError) Unwrap() error { return cli.InputError(e.Error()) }

// v1EventByDigest is the local event id of the event whose record has digest.
func v1EventByDigest(events []sealedEvent, digest string) string {
	for _, se := range events {
		if se.Digest == digest {
			return se.CapsuleID
		}
	}
	return ""
}

// v1ActEvent is v0's act event for a sealed v1 act: what the next card's spend
// so far and used approvals read (xdealv1-write-path §13 item 9). A perform is
// a commit with no amount.
func v1ActEvent(rec xdealv1.Record, auth *xdealv1.ActAuthorization, events []sealedEvent) (dealEvent, error) {
	ev := dealEvent{Kind: "act"}
	switch b := rec.Body.(type) {
	case *xdealv1.PerformBody:
		ev.Act = &dealAct{Action: "commit", Description: b.Act}
	case xdealv1.PerformBody:
		ev.Act = &dealAct{Action: "commit", Description: b.Act}
	default:
		return dealEvent{}, fmt.Errorf("%w: a %s record has no local act event yet", cli.ErrConflict, rec.Block.RecordType)
	}
	if auth != nil {
		ad, err := xdealv1.Digest(auth.Approval)
		if err != nil {
			return dealEvent{}, err
		}
		ev.Act.AuthorizedBy = v1EventByDigest(events, ad)
		if ev.Act.AuthorizedBy == "" {
			return dealEvent{}, fmt.Errorf("%w: the approval %s that authorized the act has no local step", cli.ErrConflict, ad)
		}
	}
	return ev, nil
}

// storeV1Act writes a sealed act's local event, after the append: its digest
// is the deal-book record's and it has no journal payload. Its time is the
// act's own.
func (s *dealSession) storeV1Act(dealID string, events []sealedEvent, rec xdealv1.Record, digest string, auth *xdealv1.ActAuthorization) (preparedV1, error) {
	ev, err := v1ActEvent(rec, auth, events)
	if err != nil {
		return preparedV1{}, err
	}
	if ev, err = s.fixStep(dealID, events, ev); err != nil {
		return preparedV1{}, err
	}
	ev.At = rec.Block.At
	local, err := json.Marshal(ev)
	if err != nil {
		return preparedV1{}, err
	}
	return preparedV1{se: sealedEvent{CapsuleID: v1EventID(dealID, ev.N), Digest: digest, Sequence: uint64(ev.N), Event: ev}, local: local}, nil
}

// reconcileV1Acts gives every act of the holder's book that has no local event
// one, in book order (xdealv1-write-path §13 item 8): a run that stopped after
// the append and before the local event is finished here, by digest, once.
// A settle record is reconciled by reconcileV1Settle.
func (s *dealSession) reconcileV1Acts(ctx context.Context, dealID string, book []xdealv1.Record) ([]sealedEvent, error) {
	events, err := s.v1Events(ctx, dealID)
	if err != nil {
		return nil, err
	}
	acts := v1Acts(book)
	for i, act := range acts {
		digest, err := xdealv1.Digest(act)
		if err != nil {
			return nil, err
		}
		if v1EventByDigest(events, digest) != "" {
			continue
		}
		if b, ok := settleOf(act); ok {
			if events, err = s.reconcileV1Settle(ctx, dealID, book, events, act, b, digest); err != nil {
				return nil, err
			}
			continue
		}
		auth, err := s.authorizeV1Act(ctx, dealID, acts[:i], act)
		var refused notAuthorizedError
		if errors.As(err, &refused) {
			return nil, fmt.Errorf("%w: deal %s's act %s, sealed by an earlier run, no longer passes its authorization: %v", cli.ErrConflict, dealID, digest, refused.err)
		}
		if err != nil {
			return nil, err
		}
		p, err := s.storeV1Act(dealID, events, act, digest, auth)
		if err != nil {
			return nil, err
		}
		if err := s.storeV1(ctx, dealID, p); err != nil {
			return nil, err
		}
		events = append(events, p.se)
	}
	return events, nil
}

// actResult is what an act or statement prints besides the record: the gate's
// answer when it ran, and the digests the person signs.
func actResult(result map[string]any, gate *xdealv1.ActGate, auth *xdealv1.ActAuthorization) {
	if gate != nil && gate.Gated {
		result["first_act_gate"] = gate
	}
	if auth == nil {
		return
	}
	var signs []string
	if auth.NeedsPersonSignature {
		signs = append(signs, auth.ApprovalDigest)
	}
	signs = append(signs, auth.LimitApprovals...)
	if auth.Intent != "" {
		signs = append(signs, auth.Intent)
	}
	if len(signs) > 0 {
		// x-deal-v1 §8: the act must not be relied on until the person's
		// signatures over these are held; AAC#627 defines where they travel.
		result["person_signs"] = signs
		result["person_signature"] = "missing"
	}
}

const performLong = `Seal a perform record in your deal book of an x-deal-v1 deal: delivery or
another act you did, with what you observed, and write the file to send to
--out. The other side counts it with deal receive.

The output lists the record digests the person must sign with their own key
(person_signature: missing). Until those signatures are held, no one should
rely on this act. The person cannot add them yet: where they travel is still
being defined (agent-action-capsule#627).

The act rests on your passing commit check (--check, from deal check): it is
sealed only when that check's verdict has its approval, that answer was
applied (deal note said "proceed": true: no later check or changed detail
came first), and the approval has authorized no other act. Your first act is refused
until you hold the other side's stamp proof on its agreement (deal receive
brings it); run deal perform again then. If the other side runs no stamping,
the act is sealed and the output says no stamp proof came.

--statement row=value says how a row went (met, not_met or cannot_tell, with
:remedy for a remedy the row allows); --mark names the offer's named record
this act is, so a row whose date hangs on it starts; --remedy-row makes the
act a remedy for that row. Run again with the same flags, it rewrites the
same file and seals nothing new. An --out file that already exists is
refused for a new record.`

func dealPerformCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "perform",
		Short: "Seal an act you did on an x-deal-v1 deal (delivery or another act) and write the file to send",
		Long:  performLong,
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dealID, _ := c.Flags().GetString("deal")
			check, _ := c.Flags().GetString("check")
			act, _ := c.Flags().GetString("act")
			observed, _ := c.Flags().GetString("observed")
			statements, _ := c.Flags().GetStringArray("statement")
			marks, _ := c.Flags().GetStringArray("mark")
			remedyRow, _ := c.Flags().GetString("remedy-row")
			out, _ := c.Flags().GetString("out")
			if !cli.DealIDPattern.MatchString(dealID) {
				return cli.InputError("--deal must be a deal id as printed by `deal offer`: deal- followed by 16 hex characters")
			}
			if !cli.Hex64.MatchString(check) {
				return cli.InputError("--check is required: your passing commit check, by the check_id `deal check` printed (or its record digest)")
			}
			if strings.TrimSpace(act) == "" || strings.TrimSpace(observed) == "" {
				return cli.InputError("--act and --observed are required: what you did (deliver, ship, ...) and how it was observed (carrier_scan, ...)")
			}
			// The output says which signatures of the person the act still
			// waits on, so the file to send goes to --out, never standard
			// output, where the push would be all that is printed.
			if out == "" {
				return cli.InputError("--out is required: the file to send, so the output can say which of the person's signatures the act still waits on")
			}
			v1ReceiveContext(c)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				st, events, _, err := s.v1ActSession(ctx, dealID)
				if err != nil {
					return err
				}
				// --check may be the check_id deal check printed.
				if check, err = s.resolveV1Check(ctx, dealID, check); err != nil {
					return err
				}
				stmts, err := parseStatementFlags(statements)
				if err != nil {
					return err
				}
				res, err := st.Perform(ctx, dealID, dealapi.PerformIn{
					Check: check, Act: act, Observed: observed, Statements: stmts, Marks: marks, RemedyRow: remedyRow,
					Before: func(_ xdealv1.Record, auth *xdealv1.ActAuthorization) error {
						// An old command run again must not seal a record
						// only to fail on its file.
						if err := refuseExistingOut(out); err != nil {
							return err
						}
						return refuseStaleApproval(events, auth)
					},
				})
				if err != nil {
					return libErr(err)
				}
				if !res.Again {
					// The act's local event, after the append: the next
					// card's spend so far and used approvals read it.
					p, err := s.storeV1Act(dealID, events, res.Record, res.Digest, res.Authorization)
					if err != nil {
						return err
					}
					if err := s.storeV1(ctx, dealID, p); err != nil {
						return err
					}
				}
				result := map[string]any{"deal_id": dealID, "record": xdealv1.TypePerform, "digest": res.Digest, "seq": res.Seq}
				actResult(result, res.Gate, res.Authorization)
				return writePush(c, res.Pair, out, result)
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
	cmd.Flags().String("check", "", "Your passing commit check: the check_id deal check printed (or the check record's digest); required")
	cmd.Flags().String("act", "", "What you did, a token such as deliver (required)")
	cmd.Flags().String("observed", "", "How it was observed, a token such as carrier_scan (required)")
	cmd.Flags().StringArray("statement", nil, "row=value[:remedy], how a row went (repeatable)")
	cmd.Flags().StringArray("mark", nil, "A named record of the offer that this act is (repeatable)")
	cmd.Flags().String("remedy-row", "", "Make the act a remedy for this row")
	cmd.Flags().String("out", "", "Where to write the file to send (required)")
	return cmd
}

const statementLong = `Seal a statement record in your deal book of an x-deal-v1 deal: how one or
more rows went, and write the file to send to --out (standard output with
none). The other side counts it with deal receive, and a row's status reads it.

--statement row=value is repeatable: value is met, not_met or cannot_tell,
with :remedy for a remedy the row allows. A statement can be sent at any
time: it does not wait for the other side's stamp proof, and it needs no
check. Run again with the same flags, it rewrites the same file and seals nothing new. An --out file that already
exists is refused for a new record.`

func dealStatementCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "statement",
		Short: "Say how rows of an x-deal-v1 deal went (met, not_met, cannot_tell) and write the file to send",
		Long:  statementLong,
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dealID, _ := c.Flags().GetString("deal")
			statements, _ := c.Flags().GetStringArray("statement")
			out, _ := c.Flags().GetString("out")
			if !cli.DealIDPattern.MatchString(dealID) {
				return cli.InputError("--deal must be a deal id as printed by `deal offer`: deal- followed by 16 hex characters")
			}
			if len(statements) == 0 {
				return cli.InputError("--statement row=value is required at least once")
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				st, err := s.v1Store(ctx)
				if err != nil {
					return err
				}
				stmts, err := parseStatementFlags(statements)
				if err != nil {
					return err
				}
				res, err := st.Statement(ctx, dealID, dealapi.StatementIn{Statements: stmts,
					Before: func(xdealv1.Record, *xdealv1.ActAuthorization) error { return refuseExistingOut(out) }})
				if err != nil {
					return libErr(err)
				}
				return writePush(c, res.Pair, out, map[string]any{"deal_id": dealID, "record": xdealv1.TypeStatement, "digest": res.Digest, "seq": res.Seq})
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
	cmd.Flags().StringArray("statement", nil, "row=value[:remedy], how a row went (repeatable, required)")
	cmd.Flags().String("out", "", "Where to write the file to send (default: standard output)")
	return cmd
}

// writeSealedAt writes again a record the holder's book already holds.
func (s *dealSession) writeSealedAt(ctx context.Context, c *cobra.Command, d *v1Deal, seq int, rec xdealv1.Record, out string, result map[string]any) error {
	pair, err := s.bookPairAt(ctx, d, seq)
	if err != nil {
		return err
	}
	digest, err := xdealv1.Digest(rec)
	if err != nil {
		return err
	}
	result["digest"], result["seq"] = digest, seq
	return writePush(c, pair, out, result)
}
