// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
// The kinds read are a personal_history_basic/1 History (H3). A stapled
// history_card/2 (H4) waits for capsule-cli's library entry for the staple
// verifier (capsule-cli#567) and is refused until then; history_card/1 is
// not read (staples are history_card/2 only).

const (
	// receivedHistoryKind is the one History kind `--history` reads: a
	// personal_history_basic/1 History (H3), per Steven's u527 (1). Which
	// kind follows Steven's ruling on asg-agent-team-ethan#391, which may
	// supersede u527 (1); a switch changes this constant and the verifier
	// call in verifyReceivedHistory.
	receivedHistoryKind  = personalhistory.Name
	receivedHistoryAbout = "counterparty"
	// receivedHistoryBound is an H3's request_binding: each answer is bound
	// to the request octets in the file (PH §8.2). A staple that answers no
	// request of ours would carry NOT_PRESENT (design §5.2).
	receivedHistoryBound = "BOUND"
	historyCardStaple1   = "history_card/1"
	historyCardStaple2   = "history_card/2"
	evidenceCommitment   = "evidence-commitment/1"
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
	About            string               `json:"about"`
	CounterpartyKeys []string             `json:"counterparty_keys"`
	Histories        []receivedHistoryDoc `json:"histories"`
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
	switch {
	case len(paths) == 0 && len(keys) == 0:
		return nil, nil
	case len(paths) == 0:
		return nil, cli.InputError("--counterparty-key goes with --history: it is the key the counterparty's History is verified under")
	case len(keys) == 0:
		return nil, cli.InputError("--history needs --counterparty-key: the counterparty's key from your deployment policy, never one read from the History or from anything the counterparty sent")
	}
	for _, k := range keys {
		if len(k) != 64 || !cli.IsLowerHex(k) {
			return nil, cli.InputError("--counterparty-key is a raw Ed25519 public key as 64 lowercase hex characters")
		}
	}
	h := &receivedHistory{About: receivedHistoryAbout, CounterpartyKeys: keys, Histories: []receivedHistoryDoc{}}
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
		doc, err := verifyReceivedHistory(path, raw, keys)
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
	return h, nil
}

// verifyReceivedHistory verifies one document with capsule-cli's History
// verifier under keys, and returns it as the checker reads it. Every reason
// it refuses with is the verifier's, never text from the document.
func verifyReceivedHistory(path string, raw []byte, keys []string) (receivedHistoryDoc, error) {
	var head struct {
		History any `json:"history"`
		Profile any `json:"profile"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is not a JSON History document")
	}
	kind, _ := head.History.(string)
	if profile, ok := head.Profile.(string); ok && kind == "" {
		kind = profile
	}
	switch kind {
	case receivedHistoryKind:
	case historyCardStaple2, evidenceCommitment:
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is a stapled history card: this build cannot verify a stapled " + historyCardStaple2 + " yet (capsule-cli#567), so it is not read")
	case historyCardStaple1:
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is a " + historyCardStaple1 + " staple: a stapled card is read as " + historyCardStaple2 + " only")
	default:
		return receivedHistoryDoc{}, cli.InputError("--history " + path + " is not a History this check reads: want a " + receivedHistoryKind + " History")
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
