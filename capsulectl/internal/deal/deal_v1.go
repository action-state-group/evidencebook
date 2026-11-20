package deal

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// The x-deal-v1 write path (docs/design/xdealv1-write-path.md). This file is
// the first slice: the profile's version setting and the one-way store move
// it can ask for (§3, §3a), the local version table, the refusal of the
// verbs a v1 deal has no form of (§7), and the offer and its cancellation as
// records sent whole (§4.2). Nothing here runs unless the profile declared
// x-deal-v1: v0 stays the default until `/3` is served.

const (
	dealVersionV0 = "x-deal-v0"
	dealVersionV1 = "x-deal-v1"
	// dealVersionFile is the profile's version setting, beside the store
	// like the History card's settings.
	dealVersionFile = "deal_version"
)

// dealV1Schema holds what the plugin keeps locally for a v1 deal and for
// every send. deal_versions says which version a deal is, so a version is
// never found by opening a book; deal_sent is one row per record sent whole,
// written before it is sealed so a re-run finds it (§4.2). They are created
// by the first v1 verb, never by a v0 command.
var dealV1Schema = []string{
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
}

// Send states: a row is stored as sealing before SealSent is called, sealed
// once the pair is stored, and written once the file is out.
const (
	sentSealing = "sealing"
	sentSealed  = "sealed"
	sentWritten = "written"
)

// dealVersionSetting is what the profile writes new deals as: x-deal-v0
// until `deal set-version x-deal-v1`.
func dealVersionSetting(p cli.Profile) (string, error) {
	dir, err := shDir(p)
	if err != nil {
		return "", err
	}
	raw, err := readSetting(filepath.Join(dir, dealVersionFile))
	if errors.Is(err, os.ErrNotExist) {
		return dealVersionV0, nil
	}
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(raw))
	if v != dealVersionV0 && v != dealVersionV1 {
		return "", fmt.Errorf("%w: the version setting %s names no deal version", cli.ErrConflict, dealVersionFile)
	}
	return v, nil
}

func putDealVersionSetting(p cli.Profile, v string) error {
	dir, err := shDir(p)
	if err != nil {
		return err
	}
	return putSetting(filepath.Join(dir, dealVersionFile), []byte(v+"\n"))
}

// isV1Deal reports whether the local version table names the deal as an
// x-deal-v1 deal. A store with no table, or a deal with no row, is v0: the
// check is read before anything is loaded, and opens no book.
func (s *dealSession) isV1Deal(ctx context.Context, dealID string) (bool, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='deal_versions'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var version string
	err = s.db.QueryRowContext(ctx, `SELECT version FROM deal_versions WHERE deal_id=?`, dealID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return version == dealVersionV1, err
}

// v1Refusal is what a verb with no x-deal-v1 form says about a v1 deal
// (Sherlock2, evidencebook-deal#215 section 7), before the deal loads.
func v1Refusal(verb, dealID string) error {
	switch verb {
	case "close", "terms-result":
		what := "close"
		if verb == "terms-result" {
			what = "terms result"
		}
		return cli.InputError("deal " + dealID + " is an x-deal-v1 deal and has no " + what +
			". Each row is decided by statements, dates and disputes, and the deal is settled once every row is agreed, uncontested, or ruled with its remedy done. Record a row with `deal statement`.")
	}
	return cli.InputError("deal " + dealID + " is an x-deal-v1 deal: `deal " + verb + "` is not served for x-deal-v1 deals yet")
}

// errNotMigrated is what a v1 verb says on a deal store still in the old
// layout, never the library's error (§3a, item 3).
func errNotMigrated() error {
	return fmt.Errorf("%w: %w", errNotMigratedSentinel, cli.InputError("This deal store is not yet in the layout x-deal-v1 deals need. See `deal set-version --help`; moving it is one-way."))
}

// errNotMigratedSentinel marks errNotMigrated for errors.Is.
var errNotMigratedSentinel = errors.New("deal store not migrated")

// requireV1 refuses a v1 verb unless the profile declared x-deal-v1 and the
// store is in the new layout.
func (s *dealSession) requireV1(ctx context.Context) (*evidencebook.Cadence, error) {
	v, err := dealVersionSetting(s.p)
	if err != nil {
		return nil, err
	}
	if v != dealVersionV1 {
		return nil, cli.InputError("this profile writes x-deal-v0 deals: run `deal set-version x-deal-v1` first")
	}
	return s.requireMigrated(ctx)
}

// requireMigrated refuses on a deal store still in the old layout. A verb
// that serves a deal already sent (a cancellation) needs only this: the
// setting governs the offers a profile writes, never a deal it holds.
func (s *dealSession) requireMigrated(ctx context.Context) (*evidencebook.Cadence, error) {
	c, _, err := s.cadenceOf(ctx)
	if err != nil {
		return nil, err
	}
	ok, err := c.Migrated(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotMigrated()
	}
	return c, nil
}

func dealSetVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-version VERSION",
		Short: "Choose the deal version this profile writes: x-deal-v0 (the default) or x-deal-v1 (with --migrate it also moves the deal store, one-way)",
		Long: `Choose the deal version this profile writes new deals as: x-deal-v0 (the
default) or x-deal-v1. It governs only the offers this profile writes: a deal
follows its offer's version, and deals already open keep theirs.

x-deal-v1 deals need the deal store in a new layout. Moving it is one-way:
older capsulectl builds, including one an app such as Muse or Goose bundles,
will not open it afterwards. Going back means restoring the backup written to
<store>.v1.bak during the move, which discards everything recorded after the
move; deleting or moving the backup ends that way back. If the move stops
part-way, the error says why; fix that and run the same command again, which
picks up where it stopped. x-deal-v0 never moves the store.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			version := args[0]
			if version != dealVersionV0 && version != dealVersionV1 {
				return cli.InputError("the version given to `deal set-version` must be x-deal-v0 or x-deal-v1")
			}
			migrate, _ := c.Flags().GetBool("migrate")
			if version == dealVersionV0 {
				if migrate {
					return cli.InputError("--migrate goes with x-deal-v1: x-deal-v0 never moves the store")
				}
				return runDeal(c, false, func(_ context.Context, s *dealSession, _ string, _ []sealedEvent) error {
					if err := putDealVersionSetting(s.p, version); err != nil {
						return err
					}
					return cli.Output(c, map[string]any{"deal_version": version})
				})
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				cad, _, err := s.cadenceOf(ctx)
				if err != nil {
					return err
				}
				done, err := cad.Migrated(ctx)
				if err != nil {
					return err
				}
				out := map[string]any{"deal_version": version}
				if !done {
					backup, err := backupPath(s.p)
					if err != nil {
						return err
					}
					if !migrate {
						return cli.InputError("x-deal-v1 deals need this deal store in a new layout. The move is one-way: afterwards, older capsulectl builds, including one an app such as Muse or Goose bundles, will not open the store, and going back means restoring the backup, written to " +
							backup + " during the move (if the move stops part-way, the error says why; fix that and run the same command again, which picks up where it stopped). Your open deals keep working. Run again with --migrate to move it.")
					}
					// A v0 step stored without its checkpoint after a crash gets
					// the checkpoint stamped with its own time before the
					// migration cuts one (deal_books.go coverSteps).
					if err := s.coverLiveSteps(ctx, cad); err != nil {
						return err
					}
					if err := cad.Migrate(ctx, cli.DealClock()); err != nil {
						return err
					}
					out["moved"], out["backup"] = true, backup
					out["message"] = "Moved. Backup: " + backup + "."
				}
				if err := putDealVersionSetting(s.p, version); err != nil {
					return err
				}
				return cli.Output(c, out)
			})
		},
	}
	cmd.Flags().Bool("migrate", false, "Also move the deal store to the layout x-deal-v1 deals need (one-way; a backup is written beside it)")
	return cmd
}

// backupPath is the full path of the store's backup, `<db>.v1.bak`.
func backupPath(p cli.Profile) (string, error) {
	db, err := filepath.Abs(p.Connection.Database)
	if err != nil {
		return "", err
	}
	return db + ".v1.bak", nil
}

// v1Now is the time a v1 record carries: the deal clock, whole seconds.
func v1Now() string { return cli.DealClock().UTC().Format(time.RFC3339) }

// newV1DealID mints a deal id, as `deal open` does.
func newV1DealID() (string, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	return "deal-" + hex.EncodeToString(id), nil
}

func v1Request(parts ...[]byte) string {
	sum := sha256.Sum256(bytes.Join(parts, []byte{0}))
	return hex.EncodeToString(sum[:])
}

// sentOp is a row of deal_sent.
type sentOp struct {
	recordDigest, requestDigest, dealID, kind, state string
	payload, pair                                    []byte
}

// findSentOp is the operation a request left behind, if any: it is looked
// up by the verb's own inputs, so a re-run needs no digest from the user.
func (s *dealSession) findSentOp(ctx context.Context, requestDigest string) (*sentOp, error) {
	var op sentOp
	err := s.db.QueryRowContext(ctx,
		`SELECT record_digest, request_digest, deal_id, kind, payload, state, pair FROM deal_sent WHERE request_digest=? ORDER BY rowid DESC LIMIT 1`,
		requestDigest).Scan(&op.recordDigest, &op.requestDigest, &op.dealID, &op.kind, &op.payload, &op.state, &op.pair)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// sealSentLib is the library's SealSent; a test replaces it to make the call
// fail the ways the library says it can.
var sealSentLib = func(ctx context.Context, shelf *evidencebook.Shelf, e evidencebook.Entry, actionID string) (evidencebook.Record, evidencebook.SentCapsule, error) {
	return shelf.SealSent(ctx, e, actionID)
}

// sendV1 seals a journal record that leaves the shelf whole and returns its
// pair, the standalone Capsule with the record (§4.2). A re-run of the same
// request finds the operation row and never seals a record twice: a pair
// already stored is written again; a row still sealing with no pair is sealed
// once more only when the journal holds no record of that digest.
func (s *dealSession) sendV1(ctx context.Context, requestDigest, dealID, kind string, payload []byte, digest string) (pair []byte, err error) {
	if _, err = s.shelf.Journal(ctx); err != nil {
		return nil, err
	}
	op, err := s.findSentOp(ctx, requestDigest)
	if err != nil {
		return nil, err
	}
	if op != nil && op.state != sentWritten {
		payload, digest, dealID = op.payload, op.recordDigest, op.dealID
	} else {
		op = nil
	}
	if op != nil && op.pair != nil {
		return op.pair, nil
	}
	if op == nil {
		if _, err = s.db.ExecContext(ctx,
			`INSERT INTO deal_sent (record_digest, request_digest, deal_id, kind, payload, state) VALUES (?, ?, ?, ?, ?, ?)`,
			digest, requestDigest, dealID, kind, payload, sentSealing); err != nil {
			return nil, err
		}
	} else {
		held, err := s.journalHolds(ctx, digest)
		if err != nil {
			return nil, err
		}
		if held {
			return nil, fmt.Errorf("%w: the record %s was sealed but its file was not made, and the library cannot rebuild it: for an offer, cancel it with `deal offer cancel`", cli.ErrConflict, digest)
		}
	}
	rec, sent, err := sealSentLib(ctx, s.shelf, evidencebook.Entry{
		RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload},
	}, dealID+xdealv1.SentSuffix)
	if err != nil && rec.RecordID == "" {
		return nil, err
	}
	// A record with an error is stored and its Capsule comes with it: the
	// file is made from that Capsule and the record is not sealed again.
	pair, perr := xdealv1.Pair(sent.Capsule, sent.Envelope, sent.Payload)
	if perr != nil {
		return nil, errors.Join(err, perr)
	}
	if _, serr := s.db.ExecContext(ctx, `UPDATE deal_sent SET pair=?, state=? WHERE record_digest=? AND request_digest=?`, pair, sentSealed, digest, requestDigest); serr != nil {
		return nil, errors.Join(err, serr)
	}
	return pair, nil
}

// journalHolds reports whether the journal holds a record whose payload
// commitment is the record's digest.
func (s *dealSession) journalHolds(ctx context.Context, digest string) (bool, error) {
	// Opening the journal first recovers a record a crash left stored but not
	// yet in its log; Records lists committed records only.
	if _, err := s.shelf.Journal(ctx); err != nil {
		return false, err
	}
	read, err := s.shelf.Records(ctx, evidencebook.JournalLogID)
	if err != nil {
		return false, err
	}
	for _, r := range read.Records {
		for _, c := range r.Header.PayloadCommitments {
			if c == digest {
				return true, nil
			}
		}
	}
	return false, nil
}

// writePair writes a pair to path, or to the command's output with no path,
// and marks the operation written. A file already at path holding the same
// pair is the earlier run's: it counts as written.
func (s *dealSession) writePair(ctx context.Context, c *cobra.Command, path, digest, requestDigest string, pair []byte) error {
	data := append(bytes.Clone(pair), '\n')
	if path == "" {
		if _, err := c.OutOrStdout().Write(data); err != nil {
			return err
		}
	} else if err := cli.AtomicFile(path, data, false); err != nil {
		if prev, rerr := os.ReadFile(path); rerr != nil || !bytes.Equal(prev, data) {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE deal_sent SET state=?, written=1 WHERE record_digest=? AND request_digest=?`, sentWritten, digest, requestDigest)
	return err
}

// journalRecordType is the x-deal-v1 record type of the journal record whose
// payload digest is digest, and whether the journal holds one.
func (s *dealSession) journalRecordType(ctx context.Context, digest string) (typ string, held bool, err error) {
	j, err := s.shelf.Journal(ctx)
	if err != nil {
		return "", false, err
	}
	read, err := s.shelf.Records(ctx, evidencebook.JournalLogID)
	if err != nil {
		return "", false, err
	}
	for _, r := range read.Records {
		for _, c := range r.Header.PayloadCommitments {
			if c != digest {
				continue
			}
			payload, err := j.ResolvePayload(ctx, r.RecordID, digest)
			if err != nil {
				return "", false, err
			}
			rec, err := xdealv1.Decode(payload)
			if err != nil {
				return "", true, nil
			}
			return rec.Block.RecordType, true, nil
		}
	}
	return "", false, nil
}

// sentOfferPair is the pair of the offer whose record digest is given, for the
// status provider's payment check: the pair this profile sent, else the pair
// it received. An offer with neither (one received before the pair was kept)
// has no pair: the check is then not run, which the provider says of a nil
// pair.
func (s *dealSession) sentOfferPair(ctx context.Context, offerDigest string) ([]byte, error) {
	var pair []byte
	err := s.db.QueryRowContext(ctx, `SELECT pair FROM deal_sent WHERE record_digest=? AND kind=? AND pair IS NOT NULL ORDER BY rowid DESC LIMIT 1`,
		offerDigest, xdealv1.TypePropose).Scan(&pair)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRowContext(ctx, `SELECT pair FROM deal_v1_offers WHERE offer_digest=? AND pair IS NOT NULL`, offerDigest).Scan(&pair)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
	}
	return pair, err
}
