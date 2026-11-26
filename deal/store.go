// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// version is the deal version a Store writes, as deal_versions records it.
const version = "x-deal-v1"

// tables are the store's tables, created by Open. Their names and schemas
// are the ones capsulectl-deal created before this package existed, so a
// store either wrote opens unchanged. deal_versions says which version a
// deal is and where its books are; deal_sent is one row per record sent
// whole, written before it is sealed so a re-run finds it; deal_v1 is the
// deal's side, offer and agreements; deal_v1_offers the offers received;
// deal_v1_admits binds each admission decision to the one deal it was
// sealed for (an admit record carries no deal id, x-deal-v1 PROFILE §3).
var tables = []string{
	// deal_v1_events is each deal's local step list and deal_v1_sends each
	// payer leg's send; capsulectl-deal writes both, and the store reads the
	// list to find the journal records a deal's steps bind (DealJournal).
	`CREATE TABLE IF NOT EXISTS deal_v1_events (
	deal_id TEXT NOT NULL,
	n INTEGER NOT NULL,
	kind TEXT NOT NULL,
	local TEXT NOT NULL,
	record_digest TEXT NOT NULL,
	payload BLOB,
	PRIMARY KEY (deal_id, n)
)`,
	`CREATE TABLE IF NOT EXISTS deal_v1_sends (
	deal_id TEXT NOT NULL,
	check_digest TEXT NOT NULL,
	event_n INTEGER NOT NULL,
	amount_minor INTEGER NOT NULL,
	currency TEXT NOT NULL,
	ref_type TEXT NOT NULL,
	ref_value TEXT NOT NULL,
	ref_json TEXT NOT NULL,
	reverses TEXT NOT NULL DEFAULT '',
	leg_digest TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (deal_id, check_digest)
)`,
	`CREATE TABLE IF NOT EXISTS deal_versions (
	deal_id TEXT PRIMARY KEY,
	version TEXT NOT NULL,
	own_log_id TEXT NOT NULL,
	counterparty_log_id TEXT NOT NULL,
	state TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS deal_sent (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	record_digest TEXT NOT NULL,
	request_digest TEXT NOT NULL,
	deal_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	payload BLOB NOT NULL,
	state TEXT NOT NULL,
	pair BLOB,
	written INTEGER NOT NULL DEFAULT 0,
	UNIQUE (record_digest, request_digest)
)`,
	`CREATE TABLE IF NOT EXISTS deal_v1_admits (
	deal_id TEXT PRIMARY KEY,
	admit_digest TEXT NOT NULL UNIQUE,
	payload BLOB NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS deal_v1 (
	deal_id TEXT PRIMARY KEY,
	side TEXT NOT NULL,
	role TEXT NOT NULL,
	offer_digest TEXT NOT NULL,
	counterparty_key TEXT NOT NULL,
	counterparty_agreement TEXT NOT NULL,
	own_agreement_id TEXT NOT NULL,
	own_agreement_digest TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS deal_v1_offers (
	offer_digest TEXT PRIMARY KEY,
	deal_id TEXT NOT NULL,
	signer_key TEXT NOT NULL,
	payload BLOB NOT NULL,
	pair BLOB
)`,
}

// Config opens a Store.
type Config struct {
	// Shelf is the holder's shelf: its journal holds the records sent whole
	// and each deal book is one of its books.
	Shelf *evidencebook.Shelf
	// DB is a handle on the shelf's own SQLite file (ShelfConfig.Path): the
	// store's tables sit beside the books, and a handle on another file would
	// leave its rows naming books that file does not hold.
	DB *sql.DB
	// SigningKey is the public key the shelf seals records under
	// (ShelfConfig.Signer), which an offer names as its addressee.
	SigningKey ed25519.PublicKey
	// CheckpointKey is the public key of ShelfConfig.CheckpointKey, which an
	// agreement names so the other side binds a stamp proof to it.
	CheckpointKey ed25519.PublicKey
	// ShelfLogID is the shelf's cadence log id (CadenceConfig.LogID), which an
	// agreement names.
	ShelfLogID string
	// Now is the clock, called each time a record is timed. It must be the
	// function the shelf was opened with (ShelfConfig.Now), so a record's time
	// and the window its book is placed in agree. Nil is time.Now.
	Now func() time.Time
	// Random is the source of deal ids and book log ids. Nil is the system
	// CSPRNG.
	Random io.Reader
	// ReceiveProfile is the x-deal-v1 receive profile the shelf was opened
	// with (ShelfConfig.Receive), whose first-act gate a gated act passes.
	// Only Perform needs it.
	ReceiveProfile *xdealv1.ReceiveProfile
}

// Store is a holder's x-deal-v1 deals on its shelf.
type Store struct {
	cfg Config
}

// Open opens the store on cfg.Shelf, creating its tables when absent. The
// shelf's cadence must already keep a log of book facts (Cadence.Migrated):
// the journal and SealSent need one. A host that also keeps deals of the
// legacy x-deal-v0 opens a Store only for x-deal-v1 work, so a store that
// never holds an x-deal-v1 deal never gains these tables.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Shelf == nil || cfg.DB == nil {
		return nil, errors.New("deal: Open needs a shelf and a handle on its file")
	}
	if len(cfg.SigningKey) != ed25519.PublicKeySize || len(cfg.CheckpointKey) != ed25519.PublicKeySize {
		return nil, errors.New("deal: Open needs the shelf's signing and checkpoint public keys")
	}
	if cfg.ShelfLogID == "" {
		return nil, errors.New("deal: Open needs the shelf's cadence log id, which an agreement names")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	for _, stmt := range tables {
		if _, err := cfg.DB.ExecContext(ctx, stmt); err != nil {
			return nil, err
		}
	}
	return &Store{cfg: cfg}, nil
}

// InputError is a call refused for what it was given; it is meant for the
// person who made the call. Field, when set, names the argument at fault
// (admit, intent, input, reason) and the message is Field followed by
// Reason, so a command line can put its own flag name in front.
//
// Code, when set, names a refusal whose message names a call of this
// package, and Args holds the values that message names, in the order the
// Code's doc lists. A caller with its own names for those calls, such as a
// command line, can word the refusal itself from Code and Args.
type InputError struct {
	Field, Reason string
	Code          Code
	Args          []string
}

func (e *InputError) Error() string { return e.Field + e.Reason }

func inputError(reason string) error { return &InputError{Reason: reason} }

// fieldError is an InputError about one argument; reason follows its name
// directly (" is not ...", "'s admit is ...").
func fieldError(field, reason string) error { return &InputError{Field: field, Reason: reason} }

// coded is an InputError with its Code and Args.
func coded(code Code, field, reason string, args ...string) error {
	return &InputError{Field: field, Reason: reason, Code: code, Args: args}
}

// ConflictError reports stored state that disagrees with the call: a record
// sealed but not recorded, a book that kept changing, a second decision.
// Code and Args are as on [InputError].
type ConflictError struct {
	Reason string
	Code   Code
	Args   []string
}

func (e *ConflictError) Error() string { return e.Reason }

func conflictf(format string, args ...any) error {
	return &ConflictError{Reason: fmt.Sprintf(format, args...)}
}

// codedConflict is a ConflictError with its Code and Args.
func codedConflict(code Code, reason string, args ...string) error {
	return &ConflictError{Reason: reason, Code: code, Args: args}
}

// Code names a refusal of this package whose message names a call of this
// package, or asks for the same call again. The values are for the caller's own process: a program may match on
// them or word the refusal in its own terms, but they are not vocabulary to
// write into output, a record or a message. The constants are stable:
// renaming or removing one breaks a caller that matches on it.
type Code string

// The coded refusals: 24. Each lists, in order, the Args its message names.
const (
	// CodeCancelAgain: the offer is already cancelled. No Args.
	CodeCancelAgain Code = "cancel-again"
	// CodeDeclineUnknownOffer: the profile holds no offer with this digest.
	// Args: the offer digest.
	CodeDeclineUnknownOffer Code = "decline-unknown-offer"
	// CodeDeclineOwnOffer: the caller wrote the offer it declines. No Args.
	CodeDeclineOwnOffer Code = "decline-own-offer"
	// CodeDeclineAgain: the offer is already declined. No Args.
	CodeDeclineAgain Code = "decline-again"
	// CodeAdmitNotAdmitted: the admission decision does not admit the deal.
	// Args: the decision's verdict.
	CodeAdmitNotAdmitted Code = "admit-not-admitted"
	// CodeAdmitUnknownDeal: the store holds no x-deal-v1 deal with this id.
	// Args: the deal id.
	CodeAdmitUnknownDeal Code = "admit-unknown-deal"
	// CodeAdmitAgain (a ConflictError): the deal already has another
	// admission decision. Args: the deal id, the decision's digest.
	CodeAdmitAgain Code = "admit-again"
	// CodeAgreeUnknownOffer: the profile holds no offer with this digest.
	// Args: the offer digest.
	CodeAgreeUnknownOffer Code = "agree-unknown-offer"
	// CodeAgreeDeclineUnfinished: a decline of the offer was begun and did
	// not finish. No Args.
	CodeAgreeDeclineUnfinished Code = "agree-decline-unfinished"
	// CodeAgreeBeforeReceive: the offerer agrees before it has received the
	// acceptor's agreement. No Args.
	CodeAgreeBeforeReceive Code = "agree-before-receive"
	// CodeAgreeNoAdmit: the deal has no admission decision. Args: the deal
	// id.
	CodeAgreeNoAdmit Code = "agree-no-admit"
	// CodeAgreeAdmitNotOwn (Field admit): the admission decision named is not
	// the one the deal holds. Args: the decision named, the deal's own, the
	// deal id.
	CodeAgreeAdmitNotOwn Code = "agree-admit-not-own"
	// CodeAgreeAdmitOtherDeal (Field admit): the admission decision named was
	// sealed for another deal. Args: the decision named, the other deal's id,
	// the deal id.
	CodeAgreeAdmitOtherDeal Code = "agree-admit-other-deal"
	// CodeAgreeFirst: a record for a deal book that has not opened; it opens
	// at the holder's agreement. Args: the deal id.
	CodeAgreeFirst Code = "agree-first"
	// CodeRecordNotRebuilt (a ConflictError): a record sent whole was sealed
	// but its pair was not made, and the store cannot rebuild it. Args: the
	// record digest.
	CodeRecordNotRebuilt Code = "record-not-rebuilt"
	// CodeRecordRetry (a ConflictError): the deal book kept changing while
	// the record was sealed; the same call made again seals it. No Args.
	CodeRecordRetry Code = "record-retry"
	// CodeNoAgreement: a call on a deal the holder has not agreed yet; its
	// deal book opens at its agreement. Args: the deal id.
	CodeNoAgreement Code = "no-agreement"
	// CodeDisputeOpen: the holder already has an open dispute on the row in
	// another forum; it is taken back first. Args: the row, the open
	// dispute's forum.
	CodeDisputeOpen Code = "dispute-open"
	// CodeChallengeUnknownRuling: the holder holds no such ruling of the deal
	// from its adjudicator. Args: the ruling digest, the deal id.
	CodeChallengeUnknownRuling Code = "challenge-unknown-ruling"
	// CodeSettleWhichRow: the offer has more than one payment row and the
	// call names none. No Args.
	CodeSettleWhichRow Code = "settle-which-row"
	// CodeSettlePayer: the holder owes the row, so its leg is a payer leg,
	// which rests on its check. Args: "refund" for a refund, else "payment".
	CodeSettlePayer Code = "settle-payer"
	// CodeSettleRefunded: the leg is already being refunded. Args: the leg's
	// digest.
	CodeSettleRefunded Code = "settle-refunded"
	// CodeSettleNegative: an amount of the leg is negative. No Args.
	CodeSettleNegative Code = "settle-negative"
	// CodeInFlight: a journal record that changes the deal's details while
	// one of its payments is in flight. Args: the deal id, the open send's
	// check digest, the leg it reverses ("" for a payment), its payment_ref
	// type, the record's type.
	CodeInFlight Code = "in-flight"
)

// now is the store's clock, in UTC.
func (s *Store) now() time.Time { return s.cfg.Now().UTC() }

// nowText is the time a record carries: the clock, whole seconds.
func (s *Store) nowText() string { return s.now().Format(time.RFC3339) }

func (s *Store) randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.cfg.Random, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newDealID mints a deal id: deal- and 16 random hex digits, never derived
// from anything else.
func (s *Store) newDealID() (string, error) {
	h, err := s.randomHex(8)
	return "deal-" + h, err
}

// newOwnLogID is a deal book's log id: deal/ and 32 random hex digits, never
// derived from the deal id.
func (s *Store) newOwnLogID() (string, error) {
	h, err := s.randomHex(16)
	return "deal/" + h, err
}

// keyID is the store's signing key as the other side sees it (lowercase hex).
func (s *Store) keyID() string { return hex.EncodeToString(s.cfg.SigningKey) }

// Deal is a deal this store holds, as its tables record it.
type Deal struct {
	DealID string
	// Side is offerer or acceptor and Role buyer or seller; both are empty
	// for an offer sent before its row was kept.
	Side, Role  string
	OfferDigest string
	// CounterpartyKey is the other side's signing key; CounterpartyAgreement
	// the record id of its agreement, once received.
	CounterpartyKey, CounterpartyAgreement string
	// OwnAgreementID and OwnAgreementDigest are the holder's agreement: the
	// first record of its deal book.
	OwnAgreementID, OwnAgreementDigest string
	// OwnLogID is the holder's deal book; CounterpartyLogID the other side's.
	OwnLogID, CounterpartyLogID string
	// State is offered, offer_received, agreeing, agreed, declined or
	// cancelled.
	State string
}

// Deal reads a deal; nil when the store holds no x-deal-v1 deal of that id.
func (s *Store) Deal(ctx context.Context, dealID string) (*Deal, error) {
	d := &Deal{DealID: dealID}
	err := s.cfg.DB.QueryRowContext(ctx,
		`SELECT own_log_id, counterparty_log_id, state FROM deal_versions WHERE deal_id=? AND version=?`, dealID, version).
		Scan(&d.OwnLogID, &d.CounterpartyLogID, &d.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = s.cfg.DB.QueryRowContext(ctx,
		`SELECT side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest FROM deal_v1 WHERE deal_id=?`, dealID).
		Scan(&d.Side, &d.Role, &d.OfferDigest, &d.CounterpartyKey, &d.CounterpartyAgreement, &d.OwnAgreementID, &d.OwnAgreementDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return d, nil
}

// HeldOffer is an offer the store holds: its record, its signer's key and
// whether the holder made it.
type HeldOffer struct {
	Record xdealv1.Record
	Signer string
	Own    bool
}

// HeldOffer finds an offer by its record digest among the offers the holder made
// and the ones it received; nil when it holds none.
func (s *Store) HeldOffer(ctx context.Context, digest string) (*HeldOffer, error) {
	var payload []byte
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT payload FROM deal_sent WHERE record_digest=? AND kind=?`, digest, xdealv1.TypePropose).Scan(&payload)
	own := true
	signer := s.keyID()
	if errors.Is(err, sql.ErrNoRows) {
		own = false
		err = s.cfg.DB.QueryRowContext(ctx, `SELECT payload, signer_key FROM deal_v1_offers WHERE offer_digest=?`, digest).Scan(&payload, &signer)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec, err := xdealv1.Decode(payload)
	if err != nil {
		return nil, err
	}
	return &HeldOffer{Record: rec, Signer: signer, Own: own}, nil
}

// OfferPair is the sent pair of the offer whose record digest is given: the
// pair the holder sent, else the pair it received; nil when it holds neither
// (an offer received before pairs were kept). It is the OfferPair of the
// cadence's xdealv1.StatusProvider, so a deal's status and the card read one
// source.
func (s *Store) OfferPair(ctx context.Context, offerDigest string) ([]byte, error) {
	var pair []byte
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT pair FROM deal_sent WHERE record_digest=? AND kind=? AND pair IS NOT NULL ORDER BY rowid DESC LIMIT 1`,
		offerDigest, xdealv1.TypePropose).Scan(&pair)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.cfg.DB.QueryRowContext(ctx, `SELECT pair FROM deal_v1_offers WHERE offer_digest=? AND pair IS NOT NULL`, offerDigest).Scan(&pair)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
	}
	return pair, err
}

// NoLayer reports whether a deal is with a counterparty that keeps no signed
// records of its own: the holder made its offer and the offer names no key
// (to is null, PROFILE §4.7).
func (s *Store) NoLayer(ctx context.Context, d *Deal) (bool, error) {
	if d == nil || d.OfferDigest == "" {
		return false, nil
	}
	offer, err := s.HeldOffer(ctx, d.OfferDigest)
	if err != nil || offer == nil || !offer.Own {
		return false, err
	}
	prop, ok := proposeOf(offer.Record)
	return ok && prop.To.V == nil, nil
}

func proposeOf(r xdealv1.Record) (*xdealv1.ProposeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.ProposeBody:
		return b, true
	case xdealv1.ProposeBody:
		return &b, true
	}
	return nil, false
}

func agreeOf(r xdealv1.Record) (*xdealv1.AgreeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.AgreeBody:
		return b, true
	case xdealv1.AgreeBody:
		return &b, true
	}
	return nil, false
}

func oppositeRole(role string) string {
	if role == xdealv1.RoleBuyer {
		return xdealv1.RoleSeller
	}
	return xdealv1.RoleBuyer
}
