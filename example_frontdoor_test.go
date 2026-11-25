// SPDX-License-Identifier: Apache-2.0

package evidencebook_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/action-state-group/capsule-engine/go/pack"
	"github.com/action-state-group/evidencebook"
)

// The front door, record and evaluate: RecordAct seals each act of the
// owner's agent, in capsule-engine's action form, and EvaluatePack runs a
// rules pack over them and seals the Evidence Result, returning the bundle
// report.Build renders (docs/GUIDE.md, "Start here").
//
// The acts are three purchases; the pack is capsule-engine's Everyday Rules
// 0.3.8. A rule that does not apply to an act has no claim and is counted as
// excluded; nothing rolls a rule up to one verdict.
func ExampleBook_EvaluatePack() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "evidencebook-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	alice := openNode(ctx, dir, "alice", 1)
	defer alice.book.Release()

	for _, act := range []string{
		`{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:00:00Z","amount_minor":500,"currency":"USD","target":"shop-a","action_class":"money.purchase"}`,
		`{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:01:00Z","amount_minor":600,"currency":"USD","target":"shop-a","action_class":"money.purchase"}`,
		`{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:02:00Z","amount_minor":700,"currency":"USD","target":"shop-b","action_class":"money.purchase"}`,
	} {
		if _, err := alice.book.RecordAct(ctx, json.RawMessage(act)); err != nil {
			log.Fatal(err)
		}
	}

	rules, err := pack.LoadEveryday038()
	if err != nil {
		log.Fatal(err)
	}
	report, err := alice.book.EvaluatePack(ctx, rules, evidencebook.EvaluateOptions{})
	if err != nil {
		log.Fatal(err)
	}
	var result struct {
		Aggregate struct {
			Coverage struct {
				Evaluated int `json:"evaluated_population"`
				Excluded  int `json:"excluded_not_applicable"`
			} `json:"coverage"`
			Buckets map[string][]string `json:"buckets"`
		} `json:"aggregate"`
	}
	if err := json.Unmarshal(report.Result.Header.Statement, &result); err != nil {
		log.Fatal(err)
	}
	if _, err := evidencebook.VerifyBundle(report.Bundle.JSON); err != nil {
		log.Fatal(err)
	}
	fmt.Println("contract:", report.Result.Header.SubjectRef)
	fmt.Println("evaluations:", result.Aggregate.Coverage.Evaluated, "excluded as not applicable:", result.Aggregate.Coverage.Excluded)
	fmt.Println("met:", len(result.Aggregate.Buckets["met"]), "not met:", len(result.Aggregate.Buckets["not_met"]), "not evaluable:", len(result.Aggregate.Buckets["not_evaluable"]))
	fmt.Println("bundle verifies: true")
	// Output:
	// contract: asg/everyday@0.3.8
	// evaluations: 21 excluded as not applicable: 63
	// met: 6 not met: 6 not evaluable: 9
	// bundle verifies: true
}
