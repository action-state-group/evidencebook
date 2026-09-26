// SPDX-License-Identifier: Apache-2.0

// Command bundle writes an Evidence Bundle and a signed refusal produced by a
// fresh book, for verification by other implementations.
//
//	go run ./test/interop/bundle <dir> <bundle.json> [refusal.json]
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
		fmt.Fprintln(os.Stderr, "usage: bundle <dir> <bundle.json> [refusal.json]")
		os.Exit(2)
	}
	refusalOut := ""
	if len(os.Args) == 4 {
		refusalOut = os.Args[3]
	}
	if err := run(context.Background(), os.Args[1], os.Args[2], refusalOut); err != nil {
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

func run(ctx context.Context, dir, out, refusalOut string) error {
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), "interop-log", key(101))
	if err != nil {
		return err
	}
	payloads, err := evidencebook.OpenPayloadDir(filepath.Join(dir, "payloads"))
	if err != nil {
		return err
	}
	signer, err := evidencebook.NewEd25519Signer(key(1))
	if err != nil {
		return err
	}
	clock := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	book, err := evidencebook.Open(ctx, evidencebook.Config{
		BookID: "interop", Operator: "interop-operator", Store: store, Substrate: substrate, Payloads: payloads, Signer: signer,
		Now: func() time.Time { clock = clock.Add(time.Second); return clock },
	})
	if err != nil {
		return err
	}
	defer func() { _ = book.Release() }()
	observed, err := book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: "order-17", Payloads: [][]byte{[]byte(`{"status":"approved"}`)}})
	if err != nil {
		return err
	}
	if _, err := book.Checkpoint(ctx); err != nil {
		return err
	}
	claim, err := book.Append(ctx, evidencebook.Entry{RecordType: "claim", EpistemicType: evidencebook.ProducerClaim, SubjectRef: "order-17", Links: []evidencebook.Link{{Type: evidencebook.Cites, Target: observed.RecordID}}})
	if err != nil {
		return err
	}
	bundle, err := book.Bundle(ctx, evidencebook.BundleRequest{Root: claim.RecordID})
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, bundle.JSON, 0o600); err != nil {
		return err
	}
	if refusalOut == "" {
		return nil
	}
	resp, _, err := book.Respond(ctx, []byte(`{"subject":`), evidencebook.RespondOptions{Policy: evidencebook.DefaultSharePolicy()})
	if err != nil {
		return err
	}
	if resp.Refusal == nil {
		return fmt.Errorf("a malformed request was not refused")
	}
	data, err := json.Marshal(resp.Refusal)
	if err != nil {
		return err
	}
	return os.WriteFile(refusalOut, data, 0o600)
}
