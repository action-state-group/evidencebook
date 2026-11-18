// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"
)

// FieldsBookID is the book PlantFields adds: its one record commits a field
// and no payload (evidencebook#538).
const FieldsBookID = "fields/a"

// PlantFields adds, to a scripted shelf, a book whose one record commits
// one field and no payload: its header's field_commitments is an object,
// and the record has a retention state though it commits no payload. The
// field's nonce is fresh each run (crypto/rand), so no vector holds its
// bytes; the interlock compares each language's dump of the planted shelf
// with Go's.
func PlantFields(ctx context.Context, dir string) error {
	e, err := open(ctx, filepath.Join(dir, FileName), plantStream, Start.Add(plantAt*time.Second))
	if err != nil {
		return err
	}
	book, err := e.shelf.Open(ctx, FieldsBookID)
	if err != nil {
		return errors.Join(err, e.release())
	}
	_, err = book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent,
		Fields: map[string]string{"note": "a field and no payload"}})
	return errors.Join(err, e.release())
}
