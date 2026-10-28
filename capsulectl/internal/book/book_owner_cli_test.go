package book

import (
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A jsonl profile that names no owner is refused by the core with the
// one-command fix, before any book verb runs (capsule-cli#428): the book does
// not take it for its own.
func TestABookVerbOnAnOwnerlessProfileNamesTheFix(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, _ := bookProfile(t, "legacy")
	p.Log = ""
	require.NoError(t, cli.SaveProfile(p, true))

	_, err := invoke(t, "", "book", "close", "--profile", "legacy", "--period", "day", "--counterparty", "b")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "profile legacy has no declared log owner: capsulectl profile update --profile legacy --log")
}
