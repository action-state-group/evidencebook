// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSealResultSealsOnceAndCovers(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "results", 7).open(t)
	statement := json.RawMessage(`{"result":"v0"}`)
	var first Record
	reuse := func() (Record, bool, error) { return first, first.RecordID != "", nil }
	add := func(e Entry) (Record, error) { return book.Append(ctx, e) }

	sealed, already, cut, err := book.SealResult(ctx, statement, "contract-1", nil, reuse, add)
	if err != nil {
		t.Fatal(err)
	}
	if already || cut == 0 {
		t.Fatalf("first seal: already %v, checkpoint %d; want a new record and a checkpoint", already, cut)
	}
	if sealed.Header.RecordType != RecordTypeEvidenceResult || sealed.Header.EpistemicType != DerivedMetric || sealed.Header.SubjectRef != "contract-1" {
		t.Fatalf("sealed header %+v", sealed.Header)
	}
	if covered, err := book.CheckpointCovers(ctx, sealed.Seq); err != nil || !covered {
		t.Fatalf("covered %v, %v; want the record covered", covered, err)
	}

	first = sealed
	again, already, cut, err := book.SealResult(ctx, statement, "contract-1", nil, reuse, func(Entry) (Record, error) {
		t.Fatal("a repeat appends nothing")
		return Record{}, nil
	})
	if err != nil || !already || cut != 0 || again.RecordID != sealed.RecordID {
		t.Fatalf("repeat: %v already %v checkpoint %d id %s", err, already, cut, again.RecordID)
	}
}

// A repeat whose earlier run sealed the record but stopped before its
// checkpoint cuts one now.
func TestSealResultCoversAnEarlierUncoveredRecord(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "results", 8).open(t)
	earlier := mustAppend(t, book, Entry{RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "c", Statement: json.RawMessage(`{}`)})
	if covered, _ := book.CheckpointCovers(ctx, earlier.Seq); covered {
		t.Fatal("no checkpoint was cut yet")
	}
	_, already, cut, err := book.SealResult(ctx, json.RawMessage(`{}`), "c", nil,
		func() (Record, bool, error) { return earlier, true, nil },
		func(Entry) (Record, error) { t.Fatal("nothing to append"); return Record{}, nil })
	if err != nil || !already || cut == 0 {
		t.Fatalf("%v already %v checkpoint %d; want the earlier record covered now", err, already, cut)
	}
}

// A record add committed before failing is returned with the error; with
// nothing committed, nothing is reported. reuse's error stops it first.
func TestSealResultErrors(t *testing.T) {
	ctx := context.Background()
	var book *Book // never reached: every path below returns before it
	none := func() (Record, bool, error) { return Record{}, false, nil }
	boom := errors.New("boom")

	committed := Record{RecordID: strings.Repeat("a", 64), Seq: 3}
	sealed, already, _, err := book.SealResult(ctx, json.RawMessage(`{}`), "x", nil, none, func(Entry) (Record, error) { return committed, boom })
	if !errors.Is(err, boom) || already || sealed.RecordID != committed.RecordID {
		t.Fatalf("committed: %v already %v id %q", err, already, sealed.RecordID)
	}
	sealed, _, _, err = book.SealResult(ctx, json.RawMessage(`{}`), "x", nil, none, func(Entry) (Record, error) { return Record{}, boom })
	if !errors.Is(err, boom) || sealed.RecordID != "" {
		t.Fatalf("not committed: %v id %q", err, sealed.RecordID)
	}
	_, _, _, err = book.SealResult(ctx, json.RawMessage(`{}`), "x", nil, func() (Record, bool, error) { return Record{}, false, boom }, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("reuse error: %v", err)
	}
}
