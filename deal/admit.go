// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// engineDecision is the part of `capsule-engine admission evaluate --admit`'s
// output that ParseAdmission reads (capsule-engine go/cmd/capsule-engine/
// main.go, output). The rest of that object is the engine's and is not read
// here.
type engineDecision struct {
	Verdict string  `json:"verdict"`
	Tier    *string `json:"tier"`
	Rows    []struct {
		Row string `json:"row"`
	} `json:"rows"`
	Pack *struct {
		Digest string `json:"digest"`
	} `json:"pack"`
	Admit      json.RawMessage `json:"admit"`
	AdmitsDeal *bool           `json:"admits_deal"`
}

// Admission is capsule-engine's admission decision as an admit record that
// an agreement can cite, read by ParseAdmission and sealed by Admit.
type Admission struct {
	payload []byte
}

// ParseAdmission reads the output of `capsule-engine admission evaluate
// --admit FILE` (book.admit's result) into the admit record it makes, timed
// at. It evaluates nothing: the decision is capsule-engine's. It refuses what
// an agreement cannot cite: an output with no admit body, a decision with no
// tier (a NO), one the engine says does not admit the deal (admits_deal
// false: an ASK still waiting for answers, or a LIMIT whose tier does not
// hold the deal), and an admit body that differs from the decision it came
// with. The refusal follows the engine's admits_deal, never the outcome word:
// whether a LIMIT's tier holds the deal is the engine's to say.
func ParseAdmission(engineOutput []byte, at time.Time) (Admission, error) {
	var out engineDecision
	// The engine's object has more members than these, which are its own,
	// so the outer object is read leniently; the admit body inside it is
	// read strictly below.
	if err := json.Unmarshal(engineOutput, &out); err != nil {
		return Admission{}, fieldError("input", " is not the JSON output of `capsule-engine admission evaluate --admit FILE`: "+err.Error())
	}
	if out.AdmitsDeal == nil || out.Admit == nil {
		return Admission{}, fieldError("input", " has no admit body: run `capsule-engine admission evaluate` with --admit FILE, which prints the decision as an x-deal-v1 admit")
	}
	if bytes.Equal(bytes.TrimSpace(out.Admit), []byte("null")) {
		return Admission{}, inputError("capsule-engine's decision has no tier (a NO), so it is not sealed and no agreement can cite it: this deal is not admitted")
	}
	if !*out.AdmitsDeal {
		return Admission{}, coded(CodeAdmitNotAdmitted, "", fmt.Sprintf("capsule-engine says this decision (%s) does not admit the deal: an agreement cites only one that does. If it asks for answers, answer them and run `capsule-engine admission evaluate --admit FILE` again with them; then pass the new output to ParseAdmission and Store.Admit. A LIMIT whose tier does not hold the deal admits a smaller deal only", out.Verdict), out.Verdict)
	}
	// The body is sealed exactly as the engine printed it: Decode refuses a
	// member the profile does not name, and Check runs the profile's schema
	// and record checks on the record it makes.
	doc := []byte(`{"x-deal-v1":{"profile":"` + xdealv1.Profile + `","canonicalization":"` + xdealv1.Canonicalization + `","record_type":"` + xdealv1.TypeAdmit + `","at":"` + at.UTC().Format(time.RFC3339) + `"},"body":`)
	doc = append(append(doc, out.Admit...), '}')
	rec, err := xdealv1.Decode(doc)
	if err != nil {
		return Admission{}, fieldError("input", "'s admit is not an x-deal-v1 admit body: "+err.Error())
	}
	body, ok := rec.Body.(*xdealv1.AdmitBody)
	if !ok {
		return Admission{}, fieldError("input", "'s admit is not an x-deal-v1 admit body")
	}
	// The engine's verdict and pack are the body's own outcome and pack: a
	// body that disagrees with the output it came in was not printed with it.
	// So are its tier and its rows, which the engine copies from the same
	// decision (capsule-engine go/admission/admit.go, Admit).
	rows := make([]string, len(out.Rows))
	for i, r := range out.Rows {
		rows[i] = r.Row
	}
	if body.Outcome != out.Verdict || out.Pack == nil || body.PackDigest != out.Pack.Digest ||
		out.Tier == nil || body.Tier != *out.Tier || !slices.Equal(body.Rows, rows) {
		return Admission{}, fieldError("input", "'s admit body does not match the decision it came with (its verdict, tier, rows or pack differ): pass capsule-engine's output unchanged")
	}
	payload, _, err := xdealv1.Encode(doc)
	if err != nil {
		return Admission{}, fieldError("input", "'s admit does not pass the x-deal-v1 checks: "+err.Error())
	}
	return Admission{payload: payload}, nil
}

// Admit seals the admission decision a for the deal dealID in the journal,
// once, and returns its record digest, which Agree cites. Each deal has one:
// the same decision again returns the digest already sealed and seals
// nothing; another decision for a deal whose admit is sealed is refused, and
// so is a deal already agreed. One capsule-engine output is sealed for one
// deal only: its admit's nonce is drawn for one record. The admit record
// stays in the journal; only its digest leaves the holder, in its agreement.
func (s *Store) Admit(ctx context.Context, dealID string, a Admission) (string, error) {
	if a.payload == nil {
		return "", errors.New("deal: Admit needs an Admission from ParseAdmission")
	}
	d, err := s.Repair(ctx, dealID)
	if err != nil {
		return "", err
	}
	if d == nil {
		return "", coded(CodeAdmitUnknownDeal, "", "deal "+dealID+" is not an x-deal-v1 deal this profile holds: make or receive its offer first (Store.Offer, Store.NoteReceived)", dealID)
	}
	if d.State == "cancelled" || d.State == "declined" {
		return "", inputError("this deal's offer is " + d.State + ": there is no deal to admit")
	}
	// An agreement a stopped Agree appended but did not record was found
	// above: it cites its own admit.
	if d.OwnAgreementID != "" {
		return "", inputError("deal " + dealID + " is already agreed, and its agreement cites the admission decision it was agreed on: a deal's decision cannot change after it")
	}
	return s.sealAdmit(ctx, dealID, a.payload)
}

// sealAdmit seals the admit for dealID once. The binding is stored before
// the record is sealed, so a run that stops between the two is finished by
// the next run, which seals the stored payload (same time, same digest) when
// the journal does not hold it; a binding whose record the journal never
// took has left nothing with anyone, so a new decision replaces it. The same
// body again returns the digest already sealed; another body for a deal
// whose admit is sealed is refused, so Agree never has two to choose from.
// A body whose nonce an admit in the journal already carries is refused: one
// engine output is one record (PROFILE §5, a nonce is drawn for each record),
// and a journal holding two would fail xdealv1.CheckAuthorizations for good.
func (s *Store) sealAdmit(ctx context.Context, dealID string, payload []byte) (string, error) {
	var (
		digest string
		stored []byte
	)
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT admit_digest, payload FROM deal_v1_admits WHERE deal_id=?`, dealID).Scan(&digest, &stored)
	switch {
	case err == nil:
		held, err := s.journalHolds(ctx, digest)
		if err != nil {
			return "", err
		}
		switch {
		case sameAdmitBody(stored, payload):
			payload = stored
		case held:
			return "", codedConflict(CodeAdmitAgain, "deal "+dealID+" already has an admission decision, "+digest+", and Store.Agree cites that one: a deal has one, so to decide again, start a new deal", dealID, digest)
		default:
			if digest, err = s.bindAdmit(ctx, dealID, payload, true); err != nil {
				return "", err
			}
		}
	case errors.Is(err, sql.ErrNoRows):
		if digest, err = s.bindAdmit(ctx, dealID, payload, false); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	held, err := s.journalHolds(ctx, digest)
	if err != nil || held {
		return digest, err
	}
	rec, _, err := sealSent(ctx, s.cfg.Shelf, evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}}, "admit/sent")
	if err != nil && rec.RecordID == "" {
		return "", err
	}
	return digest, err
}

// bindAdmit stores the binding of payload to dealID (replacing the deal's
// unsealed one when replace is set), after refusing a nonce another admit
// already carries.
func (s *Store) bindAdmit(ctx context.Context, dealID string, payload []byte, replace bool) (string, error) {
	rec, err := xdealv1.Decode(payload)
	if err != nil {
		return "", err
	}
	nonce := rec.Body.(*xdealv1.AdmitBody).Nonce
	if other, err := s.admitWithNonce(ctx, nonce); err != nil {
		return "", err
	} else if other != "" {
		return "", conflictf("this capsule-engine output is already sealed, as admit %s: one output is one record, so run `capsule-engine admission evaluate --admit FILE` again for this deal", other)
	}
	_, digest, err := xdealv1.Encode(payload)
	if err != nil {
		return "", err
	}
	if replace {
		if _, err := s.cfg.DB.ExecContext(ctx, `DELETE FROM deal_v1_admits WHERE deal_id=?`, dealID); err != nil {
			return "", err
		}
	}
	if _, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO deal_v1_admits (deal_id, admit_digest, payload) VALUES (?, ?, ?)`, dealID, digest, payload); err != nil {
		return "", err
	}
	return digest, nil
}

// admitWithNonce is the digest of an admit that carries nonce: one in the
// journal (however it was sealed), or one bound by Admit and not yet sealed;
// "" when there is none.
func (s *Store) admitWithNonce(ctx context.Context, nonce string) (string, error) {
	rows, err := s.cfg.DB.QueryContext(ctx, `SELECT admit_digest, payload FROM deal_v1_admits`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			digest  string
			payload []byte
		)
		if err := rows.Scan(&digest, &payload); err != nil {
			return "", err
		}
		if r, err := xdealv1.Decode(payload); err == nil {
			if b, ok := r.Body.(*xdealv1.AdmitBody); ok && b.Nonce == nonce {
				return digest, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	j, records, err := s.journalRecords(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range records {
		for _, c := range r.Header.PayloadCommitments {
			payload, err := j.ResolvePayload(ctx, r.RecordID, c)
			var gone *evidencebook.UnavailableError
			if errors.As(err, &gone) {
				// A withheld or deleted payload cannot be read and holds no
				// nonce this check can see; any other error is a failed read,
				// which must not pass the check.
				continue
			}
			if err != nil {
				return "", err
			}
			if rec, err := xdealv1.Decode(payload); err == nil {
				if b, ok := rec.Body.(*xdealv1.AdmitBody); ok && b.Nonce == nonce {
					return c, nil
				}
			}
		}
	}
	return "", nil
}

// sameAdmitBody reports whether two admit payloads hold the same body: a
// re-run seals at another time, so the blocks differ and only the bodies are
// compared.
func sameAdmitBody(a, b []byte) bool {
	ra, err := xdealv1.Decode(a)
	if err != nil {
		return false
	}
	rb, err := xdealv1.Decode(b)
	if err != nil {
		return false
	}
	ja, err := json.Marshal(ra.Body)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(rb.Body)
	return err == nil && bytes.Equal(ja, jb)
}

// dealAdmit is the admission decision Admit bound to dealID, or "" when it
// bound none.
func (s *Store) dealAdmit(ctx context.Context, dealID string) (string, error) {
	var digest string
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT admit_digest FROM deal_v1_admits WHERE deal_id=?`, dealID).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return digest, err
}

// admitDealOf is the deal an admit was sealed for by Admit, or "".
func (s *Store) admitDealOf(ctx context.Context, digest string) (string, error) {
	var dealID string
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT deal_id FROM deal_v1_admits WHERE admit_digest=?`, digest).Scan(&dealID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return dealID, err
}

// citableAdmit refuses an admit an agreement cannot cite (x-deal-v1
// PROFILE.md, admit): one whose outcome is ASK, whose questions are still
// open, or NO. A GO is cited; so is a LIMIT, which admits a deal whose value
// band is within its tier. Whether a LIMIT's tier holds this deal is the
// evaluator's, which has the pack's tiers (capsule-engine admission evaluate
// --admit prints admits_deal); the store holds no pack, so it does not
// decide it. An over-tier LIMIT cited here is still detectable offline by a
// reader holding the pack whose definition_digest is the admit's
// pack_digest: its tiers bound the value the agreement's offer states. A
// record of any other type passes.
func citableAdmit(digest string, rec *xdealv1.Record) error {
	b, ok := rec.Body.(*xdealv1.AdmitBody)
	if !ok {
		return nil
	}
	switch b.Outcome {
	case "ASK", "NO":
		return fieldError("admit", " "+digest+" is an admission decision of "+b.Outcome+
			": an agreement cites a GO, or a LIMIT whose tier holds the deal")
	}
	return nil
}

// agreementAdmit is the admit an agreement on dealID cites: the one Admit
// sealed for the deal, which admit may name or leave out. An admit Admit
// sealed for another deal is refused, so an admission decision is never
// cited on a deal it was not made for. An admit sealed some other way (not
// through Admit) is cited as named.
func (s *Store) agreementAdmit(ctx context.Context, dealID, admit string) (string, error) {
	own, err := s.dealAdmit(ctx, dealID)
	if err != nil {
		return "", err
	}
	switch {
	case admit == "" && own == "":
		return "", coded(CodeAgreeNoAdmit, "", "deal "+dealID+" has no admission decision: seal capsule-engine's with Store.Admit, which an agreement cites", dealID)
	case admit == "":
		return own, nil
	case own != "" && admit != own:
		return "", coded(CodeAgreeAdmitNotOwn, "admit", " "+admit+" is not this deal's admission decision: Store.Admit sealed "+own+" for deal "+dealID+", and the agreement cites that one", admit, own, dealID)
	}
	other, err := s.admitDealOf(ctx, admit)
	if err != nil {
		return "", err
	}
	if other != "" && other != dealID {
		return "", coded(CodeAgreeAdmitOtherDeal, "admit", " "+admit+" is the admission decision Store.Admit sealed for deal "+other+", not for deal "+dealID, admit, other, dealID)
	}
	return admit, nil
}
