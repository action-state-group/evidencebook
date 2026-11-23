// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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

// WindowInputs is one window's answer in a report over windows.
type WindowInputs struct {
	Window VerifiedWindow
	DerivedInputs
}

// windowAnswer is what one response record holds for a report over
// windows: its inputs, the holder's checkpoint key and the anchor tree.
type windowAnswer struct {
	inputs DerivedInputs
	key    string
	tree   WindowTree
}

// readWindowAnswer reads a response's statement and reply: the anchor's
// signed checkpoint in its verification material, under the key the
// response pinned, and the proofs of its rows there.
func readWindowAnswer(header Header, reply []byte) (WindowTree, string, error) {
	var statement ResponseStatement
	if err := json.Unmarshal(header.Statement, &statement); err != nil {
		return WindowTree{}, "", fmt.Errorf("%w: response statement: %v", ErrCorrupt, err)
	}
	if statement.Outcome != OutcomeArtifact || slices.Contains(statement.NotChecked, "anchor_inclusion") {
		return WindowTree{}, "", fmt.Errorf("%w: the response is not an answer whose proofs verified", ErrInvalid)
	}
	var envelope *Envelope
	for _, mediaType := range []string{MediaTypeJSON, MediaTypeCBOR} {
		if parsed, err := ParseReply(Message{Octets: reply, MediaType: mediaType}); err == nil && parsed.Envelope != nil {
			envelope = parsed.Envelope
			break
		}
	}
	if envelope == nil {
		return WindowTree{}, "", fmt.Errorf("%w: the response holds no envelope", ErrInvalid)
	}
	if envelope.Anchor.Digest != statement.Anchor {
		return WindowTree{}, "", fmt.Errorf("%w: the reply's anchor is not the one the response recorded", ErrInvalid)
	}
	decoded, err := evidencerequest.DecodeJSON(envelope.Artifact)
	artifact, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return WindowTree{}, "", fmt.Errorf("%w: the artifact is not a JSON object", ErrInvalid)
	}
	rows, _ := artifact["records"].([]any)
	item, err := verifySetInclusion(*envelope, rows, statement.Anchor, statement.CheckpointKey)
	if err != nil {
		return WindowTree{}, "", err
	}
	cp, err := readSignedCheckpoint(item, statement.CheckpointKey)
	if err != nil {
		return WindowTree{}, "", err
	}
	return WindowTree{LogID: cp.LogID, Entries: cp.Entries, Root: cp.Root}, statement.CheckpointKey, nil
}

// ReportFromWindows seals the Evidence Result evaluate returns over a
// holder's answers to one pack across a range of windows, and bundles it
// with the contract-report/v1 extension. chainRecordID is the window_chain
// record (RecordWindowChain) that states the range; responseRecordIDs are
// the recorded answers, exactly one per window of the range, each anchored
// at its window's final tree, every one to the pack opts.Derivation names
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
		tree, key, err := readWindowAnswer(record.Header, reply)
		if err != nil {
			return Report{}, fmt.Errorf("response %s: %w", id, err)
		}
		answers[i] = windowAnswer{inputs: inputs, key: key, tree: tree}
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
	key := answers[0].key
	chainOpts.CheckpointKey = key
	windows, findings, err := coverage(chainRecord.Header, payload, answers, chainOpts)
	if err != nil {
		return Report{}, err
	}
	if len(findings) > 0 {
		return Report{}, fmt.Errorf("%w: the answers do not cover the range: %s", ErrInvalid, joinFindings(findings))
	}
	inputs := make([]WindowInputs, len(answers))
	for i, a := range answers {
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

// coverage checks the chain a window_chain record carries against its
// statement, and the answers against the chain: one per window of the range,
// each at its window's final tree, under one key. It returns the proved
// windows by log id and a finding for each gap; err is for a record that
// cannot be read at all.
func coverage(chainHeader Header, payload []byte, answers []windowAnswer, opts VerifyChainOptions) (map[string]VerifiedWindow, []ChainFinding, error) {
	var statement WindowChainStatement
	if err := decodeStrict(chainHeader.Statement, &statement); err != nil || statement.RecordVersion != WindowChainRecordVersion {
		return nil, nil, fmt.Errorf("%w: the window chain record does not carry its body", ErrInvalid)
	}
	if digestBytes(payload) != statement.ChainDigest {
		return nil, nil, fmt.Errorf("%w: the window chain record's payload is not the chain it names", ErrInvalid)
	}
	var chain WindowChain
	if err := decodeStrict(payload, &chain); err != nil {
		return nil, nil, fmt.Errorf("%w: the window chain: %v", ErrInvalid, err)
	}
	var findings []ChainFinding
	if chain.Base != statement.Base || chain.Period != statement.Period || chain.From != statement.From || chain.To != statement.To {
		findings = append(findings, ChainFinding{Text: fmt.Sprintf("windows do not tile %s..%s: the chain covers %s..%s", statement.From, statement.To, chain.From, chain.To)})
	}
	for _, a := range answers {
		if a.key != opts.CheckpointKey {
			findings = append(findings, ChainFinding{Text: fmt.Sprintf("an answer at %s is under another holder checkpoint key than the others", a.tree.LogID)})
		}
	}
	verified, chainFindings := VerifyWindowChain(chain, opts)
	findings = append(findings, chainFindings...)
	windows := make(map[string]VerifiedWindow, len(verified))
	for _, v := range verified {
		windows[v.Final.LogID] = v
	}
	answered := map[string]int{}
	for _, a := range answers {
		v, ok := windows[a.tree.LogID]
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
		switch n := answered[v.Label]; {
		case n == 0:
			findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("window %s has no answer", v.Label)})
		case n > 1:
			findings = append(findings, ChainFinding{Window: v.Label, Text: fmt.Sprintf("window %s has %d answers", v.Label, n)})
		}
	}
	return windows, findings, nil
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
	From     string           `json:"from"`
	To       string           `json:"to"`
	Windows  []VerifiedWindow `json:"windows"`
	Findings []ChainFinding   `json:"findings"`
}

// CoverageOptions are what a verifier of a report's coverage brings: the
// witness directory, when the agreement names witnesses.
type CoverageOptions struct {
	Witnesses []WitnessRow
}

// VerifyReportCoverage checks a report bundle over windows (ReportFromWindows)
// from the bundle alone: it verifies the bundle, finds its Result, the one
// window_chain record the Result cites and the answers it cites, and checks
// that the chain verifies, the windows tile the stated range, and each
// window has exactly one answer, anchored at its final tree, whose proofs
// verify there under one holder key. Each gap is a named finding. An error
// is for a bundle that does not verify or is not a report over windows.
func VerifyReportCoverage(bundle []byte, opts CoverageOptions) (ReportCoverage, error) {
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
	var chainHeader *Header
	var chainPayload []byte
	var answers []windowAnswer
	for _, target := range results[0].Header.LinksTo(Cites) {
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
			chainHeader = r.Header
		case RecordTypeResponse:
			reply, err := bundlePayload(verified, r)
			if err != nil {
				return ReportCoverage{}, err
			}
			tree, key, err := readWindowAnswer(*r.Header, reply)
			if err != nil {
				return ReportCoverage{}, fmt.Errorf("answer %s: %w", target, err)
			}
			answers = append(answers, windowAnswer{key: key, tree: tree})
		}
	}
	if chainHeader == nil {
		return ReportCoverage{}, fmt.Errorf("%w: the Result cites no window chain: it is not a report over windows", ErrInvalid)
	}
	var statement WindowChainStatement
	if err := decodeStrict(chainHeader.Statement, &statement); err != nil {
		return ReportCoverage{}, fmt.Errorf("%w: the window chain record does not carry its body", ErrInvalid)
	}
	out := ReportCoverage{From: statement.From, To: statement.To, Findings: []ChainFinding{}}
	chainOpts := VerifyChainOptions{Witnesses: opts.Witnesses}
	if len(answers) == 0 {
		out.Findings = append(out.Findings, ChainFinding{Text: "the Result cites no answer"})
	} else {
		chainOpts.CheckpointKey = answers[0].key
	}
	windows, findings, err := coverage(*chainHeader, chainPayload, answers, chainOpts)
	if err != nil {
		return ReportCoverage{}, err
	}
	out.Findings = append(out.Findings, findings...)
	for _, w := range windows {
		out.Windows = append(out.Windows, w)
	}
	slices.SortFunc(out.Windows, func(a, b VerifiedWindow) int { return a.Start.Compare(b.Start) })
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
