package book

import (
	"encoding/json"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type policyOut struct {
	Policy   evidencebook.DisclosurePolicy `json:"policy"`
	Settings []evidencebook.SettingRow     `json:"settings"`
	Records  []evidencebook.Record         `json:"records"`
	Then     []string                      `json:"then"`
}

func runPolicy(t *testing.T, input string, args ...string) (policyOut, string, error) {
	t.Helper()
	out, err := invoke(t, input, append([]string{"policy"}, args...)...)
	var result policyOut
	if err == nil {
		require.NoError(t, json.Unmarshal([]byte(out), &result), out)
	}
	return result, out, err
}

func changeFile(t *testing.T, policy evidencebook.DisclosurePolicy, changes string) string {
	t.Helper()
	digest, err := policy.Digest()
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{"profile": evidencebook.PolicyChangeProfile, "base_policy": digest, "round": policy.Round, "changes": json.RawMessage(changes), "then": []string{}})
	require.NoError(t, err)
	return writeJSON(t, string(body))
}

func TestBookPolicyAdoptShowAgreeWithDoctor(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	checkpointProfile(t, p)

	_, _, err := runPolicy(t, "", "--profile", "node", "show")
	require.Error(t, err, "a book with no policy shows none")
	_, _, err = runPolicy(t, "", "--profile", "node", "adopt")
	require.Error(t, err, "--holder is required")

	adopted, _, err := runPolicy(t, "", "--profile", "node", "adopt", "--holder", "node")
	require.NoError(t, err)
	assert.Equal(t, evidencebook.HolderNode, adopted.Policy.Holder)
	assert.Len(t, adopted.Records, 0)
	_, out, err := runPolicy(t, "", "--profile", "node", "adopt", "--holder", "node")
	require.Error(t, err, "a book keeps the policy it holds: %s", out)

	shown, _, err := runPolicy(t, "", "--profile", "node", "show")
	require.NoError(t, err)
	assert.Equal(t, adopted.Policy, shown.Policy)
	require.NotEmpty(t, shown.Settings)

	findings := runDoctor(t, "--profile", "node", "--probe", "--as", "signed")
	for _, row := range shown.Settings {
		f, ok := findings[evidencebook.DoctorSettingPrefix+row.Setting]
		require.True(t, ok, row.Setting)
		assert.Equal(t, evidencebook.DoctorStateAnswered, f.State)
		assert.Equal(t, row.Value+": "+row.Sends, f.Detail, row.Setting)
	}
	assert.Equal(t, evidencebook.DoctorStateMatch, findings[evidencebook.DoctorProbeHistoryCard].State)
}

func TestBookPolicyChangeThenDoctorFollowsPolicy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	checkpointProfile(t, p)
	adopted, _, err := runPolicy(t, "", "--profile", "node", "adopt", "--holder", "node")
	require.NoError(t, err)

	changed, out, err := runPolicy(t, "", "--profile", "node", "change", "--change", changeFile(t, adopted.Policy, `{"window":"all"}`))
	require.NoError(t, err, out)
	assert.Len(t, changed.Records, 1)
	assert.Equal(t, "all", changed.Policy.Window)

	shown, _, err := runPolicy(t, "", "--profile", "node", "show")
	require.NoError(t, err)
	assert.Equal(t, changed.Policy, shown.Policy)
	findings := runDoctor(t, "--profile", "node", "--probe", "--as", "signed")
	for _, row := range shown.Settings {
		assert.Equal(t, row.Value+": "+row.Sends, findings[evidencebook.DoctorSettingPrefix+row.Setting].Detail, row.Setting)
	}
	assert.Equal(t, evidencebook.DoctorStateMatch, findings[evidencebook.DoctorProbeHistoryCard].State)
}

func TestBookPolicyChangeRefusesStaleAndSealsNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	adopted, _, err := runPolicy(t, "", "--profile", "node", "adopt", "--holder", "node")
	require.NoError(t, err)
	stale := changeFile(t, adopted.Policy, `{"window":"1"}`)
	_, _, err = runPolicy(t, "", "--profile", "node", "change", "--change", changeFile(t, adopted.Policy, `{"window":"3"}`))
	require.NoError(t, err)

	size := bookSize(t, p)
	_, out, err := runPolicy(t, "", "--profile", "node", "change", "--change", stale)
	require.Error(t, err, out)
	assert.Contains(t, out+err.Error(), "stale")
	assert.Equal(t, size, bookSize(t, p), "a stale change seals nothing")

	// stdin works, and a refused field seals nothing either.
	body, err := json.Marshal(map[string]any{"profile": evidencebook.PolicyChangeProfile, "base_policy": "x", "round": 0, "changes": map[string]any{"window": "3"}, "then": []string{}})
	require.NoError(t, err)
	_, _, err = runPolicy(t, string(body), "--profile", "node", "change", "--change", "-")
	require.Error(t, err)
	assert.Equal(t, size, bookSize(t, p))
	_, _, err = runPolicy(t, `{"profile":"nope"}`, "--profile", "node", "change", "--change", "-")
	require.Error(t, err, "a wrong profile is refused")
}

func TestBookDoctorAsFlag(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "node")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	checkpointProfile(t, p)
	_, err := invoke(t, "", "doctor", "--profile", "node", "--as", "friend")
	require.Error(t, err)
	// capsulectl configures no standard_history source, so the signed-only
	// card has nothing to add here; both views probe the public questions.
	for _, as := range []string{"stranger", "signed"} {
		findings := runDoctor(t, "--profile", "node", "--probe", "--as", as)
		assert.Equal(t, evidencebook.DoctorStateMatch, findings[evidencebook.DoctorProbeCheckpoints].State, as)
		assert.Equal(t, evidencebook.DoctorStateMatch, findings[evidencebook.DoctorProbeHistoryCard].State, as)
	}
}
