package deal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// A party's full answer to its adjudicator's request (evidencebook#861,
// docs/design/861-four-books.md §2.2, Steven's rulings u207 and u211): the
// adjudicator asks each party for the deal with derivation
// deal_adjudicator_copy/1 and subject {"correlation": <deal id>}, and the
// party answers with its full adjudicator copy (its whole deal book and the
// copy of the other side's book it holds) and its deal book's facts, or the
// request is refused. There is no selective form and no flag to withhold
// part of the book: the private floor and the agent claims are journal
// records, so a deal book never carries them (u547, u615).

// v1Answer is the answer document `deal respond` writes for an adjudicator's
// request, the one `deal ruling --answer` reads and a hosted adjudicator
// takes with the party's book: the request as received, the full copy as an
// Evidence Bundle, and the deal book's facts (evidencebook.BookEvidence)
// trimmed to what the copy reaches (xdealv1.TrimBookEvidence).
type v1Answer struct {
	Request json.RawMessage           `json:"request"`
	Bundle  json.RawMessage           `json:"bundle"`
	Facts   evidencebook.BookEvidence `json:"facts"`
}

// answerOutcomeEvidence is an answer record's outcome for an answer that
// carries evidence (PROFILE §5, answer: evidence, refusal or absence).
const answerOutcomeEvidence = "evidence"

// isAdjudicatorCopyRequest reports whether a request's derivation member is
// exactly the full adjudicator copy's token, looked up as the request reader
// does (a case variant is an unknown member).
func isAdjudicatorCopyRequest(members map[string]json.RawMessage) bool {
	var derivation string
	return json.Unmarshal(members["derivation"], &derivation) == nil && derivation == xdealv1.DerivationAdjudicatorCopy
}

// respondAdjudicatorCopy answers an adjudicator's deal_adjudicator_copy/1
// request on an x-deal-v1 deal in full, or refuses it with the request
// draft's reason: a request that does not parse (its reader's reason), one
// whose subject is not the correlation of an x-deal-v1 deal this profile
// holds with an agreement of its own (no_such_subject), and one whose deal
// names no adjudicator or another one than --requester (not_authorized).
// The request is unsigned, so the answer is only ever the copy for the
// adjudicator the agreement names, whoever passed the request on. Every
// answer or refusal of a deal this profile holds is linked by an answer
// record in the journal, sealed before the file is written.
func (s *dealSession) respondAdjudicatorCopy(c *cobra.Command, p cli.Profile, raw []byte, requester, path string, signer evidencebook.Signer) error {
	ctx := c.Context()
	requestDigest := cli.BytesDigest(raw)
	answered := map[string]interface{}{"request_digest": requestDigest, "requester": requester, "derivation": xdealv1.DerivationAdjudicatorCopy}
	dealID := ""
	refuse := func(reason string) error {
		refusal := evidencebook.Refusal{RequestDigest: requestDigest, Reason: reason, IssuedAt: cli.DealClock().UTC().Format(time.RFC3339Nano), KeyID: evidencebook.KeyID(signer)}
		body, err := refusal.SigningBody()
		if err != nil {
			return err
		}
		sig, err := signer.Sign(ctx, body)
		if err != nil {
			return err
		}
		refusal.Sig = hex.EncodeToString(sig)
		encoded, err := marshalDealRefusal(refusal)
		if err != nil {
			return err
		}
		answered["outcome"], answered["reason"] = evidencebook.OutcomeRefusal, reason
		if dealID != "" {
			digest, err := s.sealV1Answer(ctx, dealID, requestDigest, evidencebook.OutcomeRefusal, encoded)
			if err != nil {
				return err
			}
			answered["answer_record"] = digest
		}
		return s.writeAnswerBytes(c, dealID, answered, encoded, path)
	}
	check := evidencerequest.ParseRequest(evidencerequest.Message{Octets: raw, MediaType: evidencerequest.MediaTypeJSON})
	if check.Reason != "" {
		return refuse(check.Reason)
	}
	// The answer is always the newest copy the party has, so a min_freshness
	// floor is not checked (no fresher copy exists to send), and a deadline
	// passed does not stop it: the adjudicator decides what a late answer
	// counts for. An expected_pin names an anchor the adjudicator cannot
	// know before the party answers, so it cannot be met.
	if check.Request.Coverage.ExpectedPin != "" {
		return refuse(evidencebook.ReasonCoverageUnsatisfiable)
	}
	if check.Request.Subject.Kind != evidencerequest.SubjectCorrelation {
		answered["subject_kind"] = string(check.Request.Subject.Kind)
		return refuse(evidencebook.ReasonNoSuchSubject)
	}
	answered["subject_kind"] = string(evidencerequest.SubjectCorrelation)
	id := check.Request.Subject.Correlation
	if !cli.DealIDPattern.MatchString(id) {
		return refuse(evidencebook.ReasonNoSuchSubject)
	}
	d, err := s.v1DealOf(ctx, id)
	if err != nil {
		return err
	}
	if d == nil || d.OwnAgreementID == "" {
		return refuse(evidencebook.ReasonNoSuchSubject)
	}
	dealID = id
	book, err := s.ownBookV1(ctx, d)
	if err != nil {
		return err
	}
	var ag *xdealv1.AgreeBody
	ok := len(book) > 0
	if ok {
		ag, ok = agreeOf(book[0])
	}
	if !ok || ag.Kind != xdealv1.KindAgreement {
		return cli.InputError("the first record of your deal book is not your agreement")
	}
	if !ag.Adjudicator.Present || ag.Adjudicator.IsNull() || ag.Adjudicator.V.KeyID != requester {
		return refuse(evidencebook.ReasonNotAuthorized)
	}
	if path == "" {
		return cli.InputError("--output is required: the answer file to send to the adjudicator")
	}
	if _, err := os.Lstat(path); err == nil {
		return cli.InputError(path + " exists: --output names a new file")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, rec, err := s.fullAnswer(ctx, p, d, requester, raw)
	if err != nil {
		return err
	}
	digest, err := s.sealV1Answer(ctx, dealID, requestDigest, answerOutcomeEvidence, file)
	if err != nil {
		return err
	}
	answered["outcome"], answered["answer_record"] = evidencebook.OutcomeArtifact, digest
	if err := writeNewFileAfter(path, file, rec); err != nil {
		return err
	}
	id2, err := s.recordExchange(ctx, dealID, dealExchangeAnswered, "", answered)
	if err != nil {
		return err
	}
	return cli.Output(c, cli.EvidenceRespondResult{RecordID: id2, RequestDigest: requestDigest, Outcome: evidencebook.OutcomeArtifact, Relationship: evidencebook.RelationshipIdentified, Output: path})
}

// fullAnswer builds the answer document for deal d's adjudicator (key
// adjKey): the full adjudicator copy from a report snapshot, sealed and
// checked as `deal report --share adjudicator --bundle` seals and checks a
// copy, and the deal book's facts, read after it and trimmed to what it
// reaches (TrimBookEvidence), so a fact stamped between the two reads is
// left out. The newest facts are those of the newest finished stamp: a
// record after it, or one whose witness receipt has not come back, is
// untimed in this answer, and a later answer can time it. It returns the file's
// bytes and the step that puts the copy on record, which runs before the
// file is written: a shared copy is a disclose act.
func (s *dealSession) fullAnswer(ctx context.Context, p cli.Profile, d *v1Deal, adjKey string, request []byte) ([]byte, func() error, error) {
	cad, err := s.requireMigrated(ctx)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	at := cli.DealClock().UTC().Truncate(time.Second)
	prov := &xdealv1.StatusProvider{Shelf: s.shelf, OfferPair: s.sentOfferPair}
	rc := xdealv1.ReportCopy{Audience: dealprofile.AudienceAdjudicator, Referee: adjKey, Nonce: hex.EncodeToString(nonce), AsOf: at.Format("2006-01-02T15:04:05Z"), Full: true}
	snap, err := prov.ReportSnapshot(ctx, d.OwnLogID, rc)
	if err != nil {
		return nil, nil, v1ReportError(d.DealID, p, err)
	}
	pair, digest, err := s.sealV1Report(p, snap.Report, at)
	if err != nil {
		return nil, nil, err
	}
	if _, err := xdealv1.VerifyReport(pair, snap.Book.Agreement); err != nil {
		return nil, nil, cli.InputError("the report sealed with this profile's signing key does not verify against your agreement (" + err.Error() + "): the key that signed the agreement must sign the report; run it with the profile you agreed with (--profile)")
	}
	bundle, err := s.v1ReportBundle(ctx, d.OwnLogID, snap, pair, digest)
	if err != nil {
		return nil, nil, err
	}
	cc, err := caseCopyOfBundle(bundle)
	if err != nil {
		return nil, nil, err
	}
	_, full, err := xdealv1.VerifyFullCopy(cc.Report, cc.Extension)
	if err != nil {
		return nil, nil, err
	}
	evidence, err := cad.BookEvidence(ctx, d.OwnAgreementID, 0)
	if err != nil {
		return nil, nil, err
	}
	facts, err := json.Marshal(xdealv1.TrimBookEvidence(evidence, full))
	if err != nil {
		return nil, nil, err
	}
	// The request is carried as its exact bytes, so its digest in the file is
	// the digest the journal's answer names and the adjudicator computes;
	// encoding/json would compact it.
	file := []byte(`{"request":`)
	file = append(append(file, bytes.TrimSpace(request)...), `,"bundle":`...)
	file = append(append(file, bundle...), `,"facts":`...)
	file = append(append(file, facts...), "}\n"...)
	// The exact bytes are checked to contain none of the deal's own plain
	// values, as for any copy that leaves the device.
	events, err := s.v1Events(ctx, d.DealID)
	if err != nil {
		return nil, nil, err
	}
	if err := v1ShareGate(scrubPaymentRefs(file), events, dealprofile.AudienceAdjudicator, v1OwnIDs(d.DealID, snap.Book)...); err != nil {
		return nil, nil, err
	}
	record := func() error {
		_, err := s.recordV1Share(ctx, p, d.DealID, "full_bundle", dealprofile.AudienceAdjudicator, adjKey, adjKey, file, snap.Book)
		return err
	}
	return file, record, nil
}

// sealV1Answer seals the journal's answer record linking an answer document
// (the full answer, or a refusal) to the request it answers (PROFILE §5,
// answer), and returns its digest. It stays in the journal: it is appended
// there, never sent. One run is one answer document and one answer record;
// a run again makes a new answer (a new report nonce and time), and only a
// record of the same digest is not appended twice.
func (s *dealSession) sealV1Answer(ctx context.Context, dealID, requestDigest, outcome string, answer []byte) (string, error) {
	ref := func(d string) xdealv1.Ref {
		return xdealv1.Ref{Type: dealprofile.RecordRef, DigestAlg: "SHA-256", Digest: d}
	}
	rec := xdealv1.Record{
		Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypeAnswer, At: v1Now()},
		Body:  &xdealv1.AnswerBody{Request: ref(requestDigest), Outcome: outcome, Answer: ref(cli.BytesDigest(answer))},
	}
	if err := xdealv1.CheckSealed(rec, xdealv1.SealContext{OfferExists: true}); err != nil {
		return "", err
	}
	payload, digest, err := xdealv1.Encode(rec)
	if err != nil {
		return "", err
	}
	held, err := s.journalHolds(ctx, digest)
	if err != nil || held {
		return digest, err
	}
	return digest, s.appendV1Journal(ctx, payload)
}

// caseCopyOfBundle reads an x-deal-v1 report bundle's report and extension,
// as xdealv1.CheckCase and VerifyFullCopy take them.
func caseCopyOfBundle(raw []byte) (xdealv1.CaseCopy, error) {
	var b struct {
		Extensions map[string]json.RawMessage `json:"extensions"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return xdealv1.CaseCopy{}, errors.New("is not a bundle: " + err.Error())
	}
	report, ext := b.Extensions[dealprofile.ReportExtension], b.Extensions[xdealv1.Profile]
	if report == nil || ext == nil {
		return xdealv1.CaseCopy{}, errors.New("is not an x-deal-v1 report copy")
	}
	return xdealv1.CaseCopy{Report: report, Extension: ext}, nil
}

// readAnswerFile reads a party's answer to the adjudicator's request (the
// file `deal respond` wrote): its request must be a deal_adjudicator_copy/1
// request for deal dealID, and its bundle must verify as a bundle; the full
// copy and the facts are checked by xdealv1.GatherCase.
func readAnswerFile(path, dealID string) (xdealv1.CaseAnswer, error) {
	bad := func(why string) (xdealv1.CaseAnswer, error) {
		return xdealv1.CaseAnswer{}, cli.InputError("--answer " + path + " " + why)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return xdealv1.CaseAnswer{}, cli.InputError("cannot read --answer " + path)
	}
	var a v1Answer
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || a.Request == nil || a.Bundle == nil {
		return bad("is not an answer file `deal respond` wrote for an adjudicator's request")
	}
	check := evidencerequest.ParseRequest(evidencerequest.Message{Octets: a.Request, MediaType: evidencerequest.MediaTypeJSON})
	switch {
	case check.Reason != "":
		return bad("carries a request that does not parse: " + check.Reason)
	case check.Request.Derivation != xdealv1.DerivationAdjudicatorCopy:
		return bad("answers a request that is not for the deal's full adjudicator copy (" + xdealv1.DerivationAdjudicatorCopy + ")")
	case check.Request.Subject.Kind != evidencerequest.SubjectCorrelation || check.Request.Subject.Correlation != dealID:
		return bad("answers a request for another deal than " + dealID)
	}
	decoded, err := cli.DecodeBundleJSON(a.Bundle)
	if err != nil {
		return bad("does not carry a bundle: " + err.Error())
	}
	if err := cli.VerifyProducedBundle(decoded, true); err != nil {
		return bad("carries a bundle that does not verify: " + err.Error())
	}
	cc, err := caseCopyOfBundle(a.Bundle)
	if err != nil {
		return bad("carries a bundle that " + err.Error())
	}
	return xdealv1.CaseAnswer{Copy: cc, Evidence: a.Facts}, nil
}

// nullID is a Null record id as JSON: the id, or null.
func nullID(n xdealv1.Null[string]) *string {
	if !n.Present || n.V == nil {
		return nil
	}
	return n.V
}

// gatheredSideOut is what `deal ruling` prints of one side of a gathered
// case, for the ruling's page (Steven's ruling 2, u211): the sources read
// (null for one not shown to the adjudicator), the fork position, and each
// record of the book read with its time and what timed it: the side's own
// stamps (own_stamp), the other side's held proofs (holder_link), or none.
func gatheredSideOut(s xdealv1.CaseSide) map[string]any {
	records := make([]map[string]any, 0, len(s.Book))
	for _, r := range s.Book {
		row := map[string]any{"seq": r.Read.Seq, "record_id": r.Read.RecordID, "type": r.Read.Record.Block.RecordType, "at": nil, "timed_by": nil}
		if r.At != "" {
			row["at"], row["timed_by"] = r.At, r.TimedBy
		}
		records = append(records, row)
	}
	forked := make([]string, 0, len(s.Forked))
	for _, r := range s.Forked {
		forked = append(forked, r.RecordID)
	}
	return map[string]any{"key": s.Key, "read": nullID(s.Read), "own": nullID(s.Own), "held": nullID(s.Held), "fork_at": s.ForkAt, "forked": forked, "records": records}
}

// notShownToAdjudicator is how a source the adjudicator did not read is
// named (Sherlock2's string, ledger-cloud#35): never "refused".
const notShownToAdjudicator = "not shown to the adjudicator"

// rulingSourcesWords is a received ruling's sources as `deal receive`
// prints them (evidencebook#861): per side, the last record id of each
// source the adjudicator read, or "not shown to the adjudicator", and the
// position its book forked at when it did.
func rulingSourcesWords(src xdealv1.RulingSources) map[string]any {
	words := func(n xdealv1.Null[string]) string {
		if id := nullID(n); id != nil {
			return *id
		}
		return notShownToAdjudicator
	}
	side := func(own, held xdealv1.Null[string], heldBy string, forkAt int64) map[string]any {
		m := map[string]any{"own": words(own), heldBy: words(held)}
		if forkAt > 0 {
			m["fork_at"] = forkAt
			m["fork"] = fmt.Sprintf("the signer's book forked at %d", forkAt)
		}
		return m
	}
	return map[string]any{
		xdealv1.RoleBuyer:  side(src.Buyer.Own, src.Buyer.HeldBySeller, "held_by_seller", src.Buyer.ForkAt),
		xdealv1.RoleSeller: side(src.Seller.Own, src.Seller.HeldByBuyer, "held_by_buyer", src.Seller.ForkAt),
	}
}
