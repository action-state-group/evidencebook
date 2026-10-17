// SPDX-License-Identifier: BSD-3-Clause

// Command personal-history produces the positive run of the Personal-Agent
// History Profile v0 conformance vectors (spec/personal-agent-history-profile-v0.md
// §6.4) with an IP responder: evidencebook's Book.Respond, unmodified.
//
// It is not part of this repository's build. Run it from a clone of
// github.com/action-state-group/evidencebook at the commit the corpus README
// pins, the same way that repository's docs/THIRD-PARTY-VERIFIER.md runs its
// test responder:
//
//	mkdir -p test/interop/personal-history
//	cp <this file> test/interop/personal-history/main.go
//	go run ./test/interop/personal-history > fixture.json
//
// It opens a new book in a temporary directory and seeds a holder stream of
// four deals with a checkpoint after the sixth record and another after the
// eighth. It registers the Basic level definition for one recipient, and a
// second definition whose reads add statement and counterparty_ref, and then
// answers the requests the corpus needs as the requester each names. Every
// clock reading is one second after the last, from a fixed start, and every
// key is an Ed25519 test key whose seed is the SHA-256 of a public label, so
// the output is the same on every run and every machine.
//
// The keys are test keys. Never use them for anything else.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/evidencebook"
)

const (
	holderLabel = "personal-agent-history-profile-v0 conformance vectors: holder key"
	logLabel    = "personal-agent-history-profile-v0 conformance vectors: holder log key"
	logID       = "holder-stream-1"
	recipient   = "recipient-1"
	other       = "requester-2"
	profile     = "evidence-interop/0"
)

var start = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

// basicReads is the declared field set of personal_history_basic/1 (§4.1).
var basicReads = []string{"committed_at", "epistemic_type", "event_time_claim", "record_type"}

// wideReads adds statement and counterparty_ref: the §6.4 definition_form mutant.
var wideReads = []string{"committed_at", "counterparty_ref", "epistemic_type", "event_time_claim", "record_type", "statement"}

// step is one record of the holder's stream, in log order.
type step struct {
	deal string
	kind string
	// claimed is how long before its commit the holder's agent claims the step happened.
	claimed time.Duration
}

func dealRef(n int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("personal-agent-history-profile-v0 conformance vectors: deal %d", n)))
	return hex.EncodeToString(sum[:16])
}

func seeded(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(label))
	return ed25519.NewKeyFromSeed(seed[:])
}

type octets struct {
	MediaType  string `json:"media_type"`
	OctetsText string `json:"octets_text"`
}

type exchange struct {
	Name      string `json:"name"`
	Requester string `json:"requester"`
	Request   octets `json:"request"`
	Reply     octets `json:"reply"`
}

type logKey struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Kid string `json:"kid"`
}

type checkpoint struct {
	ExpectedPin string `json:"expected_pin"`
	TreeSize    uint64 `json:"tree_size"`
	LogKey      logKey `json:"log_key"`
}

type record struct {
	Digest     string `json:"digest"`
	Deal       string `json:"deal"`
	RecordType string `json:"record_type"`
}

type output struct {
	Note        string            `json:"note"`
	HolderKey   string            `json:"holder_key"`
	Recipient   string            `json:"recipient"`
	Deals       []string          `json:"deals"`
	Selection   []string          `json:"selection"`
	Records     []record          `json:"records"`
	Checkpoints []checkpoint      `json:"checkpoints"`
	Definitions map[string]string `json:"definitions"`
	Exchanges   []exchange        `json:"exchanges"`
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "personal-history:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "personal-history-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), logID, seeded(logLabel))
	if err != nil {
		return err
	}
	payloads, err := evidencebook.OpenPayloadDir(filepath.Join(dir, "payloads"))
	if err != nil {
		return err
	}
	signer, err := evidencebook.NewEd25519Signer(seeded(holderLabel))
	if err != nil {
		return err
	}
	clock := start
	now := func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	book, err := evidencebook.Open(ctx, evidencebook.Config{
		BookID: "holder", Operator: "holder-operator", Store: store, Substrate: substrate, Payloads: payloads,
		Signer: signer, Now: now,
	})
	if err != nil {
		return err
	}
	defer book.Release()

	d := []string{dealRef(1), dealRef(2), dealRef(3), dealRef(4)}
	// Deal 1 completes; deal 2 fails its second step after the first checkpoint; deal 3 completes
	// one step; deal 4 is never selected.
	steps := []step{
		{d[0], "step_completed", 40 * time.Minute},
		{d[1], "step_completed", 35 * time.Minute},
		{d[3], "step_completed", 30 * time.Minute},
		{d[0], "step_completed", 25 * time.Minute},
		{d[2], "step_completed", 20 * time.Minute},
		{d[3], "step_failed", 15 * time.Minute},
		{d[1], "step_failed", 10 * time.Minute},
		{d[3], "step_completed", 5 * time.Minute},
	}
	out := output{
		Note:        "Test keys from public labels; never use them for anything else.",
		HolderKey:   hex.EncodeToString(signer.PublicKey()),
		Recipient:   recipient,
		Deals:       d,
		Selection:   []string{d[0], d[1], d[2]},
		Definitions: map[string]string{},
	}
	slices.Sort(out.Selection) // §4.1: sorted by code point
	for i, s := range steps {
		r, err := book.Append(ctx, evidencebook.Entry{
			RecordType: s.kind, EpistemicType: evidencebook.ProducerClaim, SubjectRef: s.deal,
			EventTimeClaim: clock.Add(time.Second - s.claimed),
		})
		if err != nil {
			return err
		}
		out.Records = append(out.Records, record{Digest: r.RecordID, Deal: s.deal, RecordType: s.kind})
		if i == 5 || i == 7 {
			cp, err := book.Checkpoint(ctx)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(cp.Statement)
			out.Checkpoints = append(out.Checkpoints, checkpoint{
				ExpectedPin: hex.EncodeToString(sum[:]), TreeSize: cp.TreeSize,
				LogKey: logKey{Iss: cp.LogID, Sub: fmt.Sprintf("%s#%d", cp.LogID, cp.TreeSize), Kid: cp.KeyID},
			})
		}
	}

	define := func(name string, reads []string) (string, error) {
		doc := map[string]any{
			"completeness": "not_asserted", "kind": "projection", "name": "personal_history_basic/1",
			"reads": toAny(reads), "recipient": recipient, "selection": toAny(out.Selection),
			"subject_form": "correlation",
		}
		text, err := evidencerequest.EncodeJCS(doc)
		if err != nil {
			return "", err
		}
		out.Definitions[name] = string(text)
		return book.RegisterDerivation(evidencebook.DerivationDefinition{
			Document: text, Recipient: recipient, Forms: []evidencebook.SubjectKind{evidencebook.SubjectCorrelation},
			Fields: reads,
		})
	}
	basic, err := define("basic", basicReads)
	if err != nil {
		return err
	}
	wide, err := define("wide_reads", wideReads)
	if err != nil {
		return err
	}

	pinA, pinB := out.Checkpoints[0].ExpectedPin, out.Checkpoints[1].ExpectedPin
	ask := func(name, requester, deal, derivation, pin string) error {
		req := map[string]any{
			"coverage": map[string]any{"expected_pin": pin}, "derivation": derivation,
			"nonce": requester + "/" + name, "profile": profile, "subject": map[string]any{"correlation": deal},
		}
		text, err := evidencerequest.EncodeJCS(req)
		if err != nil {
			return err
		}
		reply, _, err := book.Respond(ctx, evidencebook.Message{Octets: text, MediaType: evidencebook.MediaTypeJSON},
			evidencebook.RespondOptions{AuthenticatedRequester: requester, Policy: evidencebook.DefaultSharePolicy()})
		if err != nil {
			return err
		}
		out.Exchanges = append(out.Exchanges, exchange{
			Name: name, Requester: requester,
			Request: octets{evidencebook.MediaTypeJSON, string(text)},
			Reply:   octets{reply.MediaType, string(reply.Octets)},
		})
		return nil
	}
	for i, deal := range out.Selection {
		if err := ask(fmt.Sprintf("history-%d", i+1), recipient, deal, basic, pinB); err != nil {
			return err
		}
	}
	others := []struct{ name, requester, deal, derivation, pin string }{
		{"not-selected", recipient, d[3], basic, pinB},
		{"other-anchor", recipient, d[0], basic, pinA},
		{"other-requester", other, d[0], basic, pinB},
	}
	for _, s := range others {
		if err := ask(s.name, s.requester, s.deal, s.derivation, s.pin); err != nil {
			return err
		}
	}
	for i, deal := range out.Selection {
		if err := ask(fmt.Sprintf("wide-reads-%d", i+1), recipient, deal, wide, pinB); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(data, '\n'))
	return err
}

func toAny(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}
