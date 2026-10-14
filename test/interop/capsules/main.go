// SPDX-License-Identifier: Apache-2.0

// Command capsules writes every record capsule a fresh book seals when its
// records carry each link type, and when it bundles them, as one JSON array
// for the producer-side registry conformance check (AAC
// agent_action_capsule.registry_conformance). It can also write the bundle.
//
//	go run ./test/interop/capsules <dir> <capsules.json> [bundle.json]
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/evidencebook"
)

func main() {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: capsules <dir> <capsules.json> [bundle.json]")
		os.Exit(2)
	}
	bundleOut := ""
	if len(os.Args) == 4 {
		bundleOut = os.Args[3]
	}
	if err := run(context.Background(), os.Args[1], os.Args[2], bundleOut); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func key(seed byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed + byte(i)
	}
	return ed25519.NewKeyFromSeed(s)
}

func run(ctx context.Context, dir, out, bundleOut string) error {
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), "links-log", key(102))
	if err != nil {
		return err
	}
	payloads, err := evidencebook.OpenPayloadDir(filepath.Join(dir, "payloads"))
	if err != nil {
		return err
	}
	signer, err := evidencebook.NewEd25519Signer(key(2))
	if err != nil {
		return err
	}
	clock := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	book, err := evidencebook.Open(ctx, evidencebook.Config{
		BookID: "links", Operator: "links-operator", Store: store, Substrate: substrate, Payloads: payloads, Signer: signer,
		Now: func() time.Time { clock = clock.Add(time.Second); return clock },
	})
	if err != nil {
		return err
	}
	defer func() { _ = book.Release() }()
	root, err := appendEveryLinkType(ctx, book)
	if err != nil {
		return err
	}
	// Bundling appends the book's own records (index roots and a disclosure
	// citing the selected set), so it runs before the capsules are listed.
	bundle, err := book.Bundle(ctx, evidencebook.BundleRequest{Root: root})
	if err != nil {
		return err
	}
	if bundleOut != "" {
		if err := os.WriteFile(bundleOut, bundle.JSON, 0o600); err != nil {
			return err
		}
	}
	stored, err := store.Records(ctx)
	if err != nil {
		return err
	}
	capsules := make([]json.RawMessage, 0, len(stored))
	for _, record := range stored {
		capsules = append(capsules, record.Capsule)
	}
	data, err := json.Marshal(capsules)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o600)
}

// appendEveryLinkType appends one record per link type, in registry order
// where the record model allows it, and returns the last record's id.
func appendEveryLinkType(ctx context.Context, book *evidencebook.Book) (string, error) {
	add := func(recordType string, epistemic evidencebook.EpistemicType, links ...evidencebook.Link) (string, error) {
		if links == nil {
			links = []evidencebook.Link{}
		}
		record, err := book.Append(ctx, evidencebook.Entry{RecordType: recordType, EpistemicType: epistemic, SubjectRef: "order-17", Links: links})
		return record.RecordID, err
	}
	link := func(t evidencebook.LinkType, target string) evidencebook.Link {
		return evidencebook.Link{Type: t, Target: target}
	}
	observed, err := add("observation", evidencebook.ObservedEvent)
	if err != nil {
		return "", err
	}
	claim, err := add("claim", evidencebook.ProducerClaim, link(evidencebook.Cites, observed))
	if err != nil {
		return "", err
	}
	corrected, err := add("claim", evidencebook.ProducerClaim, link(evidencebook.Supersedes, claim))
	if err != nil {
		return "", err
	}
	closed, err := add("close", evidencebook.ProducerClaim, link(evidencebook.Closes, corrected))
	if err != nil {
		return "", err
	}
	acknowledged, err := add("acknowledgement", evidencebook.HumanReport, link(evidencebook.Acknowledges, closed))
	if err != nil {
		return "", err
	}
	rebutted, err := add("rebuttal", evidencebook.HumanReport, link(evidencebook.Rebuts, closed))
	if err != nil {
		return "", err
	}
	return add("adjudication", evidencebook.Adjudication,
		link(evidencebook.Adjudicates, acknowledged), link(evidencebook.Adjudicates, rebutted), link(evidencebook.Cites, closed))
}
