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
// store refuses to open it, as before the shelf asked its own handle.
func TestAShelfNeverReprovisionsALogItCannotOpen(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	book := mustOpenBook(t, shelf, "deal-a")
	mustAppend(t, book, observation("s", "first"))
	require.NoError(t, shelf.Release())

	db, err := sql.Open("sqlite", "file:"+f.path)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE cll_meta SET state = X'00' WHERE log_id = 'deal-a'`)
	require.NoError(t, err)
	var before []byte
	require.NoError(t, db.QueryRow(`SELECT state FROM cll_meta WHERE log_id = 'deal-a'`).Scan(&before))

	shelf = f.open(t)
	_, err = shelf.Open(ctx, "deal-a")
	require.Error(t, err)
	var after []byte
	require.NoError(t, db.QueryRow(`SELECT state FROM cll_meta WHERE log_id = 'deal-a'`).Scan(&after))
	assert.Equal(t, before, after, "the log's state is left as it was")
	require.NoError(t, db.Close())
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
