// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const declineDir = "../../testdata/deal-v1/decline"

func testKey(seed byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
}

func keyHex(k ed25519.PrivateKey) string { return hex.EncodeToString(k.Public().(ed25519.PublicKey)) }

// sealSent seals a record as a standalone sent Capsule under key and
// returns its pair; actionID "" is the record's own "<deal_id>/sent".
func sealSent(t *testing.T, r Record, key ed25519.PrivateKey, actionID string) []byte {
	t.Helper()
	return sealSentWith(t, r, key, actionID, nil)
}

// sealSentWith is sealSent with a chain to a parent Capsule.
func sealSentWith(t *testing.T, r Record, key ed25519.PrivateKey, actionID string, chain *emit.Chain, refs ...emit.Reference) []byte {
	t.Helper()
	payload, _, err := Encode(r)
	require.NoError(t, err)
	if actionID == "" {
		actionID = r.Block.DealID + SentSuffix
	}
	identity, err := emit.NewEd25519SigningIdentity(key)
	require.NoError(t, err)
	sealed, err := emit.Seal(emit.SealInput{
		Capsule: emit.Input{ActionID: actionID, ActionType: emit.ActionTypeFYI, Operator: "seller-agent", Developer: "xdealv1-test",
			Timestamp: time.Date(2026, 11, 1, 10, 5, 0, 0, time.UTC), Chain: chain, References: refs},
		Payload: json.RawMessage(payload), Identity: identity,
	})
	require.NoError(t, err)
	pair, err := Pair(sealed.Payload, sealed.Envelope, payload)
	require.NoError(t, err)
	return pair
}

// The decline and cancellation vectors (design §3.5): a decline signed by
// the key the offer was sent to, a cancellation by the offer's signer, and
// the offer itself, each verifying; and each refusal §3.5 lists. Written to
// testdata/deal-v1/decline/vectors.json (EB_WRITE_DEAL_V1_VECTORS=1).
func TestSentVectors(t *testing.T) {
	seller, buyer := testKey(11), testKey(12)
	offerB := offerBody()
	offerB.To = Some(keyHex(seller))
	offer := record(blk(TypePropose), offerB)
	od, err := Digest(offer)
	require.NoError(t, err)
	decline := record(blk(TypeAgree), &AgreeBody{Kind: KindDecline, Offer: RefTo(od), Reason: ReasonNotSel})
	cancel := record(blk(TypeAgree), &AgreeBody{Kind: KindCancellation, Offer: RefTo(od)})
	ctx := &OfferContext{Offer: offer, Signer: keyHex(buyer)}

	type vcase struct {
		Name        string          `json:"name"`
		Pair        json.RawMessage `json:"pair"`
		Offer       json.RawMessage `json:"offer,omitempty"`
		OfferSigner string          `json:"offer_signer,omitempty"`
		Valid       bool            `json:"valid"`
		Stage       string          `json:"stage,omitempty"`
		Digest      string          `json:"digest,omitempty"`
	}
	offerRaw, err := json.Marshal(offer)
	require.NoError(t, err)
	var cases []vcase
	run := func(name string, pair []byte, withOffer bool, wantStage string) {
		t.Helper()
		var oc *OfferContext
		c := vcase{Name: name, Pair: pair, Valid: wantStage == ""}
		if withOffer {
			oc, c.Offer, c.OfferSigner = ctx, offerRaw, ctx.Signer
		}
		got, err := VerifySent(pair, oc)
		if wantStage == "" {
			require.NoError(t, err, name)
			c.Digest = got.Digest
		} else {
			require.Error(t, err, name)
			assert.Equal(t, wantStage, stageOf(err), "%s: %v", name, err)
			c.Stage = wantStage
		}
		cases = append(cases, c)
	}

	declinePair := sealSent(t, decline, seller, "")
	cancelPair := sealSent(t, cancel, buyer, "")
	run("decline", declinePair, true, "")
	run("decline-alone", declinePair, false, "")
	run("cancellation", cancelPair, true, "")
	run("offer", sealSent(t, offer, buyer, ""), false, "")

	// The payload is not the record the Capsule commits.
	tampered := mutatePair(t, declinePair, func(p map[string]any) {
		p["agent_input"].(map[string]any)["body"].(map[string]any)["reason"] = ReasonOther
	})
	run("payload-not-committed", tampered, true, StageSent)
	run("decline-by-another-key", sealSent(t, decline, buyer, ""), true, StageSent)
	run("cancellation-by-another-key", sealSent(t, cancel, seller, ""), true, StageSent)
	withAgreement := mutateRecord(t, decline, func(body map[string]any) { body["rows_digest"] = hx('1') })
	run("decline-with-agreement-members", sealRaw(t, withAgreement, seller), true, StageSchema)
	unknownReason := mutateRecord(t, decline, func(body map[string]any) { body["reason"] = "too_slow" })
	run("decline-unknown-reason", sealRaw(t, unknownReason, seller), true, StageSchema)
	run("action-id-of-another-deal", sealSent(t, decline, seller, "deal-fedcba9876543210"+SentSuffix), true, StageSent)
	performed := validRecords(t)["perform"]
	run("not-a-sent-type", sealSent(t, performed, seller, ""), false, StageSent)
	otherKey := mutatePair(t, declinePair, func(p map[string]any) { p["record"].(map[string]any)["key_id"] = keyHex(buyer) })
	run("key-id-not-the-signer", otherKey, true, StageSent)
	declineOfOther := record(blk(TypeAgree), &AgreeBody{Kind: KindDecline, Offer: *ref('2')})
	run("decline-of-another-offer", sealSent(t, declineOfOther, seller, ""), true, StageSent)
	var parent struct {
		Record struct {
			CapsuleID string `json:"capsule_id"`
		} `json:"record"`
	}
	require.NoError(t, json.Unmarshal(cancelPair, &parent))
	chained := sealSentWith(t, decline, seller, "", &emit.Chain{ParentCapsuleID: parent.Record.CapsuleID, Relation: emit.ChainConfirms})
	run("chained", chained, true, StageSent)
	twice := []byte(strings.Replace(string(declinePair), `{"agent_input":`, `{"agent_input":{},"agent_input":`, 1))
	require.NotEqual(t, string(declinePair), string(twice))
	run("agent-input-twice", twice, true, StageCanonicalization)
	referenced := sealSentWith(t, decline, seller, "", nil, emit.Reference{Type: "agent-action-capsule", DigestAlg: "SHA-256", Digest: hx('6')})
	run("with-references", referenced, true, StageSent)
	notHex := mutatePair(t, declinePair, func(p map[string]any) { p["record"].(map[string]any)["signature"] = "zz" })
	run("signature-not-hex", notHex, true, StageSent)
	run("offer-with-offer-context", sealSent(t, offer, buyer, ""), true, "")

	got, err := json.MarshalIndent(map[string]any{
		"description": "Sent decline, cancellation and offer pairs (evidencebook#461, docs/design/461-deal-profile.md §3.1, §3.5): each pair is {record: the standalone Capsule with signature and key_id, agent_input: the x-deal-v1 record}. VerifySent(pair, offer?) must pass the valid cases with the digest given and refuse the others at the stage given. Written by dealprofile/xdealv1's TestSentVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(declineDir, "vectors.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(declineDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "decline/vectors.json differs from this build's; regenerate only for an intended change")
}

// The vectors replay from the file alone, as another language would.
func TestSentVectorsReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(declineDir, "vectors.json"))
	require.NoError(t, err)
	var v struct {
		Cases []struct {
			Name        string          `json:"name"`
			Pair        json.RawMessage `json:"pair"`
			Offer       json.RawMessage `json:"offer"`
			OfferSigner string          `json:"offer_signer"`
			Valid       bool            `json:"valid"`
			Digest      string          `json:"digest"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.Cases)
	for _, c := range v.Cases {
		var oc *OfferContext
		if c.Offer != nil {
			o, err := Decode(c.Offer)
			require.NoError(t, err)
			oc = &OfferContext{Offer: o, Signer: c.OfferSigner}
		}
		got, err := VerifySent(c.Pair, oc)
		if c.Valid {
			require.NoError(t, err, c.Name)
			assert.Equal(t, c.Digest, got.Digest, c.Name)
		} else {
			assert.Error(t, err, c.Name)
		}
	}
}

func mutatePair(t *testing.T, pair []byte, f func(map[string]any)) []byte {
	t.Helper()
	var p map[string]any
	require.NoError(t, json.Unmarshal(pair, &p))
	f(p)
	out, err := json.Marshal(p)
	require.NoError(t, err)
	return out
}

func mutateRecord(t *testing.T, r Record, f func(body map[string]any)) []byte {
	t.Helper()
	return edit(t, r, func(_, body map[string]any) { f(body) })
}

// sealRaw seals record bytes the record checks refuse, as a careless sender
// would, so the reader's refusal is what is tested.
func sealRaw(t *testing.T, raw []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	doc, err := decodeJSON(raw)
	require.NoError(t, err)
	payload := mustJCS(doc)
	identity, err := emit.NewEd25519SigningIdentity(key)
	require.NoError(t, err)
	sealed, err := emit.Seal(emit.SealInput{
		Capsule: emit.Input{ActionID: dealID + SentSuffix, ActionType: emit.ActionTypeFYI, Operator: "seller-agent", Developer: "xdealv1-test",
			Timestamp: time.Date(2026, 11, 1, 10, 5, 0, 0, time.UTC)},
		Payload: json.RawMessage(payload), Identity: identity,
	})
	require.NoError(t, err)
	pair, err := Pair(sealed.Payload, sealed.Envelope, payload)
	require.NoError(t, err)
	return pair
}

// VerifySent reads an offer held as a value as one held by pointer.
func TestVerifySentTakesAValueOffer(t *testing.T) {
	seller, buyer := testKey(11), testKey(12)
	offerB := offerBody()
	offerB.To = Some(keyHex(seller))
	byValue := record(blk(TypePropose), *offerB)
	od, err := Digest(byValue)
	require.NoError(t, err)
	decline := record(blk(TypeAgree), &AgreeBody{Kind: KindDecline, Offer: RefTo(od)})
	_, err = VerifySent(sealSent(t, decline, seller, ""), &OfferContext{Offer: byValue, Signer: keyHex(buyer)})
	assert.NoError(t, err)
}

// The offer given as context must itself pass Check.
func TestVerifySentChecksTheOfferGiven(t *testing.T) {
	seller, buyer := testKey(11), testKey(12)
	offerB := offerBody()
	offerB.To = Some(keyHex(seller))
	offerB.Role = "broker"
	bad := record(blk(TypePropose), offerB)
	od, err := Digest(bad)
	require.NoError(t, err)
	decline := record(blk(TypeAgree), &AgreeBody{Kind: KindDecline, Offer: RefTo(od)})
	_, err = VerifySent(sealSent(t, decline, seller, ""), &OfferContext{Offer: bad, Signer: keyHex(buyer)})
	assert.Equal(t, StageSchema, stageOf(err), "%v", err)
}
