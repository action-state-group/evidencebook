package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readBundle(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var b map[string]any
	require.NoError(t, json.Unmarshal(raw, &b))
	return b
}

func shBlock(t *testing.T, b map[string]any) map[string]any {
	t.Helper()
	block, ok := b["extensions"].(map[string]any)[shExtension].(map[string]any)
	require.True(t, ok, "the bundle carries the x-standard-history/v0 block")
	return block
}

// No declaration, no card: the holder kind is never defaulted, and a card
// needs a checkpoint to be as of.
func TestNoCardWithoutADeclarationOrATick(t *testing.T) {
	dealFixture(t)
	out := filepath.Join(t.TempDir(), "card.json")
	_, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "card", "--out", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declare")

	_, err = invoke(t, "", "--profile", "deal", "deal", "standard-history", "declare")
	require.Error(t, err, "the kind is required")
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	_, err = invoke(t, "", "--profile", "deal", "deal", "standard-history", "card", "--out", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deal tick")
}

// The answer default follows the holder kind, a flag overrides it, and
// declaring again confirms: it never turns answering back on, and the kind
// is set once.
func TestTheAnswerDefaultFollowsTheHolderKind(t *testing.T) {
	dealFixture(t)
	node := dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	assert.Equal(t, true, node["answers"])
	assert.Contains(t, node["notice"], "--no-answer")
	assert.Equal(t, false, dealRun(t, "standard-history", "declare", "--holder-kind", "node", "--no-answer")["answers"])
	assert.Equal(t, false, dealRun(t, "standard-history", "declare", "--holder-kind", "node")["answers"], "declaring again keeps the opt-out")
	assert.Equal(t, true, dealRun(t, "standard-history", "declare", "--holder-kind", "node", "--answer")["answers"])
	_, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "declare", "--holder-kind", "person")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set once")
}

func TestAPersonDoesNotAnswerByDefault(t *testing.T) {
	dealFixture(t)
	person := dealRun(t, "standard-history", "declare", "--holder-kind", "person")
	assert.Equal(t, false, person["answers"])
	assert.NotContains(t, person, "notice")
	assert.Equal(t, true, dealRun(t, "standard-history", "declare", "--holder-kind", "person", "--answer")["answers"])
}

// The policy is its own file: the default is the last 3 months, its digest
// is the SHA-256 of the file's octets, and a change moves the digest.
func TestThePolicyDefaultAndDigest(t *testing.T) {
	dealFixture(t)
	first := dealRun(t, "standard-history", "policy")
	assert.Equal(t, map[string]any{"months": float64(3)}, first["window"])
	digest := first["policy_digest"].(string)
	assert.Len(t, digest, 64)
	set := dealRun(t, "standard-history", "policy", "--months", "6")
	assert.Equal(t, map[string]any{"months": float64(6)}, set["window"])
	assert.NotEqual(t, digest, set["policy_digest"])
	assert.Equal(t, map[string]any{"all": true}, dealRun(t, "standard-history", "policy", "--all")["window"])
	_, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "policy", "--months", "3", "--all")
	require.Error(t, err)
}

// A deal under the anchor is counted, with the amount its check rests on; the
// shared copy carries the card, policy and anchor but never the deal entries.
func TestTheCardCountsTheDealsUnderTheAnchor(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	id := retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])

	dir := t.TempDir()
	own := filepath.Join(dir, "own.json")
	dealRun(t, "standard-history", "card", "--out", own)
	shared := filepath.Join(dir, "shared.json")
	dealRun(t, "standard-history", "card", "--out", shared, "--shared")

	ownBlock := shBlock(t, readBundle(t, own))
	deals, ok := ownBlock["deals"].([]any)
	require.True(t, ok, "the own copy holds the deal entries")
	require.Len(t, deals, 1)
	entry := deals[0].(map[string]any)
	assert.Equal(t, id, entry["deal"])
	assert.Equal(t, "no_record", entry["happened"])
	assert.Equal(t, "undecided", entry["outcome"])
	assert.Contains(t, entry, "amount")

	sharedBlock := shBlock(t, readBundle(t, shared))
	assert.NotContains(t, sharedBlock, "deals", "no deal entry leaves in the shared copy")
	assert.Equal(t, ownBlock["policy"], sharedBlock["policy"])
	assert.Equal(t, ownBlock["as_of"], sharedBlock["as_of"])
	assert.Equal(t, map[string]any{"months": float64(3)}, ownBlock["policy"].(map[string]any)["window"])
	assert.Equal(t, standardhistory.Derivation, "standard_history/1")
}

// cardRequest is a standard_history/1 request over full_history in
// evidence-interop's own wire, freshness a log size of 1.
func cardRequest(t *testing.T, subject evidencerequest.Subject) string {
	t.Helper()
	size := uint64(1)
	octets, err := evidencerequest.EncodeRequest(evidencerequest.Request{
		Subject: subject, Derivation: standardhistory.Derivation, Profile: evidencerequest.ProfileID,
		Coverage: evidencerequest.Coverage{MinFreshness: &evidencerequest.Freshness{Size: &size}},
	}, evidencerequest.MediaTypeJSON)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(path, octets, 0o600))
	return path
}

func respondCard(t *testing.T, request string) evidencerequest.Reply {
	t.Helper()
	out := filepath.Join(t.TempDir(), "reply.json")
	dealRun(t, "respond", "--request", request, "--requester", "the asking party", "--output", out)
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	reply, err := evidencerequest.ParseReply(evidencerequest.Message{Octets: raw, MediaType: evidencerequest.MediaTypeJSON})
	if err != nil {
		t.Logf("reply: %s", raw)
	}
	require.NoError(t, err)
	return reply
}

func refusedWith(t *testing.T, reply evidencerequest.Reply) string {
	t.Helper()
	require.NotNil(t, reply.Refusal, "the reply is a refusal")
	require.NoError(t, reply.Refusal.Verify())
	return reply.Refusal.Reason
}

var fullHistory = evidencerequest.Subject{Kind: evidencerequest.SubjectFullHistory}

// A node answers by default: the signed envelope's artifact is the card, its
// anchor is the cadence checkpoint the bundle's as_of names, and the shared
// bundle's block carries the same anchor and policy.
func TestANodeAnswersTheCard(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])

	reply := respondCard(t, cardRequest(t, fullHistory))
	require.NotNil(t, reply.Envelope, "an envelope, not a refusal")
	require.NoError(t, reply.Envelope.Verify())
	assert.Equal(t, standardhistory.Derivation, reply.Envelope.Derivation)

	shared := filepath.Join(t.TempDir(), "shared.json")
	dealRun(t, "standard-history", "card", "--out", shared, "--shared")
	block := shBlock(t, readBundle(t, shared))
	assert.Equal(t, block["as_of"].(map[string]any)["anchor"], reply.Envelope.Anchor.Digest)
	var card map[string]any
	require.NoError(t, json.Unmarshal(reply.Envelope.Artifact, &card))
	assert.Equal(t, block["policy"].(map[string]any)["digest"], card["policy"])
	assert.Equal(t, "node", card["holder_kind"])
	assert.Equal(t, reply.Envelope.Anchor.Digest, card["anchor"])
}

// Who is answered is the holder's policy, and what is not a choice is not
// said as one: a person's default and a node's opt-out decline, an
// undeclared holder does not serve the derivation, no tick is no anchor, and a
// subject form the derivation is not registered for has no such subject,
// said only to a holder that answers.
func TestTheCardRefusalsAreTheRightTokens(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	request := cardRequest(t, fullHistory)
	wrong := cardRequest(t, evidencerequest.Subject{Kind: evidencerequest.SubjectCheckpoints})

	assert.Equal(t, evidencebook.ReasonDerivationUnsupported, refusedWith(t, respondCard(t, request)), "undeclared")

	dealRun(t, "standard-history", "declare", "--holder-kind", "node", "--no-answer")
	assert.Equal(t, evidencebook.ReasonPolicyDeclined, refusedWith(t, respondCard(t, request)), "a node that opted out")
	assert.Equal(t, evidencebook.ReasonPolicyDeclined, refusedWith(t, respondCard(t, wrong)), "a holder that declines does not say which subjects it serves")

	dealRun(t, "standard-history", "declare", "--holder-kind", "node", "--answer")
	assert.Equal(t, evidencebook.ReasonCoverageUnsatisfiable, refusedWith(t, respondCard(t, request)), "no finished tick yet")

	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	require.NotNil(t, respondCard(t, request).Envelope)
	assert.Equal(t, evidencebook.ReasonNoSuchSubject, refusedWith(t, respondCard(t, wrong)))
}

func TestAPersonWhoTurnedAnsweringOnIsAnswered(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "person")
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	request := cardRequest(t, fullHistory)
	assert.Equal(t, evidencebook.ReasonPolicyDeclined, refusedWith(t, respondCard(t, request)))
	dealRun(t, "standard-history", "declare", "--holder-kind", "person", "--answer")
	require.NotNil(t, respondCard(t, request).Envelope, "answering is not in the card, so no new tick is needed")
}

// The card is as of its anchor: a deal agreed after the tick is not on it, a
// pin to the older anchor answers that anchor's card whatever came since, and
// a change of policy needs a later tick before it is in a card.
func TestTheCardIsAsOfItsAnchor(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	retailDeal(t)
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	first := respondCard(t, cardRequest(t, fullHistory))
	require.NotNil(t, first.Envelope)
	pin := first.Envelope.Anchor.Digest

	*now = now.Add(3 * time.Hour)
	retailDeal(t)
	own := filepath.Join(t.TempDir(), "own.json")
	dealRun(t, "standard-history", "card", "--out", own)
	assert.Len(t, shBlock(t, readBundle(t, own))["deals"], 1, "a deal agreed after the tick is not on the card")

	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	own2 := filepath.Join(t.TempDir(), "own2.json")
	dealRun(t, "standard-history", "card", "--out", own2)
	deals := shBlock(t, readBundle(t, own2))["deals"].([]any)
	require.Len(t, deals, 2)
	assert.Less(t, deals[0].(map[string]any)["deal"], deals[1].(map[string]any)["deal"], "in deal id order")

	// A pin to the first anchor is answered with its own card: one deal.
	pinned := pinRequest(t, pin)
	reply := respondCard(t, pinned)
	require.NotNil(t, reply.Envelope)
	assert.Equal(t, pin, reply.Envelope.Anchor.Digest)
	var card map[string]any
	require.NoError(t, json.Unmarshal(reply.Envelope.Artifact, &card))
	assert.Equal(t, float64(1), card["in_window"].(map[string]any)["of"])
	assert.Equal(t, evidencebook.ReasonCoverageUnsatisfiable, refusedWith(t, respondCard(t, pinRequest(t, "00"+pin[2:]))), "a pin that names no anchor")

	// A policy change is in no card until a tick follows it.
	*now = now.Add(3 * time.Hour)
	dealRun(t, "standard-history", "policy", "--months", "6")
	_, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "card", "--out", filepath.Join(t.TempDir(), "x.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deal tick")
	assert.Equal(t, evidencebook.ReasonCoverageUnsatisfiable, refusedWith(t, respondCard(t, pinRequest(t, pin))))
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	later := respondCard(t, cardRequest(t, fullHistory))
	require.NotNil(t, later.Envelope)
	require.NoError(t, json.Unmarshal(later.Envelope.Artifact, &card))
	assert.Equal(t, map[string]any{"months": float64(6)}, card["window"])
}

func pinRequest(t *testing.T, pin string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pin.json")
	body := `{"subject":{"full_history":null},"coverage":{"expected_pin":"` + pin + `"},"derivation":"standard_history/1","profile":"evidence-interop/0"}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// Coverage of the request: a time floor is met by the anchor's own time and
// the envelope states it; both members, or none, is the request reader's
// refusal in an interop reply; a deadline already past is deadline_unmet.
func TestTheCardCoverageAndDeadline(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	retailDeal(t)
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	base := respondCard(t, cardRequest(t, fullHistory))
	require.NotNil(t, base.Envelope)
	pin := base.Envelope.Anchor.Digest

	write := func(body string) string {
		path := filepath.Join(t.TempDir(), "req.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		return path
	}
	head := `{"subject":{"full_history":null},"derivation":"standard_history/1","profile":"evidence-interop/0",`
	shared := filepath.Join(t.TempDir(), "shared.json")
	dealRun(t, "standard-history", "card", "--out", shared, "--shared")
	anchorTime := shBlock(t, readBundle(t, shared))["as_of"].(map[string]any)["anchor_time"].(string)

	met := respondCard(t, write(head+`"coverage":{"min_freshness":"`+anchorTime+`"}}`))
	require.NotNil(t, met.Envelope)
	assert.Equal(t, anchorTime, met.Envelope.Anchor.FreshnessTime)
	future := respondCard(t, write(head+`"coverage":{"min_freshness":"2030-01-01T00:00:00Z"}}`))
	assert.Equal(t, evidencebook.ReasonCoverageUnsatisfiable, refusedWith(t, future))

	both := respondCard(t, write(head+`"coverage":{"expected_pin":"`+pin+`","min_freshness":1}}`))
	assert.Equal(t, evidencebook.ReasonCoverageUnsatisfiable, refusedWith(t, both), "both members, in an interop reply")
	none := respondCard(t, write(head+`"coverage":{}}`))
	assert.Equal(t, evidencebook.ReasonCoverageUnsatisfiable, refusedWith(t, none))

	bad := respondCard(t, write(head+`"coverage":{"min_freshness":"not a time"}}`))
	assert.Equal(t, evidencebook.ReasonRequestMalformed, refusedWith(t, bad), "a malformed request is refused in an interop reply too")

	late := respondCard(t, write(head+`"deadline":"2020-01-01T00:00:00Z","coverage":{"expected_pin":"`+pin+`"}}`))
	assert.Equal(t, evidencebook.ReasonDeadlineUnmet, refusedWith(t, late))
}

// The settings are the holder's: a file another user can write is not
// trusted, a malformed policy is refused with what it must be, and the error
// names the fix.
func TestTheSettingsFilesAreProtectedAndChecked(t *testing.T) {
	dir := dealFixture(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	policy := filepath.Join(dir, shPolicyFile)
	require.NoError(t, os.WriteFile(policy, []byte(`{"months":0}`), 0o600))
	_, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "policy")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive whole number")
	require.NoError(t, os.WriteFile(policy, []byte(`{"months":3,"all":true}`), 0o600))
	_, err = invoke(t, "", "--profile", "deal", "deal", "standard-history", "policy")
	require.Error(t, err)

	require.NoError(t, os.WriteFile(policy, []byte(`{"months":3}`), 0o666))
	require.NoError(t, os.Chmod(policy, 0o666))
	_, err = invoke(t, "", "--profile", "deal", "deal", "standard-history", "policy")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chmod 600")
	require.NoError(t, os.Chmod(policy, 0o600))
	assert.Equal(t, map[string]any{"months": float64(3)}, dealRun(t, "standard-history", "policy")["window"])
}

// standardHistoryDir holds the golden bundles of a real run: a node's own
// copy and the copy it sends, with what `verify --bundle` printed for each.
// CAPSULECTL_UPDATE_STANDARD_HISTORY=1 rewrites them from a fresh run (keys and
// capsule ids are new each time); a normal run checks the committed files.
const standardHistoryDir = "../../testdata/standard-history"

// The golden bundles are VALID under `verify --bundle`, and each carries the
// block capsule-viewer's History module reads: exactly policy and as_of
// (as_of's three members), plus deals in the own copy only.
func TestTheStandardHistoryGoldens(t *testing.T) {
	if os.Getenv("CAPSULECTL_UPDATE_STANDARD_HISTORY") == "1" {
		dealFixture(t)
		now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
		retailDeal(t)
		dealRun(t, "standard-history", "declare", "--holder-kind", "node")
		*now = now.Add(3 * time.Hour)
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		require.NoError(t, os.MkdirAll(filepath.Join(standardHistoryDir, "verify"), 0o755))
		for name, extra := range map[string][]string{"node-own": nil, "node-shared": {"--shared"}} {
			path := filepath.Join(standardHistoryDir, name+".bundle.json")
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
			dealRun(t, append([]string{"standard-history", "card", "--out", path}, extra...)...)
			out, err := invoke(t, "", "verify", "--bundle", path)
			require.NoError(t, err, out)
			var v any
			require.NoError(t, json.Unmarshal([]byte(out), &v))
			pretty, err := json.MarshalIndent(v, "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(standardHistoryDir, "verify", name+".json"), append(pretty, '\n'), 0o644))
		}
	}
	for name, own := range map[string]bool{"node-own": true, "node-shared": false} {
		path := filepath.Join(standardHistoryDir, name+".bundle.json")
		result, err := verifyBundleOutput(t, path)
		require.NoError(t, err, name)
		assert.Equal(t, "VALID", result["verdict"], name)
		block := shBlock(t, readBundle(t, path))
		if own {
			assert.ElementsMatch(t, []string{"policy", "as_of", "deals"}, keysOf(block), name)
		} else {
			assert.ElementsMatch(t, []string{"policy", "as_of"}, keysOf(block), name)
		}
		assert.ElementsMatch(t, []string{"anchor", "anchor_time", "first_checkpoint_time"}, keysOf(block["as_of"].(map[string]any)), name+": no checkpoint or receipts member")
		policy, _ := block["policy"].(map[string]any)
		assert.Equal(t, map[string]any{"months": float64(3)}, policy["window"], name+": the default window")
		digest := sha256.Sum256([]byte(shDefaultPolicy))
		assert.Equal(t, hex.EncodeToString(digest[:]), policy["digest"], name+": the default policy's digest")
	}
}

// The policy's change time is written before the policy: a settings file the
// command refuses leaves the policy as it was, and the card is still built.
func TestAFailedPolicyChangeLeavesThePolicyAlone(t *testing.T) {
	dir := dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	settings := filepath.Join(dir, shSettingsFile)
	require.NoError(t, os.Chmod(settings, 0o644))
	_, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "policy", "--all")
	require.Error(t, err)
	require.NoError(t, os.Chmod(settings, 0o600))
	assert.Equal(t, map[string]any{"months": float64(3)}, dealRun(t, "standard-history", "policy")["window"])
	dealRun(t, "standard-history", "card", "--out", filepath.Join(t.TempDir(), "card.json"))
}

// A request whose member is a case variant of derivation asks for no derivation.
func TestADerivationMemberIsMatchedExactly(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	path := filepath.Join(t.TempDir(), "req.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"subject":{"full_history":null},"coverage":{"min_freshness":1},"Derivation":"standard_history/1","profile":"evidence-interop/0"}`), 0o600))
	out := filepath.Join(t.TempDir(), "reply.json")
	dealRun(t, "respond", "--request", path, "--requester", "x", "--output", out)
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "artifact_digest", "no card is signed for a request that names no derivation")
}

// The same policy again changes nothing, so a card as of an earlier tick is
// still built.
func TestTheSamePolicyAgainMakesNoAnchorStale(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	dealRun(t, "standard-history", "policy", "--months", "3")
	dealRun(t, "standard-history", "card", "--out", filepath.Join(t.TempDir(), "card.json"))
	dealRun(t, "standard-history", "policy", "--months", "6")
	_, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "card", "--out", filepath.Join(t.TempDir(), "again.json"))
	require.Error(t, err, "a different policy does")
}

// retailDealWithClass is retailDeal whose pay check states a counterparty
// class ("" states none).
func retailDealWithClass(t *testing.T, class string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(retailDemo, "check-pay.json"))
	require.NoError(t, err)
	var check map[string]any
	require.NoError(t, json.Unmarshal(raw, &check))
	if class != "" {
		check["counterparty_class"] = class
	}
	withClass, err := json.Marshal(check)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "check-pay.json")
	require.NoError(t, os.WriteFile(path, withClass, 0o600))
	dealID := dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))["deal_id"].(string)
	dealRun(t, "check", "--deal", dealID, "--input", path)
	dealRun(t, "note", "--deal", dealID, "--kind", "act", "--input", filepath.Join(retailDemo, "act-pay.json"))
	return dealID
}

// A pay check that states its counterparty class carries it into the deal's
// entry, and a shop_preauth deal is out of every count but in_window; a deal
// that states none is counted and in class_missing (SH §5.3, F4.5).
func TestTheStatedCounterpartyClassReachesTheCard(t *testing.T) {
	for class, want := range map[string]string{"shop_preauth": "shop_preauth", "business_agent": "business_agent", "": ""} {
		t.Run("class "+class, func(t *testing.T) { statedClassReachesTheCard(t, class, want) })
	}
}

func statedClassReachesTheCard(t *testing.T, class, want string) {
	{
		dealFixture(t)
		now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
		retailDealWithClass(t, class)
		dealRun(t, "standard-history", "declare", "--holder-kind", "node")
		*now = now.Add(2 * time.Minute)
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		own := filepath.Join(t.TempDir(), "own.json")
		dealRun(t, "standard-history", "card", "--out", own)
		entry := shBlock(t, readBundle(t, own))["deals"].([]any)[0].(map[string]any)
		if want == "" {
			assert.NotContains(t, entry, "counterparty_class", class)
		} else {
			assert.Equal(t, want, entry["counterparty_class"], class)
		}
		reply := respondCard(t, cardRequest(t, fullHistory))
		require.NotNil(t, reply.Envelope, class)
		var card map[string]any
		require.NoError(t, json.Unmarshal(reply.Envelope.Artifact, &card))
		inWindow := card["in_window"].(map[string]any)
		missing := card["class_missing"].(map[string]any)
		if class == "shop_preauth" {
			assert.Equal(t, map[string]any{"counted": float64(0), "not_counted": float64(1), "of": float64(1)}, inWindow)
			assert.Equal(t, map[string]any{"count": float64(0), "of": float64(0)}, missing)
		} else {
			assert.Equal(t, map[string]any{"counted": float64(1), "not_counted": float64(0), "of": float64(1)}, inWindow, class)
			wantMissing := float64(0)
			if class == "" {
				wantMissing = 1
			}
			assert.Equal(t, map[string]any{"count": wantMissing, "of": float64(1)}, missing, class)
		}
	}
}

// The class is the caller's statement on a pay check, one of SH's three
// values: anything else is refused before it is sealed.
func TestACounterpartyClassIsRefusedWhereTheProfileCannotCarryIt(t *testing.T) {
	dealFixture(t)
	dealID := dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))["deal_id"].(string)
	raw, err := os.ReadFile(filepath.Join(retailDemo, "check-pay.json"))
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		edit func(map[string]any)
		want string
	}{
		"another value": {func(c map[string]any) { c["counterparty_class"] = "shop" }, "must be one of"},
		"not a pay": {func(c map[string]any) {
			c["counterparty_class"] = "shop_preauth"
			c["action"] = "commit"
			delete(c, "authorized_max_minor")
		}, "pay check only"},
	} {
		var check map[string]any
		require.NoError(t, json.Unmarshal(raw, &check))
		tc.edit(check)
		text, err := json.Marshal(check)
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "check.json")
		require.NoError(t, os.WriteFile(path, text, 0o600))
		out, err := invoke(t, "", "--profile", "deal", "deal", "check", "--deal", dealID, "--input", path)
		require.Error(t, err, name)
		assert.Contains(t, err.Error()+out, tc.want, name)
	}
}

// A deal in the typed action records has no x-deal-v0 pay check to carry the
// class (the profile carries it on that record only), so it refuses a stated
// one rather than drop it.
func TestATypedDealRefusesACounterpartyClass(t *testing.T) {
	dealFixture(t)
	id := openTypedSticker(t)
	out, err := invoke(t, "", "--profile", "deal", "deal", "check", "--deal", id, "--input", writeJSON(t,
		`{"action":"pay","amount_minor":600,"authorized_max_minor":600,"counterparty_class":"shop_preauth","terms":{"item":"otter sticker","price_minor":600}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error()+out, "typed action records")
}

// Only one check of a deal states the class (two carriers read as none, SH
// §7): a re-check that states it again is refused, one that leaves it out is
// not, and the card still leaves the shop_preauth deal out of its counts.
func TestACounterpartyClassIsStatedOnceAcrossReChecks(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	raw, err := os.ReadFile(filepath.Join(retailDemo, "check-pay.json"))
	require.NoError(t, err)
	var check map[string]any
	require.NoError(t, json.Unmarshal(raw, &check))
	check["counterparty_class"] = "shop_preauth"
	withClass, err := json.Marshal(check)
	require.NoError(t, err)
	stated := filepath.Join(t.TempDir(), "stated.json")
	require.NoError(t, os.WriteFile(stated, withClass, 0o600))

	dealID := dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))["deal_id"].(string)
	dealRun(t, "check", "--deal", dealID, "--input", stated)
	out, err := invoke(t, "", "--profile", "deal", "deal", "check", "--deal", dealID, "--input", stated)
	require.Error(t, err)
	assert.Contains(t, err.Error()+out, "state it once")
	dealRun(t, "check", "--deal", dealID, "--input", filepath.Join(retailDemo, "check-pay.json"))
	dealRun(t, "note", "--deal", dealID, "--kind", "act", "--input", filepath.Join(retailDemo, "act-pay.json"))

	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	reply := respondCard(t, cardRequest(t, fullHistory))
	require.NotNil(t, reply.Envelope)
	var card map[string]any
	require.NoError(t, json.Unmarshal(reply.Envelope.Artifact, &card))
	assert.Equal(t, map[string]any{"counted": float64(0), "not_counted": float64(1), "of": float64(1)}, card["in_window"])
}
