// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// The requester's stated limits on what it will check. A hostile responder
// can shape an answer to make checking it expensive: an oversized reply, a
// deeply nested tree, thousands of checkpoints whose signatures and proofs
// must each be checked, or a pile of witness receipts. Every limit is
// checked before the work it bounds, and exceeding one is reported naming
// it, never a crash or a timeout.
//
// Where a limit falls on octets nothing has authenticated yet (the reply's
// size and nesting, and the unsigned witness_receipts carrier), exceeding it
// refuses the reply and records nothing, so the request stays open: an
// unauthenticated message must not foreclose the real answer's outcome.
// Where it falls on what the responder signed (the artifact and its
// verification material), the envelope signature is checked first (one
// Ed25519 check over a bounded body) and exceeding the limit records the
// answer as received and failed, naming the limit, before any checkpoint
// signature, proof or receipt is checked.
const (
	// MaxReplyBytes bounds a reply's octets, and an Evidence Bundle's. It
	// is the most a record's originals may hold (capsule-emit's artifact
	// record limit), so a larger reply could never be recorded anyway.
	MaxReplyBytes = 8 << 20
	// MaxReplyDepth bounds how deeply a JSON reply, its artifact or a bundle
	// nests arrays and objects; the outermost value is depth 1. It is no
	// looser than the agent-action-capsule decoder's own bound.
	MaxReplyDepth = 32
	// MaxReplyItems bounds each list an answer carries: its verification
	// entries, and the artifact's checkpoints, history entries or records
	// (a bundle's records too). It is the responder's own default cap
	// (DefaultMaxAnswerItems).
	MaxReplyItems = DefaultMaxAnswerItems
	// MaxReplyReceipts bounds the witness receipts a reply carries in
	// witness_receipts, or a bundle's checkpoint in its witnesses member.
	MaxReplyReceipts = 64
)

// Witness standing of a verified artifact, recorded in its response
// statement: witnessed only when a receipt from a witness in the
// requester's own directory covers the anchor checkpoint. A chain that only
// verifies against its own signatures could be a private fork, and reads
// unwitnessed.
const (
	Witnessed   = "witnessed"
	Unwitnessed = "unwitnessed"
)

// WitnessReceiptsMember is the reply member that carries witness receipts
// beside the envelope: an array of {checkpoint, witness}, checkpoint the
// anchor digest a receipt is for and witness a checkpoint.witnesses entry.
// It is not part of the interop profile and nothing signs it; a receipt
// authenticates itself, so it can only ever add "witnessed".
const WitnessReceiptsMember = "witness_receipts"

// checkReplyOctets applies the limits on a reply's unauthenticated octets.
func checkReplyOctets(reply Message) error {
	if len(reply.Octets) > MaxReplyBytes {
		return fmt.Errorf("%w: limit: reply bytes: %d over %d", ErrInvalid, len(reply.Octets), MaxReplyBytes)
	}
	if reply.MediaType == MediaTypeJSON && jsonDepthOver(reply.Octets, MaxReplyDepth) {
		return fmt.Errorf("%w: limit: reply depth: over %d", ErrInvalid, MaxReplyDepth)
	}
	return nil
}

// jsonDepthOver reports whether JSON text nests arrays and objects deeper
// than limit. It scans the octets without decoding them, so it costs one
// pass whatever the input; text that is not JSON is left to the decoder.
func jsonDepthOver(data []byte, limit int) bool {
	depth, inString, escaped := 0, false, false
	for _, c := range data {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == '[' || c == '{':
			if depth++; depth > limit {
				return true
			}
		case c == ']' || c == '}':
			depth--
		}
	}
	return false
}

// checkEnvelopeLimits applies the limits on what the responder signed,
// before any check whose cost they bound.
func checkEnvelopeLimits(req EvidenceRequest, e Envelope) error {
	if n := len(e.Verification); n > MaxReplyItems {
		return fmt.Errorf("%w: limit: verification: %d over %d", ErrInvalid, n, MaxReplyItems)
	}
	// A bare record's artifact is the record's own octets, which are hashed
	// and never decoded.
	if req.Subject.Kind == SubjectRecord && req.Derivation == "" {
		return nil
	}
	if jsonDepthOver(e.Artifact, MaxReplyDepth) {
		return fmt.Errorf("%w: limit: artifact depth: over %d", ErrInvalid, MaxReplyDepth)
	}
	artifact, err := evidencerequest.DecodeJSON(e.Artifact)
	if err != nil {
		// Not JSON: the content checks report it.
		return nil
	}
	fields, _ := artifact.(map[string]any)
	for _, list := range []string{"checkpoints", "history", "records"} {
		if items, ok := fields[list].([]any); ok && len(items) > MaxReplyItems {
			return fmt.Errorf("%w: limit: %s: %d over %d", ErrInvalid, list, len(items), MaxReplyItems)
		}
	}
	return nil
}

// carriedReceipts reads the reply's witness_receipts member: the entries
// for anchor, as checkpoint.witnesses entries. Anything malformed in it is
// skipped, never an error, since nothing signs it; only its size is
// refused, before any receipt is read.
func carriedReceipts(reply Message, anchor string) ([]json.RawMessage, error) {
	root, err := evidencerequest.Decode(reply)
	if err != nil {
		return nil, nil
	}
	fields, _ := root.(map[string]any)
	list, _ := fields[WitnessReceiptsMember].([]any)
	if len(list) > MaxReplyReceipts {
		return nil, fmt.Errorf("%w: limit: witness receipts: %d over %d", ErrInvalid, len(list), MaxReplyReceipts)
	}
	var out []json.RawMessage
	for _, item := range list {
		entry, _ := item.(map[string]any)
		if digest, _ := entry["checkpoint"].(string); digest != anchor {
			continue
		}
		witnessed, ok := entry["witness"].(map[string]any)
		if !ok {
			continue
		}
		raw, err := json.Marshal(witnessed)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out, nil
}

// witnessStanding is Witnessed when at least one carried receipt covers the
// anchor's signed checkpoint statement under a witness in directory, and
// Unwitnessed otherwise: with no statement in hand (an answer kind whose
// material carries no anchor checkpoint), no receipt, or only receipts that
// are malformed, forged, for another checkpoint, or from a witness the
// requester does not list.
func witnessStanding(statement []byte, carried []json.RawMessage, directory []WitnessRow) string {
	if statement == nil {
		return Unwitnessed
	}
	for _, raw := range carried {
		receipt, url, err := decodeWitnessReceipt(raw)
		if err == nil && checkReceipt(statement, receipt, url, directory) == nil {
			return Witnessed
		}
	}
	return Unwitnessed
}
