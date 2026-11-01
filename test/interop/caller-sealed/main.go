// SPDX-License-Identifier: Apache-2.0

// Command caller-sealed is Go's side of the caller-sealed record interlock:
// a deal book on a shelf whose records are Capsules their producer seals
// itself (Book.AppendCapsule), each carrying its header in
// evidence_record_header and committing three fields, as a deal step does
// (dealprofile x-deal-v0 §1). One language writes the book, another
// continues it, and every reader must accept each record and open each
// field (test/interop/python-evidencebook/caller_sealed.py is Python's side).
//
//	caller-sealed write DIR      a new DIR/shelf.db with the deal's first step
//	caller-sealed continue DIR   the deal's next step on DIR/shelf.db
//	caller-sealed read DIR       the book's records and field openings, as JSON
//
// The steps' records are the profile's positive fixtures, in order; the
// keys are the shelf vectors' test keys. Never use them for anything else.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/capsule-emit/go/artifact"
	artifactsqlite "github.com/action-state-group/capsule-emit/go/artifact/sqlite"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/internal/shelfscript"
	_ "modernc.org/sqlite"
)

// BookID is the deal's book; Fixtures are the records its steps seal, in
// order.
const BookID = "deal/deal-3f9c2a7be4d15068"

var Fixtures = []string{"01-baseline.json", "02-message-quote.json", "03-claim.json", "04-evidence.json"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "caller-sealed:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: caller-sealed write|continue|read DIR")
	}
	ctx := context.Background()
	dir := args[1]
	path := filepath.Join(dir, shelfscript.FileName)
	switch args[0] {
	case "write":
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		return step(ctx, path)
	case "continue":
		return step(ctx, path)
	case "read":
		out, err := read(ctx, path)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	}
	return fmt.Errorf("unknown command %q", args[0])
}

type env struct {
	db        *sql.DB
	originals *artifactsqlite.Store
	shelf     *evidencebook.Shelf
	at        time.Time
}

func open(ctx context.Context, path string) (*env, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: absolute}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err := db.Exec(statement); err != nil {
			return nil, errors.Join(err, db.Close())
		}
	}
	originals, err := artifactsqlite.New(db, shelfscript.Namespace, []ed25519.PublicKey{shelfscript.SignerKey.Public().(ed25519.PublicKey)})
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := originals.Init(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	signer, err := evidencebook.NewEd25519Signer(shelfscript.SignerKey)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	e := &env{db: db, originals: originals}
	e.shelf, err = evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{
		Path: path, Operator: shelfscript.Operator, Signer: signer, CheckpointKey: shelfscript.CheckpointKey,
		Originals: originals, Now: func() time.Time { return e.at },
	})
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return e, nil
}

func (e *env) release() error { return errors.Join(e.shelf.Release(), e.db.Close()) }

func fixture(name string) (json.RawMessage, error) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../dealprofile/x-deal-v0/fixtures/positive", name))
	if err != nil {
		return nil, err
	}
	var f struct {
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return f.Record, nil
}

// step seals the book's next step: the next fixture's record, chained to
// the step before it, at the record's own time.
func step(ctx context.Context, path string) (err error) {
	e, err := open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, e.release()) }()
	book, err := e.shelf.Open(ctx, BookID)
	if err != nil {
		return err
	}
	seq := int64(book.Size()) + 1
	if int(seq) > len(Fixtures) {
		return fmt.Errorf("the book already holds every fixture's step")
	}
	record, err := fixture(Fixtures[seq-1])
	if err != nil {
		return err
	}
	var block struct {
		Block struct {
			DealID string `json:"deal_id"`
			At     string `json:"at"`
		} `json:"x-deal-v0"`
	}
	if err := json.Unmarshal(record, &block); err != nil {
		return err
	}
	payload, _, err := dealprofile.Encode(record, nil)
	if err != nil {
		return err
	}
	if e.at, err = time.Parse(time.RFC3339, block.Block.At); err != nil {
		return err
	}
	in := dealprofile.StepInput{DealID: block.Block.DealID, Seq: seq, Operator: shelfscript.Operator, Developer: "caller-sealed-interop",
		At: e.at, Local: fmt.Sprintf("local event %d", seq)}
	records, err := book.Query(ctx, evidencebook.Filter{})
	if err != nil {
		return err
	}
	if len(records) > 0 {
		in.Parent = &emit.Chain{ParentCapsuleID: records[len(records)-1].RecordID, Relation: dealprofile.RelationFollows}
	}
	capsule, header, err := dealprofile.StepCapsule(in)
	if err != nil {
		return err
	}
	entry := evidencebook.Entry{RecordType: header.RecordType, EpistemicType: evidencebook.EpistemicType(header.EpistemicType),
		SubjectRef: header.SubjectRef, Fields: header.Fields}
	for _, cited := range header.Cites {
		entry.Links = append(entry.Links, evidencebook.Link{Type: evidencebook.Cites, Target: cited})
	}
	identity, err := emit.NewEd25519SigningIdentity(shelfscript.SignerKey)
	if err != nil {
		return err
	}
	seal := func(_ evidencebook.Header, headerJSON []byte) (emit.Result, error) {
		c := capsule
		c.Extensions = append(append([]emit.Extension(nil), capsule.Extensions...), emit.Extension{Name: evidencebook.RecordHeaderMember, Value: headerJSON})
		return emit.Seal(emit.SealInput{Capsule: c, Identity: identity, Payload: json.RawMessage(payload)})
	}
	originals := []artifact.Artifact{{Name: "payload", Binding: artifact.PayloadDigest, State: artifact.Present, Content: payload}}
	_, err = book.AppendCapsule(ctx, entry, originals, seal)
	return err
}

// read is the book as every reader must see it: each record, the digest of
// its Capsule's payload, and each field's opened text.
func read(ctx context.Context, path string) (out []byte, err error) {
	e, err := open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, e.release()) }()
	book, err := e.shelf.Open(ctx, BookID)
	if err != nil {
		return nil, err
	}
	type row struct {
		Seq           uint64            `json:"seq"`
		RecordID      string            `json:"record_id"`
		RecordType    string            `json:"record_type"`
		SubjectRef    string            `json:"subject_ref"`
		CommittedAt   string            `json:"committed_at"`
		Cites         []string          `json:"cites"`
		PayloadDigest string            `json:"payload_digest"`
		Fields        map[string]string `json:"fields"`
	}
	records, err := book.Query(ctx, evidencebook.Filter{})
	if err != nil {
		return nil, err
	}
	rows := []row{}
	for _, record := range records {
		r := row{Seq: record.Seq, RecordID: record.RecordID, RecordType: record.Header.RecordType, SubjectRef: record.Header.SubjectRef,
			CommittedAt: record.Header.CommittedAt, Cites: []string{}, Fields: map[string]string{}}
		for _, link := range record.Header.Links {
			r.Cites = append(r.Cites, link.Target)
		}
		held, err := e.originals.Get(ctx, record.RecordID)
		if err != nil {
			return nil, err
		}
		for _, a := range held.Artifacts {
			if a.Name == "payload" {
				r.PayloadDigest = digest(a.Content)
			}
		}
		for name := range record.Header.FieldCommitments {
			opening, err := book.Opening(ctx, record.RecordID, name)
			if err != nil {
				return nil, err
			}
			r.Fields[name] = opening.Text
		}
		rows = append(rows, r)
	}
	out, err = json.MarshalIndent(map[string]any{"book_id": BookID, "records": rows}, "", "  ")
	return append(out, '\n'), err
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
