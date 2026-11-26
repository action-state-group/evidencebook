package book

import (
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/stretchr/testify/assert"
)

// A window's delivery state follows the core's own classification
// (capsule-cli#648): a first continuity 409 is re-armed at the next run, so
// it is pending, not failed; a 409 refused again after that retry, and any
// other refusal stored for good, is failed.
func TestWitnessEntryKeepsAFirstContinuityRefusalPending(t *testing.T) {
	p := cli.Profile{}
	p.Checkpoint.Endpoint = "https://witness.example"
	refused := func(status int, extra string) cll.WitnessState {
		return cll.WitnessState{Attempts: 1, Permanent: true, LastError: "witness returned HTTP 400: " + cli.WitnessFailureBody(status) + extra}
	}
	assert.Equal(t, "pending", witnessEntry(p, refused(409, ""))["state"])
	// The mark capsule-cli keeps on a 409 it already re-armed once.
	assert.Equal(t, "failed", witnessEntry(p, refused(409, "; re-armed once after a continuity refusal"))["state"])
	assert.Equal(t, "failed", witnessEntry(p, refused(422, ""))["state"])
	assert.Equal(t, "pending", witnessEntry(p, cll.WitnessState{Attempts: 1, LastError: "witness returned HTTP 503"})["state"])
}
