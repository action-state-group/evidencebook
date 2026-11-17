// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/shelf/facts/vectors.json: the entry documents of a shelf log of
// book facts (docs/design/461-shelf-facts.md), byte for byte, which the Rust
// crate and the Python package check. EB_WRITE_SHELF_VECTORS=1 rewrites the
// file from the inputs it holds; otherwise every output must match.

type factVectorInput struct {
	Kind    string         `json:"kind"`
	Fact    *FactInputJSON `json:"fact,omitempty"`
	Journal *journalInput  `json:"journal,omitempty"`
	Stamp   *StampEntry    `json:"stamp,omitempty"`
	Marker  *markerInput   `json:"marker,omitempty"`
	Salts   []string       `json:"salts,omitempty"`
}

// FactInputJSON is FactInput as the vectors write it.
type FactInputJSON struct {
	Profile          string          `json:"profile"`
	BookID           string          `json:"book_id"`
	LogID            string          `json:"log_id"`
	Size             uint64          `json:"size"`
	CheckpointSHA256 string          `json:"checkpoint_sha256"`
	PrevFactSize     uint64          `json:"prev_fact_size"`
	Link             LinkValue       `json:"link"`
	Status           json.RawMessage `json:"status,omitempty"`
}

type journalInput struct {
	LogID  string        `json:"log_id"`
	Status JournalStatus `json:"status"`
}

type markerInput struct {
	LastTick uint64       `json:"last_tick"`
	Logs     []LoggedSize `json:"logs"`
}

type factVector struct {
	Name          string          `json:"name"`
	Input         factVectorInput `json:"input"`
	Doc           json.RawMessage `json:"doc"`
	EntryID       string          `json:"entry_id"`
	LinkOpening   *FactOpening    `json:"link_opening,omitempty"`
	StatusOpening *FactOpening    `json:"status_opening,omitempty"`
}

type audienceVector struct {
	Kind     string   `json:"kind"`
	Audience Audience `json:"audience"`
	Link     bool     `json:"link"`
	Status   bool     `json:"status"`
}

type factVectors struct {
	About        string           `json:"about"`
	Entries      []factVector     `json:"entries"`
	Audiences    []audienceVector `json:"audiences"`
	InvalidFacts []struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"invalid_facts"`
	BadOpenings []struct {
		Name       string      `json:"name"`
		Commitment string      `json:"commitment"`
		Opening    FactOpening `json:"opening"`
	} `json:"bad_openings"`
}

func saltSource(t *testing.T, salts []string) randomSource {
	t.Helper()
	var b []byte
	for _, s := range salts {
		x, err := hex.DecodeString(s)
		require.NoError(t, err)
		b = append(b, x...)
	}
	return newRandomSource(bytes.NewReader(b))
}

func buildEntry(t *testing.T, in factVectorInput) factVector {
	t.Helper()
	var doc any
	out := factVector{Input: in}
	switch in.Kind {
	case FactKindBook:
		f := in.Fact
		fact, lo, so, err := newBookFact(FactInput{Profile: f.Profile, BookID: f.BookID, LogID: f.LogID, Size: f.Size,
			CheckpointSHA256: f.CheckpointSHA256, PrevFactSize: f.PrevFactSize, Link: f.Link, Status: f.Status}, saltSource(t, in.Salts))
		require.NoError(t, err)
		doc, out.LinkOpening, out.StatusOpening = fact, &lo, &so
		require.NoError(t, lo.Verify(fact.LinkCommitment))
		require.NoError(t, so.Verify(fact.StatusCommitment))
	case FactKindJournal:
		fact, lo, so, err := newJournalFact(in.Journal.LogID, in.Journal.Status, saltSource(t, in.Salts))
		require.NoError(t, err)
		doc, out.LinkOpening, out.StatusOpening = fact, &lo, &so
	case EntryKindStamp:
		doc = *in.Stamp
	case EntryKindFormatSwitch:
		doc = newFormatSwitch(in.Marker.LastTick, in.Marker.Logs)
	default:
		t.Fatalf("unknown kind %q", in.Kind)
	}
	id, jcs, err := EntryID(doc)
	require.NoError(t, err)
	out.Doc, out.EntryID = jcs, hex.EncodeToString(id[:])
	return out
}

func TestShelfFactVectors(t *testing.T) {
	path := "testdata/shelf/facts/vectors.json"
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var v factVectors
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.Entries)
	write := os.Getenv("EB_WRITE_SHELF_VECTORS") == "1"
	for i, want := range v.Entries {
		got := buildEntry(t, want.Input)
		got.Name = want.Name
		if write {
			v.Entries[i] = got
			continue
		}
		assert.JSONEq(t, string(want.Doc), string(got.Doc), want.Name)
		assert.Equal(t, want.EntryID, got.EntryID, want.Name)
		sameOpening(t, want.LinkOpening, got.LinkOpening, want.Name+" link")
		sameOpening(t, want.StatusOpening, got.StatusOpening, want.Name+" status")
	}
	for i, a := range v.Audiences {
		link, status, err := OpeningsFor(a.Kind, a.Audience)
		require.NoError(t, err)
		if write {
			v.Audiences[i].Link, v.Audiences[i].Status = link, status
			continue
		}
		assert.Equal(t, a.Link, link, "%s/%s link", a.Kind, a.Audience)
		assert.Equal(t, a.Status, status, "%s/%s status", a.Kind, a.Audience)
	}
	for _, c := range v.InvalidFacts {
		assert.Error(t, buildInvalid(t, c.Input), c.Name)
	}
	for _, b := range v.BadOpenings {
		assert.ErrorIs(t, b.Opening.Verify(b.Commitment), ErrInvalid, b.Name)
	}
	if write {
		out, err := json.MarshalIndent(v, "", " ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(out, '\n'), 0o644))
	}
}

func TestFactErrors(t *testing.T) {
	r := saltSource(t, []string{"00", "00"})
	_, _, _, err := newBookFact(FactInput{Profile: "deal", BookID: "b", LogID: "l", CheckpointSHA256: "c", Size: 1, PrevFactSize: 2}, r)
	assert.ErrorIs(t, err, ErrInvalid, "size below the previous fact")
	_, _, _, err = newBookFact(FactInput{Profile: "deal", BookID: "b", LogID: "l", CheckpointSHA256: "c", Size: 2, Status: json.RawMessage(`[1]`)}, r)
	assert.ErrorIs(t, err, ErrInvalid, "a status that is not an object")
	_, _, _, err = newBookFact(FactInput{Profile: "deal", BookID: "b", LogID: "l", CheckpointSHA256: "c", Size: 2, Link: LinkValue{CheckedOver: []string{"a", "a"}}}, r)
	assert.ErrorIs(t, err, ErrInvalid, "a digest checked over twice")
	_, _, _, err = newBookFact(FactInput{Profile: "deal", BookID: "b", LogID: "l", CheckpointSHA256: "c", Size: 2}, newRandomSource(bytes.NewReader(nil)))
	assert.Error(t, err, "a random source that runs dry")
	_, _, err = OpeningsFor("stamp", AudienceOwner)
	assert.ErrorIs(t, err, ErrInvalid)
	_, _, err = OpeningsFor(FactKindBook, "stranger")
	assert.ErrorIs(t, err, ErrInvalid)
}

func sameOpening(t *testing.T, want, got *FactOpening, name string) {
	t.Helper()
	if want == nil || got == nil {
		assert.Equal(t, want == nil, got == nil, name)
		return
	}
	assert.Equal(t, want.Salt, got.Salt, name)
	assert.JSONEq(t, string(want.Value), string(got.Value), name)
}

// buildInvalid reads an input strictly (no unknown members, the members'
// own types) and builds it, returning the first refusal.
func buildInvalid(t *testing.T, raw json.RawMessage) error {
	t.Helper()
	var in factVectorInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return err
	}
	f := in.Fact
	_, _, _, err := newBookFact(FactInput{Profile: f.Profile, BookID: f.BookID, LogID: f.LogID, Size: f.Size,
		CheckpointSHA256: f.CheckpointSHA256, PrevFactSize: f.PrevFactSize, Link: f.Link, Status: f.Status}, saltSource(t, in.Salts))
	return err
}

func TestFactOpeningRawText(t *testing.T) {
	commitment, opening, err := commit(make([]byte, 32), map[string]any{"x": "\ufffd"})
	require.NoError(t, err)
	require.NoError(t, opening.Verify(commitment))
	forged := FactOpening{Salt: opening.Salt, Value: json.RawMessage(`{"x":"\ud800"}`)}
	assert.ErrorIs(t, forged.Verify(commitment), ErrInvalid, "a lone surrogate Go would read as U+FFFD")
	bad := FactOpening{Salt: opening.Salt, Value: json.RawMessage("{\"x\":\"\xff\"}")}
	assert.ErrorIs(t, bad.Verify(commitment), ErrInvalid, "invalid UTF-8")
}
