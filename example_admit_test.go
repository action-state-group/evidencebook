// SPDX-License-Identifier: Apache-2.0

package evidencebook_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/action-state-group/capsule-engine/go/admission"
	"github.com/action-state-group/capsule-engine/go/canon"
	"github.com/action-state-group/evidencebook"
)

// Path 5, admit a peer: verify the staple, evaluate the admission pack,
// seal the decision. docs/GUIDE.md walks the steps.
//
// The staple is the standard_history/1 vector of testdata/staple/staples.json.
// The pack and its cases are admission-baseline@1 (testdata/admission-baseline,
// VECTORS.md): the builder wrote the pack's rows and thresholds, and
// Evaluate computes the band, the tier, the card's freshness and each row's
// denominator. A case's input is what the caller verified about the
// counterparty, as the vector states it.
func Example_admitAPeer() {
	ctx := context.Background()

	// 1. Verify the staple. CheckStaple checks the presenter, the request and
	// the card's form; the card's own properties and the nonce's freshness
	// are the reader's.
	var staples []struct{ Name, Holder, Request, Reply string }
	readJSON("testdata/staple/staples.json", &staples)
	var stapled struct{ Name, Holder, Request, Reply string }
	for _, v := range staples {
		if v.Name == "standard_history/1" {
			stapled = v
		}
	}
	check, err := evidencebook.CheckStaple(evidencebook.Staple{
		Request: evidencebook.Message{Octets: []byte(stapled.Request), MediaType: evidencebook.MediaTypeJSON},
		Reply:   evidencebook.Reply{Message: evidencebook.Message{Octets: []byte(stapled.Reply), MediaType: evidencebook.MediaTypeJSON}},
	}, stapled.Holder)
	if err != nil {
		log.Fatal(err)
	}
	// The card's counts become Inputs.Card.Bands and .Disputes.
	bands, disputes, err := admission.StandardHistoryCounts(check.Artifact)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("staple: %s, %d bands, %d of %d disputes open\n", check.Derivation, len(bands), disputes.Open, disputes.Of)

	// 2. Load the pack. LoadPinned(src, id, digest) refuses a pack that is
	// not the one the builder pinned.
	src, err := os.ReadFile("testdata/admission-baseline/pack-definition.json")
	if err != nil {
		log.Fatal(err)
	}
	pack, err := admission.Load(src)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("pack:", pack.ID)

	// 3. Evaluate one deal per case: a GO, an ASK, a LIMIT after a refusal.
	var corpus struct {
		Cases []struct {
			Case  string          `json:"case"`
			Input json.RawMessage `json:"input"`
		} `json:"cases"`
	}
	readJSON("testdata/admission-baseline/cases.json", &corpus)
	input := func(name string) admission.Inputs {
		for _, c := range corpus.Cases {
			if c.Case == name {
				in, err := admission.DecodeInputs(c.Input)
				if err != nil {
					log.Fatal(err)
				}
				return in
			}
		}
		log.Fatalf("no case %s", name)
		return admission.Inputs{}
	}
	dir, err := os.MkdirTemp("", "evidencebook-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	me := openNode(ctx, dir, "goose", 1)
	defer me.book.Release()
	var granted admission.Decision
	for _, name := range []string{"t1-stranger-climbs-on-its-own-card", "t1-no-card-asks", "t1-card-refused-limits"} {
		d := admission.Evaluate(pack, input(name))
		fmt.Printf("%s: %s at %s, decided by %s", name, d.Verdict, d.Tier, d.Source)
		for _, a := range d.Asks {
			// The ask is the Evidence Request to send next: Book.Request
			// records that this book asked, and returns the octets to
			// transmit. The deadline is when to record an absence.
			one := uint64(1)
			sent, err := me.book.Request(ctx, evidencebook.EvidenceRequest{
				Subject: evidencebook.Subject{Kind: evidencebook.SubjectFullHistory}, Derivation: a.Derivation,
				Coverage: evidencebook.Coverage{MinFreshness: &evidencebook.Freshness{Size: &one}},
			}, "mesh-node")
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf(", ask %s within %ds (request recorded: %v)", a.Derivation, a.DeadlineS, sent.RecordID != "")
		}
		fmt.Println()
		if d.Verdict == "GO" {
			granted = d
		}
	}

	// 4. Seal a decision in the book. Block is the admission_decision block;
	// it holds the pack by its digest and no counterparty text.
	block, err := canon.JCS(granted.Block())
	if err != nil {
		log.Fatal(err)
	}
	sealed, err := me.book.Append(ctx, evidencebook.Entry{
		RecordType: "admission_decision", EpistemicType: evidencebook.DerivedMetric,
		SubjectRef: "mesh-node", Statement: json.RawMessage(block),
	})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := me.book.Checkpoint(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("sealed:", sealed.Header.RecordType)
	// Output:
	// staple: standard_history/1, 2 bands, 0 of 0 disputes open
	// pack: asg/admission_baseline/1.0.0
	// t1-stranger-climbs-on-its-own-card: GO at T1, decided by rows
	// t1-no-card-asks: ASK at T0, decided by rows, ask served_summary/1 within 86400s (request recorded: true)
	// t1-card-refused-limits: LIMIT at T0, decided by rows
	// sealed: admission_decision
}

func readJSON(path string, v any) {
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		log.Fatal(err)
	}
}
