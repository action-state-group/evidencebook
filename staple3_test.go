// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shared standard_history/3 staple vectors (evidencebook#579), read by
// the Go, Rust and Python checkers: a staple of the same form as
// testdata/staple/staples.json carrying a /3 card, and ones broken in a way
// every checker refuses. A Book builds no /3 card (the host counts it from a
// shelf), so the vectors are a /1 staple's holder re-signing a /3 request and
// card. EB_WRITE_STAPLE_VECTORS=1 writes them anew, for an intended change
// only: a staple carries a fresh nonce.
const standardHistory3StaplesPath = "testdata/staple-3/staples.json"

func TestStandardHistory3StapleVectors(t *testing.T) {
	if os.Getenv("EB_WRITE_STAPLE_VECTORS") == "1" {
		writeStandardHistory3Vectors(t)
	}
	var staples []stapleVector
	readJSONFile(t, standardHistory3StaplesPath, &staples)
	require.GreaterOrEqual(t, len(staples), 6)
	for _, v := range staples {
		got, err := CheckStaple(Staple{Request: Message{Octets: []byte(v.Request), MediaType: MediaTypeJSON},
			Reply: Reply{Message: Message{Octets: []byte(v.Reply), MediaType: MediaTypeJSON}}}, v.Holder)
		assert.Equal(t, v.Pass, err == nil, "%s: %v", v.Name, err)
		if err == nil {
			assert.Equal(t, v.Nonce, got.Nonce, v.Name)
			assert.Equal(t, v.Anchor, got.Anchor, v.Name)
			assert.Equal(t, DerivationStandardHistory3, got.Derivation, v.Name)
		}
	}
}

// A /3 staple carries no shelf handover, so counts_recompute stays not
// checked (SH §11.7), and a reader of /1 only refuses the /3 card (#461
// acceptance 7).
func TestStandardHistory3CardChecksWithoutAHandover(t *testing.T) {
	card := standardHistory3Card(t, strings.Repeat("b", 64))
	properties, err := standardhistory.CheckAs(standardhistory.Derivation3, card, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, standardhistory.Pass, properties["card_form"])
	assert.Equal(t, standardhistory.NotChecked, properties["counts_recompute"])
	asOne, err := standardhistory.Check(card, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, standardhistory.Fail, asOne["card_form"])
}

// A Book does not build a /3 staple: it holds no shelf facts.
func TestStapleBuildsNoStandardHistory3(t *testing.T) {
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	_, err := book.Staple(context.Background(), DerivationStandardHistory3)
	assert.ErrorIs(t, err, ErrInvalid)
}

// standardHistory3Card is a /3 card, in its JCS octets, over no deals.
func standardHistory3Card(t *testing.T, anchor string) []byte {
	t.Helper()
	asOf := standardhistory.AsOf{Anchor: anchor, AnchorTime: "2026-11-01T00:00:00Z",
		FirstCheckpointTime: "2026-01-01T00:00:00Z", CoverageStartTime: "2026-02-01T00:00:00Z"}
	policy := standardhistory.Policy{Digest: strings.Repeat("a", 64), Window: standardhistory.Window{Months: 12}}
	raw, err := standardhistory.DeriveAs(standardhistory.Derivation3, "person", "person_declared", asOf, policy, []standardhistory.Deal{})
	require.NoError(t, err)
	decoded, err := evidencerequest.DecodeJSON(raw)
	require.NoError(t, err)
	card, err := evidencerequest.EncodeJCS(decoded)
	require.NoError(t, err)
	return card
}

// A requester can ask for a /3 card and record the holder's signed answer,
// as it does for /1: the card's inclusion is not checked (IP P-13).
func TestStandardHistory3IsRecordedByTheRequester(t *testing.T) {
	ctx := context.Background()
	responder := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	pin := anchorPin(t, responder)
	requester := newFixture(t, "requester", 31).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory3, Coverage: Coverage{ExpectedPin: pin}}, "book-sh")
	require.NoError(t, err)
	// The responder serves no /3 itself, so the host that counted the card
	// signs it, here with the responder's key.
	asked := respondAs(t, responder, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: pin}}), "requester")
	card := standardHistory3Card(t, pin)
	reply := resigned(t, responder, asked, func(e *Envelope) {
		e.Derivation, e.RequestDigest = DerivationStandardHistory3, digestBytes(sent.Message().Octets)
		e.Artifact, e.ArtifactDigest = card, digestBytes(card)
	})
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 9))
	require.NoError(t, err)
	statement := string(recorded.Header.Statement)
	assert.Contains(t, statement, `"not_checked":["anchor_inclusion"]`)
	assert.Contains(t, statement, `"outcome":"artifact"`)
}

func writeStandardHistory3Vectors(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	holder := KeyID(book.signer)
	base, err := book.Staple(ctx, DerivationStandardHistory)
	require.NoError(t, err)
	got, err := CheckStaple(base, holder)
	require.NoError(t, err)
	request := func(derivation, nonce string) Message {
		return jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: derivation,
			Coverage: Coverage{ExpectedPin: got.Anchor}, Nonce: nonce, Profile: ProfileID})
	}
	derived := standardHistory3Card(t, got.Anchor)
	// The staple's reply: the holder signs the /3 request's digest and card.
	sign := func(req Message, derivation string, artifact []byte) Reply {
		return resigned(t, book, base.Reply, func(e *Envelope) {
			e.Derivation, e.RequestDigest = derivation, digestBytes(req.Octets)
			e.Artifact, e.ArtifactDigest = artifact, digestBytes(artifact)
		})
	}
	req3 := request(DerivationStandardHistory3, got.Nonce)
	good := sign(req3, DerivationStandardHistory3, derived)
	vector := func(name string, req Message, reply Reply, who string, pass bool) stapleVector {
		v := stapleVector{Name: name, Holder: who, Request: string(req.Octets), Reply: string(reply.Octets), Pass: pass}
		if pass {
			v.Nonce, v.Anchor = got.Nonce, got.Anchor
		}
		return v
	}
	broken := func(name string, edit func(map[string]any)) stapleVector {
		return vector(name, req3, widened(t, book, good, edit), holder, false)
	}
	other := standardHistoryBookSeed(t, 33)
	badValue := []byte(strings.Replace(string(derived), `"window":{"months":12}`, `"window":{"months":"12"}`, 1))
	require.NotEqual(t, string(derived), string(badValue))
	membersOnly := vector("a /3 card with the card's members and a malformed value", req3, sign(req3, DerivationStandardHistory3, badValue), holder, false)
	membersOnly.MembersOnly, membersOnly.Nonce, membersOnly.Anchor = true, got.Nonce, got.Anchor
	vectors := []stapleVector{
		vector("standard_history/3", req3, good, holder, true),
		vector("another holder", req3, good, KeyID(other.signer), false),
		vector("a /1 card under a /3 request", req3, sign(req3, DerivationStandardHistory3, base.Reply.Envelope.Artifact), holder, false),
		vector("a /3 card under a /1 request, as a reader of /1 only reads it", base.Request, sign(base.Request, DerivationStandardHistory, derived), holder, false),
		vector("a reply to another derivation", req3, base.Reply, holder, false),
		vector("another request", request(DerivationStandardHistory3, flipped(got.Nonce)), good, holder, false),
		broken("a card with more than the /3 card", func(c map[string]any) { c["counterparties"] = []any{"carol"} }),
		broken("a card without its coverage start", func(c map[string]any) { delete(c, "coverage_start") }),
		membersOnly,
		broken("a card that names /1", func(c map[string]any) { c["derivation"] = DerivationStandardHistory }),
	}
	require.NoError(t, os.MkdirAll("testdata/staple-3", 0o755))
	writeJSONFile(t, standardHistory3StaplesPath, vectors)
}

// A /3 card with the members of the card and a malformed value is refused by
// CheckStaple (Go and Python; Rust checks members only, so no shared vector).
func TestCheckStapleRefusesAMalformedStandardHistory3Card(t *testing.T) {
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	holder := KeyID(book.signer)
	base, err := book.Staple(context.Background(), DerivationStandardHistory)
	require.NoError(t, err)
	got, err := CheckStaple(base, holder)
	require.NoError(t, err)
	req := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory3,
		Coverage: Coverage{ExpectedPin: got.Anchor}, Nonce: got.Nonce, Profile: ProfileID})
	card := standardHistory3Card(t, got.Anchor)
	sign := func(artifact []byte) Staple {
		return Staple{Request: req, Reply: resigned(t, book, base.Reply, func(e *Envelope) {
			e.Derivation, e.RequestDigest = DerivationStandardHistory3, digestBytes(req.Octets)
			e.Artifact, e.ArtifactDigest = artifact, digestBytes(artifact)
		})}
	}
	_, err = CheckStaple(sign(card), holder)
	require.NoError(t, err)
	bad := []byte(strings.Replace(string(card), `"window":{"months":12}`, `"window":{"months":"12"}`, 1))
	require.NotEqual(t, string(card), string(bad))
	_, err = CheckStaple(sign(bad), holder)
	assert.ErrorIs(t, err, ErrInvalid)
}
