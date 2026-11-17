// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/checkpointed-local-log/go/cll"
	cllsqlite "github.com/action-state-group/checkpointed-local-log/go/store/sqlite"
)

// The recovery scenarios (evidencebook#461, docs/design/461-shelf-facts.md
// §7): Go leaves the scripted shelf, after ContinuePhase, as a crash leaves
// it, and any language finishes it with RecoverPhase; every dump must equal
// the scenario's vector. A plant runs the interrupted operation on a copy of
// the file and copies back only what the crash would have left: after
// ContinuePhase every book is already checkpointed at its size, so these
// operations cut no book checkpoint and only cadence rows and entries differ.

// RecoverPhase finishes whatever a plant left: Migrate (which first finishes
// a pending tree tick, or resumes a migration), then a stamp.
var RecoverPhase = []Step{
	{Op: "open", Stream: RecoverStream, At: 14400},
	{Op: "migrate"},
	{Op: "stamp", At: 14460},
	{Op: "release"},
}

// RecoverStream is the random stream of RecoverPhase.
const RecoverStream = "evidencebook shelf vectors/recover"

// Recover runs RecoverPhase on dir/shelf.db.
func Recover(ctx context.Context, dir string) error {
	return Run(ctx, filepath.Join(dir, FileName), RecoverPhase)
}

// The plants' own times and streams, after ContinuePhase.
const (
	plantAt     = 10800
	plantStream = "evidencebook shelf vectors/plant"
)

// PlantTick leaves an empty host log in the cadence and a tree tick stored
// but not finished, as a crash after the tick's row and before its root
// leaves them (the version 1 shelf §7 names).
func PlantTick(ctx context.Context, dir string) error {
	path := filepath.Join(dir, FileName)
	e, err := open(ctx, path, plantStream, Start.Add(plantAt*time.Second))
	if err != nil {
		return err
	}
	if _, err := e.shelf.HostLog(ctx, "legacy/empty", true); err != nil {
		return errors.Join(err, e.release())
	}
	if err := e.release(); err != nil {
		return err
	}
	return onCopy(ctx, path, func(copyPath string) error {
		c, err := open(ctx, copyPath, plantStream, Start.Add(plantAt*time.Second))
		if err != nil {
			return err
		}
		if _, err := c.tick(ctx); err != nil {
			return errors.Join(err, c.release())
		}
		return c.release()
	}, func(ctx context.Context, original, copied *sql.DB) error {
		return copyLastTick(ctx, path, original, copied, 0)
	})
}

// PlantMigration leaves the shelf as a crash in Migrate's step 4 leaves it:
// version 2, the migration in state migrating with its backup recorded and
// in place, its batch stored, and the batch's first `appended` entries in the
// cadence log.
func PlantMigration(ctx context.Context, dir string, appended int) error {
	path := filepath.Join(dir, FileName)
	return onCopy(ctx, path, func(copyPath string) error {
		c, err := open(ctx, copyPath, plantStream, Start.Add(plantAt*time.Second))
		if err != nil {
			return err
		}
		if err := c.cadence.Migrate(ctx, c.clock.Now()); err != nil {
			return errors.Join(err, c.release())
		}
		if err := c.release(); err != nil {
			return err
		}
		return copyFile(copyPath+".v1.bak", path+".v1.bak")
	}, func(ctx context.Context, original, copied *sql.DB) error {
		var digest string
		if err := copied.QueryRowContext(ctx, `SELECT backup_sha256 FROM shelf_migration WHERE id = 1`).Scan(&digest); err != nil {
			return err
		}
		for _, statement := range []string{
			`UPDATE shelf_meta SET version = 2 WHERE id = 1`,
			fmt.Sprintf(`INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, 'migrating', '%s')`, digest),
		} {
			if _, err := original.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return copyLastTick(ctx, path, original, copied, appended)
	})
}

// onCopy runs act on a copy of the shelf at path, then apply on the original
// and the copy, in one transaction on the original.
func onCopy(ctx context.Context, path string, act func(copyPath string) error, apply func(ctx context.Context, original, copied *sql.DB) error) error {
	scratch, err := os.MkdirTemp(filepath.Dir(path), "plant-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	copyPath := filepath.Join(scratch, FileName)
	original, err := openDB(path)
	if err != nil {
		return err
	}
	defer original.Close()
	if _, err := original.ExecContext(ctx, `VACUUM INTO ?`, copyPath); err != nil {
		return err
	}
	if err := act(copyPath); err != nil {
		return err
	}
	copied, err := openDB(copyPath)
	if err != nil {
		return err
	}
	defer copied.Close()
	return apply(ctx, original, copied)
}

// copyLastTick stores the copy's last tick in the original as not finished,
// and appends the first `appended` entries of its batch (a stamp's) to the
// original's cadence log, with their shelf_entries rows, as finishing it
// would have before a crash.
func copyLastTick(ctx context.Context, path string, original, copied *sql.DB, appended int) error {
	var n int64
	var at, due, leaves string
	if err := copied.QueryRowContext(ctx, `SELECT tick, at, due_next, leaves FROM cadence_ticks ORDER BY tick DESC LIMIT 1`).Scan(&n, &at, &due, &leaves); err != nil {
		return err
	}
	if _, err := original.ExecContext(ctx,
		`INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)`, n, at, due, leaves); err != nil {
		return err
	}
	if appended == 0 {
		return nil
	}
	rows, err := copied.QueryContext(ctx, `SELECT entry_seq, kind, book_id, log_id, doc, link_opening, status_opening FROM shelf_entries WHERE tick = ? ORDER BY entry_seq LIMIT ?`, n, appended)
	if err != nil {
		return err
	}
	type entry struct {
		seq               int64
		kind, book, log   string
		doc, link, status []byte
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.seq, &e.kind, &e.book, &e.log, &e.doc, &e.link, &e.status); err != nil {
			return errors.Join(err, rows.Close())
		}
		entries = append(entries, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return err
	}
	log, err := cllsqlite.Open(path, CadenceLogID)
	if err != nil {
		return err
	}
	defer log.Close()
	for _, e := range entries {
		sum := sha256.Sum256(e.doc)
		result, err := log.Append(ctx, cll.AppendInput{Value: sum[:], AppendedAt: when.UTC()})
		if err != nil {
			return err
		}
		if int64(result.Entry.Seq) != e.seq {
			return fmt.Errorf("planted entry %d landed at %d", e.seq, result.Entry.Seq)
		}
		if _, err := original.ExecContext(ctx,
			`INSERT INTO shelf_entries (entry_seq, kind, book_id, log_id, doc, link_opening, status_opening, tick) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			e.seq, e.kind, e.book, e.log, e.doc, e.link, e.status, n); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return errors.Join(err, out.Close())
	}
	return out.Close()
}
