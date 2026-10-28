package book

import (
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
)

// Only a jsonl profile whose log the book plugin keeps is a book: the core
// lets a sqlite store name a plugin as its owner too, which is not one.
func TestIsBookNeedsAJSONLProfileNamingTheBook(t *testing.T) {
	assert.True(t, isBook(cli.Profile{Type: "jsonl", LogID: "l", Log: Name}))
	assert.False(t, isBook(cli.Profile{Type: "jsonl", LogID: "l"}), "a profile that names no owner is not read as a book: the core refuses it and prints the one-command fix")
	assert.False(t, isBook(cli.Profile{Type: "jsonl", LogID: "l", Log: cli.LogCLL}))
	assert.False(t, isBook(cli.Profile{Type: "jsonl", LogID: "l", Log: "ledger"}))
	assert.False(t, isBook(cli.Profile{Type: "sqlite", Namespace: "ns", Log: Name}), "a sqlite store naming the book is not a book")
	assert.False(t, isBook(cli.Profile{Type: "sqlite", Namespace: "deal"}))
}
