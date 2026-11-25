// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/personalhistory"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// A counterparty's History as a rules input (capsule-cli#327, design note
// 327-received-history.md §5; SEAMS ruling 7; D1 ruled (a)). `deal check
// --history FILE` hands the pinned rules checker the counterparty's History,
// verified and attributed before any checker runs: read-only, about this
// deal's counterparty alone, under the keys the host's deployment policy
// gives with --counterparty-key, never keys read from the document or from
// anything the counterparty sent with this deal.
//
// It is read on x-deal-v1 deals only; x-deal-v0 is frozen (evidencebook#626).
// The kinds read are a personal_history_basic/1 History (H3) and a stapled
// history_card/2 (H4), each verified with capsule-cli's own verifier
// (HistoryVerdict, StapleVerdict); history_card/1 is not read (staples are
// history_card/2 only).

const (
	// receivedHistoryKind is the History kind (H3) `--history` reads beside
	// a stapled history_card/2: a personal_history_basic/1 History, per
	// Steven's u527 (1). Which
	// kind follows Steven's ruling on asg-agent-team-ethan#391, which may
	// supersede u527 (1); a switch changes this constant and the verifier
	// call in verifyReceivedHistory.
	receivedHistoryKind  = personalhistory.Name
	receivedHistoryAbout = "counterparty"
	// receivedHistoryBound is an H3's request_binding: each answer is bound
	// to the request octets in the file (PH §8.2). A staple that answers no
	// request of ours would carry NOT_PRESENT (design §5.2).
	receivedHistoryBound = "BOUND"
	// receivedHistoryNotBound is a staple's request_binding when it checks
	// out but answers no request of ours (design §5.2).
	receivedHistoryNotBound = "NOT_PRESENT"
	historyCardStaple1      = "history_card/1"
	historyCardStaple2      = "history_card/2"
)

// receivedHistoryDoc is one verified document, as the checker reads it.
// SHA256 is over the file's bytes as received; Document is those bytes.
type receivedHistoryDoc struct {
	Kind           string          `json:"kind"`
	SHA256         string          `json:"sha256"`
	RequestBinding string          `json:"request_binding"`
	Document       json.RawMessage `json:"document"`
}

// receivedHistory is the rules-checker input member received_history
// (design §5.3): one counterparty, the keys it was verified under, and at
// most one document of each kind, in the order given.
type receivedHistory struct {
	About            string   `json:"about"`
	CounterpartyKeys []string `json:"counterparty_keys"`
	// CheckpointKeys are the keys a stapled card's checkpoints were verified
	// under (--checkpoint-key), absent when none was given: input_sha256
	// binds them with the rest, so the verdict says which key vouched for
	// the card, and a send's re-read must give the same ones.
	CheckpointKeys []string             `json:"checkpoint_keys,omitempty"`
	Histories      []receivedHistoryDoc `json:"histories"`
}

// historySHA256 lists each document's SHA-256, in input order.
func (h *receivedHistory) historySHA256() []string {
	out := make([]string, len(h.Histories))
	for i, d := range h.Histories {
		out[i] = d.SHA256
	}
	return out
}

// historyFlagsGiven is whether any of the --history flags was given.
func historyFlagsGiven(c *cobra.Command) bool {
	return c.Flags().Changed("history") || c.Flags().Changed("counterparty-key")
}

// readReceivedHistory reads `deal check --history FILE` with
// --counterparty-key, verifying every document before anything else runs. It
// returns nil when no --history was given. A document that is not accepted
// under the given keys, leaves a check not run, is of a kind not read here, or
// is a second of its kind (or the same bytes twice) refuses the whole check
// with its reason: no document is dropped, so the host never believes a
// History was read when it was not (design §5.2).
func readReceivedHistory(c *cobra.Command) (*receivedHistory, error) {
	paths, _ := c.Flags().GetStringArray("history")
	keys, _ := c.Flags().GetStringSlice("counterparty-key")
	checkpointKeys, _ := c.Flags().GetStringSlice("checkpoint-key")
	switch {
	case len(paths) == 0 && len(keys) == 0:
		return nil, nil
	case len(paths) == 0:
		return nil, cli.InputError("--counterparty-key goes with --history: it is the key the counterparty's History is verified under")
	case len(keys) == 0:
		return nil, cli.InputError("--history needs --counterparty-key: the counterparty's key from your deployment policy, never one read from the History or from anything the counterparty sent")
	}
	for _, k := range append(slices.Clone(keys), checkpointKeys...) {
		if len(k) != 64 || !cli.IsLowerHex(k) {
			return nil, cli.InputError("--counterparty-key and --checkpoint-key are raw Ed25519 public keys as 64 lowercase hex characters")
		}
	}
	h := &receivedHistory{About: receivedHistoryAbout, CounterpartyKeys: keys, CheckpointKeys: checkpointKeys, Histories: []receivedHistoryDoc{}}
	seen := map[string]bool{}
	kinds := map[string]bool{}
	for _, path := range paths {
		raw, err := cli.ReadInput(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		if seen[digest] {
			return nil, cli.InputError("--history " + path + " is the same document as an earlier --history")
		}
		seen[digest] = true
		doc, err := verifyReceivedHistory(path, raw, keys, checkpointKeys)
		if err != nil {
			return nil, err
		}
		if kinds[doc.Kind] {
			return nil, cli.InputError("--history " + path + " is a second " + doc.Kind + ": a check reads at most one document of each kind, so no rule counts the same deals twice")
		}
		kinds[doc.Kind] = true
		doc.SHA256 = digest
		h.Histories = append(h.Histories, doc)
	}
	if len(checkpointKeys) > 0 && !kinds[historyCardStaple2] {
		return nil, cli.InputError("--checkpoint-key goes with a stapled " + historyCardStaple2 + ": it is the key its checkpoints are signed by")
	}
	return h, nil
}

// verifyReceivedHistory verifies one document with capsule-cli's History
// verifier under keys, and returns it as the checker reads it. Every reason
// it refuses with is the verifier's, never text from the document.
func verifyReceivedHistory(path string, raw []byte, keys, checkpointKeys []string) (receivedHistoryDoc, error) {
	var head struct {
		History any `json:"history"`
		Profile any `json:"profile"`
	}
	if json.Unmarshal(raw, &head) != nil {
		head.History = nil
	}
	if kind, _ := head.History.(string); kind != receivedHistoryKind {
		// Anything else is read as a staple, as `verify --history` reads it
		// (the kind is the file's own, history before profile), or refused.
		return verifyReceivedStaple(path, raw, keys, checkpointKeys)
	}
	file, err := personalhistory.ParseFile(raw)
	if err != nil {
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is not a one-file History (" + receivedHistoryKind + ", PH §8.2): " +
			strings.TrimPrefix(err.Error(), personalhistory.ErrInvalid.Error()+": "))
	}
	report, err := cli.HistoryVerdict(file, keys)
	if errors.Is(err, cli.ErrInput) {
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " could not be verified: " + cli.SafeError(err))
	} else if err != nil {
		return receivedHistoryDoc{}, err
	}
	if report.Verdict != "ACCEPTED" {
		why := "it is not accepted under --counterparty-key"
		if len(report.Findings) > 0 {
			why += " (" + strings.Join(report.Findings, "; ") + ")"
		}
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is not verified: " + why + ". It is not attributable to this counterparty, so the check does not run")
	}
	document, err := jcsReadyDocument(raw)
	if err != nil {
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " cannot be handed to the rules checker: " + err.Error())
	}
	return receivedHistoryDoc{Kind: receivedHistoryKind, RequestBinding: receivedHistoryBound, Document: document}, nil
}

// verifyReceivedStaple verifies a stapled history_card/2 (H4) with
// capsule-cli's StapleVerdict under one counterparty key, the one the host's
// policy gives for this deal's counterparty, and the checkpoint keys. Design
// §5.2: accepted, or checking out but answering no request of ours
// (request_binding NOT_PRESENT, a commitment staples the reply octets only),
// reads as verified; a check left unrun, or a card not accepted, refuses
// the check. The staple must be read under exactly the key given (Sherlock2
// on capsule-cli#567: a staple under another key is not this counterparty's).
func verifyReceivedStaple(path string, raw []byte, keys, checkpointKeys []string) (receivedHistoryDoc, error) {
	notRead := cli.InputError("--history " + path + " is not a History this check reads: want a " + receivedHistoryKind + " History or a stapled " + historyCardStaple2)
	if len(keys) != 1 {
		if _, err := cli.StapleVerdict(raw, nil, nil); err != nil {
			return receivedHistoryDoc{}, notRead
		}
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is a stapled card: give exactly one --counterparty-key, this deal's counterparty's, so the card is read as theirs and no one else's")
	}
	result, err := cli.StapleVerdict(raw, keys, checkpointKeys)
	if errors.Is(err, cli.ErrInput) {
		return receivedHistoryDoc{}, notRead
	} else if err != nil {
		return receivedHistoryDoc{}, err
	}
	switch {
	case len(result.Derivations) != 1:
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " staples " + fmt.Sprint(len(result.Derivations)) + " cards: a check reads one stapled card")
	case result.Derivations[0] == historyCardStaple1:
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is a " + historyCardStaple1 + " staple: a stapled card is read as " + historyCardStaple2 + " only")
	case result.Derivations[0] == "" && (result.Outcome == nil || errors.Is(result.Outcome, cli.ErrPartial)):
		// No derivation and nothing failed: no card this check reads.
		return receivedHistoryDoc{}, notRead
	case result.Derivations[0] != historyCardStaple2 && result.Derivations[0] != "":
		return receivedHistoryDoc{}, notRead
	case result.CounterpartyKey != keys[0]:
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is not verified under --counterparty-key, so it is not attributable to this counterparty")
	}
	binding, err := stapleBinding(path, result)
	if err != nil {
		return receivedHistoryDoc{}, err
	}
	document, err := jcsReadyDocument(raw)
	if err != nil {
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " cannot be handed to the rules checker: " + err.Error())
	}
	return receivedHistoryDoc{Kind: historyCardStaple2, RequestBinding: binding, Document: document}, nil
}

// stapleBinding reads a staple's verdict as design §5.2 does: accepted is
// bound to our request (BOUND); checking out but answering no request of ours
// (a commitment staples the reply octets only) is verified with
// request_binding NOT_PRESENT, tested before ErrPartial, which it also
// matches; a check left unrun, or a card not accepted, refuses with the
// verifier's own reason.
func stapleBinding(path string, result cli.StapleResult) (string, error) {
	switch {
	case result.Outcome == nil:
		return receivedHistoryBound, nil
	case errors.Is(result.Outcome, cli.ErrHistoryAnswersNoRequest):
		return receivedHistoryNotBound, nil
	case errors.Is(result.Outcome, cli.ErrPartial):
		return "", cli.InputError("--history " + path + " is not verified: " + strings.Join(result.NotRun, ", ") + " was not checked" + checkpointHint(result.NotRun))
	}
	why := "it is not accepted under --counterparty-key"
	if len(result.Report.Findings) > 0 {
		why += " (" + strings.Join(result.Report.Findings, "; ") + ")"
	}
	return "", cli.InputError("--history " + path + " is not verified: " + why + ". It is not attributable to this counterparty, so the check does not run")
}

// checkpointHint names the flag a checkpoint_key left unchecked needs.
func checkpointHint(notRun []string) string {
	if slices.Contains(notRun, "checkpoint_key") {
		return " (give --checkpoint-key, the key its checkpoints are signed by, from your deployment policy)"
	}
	return ""
}

// jcsReadyDocument is the document's bytes exactly as received, once it is
// known that RFC 8785 can digest it (no float or unsafe integer), so the
// member's input_sha256 can be computed.
func jcsReadyDocument(raw []byte) (json.RawMessage, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := JCSOf(v); err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// withReceivedHistory is the rules-checker input with the received_history
// member added, and input_sha256, the SHA-256 of that member's JCS form as
// the checker receives it (design §5.4): the keys, statuses and documents the
// verdict depended on, bound without being disclosed.
func withReceivedHistory(input []byte, h *receivedHistory) ([]byte, string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(input, &doc); err != nil {
		return nil, "", err
	}
	member, err := json.Marshal(h)
	if err != nil {
		return nil, "", err
	}
	doc["received_history"] = member
	extended, err := json.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	// The digest is over the member as it sits in the bytes the checker
	// reads, read back from them.
	var sent struct {
		Member json.RawMessage `json:"received_history"`
	}
	if err := json.Unmarshal(extended, &sent); err != nil {
		return nil, "", err
	}
	dec := json.NewDecoder(strings.NewReader(string(sent.Member)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, "", err
	}
	jcs, err := JCSOf(v)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(jcs)
	return extended, hex.EncodeToString(sum[:]), nil
}

// boundHistory is the digests a run given h binds, or nil when the checker
// never started (it could not be run, or changed since it was pinned): a
// digest would then claim an input no run read.
func boundHistory(r dealRules, h *receivedHistory, inputSHA256 string) *xdealv1.ReceivedHistoryDigests {
	if r.Status == "not_evaluated" && (r.Cause == "checker_unavailable" || r.Cause == "checker_changed") {
		return nil
	}
	return &xdealv1.ReceivedHistoryDigests{HistorySHA256: h.historySHA256(), InputSHA256: inputSHA256}
}

// rulesGivenHistory is a run's rules block when its input held
// received_history: a run the checker refused, whose answer could not be
// read, or that reported another ruleset says its cause alone, never text the
// checker wrote (its stderr, its output, the ruleset id it reported), which
// may carry the History (design §5.4), as receivedNotice does for cards. The
// other causes' reasons are capsulectl's own words.
func rulesGivenHistory(r dealRules) dealRules {
	if r.Status != "not_evaluated" || (r.Cause != "refused" && r.Cause != "unreadable" && r.Cause != "ruleset_changed") {
		return r
	}
	r.Reason = "the rules checker did not evaluate the check given the counterparty's History (" + r.Cause + ")"
	if r.Cause == "ruleset_changed" {
		// What to do next is capsulectl's own words, not the checker's.
		r.Reason += rePinHint
	}
	return r
}

// runRulesGivenHistory runs the pinned checker on input with h added, as the
// run whose rules are sealed: the checker's words stay out (rulesGivenHistory),
// the History is bound when the checker started on it (boundHistory), and a
// deny is checked against a run without the History, so History alone never
// denies (D1 condition 1). A run without the History that does not answer
// cannot show the deny is not History's alone, so the check asks.
func runRulesGivenHistory(ctx context.Context, p cli.Profile, role string, input []byte, history *dealRulesHistory, h *receivedHistory) (dealRules, error) {
	checked, inputSHA256, err := withReceivedHistory(input, h)
	if err != nil {
		return dealRules{}, err
	}
	rules := rulesGivenHistory(runRulesChecker(ctx, p, role, checked, history))
	rules.ReceivedHistory = boundHistory(rules, h, inputSHA256)
	if rules.Status == "evaluated" && rules.Verdict == "deny" {
		without := runRulesChecker(ctx, p, role, input, history)
		rules.ReceivedHistory.AloneDeny = without.Status != "evaluated" || without.Verdict != "deny"
	}
	return rules, nil
}
