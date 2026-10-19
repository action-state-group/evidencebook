// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/action-state-group/capsule-emit/go/artifact"
	artifactjsonl "github.com/action-state-group/capsule-emit/go/artifact/jsonl"
)

// ArtifactStore is where a book keeps its records' originals: the payload
// bytes a header commits by digest. It is capsule-emit's artifact store
// contract (artifact.Store) reduced to what the book calls, so any of its
// backends (jsonl, sqlite, mysql) serves. The store holds one
// artifact.Record per record that has originals, keyed by the record id:
// the record capsule, its Producer Envelope, and the originals. The store
// must trust the book's signer, since Put and Get verify the envelope
// signer against the store's trusted keys.
//
// The header commits digests, never storage locations, and the store's
// per-original ContentSHA256 is the same SHA-256 the header commits, so an
// original is found by the digest its record commits. Each record keeps its
// own copy: two records committing identical bytes hold two copies, and
// destroying one record's originals never touches the other's.
//
// Put and Purge must be durable when they return: the book stores a record
// only after its originals' Put returns, so a record that survives a crash
// has originals that survived it too. OpenOriginalsFile's store syncs its
// file; a host supplying its own store makes the same guarantee.
type ArtifactStore interface {
	Put(ctx context.Context, record artifact.Record) error
	Get(ctx context.Context, id string) (artifact.Record, error)
	// Purge erases a record's present originals and keeps their digests.
	Purge(ctx context.Context, id string) error
}

// OpenOriginalsFile opens, creating it when absent, the reference
// ArtifactStore: capsule-emit's JSONL artifact store at path, under
// namespace, trusting the given keys (the book's signer key among them). It
// suits a single writer, as the book is. The file is kept owner-only, and
// every Put and Purge is synced to stable storage before it returns.
func OpenOriginalsFile(ctx context.Context, path, namespace string, trusted ...ed25519.PublicKey) (ArtifactStore, error) {
	store, err := artifactjsonl.New(path, namespace, trusted)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := store.Init(ctx); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return SyncedFile(store, path), nil
}

// SyncedFile makes a file-backed ArtifactStore durable as the book requires:
// after each successful Put or Purge it syncs the file at path and the
// directory holding it.
func SyncedFile(store ArtifactStore, path string) ArtifactStore {
	return syncedFile{ArtifactStore: store, path: path}
}

type syncedFile struct {
	ArtifactStore
	path string
}

func (s syncedFile) Put(ctx context.Context, record artifact.Record) error {
	if err := s.ArtifactStore.Put(ctx, record); err != nil {
		return err
	}
	return syncPath(s.path)
}

func (s syncedFile) Purge(ctx context.Context, id string) error {
	if err := s.ArtifactStore.Purge(ctx, id); err != nil {
		return err
	}
	return syncPath(s.path)
}

// syncPath syncs a file and then its directory, so neither its contents nor
// its directory entry (a rewrite renames over it) is lost to a crash.
func syncPath(path string) error {
	for _, p := range []string{path, filepath.Dir(path)} {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		if err := errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
	}
	return nil
}

// payloadName is the artifact name of a record's n-th payload (from 1) when
// the caller passed bare bytes in Entry.Payloads.
func payloadName(n int) string { return "payload-" + strconv.Itoa(n) }

// payloadOriginals turns Entry.Payloads into the originals the book stores:
// unbound artifacts (no capsule digest binds them; the header's
// payload_commitments do), named payload-1, payload-2, ... in order.
func payloadOriginals(payloads [][]byte) []artifact.Artifact {
	out := make([]artifact.Artifact, 0, len(payloads))
	for i, payload := range payloads {
		out = append(out, artifact.Artifact{Name: payloadName(i + 1), State: artifact.Present, Content: append([]byte{}, payload...)})
	}
	return out
}

// checkOriginals refuses originals the book could not commit and later
// resolve: each is present with content, or declared never retained with
// none, under a distinct name. Only present originals are committed.
func checkOriginals(originals []artifact.Artifact) error {
	names := make(map[string]bool, len(originals))
	for _, original := range originals {
		switch {
		case original.State == artifact.Present && original.Content != nil:
		case original.State == artifact.NeverRetained && original.Content == nil:
		default:
			return fmt.Errorf("%w: original %q must be present with content, or never retained without", ErrInvalid, original.Name)
		}
		if names[original.Name] {
			return fmt.Errorf("%w: original %q is given more than once", ErrInvalid, original.Name)
		}
		names[original.Name] = true
	}
	return nil
}

// originalBytes finds the original committed as digest in a stored record.
// found is false when the record holds no original with that digest; held
// is false when it holds one whose bytes were purged.
func originalBytes(record artifact.Record, digest string) (data []byte, found, held bool) {
	for _, a := range record.Artifacts {
		if a.ContentSHA256 != digest {
			continue
		}
		if a.State == artifact.Present && digestBytes(a.Content) == digest {
			return a.Content, true, true
		}
		found = true
	}
	return nil, found, false
}

// MigratePayloadDir moves the payload bytes an earlier evidencebook kept in a
// payload directory (one file per payload, named by its SHA-256) into the
// artifact store, one artifact.Record per record that commits payloads. It
// reads the records from store and changes none of them: no header, record
// id, log entry or checkpoint moves, so every existing proof still holds.
// A record whose originals are already in the artifact store is skipped, so
// an interrupted migration is resumed by running it again. A payload whose
// file is missing (one deleted under the earlier per-payload retention) is
// stored as never retained rather than invented. It returns how many
// records it stored.
//
// The directory itself is left for the caller to remove once the call
// returns without error.
func MigratePayloadDir(ctx context.Context, store Store, originals ArtifactStore, dir string) (int, error) {
	records, err := store.Records(ctx)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, sr := range records {
		record, err := decodeStored(sr)
		if err != nil {
			return moved, err
		}
		if len(record.Header.PayloadCommitments) == 0 {
			continue
		}
		if existing, err := originals.Get(ctx, sr.RecordID); err == nil {
			// Held already (an earlier run stored it). The store's records are
			// immutable, so it must already hold every payload the directory
			// still has, or the directory is the only copy and must stay.
			for _, digest := range record.Header.PayloadCommitments {
				if _, _, present := originalBytes(existing, digest); present {
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, digest)); err == nil {
					return moved, fmt.Errorf("%w: record %s is in the artifact store without payload %s, which the payload directory still holds", ErrCorrupt, sr.RecordID, digest)
				}
			}
			continue
		} else if !errors.Is(err, artifact.ErrNotFound) {
			return moved, fmt.Errorf("record %s: %w", sr.RecordID, err)
		}
		held := artifact.Record{CapsuleID: sr.RecordID, Capsule: sr.Capsule, ProducerEnvelope: sr.Envelope}
		for i, digest := range record.Header.PayloadCommitments {
			data, err := os.ReadFile(filepath.Join(dir, digest))
			switch {
			case errors.Is(err, os.ErrNotExist):
				held.Artifacts = append(held.Artifacts, artifact.Artifact{Name: payloadName(i + 1), State: artifact.NeverRetained})
				continue
			case err != nil:
				return moved, err
			case digestBytes(data) != digest:
				return moved, fmt.Errorf("%w: payload file %s does not hash to its name", ErrCorrupt, digest)
			}
			held.Artifacts = append(held.Artifacts, artifact.Artifact{Name: payloadName(i + 1), State: artifact.Present, Content: data})
		}
		if err := originals.Put(ctx, held); err != nil {
			return moved, fmt.Errorf("record %s: %w", sr.RecordID, err)
		}
		moved++
	}
	return moved, nil
}
