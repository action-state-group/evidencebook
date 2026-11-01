// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"
)

// Dump is everything the contract fixes in the scripted shelf's file: the
// shelf's tables row for row (stored JSON kept as its text), the CLL entries
// and node digest of every log, and what the shelf derives from them:
// Records, each finished tick's StandardHistoryAnchor, and the cadence chain
// of every checkpoint a tick carries. Two implementations agree when their
// dumps of the same script are equal.
//
// Dump reads a copy of the file, so it never changes the shelf: deriving a
// chain opens a book, and opening a book with a pending record recovers it.
func Dump(ctx context.Context, dir string) ([]byte, error) {
	scratch, err := os.MkdirTemp("", "shelf-dump-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	path := filepath.Join(scratch, FileName)
	original, err := openDB(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	_, err = original.ExecContext(ctx, `VACUUM INTO ?`, path)
	if err := errors.Join(err, original.Close()); err != nil {
		return nil, err
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	state := map[string]any{}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM shelf_meta WHERE id = 1`).Scan(&version); err != nil {
		return nil, fmt.Errorf("shelf_meta: %w", err)
	}
	state["shelf_meta"] = map[string]any{"version": version}
	tables := []struct {
		name, query string
		row         func(*sql.Rows) (any, error)
	}{
		{"shelf_logs", `SELECT log_id, kind FROM shelf_logs ORDER BY log_id`, func(r *sql.Rows) (any, error) {
			var id, kind string
			err := r.Scan(&id, &kind)
			return map[string]any{"log_id": id, "kind": kind}, err
		}},
		{"book_records", `SELECT book_id, seq, record_id, header, capsule, envelope FROM book_records ORDER BY book_id, seq`, func(r *sql.Rows) (any, error) {
			var book, id string
			var seq int64
			var header, capsule, envelope []byte
			err := r.Scan(&book, &seq, &id, &header, &capsule, &envelope)
			return map[string]any{"book_id": book, "seq": seq, "record_id": id, "header": rawJSON(header), "capsule_sha256": digest(capsule), "envelope_sha256": digest(envelope)}, err
		}},
		{"book_checkpoints", `SELECT book_id, n, checkpoint_id, checkpoint FROM book_checkpoints ORDER BY book_id, n`, func(r *sql.Rows) (any, error) {
			var book, id string
			var n int64
			var cp []byte
			err := r.Scan(&book, &n, &id, &cp)
			return map[string]any{"book_id": book, "n": n, "checkpoint_id": id, "checkpoint": rawJSON(cp)}, err
		}},
		{"cadence_logs", `SELECT log_id FROM cadence_logs ORDER BY log_id`, scanString},
		{"cadence_live", `SELECT log_id FROM cadence_live ORDER BY log_id`, scanString},
		{"cadence_witnessed", `SELECT log_id, checkpoint_sha256, mmr_size, tick FROM cadence_witnessed ORDER BY log_id, checkpoint_sha256`, func(r *sql.Rows) (any, error) {
			var id, sum string
			var size, tick int64
			err := r.Scan(&id, &sum, &size, &tick)
			return map[string]any{"log_id": id, "checkpoint_sha256": sum, "mmr_size": size, "tick": tick}, err
		}},
		{"cadence_ticks", `SELECT tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt FROM cadence_ticks ORDER BY tick`, func(r *sql.Rows) (any, error) {
			var tick, entry, size, witnessed int64
			var at, due, leaves string
			var cp []byte
			var receipt sql.NullString
			err := r.Scan(&tick, &at, &due, &entry, &size, &cp, &leaves, &witnessed, &receipt)
			out := map[string]any{"tick": tick, "at": at, "due_next": due, "entry_seq": entry, "checkpoint_size": size, "checkpoint": base64url(cp), "leaves": leaves, "witnessed": witnessed, "receipt": nil}
			if receipt.Valid {
				out["receipt"] = receipt.String
			}
			return out, err
		}},
		{"cll_entries", `SELECT log_id, seq, value, appended_at FROM cll_entries ORDER BY log_id, seq`, func(r *sql.Rows) (any, error) {
			var id, at string
			var seq int64
			var value []byte
			err := r.Scan(&id, &seq, &value, &at)
			return map[string]any{"log_id": id, "seq": seq, "value": hex.EncodeToString(value), "appended_at": at}, err
		}},
	}
	for _, table := range tables {
		rows, err := query(ctx, db, table.query, table.row)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", table.name, err)
		}
		state[table.name] = rows
	}
	nodes, err := nodeDigests(ctx, db)
	if err != nil {
		return nil, err
	}
	state["cll_nodes_sha256"] = nodes
	derived, err := derive(ctx, path)
	if err != nil {
		return nil, err
	}
	for k, v := range derived {
		state[k] = v
	}
	return marshal(state)
}

func scanString(r *sql.Rows) (any, error) {
	var s string
	err := r.Scan(&s)
	return s, err
}

func query(ctx context.Context, db *sql.DB, q string, row func(*sql.Rows) (any, error)) ([]any, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		v, err := row(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// nodeDigests is, per CLL log, SHA-256 of its MMR nodes concatenated in
// position order: the same nodes give every root of every checkpoint.
func nodeDigests(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT log_id, node FROM cll_nodes ORDER BY log_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := map[string][]byte{}
	for rows.Next() {
		var id string
		var node []byte
		if err := rows.Scan(&id, &node); err != nil {
			return nil, err
		}
		nodes[id] = append(nodes[id], node...)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for id, b := range nodes {
		out[id] = digest(b)
	}
	return out, nil
}

// derive opens the shelf (on the dump's copy) and reads what it derives.
// Records comes first, so it lists a pending record before a chain's book
// open recovers it.
func derive(ctx context.Context, path string) (map[string]any, error) {
	e, err := open(ctx, path, "evidencebook shelf vectors/dump", Start.Add(3*time.Hour))
	if err != nil {
		return nil, err
	}
	out, err := e.derive(ctx)
	return out, errors.Join(err, e.release())
}

func (e *env) derive(ctx context.Context) (map[string]any, error) {
	listing, err := e.shelf.Records(ctx)
	if err != nil {
		return nil, err
	}
	records := []any{}
	for _, r := range listing.Records {
		records = append(records, map[string]any{"book_id": r.BookID, "seq": r.Seq, "record_id": r.RecordID, "record_type": r.Header.RecordType, "retention": string(r.Retention)})
	}
	pending := map[string]any{}
	for id, n := range listing.Pending {
		pending[id] = n
	}
	last, ok, err := e.cadence.Last(ctx)
	if err != nil || !ok {
		return nil, errors.Join(err, errors.New("the scripted cadence has no tick"))
	}
	anchors, roots, chains := []any{}, []any{}, map[string]any{}
	for n := uint64(1); n <= last.N; n++ {
		tick, err := e.cadence.Get(ctx, n)
		if err != nil {
			return nil, err
		}
		root, _, err := tick.Leaves.Tree(tick.Leaves.FillerIndex)
		if err != nil {
			return nil, err
		}
		roots = append(roots, hex.EncodeToString(root))
		anchor, err := e.cadence.StandardHistoryAnchor(ctx, n)
		if err != nil {
			return nil, err
		}
		anchors = append(anchors, map[string]any{
			"tick": anchor.Tick.N, "anchor": anchor.AsOf.Anchor, "anchor_time": anchor.AsOf.AnchorTime,
			"first_checkpoint_time": anchor.AsOf.FirstCheckpointTime, "extents": anchor.Extents,
		})
		ids := make([]string, 0, len(tick.Leaves.Deals))
		for id := range tick.Leaves.Deals {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			statement, err := base64.RawURLEncoding.DecodeString(tick.Leaves.Deals[id].Statement)
			if err != nil {
				return nil, err
			}
			key := fmt.Sprintf("%d %s", n, id)
			chain, err := e.cadence.Chain(ctx, statement)
			switch {
			case errors.Is(err, evidencebook.ErrNotFound):
				chains[key] = map[string]any{"error": "not_found"}
			case err != nil:
				return nil, fmt.Errorf("chain of %s: %w", key, err)
			default:
				chains[key] = chain
			}
		}
	}
	return map[string]any{
		"records":    map[string]any{"records": records, "pending": pending},
		"tick_roots": roots, "standard_history_anchors": anchors, "chains": chains,
	}, nil
}
