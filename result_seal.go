// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
)

// SealResult seals statement, an Evidence Result v0 document the caller has
// already checked, as one RecordTypeEvidenceResult record about subject
// (the one contract the Result reports), with links, and makes sure a
// checkpoint covers it so a bundle can be built on it at once.
//
// reuse reports a record an earlier run sealed for the same Result; when it
// finds one, nothing is appended. add appends the entry: Book.Append for a
// book that seals its own records, or AppendCapsule for a caller that seals
// Capsules itself. A checkpoint is cut whenever none covers the record yet:
// on the first seal, and on a repeat whose earlier run sealed the record but
// failed before its checkpoint. checkpoint is that checkpoint's size, or 0
// when none was cut.
//
// A failure after add committed the record (an index or automatic
// checkpoint failure) returns the record with the error, so the caller never
// loses its id. Errors from reuse and add are returned as they are.
func (b *Book) SealResult(ctx context.Context, statement json.RawMessage, subject string, links []Link, reuse func() (Record, bool, error), add func(Entry) (Record, error)) (sealed Record, already bool, checkpoint uint64, err error) {
	if sealed, already, err = reuse(); err != nil {
		return Record{}, false, 0, err
	}
	if !already {
		if sealed, err = add(Entry{
			RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric,
			SubjectRef: subject, Links: links, Statement: statement,
		}); err != nil {
			if sealed.RecordID != "" {
				return sealed, false, 0, err
			}
			return Record{}, false, 0, err
		}
	}
	covered, err := b.CheckpointCovers(ctx, sealed.Seq)
	if err != nil {
		return sealed, already, 0, err
	}
	if !covered {
		cp, err := b.Checkpoint(ctx)
		if err != nil {
			return sealed, already, 0, err
		}
		checkpoint = cp.Entries
	}
	return sealed, already, checkpoint, nil
}

// CheckpointCovers reports whether the book's latest checkpoint covers seq.
func (b *Book) CheckpointCovers(ctx context.Context, seq uint64) (bool, error) {
	checkpoints, err := b.Checkpoints(ctx)
	if err != nil || len(checkpoints) == 0 {
		return false, err
	}
	return checkpoints[len(checkpoints)-1].Covers(seq), nil
}
