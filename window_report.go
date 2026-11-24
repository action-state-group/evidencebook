// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/evidenceresult"
)

// A report over a range of a holder's windows (evidencebook#689 §5): the
// requester seals the holder's window chain as a record, asks each window's
// book pinned to its final checkpoint, and seals one Result over every
// answer, which cites the answers and the chain record. VerifyReportCoverage
// checks from the bundle alone that the windows tile the reported range and
// each has its answer, and names every gap.

// RecordTypeWindowChain is the record type of a sealed window chain.
const RecordTypeWindowChain = "window_chain"

// WindowChainRecordVersion is the record_version of a window_chain body.
const WindowChainRecordVersion = "window_chain/v1"

// WindowChainStatement is the body of a window_chain record: the range the
// requester reports on and the digest of the chain, the record's payload.
type WindowChainStatement struct {
	RecordVersion string       `json:"record_version"`
	Base          string       `json:"base"`
	Period        WindowPeriod `json:"period"`
	From          string       `json:"from"`
	To            string       `json:"to"`
	Start         string       `json:"start"`
	End           string       `json:"end"`
	ChainDigest   string       `json:"chain_digest"`
}

// RecordWindowChain seals a holder's window chain in this book as the
// range a report covers, with the chain as the record's payload. It refuses
// a chain with any finding under opts.
func (b *Book) RecordWindowChain(ctx context.Context, chain WindowChain, opts VerifyChainOptions) (Record, error) {
	verified, findings := VerifyWindowChain(chain, opts)
	if len(findings) > 0 {
		return Record{}, fmt.Errorf("%w: the window chain does not verify: %s", ErrInvalid, joinFindings(findings))
	}
	payload, err := canonicalJSON(chain)
	if err != nil {
		return Record{}, err
	}
	statement := WindowChainStatement{
		RecordVersion: WindowChainRecordVersion, Base: chain.Base, Period: chain.Period,
		From: chain.From, To: chain.To,
		Start: windowTime(verified[0].Start), End: windowTime(verified[len(verified)-1].End),
		ChainDigest: digestBytes(payload),
	}
	body, err := canonicalJSON(statement)
	if err != nil {
		return Record{}, err
	}
	return b.Append(ctx, Entry{RecordType: RecordTypeWindowChain, EpistemicType: ObservedEvent, Links: []Link{}, Statement: body, Payloads: [][]byte{payload}})
}

func joinFindings(findings []ChainFinding) string {
	texts := make([]string, len(findings))
	for i, f := range findings {
		texts[i] = f.Text
	}
	return strings.Join(texts, "; ")
}

// WindowInputs is one answer in a report over windows: a window's own, or,
// on a shelf whose windows are its journal, a deal book's placed in it
// (evidencebook#797 §4), Deal then naming the deal book.
type WindowInputs struct {
	Window VerifiedWindow
	// Deal is the log id of the deal book that gave this answer, placed in
	// Window; empty for the window's own answer.
	Deal string
	DerivedInputs
}

// ReportLimit is what no report over windows shows, stated once per report
// beside its coverage and on anything shown on its own (Sherlock2 on
// evidencebook#797, after #606).
const ReportLimit = "Each answer proves its rows are in the holder's log; the rows' field values are the holder's word; no proof shows that no row was left out."

// DealCoverage is one deal book placed in a window of the range and the
// answer that covers it. A deal book is not bounded by its window: its
// answer covers the whole book through the checkpoint it is anchored at,
// records after the window included.
type DealCoverage struct {
	LogID string `json:"log_id"`
	// Window is the label of the window the deal is placed in.
	Window  string     `json:"window"`
	Through WindowTree `json:"through"`
	// ThroughAt is when that checkpoint was issued, RFC 3339 in UTC.
	ThroughAt string `json:"through_at"`
	// Line is the coverage line a page shows for the deal.
	Line string `json:"line"`
}

// windowAnswer is what one response record holds for a report over
// windows: its inputs, the holder's checkpoint key, the anchor tree and the
// derivation it answers.
type windowAnswer struct {
	inputs     DerivedInputs
	key        string
	tree       WindowTree
	issuedAt   time.Time
	derivation string
}

// readWindowAnswer reads a response's statement and reply: the anchor's
// signed checkpoint in its verification material, under the key the
// response pinned, and the proofs of its rows there.
func readWindowAnswer(header Header, reply []byte) (windowAnswer, error) {
	var statement ResponseStatement
	if err := json.Unmarshal(header.Statement, &statement); err != nil {
		return windowAnswer{}, fmt.Errorf("%w: response statement: %v", ErrCorrupt, err)
	}
	if statement.Outcome != OutcomeArtifact || slices.Contains(statement.NotChecked, "anchor_inclusion") {
		return windowAnswer{}, fmt.Errorf("%w: the response is not an answer whose proofs verified", ErrInvalid)
	}
	var envelope *Envelope
	for _, mediaType := range []string{MediaTypeJSON, MediaTypeCBOR} {
		if parsed, err := ParseReply(Message{Octets: reply, MediaType: mediaType}); err == nil && parsed.Envelope != nil {
			envelope = parsed.Envelope
			break
		}
	}
	if envelope == nil {
		return windowAnswer{}, fmt.Errorf("%w: the response holds no envelope", ErrInvalid)
	}
	if envelope.Anchor.Digest != statement.Anchor {
		return windowAnswer{}, fmt.Errorf("%w: the reply's anchor is not the one the response recorded", ErrInvalid)
	}
	decoded, err := evidencerequest.DecodeJSON(envelope.Artifact)
	artifact, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return windowAnswer{}, fmt.Errorf("%w: the artifact is not a JSON object", ErrInvalid)
	}
	rows, _ := artifact["records"].([]any)
	item, err := verifySetInclusion(*envelope, rows, statement.Anchor, statement.CheckpointKey)
	if err != nil {
		return windowAnswer{}, err
	}
	cp, err := readSignedCheckpoint(item, statement.CheckpointKey)
	if err != nil {
		return windowAnswer{}, err
	}
	return windowAnswer{key: statement.CheckpointKey, tree: WindowTree{LogID: cp.LogID, Entries: cp.Entries, Root: cp.Root}, issuedAt: cp.IssuedAt, derivation: envelope.Derivation}, nil
}

// ReportFromWindows seals the Evidence Result evaluate returns over a
// holder's answers to one pack across a range of windows, and bundles it
// with the contract-report/v1 extension. chainRecordID is the window_chain
// record (RecordWindowChain) that states the range; responseRecordIDs are
// the recorded answers, exactly one per window of the range, each anchored
// at its window's final tree, and, for a chain of journal windows, exactly
// one per deal book a window's close lists as placed, anchored at any
// checkpoint of that book; every one to the pack opts.Derivation names
// and under one holder checkpoint key, and each with proofs. Every claim of
// the Result names one contract, cites at least one of the responses, and
// cites nothing but those and the chain record. ReportFromDerivation is the
// rule for the rest.
func (b *Book) ReportFromWindows(ctx context.Context, chainRecordID string, responseRecordIDs []string, evaluate func([]WindowInputs) (json.RawMessage, error), opts ReportOptions, chainOpts VerifyChainOptions) (Report, error) {
	if len(responseRecordIDs) == 0 {
		return Report{}, fmt.Errorf("%w: a report over windows needs their answers", ErrInvalid)
	}
	answers := make([]windowAnswer, len(responseRecordIDs))
	for i, id := range responseRecordIDs {
		inputs, err := b.DerivedInputs(ctx, id)
		if err != nil {
			return Report{}, err
		}
		if inputs.Derivation != opts.Derivation {
			return Report{}, fmt.Errorf("%w: response %s answers pack %s, not %q", ErrInvalid, id, inputs.Derivation, opts.Derivation)
		}
		if !inputs.Proven {
			return Report{}, fmt.Errorf("%w: response %s carries no proofs; a report over windows needs them", ErrInvalid, id)
		}
		record, err := b.Get(ctx, id)
		if err != nil {
			return Report{}, err
		}
		reply, err := b.ReplyOctets(ctx, id)
		if err != nil {
			return Report{}, err
		}
		answer, err := readWindowAnswer(record.Header, reply)
		if err != nil {
			return Report{}, fmt.Errorf("response %s: %w", id, err)
		}
		answer.inputs = inputs
		answers[i] = answer
	}
	chainRecord, err := b.Get(ctx, chainRecordID)
	if err != nil {
		return Report{}, err
	}
	if chainRecord.Header.RecordType != RecordTypeWindowChain {
		return Report{}, fmt.Errorf("%w: %s is not a window chain record", ErrInvalid, chainRecordID)
	}
	payload, err := b.payloadOf(ctx, chainRecord)
	if err != nil {
		return Report{}, err
	}
	// The answers were recorded under the holder keys this requester pinned
	// (RecordResponse), so their key is the chain's; a key the caller names
	// must be that one.
	if chainOpts.CheckpointKey != "" && chainOpts.CheckpointKey != answers[0].key {
		return Report{}, fmt.Errorf("%w: the answers are under holder checkpoint key %s, not %s", ErrInvalid, answers[0].key, chainOpts.CheckpointKey)
	}
	chainOpts.CheckpointKey = answers[0].key
	windows, deals, findings, err := coverageWithDeals(chainRecord.Header, payload, answers, chainOpts)
	if err != nil {
		return Report{}, err
	}
	if len(findings) > 0 {
		return Report{}, fmt.Errorf("%w: the answers do not cover the range: %s", ErrInvalid, joinFindings(findings))
	}
	inputs := make([]WindowInputs, len(answers))
	for i, a := range answers {
		if d, ok := deals[a.tree.LogID]; ok {
			inputs[i] = WindowInputs{Window: windows[d.window], Deal: a.tree.LogID, DerivedInputs: a.inputs}
			continue
		}
		inputs[i] = WindowInputs{Window: windows[a.tree.LogID], DerivedInputs: a.inputs}
	}
	result, err := evaluate(inputs)
	if err != nil {
		return Report{}, err
	}
	contract, err := checkWindowsResult(result, responseRecordIDs, chainRecordID)
	if err != nil {
		return Report{}, err
	}
	canonical, err := canonicalStatement(result)
	if err != nil {
		return Report{}, err
	}
	links := []Link{{Type: Cites, Target: chainRecordID}}
	for _, id := range responseRecordIDs {
		links = append(links, Link{Type: Cites, Target: id})
	}
	b.reportMu.Lock()
	reuse := func() (Record, bool, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, record := range b.logLocked() {
			if record.Header.RecordType != RecordTypeEvidenceResult || !slices.Contains(record.Header.LinksTo(Cites), chainRecordID) {
				continue
			}
			if existing, err := canonicalStatement(record.Header.Statement); err == nil && bytes.Equal(existing, canonical) {
				return record, true, nil
			}
		}
		return Record{}, false, nil
	}
	sealed, _, _, err := b.SealResult(ctx, result, contract, links, reuse, func(entry Entry) (Record, error) { return b.Append(ctx, entry) })
	b.reportMu.Unlock()
	if err != nil {
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

// payloadOf is a record's first original, from the artifact store.
func (b *Book) payloadOf(ctx context.Context, record Record) ([]byte, error) {
	if len(record.Header.PayloadCommitments) == 0 {
		return nil, fmt.Errorf("%w: record %s carries no payload", ErrNotFound, record.RecordID)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resolveLocked(ctx, record.RecordID, record.Header.PayloadCommitments[0])
}

// placedDeal is a deal book a window of the chain lists as placed, and its
// answer once one is found.
type placedDeal struct {
	window   string // the placing window's log id
	label    string
	answers  int
	coverage DealCoverage
}

// coverage checks the chain a window_chain record carries against its
// statement, and the answers against the chain: one per window of the range,
// each at its window's final tree, and one per deal book placed in a window
// of a journal chain (evidencebook#797 §4), at any checkpoint of that book,
// all under one key. It returns the proved windows by log id, the placed
// deals by log id, and a finding for each gap; err is for a record that
// cannot be read at all.
func coverage(chainHeader Header, payload []byte, answers []windowAnswer, opts VerifyChainOptions) (map[string]VerifiedWindow, []ChainFinding, error) {
	windows, _, findings, err := coverageWithDeals(chainHeader, payload, answers, opts)
	return windows, findings, err
}

// coverageWithDeals is coverage with the placed deals by log id.
func coverageWithDeals(chainHeader Header, payload []byte, answers []windowAnswer, opts VerifyChainOptions) (map[string]VerifiedWindow, map[string]*placedDeal, []ChainFinding, error) {
	var statement WindowChainStatement
	if err := decodeStrict(chainHeader.Statement, &statement); err != nil || statement.RecordVersion != WindowChainRecordVersion {
		return nil, nil, nil, fmt.Errorf("%w: the window chain record does not carry its body", ErrInvalid)
	}
	if digestBytes(payload) != statement.ChainDigest {
		return nil, nil, nil, fmt.Errorf("%w: the window chain record's payload is not the chain it names", ErrInvalid)
	}
	var chain WindowChain
	if err := decodeStrict(payload, &chain); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: the window chain: %v", ErrInvalid, err)
	}
	var findings []ChainFinding
	if chain.Base != statement.Base || chain.Period != statement.Period || chain.From != statement.From || chain.To != statement.To {
		findings = append(findings, ChainFinding{Text: fmt.Sprintf("windows do not tile %s..%s: the chain covers %s..%s", statement.From, statement.To, chain.From, chain.To)})
	}
	for _, a := range answers {
		if a.key != opts.CheckpointKey {
			findings = append(findings, ChainFinding{Text: fmt.Sprintf("an answer at %s is under holder checkpoint key %s, not the holder's %s", a.tree.LogID, a.key, opts.CheckpointKey)})
		}
		if a.derivation != answers[0].derivation {
			findings = append(findings, ChainFinding{Text: fmt.Sprintf("an answer at %s answers derivation %s, not %s as the others", a.tree.LogID, a.derivation, answers[0].derivation)})
		}
	}
	verified, chainFindings := VerifyWindowChain(chain, opts)
	findings = append(findings, chainFindings...)
	windows := make(map[string]VerifiedWindow, len(verified))
	deals := map[string]*placedDeal{}
	for _, v := range verified {
		windows[v.Final.LogID] = v
		for _, logID := range v.Placed {
			deals[logID] = &placedDeal{window: v.Final.LogID, label: v.Label}
		}
	}
	answered := map[string]int{}
	for _, a := range answers {
		v, ok := windows[a.tree.LogID]
		if d, placed := deals[a.tree.LogID]; placed && !ok {
			d.answers++
			at := windowTime(a.issuedAt)
			d.coverage = DealCoverage{LogID: a.tree.LogID, Window: d.label, Through: a.tree, ThroughAt: at,
				Line: fmt.Sprintf("deal %s, placed in window %s, answered over its whole book, through its checkpoint of %d records issued %s, records after the window included", a.tree.LogID, d.label, a.tree.Entries, at)}
			continue
		}
		switch {
		case !ok:
			findings = append(findings, ChainFinding{Text: fmt.Sprintf("an answer anchored in log %s is outside the chain's windows", a.tree.LogID)})
		case a.tree != v.Final:
			findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("window %s's answer is anchored at a tree of %d records, not its final tree of %d", v.Label, a.tree.Entries, v.Final.Entries)})
		default:
			answered[v.Label]++
		}
	}
	for _, v := range verified {
		if chain.Period.Partial(v.Window) {
			// The conversion's period began in the predecessor book, which
			// no window answers for (evidencebook#808).
			periodStart := chain.Period.start(v.Start)
			findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("window %s starts at %s; [%s, %s) is in the predecessor book, not covered", v.Label, windowTime(v.Start), windowTime(periodStart), windowTime(v.Start))})
		}
		switch n := answered[v.Label]; {
		case n == 0:
			findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("window %s has no answer", v.Label)})
		case n > 1:
			findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("window %s has %d answers", v.Label, n)})
		}
		for _, logID := range v.Placed {
			switch n := deals[logID].answers; {
			case n == 0:
				findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("deal %s of window %s has no answer", logID, v.Label)})
			case n > 1:
				findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("deal %s of window %s has %d answers", logID, v.Label, n)})
			}
		}
	}
	return windows, deals, findings, nil
}

// checkWindowsResult is checkDerivedResult for a report over windows: one
// contract across the claims, and each claim's evidence at least one of the
// responses and nothing but them and the chain record.
func checkWindowsResult(result []byte, responses []string, chain string) (string, error) {
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
		cited := 0
		for _, evidence := range claim.Evidence {
			switch {
			case slices.Contains(responses, evidence):
				cited++
			case evidence == chain:
			default:
				return "", fmt.Errorf("%w: claim %d cites %s, which is neither one of the answers nor the window chain", ErrInvalid, claim.Index, evidence)
			}
		}
		if cited == 0 {
			return "", fmt.Errorf("%w: claim %d cites none of the answers", ErrInvalid, claim.Index)
		}
	}
	return contract, nil
}

// ReportCoverage is what VerifyReportCoverage found: the range the report
// states, the windows its chain proved, and every finding. A report covers
// its range only when Findings is empty.
type ReportCoverage struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Derivation is the pack the answers answer, the first answer's when
	// they differ (a finding then says so).
	Derivation string           `json:"derivation,omitempty"`
	Windows    []VerifiedWindow `json:"windows"`
	// Deals are the deal books placed in the range's windows that have
	// their answer, in window order then log id (evidencebook#797 §4).
	Deals    []DealCoverage `json:"deals,omitempty"`
	Findings []ChainFinding `json:"findings"`
	// Limit is ReportLimit: what the report does not show.
	Limit string `json:"limit"`
}

// CoverageOptions are what a verifier of a report's coverage brings: the
// holder's checkpoint key, obtained independently of the bundle, and the
// witness directory when the agreement names witnesses.
type CoverageOptions struct {
	// CheckpointKey is the key id the holder signs its checkpoints with.
	// Required: a key read from the bundle would let whoever built it
	// stand in for the holder.
	CheckpointKey string
	Witnesses     []WitnessRow
}

// VerifyReportCoverage checks a report bundle over windows (ReportFromWindows)
// from the bundle alone: it verifies the bundle, finds its Result, the one
// window_chain record the Result cites and the answers it cites, and checks
// that the chain verifies, the windows tile the stated range, and each
// window has exactly one answer, anchored at its final tree, whose proofs
// verify there under one holder key. Each gap is a named finding. An error
// is for a bundle that does not verify or is not a report over windows.
func VerifyReportCoverage(bundle []byte, opts CoverageOptions) (ReportCoverage, error) {
	if opts.CheckpointKey == "" {
		return ReportCoverage{}, fmt.Errorf("%w: the holder's checkpoint key is required", ErrInvalid)
	}
	verified, err := VerifyBundle(bundle)
	if err != nil {
		return ReportCoverage{}, err
	}
	byID := make(map[string]PeerRecord, len(verified.Records))
	var results []PeerRecord
	for _, r := range verified.Records {
		if r.Header == nil || !r.HeaderVerified {
			continue
		}
		byID[r.RecordID] = r
		if r.Header.RecordType == RecordTypeEvidenceResult {
			results = append(results, r)
		}
	}
	if len(results) != 1 {
		return ReportCoverage{}, fmt.Errorf("%w: the bundle holds %d Evidence Results, not one", ErrInvalid, len(results))
	}
	result := results[0]
	var chainHeader *Header
	var chainPayload []byte
	chainID := ""
	var answers []windowAnswer
	var answerIDs []string
	for _, target := range result.Header.LinksTo(Cites) {
		r, ok := byID[target]
		if !ok {
			return ReportCoverage{}, fmt.Errorf("%w: the Result cites %s, which the bundle does not carry verified", ErrInvalid, target)
		}
		switch r.Header.RecordType {
		case RecordTypeWindowChain:
			if chainHeader != nil {
				return ReportCoverage{}, fmt.Errorf("%w: the Result cites two window chains", ErrInvalid)
			}
			if chainPayload, err = bundlePayload(verified, r); err != nil {
				return ReportCoverage{}, err
			}
			chainHeader, chainID = r.Header, target
		case RecordTypeResponse:
			reply, err := bundlePayload(verified, r)
			if err != nil {
				return ReportCoverage{}, err
			}
			answer, err := readWindowAnswer(*r.Header, reply)
			if err != nil {
				return ReportCoverage{}, fmt.Errorf("answer %s: %w", target, err)
			}
			answers = append(answers, answer)
			answerIDs = append(answerIDs, target)
		}
	}
	if chainHeader == nil {
		return ReportCoverage{}, fmt.Errorf("%w: the Result cites no window chain: it is not a report over windows", ErrInvalid)
	}
	var statement WindowChainStatement
	if err := decodeStrict(chainHeader.Statement, &statement); err != nil {
		return ReportCoverage{}, fmt.Errorf("%w: the window chain record does not carry its body", ErrInvalid)
	}
	out := ReportCoverage{From: statement.From, To: statement.To, Findings: []ChainFinding{}, Limit: ReportLimit}
	if _, err := checkWindowsResult(result.Header.Statement, answerIDs, chainID); err != nil {
		out.Findings = append(out.Findings, ChainFinding{Text: "the Result: " + strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")})
	}
	if len(answers) == 0 {
		out.Findings = append(out.Findings, ChainFinding{Text: "the Result cites no answer"})
	} else {
		out.Derivation = answers[0].derivation
	}
	windows, deals, findings, err := coverageWithDeals(*chainHeader, chainPayload, answers, VerifyChainOptions{CheckpointKey: opts.CheckpointKey, Witnesses: opts.Witnesses})
	if err != nil {
		return ReportCoverage{}, err
	}
	out.Findings = append(out.Findings, findings...)
	for _, w := range windows {
		out.Windows = append(out.Windows, w)
	}
	slices.SortFunc(out.Windows, func(a, b VerifiedWindow) int { return a.Start.Compare(b.Start) })
	for _, w := range out.Windows {
		for _, logID := range w.Placed {
			if d := deals[logID]; d.answers == 1 {
				out.Deals = append(out.Deals, d.coverage)
			}
		}
	}
	return out, nil
}

// bundlePayload is the first payload a record of a verified bundle commits.
func bundlePayload(verified VerifiedBundle, r PeerRecord) ([]byte, error) {
	if len(r.Header.PayloadCommitments) == 0 {
		return nil, fmt.Errorf("%w: record %s carries no payload", ErrInvalid, r.RecordID)
	}
	data, ok := verified.Payloads[r.Header.PayloadCommitments[0]]
	if !ok {
		return nil, fmt.Errorf("%w: the bundle does not carry record %s's payload", ErrInvalid, r.RecordID)
	}
	return data, nil
}
