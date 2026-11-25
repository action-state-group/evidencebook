// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidenceresult"
	"github.com/action-state-group/capsule-engine/go/guard"
	"github.com/action-state-group/capsule-engine/go/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// purchase is an act in capsule-engine's action form: a payment of amount
// minor units to target, at minute past noon.
func purchase(target string, amount, minute int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:%02d:00Z","amount_minor":%d,"currency":"USD","target":%q,"action_class":"money.purchase"}`, minute, amount, target))
}

func everyday(t *testing.T) pack.Pack {
	t.Helper()
	p, err := pack.LoadEveryday038()
	require.NoError(t, err)
	return p
}

type resultDoc struct {
	ResultVersion string     `json:"result_version"`
	GeneratedAt   string     `json:"generated_at"`
	Claims        []actClaim `json:"claims"`
	Aggregate     struct {
		Coverage struct {
			EvaluatedPopulation   int `json:"evaluated_population"`
			ExcludedNotApplicable int `json:"excluded_not_applicable"`
			UnknownCount          int `json:"unknown_count"`
		} `json:"coverage"`
		Buckets map[string][]string `json:"buckets"`
	} `json:"aggregate"`
}

func decodeResult(t *testing.T, statement json.RawMessage) resultDoc {
	t.Helper()
	var doc resultDoc
	require.NoError(t, json.Unmarshal(statement, &doc))
	return doc
}

// Every claim is the engine's own verdict for one rule on one act, a rule
// that does not apply has no claim and is counted as excluded, and the
// Result passes the Evidence Result v0 gate.
func TestEvaluatePackProjectsEachRuleOnEachAct(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "alice", 1).open(t)
	p := everyday(t)
	inputs := []json.RawMessage{purchase("shop-a", 500, 0), purchase("shop-a", 700, 1), purchase("shop-b", 900000, 2)}
	var acts []Record
	for _, in := range inputs {
		act, err := book.RecordAct(ctx, in)
		require.NoError(t, err)
		assert.Equal(t, RecordTypeAgentAct, act.Header.RecordType)
		assert.Equal(t, ProducerClaim, act.Header.EpistemicType)
		acts = append(acts, act)
	}

	rep, err := book.EvaluatePack(ctx, p, EvaluateOptions{})
	require.NoError(t, err)
	assert.Equal(t, RecordTypeEvidenceResult, rep.Result.Header.RecordType)
	assert.Equal(t, "asg/everyday@0.3.8", rep.Result.Header.SubjectRef)
	for _, act := range acts {
		assert.Contains(t, rep.Result.Header.LinksTo(Cites), act.RecordID)
	}
	raw, err := evidenceresult.Decode(rep.Result.Header.Statement)
	require.NoError(t, err)
	_, err = evidenceresult.Check(raw, nil)
	require.NoError(t, err)

	// The expected claims, from the engine run independently here.
	engine, err := guard.NewEngine(p)
	require.NoError(t, err)
	want := map[string]string{}
	excluded := 0
	var history guard.MemoryHistory
	for i, in := range inputs {
		a, err := guard.DecodeAction(bytes.NewReader(in))
		require.NoError(t, err)
		d, err := engine.Check(a, history)
		require.NoError(t, err)
		for _, r := range engine.Report(a, d).Rules {
			id := fmt.Sprintf("act-%d/%s", acts[i].Seq, r.Rule)
			switch r.Verdict {
			case guard.VerdictNotApplicable:
				excluded++
			case guard.VerdictPass:
				want[id] = "met"
			case guard.VerdictFail:
				want[id] = "not_met"
			default:
				want[id] = "not_evaluable"
			}
		}
		history = append(history, guard.RecordFor(a, d))
	}

	doc := decodeResult(t, rep.Result.Header.Statement)
	assert.Equal(t, "evidence-result-v0", doc.ResultVersion)
	committed, err := time.Parse(time.RFC3339Nano, acts[len(acts)-1].Header.CommittedAt)
	require.NoError(t, err)
	assert.Equal(t, committed.UTC().Truncate(time.Second).Format(time.RFC3339), doc.GeneratedAt)
	got := map[string]string{}
	verdicts := map[string]int{}
	for _, c := range doc.Claims {
		got[c.ID] = c.Verdict
		verdicts[c.Verdict]++
		assert.Equal(t, "asg/everyday@0.3.8", c.ContractRef)
		assert.Equal(t, "recomputed", c.Tier)
		assert.Equal(t, "self-attested", c.Grade)
		assert.Len(t, c.Evidence, 1)
		assert.Equal(t, c.Evidence, c.Presentation.Evidence)
		if c.Verdict == "not_evaluable" {
			assert.Equal(t, "INSUFFICIENT", c.Sufficiency)
		} else {
			assert.Equal(t, "SATISFIED", c.Sufficiency)
		}
		assert.Contains(t, doc.Aggregate.Buckets[c.Verdict], c.ID)
	}
	assert.Equal(t, want, got)
	// The fixture exercises all three verdicts and the exclusion.
	assert.Positive(t, verdicts["met"])
	assert.Positive(t, verdicts["not_met"])
	assert.Positive(t, verdicts["not_evaluable"])
	assert.Positive(t, excluded)
	assert.Equal(t, excluded, doc.Aggregate.Coverage.ExcludedNotApplicable)
	assert.Equal(t, len(doc.Claims), doc.Aggregate.Coverage.EvaluatedPopulation)
	assert.Zero(t, doc.Aggregate.Coverage.UnknownCount)

	// The bundle is rooted at the Result, carries the contract report, and
	// verifies.
	_, err = VerifyBundle(rep.Bundle.JSON)
	require.NoError(t, err)
	assert.Contains(t, string(rep.Bundle.JSON), ExtensionContractReport)
}

// Running the pack again over the same acts finds the Result it sealed,
// though the first run's bundle added records to the book; a new act gives a
// new Result.
func TestEvaluatePackSealsOneResultPerSetOfActs(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "alice", 1).open(t)
	p := everyday(t)
	_, err := book.RecordAct(ctx, purchase("shop-a", 500, 0))
	require.NoError(t, err)

	first, err := book.EvaluatePack(ctx, p, EvaluateOptions{})
	require.NoError(t, err)
	again, err := book.EvaluatePack(ctx, p, EvaluateOptions{})
	require.NoError(t, err)
	assert.Equal(t, first.Result.RecordID, again.Result.RecordID)

	_, err = book.RecordAct(ctx, purchase("shop-a", 700, 1))
	require.NoError(t, err)
	later, err := book.EvaluatePack(ctx, p, EvaluateOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, first.Result.RecordID, later.Result.RecordID)
}

func TestRecordActRefusesWhatTheEngineCannotRead(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "alice", 1).open(t)
	for name, act := range map[string]string{
		"not json":     `{"verb":`,
		"no timestamp": `{"verb":"pay","operator":"alice","developer":"alice-agent"}`,
		"unknown key":  `{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:00:00Z","colour":"red"}`,
	} {
		_, err := book.RecordAct(ctx, json.RawMessage(act))
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
	assert.Zero(t, book.Size(), "a refused act seals nothing")
}

func TestEvaluatePackRefusals(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "alice", 1).open(t)
	_, err := book.EvaluatePack(ctx, everyday(t), EvaluateOptions{})
	assert.ErrorIs(t, err, ErrInvalid, "a book with no act")

	_, err = book.RecordAct(ctx, purchase("shop-a", 500, 0))
	require.NoError(t, err)
	unversioned := everyday(t)
	unversioned.ID = "everyday"
	_, err = book.EvaluatePack(ctx, unversioned, EvaluateOptions{})
	assert.ErrorIs(t, err, ErrInvalid, "a pack id with no version")
}

func TestPackContractRef(t *testing.T) {
	for id, want := range map[string]string{
		"asg/everyday/0.3.8": "asg/everyday@0.3.8",
		"acme/rules/1.2.0":   "acme/rules@1.2.0",
		"rules/1":            "rules@1",
	} {
		got, err := packContractRef(id)
		require.NoError(t, err, id)
		assert.Equal(t, want, got)
	}
	for _, id := range []string{"", "rules", "/1", "rules/", "a@b/1", "a b/1"} {
		_, err := packContractRef(id)
		assert.ErrorIs(t, err, ErrInvalid, id)
	}
}
