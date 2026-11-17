// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/internal/detrand"
	"github.com/stretchr/testify/require"
)

// testdata/shelf/receive: the received store's vectors
// (docs/design/461-shelf-facts.md §3, §7). script.json holds a vector
// profile, the store's bounds, the input records and stamp proofs, and a
// script of steps with each step's expected outcome; after.json is the
// store's dump after the script. EB_WRITE_SHELF_VECTORS=1 rebuilds the
// inputs from Go-made shelves and rewrites both files; otherwise the script
// runs from the file's inputs and both must match byte for byte.

// vectorRecord is the vector profile's record: a test profile, not a
// format. Its bytes are the record's canonical JSON (RFC 8785), and sig is
// hex(SHA-256(UTF8(secret) ‖ JCS(the record without sig))) under the
// sender's secret in the script.
type vectorRecord struct {
	V        int    `json:"v"`
	Sender   string `json:"sender"`
	Log      string `json:"log"`
	Position uint64 `json:"position"`
	Prev     string `json:"prev"`
	Digest   string `json:"digest"`
	Deal     string `json:"deal"`
	Offer    bool   `json:"offer"`
	Sig      string `json:"sig,omitempty"`
}

// vectorProfileSpec is the vector profile's fixed tables.
type vectorProfileSpec struct {
	// Secrets sign each sender's records.
	Secrets map[string]string `json:"secrets"`
	// Admitted are the senders Admit lets push a record that is not an
	// offer; an offer is admitted from any sender.
	Admitted []string `json:"admitted"`
	// Answered are the offer digests Answered reports answered.
	AnsweredOffers []string `json:"answered"`
	// ProofKeys maps a stamp proof's checkpoint key id to its sender.
	ProofKeys map[string]string `json:"proof_keys"`
}

func vectorSig(secret string, r vectorRecord) (string, error) {
	r.Sig = ""
	body, err := canonicalJSON(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(secret), body...))
	return hex.EncodeToString(sum[:]), nil
}

func (p vectorProfileSpec) sign(r vectorRecord) (string, error) {
	r.V = 1
	sig, err := vectorSig(p.Secrets[r.Sender], r)
	if err != nil {
		return "", err
	}
	r.Sig = sig
	b, err := canonicalJSON(r)
	return string(b), err
}

func (p vectorProfileSpec) Read(_ context.Context, record []byte) (ReceivedRecord, error) {
	var r vectorRecord
	decoder := json.NewDecoder(bytes.NewReader(record))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return ReceivedRecord{}, err
	}
	if canonical, err := canonicalJSON(r); err != nil || !bytes.Equal(canonical, record) || r.V != 1 {
		return ReceivedRecord{}, errors.New("not a canonical vector record of version 1")
	}
	secret, ok := p.Secrets[r.Sender]
	if !ok {
		return ReceivedRecord{}, errors.New("an unknown sender")
	}
	if sig, err := vectorSig(secret, r); err != nil || sig != r.Sig {
		return ReceivedRecord{}, errors.New("a bad signature")
	}
	return ReceivedRecord{Digest: r.Digest, SenderKey: r.Sender, LogID: r.Log, Position: r.Position, Prev: r.Prev, DealID: r.Deal, Offer: r.Offer}, nil
}

func (p vectorProfileSpec) Admit(_ context.Context, r ReceivedRecord) (bool, error) {
	if r.Offer {
		return true, nil
	}
	for _, s := range p.Admitted {
		if s == r.SenderKey {
			return true, nil
		}
	}
	return false, nil
}

func (p vectorProfileSpec) Answered(_ context.Context, r ReceivedRecord) (bool, error) {
	for _, d := range p.AnsweredOffers {
		if d == r.Digest {
			return true, nil
		}
	}
	return false, nil
}

func (p vectorProfileSpec) ProofSender(_ context.Context, keyID, _ string) (string, bool, error) {
	s, ok := p.ProofKeys[keyID]
	return s, ok, nil
}

type vectorBounds struct {
	MaxRecordBytes            int64 `json:"max_record_bytes"`
	MaxRecordsPerLog          int64 `json:"max_records_per_log"`
	MaxOffersPerSender        int64 `json:"max_offers_per_sender"`
	MaxOfferBytesPerSender    int64 `json:"max_offer_bytes_per_sender"`
	MaxOffers                 int64 `json:"max_offers"`
	MaxOfferBytes             int64 `json:"max_offer_bytes"`
	MaxOfferEvidencePerSender int64 `json:"max_offer_evidence_per_sender"`
	MaxOfferEvidence          int64 `json:"max_offer_evidence"`
}

func (b vectorBounds) bounds() ReceiveBounds {
	return ReceiveBounds{MaxRecordBytes: b.MaxRecordBytes, MaxRecordsPerLog: b.MaxRecordsPerLog,
		MaxOffersPerSender: b.MaxOffersPerSender, MaxOfferBytesPerSender: b.MaxOfferBytesPerSender,
		MaxOffers: b.MaxOffers, MaxOfferBytes: b.MaxOfferBytes,
		MaxOfferEvidencePerSender: b.MaxOfferEvidencePerSender, MaxOfferEvidence: b.MaxOfferEvidence}
}

type vectorRecordOutcome struct {
	Digest     string `json:"digest"`
	Stored     bool   `json:"stored"`
	Duplicate  bool   `json:"duplicate"`
	Deleted    bool   `json:"deleted"`
	Counted    bool   `json:"counted"`
	AskForBook bool   `json:"ask_for_book"`
	Refused    string `json:"refused"`
}

type vectorProofOutcome struct {
	Accepted  bool   `json:"accepted"`
	Duplicate bool   `json:"duplicate"`
	Refused   string `json:"refused"`
}

type vectorChanges struct {
	Grew    []string `json:"grew"`
	Shrank  []string `json:"shrank"`
	Forked  []string `json:"forked"`
	Covered []string `json:"covered"`
}

type vectorExpect struct {
	// Error is the class of the call's error ("" for none).
	Error   string               `json:"error"`
	Record  *vectorRecordOutcome `json:"record,omitempty"`
	Proof   *vectorProofOutcome  `json:"proof,omitempty"`
	Changes *vectorChanges       `json:"changes,omitempty"`
	// Deleted is what a forget step deleted.
	Deleted []string `json:"deleted,omitempty"`
}

type vectorStep struct {
	// At is the store's clock during the step (and a forget's now).
	At      string        `json:"at"`
	Op      string        `json:"op"`
	Record  string        `json:"record,omitempty"`
	Proof   string        `json:"proof,omitempty"`
	Deal    string        `json:"deal,omitempty"`
	Settled *bool         `json:"settled,omitempty"`
	Expect  *vectorExpect `json:"expect,omitempty"`
}

type receiveVectors struct {
	About            string                `json:"about"`
	Profile          vectorProfileSpec     `json:"profile"`
	Bounds           vectorBounds          `json:"bounds"`
	RetentionSeconds int64                 `json:"retention_seconds"`
	Records          map[string]string     `json:"records"`
	Proofs           map[string]StampProof `json:"proofs"`
	Steps            []vectorStep          `json:"steps"`
}

// errorClass names a store error by its sentinel.
func errorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrRefused):
		return "refused"
	case errors.Is(err, ErrOverLimit):
		return "over_limit"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrDeleted):
		return "deleted"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	}
	return "other: " + err.Error()
}

// buildReceiveInputs makes the vectors' inputs: a holder's shelf whose book
// deal/a gives alice's records and two stamp proofs of one book checkpoint
// (the second after a received-only change), and a second shelf under the
// same checkpoint key whose book at the same size gives a contradicting
// proof. Both draw from fixed detrand streams under the fixture's clock.
func buildReceiveInputs(t *testing.T) receiveVectors {
	t.Helper()
	ctx := context.Background()
	holder := func(seed string, payloads ...string) (*Shelf, *Cadence, *statusValues) {
		f := newShelfFixture(t)
		f.cfg.Random = detrand.New(seed)
		s := f.open(t)
		c, values := newStampCadence(t, s)
		values.set("deal/a", `{"row":"open"}`)
		book := mustOpenBook(t, s, "deal/a")
		for _, p := range payloads {
			mustAppend(t, book, observation("a", p))
		}
		mustTick(t, c, tickTime)
		require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
		return s, c, values
	}
	proofOf := func(s *Shelf, c *Cadence) StampProof {
		var bookID string
		require.NoError(t, s.db.QueryRow(`SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' AND kind = ? LIMIT 1`, FactKindBook).Scan(&bookID))
		p, err := c.StampProof(ctx, bookID, 0, AudienceCounterparty)
		require.NoError(t, err)
		return p
	}
	h, hc, hv := holder("evidencebook shelf vectors/receive holder", "1", "2", "3")
	p1 := proofOf(h, hc)
	hv.set("deal/a", `{"row":"agreed"}`)
	_, err := hc.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	p2 := proofOf(h, hc)
	o, oc, _ := holder("evidencebook shelf vectors/receive other", "x", "y", "z")
	pc := proofOf(o, oc)
	key, err := checkpointFromStatement(p1.BookCheckpoint)
	require.NoError(t, err)

	v := receiveVectors{
		About: "The received store's vectors (evidencebook#461, docs/design/461-shelf-facts.md §3 and §7), written by shelf_receive_vectors_test.go (EB_WRITE_SHELF_VECTORS=1). The profile is a test profile, not a record format; see README.md.",
		Profile: vectorProfileSpec{
			Secrets:   map[string]string{"alice": "alice secret", "bob": "bob secret", "mallory": "mallory secret"},
			Admitted:  []string{"alice"},
			ProofKeys: map[string]string{key.KeyID: "alice"},
		},
		Bounds:           vectorBounds{},
		RetentionSeconds: 3600,
		Records:          map[string]string{},
		Proofs:           map[string]StampProof{"p1": p1, "p2": p2, "pc": pc},
	}
	b := ReceiveBounds{MaxOffersPerSender: 2}.withDefaults()
	v.Bounds = vectorBounds{b.MaxRecordBytes, b.MaxRecordsPerLog, b.MaxOffersPerSender, b.MaxOfferBytesPerSender, b.MaxOffers, b.MaxOfferBytes, b.MaxOfferEvidencePerSender, b.MaxOfferEvidence}
	add := func(name string, r vectorRecord) {
		text, err := v.Profile.sign(r)
		require.NoError(t, err)
		v.Records[name] = text
	}
	rows, err := h.db.Query(`SELECT seq, value FROM cll_entries WHERE log_id = 'deal/a' ORDER BY seq`)
	require.NoError(t, err)
	var entries []string
	for rows.Next() {
		var seq uint64
		var value []byte
		require.NoError(t, rows.Scan(&seq, &value))
		entries = append(entries, hex.EncodeToString(value))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.GreaterOrEqual(t, len(entries), 3)
	for i, e := range entries {
		prev := ""
		if i > 0 {
			prev = entries[i-1]
		}
		add(fmt.Sprintf("a%d", i+1), vectorRecord{Sender: "alice", Log: "deal/a", Position: uint64(i + 1), Prev: prev, Digest: e, Deal: "deal-1"})
	}
	add("a2-fork", vectorRecord{Sender: "alice", Log: "deal/a", Position: 2, Prev: entries[0], Digest: dg("a2-fork"), Deal: "deal-1"})
	add("b1", vectorRecord{Sender: "alice", Log: "deal/b", Position: 1, Digest: dg("b1"), Deal: "deal-2"})
	add("m1", vectorRecord{Sender: "mallory", Log: "deal/m", Position: 1, Digest: dg("m1"), Deal: "deal-m"})
	for _, o := range []string{"o1", "o2", "o3", "o4"} {
		add(o, vectorRecord{Sender: "bob", Log: "offers/" + o, Position: 1, Digest: dg(o), Deal: "offer-" + o, Offer: true})
	}
	v.Profile.AnsweredOffers = []string{dg("o1")}

	base := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	minute := 0
	at := func() string {
		minute++
		return base.Add(time.Duration(minute) * time.Minute).Format(time.RFC3339)
	}
	receive := func(record, proof string) {
		v.Steps = append(v.Steps, vectorStep{At: at(), Op: "receive", Record: record, Proof: proof})
	}
	no, yes := false, true
	receive("a1", "")
	receive("a1", "") // a duplicate
	for i := 3; i <= len(entries); i++ {
		receive(fmt.Sprintf("a%d", i), "") // past a gap at 2
	}
	receive("", "p1")                               // a proof past the records held
	receive("a2", "")                               // the fill: the prefix reaches p1, which recomputes
	receive(fmt.Sprintf("a%d", len(entries)), "p2") // a duplicate carrying a proof it lacked
	receive("", "p1")                               // the same proof again
	receive("", "pc")                               // a contradicting proof
	receive("a2-fork", "")                          // a late fork
	receive("m1", "")                               // a sender not admitted
	receive("o1", "")
	receive("o2", "")
	receive("o3", "") // o1 is answered: it leaves the bounds
	receive("o4", "") // o2 is dropped
	receive("b1", "")
	v.Steps = append(v.Steps,
		vectorStep{At: at(), Op: "forget", Deal: "deal-2", Settled: &no},
		vectorStep{At: base.Add(3 * time.Hour).Format(time.RFC3339), Op: "forget", Deal: "deal-2", Settled: &yes})
	minute += 180
	receive("b1", "") // read as deleted
	v.Steps = append(v.Steps, vectorStep{At: at(), Op: "reopen"})
	receive("a1", "") // the prefix recomputed from the store
	return v
}

func changesOf(c ReceiveChanges) *vectorChanges {
	or := func(l []string) []string {
		if l == nil {
			return []string{}
		}
		return l
	}
	return &vectorChanges{Grew: or(c.Grew), Shrank: or(c.Shrank), Forked: or(c.Forked), Covered: or(c.Covered)}
}

// runReceiveVectors runs the script on a fresh shelf and fills each step's
// expect; it returns the store's dump.
func runReceiveVectors(t *testing.T, v *receiveVectors) []byte {
	t.Helper()
	ctx := context.Background()
	f := newShelfFixture(t)
	var now time.Time
	f.cfg.Now = func() time.Time { return now }
	f.cfg.Receive = ReceiveConfig{Profile: v.Profile, Bounds: v.Bounds.bounds(), Retention: time.Duration(v.RetentionSeconds) * time.Second}
	s := f.open(t)
	for i := range v.Steps {
		step := &v.Steps[i]
		var err error
		now, err = time.Parse(time.RFC3339, step.At)
		require.NoError(t, err)
		expect := &vectorExpect{}
		switch step.Op {
		case "receive":
			var record []byte
			if step.Record != "" {
				text, ok := v.Records[step.Record]
				require.True(t, ok, step.Record)
				record = []byte(text)
			}
			var proof *StampProof
			if step.Proof != "" {
				p, ok := v.Proofs[step.Proof]
				require.True(t, ok, step.Proof)
				proof = &p
			}
			got, err := s.Receive(ctx, record, proof)
			expect.Error = errorClass(err)
			if got.Record != nil {
				expect.Record = &vectorRecordOutcome{Digest: got.Record.Digest, Stored: got.Record.Stored, Duplicate: got.Record.Duplicate,
					Deleted: got.Record.Deleted, Counted: got.Record.Counted, AskForBook: got.Record.AskForBook, Refused: errorClass(got.Record.Refused)}
			}
			if got.Proof != nil {
				expect.Proof = &vectorProofOutcome{Accepted: got.Proof.Accepted, Duplicate: got.Proof.Duplicate, Refused: errorClass(got.Proof.Refused)}
			}
			expect.Changes = changesOf(got.Changes)
		case "forget":
			settled := step.Settled != nil && *step.Settled
			deleted, err := s.Forget(ctx, step.Deal, now, func(context.Context, string) (bool, error) { return settled, nil })
			expect.Error = errorClass(err)
			expect.Deleted = deleted
			if expect.Deleted == nil {
				expect.Deleted = []string{}
			}
		case "reopen":
			require.NoError(t, s.Release())
			s = f.open(t)
		default:
			t.Fatalf("step %d: unknown op %q", i, step.Op)
		}
		step.Expect = expect
	}
	return dumpReceived(t, s)
}

type receivedDump struct {
	Received   []map[string]any `json:"shelf_received"`
	Proofs     []map[string]any `json:"shelf_received_proofs"`
	Tombstones []map[string]any `json:"shelf_received_tombstones"`
	Evidence   []map[string]any `json:"shelf_evidence"`
	Conflicts  []map[string]any `json:"shelf_received_conflicts"`
	Vacated    []map[string]any `json:"shelf_received_vacated"`
	Prefixes   []map[string]any `json:"counted_prefixes"`
}

// dumpReceived is the store's tables in the README's order: TEXT and
// INTEGER columns as they are, every BLOB as lowercase hex (null as null),
// then each sender's counted prefix in every log the tables name.
func dumpReceived(t *testing.T, s *Shelf) []byte {
	t.Helper()
	ctx := context.Background()
	table := func(query string, blobs ...string) []map[string]any {
		rows, err := s.db.QueryContext(ctx, query)
		require.NoError(t, err)
		defer rows.Close()
		columns, err := rows.Columns()
		require.NoError(t, err)
		isBlob := map[string]bool{}
		for _, b := range blobs {
			isBlob[b] = true
		}
		out := []map[string]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			row := map[string]any{}
			for i, c := range columns {
				switch v := values[i].(type) {
				case nil:
					row[c] = nil
				case []byte:
					if isBlob[c] {
						row[c] = hex.EncodeToString(v)
					} else {
						row[c] = string(v)
					}
				case string:
					if isBlob[c] {
						row[c] = hex.EncodeToString([]byte(v))
					} else {
						row[c] = v
					}
				default:
					row[c] = v
				}
			}
			out = append(out, row)
		}
		require.NoError(t, rows.Err())
		return out
	}
	d := receivedDump{
		Received:   table(`SELECT digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at FROM shelf_received ORDER BY digest`, "record"),
		Proofs:     table(`SELECT sender_key, log_id, size, shelf_checkpoint_sha256, book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at, verified FROM shelf_received_proofs ORDER BY sender_key, log_id, size, shelf_checkpoint_sha256`, "book_checkpoint", "fact", "status_opening", "inclusion_proof", "shelf_checkpoint"),
		Tombstones: table(`SELECT digest, sender_key, received_at, deleted_at FROM shelf_received_tombstones ORDER BY digest`),
		Evidence:   table(`SELECT id, kind, a, b, noted_at FROM shelf_evidence ORDER BY id`, "a", "b"),
		Conflicts:  table(`SELECT sender_key, log_id, kind, at, evidence_id, offer FROM shelf_received_conflicts ORDER BY sender_key, log_id, kind, at`),
		Vacated:    table(`SELECT sender_key, log_id, position, digest, deal_id, offer FROM shelf_received_vacated ORDER BY sender_key, log_id, position, digest`),
		Prefixes:   []map[string]any{},
	}
	keys := map[[2]string]bool{}
	for _, rows := range [][]map[string]any{d.Received, d.Conflicts, d.Vacated} {
		for _, r := range rows {
			keys[[2]string{r["sender_key"].(string), r["log_id"].(string)}] = true
		}
	}
	sorted := make([][2]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i][0] < sorted[j][0] || (sorted[i][0] == sorted[j][0] && sorted[i][1] < sorted[j][1])
	})
	for _, k := range sorted {
		p, err := s.CountedPrefix(ctx, k[0], k[1])
		require.NoError(t, err)
		d.Prefixes = append(d.Prefixes, map[string]any{"sender_key": k[0], "log_id": k[1], "size": p.Size, "last": p.Last, "fork_at": p.ForkAt})
	}
	out, err := json.MarshalIndent(d, "", "  ")
	require.NoError(t, err)
	return append(out, '\n')
}

func TestShelfReceiveVectors(t *testing.T) {
	dir := filepath.Join("testdata", "shelf", "receive")
	scriptPath, dumpPath := filepath.Join(dir, "script.json"), filepath.Join(dir, "after.json")
	write := os.Getenv("EB_WRITE_SHELF_VECTORS") == "1"
	var v receiveVectors
	if write {
		v = buildReceiveInputs(t)
	} else {
		b, err := os.ReadFile(scriptPath)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &v))
	}
	dump := runReceiveVectors(t, &v)
	script, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	script = append(script, '\n')
	if write {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(scriptPath, script, 0o644))
		require.NoError(t, os.WriteFile(dumpPath, dump, 0o644))
	}
	for path, got := range map[string][]byte{scriptPath: script, dumpPath: dump} {
		want, err := os.ReadFile(path)
		require.NoError(t, err)
		if !bytes.Equal(got, want) {
			t.Fatalf("%s differs from this build's store; regenerate only for an intended change (EB_WRITE_SHELF_VECTORS=1)", path)
		}
	}
}
