// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/fxamacker/cbor/v2"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
)

// Registering a Standard History card on a SCITT transparency service
// (agent-action-capsule spec/standard-history-v0.md §13, cited as SH).
//
// Registration is opt-in and has no default: nothing here names or reaches a
// service. The host supplies a CardStatementRegistrar (its own client for the
// service it chose) and the relying party's transparency policy. What it
// shows is existence at a time and nothing else (SH §13): never that the
// counts are right, never anchor_inclusion (it stays NOT_CHECKED for a
// card), never that the holder registered no other card. A failed
// registration changes nothing about the card, and carriage of the card
// statement beside the card is the host's (SH §13.3): a card statement is
// never an entry of an envelope's verification.
//
// A service the holder operates confers nothing (SH §13.2), and a card
// statement's iss and sub tell the service, and anyone who reads its log,
// which holder key registered a card for which anchor and when. The log is
// append-only, so a registration can never be withdrawn, or the link it makes
// between the key and the anchor (SH §13.5). The statement carries the card's
// digest, not the card, but a digest does not hide a card from a party that
// can guess it: a party that holds the anchor, the policy digest and `since`
// can test guesses of the counts of a small card against it. A holder
// registers knowing that, and only on a service whose published policy it
// accepts. Not registering is no finding against a card or its holder.

const (
	coseSign1Tag        = 18
	coseLabelAlg        = 1
	coseLabelCWTClaims  = 15
	cwtLabelIss         = 1
	cwtLabelSub         = 2
	coseAlgEdDSA        = -8
	coseLabelReceipts   = 394
	maxCardReceipts     = 16
	cardStatementMaxLen = 10485760
)

// CardRegisteredReport is the card_registered check (SH §13.3) over the card
// statements beside one card: the AAC witness package's report, passed
// through unchanged. It is reported beside the card properties, never as one,
// and never changes a level or anchor_inclusion.
type CardRegisteredReport = aacwitness.CardRegisteredReport

// CardStatementRegistrar is the host's client for the one transparency
// service it chose. RegisterStatement posts the card statement's octets and
// returns the octets of the Receipt the service issued for it. The library
// reaches no service itself and has no default one.
type CardStatementRegistrar interface {
	RegisterStatement(ctx context.Context, statement []byte) ([]byte, error)
}

type coseSign1 struct {
	_           struct{} `cbor:",toarray"`
	Protected   []byte
	Unprotected map[int64][][]byte
	Payload     []byte
	Signature   []byte
}

var cardCBOR = func() cbor.EncMode {
	mode, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	return mode
}()

func encodeCardStatement(s coseSign1) ([]byte, error) {
	return cardCBOR.Marshal(cbor.RawTag{Number: coseSign1Tag, Content: mustMarshal(s)})
}

func mustMarshal(v any) cbor.RawMessage {
	out, err := cardCBOR.Marshal(v)
	if err != nil {
		panic(err) // the values encoded here are byte strings and maps of them
	}
	return out
}

// cardSigStructure is the COSE Sig_structure the signature covers
// (RFC 9052 §4.4): the octets whose SHA-256 is the statement entry E.
func cardSigStructure(protected, payload []byte) ([]byte, error) {
	return cardCBOR.Marshal([]any{"Signature1", protected, []byte{}, payload})
}

// BuildCardStatement signs a card statement (SH §13.1) over card, the card's
// octets, as of anchor (the card's anchor digest, which is the envelope's
// anchor.digest). The statement carries no Receipt yet: its unprotected map
// is empty, which is the form to submit to a service. A Receipt added later
// changes neither the statement entry nor the signature.
func BuildCardStatement(ctx context.Context, signer Signer, card []byte, anchor string) ([]byte, error) {
	if signer == nil {
		return nil, fmt.Errorf("%w: a card statement needs the holder's signer", ErrInvalid)
	}
	if len(card) == 0 {
		return nil, fmt.Errorf("%w: a card statement needs the card octets", ErrInvalid)
	}
	if !hex64.MatchString(anchor) {
		return nil, fmt.Errorf("%w: the anchor is not 64 lowercase hex characters", ErrInvalid)
	}
	protected, err := cardCBOR.Marshal(map[int64]any{
		coseLabelAlg: coseAlgEdDSA,
		coseLabelCWTClaims: map[int64]any{
			cwtLabelIss: KeyID(signer),
			cwtLabelSub: anchor,
		},
	})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(card)
	toSign, err := cardSigStructure(protected, digest[:])
	if err != nil {
		return nil, err
	}
	signature, err := signer.Sign(ctx, toSign)
	if err != nil {
		return nil, err
	}
	return encodeCardStatement(coseSign1{Protected: protected, Unprotected: map[int64][][]byte{}, Payload: digest[:], Signature: signature})
}

// AttachCardReceipt adds receipt, the octets of one service Receipt, to
// statement and returns the statement with it. It first checks the Receipt
// alone, on the statement, under the relying party's transparency policy with
// the card, the holder key identifier and the anchor, and refuses a Receipt
// that does not make that statement PASS, so only a Receipt of a listed
// service over this statement is ever kept, even beside Receipts that pass. A card statement holds 1 to 16 Receipts (SH §13.1); a
// seventeenth is refused, and a holder registers a second statement instead.
func AttachCardReceipt(card []byte, holder, anchor string, statement, receipt []byte, policy aacwitness.AnchorWitnessPolicy) ([]byte, error) {
	if len(statement) > cardStatementMaxLen {
		return nil, fmt.Errorf("%w: the card statement is over %d octets", ErrInvalid, cardStatementMaxLen)
	}
	var tagged cbor.RawTag
	if err := cbor.Unmarshal(statement, &tagged); err != nil || tagged.Number != coseSign1Tag {
		return nil, fmt.Errorf("%w: not a card statement (tag 18 around a COSE_Sign1)", ErrInvalid)
	}
	var parsed coseSign1
	if err := cbor.Unmarshal(tagged.Content, &parsed); err != nil {
		return nil, fmt.Errorf("%w: not a card statement: %v", ErrInvalid, err)
	}
	for label := range parsed.Unprotected {
		if label != coseLabelReceipts {
			return nil, fmt.Errorf("%w: the card statement has an unprotected label besides receipts", ErrInvalid)
		}
	}
	if len(parsed.Unprotected[coseLabelReceipts]) >= maxCardReceipts {
		return nil, fmt.Errorf("%w: a card statement holds at most %d Receipts", ErrInvalid, maxCardReceipts)
	}
	existing := parsed.Unprotected[coseLabelReceipts]
	// The new Receipt is checked alone: card_registered passes when any one
	// Receipt does, so checking the whole statement would let a Receipt that
	// passes hide one that does not.
	parsed.Unprotected = map[int64][][]byte{coseLabelReceipts: {receipt}}
	alone, err := encodeCardStatement(parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if report := aacwitness.CheckCardStatement(card, holder, anchor, alone, policy); report.Status != aacwitness.AnchorPass {
		return nil, fmt.Errorf("%w: the Receipt does not make the card statement pass (%s): %s", ErrInvalid, report.Status, report.Detail)
	}
	parsed.Unprotected = map[int64][][]byte{coseLabelReceipts: append(existing[:len(existing):len(existing)], receipt)}
	candidate, err := encodeCardStatement(parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(candidate) > cardStatementMaxLen {
		return nil, fmt.Errorf("%w: the card statement would be over %d octets", ErrInvalid, cardStatementMaxLen)
	}
	return candidate, nil
}

// RegisterCard builds the card statement for card, the artifact of env (the
// answer it was derived in), has the host's registrar post it to the service
// the host chose, and returns the statement with the service's Receipt
// attached, checked under policy. The signer must be the envelope's
// responder: a statement under another key would be logged for good, append
// only, and then fail SH §13.3 step 2 at every reader, so it is refused
// before anything is posted. It is a call of its own: a card is complete
// without it, and an error here (the service is down, or its Receipt does not
// pass) leaves the card exactly as it was. The host keeps the returned octets
// beside the card (SH §13.3).
func RegisterCard(ctx context.Context, signer Signer, card []byte, env Envelope, registrar CardStatementRegistrar, policy aacwitness.AnchorWitnessPolicy) ([]byte, error) {
	if registrar == nil {
		return nil, fmt.Errorf("%w: no transparency service is configured; registration is opt-in and has no default", ErrInvalid)
	}
	if signer == nil || KeyID(signer) != env.Responder {
		return nil, fmt.Errorf("%w: the signer is not the envelope's responder, the holder key", ErrInvalid)
	}
	statement, err := BuildCardStatement(ctx, signer, card, env.Anchor.Digest)
	if err != nil {
		return nil, err
	}
	receipt, err := registrar.RegisterStatement(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("card registration: %w", err)
	}
	return AttachCardReceipt(card, env.Responder, env.Anchor.Digest, statement, receipt, policy)
}

// CheckCardRegistered is card_registered (SH §13.3) over the card statements
// received beside card, the octets of the card's artifact, under the
// envelope's responder (the holder key) and anchor and the relying party's
// transparency policy. NOT_PRESENT when there is no statement. A verifier may
// say a PASS means the holder registered this card with the service named, as
// the policy names it, at the registration time (or with no time when the
// Receipt has none); it must not say the service checked the counts, the
// deals or the anchor, or that this is the holder's only card (SH §13.4).
func CheckCardRegistered(card []byte, env Envelope, statements [][]byte, policy aacwitness.AnchorWitnessPolicy) CardRegisteredReport {
	return aacwitness.CheckCardRegistered(card, env.Responder, env.Anchor.Digest, statements, policy)
}
