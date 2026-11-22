// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
)

type cardRegistrationCase struct {
	Name       string                          `json:"name"`
	Card       string                          `json:"card"`
	Holder     string                          `json:"holder"`
	Anchor     string                          `json:"anchor"`
	Statements []string                        `json:"statements"`
	Policy     json.RawMessage                 `json:"policy"`
	Expected   aacwitness.CardRegisteredReport `json:"expected"`
}

type cardRegistrationVectors struct {
	Policies map[string]aacwitness.AnchorWitnessPolicy `json:"policies"`
	Cases    []cardRegistrationCase                    `json:"cases"`
}

func loadCardRegistrationVectors(t *testing.T) cardRegistrationVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/card-registration/vectors.json")
	require.NoError(t, err)
	var v cardRegistrationVectors
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

func (v cardRegistrationVectors) policy(t *testing.T, c cardRegistrationCase) aacwitness.AnchorWitnessPolicy {
	t.Helper()
	var name string
	if json.Unmarshal(c.Policy, &name) == nil {
		return v.Policies[name]
	}
	var inline aacwitness.AnchorWitnessPolicy
	require.NoError(t, json.Unmarshal(c.Policy, &inline))
	return inline
}

func (v cardRegistrationVectors) find(t *testing.T, name string) cardRegistrationCase {
	t.Helper()
	for _, c := range v.Cases {
		if c.Name == name {
			return c
		}
	}
	require.Failf(t, "no such vector case", "%s", name)
	return cardRegistrationCase{}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// vectorHolderSigner is the vectors' holder key, derived from a public label.
func vectorHolderSigner(t *testing.T) *Ed25519Signer {
	t.Helper()
	seed := sha256.Sum256([]byte("card-registration-v0 key holder"))
	signer, err := NewEd25519Signer(ed25519.NewKeyFromSeed(seed[:]))
	require.NoError(t, err)
	return signer
}

// splitReceipts returns a vector statement's Receipts and the same statement
// with an empty unprotected map, the form BuildCardStatement signs.
func splitReceipts(t *testing.T, statement []byte) (receipts [][]byte, bare []byte) {
	t.Helper()
	var tagged cbor.RawTag
	require.NoError(t, cbor.Unmarshal(statement, &tagged))
	var parsed coseSign1
	require.NoError(t, cbor.Unmarshal(tagged.Content, &parsed))
	receipts = parsed.Unprotected[coseLabelReceipts]
	parsed.Unprotected = map[int64][][]byte{}
	bare, err := encodeCardStatement(parsed)
	require.NoError(t, err)
	return receipts, bare
}

// TestCardRegisteredVectors runs the vectors AAC defines for SH §13.3 through
// CheckCardRegistered, the report and its aggregation unchanged.
func TestCardRegisteredVectors(t *testing.T) {
	v := loadCardRegistrationVectors(t)
	require.NotEmpty(t, v.Cases)
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var statements [][]byte
			for _, s := range c.Statements {
				statements = append(statements, mustHex(t, s))
			}
			env := Envelope{Responder: c.Holder, Anchor: Anchor{Digest: c.Anchor}}
			got := CheckCardRegistered(mustHex(t, c.Card), env, statements, v.policy(t, c))
			assert.Equal(t, aacwitness.CardRegisteredProperty, got.Property)
			assert.Equal(t, c.Expected.Status, got.Status)
			assert.Equal(t, c.Expected.Service, got.Service)
			assert.Equal(t, c.Expected.RegistrationTime, got.RegistrationTime)
			require.Len(t, got.Statements, len(c.Expected.Statements))
			for i, want := range c.Expected.Statements {
				assert.Equal(t, want.Status, got.Statements[i].Status, "statement %d", i)
				assert.Equal(t, want.Service, got.Statements[i].Service, "statement %d", i)
				assert.Equal(t, want.RegistrationTime, got.Statements[i].RegistrationTime, "statement %d", i)
			}
		})
	}
}

// TestBuildCardStatementMatchesVector signs the vectors' card with the
// vectors' holder key and gets, byte for byte, the vector statement with its
// Receipts removed; attaching the vector's Receipt gives the vector statement.
func TestBuildCardStatementMatchesVector(t *testing.T) {
	v := loadCardRegistrationVectors(t)
	c := v.find(t, "pass-eddsa-time")
	card := mustHex(t, c.Card)
	vector := mustHex(t, c.Statements[0])
	receipts, bare := splitReceipts(t, vector)
	require.Len(t, receipts, 1)

	signer := vectorHolderSigner(t)
	require.Equal(t, c.Holder, KeyID(signer))
	built, err := BuildCardStatement(t.Context(), signer, card, c.Anchor)
	require.NoError(t, err)
	assert.Equal(t, bare, built)

	attached, err := AttachCardReceipt(card, c.Holder, c.Anchor, built, receipts[0], v.policy(t, c))
	require.NoError(t, err)
	assert.Equal(t, vector, attached)
}

func TestBuildCardStatementErrors(t *testing.T) {
	signer := vectorHolderSigner(t)
	anchor := hex.EncodeToString(make([]byte, 32))
	_, err := BuildCardStatement(t.Context(), nil, []byte("card"), anchor)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = BuildCardStatement(t.Context(), signer, nil, anchor)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = BuildCardStatement(t.Context(), signer, []byte("card"), "NOT-HEX")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestAttachCardReceiptRefuses(t *testing.T) {
	v := loadCardRegistrationVectors(t)
	c := v.find(t, "pass-eddsa-time")
	card := mustHex(t, c.Card)
	policy := v.policy(t, c)
	receipts, bare := splitReceipts(t, mustHex(t, c.Statements[0]))

	t.Run("a Receipt by an unlisted service", func(t *testing.T) {
		other := v.find(t, "receipt-unlisted-service")
		otherReceipts, _ := splitReceipts(t, mustHex(t, other.Statements[0]))
		_, err := AttachCardReceipt(card, c.Holder, c.Anchor, bare, otherReceipts[0], policy)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("a Receipt over the card digest instead of the statement entry", func(t *testing.T) {
		other := v.find(t, "receipt-over-card-digest")
		otherReceipts, _ := splitReceipts(t, mustHex(t, other.Statements[0]))
		_, err := AttachCardReceipt(card, c.Holder, c.Anchor, bare, otherReceipts[0], policy)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("octets that are not a Receipt", func(t *testing.T) {
		_, err := AttachCardReceipt(card, c.Holder, c.Anchor, bare, []byte("not a receipt"), policy)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("a policy that lists no service", func(t *testing.T) {
		_, err := AttachCardReceipt(card, c.Holder, c.Anchor, bare, receipts[0], aacwitness.AnchorWitnessPolicy{})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("another card", func(t *testing.T) {
		_, err := AttachCardReceipt([]byte("another card"), c.Holder, c.Anchor, bare, receipts[0], policy)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("not a statement", func(t *testing.T) {
		_, err := AttachCardReceipt(card, c.Holder, c.Anchor, []byte{0xa0}, receipts[0], policy)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("an unprotected label besides receipts", func(t *testing.T) {
		extra := v.find(t, "unprotected-extra")
		_, err := AttachCardReceipt(mustHex(t, extra.Card), extra.Holder, extra.Anchor, mustHex(t, extra.Statements[0]), receipts[0], policy)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("a seventeenth Receipt", func(t *testing.T) {
		statement := bare
		var err error
		for i := 0; i < maxCardReceipts; i++ {
			statement, err = AttachCardReceipt(card, c.Holder, c.Anchor, statement, receipts[0], policy)
			require.NoError(t, err, "Receipt %d", i+1)
		}
		_, err = AttachCardReceipt(card, c.Holder, c.Anchor, statement, receipts[0], policy)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "at most 16")
	})
}

type stubRegistrar struct {
	receipt []byte
	err     error
	posted  [][]byte
}

func (s *stubRegistrar) RegisterStatement(_ context.Context, statement []byte) ([]byte, error) {
	s.posted = append(s.posted, statement)
	return s.receipt, s.err
}

func TestRegisterCard(t *testing.T) {
	v := loadCardRegistrationVectors(t)
	c := v.find(t, "pass-eddsa-time")
	card := mustHex(t, c.Card)
	policy := v.policy(t, c)
	vector := mustHex(t, c.Statements[0])
	receipts, bare := splitReceipts(t, vector)
	signer := vectorHolderSigner(t)

	t.Run("registers and the card_registered check passes", func(t *testing.T) {
		registrar := &stubRegistrar{receipt: receipts[0]}
		statement, err := RegisterCard(t.Context(), signer, card, c.Anchor, registrar, policy)
		require.NoError(t, err)
		assert.Equal(t, [][]byte{bare}, registrar.posted, "the service is sent the statement with no Receipt")
		assert.Equal(t, vector, statement)
		report := CheckCardRegistered(card, Envelope{Responder: c.Holder, Anchor: Anchor{Digest: c.Anchor}}, [][]byte{statement}, policy)
		assert.Equal(t, aacwitness.AnchorPass, report.Status)
		require.NotNil(t, report.Service)
		assert.Equal(t, "service-a", *report.Service)
	})
	t.Run("no service configured is an error, never a default", func(t *testing.T) {
		_, err := RegisterCard(t.Context(), signer, card, c.Anchor, nil, policy)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "no default")
	})
	t.Run("a service that is down leaves nothing", func(t *testing.T) {
		down := errors.New("service unavailable")
		statement, err := RegisterCard(t.Context(), signer, card, c.Anchor, &stubRegistrar{err: down}, policy)
		require.ErrorIs(t, err, down)
		assert.Nil(t, statement)
	})
	t.Run("a Receipt that does not pass is refused", func(t *testing.T) {
		statement, err := RegisterCard(t.Context(), signer, card, c.Anchor, &stubRegistrar{receipt: []byte("junk")}, policy)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Nil(t, statement)
	})
	t.Run("no statement is NOT_PRESENT, not a finding", func(t *testing.T) {
		report := CheckCardRegistered(card, Envelope{Responder: c.Holder, Anchor: Anchor{Digest: c.Anchor}}, nil, policy)
		assert.Equal(t, aacwitness.AnchorNotPresent, report.Status)
	})
}

// card_registered is reported beside the card properties and is never one
// (SH §13.3): a standard card's property set does not name it.
func TestCardRegisteredIsNotACardProperty(t *testing.T) {
	assert.False(t, slices.Contains(standardhistory.Properties, aacwitness.CardRegisteredProperty))
	assert.False(t, slices.Contains(standardhistory.Properties, "anchor_inclusion"))
}

// TestCardRegisteredRecordedAnchorReceipt checks a statement and Receipt
// recorded from a real capsule-anchor run (POST /transparency/register-statement,
// recorded by RegisterCard), so the leaf and the Receipt form are the real
// service's, not a stub's.
func TestCardRegisteredRecordedAnchorReceipt(t *testing.T) {
	raw, err := os.ReadFile("testdata/card-registration/capsule-anchor-receipt.json")
	require.NoError(t, err)
	var rec struct {
		Card      string                         `json:"card"`
		Holder    string                         `json:"holder"`
		Anchor    string                         `json:"anchor"`
		Statement string                         `json:"statement"`
		Policy    aacwitness.AnchorWitnessPolicy `json:"policy"`
	}
	require.NoError(t, json.Unmarshal(raw, &rec))
	env := Envelope{Responder: rec.Holder, Anchor: Anchor{Digest: rec.Anchor}}
	statements := [][]byte{mustHex(t, rec.Statement)}
	report := CheckCardRegistered(mustHex(t, rec.Card), env, statements, rec.Policy)
	assert.Equal(t, aacwitness.AnchorPass, report.Status)
	require.NotNil(t, report.Service)
	assert.Equal(t, "local-capsule-anchor", *report.Service)
	require.NotNil(t, report.RegistrationTime, "capsule-anchor's Receipt carries its registration time")
	assert.Positive(t, *report.RegistrationTime)

	// The same statement under a policy that does not list the service is not
	// a pass: a service the relying party does not accept confers nothing.
	report = CheckCardRegistered(mustHex(t, rec.Card), env, statements, aacwitness.AnchorWitnessPolicy{})
	assert.NotEqual(t, aacwitness.AnchorPass, report.Status)
}
