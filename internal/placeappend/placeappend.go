// SPDX-License-Identifier: Apache-2.0

// Package placeappend appends a record that names its own place in a book:
// it is built for the book's next place and appended only there
// (Book.AppendAt), and a write that took that place first makes it be built
// again from a fresh read. The deal-book writer (package deal) and the case
// book's ruling writer (xdealv1.SealRuling) both append this way.
package placeappend

import (
	"context"
	"errors"
	"fmt"

	"github.com/action-state-group/evidencebook"
)

// Tries is how many times a record whose place was taken is built again
// before Append gives up.
const Tries = 8

// ErrKeptChanging is what Append returns when every try found its place
// taken. It wraps evidencebook.ErrBusy: sending the same call again later may
// succeed.
var ErrKeptChanging = fmt.Errorf("%w: the book kept changing while the record was sealed", evidencebook.ErrBusy)

// Try is one built record.
type Try struct {
	// Skip, when set, appends nothing: the build found its answer in the book
	// (the record already there).
	Skip bool
	// Entry is the record, built for the place it was given.
	Entry evidencebook.Entry
	// Place, when set, appends through it instead of on the book directly (a
	// windowed shelf places a new book in a window as it appends its first
	// record). It calls appendAt at most once and returns evidencebook.ErrBusy
	// when the place was taken without calling it.
	Place func(appendAt func(*evidencebook.Book) error) error
	// Done runs once the record is appended, with the stored record.
	Done func(evidencebook.Record) error
}

// Append builds a record for book's next place and appends it there, up to
// tries times. Only the append's own evidencebook.ErrBusy (the place was
// taken) is retried: a busy error from the build or from Done is returned as
// it is, since a retry there could append the record twice.
func Append(ctx context.Context, book *evidencebook.Book, build func(size uint64) (Try, error), tries int) error {
	for range tries {
		size := book.Size()
		t, err := build(size)
		if err != nil || t.Skip {
			return err
		}
		var stored evidencebook.Record
		appendAt := func(b *evidencebook.Book) error {
			var err error
			stored, err = b.AppendAt(ctx, size+1, t.Entry)
			return err
		}
		if t.Place != nil {
			err = t.Place(appendAt)
		} else {
			err = appendAt(book)
		}
		if errors.Is(err, evidencebook.ErrBusy) {
			continue
		}
		if err != nil {
			return err
		}
		if t.Done != nil {
			return t.Done(stored)
		}
		return nil
	}
	return ErrKeptChanging
}
