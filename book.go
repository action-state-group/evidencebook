// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	emit "github.com/action-state-group/capsule-emit-go"
)

// DefaultDeveloper is the AAC developer value a book writes when none is set.
const DefaultDeveloper = "evidencebook"

// Config assembles a book. Store, Substrate, Payloads and Signer are required;
// every index defaults to the reference implementation.
type Config struct {
	BookID    string
	Operator  string
	Developer string

	Store     Store
	Substrate Substrate
	Payloads  PayloadResolver
	Signer    Signer

	Operational   OperationalIndex
	Authenticated []AuthenticatedIndex
	Discovery     DiscoveryIndex

	// CheckpointEvery checkpoints after every N appends; zero checkpoints
	// only when Checkpoint is called or an answer requires it.
	CheckpointEvery uint64
	Now             func() time.Time
}

// Book is an evidence store: records, typed links, payload separation, three
// index classes and request/reconcile mechanics over an embedded commitment
// substrate. The substrate is never exposed; callers get records, bundles and
// answers, and the book composes every proof itself.
type Book struct {
	mu sync.Mutex

	id, operator, developer string
	checkpointEvery         uint64
	now                     func() time.Time

	store       Store
	commitments Substrate
	index       []AuthenticatedIndex
	operational OperationalIndex
	discovery   DiscoveryIndex
	payloads    PayloadResolver
	signer      Signer

	records map[string]Record
	order   []string
	roots   map[string]IndexRoot
	// pending is a record already in the store whose substrate append
	// failed. It holds the next position until the append succeeds, so a
	// transient substrate error never lets two records claim one seq.
	pending *Record
	// checkpointing suppresses the automatic checkpoint while the book is
	// committing index roots inside a checkpoint.
	checkpointing bool
}

// Open validates cfg, reconciles the record store with the substrate, and
// rebuilds every index from the committed history.
func Open(ctx context.Context, cfg Config) (*Book, error) {
	if cfg.BookID == "" || cfg.Operator == "" {
		return nil, fmt.Errorf("%w: book id and operator are required", ErrInvalid)
	}
	if cfg.Store == nil || cfg.Substrate == nil || cfg.Payloads == nil || cfg.Signer == nil {
		return nil, fmt.Errorf("%w: store, substrate, payloads and signer are required", ErrInvalid)
	}
	b := &Book{
		id: cfg.BookID, operator: cfg.Operator, developer: cfg.Developer,
		checkpointEvery: cfg.CheckpointEvery, now: cfg.Now,
		store: cfg.Store, commitments: cfg.Substrate, payloads: cfg.Payloads, signer: cfg.Signer,
		operational: cfg.Operational, index: cfg.Authenticated, discovery: cfg.Discovery,
		records: make(map[string]Record), roots: make(map[string]IndexRoot),
	}
	if b.developer == "" {
		b.developer = DefaultDeveloper
	}
	if b.now == nil {
		b.now = time.Now
	}
	if b.operational == nil {
		operational, err := OpenSQLiteIndex("")
		if err != nil {
			return nil, err
		}
		b.operational = operational
	}
	if b.index == nil {
		b.index = []AuthenticatedIndex{NewRecordIDIndex(), NewSubjectIndex()}
	}
	if b.discovery == nil {
		b.discovery = NewTokenIndex()
	}
	if err := b.recover(ctx); err != nil {
		return nil, err
	}
	if err := b.rebuildIndexes(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

// recover loads stored records, checks each against the substrate position it
// claims, and re-appends any record stored but not yet committed (a crash
// between the two writes). A committed identity with no stored record is
// corruption: the book could never produce it.
func (b *Book) recover(ctx context.Context) error {
	stored, err := b.store.Records(ctx)
	if err != nil {
		return err
	}
	for i, sr := range stored {
		if sr.Seq != uint64(i)+1 {
			return fmt.Errorf("%w: stored records are not dense at seq %d", ErrCorrupt, sr.Seq)
		}
		record, err := decodeStored(sr)
		if err != nil {
			return err
		}
		if record.Header.BookID != b.id {
			return fmt.Errorf("%w: record %s belongs to book %q", ErrCorrupt, record.RecordID, record.Header.BookID)
		}
		b.records[record.RecordID] = record
		b.order = append(b.order, record.RecordID)
	}
	committed, err := b.commitments.Size(ctx)
	if err != nil {
		return err
	}
	if committed > uint64(len(b.order)) {
		return fmt.Errorf("%w: substrate holds %d identities, store holds %d records", ErrCorrupt, committed, len(b.order))
	}
	for after := uint64(0); after < committed; {
		entries, err := b.commitments.Entries(ctx, after, 0)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("%w: substrate scan stopped at %d of %d", ErrCorrupt, after, committed)
		}
		for _, entry := range entries {
			if entry.Seq > committed || b.order[entry.Seq-1] != entry.RecordID {
				return fmt.Errorf("%w: substrate seq %d is not stored record %s", ErrCorrupt, entry.Seq, b.order[entry.Seq-1])
			}
			after = entry.Seq
		}
	}
	for _, id := range b.order[committed:] {
		if err := b.commitLocked(ctx, b.records[id]); err != nil {
			return err
		}
	}
	for _, id := range b.order {
		if h := b.records[id].Header; h.RecordType == RecordTypeIndexRoot {
			var root IndexRoot
			if err := json.Unmarshal(h.Statement, &root); err != nil {
				return fmt.Errorf("%w: index_root record %s: %v", ErrCorrupt, id, err)
			}
			b.roots[root.Index] = root
		}
	}
	return nil
}

// commitLocked appends a stored record's identity to the substrate at the
// time the record says it was committed, and requires it to land at its seq.
func (b *Book) commitLocked(ctx context.Context, record Record) error {
	at, err := time.Parse(time.RFC3339Nano, record.Header.CommittedAt)
	if err != nil {
		return fmt.Errorf("%w: committed_at of %s: %v", ErrCorrupt, record.RecordID, err)
	}
	seq, err := b.commitments.Append(ctx, record.RecordID, at)
	if err != nil {
		return err
	}
	if seq != record.Seq {
		return fmt.Errorf("%w: record %s landed at %d, not %d", ErrCorrupt, record.RecordID, seq, record.Seq)
	}
	return nil
}

// flushPendingLocked retries the substrate append of a stored record whose
// first append failed. Until it succeeds the book appends nothing new.
func (b *Book) flushPendingLocked(ctx context.Context) error {
	if b.pending == nil {
		return nil
	}
	record := *b.pending
	if err := b.commitLocked(ctx, record); err != nil {
		return fmt.Errorf("record %s is stored but not yet committed: %w", record.RecordID, err)
	}
	b.pending = nil
	return b.admitLocked(ctx, record)
}

// admitLocked makes a committed record visible: cache, order and indexes.
func (b *Book) admitLocked(ctx context.Context, record Record) error {
	b.records[record.RecordID] = record
	b.order = append(b.order, record.RecordID)
	if err := errors.Join(b.operational.Add(ctx, record), b.discovery.Add(ctx, record), b.addAuthenticated(record)); err != nil {
		return fmt.Errorf("index record %s: %w", record.RecordID, err)
	}
	return nil
}

// decodeStored re-derives a record from its durable form and checks that the
// header bytes are canonical and are what the capsule commits.
func decodeStored(sr StoredRecord) (Record, error) {
	var header Header
	if err := json.Unmarshal(sr.Header, &header); err != nil {
		return Record{}, fmt.Errorf("%w: header of %s: %v", ErrCorrupt, sr.RecordID, err)
	}
	canonical, err := canonicalJSON(header)
	if err != nil || !bytes.Equal(canonical, sr.Header) {
		return Record{}, fmt.Errorf("%w: header of %s is not canonical", ErrCorrupt, sr.RecordID)
	}
	if header.Seq != sr.Seq {
		return Record{}, fmt.Errorf("%w: header seq of %s disagrees with store", ErrCorrupt, sr.RecordID)
	}
	if err := checkCapsuleCommitsHeader(sr.RecordID, sr.Capsule, sr.Header); err != nil {
		return Record{}, err
	}
	return Record{RecordID: sr.RecordID, Seq: sr.Seq, Header: header}, nil
}

type capsuleBinding struct {
	CapsuleID        string `json:"capsule_id"`
	ModelAttestation struct {
		ComputeAttestation struct {
			AgentInputDigest string `json:"agent_input_digest"`
		} `json:"compute_attestation"`
	} `json:"model_attestation"`
}

func checkCapsuleCommitsHeader(recordID string, capsule, header []byte) error {
	result, err := emit.VerifyCapsule(capsule)
	if err != nil || !result.OK || result.CapsuleID == nil || *result.CapsuleID != recordID {
		return fmt.Errorf("%w: capsule for %s does not verify as that id", ErrCorrupt, recordID)
	}
	var binding capsuleBinding
	if err := json.Unmarshal(capsule, &binding); err != nil {
		return fmt.Errorf("%w: capsule for %s: %v", ErrCorrupt, recordID, err)
	}
	if binding.ModelAttestation.ComputeAttestation.AgentInputDigest != digestBytes(header) {
		return fmt.Errorf("%w: capsule %s does not commit its header", ErrCorrupt, recordID)
	}
	return nil
}

func (b *Book) source() RecordSource {
	return func(ctx context.Context, yield func(Record) error) error {
		for _, id := range b.order {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := yield(b.records[id]); err != nil {
				return err
			}
		}
		return nil
	}
}

func (b *Book) rebuildIndexes(ctx context.Context) error {
	if err := b.operational.Rebuild(ctx, b.source()); err != nil {
		return err
	}
	if err := b.discovery.Rebuild(ctx, b.source()); err != nil {
		return err
	}
	for _, index := range b.index {
		index.Reset()
	}
	return b.source()(ctx, b.addAuthenticated)
}

func (b *Book) addAuthenticated(record Record) error {
	if record.Header.RecordType == RecordTypeIndexRoot {
		// A root record is proven by the substrate, not by the index it
		// commits; indexing it would make every root commit the next one stale.
		return nil
	}
	for _, index := range b.index {
		if key, ok := index.KeyOf(record); ok {
			if err := index.Add(key, record.RecordID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Append validates entry, commits its payload bytes by digest, seals the
// header into a record capsule, stores the record, appends its identity to
// the substrate, and updates every index — in that order. When the record is
// committed but a later step (indexing, an automatic checkpoint) fails, the
// committed record is returned together with the error: do not retry it.
func (b *Book) Append(ctx context.Context, entry Entry) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.appendLocked(ctx, entry)
}

func (b *Book) appendLocked(ctx context.Context, entry Entry) (Record, error) {
	if err := b.flushPendingLocked(ctx); err != nil {
		return Record{}, err
	}
	seq := uint64(len(b.order)) + 1
	committedAt := b.now().UTC().Truncate(time.Microsecond)
	header := Header{
		Version: HeaderVersion, BookID: b.id, Seq: seq,
		RecordType: entry.RecordType, EpistemicType: entry.EpistemicType,
		CommittedAt: committedAt.Format(time.RFC3339Nano),
		Links:       append([]Link{}, entry.Links...),
		SubjectRef:  entry.SubjectRef, PrincipalRef: entry.PrincipalRef, CounterpartyRef: entry.CounterpartyRef,
		Statement: entry.Statement,
	}
	if !entry.EventTimeClaim.IsZero() {
		header.EventTimeClaim = entry.EventTimeClaim.UTC().Format(time.RFC3339Nano)
	}
	if !entry.Correlation.empty() {
		correlation := entry.Correlation
		header.Correlation = &correlation
	}
	for _, payload := range entry.Payloads {
		header.PayloadCommitments = append(header.PayloadCommitments, digestBytes(payload))
	}
	if len(header.PayloadCommitments) > 0 {
		header.RetentionState = Available
	}
	if err := validateHeader(header); err != nil {
		return Record{}, err
	}
	headerBytes, err := canonicalJSON(header)
	if err != nil {
		return Record{}, fmt.Errorf("%w: header: %v", ErrInvalid, err)
	}
	for i, payload := range entry.Payloads {
		if err := b.payloads.Put(ctx, header.PayloadCommitments[i], payload); err != nil {
			return Record{}, fmt.Errorf("store payload: %w", err)
		}
	}
	sealed, err := b.seal(ctx, header, headerBytes, committedAt)
	if err != nil {
		return Record{}, err
	}
	stored := StoredRecord{Seq: seq, RecordID: sealed.CapsuleID, Header: headerBytes, Capsule: sealed.Payload, Envelope: sealed.Envelope}
	if err := b.store.PutRecord(ctx, stored); err != nil {
		return Record{}, err
	}
	record := Record{RecordID: sealed.CapsuleID, Seq: seq, Header: header}
	if err := b.commitLocked(ctx, record); err != nil {
		b.pending = &record
		return Record{}, fmt.Errorf("record %s is stored but not yet committed; it keeps seq %d and is retried first: %w", record.RecordID, seq, err)
	}
	if err := b.admitLocked(ctx, record); err != nil {
		return record, err
	}
	if b.checkpointEvery > 0 && !b.checkpointing && seq%b.checkpointEvery == 0 {
		if _, err := b.checkpointLocked(ctx); err != nil {
			return record, fmt.Errorf("record committed; automatic checkpoint failed: %w", err)
		}
	}
	return record, nil
}

func (b *Book) seal(ctx context.Context, header Header, headerBytes []byte, at time.Time) (emit.Result, error) {
	identity, err := signingIdentity(ctx, b.signer)
	if err != nil {
		return emit.Result{}, err
	}
	var references []emit.Reference
	for _, link := range header.Links {
		references = append(references, emit.Reference{Type: "agent-action-capsule", DigestAlg: "SHA-256", Digest: link.Target, CitationPurpose: string(link.Type)})
	}
	sealed, err := emit.Seal(emit.SealInput{
		Capsule: emit.Input{
			ActionID: fmt.Sprintf("%s#%d", b.id, header.Seq), ActionType: emit.ActionTypeFYI,
			Operator: b.operator, Developer: b.developer, Timestamp: at, References: references,
		},
		Payload:  json.RawMessage(headerBytes),
		Identity: identity,
	})
	if err != nil {
		return emit.Result{}, fmt.Errorf("seal record: %w", err)
	}
	if err := checkCapsuleCommitsHeader(sealed.CapsuleID, sealed.Payload, headerBytes); err != nil {
		return emit.Result{}, err
	}
	return sealed, nil
}

// Get returns one record by id.
func (b *Book) Get(_ context.Context, recordID string) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	record, ok := b.records[recordID]
	if !ok {
		return Record{}, fmt.Errorf("%w: record %s", ErrNotFound, recordID)
	}
	return record, nil
}

// Query answers from the operational index. Its result is a local lookup,
// not a proof; use Bundle or Respond for anything a stranger must check.
func (b *Book) Query(ctx context.Context, filter Filter) ([]Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.queryLocked(ctx, filter)
}

func (b *Book) queryLocked(ctx context.Context, filter Filter) ([]Record, error) {
	ids, err := b.operational.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		out = append(out, b.records[id])
	}
	return out, nil
}

// Discover returns approximate candidates. See Candidates for what that
// result may and may not be used for.
func (b *Book) Discover(ctx context.Context, query string, limit int) (Candidates, error) {
	return b.discovery.Search(ctx, query, limit)
}

// Rebuild re-derives the operational and discovery indexes from the committed
// history alone and returns the operational index's state digest.
func (b *Book) Rebuild(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.rebuildIndexes(ctx); err != nil {
		return "", err
	}
	return b.operational.StateDigest(ctx)
}

// IndexState is the operational index's current state digest.
func (b *Book) IndexState(ctx context.Context) (string, error) {
	return b.operational.StateDigest(ctx)
}

// Info names the book and the substrate it states it uses.
func (b *Book) Info() (string, SubstrateInfo) {
	return b.id, b.commitments.Info()
}

// Size is the number of committed records.
func (b *Book) Size() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return uint64(len(b.order))
}

// Checkpoint commits any changed authenticated-index roots as records, then
// checkpoints the commitment history underlying this book.
func (b *Book) Checkpoint(ctx context.Context) (Checkpoint, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checkpointLocked(ctx)
}

func (b *Book) checkpointLocked(ctx context.Context) (Checkpoint, error) {
	if err := b.flushPendingLocked(ctx); err != nil {
		return Checkpoint{}, err
	}
	b.checkpointing = true
	defer func() { b.checkpointing = false }()
	for _, index := range b.index {
		root := index.Root()
		if b.roots[root.Index] == root {
			continue
		}
		statement, err := json.Marshal(struct {
			IndexRoot
			AsOfSeq uint64 `json:"as_of_seq"`
		}{root, uint64(len(b.order))})
		if err != nil {
			return Checkpoint{}, err
		}
		if _, err := b.appendLocked(ctx, Entry{RecordType: RecordTypeIndexRoot, EpistemicType: DerivedMetric, Statement: statement}); err != nil {
			return Checkpoint{}, err
		}
		b.roots[root.Index] = root
	}
	cp, err := b.commitments.Checkpoint(ctx, b.now())
	if err != nil {
		return Checkpoint{}, err
	}
	if err := b.store.PutCheckpoint(ctx, cp); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// Checkpoints returns every checkpoint this book has issued, oldest first.
func (b *Book) Checkpoints(ctx context.Context) ([]Checkpoint, error) {
	return b.store.Checkpoints(ctx)
}

// Release closes the store, substrate and operational index.
func (b *Book) Release() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return errors.Join(b.store.Release(), b.commitments.Release(), b.operational.Release())
}

func (b *Book) latestLocked(ctx context.Context) (Checkpoint, bool, error) {
	cp, err := b.commitments.Latest(ctx)
	if errors.Is(err, ErrNotFound) {
		return Checkpoint{}, false, nil
	}
	return cp, err == nil, err
}

func (b *Book) storedLocked(ctx context.Context, recordID string) (StoredRecord, error) {
	return b.store.GetRecord(ctx, recordID)
}
