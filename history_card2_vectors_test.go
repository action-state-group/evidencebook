// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The shared history_card/2 vectors, read by the Go, Rust and Python
// checkers: the positions P(n, W) of ER -03 §8.1.2, and staples (the same
// form as testdata/staple/staples.json) carrying a history_card/2 or broken
// in one of the seven ordered checks. EB_WRITE_STAPLE_VECTORS=1 writes them
// anew, for an intended change only: a staple carries a fresh nonce.
const (
	historyCard2StaplesPath = "testdata/history-card-2/staples.json"
)

func TestHistoryCard2Vectors(t *testing.T) {
	if os.Getenv("EB_WRITE_STAPLE_VECTORS") == "1" {
		writeHistoryCard2Vectors(t)
	}
	var staples []stapleVector
	readJSONFile(t, historyCard2StaplesPath, &staples)
	if len(staples) < 10 {
		t.Fatal("no staple vectors")
	}
	for _, v := range staples {
		got, err := CheckStaple(Staple{Request: Message{Octets: []byte(v.Request), MediaType: MediaTypeJSON},
			Reply: Reply{Message: Message{Octets: []byte(v.Reply), MediaType: MediaTypeJSON}}}, v.Holder)
		if (err == nil) != v.Pass {
			t.Errorf("%s: pass %v, got %v", v.Name, v.Pass, err)
		}
		if err == nil && (got.Nonce != v.Nonce || got.Anchor != v.Anchor) {
			t.Errorf("%s: read %s at %s", v.Name, got.Nonce, got.Anchor)
		}
	}
}

func readJSONFile(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatal(err)
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeHistoryCard2Vectors(t *testing.T) {
	ctx := context.Background()
	book := cardBook(t, 3, 20)
	holder := KeyID(book.signer)
	card, err := book.Staple(ctx, DerivationHistoryCard2)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := CheckStaple(card, holder)
	if err != nil {
		t.Fatal(err)
	}
	single := cardBook(t, 16, 0)
	oneStaple, err := single.Staple(ctx, DerivationHistoryCard2)
	if err != nil {
		t.Fatal(err)
	}
	oc, err := CheckStaple(oneStaple, KeyID(single.signer))
	if err != nil {
		t.Fatal(err)
	}
	other := standardHistoryBookSeed(t, 33)
	vector := func(name string, s Staple, reply Reply, who string, pass bool, c StapleCheck) stapleVector {
		v := stapleVector{Name: name, Holder: who, Request: string(s.Request.Octets), Reply: string(reply.Octets), Pass: pass}
		if pass {
			v.Nonce, v.Anchor = c.Nonce, c.Anchor
		}
		return v
	}
	broken := func(name string, edit func(map[string]any)) stapleVector {
		return vector(name, card, widened(t, book, card.Reply, edit), holder, false, cc)
	}
	history := func(c map[string]any) []any { return c["history"].([]any) }
	entry := func(c map[string]any, i int) map[string]any { return history(c)[i].(map[string]any) }
	vectors := []stapleVector{
		vector("history_card/2", card, card.Reply, holder, true, cc),
		vector("history_card/2 over one checkpoint", oneStaple, oneStaple.Reply, KeyID(single.signer), true, oc),
		vector("another holder", card, card.Reply, KeyID(other.signer), false, cc),
		vector("a reply to the history_card/1 derivation", card, func() Reply {
			one, err := book.Staple(ctx, DerivationHistoryCard)
			if err != nil {
				t.Fatal(err)
			}
			return one.Reply
		}(), holder, false, cc),
		broken("a window of 0", func(c map[string]any) { c["window"] = 0 }),
		broken("a window of 65", func(c map[string]any) { c["window"] = 65 }),
		broken("a window that is text", func(c map[string]any) { c["window"] = "3" }),
		broken("no window", func(c map[string]any) { delete(c, "window") }),
		broken("a window the positions do not follow", func(c map[string]any) { c["window"] = 5 }),
		broken("no history", func(c map[string]any) { c["history"] = []any{} }),
		broken("a first entry not at position 0", func(c map[string]any) { entry(c, 0)["position"] = 1 }),
		broken("positions that repeat", func(c map[string]any) { entry(c, 1)["position"] = 0 }),
		broken("a missing sample position", func(c map[string]any) { c["history"] = slices.Delete(history(c), 2, 3) }),
		broken("an extra position", func(c map[string]any) {
			extra := map[string]any{}
			for k, v := range entry(c, 1) {
				extra[k] = v
			}
			extra["position"] = 5
			c["history"] = slices.Insert(history(c), 2, any(extra))
		}),
		broken("a position that is not an integer", func(c map[string]any) { entry(c, 1)["position"] = "4" }),
		broken("an entry without receipts", func(c map[string]any) { delete(entry(c, 1), "receipts") }),
		broken("receipts that are not a list", func(c map[string]any) { entry(c, 1)["receipts"] = "none" }),
		broken("an entry with another member", func(c map[string]any) { entry(c, 1)["record"] = "a record body" }),
		broken("a first entry with a proof", func(c map[string]any) { entry(c, 0)["consistency_from_previous"] = "00" }),
		broken("a later entry without a proof", func(c map[string]any) { entry(c, 2)["consistency_from_previous"] = nil }),
		broken("a proof between the wrong pair", func(c map[string]any) {
			entry(c, 3)["consistency_from_previous"] = entry(c, 2)["consistency_from_previous"]
		}),
		broken("checkpoints in the wrong order", func(c map[string]any) {
			a, b := entry(c, 1)["checkpoint"], entry(c, 2)["checkpoint"]
			entry(c, 1)["checkpoint"], entry(c, 2)["checkpoint"] = b, a
		}),
		broken("a card that does not end at its anchor", func(c map[string]any) { c["history"] = history(c)[:len(history(c))-1] }),
		broken("a card of the history_card/1 derivation", func(c map[string]any) { c["derivation"] = DerivationHistoryCard }),
		broken("a card with another member", func(c map[string]any) { c["records"] = []any{"a record body"} }),
	}
	writeJSONFile(t, historyCard2StaplesPath, vectors)
}
