// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal perform` and `deal statement` on an x-deal-v1 deal
// (xdealv1-write-path §13, evidencebook#626 B2). A perform is an act: it
// rests on the holder's passing commit check, the first one is gated until
// the counterparty's stamp proof covers its agreement (u610, u611), and the
// library checks its authorization (xdealv1.CheckActs) on the finished record,
// inside the deal-book writer. A statement says how a row went; it is never
// gated and cites no check. Both seal one record in the holder's deal book and
// write the file that travels to the other side, as `deal dispute` does.
//
// The deal book is the source of truth for acts. After the append, an act
// writes v0's act event to the deal's local list, so the next card's spend so
// far and its used approvals read it unchanged; a run that stops in between is
// repaired by reconcileV1Acts on the next command.

// v1ActDeal is what an act or statement on a v1 deal reads first: the deal,
// the holder's book (its agreement first) and the offer's body.
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

// parseStatements reads --statement row=value[:remedy] against the offer's
// rows. The value is met, not_met or cannot_tell (PROFILE §3.3); a remedy
// must be one the row allows.
func (a *v1ActDeal) parseStatements(flags []string) ([]xdealv1.StatementItem, error) {
	out := []xdealv1.StatementItem{}
	seen := map[string]bool{}
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
		r, err := a.row(row)
		if err != nil {
			return nil, err
		}
		if remedy != "" && !slices.ContainsFunc(r.Remedies, func(rr xdealv1.RowRemedy) bool { return rr.ID == remedy }) {
			return nil, cli.InputError("--statement " + f + ": row " + row + " allows no remedy " + remedy)
		}
		if seen[row] {
			return nil, cli.InputError("--statement names row " + row + " twice: one statement per row in a record")
		}
		seen[row] = true
		out = append(out, xdealv1.StatementItem{Row: row, Value: value, Remedy: remedy})
	}
	return out, nil
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

// sealedLike finds the record of the holder's book a re-run of the same
// command already sealed: one of the same type whose body and marks are the
// command's, byte for byte. Its place (seq) is 0 when there is none. With
// restated, a match is a re-run only when no later record of the book (a
// statement, a perform or a settle) states any of the same rows: a row reads
// the side's latest statement (status_rows.go), so repeating an older one
// after another is a new statement. A perform needs no such rule: its check's
// approval authorizes one act only.
func sealedLike(book []xdealv1.Record, recordType string, body xdealv1.Body, marks []string, restated []xdealv1.StatementItem) (int, error) {
	want, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	rows := map[string]bool{}
	for _, it := range restated {
		rows[it.Row] = true
	}
	statedLater := func(i int) bool {
		for _, r := range book[i+1:] {
			for _, it := range statementsOf(r) {
				if rows[it.Row] {
					return true
				}
			}
		}
		return false
	}
	for i, r := range book {
		if r.Block.RecordType != recordType || !slices.Equal(r.Block.Marks, marks) || statedLater(i) {
			continue
		}
		got, err := json.Marshal(r.Body)
		if err != nil {
			return 0, err
		}
		if bytes.Equal(got, want) {
			return i + 1, nil
		}
	}
	return 0, nil
}

// statementsOf are the statements a record of the holder's book carries.
func statementsOf(r xdealv1.Record) []xdealv1.StatementItem {
	switch b := r.Body.(type) {
	case *xdealv1.StatementBody:
		return b.Statements
	case xdealv1.StatementBody:
		return b.Statements
	case *xdealv1.PerformBody:
		return b.Statements
	case xdealv1.PerformBody:
		return b.Statements
	case *xdealv1.SettleBody:
		return b.Statements
	case xdealv1.SettleBody:
		return b.Statements
	}
	return nil
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
// settlement member given, which rule 5 holds to the checked amount.
func (s *dealSession) authorizeV1ActWith(ctx context.Context, dealID string, earlier []xdealv1.Record, act xdealv1.Record, member json.RawMessage) (*xdealv1.ActAuthorization, error) {
	journal, err := s.v1DealJournal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	ins := make([]xdealv1.ActIn, 0, len(earlier)+1)
	for _, r := range earlier {
		ins = append(ins, xdealv1.ActIn{Record: r})
	}
	ins = append(ins, xdealv1.ActIn{Record: act, Settlement: member})
	auths, err := xdealv1.CheckActs(journal, ins)
	if err != nil {
		return nil, notAuthorizedError{err}
	}
	for i := range auths {
		if auths[i].Act.Block.Seq == act.Block.Seq {
			return &auths[i], nil
		}
	}
	return nil, nil
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
sealed only when that check's verdict has its approval, no detail changed
since, and the approval has authorized no other act. Your first act is refused
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
				a, err := s.loadV1ActDeal(ctx, dealID)
				if err != nil {
					return err
				}
				// After the deal is known to be v1: --check may be the
				// check_id deal check printed.
				if check, err = s.resolveV1Check(ctx, dealID, check); err != nil {
					return err
				}
				stmts, err := a.parseStatements(statements)
				if err != nil {
					return err
				}
				mk, err := a.parseMarks(marks)
				if err != nil {
					return err
				}
				ref := xdealv1.RefTo(check)
				body := xdealv1.PerformBody{Act: act, Observed: observed, Statements: stmts, Check: &ref}
				if remedyRow != "" {
					if _, err := a.row(remedyRow); err != nil {
						return err
					}
					body.RemedyFor = &xdealv1.RemedyFor{Row: remedyRow}
				}
				events, err := s.reconcileV1Acts(ctx, dealID, a.book)
				if err != nil {
					return err
				}
				result := map[string]any{"deal_id": dealID, "record": xdealv1.TypePerform}
				if seq, err := sealedLike(a.book, xdealv1.TypePerform, body, mk, nil); err != nil {
					return err
				} else if seq > 0 {
					// A re-run says again which signatures the act waits on.
					rec := a.book[seq-1]
					var before []xdealv1.Record
					for _, r := range v1Acts(a.book) {
						if r.Block.Seq < rec.Block.Seq {
							before = append(before, r)
						}
					}
					auth, err := s.authorizeV1Act(ctx, dealID, before, rec)
					if err != nil {
						return err
					}
					actResult(result, nil, auth)
					return s.writeSealedAt(ctx, c, a.d, seq, rec, out, result)
				}
				if err := refuseExistingOut(out); err != nil {
					return err
				}
				earlier := v1Acts(a.book)
				var gate xdealv1.ActGate
				var auth *xdealv1.ActAuthorization
				// Checked on the finished record, again after a rebuild: the
				// act's own time is the one the gate and the authorization read.
				validate := func(rec xdealv1.Record) error {
					// The counterparty's published evidence commitment is not
					// held by this plugin yet, so its shelf log is "" and the
					// gate skips that comparison (§13 item 1).
					g, err := s.gateFirstAct(ctx, dealID, rec, "")
					if err != nil {
						return err
					}
					if g.Gated && !g.Allowed {
						return cli.InputError("your first act on deal " + dealID + " waits on the other side's stamp proof: " + g.Reason)
					}
					gate = g
					if auth, err = s.authorizeV1Act(ctx, dealID, earlier, rec); err != nil {
						return err
					}
					return refuseStaleApproval(events, auth)
				}
				sealed, err := s.sealBookV1(ctx, dealID, bookRecordIn{Body: body, Marks: mk, Validate: validate})
				if err != nil {
					return err
				}
				p, err := s.storeV1Act(dealID, events, sealed.Record, sealed.Digest, auth)
				if err != nil {
					return err
				}
				if err := s.storeV1(ctx, dealID, p); err != nil {
					return err
				}
				result["digest"], result["seq"] = sealed.Digest, sealed.Seq
				actResult(result, &gate, auth)
				return writePush(c, sealed.Pair, out, result)
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
				a, err := s.loadV1ActDeal(ctx, dealID)
				if err != nil {
					return err
				}
				stmts, err := a.parseStatements(statements)
				if err != nil {
					return err
				}
				body := xdealv1.StatementBody{Statements: stmts}
				result := map[string]any{"deal_id": dealID, "record": xdealv1.TypeStatement}
				if seq, err := sealedLike(a.book, xdealv1.TypeStatement, body, nil, stmts); err != nil {
					return err
				} else if seq > 0 {
					return s.writeSealedAt(ctx, c, a.d, seq, a.book[seq-1], out, result)
				}
				if err := refuseExistingOut(out); err != nil {
					return err
				}
				sealed, err := s.sealBookV1(ctx, dealID, bookRecordIn{Body: body})
				if err != nil {
					return err
				}
				result["digest"], result["seq"] = sealed.Digest, sealed.Seq
				return writePush(c, sealed.Pair, out, result)
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
