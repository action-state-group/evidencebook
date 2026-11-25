// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
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
		"a History that fails a property":         {[]string{"--history", historyFixture("not-accepted.json"), "--counterparty-key", historyHolderKey}, "is not verified"},
		"a History answered under another key":    {[]string{"--history", historyFixture("accepted.json"), "--counterparty-key", otherKey}, "is not verified"},
		"a second document of one kind":           {[]string{"--history", historyFixture("accepted.json"), "--history", historyFixture("accepted-2.json"), "--counterparty-key", historyHolderKey}, "is a second personal_history_basic/1"},
		"the same document twice":                 {[]string{"--history", historyFixture("accepted.json"), "--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey}, "the same document as an earlier --history"},
		"a document that is no History":           {[]string{"--history", notAHistory, "--counterparty-key", historyHolderKey}, "is not a History this check reads"},
		"a malformed History":                     {[]string{"--history", malformed, "--counterparty-key", historyHolderKey}, "is not a one-file History"},
		"a history_card/1 file that is no staple": {[]string{"--history", card1, "--counterparty-key", historyHolderKey}, "is not a History this check reads"},
		"a history_card/2 file that is no staple": {[]string{"--history", card2, "--counterparty-key", historyHolderKey}, "is not a History this check reads"},
		"a commitment that staples nothing":       {[]string{"--history", commitment, "--counterparty-key", historyHolderKey}, "is not a History this check reads"},
		"a checkpoint key with no staple":         {[]string{"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey, "--checkpoint-key", historyHolderKey}, "--checkpoint-key goes with a stapled history_card/2"},
		"no key":                                  {[]string{"--history", historyFixture("accepted.json")}, "needs --counterparty-key"},
		"a key and no History":                    {[]string{"--counterparty-key", historyHolderKey}, "goes with --history"},
		"a key that is not a key":                 {[]string{"--history", historyFixture("accepted.json"), "--counterparty-key", "ABCD"}, "64 lowercase hex"},
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

// sealedVerdictHistory is the received_history member of the deal's last
// journal verdict, or nil.
func sealedVerdictHistory(t *testing.T, dealID string) *xdealv1.ReceivedHistoryDigests {
	t.Helper()
	var out *xdealv1.ReceivedHistoryDigests
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), dealID)
		require.NoError(t, err)
		for _, r := range journal {
			if b, ok := r.Body.(*xdealv1.VerdictBody); ok {
				out = b.ReceivedHistory
			}
		}
	})
	return out
}

// Design §6 test 3 (sealed side): the verdict whose rules read the History
// seals its SHA-256 and the member's input_sha256, the SHA-256 of the JCS of
// the member exactly as the checker received it, and nothing of the
// document or the keys. A check without --history seals none.
func TestReceivedHistorySealsItsDigestsOnly(t *testing.T) {
	h := openedV1(t)
	checker := recordingChecker(t, checkerPrints("allow", ""))
	dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit))
	assert.Nil(t, sealedVerdictHistory(t, h.dealID), "no History, no member")

	dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	got := sealedVerdictHistory(t, h.dealID)
	require.NotNil(t, got)
	assert.Equal(t, []string{fileSHA256(t, historyFixture("accepted.json"))}, got.HistorySHA256)

	raw, err := os.ReadFile(checker + ".input")
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var input map[string]any
	require.NoError(t, dec.Decode(&input))
	jcs, err := JCSOf(input["received_history"])
	require.NoError(t, err)
	sum := sha256.Sum256(jcs)
	assert.Equal(t, hex.EncodeToString(sum[:]), got.InputSHA256, "input_sha256 is over the member the checker read")

	stored := v1Stored(t, h.dealID)
	for _, leaked := range []string{"\"definition\"", "\"exchanges\"", historyHolderKey} {
		assert.NotContains(t, stored, leaked, "the journal and local events hold no History content or key")
	}
}

// A run that never started binds nothing: the checker changed since it was
// pinned, so no digest claims an input no run read.
func TestReceivedHistoryUnreadByAnyRunIsNotBound(t *testing.T) {
	h := openedV1(t)
	checker := recordingChecker(t, checkerPrints("allow", ""))
	require.NoError(t, os.WriteFile(checker, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	assert.Equal(t, "checker_changed", out["rules"].(map[string]any)["cause"])
	assert.Nil(t, sealedVerdictHistory(t, h.dealID))
}

// A refused run binds what it was given: its verdict may rest on the input.
func TestReceivedHistoryRefusedRunIsBound(t *testing.T) {
	h := openedV1(t)
	recordingChecker(t, "exit 3")
	dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	require.NotNil(t, sealedVerdictHistory(t, h.dealID))
}

// Design §6 test 4 (shares) and D1 condition 2: neither your own copy nor
// the counterparty's carries the verdict's received_history or its digests.
// An adjudicator's copy needs an agreement that names one; it carries deal
// book records, never a journal verdict, and the share rule refuses the
// member for it too (TestReceivedHistoryIsNeverShareable).
func TestReceivedHistoryIsInNoSharedCopy(t *testing.T) {
	dealID := noLayerAgreed(t)
	recordingChecker(t, checkerPrints("allow", ""))
	dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+strings.Repeat("ab", 32)+`"`)),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	bound := sealedVerdictHistory(t, dealID)
	require.NotNil(t, bound)
	for _, audience := range []string{"keep", "counterparty"} {
		file := filepath.Join(t.TempDir(), audience+".json")
		args := []string{"report", "--deal", dealID, "--share", audience, "--bundle", file}
		if audience != "keep" {
			args = append(args, "--to", "the other side")
		}
		dealRun(t, args...)
		copied := string(mustRead(t, file))
		assert.NotContains(t, copied, "received_history", audience)
		assert.NotContains(t, copied, bound.InputSHA256, audience)
		assert.NotContains(t, copied, bound.HistorySHA256[0], audience)
	}
}

// The share rule refuses a record holding received_history even when its
// only other strings are shareable: its two digests end in neither _digest
// nor _commitment, and the member is refused by name.
func TestReceivedHistoryIsNeverShareable(t *testing.T) {
	member := map[string]interface{}{"history_sha256": []interface{}{strings.Repeat("a", 64)}, "input_sha256": strings.Repeat("b", 64)}
	for _, audience := range []string{"counterparty", "adjudicator"} {
		assert.False(t, dealRecordShareable(map[string]interface{}{"result": "pass", "received_history": member}, "", audience, dealPrivate{}), audience)
		assert.True(t, dealRecordShareable(map[string]interface{}{"result": "pass"}, "", audience, dealPrivate{}), audience)
	}
	for _, k := range []string{"history_sha256", "input_sha256"} {
		assert.False(t, strings.HasSuffix(k, "_digest") || strings.HasSuffix(k, "_commitment") || dealShareKeys[k], k)
	}
}

// Given the History, the other causes whose reason would carry the checker's
// own text say the cause alone too: an answer that cannot be read, and one
// that reports another ruleset (whose id is the checker's word); the latter
// keeps capsulectl's re-pin hint.
func TestReceivedHistoryOtherCausesKeepTheCauseAlone(t *testing.T) {
	marker := "definition-of-theirs"
	for name, tc := range map[string]struct {
		body, cause string
	}{
		"an unreadable answer": {`printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"` + marker + `"}'`, "unreadable"},
		"another ruleset":      {`printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"` + marker + `","definition_digest":"` + strings.Repeat("cd", 32) + `","verdict":"allow","findings":[]}'`, "ruleset_changed"},
	} {
		t.Run(name, func(t *testing.T) {
			h := openedV1(t)
			recordingChecker(t, tc.body)
			out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
				"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
			rules := out["rules"].(map[string]any)
			assert.Equal(t, tc.cause, rules["cause"])
			if tc.cause == "ruleset_changed" {
				assert.Contains(t, rules["reason"], strings.TrimSpace(rePinHint), "the re-pin hint is capsulectl's own")
			}
			rawOut, err := json.Marshal(out)
			require.NoError(t, err)
			assert.NotContains(t, string(rawOut), marker)
			assert.NotContains(t, v1Stored(t, h.dealID), marker)
			require.NotNil(t, sealedVerdictHistory(t, h.dealID), "the checker started with the History: bound")
		})
	}
}

// An evaluated answer that cannot be sealed is downgraded to unreadable
// (sealableRules) and still binds the History the run read.
func TestReceivedHistoryStaysBoundThroughADowngrade(t *testing.T) {
	h := openedV1(t)
	recordingChecker(t, checkerPrints("allow", `{"id":"x","check":"mail ann@example.com","verdict":"pass"}`))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	assert.Equal(t, "unreadable", out["rules"].(map[string]any)["cause"])
	require.NotNil(t, sealedVerdictHistory(t, h.dealID))
}

// historyDeniesBody is a checker that denies only when handed the History,
// and allows otherwise.
var historyDeniesBody = `if grep -q received_history "$INPUT"; then ` + checkerPrints("deny", `{"id":"known-counterparty","check":"card/1","verdict":"fail","reason":"their History lists no deals"}`) + `; else ` + checkerPrints("allow", "") + `; fi`

// D1 condition 1: History alone never makes the verdict NO. A deny the
// checker gives only when handed the History asks instead, and the checker's
// own deny is sealed as given; a deny it gives without the History too
// stays a deny.
func TestReceivedHistoryAloneNeverDenies(t *testing.T) {
	h := openedV1(t)
	checker := recordingChecker(t, historyDeniesBody)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	assert.Equal(t, "pause", out["verdict"], "History alone asks, never denies")
	assert.Equal(t, "deny", out["rules"].(map[string]any)["verdict"], "the checker's own answer is kept")
	assert.Contains(t, differenceTexts(out), historyAloneText)
	runs := checkerInputs(t, checker)
	require.Len(t, runs, 2, "a deny is checked against a run without the History")
	assert.Contains(t, runs[0], "received_history")
	assert.NotContains(t, runs[1], "received_history")
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		for _, r := range journal {
			if b, ok := r.Body.(*xdealv1.VerdictBody); ok {
				assert.Equal(t, "pause", b.Result)
				require.NotNil(t, b.Rules)
				assert.Equal(t, "deny", b.Rules.Verdict, "the sealed rules are the checker's, re-checkable")
				require.NotNil(t, b.ReceivedHistory)
				assert.True(t, b.ReceivedHistory.AloneDeny, "the verdict says the deny was the History's alone")
			}
		}
	})

	h2 := openedV1(t)
	recordingChecker(t, checkerPrints("deny", denyFinding))
	out = dealRun(t, "check", "--deal", h2.dealID, "--input", writeJSON(t, historyCommit),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	assert.Equal(t, "deny", out["verdict"], "a deny without the History too is a deny")
	assert.NotContains(t, differenceTexts(out), historyAloneText)
}

// historyPayCheck is a buyer's passing pay check, on an agreed v1 deal,
// whose rules read the seller's History; it returns the deal and the check.
func historyPayCheck(t *testing.T, body string) (*handshakeV1, string) {
	t.Helper()
	h := agreedBoth(t, paymentOffer)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, `{"verbatim":"buy the lamp, at most 70 dollars","max_total_minor":7000}`), "--opening", writeJSON(t, v1Opening))
	recordingChecker(t, body)
	in := fmt.Sprintf(`{"action":"pay","amount_minor":6000,"authorized_max_minor":6000,"terms":{"item":"lamp","price_minor":6000,"currency":"USD"},"payment_ref":%q}`, v1LnHash)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, in),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)
	require.Equal(t, "pass", out["verdict"], "%v", out)
	var check string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check = checkDigest(t, journal)
	})
	return h, check
}

// The send re-runs a check that read the History on the same History
// (Sherlock2 on #897): without it, or with another History or key, the
// payment is refused and nothing is sent; with it, the re-run reads it.
func TestReceivedHistoryIsReadAgainAtTheSend(t *testing.T) {
	h, check := historyPayCheck(t, `if grep -q received_history "$INPUT"; then `+checkerPrints("allow", "")+`; else `+checkerPrints("deny", denyFinding)+`; fi`)
	sent := []string{"settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash"}
	assert.Contains(t, v1Err(t, sent...), "read the counterparty's History: give the same --history")
	assert.Contains(t, v1Err(t, append(sent, "--history", historyFixture("accepted-2.json"), "--counterparty-key", historyHolderKey)...), "is not the History check")
	assert.Contains(t, v1Err(t, append(sent, "--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey+","+strings.Repeat("cd", 32))...), "is not the History check")
	dealRun(t, append(sent, "--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey)...)
}

// A check that read no History takes none at the send, and --history goes
// with --sent only.
func TestReceivedHistoryAtTheSendNeedsACheckThatReadOne(t *testing.T) {
	h, check := buyerCheckedFor(t, paymentOffer, 6000, v1LnHash)
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash",
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey), "read none")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", filepath.Join(t.TempDir(), "p.json"),
		"--history", historyFixture("accepted.json"), "--counterparty-key", historyHolderKey), "--history goes with --sent")
}

// At the send, a deny History alone causes still asks: the pay that passed
// on the History is checked again, and the rules' answer then is held to
// the same condition (rulesChange reads the effective verdict).
func TestReceivedHistoryAloneNeverDeniesAtTheSend(t *testing.T) {
	orig := &dealRules{Status: "evaluated", Verdict: "allow"}
	now := dealRules{Status: "evaluated", Verdict: "deny", ReceivedHistory: &xdealv1.ReceivedHistoryDigests{AloneDeny: true}}
	refuse, verdict, _ := rulesChange(orig, now)
	assert.True(t, refuse, "an escalation the check did not have still stops the send")
	assert.Equal(t, "rules: escalate", verdict, "but as a question, never a deny")
	now.ReceivedHistory.AloneDeny = false
	_, verdict, _ = rulesChange(orig, now)
	assert.Equal(t, "denied by your rules", verdict)
}

// card2Vector is one of evidencebook's history_card/2 staple vectors
// (testdata/history-card-2/staples.json).
type card2Vector struct {
	Name    string `json:"name"`
	Holder  string `json:"holder"`
	Request string `json:"request"`
	Reply   string `json:"reply"`
	Pass    bool   `json:"pass"`
}

func card2Vectors(t *testing.T) map[string]card2Vector {
	t.Helper()
	var vs []card2Vector
	require.NoError(t, json.Unmarshal(mustRead(t, filepath.Join("..", "..", "..", "testdata", "history-card-2", "staples.json")), &vs))
	out := map[string]card2Vector{}
	for _, v := range vs {
		out[v.Name] = v
	}
	return out
}

// card2Staple writes the vector as the {history, request, reply} file a
// counterparty staples.
func card2Staple(t *testing.T, v card2Vector) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"history": "history_card/2",
		"request": map[string]any{"media_type": "application/json", "octets_text": v.Request},
		"reply":   map[string]any{"media_type": "application/json", "octets_text": v.Reply}})
	require.NoError(t, err)
	return writeJSON(t, string(raw))
}

// card2CheckpointKey is the key the card's last checkpoint is signed by.
func card2CheckpointKey(t *testing.T, reply string) string {
	t.Helper()
	rep, err := evidencerequest.ParseReply(evidencerequest.Message{Octets: []byte(reply), MediaType: evidencerequest.MediaTypeJSON})
	require.NoError(t, err)
	require.NotNil(t, rep.Envelope)
	decoded, err := evidencerequest.DecodeJSON(rep.Envelope.Artifact)
	require.NoError(t, err)
	history := decoded.(map[string]any)["history"].([]any)
	statement, err := hex.DecodeString(history[len(history)-1].(map[string]any)["checkpoint"].(string))
	require.NoError(t, err)
	signed, err := witness.ParseSignedCheckpoint(statement)
	require.NoError(t, err)
	return signed.KeyID
}

// A stapled history_card/2, verified under the counterparty's key and the
// checkpoint key, reaches the checker bound to the request it answers.
func TestReceivedHistoryReadsAStapledCard2(t *testing.T) {
	v := card2Vectors(t)["history_card/2"]
	require.True(t, v.Pass)
	path := card2Staple(t, v)
	h := openedV1(t)
	checker := recordingChecker(t, checkerPrints("allow", ""))
	dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", path, "--counterparty-key", v.Holder, "--checkpoint-key", card2CheckpointKey(t, v.Reply))
	inputs := checkerInputs(t, checker)
	require.Len(t, inputs, 1)
	doc := inputs[0]["received_history"].(map[string]any)["histories"].([]any)[0].(map[string]any)
	assert.Equal(t, "history_card/2", doc["kind"])
	assert.Equal(t, "BOUND", doc["request_binding"])
	assert.Equal(t, fileSHA256(t, path), doc["sha256"])
	require.NotNil(t, sealedVerdictHistory(t, h.dealID))
}

// Design §6 test 2: a staple that checks out but answers no request of ours
// (a commitment staples the reply octets only) is read as verified, with
// request_binding NOT_PRESENT; ErrHistoryAnswersNoRequest is read before
// ErrPartial, which it also matches. A check left unrun refuses, naming it,
// and a card not accepted refuses with the verifier's findings
// (TestReceivedHistoryReadsAPublishedCommitment runs the NOT_PRESENT case
// end to end).
func TestReceivedHistoryStapleOutcomes(t *testing.T) {
	binding, err := stapleBinding("f", cli.StapleResult{})
	require.NoError(t, err)
	assert.Equal(t, "BOUND", binding)
	binding, err = stapleBinding("f", cli.StapleResult{Outcome: cli.ErrHistoryAnswersNoRequest})
	require.NoError(t, err)
	assert.Equal(t, "NOT_PRESENT", binding)
	_, err = stapleBinding("f", cli.StapleResult{Outcome: cli.ErrPartial, NotRun: []string{"checkpoint_key"}})
	assert.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "checkpoint_key was not checked (give --checkpoint-key")
	_, err = stapleBinding("f", cli.StapleResult{Outcome: cli.ErrHistoryNotAccepted, Report: cli.FrameReport{Findings: []string{"staples[0]: envelope_signature FAIL"}}})
	assert.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "envelope_signature FAIL")
}

// Design §6 tests 1 and 2: a staple that is not verified under the
// counterparty's key (another holder's card, or the card read under another
// key), one whose checkpoint key was not given or is another, one given with
// several counterparty keys, and one whose checks fail, each refuses the
// check with its reason: no checker runs, nothing is sealed.
func TestReceivedHistoryRefusesAStapleThatDoesNotVerify(t *testing.T) {
	vs := card2Vectors(t)
	good := vs["history_card/2"]
	ck := card2CheckpointKey(t, good.Reply)
	other := strings.Repeat("ab", 32)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a card that is not the holder's": {[]string{"--history", card2Staple(t, vs["another holder"]), "--counterparty-key", vs["another holder"].Holder, "--checkpoint-key", ck}, "is not verified"},
		"a card read under another key":   {[]string{"--history", card2Staple(t, good), "--counterparty-key", other, "--checkpoint-key", ck}, "is not verified"},
		"no checkpoint key":               {[]string{"--history", card2Staple(t, good), "--counterparty-key", good.Holder}, "checkpoint_key was not checked"},
		"another checkpoint key":          {[]string{"--history", card2Staple(t, good), "--counterparty-key", good.Holder, "--checkpoint-key", other}, "is not verified"},
		"two counterparty keys":           {[]string{"--history", card2Staple(t, good), "--counterparty-key", good.Holder + "," + other, "--checkpoint-key", ck}, "exactly one --counterparty-key"},
		"a card that fails a check":       {[]string{"--history", card2Staple(t, vs["a window of 0"]), "--counterparty-key", good.Holder, "--checkpoint-key", ck}, "is not verified"},
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

// publishedCard2 is a holder node's own stapled history_card/2 and the
// evidence-commitment/1 its node publishes for it, made by the code a node
// runs: `deal standard-history staple` signs the card with the holder's
// profile key, and the core's `commitment publish` signs the commitment
// with it. It returns the {history, request, reply} staple, the commitment,
// the staple as the staple verb printed it, the holder's key and the card's
// checkpoint key.
func publishedCard2(t *testing.T) (staple, printed, commitment, holder, checkpointKey string) {
	t.Helper()
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	out, err := stapleRun(t)
	require.NoError(t, err, out)
	printed = filepath.Join(t.TempDir(), "staple.json")
	require.NoError(t, os.WriteFile(printed, []byte(out), 0o600))
	dir := t.TempDir()
	published, err := invoke(t, "", "commitment", "publish", "--profile", "deal", "--staple", printed,
		"--community", "acme-loop", "--binding", "acme/respond", "--derivation", "history_card/2", "--out", dir)
	require.NoError(t, err, published)
	var rep struct {
		NodeID string `json:"node_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(published), &rep), published)
	commitment = filepath.Join(dir, rep.NodeID+".json")
	require.FileExists(t, commitment)

	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	key, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	holder = hex.EncodeToString(key.Public().(ed25519.PublicKey))
	doc, _ := readStaple(t, out)
	raw, err := json.Marshal(map[string]any{"history": "history_card/2",
		"request": map[string]any{"media_type": "application/json", "octets_text": string(doc.Staple.Request)},
		"reply":   map[string]any{"media_type": "application/json", "octets_text": string(doc.Staple.Reply)}})
	require.NoError(t, err)
	staple = writeJSON(t, string(raw))
	return staple, printed, commitment, holder, card2CheckpointKey(t, string(doc.Staple.Reply))
}

// Design §6 test 2, end to end: the commitment a node publishes staples its
// card's reply octets only, so it answers no request of ours. Verified under
// the holder's key and the checkpoint key, it reaches the checker with
// request_binding NOT_PRESENT, and its digests are sealed; the same card
// stapled with its request reaches it BOUND.
func TestReceivedHistoryReadsAPublishedCommitment(t *testing.T) {
	staple, printed, commitment, holder, ck := publishedCard2(t)
	for name, tc := range map[string]struct{ path, binding string }{
		"the published commitment":    {commitment, "NOT_PRESENT"},
		"the staple with its request": {staple, "BOUND"},
		"the staple as printed":       {printed, "BOUND"},
	} {
		t.Run(name, func(t *testing.T) {
			h := openedV1(t)
			checker := recordingChecker(t, checkerPrints("allow", ""))
			dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
				"--history", tc.path, "--counterparty-key", holder, "--checkpoint-key", ck)
			inputs := checkerInputs(t, checker)
			require.Len(t, inputs, 1)
			doc := inputs[0]["received_history"].(map[string]any)["histories"].([]any)[0].(map[string]any)
			assert.Equal(t, "history_card/2", doc["kind"])
			assert.Equal(t, tc.binding, doc["request_binding"])
			assert.Equal(t, fileSHA256(t, tc.path), doc["sha256"])
			assert.Equal(t, []any{ck}, inputs[0]["received_history"].(map[string]any)["checkpoint_keys"], "the checkpoint key is bound with the rest")
			sealed := sealedVerdictHistory(t, h.dealID)
			require.NotNil(t, sealed)
			assert.Equal(t, []string{fileSHA256(t, tc.path)}, sealed.HistorySHA256)
		})
	}
	// The same commitment read under another key is not the holder's.
	h := openedV1(t)
	recordingChecker(t, checkerPrints("allow", ""))
	assert.Contains(t, v1Err(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", commitment, "--counterparty-key", strings.Repeat("ab", 32), "--checkpoint-key", ck), "is not verified")
}

// A card whose reply is no signed envelope is refused with the verifier's
// own findings, not as a document of another kind.
func TestReceivedHistoryBrokenCardSaysWhy(t *testing.T) {
	v := card2Vectors(t)["history_card/2"]
	broken := v
	broken.Reply = strings.Replace(v.Reply, `"sig":"`, `"sig":"00`, 1)
	require.NotEqual(t, v.Reply, broken.Reply)
	h := openedV1(t)
	checker := recordingChecker(t, checkerPrints("allow", ""))
	msg := v1Err(t, "check", "--deal", h.dealID, "--input", writeJSON(t, historyCommit),
		"--history", card2Staple(t, broken), "--counterparty-key", v.Holder, "--checkpoint-key", card2CheckpointKey(t, v.Reply))
	assert.Contains(t, msg, "is not verified")
	assert.NotContains(t, msg, "is not a History this check reads")
	assert.Empty(t, checkerInputs(t, checker))
}

// A send that rests on a check that read a stapled card re-reads the same
// staple under the same keys; another checkpoint key is refused, and
// nothing is sent.
func TestReceivedHistoryStapleIsReadAgainAtTheSend(t *testing.T) {
	staple, _, _, holder, ck := publishedCard2(t)
	h := agreedBoth(t, paymentOffer)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, `{"verbatim":"buy the lamp, at most 70 dollars","max_total_minor":7000}`), "--opening", writeJSON(t, v1Opening))
	recordingChecker(t, checkerPrints("allow", ""))
	in := fmt.Sprintf(`{"action":"pay","amount_minor":6000,"authorized_max_minor":6000,"terms":{"item":"lamp","price_minor":6000,"currency":"USD"},"payment_ref":%q}`, v1LnHash)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, in), "--history", staple, "--counterparty-key", holder, "--checkpoint-key", ck)
	require.Equal(t, "pass", out["verdict"], "%v", out)
	var check string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check = checkDigest(t, journal)
	})
	sent := []string{"settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash", "--history", staple, "--counterparty-key", holder}
	assert.Contains(t, v1Err(t, append(sent, "--checkpoint-key", strings.Repeat("ab", 32))...), "is not verified")
	assert.Contains(t, v1Err(t, sent...), "checkpoint_key was not checked")
	// The card verifies under this list too, but it is not the one the
	// check's verdict bound: refused by input_sha256.
	assert.Contains(t, v1Err(t, append(sent, "--checkpoint-key", ck+","+strings.Repeat("ab", 32))...), "is not the History check")
	dealRun(t, append(sent, "--checkpoint-key", ck)...)
}
