// SPDX-License-Identifier: Apache-2.0

// Command goose-card is Go's check of a Goose node's standard_history/1
// card (python/examples/goose): it opens a copy of the node's shelf with
// Go's evidencebook, derives the card's anchor from the cadence
// (Cadence.StandardHistoryAnchor) and every deal entry from the deal
// books under it (dealprofile.StandardHistoryDeal), and requires the card
// the Python node wrote to be exactly the card Go derives
// (DeriveStandardHistory), with every SH §6 property PASS against those
// inputs.
//
//	goose-card SHELF CARD KEY
//
// SHELF is the node's shelf.db, CARD the JSON the node's
// loop_standard_history tool wrote ({"card", "as_of", "policy", ...}), and
// KEY the node's signing key (hex): the key its deal steps' records verify
// under, which the relying party holds.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	artifactsqlite "github.com/action-state-group/capsule-emit/go/artifact/sqlite"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	_ "modernc.org/sqlite"
)

// The node's layout (python/examples/goose/goose_node.py).
const (
	namespace    = "goose"
	cadenceLogID = "goose/cadence"
	nodeBook     = "goose/node"
	halfReceived = "goose_half_received"
	holderKind   = "person"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "goose-card:", err)
		os.Exit(1)
	}
}

type written struct {
	Card   string                 `json:"card"`
	Tick   uint64                 `json:"tick"`
	AsOf   standardhistory.AsOf   `json:"as_of"`
	Policy standardhistory.Policy `json:"policy"`
}

func run(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: goose-card SHELF CARD KEY")
	}
	raw, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	var card written
	if err := json.Unmarshal(raw, &card); err != nil {
		return fmt.Errorf("card file: %w", err)
	}
	key, err := hex.DecodeString(args[2])
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("KEY is not a hex Ed25519 public key")
	}
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "goose-card")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// A snapshot: opening a shelf takes its writer lock, and the node may be
	// running. VACUUM INTO copies one consistent read of the file, WAL included.
	copied := filepath.Join(dir, "shelf.db")
	if err := snapshot(ctx, args[0], copied); err != nil {
		return err
	}
	asOf, deals, err := derive(ctx, copied, card.Tick, ed25519.PublicKey(key))
	if err != nil {
		return err
	}
	if asOf != card.AsOf {
		return fmt.Errorf("the card is as of %+v, the shelf's tick %d is %+v", card.AsOf, card.Tick, asOf)
	}
	want, err := evidencebook.DeriveStandardHistory(holderKind, asOf, card.Policy, deals)
	if err != nil {
		return err
	}
	if string(want) != card.Card {
		return fmt.Errorf("the card is not the one Go derives from the shelf:\n got %s\nwant %s", card.Card, want)
	}
	properties, err := standardhistory.Check([]byte(card.Card), &card.Policy, &asOf, deals)
	if err != nil {
		return err
	}
	for _, p := range standardhistory.Properties {
		if properties[p] != standardhistory.Pass {
			return fmt.Errorf("the card fails %s", p)
		}
	}
	out, err := json.Marshal(map[string]any{"accepted": true, "anchor": asOf.Anchor, "deals": len(deals), "properties": properties})
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// derive reads the shelf as Go's evidencebook opens it: tick n's anchor and
// extents (the newest tick's when n is 0), and one deal entry per deal book
// under them.
func derive(ctx context.Context, path string, n uint64, key ed25519.PublicKey) (standardhistory.AsOf, []standardhistory.Deal, error) {
	var asOf standardhistory.AsOf
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		return asOf, nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	originals, err := artifactsqlite.New(db, namespace, []ed25519.PublicKey{key})
	if err != nil {
		return asOf, nil, err
	}
	// The verifier writes nothing it keeps; the shelf still wants a signer.
	_, throwaway, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return asOf, nil, err
	}
	signer, err := evidencebook.NewEd25519Signer(throwaway)
	if err != nil {
		return asOf, nil, err
	}
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{
		Path: path, Operator: "goose-card", Signer: signer, CheckpointKey: throwaway, Originals: originals,
		AuthenticatedIndexes: func(string) []evidencebook.AuthenticatedIndex { return []evidencebook.AuthenticatedIndex{} },
	})
	if err != nil {
		return asOf, nil, err
	}
	defer shelf.Release()
	cadence, err := shelf.Cadence(ctx, evidencebook.CadenceConfig{LogID: cadenceLogID,
		Ended: func(context.Context, string) (bool, error) { return false, nil }})
	if err != nil {
		return asOf, nil, err
	}
	anchor, err := cadence.StandardHistoryAnchor(ctx, n)
	if err != nil {
		return asOf, nil, err
	}
	received := map[string]bool{}
	if extent := anchor.Extents[nodeBook]; extent > 0 {
		records, err := records(ctx, shelf, nodeBook, extent)
		if err != nil {
			return asOf, nil, err
		}
		for _, r := range records {
			var statement struct {
				DealID string `json:"deal_id"`
			}
			if r.Header.RecordType == halfReceived && json.Unmarshal(r.Header.Statement, &statement) == nil {
				received[statement.DealID] = true
			}
		}
	}
	books := make([]string, 0, len(anchor.Extents))
	for id := range anchor.Extents {
		if strings.HasPrefix(id, "deal/") && strings.Count(id, "/") == 1 {
			books = append(books, id)
		}
	}
	sort.Strings(books)
	deals := []standardhistory.Deal{}
	for _, id := range books {
		records, err := records(ctx, shelf, id, anchor.Extents[id])
		if err != nil {
			return asOf, nil, err
		}
		in := dealprofile.HistoryInput{CounterpartyHalfVerified: received[strings.TrimPrefix(id, "deal/")]}
		for _, r := range records {
			held, err := originals.Get(ctx, r.RecordID)
			if err != nil {
				return asOf, nil, fmt.Errorf("%s: %w", r.RecordID, err)
			}
			at, err := time.Parse(time.RFC3339Nano, r.Header.CommittedAt)
			if err != nil {
				return asOf, nil, err
			}
			for _, a := range held.Artifacts {
				if a.Name == "payload" {
					in.Records = append(in.Records, dealprofile.HistoryRecord{Payload: a.Content, CommittedAt: at})
				}
			}
		}
		deal, ok, err := dealprofile.StandardHistoryDeal(in)
		if err != nil {
			return asOf, nil, fmt.Errorf("%s: %w", id, err)
		}
		if ok {
			deals = append(deals, deal)
		}
	}
	return anchor.AsOf, deals, nil
}

// records is a book's first extent records.
func records(ctx context.Context, shelf *evidencebook.Shelf, id string, extent uint64) ([]evidencebook.Record, error) {
	book, err := shelf.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	all, err := book.Query(ctx, evidencebook.Filter{})
	if err != nil {
		return nil, err
	}
	if uint64(len(all)) < extent {
		return nil, fmt.Errorf("%s holds %d records, the tick commits %d", id, len(all), extent)
	}
	return all[:extent], nil
}

// snapshot writes one consistent copy of the SQLite file at from to to.
func snapshot(ctx context.Context, from, to string) error {
	if _, err := os.Stat(from); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: from, RawQuery: "mode=ro"}).String())
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `VACUUM INTO ?`, to)
	return errors.Join(err, db.Close())
}
