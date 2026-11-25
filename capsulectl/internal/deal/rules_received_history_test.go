// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyHolderKey is the trusted holder key of the received-history fixtures
// (testdata/received-history/README.md).
const historyHolderKey = "27cb07b534232cb4f4b7b269a8436fafb17c19be41fbab20ac948d1c81b0a034"

const historyCommit = `{"action":"commit","terms":{"item":"lamp","price_minor":6000,"currency":"USD"}}`

func historyFixture(name string) string {
	return filepath.Join("testdata", "received-history", name)
}

// recordingChecker pins a checker that appends every input it is given to
// <path>.all, one per line, then runs body.
func recordingChecker(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CAPSULECTL_PLUGIN_ROOTS", root)
	path := filepath.Join(root, "checker")
	// The checker runs from a private copy, so the body names its input by
	// the original's path ($INPUT).
	body = strings.ReplaceAll(body, "$INPUT", path+".input")
	script := "#!/bin/sh\ncat > \"" + path + ".input\"\ncat \"" + path + ".input\" >> \"" + path + ".all\"\necho >> \"" + path + ".all\"\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	pinChecker(t, map[string]any{"command": []string{path}, "definition_digest": exampleRulesDigest})
	return path
}

// checkerInputs are the inputs a recordingChecker was given, in order.
func checkerInputs(t *testing.T, checker string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(checker + ".all")
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &m))
			out = append(out, m)
		}
	}
	require.NoError(t, sc.Err())
	return out
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	sum := sha256.Sum256(mustRead(t, path))
	return hex.EncodeToString(sum[:])
}

// Design §6 test 1, on an x-deal-v1 deal: a document that does not verify, or
// may not be read, refuses the whole check with its reason. No checker runs
// and nothing is sealed.
func TestReceivedHistoryRefusesWhatDoesNotVerify(t *testing.T) {
	otherKey := strings.Repeat("ab", 32)
	notAHistory := writeJSON(t, `{"history":"selection/1","deals":[]}`)
	card1 := writeJSON(t, `{"history":"history_card/1","request":{},"reply":{}}`)
	card2 := writeJSON(t, `{"history":"history_card/2","request":{},"reply":{}}`)
	commitment := writeJSON(t, `{"profile":"evidence-commitment/1"}`)
	malformed := writeJSON(t, `{"history":"personal_history_basic/1","definition":{}}`)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a History that fails a property":                {[]string{"--history", historyFixture("not-accepted.json"), "--counterparty-key", historyHolderKey}, "is not verified"},
		"a History answered under another key":           {[]string{"--history", historyFixture("accepted.json"), "--counterparty-key", otherKey}, "is not verified"},
		"a second document of one kind":                  {[]string{"--history", historyFixture("accepted.json"), "--history", historyFixture("accepted-2.json"), "--counterparty-key", historyHolderKey}, "is a second personal_history_basic/1"},
		"the same document twice":                        {[]string{"--history", historyFixture("accepted.json"), "--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey}, "the same document as an earlier --history"},
		"a document that is no History":                  {[]string{"--history", notAHistory, "--counterparty-key", historyHolderKey}, "is not a History this check reads"},
		"a malformed History":                            {[]string{"--history", malformed, "--counterparty-key", historyHolderKey}, "is not a one-file History"},
		"a history_card/1 staple":                        {[]string{"--history", card1, "--counterparty-key", historyHolderKey}, "read as history_card/2 only"},
		"a history_card/2 staple, until capsule-cli#567": {[]string{"--history", card2, "--counterparty-key", historyHolderKey}, "capsule-cli#567"},
		"a commitment staple, until capsule-cli#567":     {[]string{"--history", commitment, "--counterparty-key", historyHolderKey}, "capsule-cli#567"},
		"no key":                  {[]string{"--history", historyFixture("accepted.json")}, "needs --counterparty-key"},
		"a key and no History":    {[]string{"--counterparty-key", historyHolderKey}, "goes with --history"},
		"a key that is not a key": {[]string{"--history", historyFixture("accepted.json"), "--counterparty-key", "ABCD"}, "64 lowercase hex"},
	} {
		t.Run(name, func(t *testing.T) {
			h := openedV1(t)
			checker := recordingChecker(t, checkerPrints("allow", ""))
			before, _ := journalTypes(t, h.dealID)
			args := append([]string{"check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit)}, tc.args...)
			assert.Contains(t, v1Err(t, args...), tc.want)
			assert.Empty(t, checkerInputs(t, checker), "no checker runs")
			after, _ := journalTypes(t, h.dealID)
			assert.Equal(t, before, after, "nothing is sealed")
		})
	}
}

// --history goes with an x-deal-v1 deal's check and a pinned rules checker,
// refused before anything is sealed otherwise.
func TestReceivedHistoryNeedsAV1DealAndAChecker(t *testing.T) {
	args := []string{"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey}

	h := openedV1(t)
	assert.Contains(t, v1Err(t, append([]string{"check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit)}, args...)...), "needs a pinned rules checker")

	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, v0BuyerOpen))["deal_id"].(string)
	checker := recordingChecker(t, checkerPrints("allow", ""))
	assert.Contains(t, v1Err(t, append([]string{"check", "--deal", id, "--input", writeJSON(t, historyCommit)}, args...)...), "goes with an x-deal-v1 deal's check")
	assert.Empty(t, checkerInputs(t, checker))
}

// Design §6 test 3 (input side): a verified History reaches the checker whose
// verdict is sealed as received_history: about the counterparty, under the
// keys given, the document exactly as received with its SHA-256.
func TestReceivedHistoryReachesTheChecker(t *testing.T) {
	h := openedV1(t)
	checker := recordingChecker(t, checkerPrints("allow", ""))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	assert.Equal(t, "evaluated", out["rules"].(map[string]any)["status"])

	inputs := checkerInputs(t, checker)
	require.Len(t, inputs, 1, "one run, the sealed one")
	member, ok := inputs[0]["received_history"].(map[string]any)
	require.True(t, ok, "%v", inputs[0])
	assert.Equal(t, "counterparty", member["about"])
	assert.Equal(t, []any{historyHolderKey}, member["counterparty_keys"])
	histories := member["histories"].([]any)
	require.Len(t, histories, 1)
	doc := histories[0].(map[string]any)
	assert.Equal(t, "personal_history_basic/1", doc["kind"])
	assert.Equal(t, "BOUND", doc["request_binding"])
	assert.Equal(t, fileSHA256(t, historyFixture("accepted.json")), doc["sha256"])
	var want any
	require.NoError(t, json.Unmarshal(mustRead(t, historyFixture("accepted.json")), &want))
	assert.Equal(t, want, doc["document"], "the document as received")
}

// The member's input_sha256 binds the keys, statuses and documents: it changes
// with any of them, and is the SHA-256 of the member's JCS.
func TestReceivedHistoryInputDigestBindsTheMember(t *testing.T) {
	raw := mustRead(t, historyFixture("accepted.json"))
	base := &receivedHistory{About: "counterparty", CounterpartyKeys: []string{historyHolderKey},
		Histories: []receivedHistoryDoc{{Kind: "personal_history_basic/1", SHA256: fileSHA256(t, historyFixture("accepted.json")), RequestBinding: "BOUND", Document: raw}}}
	input := []byte(`{"schema":"external-check-input/v0","record":{}}`)
	extended, digest, err := withReceivedHistory(input, base)
	require.NoError(t, err)
	jcs, err := JCSOf(base)
	require.NoError(t, err)
	sum := sha256.Sum256(jcs)
	assert.Equal(t, hex.EncodeToString(sum[:]), digest)
	assert.Contains(t, string(extended), `"received_history":`)

	key := *base
	key.CounterpartyKeys = []string{historyHolderKey, strings.Repeat("cd", 32)}
	status := *base
	status.Histories = []receivedHistoryDoc{base.Histories[0]}
	status.Histories[0].RequestBinding = "NOT_PRESENT"
	for name, h := range map[string]*receivedHistory{"a key": &key, "a status": &status} {
		_, other, err := withReceivedHistory(input, h)
		require.NoError(t, err)
		assert.NotEqual(t, digest, other, "changing %s changes input_sha256", name)
	}
}

// Design §6 test 4 (the checker's words): a checker that refuses with the
// History on stderr is reported by its cause alone; none of the document
// reaches the output, the card or the sealed records.
func TestReceivedHistoryRefusalKeepsTheCauseAlone(t *testing.T) {
	marker := "\"definition\""
	h := openedV1(t)
	recordingChecker(t, `grep -o '"definition"' "$INPUT" >&2; exit 3`)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	rules := out["rules"].(map[string]any)
	assert.Equal(t, "not_evaluated", rules["status"])
	assert.Equal(t, "refused", rules["cause"])
	rawOut, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(rawOut), strings.Trim(marker, `"`), "no word of the History reaches the output")
	assert.NotContains(t, v1Stored(t, h.dealID), strings.Trim(marker, `"`), "nor the journal or the local events")
}

// v1Stored is a v1 deal's journal records and local events, as JSON text.
func v1Stored(t *testing.T, dealID string) string {
	t.Helper()
	var out []byte
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), dealID)
		require.NoError(t, err)
		events, _, _, err := s.v1StoredEvents(t.Context(), dealID)
		require.NoError(t, err)
		out, err = json.Marshal([]any{journal, events})
		require.NoError(t, err)
	})
	return string(out)
}

// Design §6 test 6: --received behaves as before beside --history. The notice
// run reads the input without received_history, and the sealed run reads no
// cards.
func TestReceivedHistoryLeavesTheCardsNoticeAsItWas(t *testing.T) {
	notice := `if grep -q received_cards "$INPUT"; then
  printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"example-rules/1.0.0","definition_digest":"` + exampleRulesDigest + `","verdict":"allow","findings":[],"notice":{"line":"read 2 cards"}}'
else ` + checkerPrints("allow", "") + `; fi`

	h := openedV1(t)
	checker := recordingChecker(t, notice)
	without := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit), "--received", cardsFile(t))
	plain := checkerInputs(t, checker)
	require.Len(t, plain, 2)
	require.NoError(t, os.Remove(checker+".all"))

	with := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit), "--received", cardsFile(t),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	assert.Equal(t, without["rules_notice"], with["rules_notice"])
	assert.Equal(t, "read 2 cards", with["rules_notice"])
	runs := checkerInputs(t, checker)
	require.Len(t, runs, 2)
	assert.Contains(t, runs[0], "received_history", "the sealed run reads the History")
	assert.NotContains(t, runs[0], "received_cards", "and no cards")
	assert.NotContains(t, runs[1], "received_history", "the notice run reads no History")
	assert.Equal(t, plain[1]["received_cards"], runs[1]["received_cards"], "the cards as before")
}
