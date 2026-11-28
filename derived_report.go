// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/evidenceresult"
)

// ExtensionContractReport is the bundle extension that opts a verified
// Evidence Result into the contract-report view (agent-action-capsule
// spec/contract-report-v1.md §1): a closed empty object.
const ExtensionContractReport = "contract-report/v1"

// definitionDigest is the identifier of a derivation named by the digest of
// its definition (ER -03 §Derivation), as opposed to a registered token.
var definitionDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// DerivedRecord is one row of a holder's answer to a derivation request: the
// digest and leaf of a record in the holder's log, and the fields the
// derivation declares, as the holder attested them.
type DerivedRecord struct {
	Digest    string
	LeafIndex uint64
	Fields    map[string]any
}

// DerivedInputs is what a requester holds after a holder answered a request
// that named a derivation by the digest of its definition, read back from
// the response record the requester sealed (RecordResponse).
type DerivedInputs struct {
	// Response is the id of the response record. The Result a requester
	// signs over these inputs cites it: it holds the holder's signed reply.
	Response string
	// Derivation is the digest of the definition the holder answered, the
	// one the request named.
	Derivation string
	// Anchor is the digest of the checkpoint the holder answered at (the
	// artifact's own anchor member, which a request pinning an exchange half
	// keeps apart from the envelope's).
	Anchor string
	// Proven reports that the answer carried the anchor's checkpoint and a
	// proof for every row, and that RecordResponse checked them: the rows
	// are records of the holder's log at that checkpoint. It never says that
	// no row was left out, or that a field value is the record's: those stay
	// the holder's word.
	Proven  bool
	Records []DerivedRecord
}

// DerivedInputs reads a response record this book sealed for a request that
// named a derivation by the digest of its definition. It refuses a response
// that is a refusal or whose answer failed a check, one that answers a
// registered token (a history card, say), and an artifact that is not a
// record set.
func (b *Book) DerivedInputs(ctx context.Context, responseRecordID string) (DerivedInputs, error) {
	record, err := b.Get(ctx, responseRecordID)
	if err != nil {
		return DerivedInputs{}, err
	}
	if record.Header.RecordType != RecordTypeResponse {
		return DerivedInputs{}, fmt.Errorf("%w: %s is not a response this book recorded", ErrNotFound, responseRecordID)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(record.Header.Statement, &statement); err != nil {
		return DerivedInputs{}, fmt.Errorf("%w: response %s: %v", ErrCorrupt, responseRecordID, err)
	}
	if statement.Outcome != OutcomeArtifact {
		return DerivedInputs{}, fmt.Errorf("%w: response %s is %s, not an answer that verified", ErrInvalid, responseRecordID, statement.Outcome)
	}
	octets, err := b.ReplyOctets(ctx, responseRecordID)
	if err != nil {
		return DerivedInputs{}, err
	}
	var envelope *Envelope
	for _, mediaType := range []string{MediaTypeJSON, MediaTypeCBOR} {
		if reply, err := ParseReply(Message{Octets: octets, MediaType: mediaType}); err == nil && reply.Envelope != nil {
			envelope = reply.Envelope
			break
		}
	}
	if envelope == nil {
		return DerivedInputs{}, fmt.Errorf("%w: response %s holds no envelope", ErrInvalid, responseRecordID)
	}
	if !definitionDigest.MatchString(envelope.Derivation) {
		return DerivedInputs{}, fmt.Errorf("%w: response %s answers %q, not a derivation named by the digest of its definition", ErrInvalid, responseRecordID, envelope.Derivation)
	}
	decoded, err := evidencerequest.DecodeJSON(envelope.Artifact)
	artifact, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return DerivedInputs{}, fmt.Errorf("%w: response %s: the artifact is not a JSON object", ErrInvalid, responseRecordID)
	}
	rows, isList := artifact["records"].([]any)
	anchor, _ := artifact["anchor"].(string)
	if !isList || anchor == "" {
		return DerivedInputs{}, fmt.Errorf("%w: response %s: the artifact holds no records list or anchor", ErrInvalid, responseRecordID)
	}
	inputs := DerivedInputs{
		Response: responseRecordID, Derivation: envelope.Derivation, Anchor: anchor,
		Proven:  len(envelope.Verification) > 0 && !slices.Contains(statement.NotChecked, "anchor_inclusion"),
		Records: make([]DerivedRecord, len(rows)),
	}
	for i, item := range rows {
		row, _ := item.(map[string]any)
		digest, _ := row["digest"].(string)
		leafIndex, leafOK := evidencerequest.UintValue(row["leaf_index"])
		if digest == "" || !leafOK {
			return DerivedInputs{}, fmt.Errorf("%w: response %s: row %d names no record and leaf", ErrInvalid, responseRecordID, i)
		}
		fields := make(map[string]any, len(row))
		for name, value := range row {
			if name != "digest" && name != "leaf_index" {
				fields[name] = value
			}
		}
		inputs.Records[i] = DerivedRecord{Digest: digest, LeafIndex: leafIndex, Fields: fields}
	}
	return inputs, nil
}

// UnprovenInputsTitle is what the title of a Result's view must begin with
// when ReportFromDerivation is allowed to report over inputs the holder did
// not prove, so the page never reads as a report over proven ones.
const UnprovenInputsTitle = "Inputs not proven: the holder's word only. "

// ReportOptions selects how ReportFromDerivation builds the report. Its
// Bundle names the anchor and witnesses as for Book.Bundle; Root, Payloads
// and the contract-report extension are the report's own.
type ReportOptions struct {
	Bundle BundleRequest
	// Derivation is the digest of the pack the caller's evaluator runs. It
	// must be the one the holder answered (DerivedInputs.Derivation), so a
	// Result is never signed for one pack over another's inputs.
	Derivation string
	// AllowUnproven lets a report be built over an answer that carries no
	// proofs (DerivedInputs.Proven false), for a holder that registered the
	// pack without Proofs. Without it that is refused. With it the Result's
	// view title must begin with UnprovenInputsTitle.
	AllowUnproven bool
}

// Report is a requester's report over a holder's answer: the Result the
// requester signed, and the one Evidence Bundle that is both its machine view
// and, with the contract-report/v1 extension, what a viewer renders.
type Report struct {
	Result Record
	Bundle Bundle
}

// ReportFromDerivation seals the Evidence Result evaluate returns over the
// holder's answer recorded as responseRecordID, and bundles it. The Result
// is the requester's evaluation, signed by this book's key, over the rows
// the holder sent; it is not the holder's. The pack and its evaluation are
// the caller's (this package runs none): evaluate gets DerivedInputs, and
// opts.Derivation, the digest of the pack the caller runs, must be the one the
// holder answered.
//
// Before it signs, the Result must be a valid Evidence Result v0 whose every
// claim names one contract and cites exactly the response record as its
// evidence, since the response is the record of this book that holds what
// the holder signed. The source records are not in this book, so a claim
// cannot cite them and be resolved by a reader; their digests stay in the
// signed reply, which the bundle carries whole. The bundle is built with
// every payload, and refused when the reply is no longer held. An answer
// that carries no proofs is refused unless opts.AllowUnproven, and then the
// Result's view title must begin with UnprovenInputsTitle. Calls for one
// Result seal it once.
func (b *Book) ReportFromDerivation(ctx context.Context, responseRecordID string, evaluate func(DerivedInputs) (json.RawMessage, error), opts ReportOptions) (Report, error) {
	inputs, err := b.DerivedInputs(ctx, responseRecordID)
	if err != nil {
		return Report{}, err
	}
	if opts.Derivation != inputs.Derivation {
		return Report{}, fmt.Errorf("%w: the evaluator runs pack %q, the holder answered %s", ErrInvalid, opts.Derivation, inputs.Derivation)
	}
	if !inputs.Proven && !opts.AllowUnproven {
		return Report{}, fmt.Errorf("%w: response %s carries no proofs, so its rows are the holder's word only", ErrInvalid, responseRecordID)
	}
	result, err := evaluate(inputs)
	if err != nil {
		return Report{}, err
	}
	contract, err := checkDerivedResult(result, inputs)
	if err != nil {
		return Report{}, err
	}
	canonical, err := canonicalStatement(result)
	if err != nil {
		return Report{}, err
	}
	// SealResult leaves serializing a look-up and a seal to its caller, so
	// two reports of one Result seal it once.
	b.reportMu.Lock()
	reuse := func() (Record, bool, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, record := range b.logLocked() {
			if record.Header.RecordType != RecordTypeEvidenceResult || !slices.Contains(record.Header.LinksTo(Cites), responseRecordID) {
				continue
			}
			if existing, err := canonicalStatement(record.Header.Statement); err == nil && bytes.Equal(existing, canonical) {
				return record, true, nil
			}
		}
		return Record{}, false, nil
	}
	sealed, _, _, err := b.SealResult(ctx, result, contract, []Link{{Type: Cites, Target: responseRecordID}}, reuse, func(entry Entry) (Record, error) { return b.Append(ctx, entry) })
	b.reportMu.Unlock()
	if err != nil {
		// A Result committed before the failure is returned with it, as
		// SealResult does, so the caller does not lose its id.
		return Report{Result: sealed}, err
	}
	request := opts.Bundle
	request.Root, request.Payloads = sealed.RecordID, PayloadsAll
	request.Extensions = make(map[string]json.RawMessage, len(opts.Bundle.Extensions)+1)
	for kind, block := range opts.Bundle.Extensions {
		request.Extensions[kind] = block
	}
	request.Extensions[ExtensionContractReport] = json.RawMessage(`{}`)
	bundle, err := b.Bundle(ctx, request)
	if err != nil {
		return Report{Result: sealed}, err
	}
	return Report{Result: sealed, Bundle: bundle}, nil
}

// canonicalStatement is the JCS of a JSON document, so two spellings of one
// Result compare equal; a book also holds every statement in this form
// (commitEntryLocked). A repeated member name in any object, invalid UTF-8 and
// an unpaired surrogate escape are refused (evidencerequest.DecodeJSON).
func canonicalStatement(doc []byte) ([]byte, error) {
	decoded, err := evidencerequest.DecodeJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	out, err := evidencerequest.EncodeJCS(decoded)
	if err != nil {
		// A number JCS cannot carry (a fraction, an integer past 2^53).
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return out, nil
}

// checkDerivedResult validates the Result an evaluator returned with the
// whole Evidence Result v0 gate (schema and cross-claim rules) and returns
// the one contract its claims report: at least one claim, one contract_ref
// across them, and each claim's evidence exactly the response record. An
// answer without proofs adds the title rule of UnprovenInputsTitle.
func checkDerivedResult(result []byte, inputs DerivedInputs) (string, error) {
	doc, err := evidenceresult.Decode(result)
	if err != nil {
		return "", fmt.Errorf("%w: the Result is not a valid Evidence Result v0: %v", ErrInvalid, err)
	}
	claims, err := evidenceresult.Check(doc, nil)
	if err != nil {
		return "", fmt.Errorf("%w: the Result is not a valid Evidence Result v0: %v", ErrInvalid, err)
	}
	if len(claims) == 0 {
		return "", fmt.Errorf("%w: the Result has no claim", ErrInvalid)
	}
	contract := ""
	for _, claim := range claims {
		if claim.ContractRef == "" || contract != "" && claim.ContractRef != contract {
			return "", fmt.Errorf("%w: claim %d does not report the one contract of the Result", ErrInvalid, claim.Index)
		}
		contract = claim.ContractRef
		if !slices.Equal(claim.Evidence, []string{inputs.Response}) {
			return "", fmt.Errorf("%w: claim %d must cite exactly the response record %s as its evidence", ErrInvalid, claim.Index, inputs.Response)
		}
	}
	if !inputs.Proven {
		view, _ := doc["view"].(map[string]interface{})
		if title, _ := view["title"].(string); !strings.HasPrefix(title, UnprovenInputsTitle) {
			return "", fmt.Errorf("%w: the inputs are not proven, so the Result's view title must begin %q", ErrInvalid, UnprovenInputsTitle)
		}
	}
	return contract, nil
}
