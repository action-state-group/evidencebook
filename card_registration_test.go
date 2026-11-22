// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
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

func vectorEnv(c cardRegistrationCase) Envelope {
	return Envelope{Responder: c.Holder, Anchor: Anchor{Digest: c.Anchor}}
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
		statement, err := RegisterCard(t.Context(), signer, card, vectorEnv(c), registrar, policy)
		require.NoError(t, err)
		assert.Equal(t, [][]byte{bare}, registrar.posted, "the service is sent the statement with no Receipt")
		assert.Equal(t, vector, statement)
		report := CheckCardRegistered(card, Envelope{Responder: c.Holder, Anchor: Anchor{Digest: c.Anchor}}, [][]byte{statement}, policy)
		assert.Equal(t, aacwitness.AnchorPass, report.Status)
		require.NotNil(t, report.Service)
		assert.Equal(t, "service-a", *report.Service)
	})
	t.Run("no service configured is an error, never a default", func(t *testing.T) {
		_, err := RegisterCard(t.Context(), signer, card, vectorEnv(c), nil, policy)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "no default")
	})
	t.Run("a signer that is not the responder posts nothing", func(t *testing.T) {
		registrar := &stubRegistrar{receipt: receipts[0]}
		other := Envelope{Responder: hex.EncodeToString(bytes32(1)), Anchor: Anchor{Digest: c.Anchor}}
		_, err := RegisterCard(t.Context(), signer, card, other, registrar, policy)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Empty(t, registrar.posted)
	})
	t.Run("a service that is down leaves nothing", func(t *testing.T) {
		down := errors.New("service unavailable")
		statement, err := RegisterCard(t.Context(), signer, card, vectorEnv(c), &stubRegistrar{err: down}, policy)
		require.ErrorIs(t, err, down)
		assert.Nil(t, statement)
	})
	t.Run("a Receipt that does not pass is refused", func(t *testing.T) {
		statement, err := RegisterCard(t.Context(), signer, card, vectorEnv(c), &stubRegistrar{receipt: []byte("junk")}, policy)
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

// testReceiptService issues RFC 9942 Receipts in the one form of IP1 §6.2.2
// (a one-leaf tree, EdDSA) for the leaf entry of a card statement, so a
// statement built over a real card can be registered without a network.
type testReceiptService struct {
	key  ed25519.PrivateKey
	iat  int64
	name string
}

func newTestReceiptService(seed byte) testReceiptService {
	return testReceiptService{key: ed25519.NewKeyFromSeed(bytes32(seed)), iat: 1_795_000_000, name: "test-service"}
}

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}

func (s testReceiptService) policy() aacwitness.AnchorWitnessPolicy {
	spki := "302a300506032b6570032100" + hex.EncodeToString(s.key.Public().(ed25519.PublicKey))
	return aacwitness.AnchorWitnessPolicy{Witnesses: []aacwitness.AnchorWitness{{Name: s.name, Keys: []string{spki}}}}
}

// receiptFor issues a Receipt over entry, which is what a service that logged
// the statement's Sig_structure digest signs.
func (s testReceiptService) receiptFor(t *testing.T, entry []byte) []byte {
	t.Helper()
	leaf := sha256.Sum256(append([]byte{0x00}, entry...))
	protected, err := cardCBOR.Marshal(map[int64]any{1: -8, 395: 1, 15: map[int64]any{6: s.iat}})
	require.NoError(t, err)
	toSign, err := cardSigStructure(protected, leaf[:])
	require.NoError(t, err)
	proof, err := cardCBOR.Marshal([]any{1, 0, []any{}})
	require.NoError(t, err)
	receipt, err := cardCBOR.Marshal(cbor.RawTag{Number: coseSign1Tag, Content: mustMarshal([]any{
		protected,
		map[int64]any{396: map[int64]any{-1: [][]byte{proof}}},
		nil,
		ed25519.Sign(s.key, toSign),
	})})
	require.NoError(t, err)
	return receipt
}

// RegisterStatement lets the service act as the host's CardStatementRegistrar.
func (s testReceiptService) registrar(t *testing.T) CardStatementRegistrar {
	return registrarFunc(func(_ context.Context, statement []byte) ([]byte, error) {
		return s.receiptFor(t, statementEntry(t, statement)), nil
	})
}

type registrarFunc func(context.Context, []byte) ([]byte, error)

func (f registrarFunc) RegisterStatement(ctx context.Context, statement []byte) ([]byte, error) {
	return f(ctx, statement)
}

// statementEntry is E of a card statement: SHA-256 over its Sig_structure.
func statementEntry(t *testing.T, statement []byte) []byte {
	t.Helper()
	var tagged cbor.RawTag
	require.NoError(t, cbor.Unmarshal(statement, &tagged))
	var parsed coseSign1
	require.NoError(t, cbor.Unmarshal(tagged.Content, &parsed))
	sig, err := cardSigStructure(parsed.Protected, parsed.Payload)
	require.NoError(t, err)
	entry := sha256.Sum256(sig)
	return entry[:]
}

// An existing passing Receipt must not let a bad one be kept: card_registered
// passes when any Receipt does, so AttachCardReceipt checks the new one alone.
func TestAttachCardReceiptChecksTheNewReceiptAlone(t *testing.T) {
	signer := vectorHolderSigner(t)
	service := newTestReceiptService(7)
	anchor := hex.EncodeToString(bytes32(3))
	card := []byte(`{"card":"x"}`)
	statement, err := RegisterCard(t.Context(), signer, card, Envelope{Responder: KeyID(signer), Anchor: Anchor{Digest: anchor}}, service.registrar(t), service.policy())
	require.NoError(t, err)

	v := loadCardRegistrationVectors(t)
	c := v.find(t, "pass-eddsa-time")
	foreign, _ := splitReceipts(t, mustHex(t, c.Statements[0]))
	for name, receipt := range map[string][]byte{
		"junk":                             []byte("junk"),
		"a Receipt over another entry":     service.receiptFor(t, bytes32(9)),
		"a Receipt of an unlisted service": newTestReceiptService(8).receiptFor(t, statementEntry(t, statement)),
		"a vector's Receipt":               foreign[0],
	} {
		_, err := AttachCardReceipt(card, KeyID(signer), anchor, statement, receipt, service.policy())
		require.ErrorIs(t, err, ErrInvalid, name)
	}
	// An earlier Receipt that is not a byte string leaves a statement that
	// can never pass, so a valid new Receipt is still refused.
	var tagged cbor.RawTag
	require.NoError(t, cbor.Unmarshal(statement, &tagged))
	var parsed coseSign1
	require.NoError(t, cbor.Unmarshal(tagged.Content, &parsed))
	broken, err := cardCBOR.Marshal(cbor.RawTag{Number: coseSign1Tag, Content: mustMarshal([]any{
		parsed.Protected, map[int64]any{coseLabelReceipts: []any{nil}}, parsed.Payload, parsed.Signature,
	})})
	require.NoError(t, err)
	_, err = AttachCardReceipt(card, KeyID(signer), anchor, broken, service.receiptFor(t, statementEntry(t, statement)), service.policy())
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "would not pass")

	// A second good Receipt is kept beside the first.
	two, err := AttachCardReceipt(card, KeyID(signer), anchor, statement, service.receiptFor(t, statementEntry(t, statement)), service.policy())
	require.NoError(t, err)
	assert.NotEqual(t, statement, two)
}

// Registering a card changes nothing about the card, the answer or what the
// requester records: the card is built, answered and recorded exactly as
// before, anchor_inclusion stays NOT_CHECKED, and card_registered is a
// separate report over the card's octets (SH §13.3, §13.4).
func TestRegisteringACardChangesNothingAboutTheAnswer(t *testing.T) {
	ctx := context.Background()
	responder := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	pin := anchorPin(t, responder)
	requester := newFixture(t, "requester", 31).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: pin}}, "book-sh")
	require.NoError(t, err)
	reply := respondAs(t, responder, sent.Message(), "requester")
	env := mustEnvelope(t, reply)
	card := bytes.Clone(env.Artifact)

	service := newTestReceiptService(5)
	before := CheckCardRegistered(card, env, nil, service.policy())
	assert.Equal(t, aacwitness.AnchorNotPresent, before.Status)

	statement, err := RegisterCard(ctx, responder.signer, card, env, service.registrar(t), service.policy())
	require.NoError(t, err)
	after := CheckCardRegistered(card, env, [][]byte{statement}, service.policy())
	require.Equal(t, aacwitness.AnchorPass, after.Status)
	require.NotNil(t, after.Service)
	assert.Equal(t, "test-service", *after.Service)
	require.NotNil(t, after.RegistrationTime)
	assert.Equal(t, service.iat, *after.RegistrationTime)

	// The requester records the same answer, and anchor_inclusion is still
	// the one check it could not make.
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 9))
	require.NoError(t, err)
	assert.Contains(t, string(recorded.Header.Statement), `"not_checked":["anchor_inclusion"]`)
	assert.Equal(t, env.Artifact, card, "the card's octets are untouched")

	// A statement over this card does not pass for another card.
	other := CheckCardRegistered([]byte(`{"another":"card"}`), env, [][]byte{statement}, service.policy())
	assert.Equal(t, aacwitness.AnchorFail, other.Status)
}

// TestBuildCardStatementMatchesGolden pins the statement BuildCardStatement
// signs over the vectors' card to a golden file the Goose demo's own builder
// (python/examples/goose/card_registration.py) is tested against, so the
// demo's bytes are Go's. EB_WRITE_CARD_GOLDEN=1 rewrites it for an intended change.
func TestBuildCardStatementMatchesGolden(t *testing.T) {
	v := loadCardRegistrationVectors(t)
	c := v.find(t, "pass-eddsa-time")
	built, err := BuildCardStatement(t.Context(), vectorHolderSigner(t), mustHex(t, c.Card), c.Anchor)
	require.NoError(t, err)
	path := "testdata/card-registration/built-pass-eddsa-time.hex"
	if os.Getenv("EB_WRITE_CARD_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(path, []byte(hex.EncodeToString(built)+"\n"), 0o644))
	}
	golden, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(built), strings.TrimSpace(string(golden)))
}
