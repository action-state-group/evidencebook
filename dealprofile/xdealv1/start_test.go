// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const startDir = "../../testdata/deal-v1/start"

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

const (
	stAgreeBy = "2026-11-02T23:59:59Z"
	stBefore  = "2026-11-01T12:00:00Z"
	stAfter   = "2026-11-05T12:00:00Z"
)

// stBoth is both agreements held, matching and joined: the base of the
// cases where the offerer's agreement has or lacks a time.
func stBoth(side, at string) StartInput {
	return StartInput{At: at, Side: side, CounterpartyLayer: true, AgreeBy: stAgreeBy, CounterpartAgreementHeld: true,
		SameOfferAndRows: boolp(true), OffererNamesAcceptor: boolp(true)}
}

// The start vectors at the input level (design §5.4): each case's StartInput
// and its StartResult. Written to testdata/deal-v1/start/vectors.json
// (EB_WRITE_DEAL_V1_VECTORS=1), which the Rust and Python ports replay.
func TestStartVectors(t *testing.T) {
	type vcase struct {
		Name   string      `json:"name"`
		Why    string      `json:"why"`
		Input  StartInput  `json:"input"`
		Expect StartResult `json:"expect"`
	}
	var cases []vcase
	add := func(name, why string, in StartInput, want StartResult) {
		t.Helper()
		got, err := Start(in)
		require.NoError(t, err, name)
		require.Equal(t, want, got, name)
		cases = append(cases, vcase{name, why, in, want})
	}
	in := func(f func(*StartInput), base StartInput) StartInput { f(&base); return base }
	notStarted := StartResult{State: StateNotStarted}
	awaiting := StartResult{State: StateAwaitingAgreement}

	add("timed-in-time", "both agree, the offerer's agreement timed before agree_by: started, not local",
		in(func(s *StartInput) { s.OffererAgreementTime = strp("2026-11-01T10:00:00Z") }, stBoth(SideAcceptor, stAfter)),
		started(false, ByOffererInTime))
	add("untimed-before-agree-by", "both agree, the offerer's agreement untimed, T before agree_by: started, local",
		stBoth(SideAcceptor, stBefore), started(true, ByOffererInTime))
	add("untimed-offerer-side", "the offerer computes the same from its own book and the acceptor's agreement",
		stBoth(SideOfferer, stBefore), started(true, ByOffererInTime))
	add("untimed-after-agree-by", "the same, T after agree_by, not named: not started",
		stBoth(SideAcceptor, stAfter), notStarted)
	acceptorOnly := StartInput{At: stBefore, Side: SideAcceptor, CounterpartyLayer: true, AgreeBy: stAgreeBy}
	add("acceptor-only-before", "the acceptor holds only its own agreement, T before agree_by: awaiting agreement",
		acceptorOnly, awaiting)
	add("acceptor-only-after", "the same, T after agree_by: not started",
		in(func(s *StartInput) { s.At = stAfter }, acceptorOnly), notStarted)
	late := in(func(s *StartInput) { s.OffererAgreementTime = strp("2026-11-03T10:00:00Z") }, stBoth(SideAcceptor, stAfter))
	add("late-named", "the offerer's agreement timed after agree_by, and the acceptor's book names it: started",
		in(func(s *StartInput) { s.AcceptorNamesOfferer = true }, late), started(false, ByAcceptorNamed))
	add("late-not-named", "the offerer's agreement timed after agree_by, not named: not started", late, notStarted)
	add("cancellation-named-first", "the acceptor named the offer's cancellation, then the offerer's agreement arrived in time: not started",
		in(func(s *StartInput) {
			s.CancellationNamedFirst = true
			s.OffererAgreementTime = strp("2026-11-01T10:00:00Z")
		}, stBoth(SideAcceptor, stBefore)), notStarted)
	add("named-then-cancelled", "the acceptor named the offerer's agreement, then a cancellation: started",
		in(func(s *StartInput) { s.AcceptorNamesOfferer = true }, stBoth(SideAcceptor, stAfter)), started(false, ByAcceptorNamed))
	other := in(func(s *StartInput) { s.SameOfferAndRows = boolp(false) }, stBoth(SideAcceptor, stBefore))
	add("other-rows-before", "two agreements citing different rows digests: the other is set aside; awaiting before agree_by",
		other, awaiting)
	add("other-rows-after", "the same after agree_by: not started",
		in(func(s *StartInput) { s.At = stAfter }, other), notStarted)
	add("offerer-names-another", "the offerer's agreement names another record than the acceptor's agreement: it does not join; awaiting before agree_by",
		in(func(s *StartInput) { s.OffererNamesAcceptor = boolp(false) }, stBoth(SideAcceptor, stBefore)), awaiting)
	noLayer := StartInput{At: stAfter, Side: SideAcceptor, AgreeBy: stAgreeBy}
	add("no-layer-before-act", "a counterparty with no layer, before the holder's first act: not started", noLayer, notStarted)
	add("no-layer-timed-act", "no layer, after a timed act: started, not local, by first act",
		in(func(s *StartInput) { s.FirstAct = &FirstAct{Time: strp("2026-11-01T10:15:00Z")} }, noLayer), started(false, ByFirstAct))
	add("no-layer-untimed-act", "no layer, after an untimed act: started, local; agree_by plays no part",
		in(func(s *StartInput) { s.FirstAct = &FirstAct{} }, noLayer), started(true, ByFirstAct))

	got, err := json.MarshalIndent(map[string]any{
		"description": "start (evidencebook#461, docs/design/461-deal-profile.md §5.2 and §5.4): Start(input) must give expect for each case. Each input but at is the status value's start member. The cases where StartFromBooks derives the input from the two books (an untimed offerer agreement, one timed only by a witness the agreement does not name, an empty witness list) are its own vectors. Written by dealprofile/xdealv1's TestStartVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(startDir, "vectors.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(startDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "start vectors.json differs from this build's; regenerate only for an intended change")
}

func TestStartRefuses(t *testing.T) {
	for name, in := range map[string]StartInput{
		"bad at":       {At: "2026-11-01 12:00:00", Side: SideAcceptor, CounterpartyLayer: true, AgreeBy: stAgreeBy},
		"bad agree_by": {At: stBefore, Side: SideAcceptor, CounterpartyLayer: true, AgreeBy: "tomorrow"},
		"bad side":     {At: stBefore, Side: "buyer", CounterpartyLayer: true, AgreeBy: stAgreeBy},
		"offerer without the acceptor's agreement": {At: stBefore, Side: SideOfferer, CounterpartyLayer: true, AgreeBy: stAgreeBy},
		"bad offerer time": func() StartInput {
			s := stBoth(SideAcceptor, stBefore)
			s.OffererAgreementTime = strp("2026-11-31T00:00:00Z")
			return s
		}(),
		"bad first act time": {At: stBefore, FirstAct: &FirstAct{Time: strp("noon")}},
	} {
		_, err := Start(in)
		assert.Error(t, err, name)
	}
}
