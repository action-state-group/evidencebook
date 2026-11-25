// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/action-state-group/capsule-engine/go/pack"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/report"
)

// The front door's record and report calls, end to end (docs/GUIDE.md,
// "Start here"): Book.RecordAct seals each act of the owner's agent,
// Book.EvaluatePack runs capsule-engine's Everyday Rules 0.3.8 over them and
// seals the Evidence Result, Build renders the bundle it returns as the
// contract report, and SealPage records the page's digest in the same book.
func Example_frontDoor() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "report-front-door")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	book, err := openBook(ctx, dir)
	if err != nil {
		log.Fatal(err)
	}
	defer book.Release()

	for _, act := range []string{
		`{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:00:00Z","amount_minor":500,"currency":"USD","target":"shop-a","action_class":"money.purchase"}`,
		`{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:01:00Z","amount_minor":600,"currency":"USD","target":"shop-a","action_class":"money.purchase"}`,
		`{"verb":"pay","operator":"alice","developer":"alice-agent","timestamp":"2026-10-27T12:02:00Z","amount_minor":700,"currency":"USD","target":"shop-b","action_class":"money.purchase"}`,
	} {
		if _, err := book.RecordAct(ctx, json.RawMessage(act)); err != nil {
			log.Fatal(err)
		}
	}
	rules, err := pack.LoadEveryday038()
	if err != nil {
		log.Fatal(err)
	}
	evaluated, err := book.EvaluatePack(ctx, rules, evidencebook.EvaluateOptions{})
	if err != nil {
		log.Fatal(err)
	}

	page, err := report.Build(evaluated.Bundle.JSON, report.View{Card: "obligation", ContractReport: true})
	if err != nil {
		log.Fatal(err)
	}
	sealed, err := report.SealPage(ctx, book, page.HTML)
	if err != nil {
		log.Fatal(err)
	}
	check, err := report.CheckPage(ctx, book, page.HTML)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("verdict:", page.Verdict)
	fmt.Println("claims:", page.Claims, "unsupported:", page.UnsupportedClaims)
	fmt.Println("page sealed:", sealed.Checkpoint > 0)
	fmt.Println("CheckPage:", check.Outcome, check.Verdict)
	// Output:
	// verdict: VALID
	// claims: 21 unsupported: 0
	// page sealed: true
	// CheckPage: match VALID
}
