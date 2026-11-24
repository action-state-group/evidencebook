// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"fmt"
	"time"
)

// A book that continues in a windowed store (evidencebook#808,
// docs/design/689-window-books.md §11): a JSONL book a holder converts is
// closed as a whole, by a book_close record and a final checkpoint right
// after it, and the store's first window names that final tree as its
// previous. Its records never move.

// RecordTypeBookClose is the record type of a book's close into a windowed
// store, its last record.
const RecordTypeBookClose = "book_close"

// BookCloseVersion is the record_version of a book_close body.
const BookCloseVersion = "book_close/v1"

// BookCloseStatement is the body of a book_close record: the book's records
// before it by record_type, and the windowed store that continues it.
type BookCloseStatement struct {
	RecordVersion string         `json:"record_version"`
	CountsByKind  map[string]int `json:"counts_by_kind"`
	Successor     BookSuccessor  `json:"successor"`
}

// BookSuccessor names the windowed store a closed book continues in: its
// base log id, period and grace in seconds. Its first window starts at the
// first whole second after the close record's commit time (SuccessorSince).
type BookSuccessor struct {
	Base         string       `json:"base"`
	Period       WindowPeriod `json:"period"`
	GraceSeconds int64        `json:"grace_seconds"`
}

// SuccessorSince is when the windowed store continuing a book closed at
// committedAt starts: the first whole second after it, so no window holds a
// moment the book also does.
func SuccessorSince(committedAt time.Time) time.Time {
	return committedAt.UTC().Truncate(time.Second).Add(time.Second)
}

// CloseForWindows closes a book into a windowed store with the window it
// names (evidencebook#808): a checkpoint that commits any pending index
// roots, the book_close record, and the final checkpoint, whose tree ends
// at that record. The book takes no record after it. It returns the policy
// the store is created with: the period, base and grace named, Since from
// the close record's commit time, the book's final tree as the predecessor,
// and the disclosure policy in force at the close. A book already closed
// returns its policy again when it names the same successor, so a host
// resumes a conversion a crash interrupted; a different successor is
// refused. A window book cannot be closed this way. The store starts at
// the first whole second after the close, so its host waits until then
// before its first write (Shelf.Current refuses an earlier clock).
func (b *Book) CloseForWindows(ctx context.Context, base string, period WindowPeriod, grace time.Duration) (*WindowPolicy, error) {
	successor := BookSuccessor{Base: base, Period: period, GraceSeconds: int64(grace / time.Second)}
	if grace%time.Second != 0 {
		return nil, fmt.Errorf("%w: a grace is whole seconds", ErrInvalid)
	}
	b.mu.Lock()
	if b.window != nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("%w: a window book is closed by its shelf", ErrInvalid)
	}
	if n := len(b.log); n == 0 || b.log[n-1].Header.RecordType != RecordTypeBookClose {
		if n == 0 {
			b.mu.Unlock()
			return nil, fmt.Errorf("%w: an empty book has nothing to continue; create the windowed store instead", ErrInvalid)
		}
		// The roots are committed and the close appended under one hold of
		// the lock, so no record makes a root stale between them; and the
		// successor is checked first, since the close cannot be undone.
		cp, err := b.checkpointLocked(ctx)
		if err == nil {
			provisional := WindowPolicy{Period: period, Base: base, Since: SuccessorSince(time.Unix(0, 0)), Grace: grace,
				Predecessor: &WindowTree{LogID: cp.LogID, Entries: cp.Entries + 1, Root: cp.Root}}
			err = provisional.validate()
		}
		if err == nil {
			counts := map[string]int{}
			for _, record := range b.log {
				counts[record.Header.RecordType]++
			}
			var body []byte
			if body, err = canonicalJSON(BookCloseStatement{RecordVersion: BookCloseVersion, CountsByKind: counts, Successor: successor}); err == nil {
				b.closing = true
				_, err = b.appendLocked(ctx, Entry{RecordType: RecordTypeBookClose, EpistemicType: ProducerClaim, Links: []Link{}, Statement: body})
				b.closing = false
			}
		}
		if err != nil {
			b.mu.Unlock()
			return nil, err
		}
	}
	last := b.log[len(b.log)-1]
	b.mu.Unlock()
	var stated BookCloseStatement
	if err := decodeStrict(last.Header.Statement, &stated); err != nil || stated.RecordVersion != BookCloseVersion {
		return nil, fmt.Errorf("%w: book %s's book_close record does not carry its body", ErrCorrupt, b.id)
	}
	if stated.Successor != successor {
		return nil, fmt.Errorf("%w: book %s is closed into a %s window under %q with a grace of %ds, not this one", ErrInvalid, b.id, stated.Successor.Period, stated.Successor.Base, stated.Successor.GraceSeconds)
	}
	final, err := b.finalOfClosed(ctx)
	if err != nil {
		return nil, err
	}
	closedAt, err := time.Parse(time.RFC3339Nano, last.Header.CommittedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: book %s's book_close commit time: %v", ErrCorrupt, b.id, err)
	}
	policy := &WindowPolicy{
		Period: period, Base: base, Since: SuccessorSince(closedAt), Grace: grace,
		Predecessor: &WindowTree{LogID: final.LogID, Entries: final.Entries, Root: final.Root},
	}
	b.mu.Lock()
	inForce, ok, err := policyInForce(b.log)
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if ok {
		policy.PredecessorPolicy = &inForce
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return policy, nil
}

// finalOfClosed is a closed book's final checkpoint, taken now when a crash
// left it untaken: a checkpoint of every record, which ends at the close.
func (b *Book) finalOfClosed(ctx context.Context) (Checkpoint, error) {
	b.mu.Lock()
	size := uint64(len(b.log))
	b.mu.Unlock()
	stored, err := b.store.Checkpoints(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	for i := len(stored) - 1; i >= 0; i-- {
		if stored[i].Entries == size {
			return stored[i], nil
		}
	}
	final, err := b.Checkpoint(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	if final.Entries != size {
		return Checkpoint{}, fmt.Errorf("%w: book %s's final checkpoint covers %d of %d records", ErrCorrupt, b.id, final.Entries, size)
	}
	return final, nil
}

// Closed reports whether the book is closed into a windowed store: its last
// record is its book_close.
func (b *Book) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.log)
	return n > 0 && b.log[n-1].Header.RecordType == RecordTypeBookClose
}
