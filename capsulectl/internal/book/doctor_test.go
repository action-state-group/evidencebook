package book

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runDoctor runs `capsulectl book doctor` and returns its findings by check.
func runDoctor(t *testing.T, args ...string) map[string]evidencebook.DoctorFinding {
	t.Helper()
	out, err := invoke(t, "", append([]string{"doctor"}, args...)...)
	require.NoError(t, err, out)
	var result struct {
		Findings []evidencebook.DoctorFinding `json:"findings"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result), out)
	byCheck := map[string]evidencebook.DoctorFinding{}
	for _, f := range result.Findings {
		byCheck[f.Check] = f
	}
	return byCheck
}

// checkpointCount is the number of checkpoints the profile's book holds.
func checkpointCount(t *testing.T, p cli.Profile) int {
	t.Helper()
	opened, err := openBook(t.Context(), p, false)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.release()) }()
	checkpoints, err := opened.book.Checkpoints(t.Context())
	require.NoError(t, err)
	return len(checkpoints)
}

func checkpointProfile(t *testing.T, p cli.Profile) {
	t.Helper()
	opened, err := openBook(t.Context(), p, false)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.release()) }()
	_, err = opened.book.Checkpoint(t.Context())
	require.NoError(t, err)
}

func TestBookDoctorHealthyNodeSealsNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"}, half{"x2", "r2", "p2"})
	checkpointProfile(t, p)
	records, checkpoints := bookSize(t, p), checkpointCount(t, p)

	findings := runDoctor(t, "--profile", "node", "--probe")
	assert.Equal(t, records, bookSize(t, p), "doctor --probe adds no record")
	assert.Equal(t, checkpoints, checkpointCount(t, p), "doctor --probe cuts no checkpoint")
	for _, check := range []string{evidencebook.DoctorProbeCheckpoints, evidencebook.DoctorProbeHistoryCard} {
		assert.Equal(t, evidencebook.DoctorStateMatch, findings[check].State, "%s: %+v", check, findings[check])
	}
	assert.Equal(t, evidencebook.DoctorStateOn, findings[evidencebook.DoctorSharing].State)
	assert.Equal(t, evidencebook.DoctorStateUnbound, findings[evidencebook.DoctorControllerBinding].State)
	assert.Equal(t, evidencebook.DoctorStateUnwitnessed, findings[evidencebook.DoctorWitnessed].State)
	assert.Equal(t, evidencebook.DoctorStateNotChecked, findings[evidencebook.DoctorPolicyCommitment].State)
	assert.Contains(t, findings, evidencebook.DoctorStrangerView)
	assert.Contains(t, findings, evidencebook.DoctorIdentifiedView)
}

func TestBookDoctorSharingOff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	checkpointProfile(t, p)
	policy := writeJSON(t, `{"record_at_completion":"off","history_segments":"off","adjudications":"off"}`)

	findings := runDoctor(t, "--profile", "node", "--policy", policy, "--probe")
	assert.Equal(t, evidencebook.DoctorStateOff, findings[evidencebook.DoctorSharing].State)
	assert.Contains(t, findings[evidencebook.DoctorIdentifiedView].Detail, "history_segments=off")
	assert.Equal(t, evidencebook.DoctorStateMatch, findings[evidencebook.DoctorProbeCheckpoints].State, "commitments stay public")
}

// A node whose served checkpoints are signed under a key other than the
// one its profile now holds (a checkpoint key rotated after the book was
// checkpointed): its served answer disagrees with what it advertises.
func TestBookDoctorServedAnswerDisagrees(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, keys := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	checkpointProfile(t, p)
	rotated, rotatedKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	p.Checkpoint.Signing.Value = hex.EncodeToString(rotatedKey.Seed())
	p.Checkpoint.TrustedKeys = []string{hex.EncodeToString(keys.checkpoint), hex.EncodeToString(rotated)}
	require.NoError(t, cli.SaveProfile(p, true))
	records, checkpoints := bookSize(t, p), checkpointCount(t, p)

	findings := runDoctor(t, "--profile", "node", "--probe")
	for _, check := range []string{evidencebook.DoctorProbeCheckpoints, evidencebook.DoctorProbeHistoryCard} {
		assert.Equal(t, evidencebook.DoctorStateMismatch, findings[check].State, "%s: %+v", check, findings[check])
		assert.Contains(t, findings[check].Detail, "fails verification")
	}
	assert.Equal(t, records, bookSize(t, p))
	assert.Equal(t, checkpoints, checkpointCount(t, p))
}

// A node that never checkpointed is refused to a stranger, as its
// stranger view says: the probe has nothing to verify against.
func TestBookDoctorNoCheckpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	records := bookSize(t, p)

	findings := runDoctor(t, "--profile", "node", "--probe")
	assert.Equal(t, evidencebook.DoctorStateMissing, findings[evidencebook.DoctorCheckpoint].State)
	for _, check := range []string{evidencebook.DoctorProbeCheckpoints, evidencebook.DoctorProbeHistoryCard} {
		assert.Equal(t, evidencebook.DoctorStateNotChecked, findings[check].State, "%s: %+v", check, findings[check])
		assert.Contains(t, findings[check].Detail, "no checkpoint to verify against")
	}
	assert.Equal(t, records, bookSize(t, p))
	assert.Zero(t, checkpointCount(t, p), "the probe issued no checkpoint")
}

func TestBookDoctorReadsWitnessAndBindings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, keys := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	checkpointProfile(t, p)
	opened, err := openBook(t.Context(), p, false)
	require.NoError(t, err)
	checkpoints, err := opened.book.Checkpoints(t.Context())
	require.NoError(t, err)
	require.NoError(t, opened.release())
	latest := checkpoints[len(checkpoints)-1]
	store := newBookWitnessStore(p)
	require.NoError(t, store.seed("w1", latest.TreeSize, latest.Statement, day0))
	binding := writeJSON(t, `{"binding_version":"controller-binding/0","keys":["`+hex.EncodeToString(keys.record)+`","ff"],"sigs":[]}`)

	findings := runDoctor(t, "--profile", "node", "--controller-binding", binding)
	assert.Equal(t, evidencebook.DoctorStateNotChecked, findings[evidencebook.DoctorControllerBinding].State)
	assert.Len(t, findings[evidencebook.DoctorControllerBinding].Records, 1)
	// The checkpoint is due for delivery to w1, with no receipt yet.
	assert.Equal(t, evidencebook.DoctorStateUnwitnessed, findings[evidencebook.DoctorWitnessed].State)
	assert.Contains(t, findings[evidencebook.DoctorWitnessed].Detail, "no witness receipt")

	states, err := store.load()
	require.NoError(t, err)
	states[0].Receipt = &cll.WitnessReceiptState{Bytes: []byte("receipt")}
	require.NoError(t, store.save(states))
	findings = runDoctor(t, "--profile", "node")
	assert.Equal(t, evidencebook.DoctorStateWitnessed, findings[evidencebook.DoctorWitnessed].State)
	assert.Contains(t, findings[evidencebook.DoctorWitnessed].Detail, "w1")
}

func TestBookDoctorRefusesBadInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	initBook(t, p)
	bad := filepath.Join(t.TempDir(), "binding.json")
	require.NoError(t, os.WriteFile(bad, []byte("not json"), 0o600))
	_, err := invoke(t, "", "doctor", "--profile", "node", "--controller-binding", bad)
	assert.ErrorIs(t, err, cli.ErrInput)
	_, err = invoke(t, "", "doctor", "--profile", "node", "--policy", writeJSON(t, `{"history_segments":"everyone"}`))
	assert.Error(t, err)
}
