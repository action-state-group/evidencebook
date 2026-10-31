package deal

import (
	"errors"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/spf13/cobra"
)

// respondStandardHistory answers a standard_history/1 request (SH §3) over
// the full_history subject. The answer is evidence-interop's own signed
// Envelope, the one `capsulectl book respond` writes (IP §6.2), whose artifact
// is the card's JCS octets, or its signed Refusal: not the deal-local envelope
// a deal copy travels in (Sherlock2 confirmed the choice on
// evidencebook-deal#85). The steps follow the request draft's order
// (evidencerequest.Decide): the deadline, derivation support, who is
// answered, the subject, then coverage.
//
// The holder's declaration decides the first two. An undeclared holder does
// not serve the derivation (derivation_unsupported). One that does not
// answer (a person by default, or a node that opted out) declines
// (policy_declined): SH §3 makes who is answered the holder's policy, so
// that token reads as a choice. A deal with no amount is a gap in the data,
// not a choice, so the card that cannot be built at the anchor is
// coverage_unsatisfiable. The coverage is a cadence anchor: a tick's
// checkpoint. None is issued here, since `deal tick` cuts them.
func (s *dealSession) respondStandardHistory(c *cobra.Command, check evidencerequest.RequestCheck, raw []byte, requester, path string, signer evidencebook.Signer) error {
	ctx := c.Context()
	req := check.Request
	digest := evidencerequest.Digest(raw)
	now := cli.DealClock().UTC()
	answered := map[string]interface{}{"request_digest": digest, "requester": requester, "subject_kind": string(req.Subject.Kind)}
	refuse := func(reason string) error {
		refusal := evidencebook.Refusal{RequestDigest: digest, Reason: reason, IssuedAt: evidencerequest.ProfileTime(now), KeyID: evidencebook.KeyID(signer)}
		refusal, err := refusal.Sign(ctx, signer)
		if err != nil {
			return err
		}
		reply, err := evidencerequest.EncodeReply(evidencerequest.MediaTypeJSON, nil, &refusal)
		if err != nil {
			return err
		}
		answered["outcome"], answered["reason"] = evidencebook.OutcomeRefusal, reason
		return s.writeAnswerBytes(c, "", answered, reply.Octets, path)
	}
	if check.Reason != "" {
		// The request reader's own refusal (profile §6.1), in the interop
		// reply every other answer here is.
		return refuse(check.Reason)
	}
	if req.Deadline != "" {
		deadline, ok := evidencerequest.ParseRequestTime(req.Deadline)
		if !ok {
			return refuse(evidencebook.ReasonRequestMalformed)
		}
		if !now.Before(deadline) {
			return refuse(evidencebook.ReasonDeadlineUnmet)
		}
	}
	settings, declared, err := readSHSettings(s.p)
	if err != nil {
		return err
	}
	if !declared {
		return refuse(evidencebook.ReasonDerivationUnsupported)
	}
	if !settings.answers() {
		return refuse(evidencebook.ReasonPolicyDeclined)
	}
	if req.Subject.Kind != evidencerequest.SubjectFullHistory {
		// Another subject form is no form this derivation is registered for
		// (ER §3.3). It is said after who is answered, so a holder that
		// declines does not say which forms it serves.
		return refuse(evidencebook.ReasonNoSuchSubject)
	}
	cadence, _, err := s.cadenceOf(ctx)
	if err != nil {
		return err
	}
	last, ok, err := cadence.Last(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return refuse(evidencebook.ReasonCoverageUnsatisfiable)
	}
	// The anchors are the finished ticks' cadence checkpoints, oldest first.
	var floorTime time.Time
	if f := req.Coverage.MinFreshness; f != nil && f.Size == nil {
		var ok bool
		if floorTime, ok = evidencerequest.ParseRequestTime(f.Time); !ok {
			return refuse(evidencebook.ReasonRequestMalformed)
		}
	}
	var tick evidencebook.CadenceTick
	found := false
	for n := last.N; n >= 1 && !found; n-- {
		t, err := cadence.Get(ctx, n)
		if err != nil {
			if errors.Is(err, evidencebook.ErrNotFound) {
				continue
			}
			return err
		}
		if t.CheckpointSize == 0 {
			continue
		}
		view := evidencerequest.AnchorView{Digest: evidencerequest.Digest(t.Checkpoint), Entries: mmrLeafCount(t.CheckpointSize), IssuedAt: t.At}
		switch {
		case req.Coverage.ExpectedPin != "":
			if found = view.Digest == req.Coverage.ExpectedPin; found {
				tick = t
			}
		case req.Coverage.MinFreshness != nil:
			if found = evidencerequest.AnchorMeets(view, req.Coverage.MinFreshness, floorTime); found {
				tick = t
			}
		}
	}
	if !found {
		return refuse(evidencebook.ReasonCoverageUnsatisfiable)
	}
	h, err := s.standardHistory(ctx, tick.N)
	if errors.Is(err, dealprofile.ErrUnpriced) || errors.Is(err, errNoTick) || errors.Is(err, errStaleAnchor) || errors.Is(err, errLegacyDeal) {
		return refuse(evidencebook.ReasonCoverageUnsatisfiable)
	}
	if err != nil {
		return err
	}
	envelope := evidencebook.Envelope{
		Profile: req.Profile, RequestDigest: digest, Subject: req.Subject, Derivation: req.Derivation,
		Anchor: evidencebook.Anchor{Digest: h.AsOf.Anchor}, ArtifactDigest: evidencerequest.Digest(h.Card),
		Responder: evidencebook.KeyID(signer), IssuedAt: evidencerequest.ProfileTime(now), Artifact: h.Card,
	}
	if f := req.Coverage.MinFreshness; f != nil {
		if f.Size != nil {
			size := tick.CheckpointSize
			envelope.Anchor.FreshnessSize = &size
		} else {
			envelope.Anchor.FreshnessTime = h.AsOf.AnchorTime
		}
	}
	if envelope, err = envelope.Sign(ctx, signer); err != nil {
		return err
	}
	reply, err := evidencerequest.EncodeReply(evidencerequest.MediaTypeJSON, &envelope, nil)
	if err != nil {
		return err
	}
	answered["outcome"], answered["anchor"], answered["artifact_digest"] = evidencebook.OutcomeArtifact, envelope.Anchor.Digest, envelope.ArtifactDigest
	return s.writeAnswerBytes(c, "", answered, reply.Octets, path)
}
