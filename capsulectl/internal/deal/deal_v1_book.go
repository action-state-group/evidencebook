package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// The x-deal-v1 deal book and the records that come from the other side
// (docs/design/xdealv1-write-path.md, slice 2). A v1 deal book opens at the
// holder's agreement and holds the deal's records only; its log id is random,
// never derived from the deal id (§3). This file holds the writer every v1
// deal-book record goes through (sealBookV1), the local tables that say which
// deal is which, and the context that opens the shelf's store of received
// records for the verbs that need it.

// v1ReceiveKey marks a context whose session opens the shelf with the x-deal-v1
// receive profile: `deal receive`, `deal agree` and the first-act gate read the
// store of received records. No other verb opens it.
type v1ReceiveKey struct{}

func v1ReceiveContext(c *cobra.Command) {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	c.SetContext(context.WithValue(ctx, v1ReceiveKey{}, true))
}

// dealV1Tables are the deal's own row (which side, which offer, which books),
// and the offers received from the other side (the offer's payload, to answer
// it). The holder's own deal book is read back from the book itself.
var dealV1Tables = []string{
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
	payload BLOB NOT NULL
)`,
}

func (s *dealSession) ensureV1Tables(ctx context.Context) error {
	for _, stmt := range append(append([]string{}, dealV1Schema...), dealV1Tables...) {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// v1Deal is the deal's row in deal_v1 and deal_versions.
type v1Deal struct {
	DealID, Side, Role, OfferDigest, CounterpartyKey, CounterpartyAgreement string
	OwnAgreementID, OwnAgreementDigest                                      string
	OwnLogID, CounterpartyLogID, State                                      string
}

// v1DealOf reads a deal's rows; nil when the deal is not a v1 deal here.
func (s *dealSession) v1DealOf(ctx context.Context, dealID string) (*v1Deal, error) {
	d := &v1Deal{DealID: dealID}
	err := s.db.QueryRowContext(ctx,
		`SELECT own_log_id, counterparty_log_id, state FROM deal_versions WHERE deal_id=? AND version=?`, dealID, dealVersionV1).
		Scan(&d.OwnLogID, &d.CounterpartyLogID, &d.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest FROM deal_v1 WHERE deal_id=?`, dealID).
		Scan(&d.Side, &d.Role, &d.OfferDigest, &d.CounterpartyKey, &d.CounterpartyAgreement, &d.OwnAgreementID, &d.OwnAgreementDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return d, nil
}

// newOwnLogID is a deal book's log id: deal/ and 32 random hex, never derived
// from the deal id (design §3.1).
func newOwnLogID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "deal/" + hex.EncodeToString(b), nil
}

// checkpointKeyID is the hex of this profile's checkpoint public key, which an
// agreement names so the other side binds a stamp proof to it.
func (s *dealSession) checkpointKeyID() (string, error) {
	if s.p.Checkpoint.Signing == (cli.Secret{}) {
		return "", errNoCheckpointKey(s.p)
	}
	key, err := cli.PrivateKey(s.p.Checkpoint.Signing)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(key.Public().(ed25519.PublicKey)), nil
}

// bookSealed is a record the deal-book writer sealed: its record id (its
// Capsule id, what `seen` names), its record digest (what `prev` and
// `agreement_ref` name), its place and the pair that travels.
type bookSealed struct {
	Seq      uint64
	RecordID string
	Digest   string
	Record   xdealv1.Record
	Pair     []byte
}

// bookRecordIn is what a v1 deal-book verb gives the writer: the body and the
// marks the type carries. A deal-book record carries no block refs (evidencebook
// v0.17.0 refuses them): it cites by its body's own fields. The writer fills the block's
// chain: deal_id, at, seq, prev, agreement_ref and seen.
type bookRecordIn struct {
	Body  xdealv1.Body
	Marks []string
	// Seen, when set, is the record id `seen` names instead of the other
	// side's last counted record: the offerer's agreement names the
	// acceptor's agreement, whatever else it has held since.
	Seen string
	// Validate, when set, checks the finished record (its chain members
	// included) before anything is stored: what is checked is what is sealed.
	Validate func(xdealv1.Record) error
}

// sealBookV1 appends one record to the holder's deal book of dealID and
// returns it with the pair that is sent (design §3.1, §4.1). The first record
// of a book is the agreement: it opens the book under a fresh random log id
// (own_log_id in deal_versions). Every later record names the book's agreement
// and the record before it, and carries `seen`: the last record held from the
// other side's counted prefix, null when the other side runs no layer
// (design §3.4). The record passes the profile's checks before anything is
// stored, and the same operation records it in deal_v1_book so the book can
// be read back; this writer is the one every deal-book record goes through
// (an act, a settlement, a statement, a dispute), so a caller adds no chain
// member of its own.
func (s *dealSession) sealBookV1(ctx context.Context, dealID string, in bookRecordIn) (bookSealed, error) {
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return bookSealed{}, err
	}
	if d == nil {
		return bookSealed{}, cli.InputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
	}
	isAgreement := func() bool {
		switch in.Body.(type) {
		case xdealv1.AgreeBody, *xdealv1.AgreeBody:
			return true
		}
		return false
	}
	logID := d.OwnLogID
	if logID == "" {
		if !isAgreement() {
			return bookSealed{}, cli.InputError("deal " + dealID + " has no deal book yet: it opens at your agreement (`deal agree`)")
		}
		var err error
		if logID, err = newOwnLogID(); err != nil {
			return bookSealed{}, err
		}
		// The log id is stored before the book exists: a run that stops after
		// the record is appended finds the book, never makes a second one.
		if _, err = s.db.ExecContext(ctx, `UPDATE deal_versions SET own_log_id=?, state='agreeing' WHERE deal_id=?`, logID, dealID); err != nil {
			return bookSealed{}, err
		}
		d.OwnLogID = logID
	}
	book, err := s.shelf.Open(ctx, logID)
	if err != nil {
		return bookSealed{}, err
	}
	if d.OwnAgreementID == "" && book.Size() > 0 {
		// A run stopped between appending the agreement and recording it.
		if err = s.repairOpening(ctx, d, book); err != nil {
			return bookSealed{}, err
		}
	}
	attempt := func() (bookSealed, error) {
		size := book.Size()
		opening := size == 0
		if opening && !isAgreement() {
			return bookSealed{}, cli.InputError("deal " + dealID + " has no deal book yet: it opens at your agreement (`deal agree`)")
		}
		block := xdealv1.Block{
			Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID,
			RecordType: in.Body.RecordType(), At: v1Now(), Marks: in.Marks,
			Seq: int64(size) + 1,
		}
		var prevID string
		if size > 0 {
			last, err := book.Query(ctx, evidencebook.Filter{FromSeq: size, ToSeq: size})
			if err != nil {
				return bookSealed{}, err
			}
			if len(last) != 1 || len(last[0].Header.PayloadCommitments) != 1 {
				return bookSealed{}, fmt.Errorf("%w: the deal book's last record has no record digest", cli.ErrConflict)
			}
			prevID = last[0].RecordID
			prev := xdealv1.RefTo(last[0].Header.PayloadCommitments[0])
			block.Prev = &prev
			agreement := xdealv1.RefTo(d.OwnAgreementDigest)
			block.AgreementRef = &agreement
		}
		block.Seen = xdealv1.NullOf[string]()
		if d.CounterpartyKey != "" && d.CounterpartyLogID != "" {
			cp, err := s.shelf.CountedPrefix(ctx, d.CounterpartyKey, d.CounterpartyLogID)
			if err != nil {
				return bookSealed{}, err
			}
			if cp.Last != "" {
				block.Seen = xdealv1.Some(cp.Last)
			}
		}
		if in.Seen != "" {
			block.Seen = xdealv1.Some(in.Seen)
		}
		rec := xdealv1.Record{Block: block, Body: in.Body}
		if in.Validate != nil {
			if err := in.Validate(rec); err != nil {
				return bookSealed{}, err
			}
		}
		if err := xdealv1.Check(rec); err != nil {
			return bookSealed{}, cli.InputError("the record does not pass the x-deal-v1 checks: " + err.Error())
		}
		if xdealv1.PlacementOf(rec) != xdealv1.PlacementDealBook {
			return bookSealed{}, cli.InputError("a " + block.RecordType + " record is not sealed in a deal book")
		}
		payload, digest, err := xdealv1.Encode(rec)
		if err != nil {
			return bookSealed{}, err
		}
		entry, err := xdealv1.BookEntry(rec, prevID)
		if err != nil {
			return bookSealed{}, err
		}
		// BookEntry seals the header's record type as deal_step (design §3.1;
		// evidencebook v0.18.1, #531), which the test below pins.
		bookAppendHook()
		// The record names its own place, so it is appended only as the book's
		// next record: a write that landed since the tail was read (Receive names a
		// counted ruling in the same book) is refused with ErrBusy, and the
		// caller reads the tail again and rebuilds the record.
		stored, err := book.AppendAt(ctx, size+1, entry)
		if err != nil {
			if errors.Is(err, evidencebook.ErrBusy) {
				return bookSealed{}, errPlaceTaken
			}
			return bookSealed{}, err
		}
		sealed, err := book.Sealed(ctx, stored.RecordID)
		if err != nil {
			return bookSealed{}, err
		}
		pair, err := xdealv1.BookPair(sealed, payload)
		if err != nil {
			return bookSealed{}, err
		}
		if opening {
			if _, err = s.db.ExecContext(ctx, `UPDATE deal_v1 SET own_agreement_id=?, own_agreement_digest=? WHERE deal_id=?`, stored.RecordID, digest, dealID); err != nil {
				return bookSealed{}, err
			}
			if _, err = s.db.ExecContext(ctx, `UPDATE deal_versions SET state='agreed' WHERE deal_id=?`, dealID); err != nil {
				return bookSealed{}, err
			}
		}
		return bookSealed{Seq: size + 1, RecordID: stored.RecordID, Digest: digest, Record: rec, Pair: pair}, nil
	}
	for try := 0; try < bookAppendTries; try++ {
		sealed, err := attempt()
		if !errors.Is(err, errPlaceTaken) {
			return sealed, err
		}
	}
	return bookSealed{}, fmt.Errorf("%w: the deal book kept changing while the record was sealed; run the command again", cli.ErrConflict)
}

// errPlaceTaken is what a try returns when the append itself was refused its
// place (ErrBusy from AppendAt): the only error the writer retries. An ErrBusy
// from anywhere else (the validation, a read, the sealed record after a
// successful append) is returned as it is, never retried: a retry there could
// append the record twice.
var errPlaceTaken = errors.New("the record's place was taken")

// bookAppendTries is how many times the writer rebuilds a record whose place
// was taken by another write before it gives up.
const bookAppendTries = 8

// bookAppendHook is a seam for tests: it runs after a record is built and
// before it is appended, where a write from elsewhere can land.
var bookAppendHook = func() {}

// repairOpening records the agreement a stopped run appended but did not
// record: the first record of the deal book, from its header.
func (s *dealSession) repairOpening(ctx context.Context, d *v1Deal, book *evidencebook.Book) error {
	first, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: 1})
	if err != nil {
		return err
	}
	if len(first) != 1 || len(first[0].Header.PayloadCommitments) != 1 {
		return fmt.Errorf("%w: the deal book's first record has no record digest", cli.ErrConflict)
	}
	d.OwnAgreementID, d.OwnAgreementDigest = first[0].RecordID, first[0].Header.PayloadCommitments[0]
	if _, err = s.db.ExecContext(ctx, `UPDATE deal_v1 SET own_agreement_id=?, own_agreement_digest=? WHERE deal_id=?`, d.OwnAgreementID, d.OwnAgreementDigest, d.DealID); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE deal_versions SET state='agreed' WHERE deal_id=?`, d.DealID)
	return err
}

// ownBookV1 is the holder's deal book of the deal as records, in order, read
// back from the book.
func (s *dealSession) ownBookV1(ctx context.Context, d *v1Deal) ([]xdealv1.Record, error) {
	if d.OwnLogID == "" {
		return nil, nil
	}
	book, err := s.shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return nil, err
	}
	var out []xdealv1.Record
	if book.Size() == 0 {
		return nil, nil
	}
	all, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: book.Size()})
	if err != nil {
		return nil, err
	}
	for _, r := range all {
		if len(r.Header.PayloadCommitments) != 1 {
			return nil, fmt.Errorf("%w: a deal book record has no record digest", cli.ErrConflict)
		}
		payload, err := book.ResolvePayload(ctx, r.RecordID, r.Header.PayloadCommitments[0])
		if err != nil {
			return nil, err
		}
		rec, err := xdealv1.Decode(payload)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// gateFirstAct applies Steven's first-act gate (u610, u611; evidencebook
// GateAct) to an act the holder is about to seal in its deal book: a first
// perform or payer settle is refused until the counterparty's stamp proof
// covers its agreement, on both sides; a counterparty with no layer is not
// refused, and the answer says so (Unguarded), for the receipt to state. A
// payee leg, a statement and every later act are never gated. The act is the
// record the writer will seal, with its body and type; commitmentShelfLog is
// the shelf log the counterparty's published evidence commitment names, passed
// whenever the holder holds that commitment and "" when it holds none.
func (s *dealSession) gateFirstAct(ctx context.Context, dealID string, act xdealv1.Record, commitmentShelfLog string) (xdealv1.ActGate, error) {
	if s.v1Profile == nil {
		return xdealv1.ActGate{}, errors.New("the first-act gate needs the shelf opened for x-deal-v1 records (v1ReceiveContext)")
	}
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return xdealv1.ActGate{}, err
	}
	if d == nil || d.OwnAgreementID == "" {
		return xdealv1.ActGate{}, cli.InputError("deal " + dealID + " has no agreement of yours yet: `deal agree` opens your deal book")
	}
	own, err := s.ownBookV1(ctx, d)
	if err != nil {
		return xdealv1.ActGate{}, err
	}
	act.Block.DealID = dealID
	return s.v1Profile.GateAct(ctx, xdealv1.ActGateInput{Own: own, Act: act, CounterpartAgreement: d.CounterpartyAgreement, CommitmentShelfLog: commitmentShelfLog})
}
