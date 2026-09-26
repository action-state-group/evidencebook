// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func seededKey(seed byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed + byte(i)
	}
	return ed25519.NewKeyFromSeed(s)
}

type bookFixture struct {
	dir   string
	id    string
	seed  byte
	clock *testClock
}

func newFixture(t *testing.T, id string, seed byte) *bookFixture {
	t.Helper()
	return &bookFixture{dir: t.TempDir(), id: id, seed: seed, clock: &testClock{t: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}}
}

func (f *bookFixture) config(t *testing.T) Config {
	t.Helper()
	store, err := OpenFileStore(filepath.Join(f.dir, "records"))
	if err != nil {
		t.Fatal(err)
	}
	substrate, err := OpenCLL(filepath.Join(f.dir, "cll.jsonl"), f.id+"-log", seededKey(f.seed+100))
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := OpenPayloadDir(filepath.Join(f.dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewEd25519Signer(seededKey(f.seed))
	if err != nil {
		t.Fatal(err)
	}
	return Config{BookID: f.id, Operator: f.id + "-operator", Store: store, Substrate: substrate, Payloads: payloads, Signer: signer, Now: f.clock.Now}
}

func (f *bookFixture) open(t *testing.T) *Book {
	t.Helper()
	book, err := Open(context.Background(), f.config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	return book
}

func mustAppend(t *testing.T, book *Book, entry Entry) Record {
	t.Helper()
	record, err := book.Append(context.Background(), entry)
	if err != nil {
		t.Fatalf("append %s: %v", entry.RecordType, err)
	}
	return record
}

func observation(subject string, payload string) Entry {
	return Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: subject, Payloads: [][]byte{[]byte(payload)}}
}

func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
