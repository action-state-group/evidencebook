// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// StoredRecord is the durable form of one record: the canonical header bytes,
// the sealed signature-free capsule, and its Producer Envelope.
type StoredRecord struct {
	Seq      uint64          `json:"seq"`
	RecordID string          `json:"record_id"`
	Header   json.RawMessage `json:"header"`
	Capsule  json.RawMessage `json:"capsule"`
	Envelope []byte          `json:"envelope"`
}

// Store holds complete records and the checkpoints the book has observed. A
// record is stored before its identity is appended to the substrate, so the
// substrate never commits an identity the book cannot produce.
type Store interface {
	PutRecord(ctx context.Context, record StoredRecord) error
	GetRecord(ctx context.Context, recordID string) (StoredRecord, error)
	Records(ctx context.Context) ([]StoredRecord, error)
	PutCheckpoint(ctx context.Context, cp Checkpoint) error
	Checkpoints(ctx context.Context) ([]Checkpoint, error)
	Release() error
}

// FileStore is an append-only JSONL record journal plus a checkpoint journal.
type FileStore struct {
	mu          sync.Mutex
	records     *os.File
	checkpoints *os.File
	byID        map[string]StoredRecord
	history     []Checkpoint
}

// OpenFileStore opens (creating if absent) a store in dir. A torn final line
// left by a crash is truncated; a malformed complete line is corruption.
func OpenFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &FileStore{byID: make(map[string]StoredRecord)}
	var err error
	if s.records, err = openJournal(filepath.Join(dir, "records.jsonl"), func(line []byte) error {
		var record StoredRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		s.byID[record.RecordID] = record
		return nil
	}); err != nil {
		return nil, err
	}
	if s.checkpoints, err = openJournal(filepath.Join(dir, "checkpoints.jsonl"), func(line []byte) error {
		var cp Checkpoint
		if err := json.Unmarshal(line, &cp); err != nil {
			return err
		}
		s.history = append(s.history, cp)
		return nil
	}); err != nil {
		return nil, errors.Join(err, s.records.Close())
	}
	return s, nil
}

func openJournal(path string, apply func([]byte) error) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(file)
	var offset int64
	for {
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			if len(line) > 0 {
				if err := file.Truncate(offset); err != nil {
					return nil, errors.Join(err, file.Close())
				}
			}
			break
		}
		if readErr != nil {
			return nil, errors.Join(readErr, file.Close())
		}
		if err := apply(bytes.TrimSuffix(line, []byte("\n"))); err != nil {
			return nil, errors.Join(fmt.Errorf("%w: %s at offset %d: %v", ErrCorrupt, filepath.Base(path), offset, err), file.Close())
		}
		offset += int64(len(line))
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func appendLine[T StoredRecord | Checkpoint](file *os.File, value T) error {
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (s *FileStore) PutRecord(ctx context.Context, record StoredRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byID[record.RecordID]; ok {
		if existing.Seq != record.Seq || !bytes.Equal(existing.Capsule, record.Capsule) {
			return fmt.Errorf("%w: record %s already stored with different content", ErrCorrupt, record.RecordID)
		}
		return nil
	}
	if err := appendLine(s.records, record); err != nil {
		return err
	}
	s.byID[record.RecordID] = record
	return nil
}

func (s *FileStore) GetRecord(_ context.Context, recordID string) (StoredRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.byID[recordID]
	if !ok {
		return StoredRecord{}, fmt.Errorf("%w: record %s", ErrNotFound, recordID)
	}
	return record, nil
}

func (s *FileStore) Records(context.Context) ([]StoredRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StoredRecord, 0, len(s.byID))
	for _, record := range s.byID {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (s *FileStore) PutCheckpoint(ctx context.Context, cp Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, held := range s.history {
		if held.ID == cp.ID {
			return nil
		}
	}
	if err := appendLine(s.checkpoints, cp); err != nil {
		return err
	}
	s.history = append(s.history, cp)
	return nil
}

func (s *FileStore) Checkpoints(context.Context) ([]Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Checkpoint(nil), s.history...), nil
}

func (s *FileStore) Release() error {
	return errors.Join(s.records.Close(), s.checkpoints.Close())
}
