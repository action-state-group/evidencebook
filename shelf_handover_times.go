// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// Record times from a handover (evidencebook#461,
// docs/design/461-deal-profile.md §8.1, invariant C): a reader handed the
// whole shelf resolves the record time of a book's position as the holder's
// RecordTime does, from the anchor and the earlier witnessed checkpoints the
// handover carries with their receipts. A checkpoint after the anchor is not
// handed over, so a time only a later checkpoint gives is not resolved. The
// journal's sizes are never handed over, so its records get no time here.

// HandoverTimes resolves record times from a verified handover.
type HandoverTimes struct {
	// facts are the book facts after the format switch, in entry order.
	facts []handoverFact
	// checkpoints are the anchor and the earlier witnessed checkpoints, each
	// with the earliest registration time of each witness whose receipt
	// verifies under the reader's directory.
	checkpoints []handoverRegistration
}

type handoverFact struct {
	seq    uint64
	bookID string
	size   uint64
}

type handoverRegistration struct {
	entries uint64
	// at and atKey are the earliest registration times by witness URL and
	// by verified key id.
	at, atKey map[string]int64
}

// VerifyHandoverTimes replays h with VerifyHandover, then reads the
// registration times its receipts give under the reader's directory: a
// receipt that does not verify, or gives no registration time, gives none.
func VerifyHandoverTimes(h Handover, pinnedKey string, witnesses aacwitness.Directory) (HandoverTimes, error) {
	if _, err := VerifyHandover(h, pinnedKey, witnesses); err != nil {
		return HandoverTimes{}, err
	}
	var out HandoverTimes
	for _, d := range h.Documents {
		if d.StatusOpening == nil {
			continue
		}
		var f BookFact
		if err := json.Unmarshal(d.Doc, &f); err != nil {
			return HandoverTimes{}, fmt.Errorf("%w: handover: the document at %d: %v", ErrInvalid, d.Seq, err)
		}
		if f.Kind == FactKindBook {
			out.facts = append(out.facts, handoverFact{seq: d.Seq, bookID: f.BookID, size: f.Size})
		}
	}
	sort.Slice(out.facts, func(i, j int) bool { return out.facts[i].seq < out.facts[j].seq })
	given := append([]HandoverCheckpoint{{Checkpoint: h.Anchor, Receipts: h.Receipts}}, h.Witnessed...)
	for _, w := range given {
		cp, err := checkpointFromStatement(w.Checkpoint)
		if err != nil {
			return HandoverTimes{}, err
		}
		reg := handoverRegistration{entries: cp.Entries, at: map[string]int64{}, atKey: map[string]int64{}}
		for _, r := range w.Receipts {
			check, verified, err := verifyWitnessReceipt(w.Checkpoint, r, witnesses)
			if err != nil || check.State != WitnessVerified || verified.registeredAt == nil {
				continue
			}
			keepEarliest(reg.at, r.TSURL, *verified.registeredAt)
			keepEarliest(reg.atKey, verified.keyID, *verified.registeredAt)
		}
		out.checkpoints = append(out.checkpoints, reg)
	}
	return out, nil
}

// RecordTime is the record time of the record at position (from 1) of the
// book bookID, from the witnesses named (their URLs), as Shelf.RecordTime
// computes it: the earliest registration time of a handed-over checkpoint
// that covers the first fact of the book whose size covers the position.
func (t HandoverTimes) RecordTime(bookID string, position uint64, witnesses []string) (time.Time, bool, error) {
	return t.recordTime(bookID, position, witnesses, false)
}

// RecordTimeByKey is RecordTime with the witnesses named by their verified
// key ids, as Shelf.RecordTimeByKey.
func (t HandoverTimes) RecordTimeByKey(bookID string, position uint64, keyIDs []string) (time.Time, bool, error) {
	return t.recordTime(bookID, position, keyIDs, true)
}

func (t HandoverTimes) recordTime(bookID string, position uint64, witnesses []string, byKey bool) (time.Time, bool, error) {
	if position == 0 {
		return time.Time{}, false, fmt.Errorf("%w: a record position counts from 1", ErrInvalid)
	}
	var seq uint64
	for _, f := range t.facts {
		if leaves, ok := mmr.LeafCount(f.size); f.bookID == bookID && ok && leaves >= position {
			seq = f.seq
			break
		}
	}
	if seq == 0 {
		return time.Time{}, false, nil
	}
	var earliest int64
	found := false
	for _, cp := range t.checkpoints {
		if cp.entries < seq {
			continue
		}
		at := cp.at
		if byKey {
			at = cp.atKey
		}
		for _, w := range witnesses {
			if at, ok := at[w]; ok && (!found || at < earliest) {
				earliest, found = at, true
			}
		}
	}
	if !found {
		return time.Time{}, false, nil
	}
	return time.Unix(earliest, 0).UTC(), true, nil
}

// keepEarliest keeps at in m under name unless m holds an earlier time.
func keepEarliest(m map[string]int64, name string, at int64) {
	if held, ok := m[name]; !ok || at < held {
		m[name] = at
	}
}
