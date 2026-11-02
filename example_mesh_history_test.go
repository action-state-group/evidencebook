// SPDX-License-Identifier: Apache-2.0

package evidencebook_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/inferencehistory"
	"github.com/action-state-group/evidencebook"
)

// The Mesh node's History, inference-service-history/0 (docs/GUIDE.md, path 4):
// the node's one stream records the exchanges it served, and at a period
// boundary the node answers with its history_card/1 and the exchanges it
// served per closed week and model. Go checks the History with
// agent-action-capsule's inferencehistory; the derivation is the Mesh
// plugin's, which the Rust crate checks against the library
// (rust/interop/tests/mesh_history.rs), so this file derives the same counts
// from the stream itself.

const (
	meshHistoryDefinition = "testdata/mesh-history/service-history-1.json"
	meshLogID             = "mesh/node"
)

// meshContract is the Evidence Contract the relying party evaluates the
// answer under: one requirement, which the History's checks do not reach.
var meshContract = map[string]any{
	"id": "S", "version": "1",
	"requirements": []any{map[string]any{
		"id": "r-service", "profile": "outcome",
		"statement":             "The node's served exchanges are shown under a verified envelope.",
		"evidence_requirements": map[string]any{"accepted_epistemic_types": []any{"producer_claim"}},
	}},
}

// meshServes appends the exchanges the Mesh node served to its stream: n of
// model, each a record the node states, as the plugin's ledger does.
func meshServes(ctx context.Context, book *evidencebook.Book, model string, n int) {
	statement, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		log.Fatal(err)
	}
	for range n {
		if _, err := book.Append(ctx, evidencebook.Entry{
			RecordType: "served", EpistemicType: evidencebook.ObservedEvent,
			Links: []evidencebook.Link{}, Statement: statement,
		}); err != nil {
			log.Fatal(err)
		}
	}
}

// meshPolicy is the definition's period_policy (ISH §6.1): the weeks the
// node counts, from the epoch.
type meshPolicy struct {
	Epoch   string `json:"epoch"`
	Floor   uint64 `json:"floor"`
	LengthS int64  `json:"length_s"`
}

func (p meshPolicy) unix(t string) int64 {
	at, err := time.Parse(time.RFC3339, t)
	if err != nil {
		log.Fatal(err)
	}
	return at.Unix()
}

// periodOf is the period holding t, counted from the epoch.
func (p meshPolicy) periodOf(t int64) int64 {
	d := t - p.unix(p.Epoch)
	k := d / p.LengthS
	if d%p.LengthS < 0 {
		k--
	}
	return k
}

func (p meshPolicy) onBoundary(t int64) bool { return (t-p.unix(p.Epoch))%p.LengthS == 0 }

func (p meshPolicy) start(k int64) string {
	return time.Unix(p.unix(p.Epoch)+k*p.LengthS, 0).UTC().Format("2006-01-02T15:04:05Z")
}

// cut is one checkpoint of the stream: its time and the records it covers.
type cut struct {
	at     time.Time
	leaves uint64
}

// periodCounts is the period_counts member (ISH §6) at the anchor, the last
// cut: the exchanges served in each closed period, per model, with the
// floor and complementary suppression applied. A checkpoint commits its
// records into the period holding its time, never earlier than the one
// before; the first at a boundary commits into the period it closes.
func periodCounts(policy meshPolicy, policyDigest string, chain []cut, model func(leaf uint64) string) map[string]any {
	anchor := chain[len(chain)-1].at.Unix()
	if !policy.onBoundary(anchor) {
		log.Fatal("the anchor is not at a period boundary")
	}
	lastCounted := policy.periodOf(anchor) - 1
	commits := make([]int64, len(chain))
	committedAt, closed := int64(-1<<62), int64(-1<<62)
	for i, c := range chain {
		committedAt = max(committedAt, c.at.Unix())
		k := policy.periodOf(committedAt)
		if policy.onBoundary(committedAt) && k-1 > closed {
			k--
		}
		closed = max(closed, policy.periodOf(committedAt)-1)
		commits[i] = k
	}
	first := lastCounted + 1
	for i, c := range chain {
		if c.leaves > 0 {
			first = commits[i]
			break
		}
	}
	counted := make([]map[string]uint64, lastCounted-first+1)
	for i := range counted {
		counted[i] = map[string]uint64{}
	}
	from := uint64(0)
	for i, c := range chain {
		for leaf := from; leaf < c.leaves && commits[i] <= lastCounted; leaf++ {
			if m := model(leaf); m != "" {
				counted[commits[i]-first][m]++
			}
		}
		from = max(from, c.leaves)
	}
	periods := make([]any, len(counted))
	for i, byModel := range counted {
		k := first + int64(i)
		periods[i] = map[string]any{"from": policy.start(k), "to": policy.start(k + 1), "cells": meshCells(byModel, policy.Floor)}
	}
	return map[string]any{"policy": policyDigest, "periods": periods}
}

// meshCells is one period's cells: one per model by name, then the total,
// each suppressed under ISH §6.2.
func meshCells(byModel map[string]uint64, floor uint64) []any {
	models := make([]string, 0, len(byModel))
	var total uint64
	for m, n := range byModel {
		models = append(models, m)
		total += n
	}
	sort.Strings(models)
	type cell struct {
		model string // empty: the total
		n     uint64
		why   string
	}
	cells := make([]*cell, 0, len(models)+1)
	suppressed := 0
	for _, m := range models {
		c := &cell{model: m, n: byModel[m]}
		if c.n < floor {
			c.why = "below_floor"
			suppressed++
		}
		cells = append(cells, c)
	}
	sum := &cell{n: total}
	if total < floor {
		sum.why = "below_floor"
	}
	// A published total beside exactly one suppressed model gives that
	// model away: the smallest other published model goes with it.
	if sum.why == "" && suppressed == 1 {
		var smallest *cell
		for _, c := range cells {
			if c.why == "" && (smallest == nil || c.n < smallest.n) {
				smallest = c
			}
		}
		if smallest != nil {
			smallest.why = "complementary"
		}
	}
	out := make([]any, 0, len(cells)+1)
	for _, c := range append(cells, sum) {
		entry := map[string]any{"label": inferencehistory.SelfReported, "unit": "exchanges_served", "dimension": nil, "value": nil}
		if c.model != "" {
			entry["dimension"] = map[string]any{"model": c.model}
		}
		if c.why != "" {
			entry["suppressed"] = c.why
		} else {
			entry["value"] = c.n
		}
		out = append(out, entry)
	}
	return out
}

// meshHistory serves the Mesh node's History at a period boundary and
// checks it as a relying party does, with inferencehistory. It returns the
// lines the example prints.
func meshHistory(ctx context.Context, mesh *party, book *evidencebook.Book, boundary time.Time) []string {
	raw, err := os.ReadFile(meshHistoryDefinition)
	if err != nil {
		log.Fatal(err)
	}
	definition, err := evidencerequest.DecodeJSON(raw)
	if err != nil {
		log.Fatal(err)
	}
	jcs, err := evidencerequest.EncodeJCS(definition)
	if err != nil {
		log.Fatal(err)
	}
	derivation := hexSHA256(jcs)
	var parsed struct {
		Policy json.RawMessage `json:"period_policy"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Fatal(err)
	}
	var policy meshPolicy
	if err := json.Unmarshal(parsed.Policy, &policy); err != nil {
		log.Fatal(err)
	}
	policyDecoded, err := evidencerequest.DecodeJSON(parsed.Policy)
	if err != nil {
		log.Fatal(err)
	}
	policyJCS, err := evidencerequest.EncodeJCS(policyDecoded)
	if err != nil {
		log.Fatal(err)
	}

	// The node cuts its checkpoint at the boundary and answers its
	// history_card/1 there.
	anchor, err := book.CheckpointAt(ctx, boundary)
	if err != nil {
		log.Fatal(err)
	}
	cardRequest := mustJCS(map[string]any{
		"subject": map[string]any{"full_history": nil}, "derivation": evidencebook.DerivationHistoryCard,
		"coverage": map[string]any{"min_freshness": uint64(1)},
		"nonce":    "mesh-history-card", "profile": "evidence-interop/0",
	})
	reply, _, err := book.Respond(ctx, evidencebook.Message{Octets: cardRequest, MediaType: evidencebook.MediaTypeJSON},
		evidencebook.RespondOptions{Policy: evidencebook.DefaultSharePolicy()})
	if err != nil {
		log.Fatal(err)
	}
	received, err := evidencebook.ParseReply(reply.Message)
	if err != nil || received.Envelope == nil {
		log.Fatalf("the node did not answer its history card: %v %+v", err, received.Refusal)
	}
	card := received.Envelope.Artifact
	pin := received.Envelope.Anchor.Digest

	// The stream's checkpoints through the anchor, and the model each served
	// record states.
	checkpoints, err := book.Checkpoints(ctx)
	if err != nil {
		log.Fatal(err)
	}
	var chain []cut
	for _, cp := range checkpoints {
		chain = append(chain, cut{at: cp.IssuedAt, leaves: cp.Entries})
		if cp.ID == anchor.ID {
			break
		}
	}
	served, err := book.Query(ctx, evidencebook.Filter{RecordType: "served"})
	if err != nil {
		log.Fatal(err)
	}
	models := map[uint64]string{}
	for _, r := range served {
		var statement struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(r.Header.Statement, &statement); err != nil {
			log.Fatal(err)
		}
		models[r.Header.Seq-1] = statement.Model
	}

	// The History: the counts over the stream, and the card embedded unchanged.
	var cardValue any
	if err := json.Unmarshal(card, &cardValue); err != nil {
		log.Fatal(err)
	}
	last := chain[len(chain)-1]
	history := mustJCS(map[string]any{
		"anchor": pin,
		"coverage_declaration": map[string]any{
			"interval": map[string]any{"from": nil, "to": pin}, "kind": "stream_interval", "rule": "all_records",
			"live_set": []any{map[string]any{"checkpoint": pin, "count": last.leaves, "start": uint64(0), "stream": cardValue.(map[string]any)["evidence_stream"]}},
		},
		"declared":            []any{"history_card", "period_counts"},
		"derivation":          derivation,
		"evidence_stream":     cardValue.(map[string]any)["evidence_stream"],
		"history_card":        cardValue,
		"history_card_digest": hexSHA256(card),
		"period_counts":       periodCounts(policy, hexSHA256(policyJCS), chain, func(leaf uint64) string { return models[leaf] }),
		"subject":             map[string]any{"full_history": nil},
	})

	// The node signs the History as the answer to the relying party's
	// request, which names the definition's digest as its derivation.
	request := mustJCS(map[string]any{
		"subject": map[string]any{"full_history": nil}, "derivation": derivation,
		"coverage": map[string]any{"expected_pin": pin},
		"nonce":    "mesh-history", "profile": "evidence-interop/0",
	})
	signer, err := evidencebook.NewEd25519Signer(mesh.key)
	if err != nil {
		log.Fatal(err)
	}
	envelope, err := evidencebook.Envelope{
		Profile: "evidence-interop/0", RequestDigest: hexSHA256(request),
		Subject: evidencebook.Subject{Kind: evidencebook.SubjectFullHistory}, Derivation: derivation,
		Anchor: evidencebook.Anchor{Digest: pin}, ArtifactDigest: hexSHA256(history),
		Responder: evidencebook.KeyID(signer), IssuedAt: evidencerequest.ProfileTime(boundary.Add(time.Second)),
		Artifact: history,
	}.Sign(ctx, signer)
	if err != nil {
		log.Fatal(err)
	}
	replyOctets, err := evidencerequest.EncodeReply(evidencebook.MediaTypeJSON, &envelope, nil)
	if err != nil {
		log.Fatal(err)
	}

	// The relying party checks it under the node's pinned keys.
	verifier, err := evidencerequest.NewVerifier(
		evidencerequest.TrustPolicy{ResponderKeys: []string{mesh.keys.Signer}},
		evidencerequest.VerificationPolicy{ProfilesImplemented: []string{"evidence-interop/0"}, KindsImplemented: slices.Clone(evidencerequest.CLLKinds)},
		evidencerequest.ProfileTime(boundary.Add(time.Hour)), meshContract)
	if err != nil {
		log.Fatal(err)
	}
	result, err := inferencehistory.Evaluate(verifier,
		inferencehistory.Pins{LogKeys: []string{mesh.keys.Checkpoint}},
		definition, inferencehistory.Exchange{
			Request: evidencerequest.Message{Octets: request, MediaType: evidencebook.MediaTypeJSON},
			Reply:   replyOctets.Message,
		}, nil)
	if err != nil {
		log.Fatal(err)
	}
	names := make([]string, 0, len(result.Properties))
	for name := range result.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{fmt.Sprintf("inference-service-history/0 mesh/node at %s: accepted %t", evidencerequest.ProfileTime(last.at), result.Accepted)}
	for _, name := range names {
		lines = append(lines, "  "+name+" "+result.Properties[name])
	}
	return lines
}

func mustJCS(v any) []byte {
	out, err := evidencerequest.EncodeJCS(v)
	if err != nil {
		log.Fatal(err)
	}
	return out
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var (
	_ = slices.Sort[[]int]
)
