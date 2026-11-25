// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidenceresult"
	"github.com/action-state-group/capsule-engine/go/guard"
	"github.com/action-state-group/capsule-engine/go/pack"
)

// RecordTypeAgentAct is the record type of an act of the book owner's own
// agent, sealed by RecordAct. Its statement is the act in capsule-engine's
// action form, the JSON `capsule-engine check --action` reads.
const RecordTypeAgentAct = "agent_act"

// RecordAct seals act, one act of the book owner's agent, as the next
// record of the book. act is capsule-engine's action form (guard.Action:
// verb, operator, developer, timestamp, and the fields a pack's rules read,
// such as amount_minor, currency and target); it is refused unless
// guard.DecodeAction reads it. The record carries the act, in its canonical
// form, as its statement, so a bundle that discloses the record discloses
// every member of the act.
func (b *Book) RecordAct(ctx context.Context, act json.RawMessage) (Record, error) {
	if _, err := guard.DecodeAction(bytes.NewReader(act)); err != nil {
		return Record{}, fmt.Errorf("%w: the act is not in capsule-engine's action form: %v", ErrInvalid, err)
	}
	canonical, err := canonicalStatement(act)
	if err != nil {
		return Record{}, err
	}
	return b.Append(ctx, Entry{RecordType: RecordTypeAgentAct, EpistemicType: ProducerClaim, Statement: canonical})
}

// EvaluateOptions selects how EvaluatePack bundles the Result. Bundle names
// the anchor and witnesses as for Book.Bundle; Root, Payloads and the
// contract-report extension are the report's own.
type EvaluateOptions struct {
	Bundle BundleRequest
}

// EvaluatePack runs p, a capsule-engine rules pack, over every act
// RecordAct sealed in this book, in log order, each act against the acts
// before it, and seals the outcome as an Evidence Result v0 the book signs.
// It returns the sealed Result and the one Evidence Bundle rooted at it, with
// every payload and the contract-report/v1 extension, which report.Build
// renders.
//
// The rules are capsule-engine's: this package decides nothing itself. Each
// act's per-rule verdicts (guard.Engine.Report) become claims as AAC
// spec/evidence-result-v0.md §1 to §4 give them: one claim per rule and act,
// whose evidence is the act's record; a rule that passed is met and one
// that failed is not_met, both SATISFIED; a rule the engine could not
// evaluate is not_evaluable with sufficiency INSUFFICIENT; a rule that does
// not apply to the act has no claim and counts in
// aggregate.coverage.excluded_not_applicable. Nothing rolls a rule up to
// one verdict. Every claim is tier recomputed and grade self-attested.
// contract_ref is the pack's id with its last "/" read as "@" (a pack
// "acme/rules/1.2.0" reports "acme/rules@1.2.0"), and generated_at is the
// last act's committed time, so calls over the same acts seal one Result.
//
// A book with no acts, or a pack whose id has no version, is refused with
// ErrInvalid.
func (b *Book) EvaluatePack(ctx context.Context, p pack.Pack, opts EvaluateOptions) (Report, error) {
	contract, err := packContractRef(p.ID)
	if err != nil {
		return Report{}, err
	}
	acts, err := b.Query(ctx, Filter{RecordType: RecordTypeAgentAct})
	if err != nil {
		return Report{}, err
	}
	if len(acts) == 0 {
		return Report{}, fmt.Errorf("%w: the book holds no act sealed by RecordAct, so there is nothing to evaluate", ErrInvalid)
	}
	engine, err := guard.NewEngine(p)
	if err != nil {
		return Report{}, fmt.Errorf("%w: the rules pack %s: %v", ErrInvalid, p.ID, err)
	}
	result, err := evaluateActs(engine, contract, acts)
	if err != nil {
		return Report{}, err
	}
	doc, err := evidenceresult.Decode(result)
	if err == nil {
		_, err = evidenceresult.Check(doc, nil)
	}
	if err != nil {
		// The projection is this package's; a Result it builds that the
		// gate refuses is a bug here, never the caller's input.
		return Report{}, fmt.Errorf("evidencebook: the Result over the acts is not a valid Evidence Result v0: %w", err)
	}
	canonical, err := canonicalStatement(result)
	if err != nil {
		return Report{}, err
	}
	links := make([]Link, len(acts))
	for i, act := range acts {
		links[i] = Link{Type: Cites, Target: act.RecordID}
	}
	b.reportMu.Lock()
	reuse := func() (Record, bool, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, record := range b.logLocked() {
			if record.Header.RecordType != RecordTypeEvidenceResult || record.Header.SubjectRef != contract {
				continue
			}
			if existing, err := canonicalStatement(record.Header.Statement); err == nil && bytes.Equal(existing, canonical) {
				return record, true, nil
			}
		}
		return Record{}, false, nil
	}
	sealed, _, _, err := b.SealResult(ctx, canonical, contract, links, reuse, func(entry Entry) (Record, error) { return b.Append(ctx, entry) })
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

// packContractRef is a pack id in Evidence Result's <contract_id>@<version>
// spelling: its last "/" becomes "@". The pack keeps its one identity, and
// this is derived from it, never chosen here.
func packContractRef(id string) (string, error) {
	i := strings.LastIndex(id, "/")
	if i <= 0 || i == len(id)-1 || strings.ContainsAny(id, "@ \t\r\n") {
		return "", fmt.Errorf("%w: the rules pack id %q is not <id>/<version>, so a Result cannot name its contract", ErrInvalid, id)
	}
	return id[:i] + "@" + id[i+1:], nil
}

// The Evidence Result v0 document EvaluatePack seals. Field order is the
// struct order; every list is present, empty or not.
type actResult struct {
	ResultVersion string       `json:"result_version"`
	GeneratedAt   string       `json:"generated_at"`
	Claims        []actClaim   `json:"claims"`
	Aggregate     actAggregate `json:"aggregate"`
}

type actDigest struct {
	DigestAlg string `json:"digest_alg"`
	Digest    string `json:"digest"`
}

type actCarrier struct {
	Kind     string      `json:"kind"`
	Status   string      `json:"status"`
	Evidence []actDigest `json:"evidence"`
}

type actClaim struct {
	ID             string      `json:"id"`
	ContractRef    string      `json:"contract_ref"`
	RequirementRef string      `json:"requirement_ref"`
	Tier           string      `json:"tier"`
	Grade          string      `json:"grade"`
	Sufficiency    string      `json:"sufficiency"`
	Verdict        string      `json:"verdict"`
	Evidence       []actDigest `json:"evidence"`
	Proofs         []actDigest `json:"proofs"`
	Presentation   actCarrier  `json:"presentation"`
}

type actAggregate struct {
	Coverage struct {
		EvaluatedPopulation   int `json:"evaluated_population"`
		ExcludedNotApplicable int `json:"excluded_not_applicable"`
		UnknownCount          int `json:"unknown_count"`
	} `json:"coverage"`
	Buckets struct {
		Met          []string `json:"met"`
		NotMet       []string `json:"not_met"`
		NotEvaluable []string `json:"not_evaluable"`
	} `json:"buckets"`
}

// evaluateActs checks each act against the ones before it and projects the
// per-rule verdicts into the Result document.
func evaluateActs(engine *guard.Engine, contract string, acts []Record) (json.RawMessage, error) {
	var out actResult
	out.ResultVersion = "evidence-result-v0"
	out.Claims = []actClaim{}
	out.Aggregate.Buckets.Met, out.Aggregate.Buckets.NotMet, out.Aggregate.Buckets.NotEvaluable = []string{}, []string{}, []string{}
	var history guard.MemoryHistory
	for _, record := range acts {
		action, err := guard.DecodeAction(bytes.NewReader(record.Header.Statement))
		if err != nil {
			return nil, fmt.Errorf("%w: act record %s no longer reads as an action: %v", ErrInvalid, record.RecordID, err)
		}
		decision, err := engine.Check(action, history)
		if err != nil {
			return nil, fmt.Errorf("evidencebook: the rules engine on act record %s: %w", record.RecordID, err)
		}
		cited := []actDigest{{DigestAlg: "SHA-256", Digest: record.RecordID}}
		for _, rule := range engine.Report(action, decision).Rules {
			claim := actClaim{
				ID:          "act-" + strconv.FormatUint(record.Seq, 10) + "/" + rule.Rule,
				ContractRef: contract, RequirementRef: rule.Rule,
				Tier: "recomputed", Grade: "self-attested",
				Evidence: cited, Proofs: []actDigest{},
			}
			switch rule.Verdict {
			case guard.VerdictNotApplicable:
				out.Aggregate.Coverage.ExcludedNotApplicable++
				continue
			case guard.VerdictPass:
				claim.Sufficiency, claim.Verdict = "SATISFIED", "met"
				out.Aggregate.Buckets.Met = append(out.Aggregate.Buckets.Met, claim.ID)
			case guard.VerdictFail:
				claim.Sufficiency, claim.Verdict = "SATISFIED", "not_met"
				out.Aggregate.Buckets.NotMet = append(out.Aggregate.Buckets.NotMet, claim.ID)
			case guard.VerdictNotEvaluable:
				claim.Sufficiency, claim.Verdict = "INSUFFICIENT", "not_evaluable"
				out.Aggregate.Buckets.NotEvaluable = append(out.Aggregate.Buckets.NotEvaluable, claim.ID)
			default:
				return nil, fmt.Errorf("evidencebook: the rules engine gave rule %s the verdict %q, which no Result verdict maps", rule.Rule, rule.Verdict)
			}
			claim.Presentation = actCarrier{Kind: "disclosure", Status: claim.Sufficiency, Evidence: cited}
			out.Claims = append(out.Claims, claim)
			out.Aggregate.Coverage.EvaluatedPopulation++
		}
		history = append(history, guard.RecordFor(action, decision))
	}
	last := acts[len(acts)-1]
	at, err := time.Parse(time.RFC3339Nano, last.Header.CommittedAt)
	if err != nil {
		return nil, fmt.Errorf("evidencebook: act record %s has no readable committed_at: %w", last.RecordID, err)
	}
	out.GeneratedAt = at.UTC().Truncate(time.Second).Format(time.RFC3339)
	if len(out.Claims) == 0 {
		return nil, errors.Join(ErrInvalid, fmt.Errorf("no rule of the pack applies to any of the %d acts, so there is no claim to report", len(acts)))
	}
	return json.Marshal(out)
}
