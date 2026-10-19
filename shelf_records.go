// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ShelfRecord is a committed record of one of a shelf's books, as Records
// lists it.
type ShelfRecord struct {
	BookID   string
	Seq      uint64
	RecordID string
	Header   Header
	// Retention is the record's retention state now, as Book.Retention
	// gives it; empty for a record that commits no payload or field, which
	// has none.
	Retention RetentionState
}

// ShelfRecords is what Records reads: the committed records, and how many
// records each book holds beyond them.
type ShelfRecords struct {
	Records []ShelfRecord
	// Pending counts, by book, the records stored above the book's
	// committed entry count: a crash after PutRecord and before the log
	// append, which the book's next Open recovers. They are not listed.
	Pending map[string]int
}

// Records lists the committed records of the shelf's books, in book id then
// seq order, without opening a book: nothing is recovered, verified or
// written, so a reader across every deal of a profile costs two queries
// (the records, and every log's entry count), not one book opened per deal. A record is committed when 1 <= seq <= its
// book's log entry count (an entry count, not an MMR size). bookIDs narrows
// the listing to those books; none lists every book.
//
// The headers are as stored. Records is for readers on the device that
// wrote them; a book's Open is what checks them against its log.
func (s *Shelf) Records(ctx context.Context, bookIDs ...string) (ShelfRecords, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return ShelfRecords{}, ErrReleased
	}
	query := `SELECT book_id, seq, record_id, header FROM book_records`
	var args []any
	if len(bookIDs) > 0 {
		query += ` WHERE book_id IN (?` + strings.Repeat(`, ?`, len(bookIDs)-1) + `)`
		for _, id := range bookIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY book_id, seq`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ShelfRecords{}, err
	}
	type stored struct {
		book   string
		record Record
	}
	var all []stored
	for rows.Next() {
		var st stored
		var header []byte
		if err := rows.Scan(&st.book, &st.record.Seq, &st.record.RecordID, &header); err != nil {
			return ShelfRecords{}, errors.Join(err, rows.Close())
		}
		if err := json.Unmarshal(header, &st.record.Header); err != nil {
			return ShelfRecords{}, errors.Join(fmt.Errorf("%w: header of %s in %s: %v", ErrCorrupt, st.record.RecordID, st.book, err), rows.Close())
		}
		all = append(all, st)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ShelfRecords{}, err
	}
	entries := map[string]uint64{}
	counts, err := s.db.QueryContext(ctx, `SELECT log_id, COUNT(*) FROM cll_entries GROUP BY log_id`)
	if err != nil {
		return ShelfRecords{}, err
	}
	for counts.Next() {
		var id string
		var n uint64
		if err := counts.Scan(&id, &n); err != nil {
			return ShelfRecords{}, errors.Join(err, counts.Close())
		}
		entries[id] = n
	}
	if err := errors.Join(counts.Err(), counts.Close()); err != nil {
		return ShelfRecords{}, err
	}

	now := time.Now()
	if s.cfg.Now != nil {
		now = s.cfg.Now()
	}
	out := ShelfRecords{Records: []ShelfRecord{}, Pending: map[string]int{}}
	for i := 0; i < len(all); {
		book := all[i].book
		committed := entries[book]
		records := map[string]Record{}
		var order []string
		var listed []Record
		for ; i < len(all) && all[i].book == book; i++ {
			record := all[i].record
			if record.Seq < 1 || record.Seq > committed {
				out.Pending[book]++
				continue
			}
			if record.Seq != uint64(len(order))+1 {
				return ShelfRecords{}, fmt.Errorf("%w: %s holds record %s at seq %d after %d records", ErrCorrupt, book, record.RecordID, record.Seq, len(order))
			}
			records[record.RecordID] = record
			order = append(order, record.RecordID)
			listed = append(listed, record)
		}
		for _, record := range listed {
			view := ShelfRecord{BookID: book, Seq: record.Seq, RecordID: record.RecordID, Header: record.Header}
			if len(record.Header.PayloadCommitments) > 0 || len(record.Header.FieldCommitments) > 0 {
				state, _, _, err := retentionOf(records, order, record.RecordID, now)
				if err != nil {
					return ShelfRecords{}, err
				}
				view.Retention = state
			}
			out.Records = append(out.Records, view)
		}
	}
	return out, nil
}
