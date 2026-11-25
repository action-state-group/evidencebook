package deal

import (
	"bytes"
	"context"
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

// dealVersionSetting is what the profile writes new deals as: x-deal-v1,
// the default (evidencebook#626), unless the profile set the legacy x-deal-v0
// with `deal set-version x-deal-v0`.
func dealVersionSetting(p cli.Profile) (string, error) {
	dir, err := shDir(p)
	if err != nil {
		return "", err
	}
	raw, err := readSetting(filepath.Join(dir, dealVersionFile))
	if errors.Is(err, os.ErrNotExist) {
		return dealVersionV1, nil
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
			". Each row is decided by statements, dates and disputes, and the deal is settled once every row is agreed, uncontested, or ruled with its remedy done. Record a row with `deal statement`. See `deal report`.")
	}
	return cli.InputError("deal " + dealID + " is an x-deal-v1 deal: `deal " + verb + "` is not served for x-deal-v1 deals yet")
}

// errNotMigrated is what a v1 verb says on a deal store still in the old
// layout, never the library's error (§3a, item 3).
func errNotMigrated() error {
	return fmt.Errorf("%w: %w", errNotMigratedSentinel, cli.InputError("This deal store is in an older layout, and x-deal-v1 deals (the default) need the new one. Move it once with `deal set-version x-deal-v1 --migrate` (one-way; a backup is kept beside the store, and `deal set-version --help` says more). Your open deals keep their version and keep working after the move."))
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
		return nil, cli.InputError("this profile is set to the legacy x-deal-v0: run `deal set-version x-deal-v1` to write x-deal-v1 deals")
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
		Short: "Choose the deal version this profile writes: x-deal-v1 (the default) or the legacy x-deal-v0 (with --migrate it also moves the deal store, one-way)",
		Long: `Choose the deal version this profile writes new deals as: x-deal-v1 (the
default) or the legacy x-deal-v0, which is frozen and kept only for a
counterparty that runs x-deal-v0 alone; it will be removed once those have
moved. It governs only the deals this profile starts: a deal follows its
offer's version, and deals already open keep theirs.

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

func v1Request(parts ...[]byte) string {
	sum := sha256.Sum256(bytes.Join(parts, []byte{0}))
	return hex.EncodeToString(sum[:])
}

// journalRecords reads the whole journal in order, across its windows on a
// windowed store (evidencebook#797), with the reader that resolves its
// payloads. Opening the journal recovers a record a crash left stored but
// not yet in its log, so it is read here.
func (s *dealSession) journalRecords(ctx context.Context) (evidencebook.JournalReader, []evidencebook.Record, error) {
	j, err := s.shelf.JournalView(ctx)
	if err != nil {
		return nil, nil, err
	}
	records, err := j.Query(ctx, evidencebook.Filter{})
	return j, records, err
}

// journalHolds reports whether the journal holds a record whose payload
// commitment is the record's digest.
func (s *dealSession) journalHolds(ctx context.Context, digest string) (bool, error) {
	_, records, err := s.journalRecords(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range records {
		for _, c := range r.Header.PayloadCommitments {
			if c == digest {
				return true, nil
			}
		}
	}
	return false, nil
}

// journalRecord is the x-deal-v1 record the journal holds under payload
// digest digest (nil when the journal's record there is not one), and
// whether the journal holds one.
func (s *dealSession) journalRecord(ctx context.Context, digest string) (rec *xdealv1.Record, held bool, err error) {
	j, records, err := s.journalRecords(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, r := range records {
		for _, c := range r.Header.PayloadCommitments {
			if c != digest {
				continue
			}
			payload, err := j.ResolvePayload(ctx, r.RecordID, digest)
			if err != nil {
				return nil, false, err
			}
			r, err := xdealv1.Decode(payload)
			if err != nil {
				return nil, true, nil
			}
			return &r, true, nil
		}
	}
	return nil, false, nil
}

// initMigratesStore makes `deal init` start a new store in the new layout; a
// seam, so the x-deal-v0 suite can build the old layout it tests.
var initMigratesStore = true

// migrateNewStoreFn is migrateNewStore, a seam for tests.
var migrateNewStoreFn = migrateNewStore

// migrateNewStore moves a store `deal init` just made, which holds nothing,
// to the layout x-deal-v1 deals need, and removes the backup the move writes,
// which holds nothing either.
func migrateNewStore(ctx context.Context, p cli.Profile) (err error) {
	s, err := openDealSession(ctx, p)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	cad, _, err := s.cadenceOf(ctx)
	if err != nil {
		return err
	}
	if err := cad.Migrate(ctx, cli.DealClock()); err != nil {
		return err
	}
	// The backup of a store that held nothing is removed; failing to remove
	// it leaves an empty file, which harms nothing, so it is not an error.
	if backup, err := backupPath(p); err == nil {
		_ = os.Remove(backup)
	}
	return nil
}

// requireLegacyV0 refuses a verb that starts an x-deal-v0 deal or sale unless
// the profile set the legacy version: x-deal-v1 is the default (#626).
func requireLegacyV0(c *cobra.Command, what string) error {
	p, err := cli.Selected(c)
	if err != nil {
		return err
	}
	v, err := dealVersionSetting(p)
	if err != nil {
		return err
	}
	if v == dealVersionV0 {
		return nil
	}
	return cli.InputError(what + ", and this profile writes x-deal-v1 deals: start one with `deal offer`. For a counterparty that runs only x-deal-v0, set the legacy version first: `deal set-version x-deal-v0`.")
}
