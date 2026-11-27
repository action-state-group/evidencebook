// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// sqliteOpens counts the SQLite connections opened on files under a
// directory, through the driver's connection hook (one per connection, the
// unit a fresh schema parse is paid in).
var sqliteOpens = struct {
	once sync.Once
	mu   sync.Mutex
	dirs map[string]int
}{dirs: map[string]int{}}

func countSQLiteOpens(t *testing.T, dir string) func() int {
	t.Helper()
	sqliteOpens.once.Do(func() {
		sqlite.RegisterConnectionHook(func(_ sqlite.ExecQuerierContext, dsn string) error {
			sqliteOpens.mu.Lock()
			defer sqliteOpens.mu.Unlock()
			for d := range sqliteOpens.dirs {
				if strings.Contains(dsn, d) {
					sqliteOpens.dirs[d]++
				}
			}
			return nil
		})
	})
	sqliteOpens.mu.Lock()
	sqliteOpens.dirs[dir] = 0
	sqliteOpens.mu.Unlock()
	t.Cleanup(func() {
		sqliteOpens.mu.Lock()
		delete(sqliteOpens.dirs, dir)
		sqliteOpens.mu.Unlock()
	})
	return func() int {
		sqliteOpens.mu.Lock()
		defer sqliteOpens.mu.Unlock()
		n := sqliteOpens.dirs[dir]
		sqliteOpens.dirs[dir] = 0
		return n
	}
}

// A shelf asks its own handle whether a book's log is new, so a new book
// costs its CLL store's Init and Open and nothing else, and a book the file
// already holds costs only its CLL store's Open (evidencebook#1040): no
// failed open, no read-only probe of its own.
func TestAShelfOpensABooksLogWithoutAProbeOfItsOwn(t *testing.T) {
	f := newShelfFixture(t)
	opens := countSQLiteOpens(t, f.path)
	shelf := f.open(t)
	require.Positive(t, opens(), "the shelf's own handle")
	_ = mustOpenBook(t, shelf, "deal-a")
	assert.Equal(t, 2, opens(), "a new book: its CLL store's Init and Open")
	require.NoError(t, shelf.Release())
	opens()

	shelf = f.open(t)
	opens()
	_ = mustOpenBook(t, shelf, "deal-a")
	assert.Equal(t, 1, opens(), "a book the file holds: its CLL store's Open")
}

// A log the file holds state of is never provisioned over when its CLL
// store refuses to open it, whichever of the shelf's openers reaches it (a
// book, the cadence log, a host log): the shelf's own handle sees the
// state, so no Init writes a fresh meta row beside it.
func TestAShelfNeverReprovisionsALogItCannotOpen(t *testing.T) {
	ctx := context.Background()
	const logID = "deal-x"
	for name, open := range map[string]func(*Shelf) error{
		"book":     func(s *Shelf) error { _, err := s.Open(ctx, logID); return err },
		"host log": func(s *Shelf) error { _, err := s.HostLog(ctx, logID, false); return err },
		"cadence": func(s *Shelf) error {
			_, err := s.Cadence(ctx, CadenceConfig{LogID: logID, Ended: func(context.Context, string) (bool, error) { return false, nil }})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newShelfFixture(t)
			db, err := sql.Open("sqlite", "file:"+f.path)
			require.NoError(t, err)
			defer db.Close()
			// An entry with no meta row: not a log anyone provisioned.
			for _, statement := range []string{
				`CREATE TABLE cll_entries (log_id TEXT NOT NULL, seq INTEGER NOT NULL, value BLOB NOT NULL, appended_at TEXT NOT NULL, PRIMARY KEY(log_id, seq))`,
				`INSERT INTO cll_entries VALUES ('` + logID + `', 1, x'00', '2026-10-19T00:00:00Z')`,
			} {
				_, err := db.Exec(statement)
				require.NoError(t, err)
			}
			shelf := f.open(t)
			require.Error(t, open(shelf), "a log with entries and no state must not open")
			var meta, entries int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'cll_meta'`).Scan(&meta))
			assert.Zero(t, meta, "no Init ran: the file has no meta table")
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM cll_entries WHERE log_id = ?`, logID).Scan(&entries))
			assert.Equal(t, 1, entries, "the log's entries are left as they were")
		})
	}
}

// ReadShelfWindowFrom reads on the caller's handle what ReadShelfWindow reads
// by path: the stored policy, or nil for a shelf with none.
func TestReadShelfWindowFromReadsWhatReadShelfWindowReads(t *testing.T) {
	ctx := context.Background()
	for _, f := range []*shelfFixture{newGraceFixture(t), newShelfFixture(t)} {
		require.NoError(t, f.open(t).Release())
		byPath, err := ReadShelfWindow(ctx, f.path)
		require.NoError(t, err)
		opens := countSQLiteOpens(t, f.path)
		db, err := sql.Open("sqlite", "file:"+f.path)
		require.NoError(t, err)
		onHandle, err := ReadShelfWindowFrom(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, byPath, onHandle)
		assert.Equal(t, 1, opens(), "the caller's handle only")
		require.NoError(t, db.Close())
	}
	// A file that is not a shelf has no window.
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/other.db")
	require.NoError(t, err)
	policy, err := ReadShelfWindowFrom(ctx, db)
	require.NoError(t, err)
	assert.Nil(t, policy)
	require.NoError(t, db.Close())
}
