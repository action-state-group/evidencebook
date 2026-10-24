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

	cited := mustAppend(t, book, observation("contract-1", "daily report"))
	links := []Link{{Type: Cites, Target: cited.RecordID}}
	sealed, already, cut, err := book.SealResult(ctx, statement, "contract-1", links, reuse, add)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed.Header.Links) != 1 || sealed.Header.Links[0] != links[0] {
		t.Fatalf("sealed links %+v; want %+v", sealed.Header.Links, links)
	}
	if got, err := book.Get(ctx, sealed.RecordID); err != nil || string(got.Header.Statement) != string(statement) {
		t.Fatalf("sealed statement %s, %v; want %s", got.Header.Statement, err, statement)
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

// checkpointsFail is a store whose checkpoint history cannot be read.
type checkpointsFail struct{ Store }

func (checkpointsFail) Checkpoints(context.Context) ([]Checkpoint, error) {
	return nil, errors.New("checkpoint history unreadable")
}

// A checkpoint that cannot be read (here) or cut (below) returns the sealed
// record and already as they were, with no checkpoint and the error.
func TestSealResultKeepsTheRecordWhenTheCheckpointLookupFails(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "results", 10)
	config := f.config(t)
	config.Store = checkpointsFail{config.Store}
	book, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	for _, reused := range []bool{false, true} {
		var committed Record
		add := func(e Entry) (Record, error) {
			committed, err = book.Append(ctx, e)
			return committed, err
		}
		reuse := func() (Record, bool, error) { return committed, reused && committed.RecordID != "", nil }
		if reused {
			committed = mustAppend(t, book, Entry{RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "c", Statement: json.RawMessage(`{}`)})
		}
		sealed, already, cut, err := book.SealResult(ctx, json.RawMessage(`{}`), "c", nil, reuse, add)
		if err == nil || !strings.Contains(err.Error(), "unreadable") {
			t.Fatalf("reused %v: %v; want the lookup's error", reused, err)
		}
		if sealed.RecordID == "" || sealed.RecordID != committed.RecordID || already != reused || cut != 0 {
			t.Fatalf("reused %v: id %q already %v checkpoint %d", reused, sealed.RecordID, already, cut)
		}
	}
}

func TestSealResultKeepsTheRecordWhenTheCheckpointCutFails(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "results", 9).open(t)
	committed := mustAppend(t, book, Entry{RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "c", Statement: json.RawMessage(`{}`)})
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	for _, reused := range []bool{false, true} {
		reuse := func() (Record, bool, error) { return committed, reused, nil }
		add := func(Entry) (Record, error) { return committed, nil }
		sealed, already, cut, err := book.SealResult(ctx, json.RawMessage(`{}`), "c", nil, reuse, add)
		if err == nil {
			t.Fatalf("reused %v: a released book's checkpoint failed silently", reused)
		}
		if sealed.RecordID != committed.RecordID || already != reused || cut != 0 {
			t.Fatalf("reused %v: id %q already %v checkpoint %d", reused, sealed.RecordID, already, cut)
		}
	}
}
