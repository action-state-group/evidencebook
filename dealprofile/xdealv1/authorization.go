// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"fmt"
	"slices"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile"
)

// Authorization (design §3.2, invariant D): an act is a deal-book perform or
// settle that cites, by digest, the passing check in the holder's journal it
// rests on. v0's authorization conditions hold unchanged (x-deal-v0
// PROFILE.md §6, rules 5 and 6), with that citation in place of v0's
// authorized_by: the check has exactly one verdict; the verdict's first
// approval proceeds; a standing intent answers only a passing verdict, for an
// action in the allowed in force; one approval authorizes at most one act;
// and a change of identifiers between the check and the act needs a new
// check.

// StageAuthorization is the authorization of acts (CheckAuthorizations).
const StageAuthorization = "authorization"

// ActAuthorization is what authorizes one act.
type ActAuthorization struct {
	Act, Check, Verdict, Approval Record
	// ApprovalDigest is the approval's record digest, SHA-256 over its JCS
	// bytes. It covers the approval's approves ref (the verdict's digest,
	// which covers its checks ref, the check's digest) and the approval's
	// card_commitment, which equals the verdict's: a signature over it binds
	// the check and the card the person answered. A person-key signature
	// over this digest (Steven db13b75b; its member is AAC#627's) travels
	// beside the approval record, never inside it, so adding it changes no
	// record format.
	ApprovalDigest string
	// NeedsPersonSignature says the approval is the person's own (approver
	// "user"). The approval record's Capsule is signed by the holder's record
	// key, which is the agent's: that signature never stands for the
	// person's, so a caller that requires the person's signature refuses the
	// act until one over ApprovalDigest, from the person's own key, is held.
	NeedsPersonSignature bool
}

// CheckAuthorizations checks every act that cites a check. journal is the
// holder's journal records of the deal's task, in journal order; acts are
// its deal-book perform and settle records, in book order. A journal record
// is between a check and an act when it comes after the check in the journal
// and its at is not after the act's. It returns one ActAuthorization per act
// that cites a check, in order; an act that cites none is not gated (a payer
// settle always cites one, which the schema requires).
func CheckAuthorizations(journal, acts []Record) ([]ActAuthorization, error) {
	bad := func(i int, format string, args ...any) error {
		return stageErr(StageAuthorization, "act %d: %s", i+1, fmt.Sprintf(format, args...))
	}
	digests := make([]string, len(journal))
	for i, r := range journal {
		d, err := Digest(r)
		if err != nil {
			return nil, err
		}
		digests[i] = d
	}
	used := map[string]int{}
	var out []ActAuthorization
	for i, act := range acts {
		checkRef, ok := actCheck(act)
		if !ok {
			return nil, bad(i, "a %s is not an act", act.Block.RecordType)
		}
		if checkRef == nil {
			continue
		}
		ci := slices.Index(digests, checkRef.Digest)
		if ci < 0 || journal[ci].Block.RecordType != TypeCheck {
			return nil, bad(i, "it cites no check the journal holds")
		}
		check := journal[ci]
		if check.Block.DealID != act.Block.DealID {
			return nil, bad(i, "its check serves another deal")
		}
		cb, _ := check.Body.(*CheckBody)
		// The check's verdict: exactly one.
		vi := -1
		for j, r := range journal {
			if r.Block.RecordType == TypeVerdict && citesAs(r, "checks", digests[ci]) {
				if vi >= 0 {
					return nil, bad(i, "its check has two verdicts")
				}
				vi = j
			}
		}
		if vi < 0 {
			return nil, bad(i, "its check has no verdict")
		}
		verdict := journal[vi]
		vb, _ := verdict.Body.(*dealprofile.VerdictBody)
		// The verdict's first approval decides; a later answer never
		// authorizes (a changed answer needs a new check).
		ai := -1
		for j, r := range journal {
			if r.Block.RecordType == TypeApproval && citesAs(r, "approves", digests[vi]) {
				ai = j
				break
			}
		}
		if ai < 0 {
			return nil, bad(i, "its verdict has no sealed approval")
		}
		approval := journal[ai]
		ab, _ := approval.Body.(*dealprofile.ApprovalBody)
		switch {
		case ab == nil || vb == nil || cb == nil:
			return nil, bad(i, "its check, verdict or approval is not of its type")
		case ab.Choice == ChoiceConfirm:
			return nil, bad(i, "a confirm_limits approval authorizes no act")
		case !ab.Proceed:
			return nil, bad(i, "its verdict's first approval does not proceed")
		case vb.Result == "deny":
			return nil, bad(i, "its verdict denies")
		case ab.Approver == "standing_intent" && vb.Result != "pass":
			return nil, bad(i, "a standing intent answers only a passing verdict")
		case ab.Approver == "standing_intent" && !allowedInForce(journal, ci, cb.Action):
			return nil, bad(i, "a standing intent approves only an action in the allowed in force")
		}
		if prev, dup := used[digests[ai]]; dup {
			return nil, bad(i, "its approval already authorized act %d", prev+1)
		}
		used[digests[ai]] = i
		actAt, err := time.Parse(time.RFC3339, act.Block.At)
		if err != nil {
			return nil, bad(i, "its at: %v", err)
		}
		for _, r := range journal[ci+1:] {
			if at, err := time.Parse(time.RFC3339, r.Block.At); err == nil && at.After(actAt) {
				continue
			}
			if changesIdentifiers(r) {
				return nil, bad(i, "a %s changed an identifier between its check and it: a new check is needed", r.Block.RecordType)
			}
		}
		out = append(out, ActAuthorization{Act: act, Check: check, Verdict: verdict, Approval: approval,
			ApprovalDigest: digests[ai], NeedsPersonSignature: ab.Approver == "user"})
	}
	return out, nil
}

// actCheck is the check an act cites, nil for none; ok is false for a
// record that is not an act.
func actCheck(r Record) (check *Ref, ok bool) {
	switch b := r.Body.(type) {
	case *PerformBody:
		return b.Check, true
	case PerformBody:
		return b.Check, true
	case *SettleBody:
		return b.Check, true
	case SettleBody:
		return b.Check, true
	}
	return nil, false
}

// citesAs says r's refs cite digest d with relation rel.
func citesAs(r Record, rel, d string) bool {
	for _, ref := range r.Block.Refs {
		if ref.Rel == rel && ref.Digest == d {
			return true
		}
	}
	return false
}

// changesIdentifiers says a journal record changes an identifier of the
// other side, as v0 rule 5 lists them: a detail_change, or a message or
// evidence that carries counterparty identifiers.
func changesIdentifiers(r Record) bool {
	switch r.Block.RecordType {
	case TypeDetailChange:
		return true
	case TypeMessage, TypeEvidence:
		return r.Block.Counterparty != nil
	}
	return false
}

// allowedInForce says action is in the allowed in force at the check at
// journal index ci (v0 rule 6): the first intent's allowed (absent places no
// restriction, present and empty allows nothing); a later intent may only
// drop actions, and a wider one is a proposal that only a confirm_limits
// approval with proceed puts in force.
func allowedInForce(journal []Record, ci int, action string) bool {
	var allowed []string
	set, seenIntent := false, false
	for _, r := range journal[:ci] {
		switch b := r.Body.(type) {
		case *dealprofile.IntentBody:
			if !seenIntent {
				allowed, set, seenIntent = slices.Clone(b.Allowed), b.Allowed != nil, true
				continue
			}
			if b.Allowed != nil && (!set || subset(b.Allowed, allowed)) {
				allowed, set = slices.Clone(b.Allowed), true
			}
		case *dealprofile.ApprovalBody:
			if b.Choice == ChoiceConfirm && b.Proceed && b.Limits != nil {
				allowed, set = slices.Clone(b.Limits.New.Allowed), b.Limits.New.Allowed != nil
			}
		}
	}
	return !set || slices.Contains(allowed, action)
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
