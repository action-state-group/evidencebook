// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
)

// Doctor checks, one finding each, in the order Doctor reports them.
const (
	DoctorSelfAttested      = "self_attested"
	DoctorControllerBinding = "controller_binding"
	DoctorCheckpoint        = "checkpoint"
	DoctorWitnessed         = "witness"
	DoctorNotChecked        = "not_checked"
	DoctorUnsupported       = "unsupported"
	DoctorSharing           = "sharing"
	DoctorPolicyCommitment  = "policy_in_commitment"
	DoctorStrangerView      = "stranger_view"
	DoctorIdentifiedView    = "identified_view"
	DoctorProbeCheckpoints  = "probe_checkpoints"
	DoctorProbeHistoryCard  = "probe_history_card"
)

// Doctor finding states. NOT_CHECKED means what it means everywhere: the
// check was not run, never that it passed or failed.
const (
	DoctorStateSelfAttested = "self_attested"
	DoctorStateUnbound      = "unbound"
	DoctorStatePresent      = "present"
	DoctorStateMissing      = "missing"
	DoctorStateWitnessed    = "witnessed"
	DoctorStateUnwitnessed  = "unwitnessed"
	DoctorStateNotChecked   = "NOT_CHECKED"
	DoctorStateUnsupported  = "unsupported"
	DoctorStateOn           = "on"
	DoctorStateOff          = "off"
	DoctorStateAnswered     = "answered"
	DoctorStateMatch        = "match"
	DoctorStateMismatch     = "mismatch"
)

// DoctorFinding is one line of a doctor report. A report is a list of
// findings and nothing else: no score, rank or grade is computed from it.
type DoctorFinding struct {
	Check  string `json:"check"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	// Records names what shows the finding: record ids, a binding digest,
	// or an envelope's anchor.
	Records []string `json:"records,omitempty"`
}

// DoctorWitness is what the host knows of one witness delivery of this
// book's checkpoints: the witness, the checkpoint's tree size, and whether
// a receipt is held for it.
type DoctorWitness struct {
	Witness  string
	TreeSize uint64
	Receipt  bool
}

// DoctorTransport carries one request to this node's own responder and
// returns its reply, as a stranger would send and receive it. It must not
// authenticate the request: an authenticated request is sealed by Respond.
type DoctorTransport func(ctx context.Context, request Message) (Message, error)

// DoctorOptions is what Doctor reads besides the book.
type DoctorOptions struct {
	// Policy is the sharing policy this node advertises and answers under.
	Policy SharePolicy
	// ControllerBindings are controller binding records (controller-binding/0)
	// the host holds, as delivered.
	ControllerBindings []json.RawMessage
	// Witnesses are the host's witness deliveries; none means the book is
	// not delivered to any witness.
	Witnesses []DoctorWitness
	// Probe, when set, asks the node's own responder for checkpoints and
	// then history_card/1, as a stranger, and verifies both answers.
	Probe DoctorTransport
	// Keys are the responder keys this node advertises, which the probe
	// verifies under. Either left empty is this book's own key.
	Keys ResponderKeys
}

// Doctor inspects this book and returns a list of findings: what is
// self-attested, unwitnessed, NOT_CHECKED or unsupported; whether the
// signing key is bound to a controller; whether sharing is on and whether
// the policy is in a commitment; and what a stranger and an identified
// requester would receive. With opts.Probe it asks the node's own responder
// as a stranger and compares the answers with the advertised policy.
//
// Doctor seals nothing and never forces a checkpoint. The identified view
// is computed from the policy, not asked over the wire: Respond seals a
// record for every authenticated requester.
func (b *Book) Doctor(ctx context.Context, opts DoctorOptions) ([]DoctorFinding, error) {
	if err := opts.Policy.Validate(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	findings, latest, err := b.doctorLocked(ctx, opts)
	b.mu.Unlock()
	if err != nil || opts.Probe == nil {
		return findings, err
	}
	// The probe runs without the book's lock: the transport may reach this
	// same book's Respond.
	keys := opts.Keys
	if keys.Signer == "" {
		keys.Signer = KeyID(b.signer)
	}
	if keys.Checkpoint == "" && latest != nil {
		keys.Checkpoint = latest.KeyID
	}
	// The floor is one entry: any checkpoint meets it, so the responder is
	// never asked to issue one.
	one := uint64(1)
	coverage := Coverage{MinFreshness: &Freshness{Size: &one}}
	current := b.currentAnchor(ctx, latest)
	cards, err := probe(ctx, opts.Probe, keys, current, DoctorProbeCheckpoints, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: coverage})
	if err != nil {
		return nil, err
	}
	card, err := probe(ctx, opts.Probe, keys, current, DoctorProbeHistoryCard, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard, Coverage: coverage})
	if err != nil {
		return nil, err
	}
	return append(findings, cards, card), nil
}

// currentAnchor reports whether anchor is this book's latest checkpoint as
// of the snapshot, or a checkpoint cut after it: the book's lock is not held
// while the probe runs, so another writer may checkpoint meanwhile.
func (b *Book) currentAnchor(ctx context.Context, snapshot *Checkpoint) func(string) (bool, error) {
	return func(anchor string) (bool, error) {
		if snapshot == nil {
			return false, nil
		}
		if anchor == anchorDigest(*snapshot) {
			return true, nil
		}
		history, err := b.Checkpoints(ctx)
		if err != nil {
			return false, err
		}
		for _, cp := range history {
			if cp.Kind == SubstrateKindCLL && cp.TreeSize > snapshot.TreeSize && anchorDigest(cp) == anchor {
				return true, nil
			}
		}
		return false, nil
	}
}

func (b *Book) doctorLocked(ctx context.Context, opts DoctorOptions) ([]DoctorFinding, *Checkpoint, error) {
	view, err := b.viewLocked(ctx)
	if err != nil {
		return nil, nil, err
	}
	var latest *Checkpoint
	if n := len(view.checkpoints); n > 0 {
		latest = &view.checkpoints[n-1]
	}
	signer := KeyID(b.signer)
	findings := []DoctorFinding{{
		Check: DoctorSelfAttested, State: DoctorStateSelfAttested,
		Detail: fmt.Sprintf("each of the %d records is sealed by this book's own signer and by no other party; its content is this node's own statement", len(b.order)),
	}}
	findings = append(findings, controllerBinding(signer, opts.ControllerBindings))

	_, isCLL := b.commitments.(cllMaterialSource)
	switch {
	case !isCLL:
		findings = append(findings, DoctorFinding{Check: DoctorCheckpoint, State: DoctorStateMissing, Detail: "this book's substrate is not a Checkpointed Local Log, so it has no checkpoint a responder can anchor an answer to"})
	case latest == nil:
		findings = append(findings, DoctorFinding{Check: DoctorCheckpoint, State: DoctorStateMissing, Detail: "the book has no checkpoint, so a stranger's checkpoints and history_card/1 requests are refused: a stranger's request never issues one"})
	case latest.Entries < uint64(len(b.order)):
		findings = append(findings, DoctorFinding{Check: DoctorCheckpoint, State: DoctorStatePresent, Detail: fmt.Sprintf("the latest checkpoint covers %d of %d records; the rest are under no checkpoint yet", latest.Entries, len(b.order)), Records: []string{anchorDigest(*latest)}})
	default:
		findings = append(findings, DoctorFinding{Check: DoctorCheckpoint, State: DoctorStatePresent, Detail: fmt.Sprintf("the latest checkpoint covers all %d records", latest.Entries), Records: []string{anchorDigest(*latest)}})
	}
	findings = append(findings, witnessed(latest, opts.Witnesses))

	var notChecked []string
	var properties []string
	for _, id := range b.order {
		h := b.records[id].Header
		if h.RecordType != RecordTypeResponse {
			continue
		}
		var st ResponseStatement
		if json.Unmarshal(h.Statement, &st) == nil && len(st.NotChecked) > 0 {
			notChecked = append(notChecked, id)
			properties = append(properties, st.NotChecked...)
		}
	}
	slices.Sort(properties)
	if len(notChecked) > 0 {
		findings = append(findings, DoctorFinding{Check: DoctorNotChecked, State: DoctorStateNotChecked, Detail: fmt.Sprintf("%d received answers carry properties this node could not check: %s", len(notChecked), strings.Join(slices.Compact(properties), ", ")), Records: notChecked})
	} else {
		findings = append(findings, DoctorFinding{Check: DoctorNotChecked, State: DoctorStateNotChecked, Detail: "no received answer in this book carries an unchecked property"})
	}

	unsupported := "range, correlation and full_history are answered to no one except through a recipient-specific derivation; every derivation other than history_card/1"
	if registered := len(b.derivations); registered > 0 {
		unsupported += fmt.Sprintf(" and the %d registered here", registered)
	}
	unsupported += " is refused derivation_unsupported"
	if !isCLL {
		unsupported += "; this book's substrate is not a Checkpointed Local Log, so it serves no anchors and every request is refused"
	}
	findings = append(findings, DoctorFinding{Check: DoctorUnsupported, State: DoctorStateUnsupported, Detail: unsupported})

	p := opts.Policy
	switches := fmt.Sprintf("record_at_completion=%s, history_segments=%s, adjudications=%s", p.RecordAtCompletion, p.HistorySegments, p.Adjudications)
	if p.Witness != "" {
		switches += ", witness=" + p.Witness
	}
	sharing := DoctorStateOn
	if p.HistorySegments == "off" && p.RecordAtCompletion == "off" && p.Adjudications == "off" {
		sharing = DoctorStateOff
	}
	findings = append(findings,
		DoctorFinding{Check: DoctorSharing, State: sharing, Detail: switches},
		DoctorFinding{Check: DoctorPolicyCommitment, State: DoctorStateNotChecked, Detail: "this book publishes no commitment document, so whether the policy digest is in one is not checked"},
	)

	stranger := "checkpoints and history_card/1 (commitments only, no record); every other subject is refused not_authorized"
	switch {
	case !isCLL:
		stranger = "refusals only: this book serves no anchors, so every request is refused"
	case latest == nil:
		stranger = "refusals only: checkpoints and history_card/1 are public, but there is no checkpoint to answer with; every other subject is refused not_authorized"
	}
	findings = append(findings, DoctorFinding{Check: DoctorStrangerView, State: DoctorStateAnswered, Detail: stranger})
	identified := identifiedView(p, b.derivations)
	if !isCLL {
		identified = "refusals only: this book serves no anchors, so every request is refused"
	}
	findings = append(findings, DoctorFinding{Check: DoctorIdentifiedView, State: DoctorStateAnswered, Detail: identified})
	return findings, latest, nil
}

// identifiedView is what an authenticated requester that no record names
// as its counterparty receives, by the same gate Respond applies
// (gate.responderPolicy, SharePolicy.mayHave).
func identifiedView(p SharePolicy, derivations map[string]derivationEntry) string {
	var view string
	switch p.HistorySegments {
	case "peers":
		view = "what a stranger receives, plus the records and exchanges it asks for by digest"
	case "counterparties", "prospective":
		view = "what a stranger receives; record and exchange subjects are refused until a record names it as counterparty, and then it receives those records"
	default:
		view = "what a stranger receives; record and exchange subjects are refused (history_segments=off)"
	}
	granted := 0
	for _, entry := range derivations {
		if entry.def.Recipient != "" {
			granted++
		}
	}
	if granted > 0 {
		view += fmt.Sprintf("; %d recipient-specific derivations are answered to their named recipients only", granted)
	}
	return view
}

// controllerBinding reports whether a controller binding names the signing
// key, and which record shows it, never who holds either key.
func controllerBinding(signer string, bindings []json.RawMessage) DoctorFinding {
	var names []string
	for _, raw := range bindings {
		var record struct {
			Version string   `json:"binding_version"`
			Keys    []string `json:"keys"`
		}
		if json.Unmarshal(raw, &record) != nil || record.Version != "controller-binding/0" || !slices.Contains(record.Keys, signer) {
			continue
		}
		// The binding digest: SHA-256 of JCS of the record without sigs
		// (Controller Binding v0 §4).
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			continue
		}
		delete(fields, "sigs")
		body, err := canonical.JCS(fields)
		if err != nil {
			continue
		}
		names = append(names, digestBytes(body))
	}
	if len(names) == 0 {
		return DoctorFinding{Check: DoctorControllerBinding, State: DoctorStateUnbound, Detail: "no controller binding record names the signing key " + signer}
	}
	return DoctorFinding{
		Check: DoctorControllerBinding, State: DoctorStateNotChecked,
		Detail:  fmt.Sprintf("%d controller binding records name the signing key; their signatures are not checked, so the key is not shown bound", len(names)),
		Records: names,
	}
}

// witnessed reports whether the latest checkpoint has a witness receipt.
func witnessed(latest *Checkpoint, deliveries []DoctorWitness) DoctorFinding {
	f := DoctorFinding{Check: DoctorWitnessed, State: DoctorStateUnwitnessed}
	switch {
	case latest == nil:
		f.Detail = "there is no checkpoint to witness"
		return f
	case len(deliveries) == 0:
		f.Detail = "no witness is configured: no checkpoint is delivered to one"
		return f
	}
	var held []string
	for _, w := range deliveries {
		if w.TreeSize == latest.TreeSize && w.Receipt {
			held = append(held, w.Witness)
		}
	}
	if len(held) == 0 {
		f.Detail = fmt.Sprintf("no witness receipt is held for the latest checkpoint (tree size %d)", latest.TreeSize)
		return f
	}
	f.State = DoctorStateWitnessed
	f.Detail = fmt.Sprintf("a receipt is held for the latest checkpoint (tree size %d) from %s; doctor does not re-verify it", latest.TreeSize, strings.Join(held, ", "))
	return f
}

// probe sends one public request through the transport, verifies the reply
// under the advertised keys, and compares it with what the advertised
// policy answers a stranger: an artifact anchored at the latest checkpoint.
// It records nothing.
func probe(ctx context.Context, send DoctorTransport, keys ResponderKeys, current func(string) (bool, error), check string, req EvidenceRequest) (DoctorFinding, error) {
	f := DoctorFinding{Check: check, State: DoctorStateMismatch}
	asked := "checkpoints"
	if req.Derivation != "" {
		asked = req.Derivation
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return f, err
	}
	req.Profile, req.Nonce = ProfileID, hex.EncodeToString(nonce)
	data, err := EncodeRequest(req, MediaTypeJSON)
	if err != nil {
		return f, err
	}
	request := Message{Octets: data, MediaType: MediaTypeJSON}
	read := readRequest(request)
	if read.reason != "" {
		return f, fmt.Errorf("%w: the probe's own request: %s", ErrInvalid, read.reason)
	}
	sent := sentRequest{bytes: data, digest: digestBytes(data), request: read.request, floorTime: read.floorTime}
	reply, err := send(ctx, request)
	if err != nil {
		f.Detail = fmt.Sprintf("the responder did not answer %s: %v", asked, err)
		return f, nil
	}
	parsed, err := ParseReply(reply)
	if err != nil {
		f.Detail = fmt.Sprintf("the answer to %s is not a reply: %v", asked, err)
		return f, nil
	}
	if keys.Checkpoint == "" {
		keys.Checkpoint = keys.Signer // nothing to pin; every checkpoint then fails to verify
	}
	st, err := verifyReply(sent, parsed, keys)
	anchored := false
	if err == nil && st.Outcome == OutcomeArtifact {
		if anchored, err = current(st.Anchor); err != nil {
			return f, err
		}
	}
	switch {
	case err != nil:
		f.Detail = fmt.Sprintf("the answer to %s does not verify under the advertised responder key: %v", asked, err)
	case st.Outcome == OutcomeArtifactFailed:
		f.Detail = fmt.Sprintf("the artifact answering %s fails verification: %s", asked, st.Failure)
		f.Records = []string{st.Anchor}
	case st.Outcome != OutcomeArtifact:
		f.Detail = fmt.Sprintf("%s is refused %s, but the advertised policy answers it to anyone", asked, st.Reason)
	case !anchored:
		f.Detail = fmt.Sprintf("the artifact answering %s verifies, but its anchor is not this book's latest checkpoint", asked)
		f.Records = []string{st.Anchor}
	default:
		f.State = DoctorStateMatch
		f.Detail = fmt.Sprintf("%s is answered with an artifact that verifies, anchored at the latest checkpoint, as the advertised policy says", asked)
		f.Records = []string{st.Anchor}
	}
	return f, nil
}
