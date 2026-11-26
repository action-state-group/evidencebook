// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// CheckIn is a check and its verdict as the caller evaluated them: the
// check's body (its Nonce is the store's) and the verdict's (its Nonce is
// the store's, its checks ref the store's too). The verdict must carry its
// Rules block, which says which rules evaluated the act. The
// caller's Producer, when set, goes in both records' blocks. LocalValues are
// as for JournalRecord.
type CheckIn struct {
	Check       xdealv1.CheckBody
	Verdict     xdealv1.VerdictBody
	Producer    map[string]any
	LocalValues []string
}

// Checked is the record digests of a sealed check and its verdict: an act
// cites the check, the person's approval answers the verdict.
type Checked struct {
	Check, Verdict string
}

// Check is the design's deal.check: it seals a check and its verdict in the
// deal's journal, as two steps of one SealJournal call, the verdict citing
// the check. It evaluates nothing: the caller evaluated the act against the
// participant's own rules and the deal's terms, and the verdict is the
// caller's own word. Its rules members (which checker, which definition)
// are the caller's claim; this package runs and attests no rules (Sherlock2,
// u233, ops evidence-network/inbox.md:666, 2026-11-25). A verdict with no rules block is refused: under a standing
// intent nothing would record whether the person's caps were evaluated
// (u234, ops evidence-network/inbox.md:669; xdealv1.CheckActs escalates such an act). Under a standing
// intent an act also escalates to the person when the rules did not allow
// the act on their own (xdealv1.RulesUnevaluated, evidencebook#982),
// whatever the verdict's Result says. A finding names its Check exactly when
// a check measured it: one that names none reads as a rule the ruleset only
// declares and never escalates, so a measured finding must keep its Check.
// The person's answer to the verdict is an approval record, sealed with
// SealJournal.
func (s *Store) Check(ctx context.Context, dealID string, in CheckIn) (Checked, error) {
	if in.Verdict.Rules == nil {
		return Checked{}, fieldError("verdict", " carries no rules block: say which rules evaluated the act")
	}
	check, err := bodyMap(in.Check)
	if err != nil {
		return Checked{}, err
	}
	verdict, err := bodyMap(in.Verdict)
	if err != nil {
		return Checked{}, err
	}
	delete(check, "nonce")
	delete(verdict, "nonce")
	block := func(refs ...any) map[string]any {
		b := map[string]any{}
		if in.Producer != nil {
			b["producer"] = in.Producer
		}
		if len(refs) > 0 {
			b["refs"] = refs
		}
		return b
	}
	steps, err := s.SealJournal(ctx, dealID,
		JournalStep{Kind: xdealv1.TypeCheck, Build: func(StepFacts) (StepParts, error) {
			return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeCheck, Block: block(), Body: check, LocalValues: in.LocalValues}}, nil
		}},
		JournalStep{Kind: xdealv1.TypeVerdict, Build: func(f StepFacts) (StepParts, error) {
			// The step before it is the check: its digest is the ref.
			ref := xdealv1.RefTo(f.Prev)
			rel := map[string]any{"rel": "checks", "type": ref.Type, "digest_alg": ref.DigestAlg, "digest": ref.Digest}
			return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeVerdict, Block: block(rel), Body: verdict, LocalValues: in.LocalValues}}, nil
		}},
	)
	if err != nil {
		return Checked{}, err
	}
	return Checked{Check: steps[0].Digest, Verdict: steps[1].Digest}, nil
}

// bodyMap is a body as the JSON object a JournalRecord takes.
func bodyMap(body any) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	return m, dec.Decode(&m)
}
