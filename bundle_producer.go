// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/registries"
)

// producerKeyKind is the extension that declares a bundle's producer key.
const producerKeyKind = "producer-key/v1"

// checkBundleRequest refuses, as ErrInvalid, every request input
// agent-action-capsule's producer would refuse, so that a refusal it still
// returns is about the book's data (ErrCorrupt), never the request. It
// normalizes req.Suppress (sorted, without repeats), the list the bundle and
// the disclosure record both state, and returns the caller's extensions
// decoded.
func checkBundleRequest(req *BundleRequest) (map[string]any, error) {
	if req.ClosureDepth < 0 || int64(req.ClosureDepth) > canonical.MaxSafeInteger {
		return nil, fmt.Errorf("%w: closure depth must be a non-negative safe integer", ErrInvalid)
	}
	if req.ProducerKey != nil && len(req.ProducerKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: producer key must be a raw %d-byte Ed25519 public key", ErrInvalid, ed25519.PublicKeySize)
	}
	suppress := make([]string, 0, len(req.Suppress))
	for _, member := range req.Suppress {
		if _, eligible := registries.DisclosureEligibleFields[member]; !eligible {
			return nil, fmt.Errorf("%w: cannot suppress %q: not a disclosure-eligible member (agent_input or agent_output)", ErrInvalid, member)
		}
		if !slices.Contains(suppress, member) {
			suppress = append(suppress, member)
		}
	}
	sort.Strings(suppress)
	req.Suppress = suppress
	decoded := make(map[string]any, len(req.Extensions))
	for kind, raw := range req.Extensions {
		switch kind {
		case producerKeyKind:
			return nil, fmt.Errorf("%w: set the producer key with BundleRequest.ProducerKey, not the %s extension", ErrInvalid, kind)
		case aacbundle.ComposedKind:
			return nil, fmt.Errorf("%w: the %s extension is agent-action-capsule's own", ErrInvalid, kind)
		}
		value, err := decodeJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: extension %s is not JSON: %v", ErrInvalid, kind, err)
		}
		if _, err := canonical.JCS(value); err != nil {
			return nil, fmt.Errorf("%w: extension %s has no canonical form: %v", ErrInvalid, kind, err)
		}
		decoded[kind] = value
	}
	// The producer's self-check refuses a bundle nested too deeply, so the
	// extensions are measured where the bundle puts them.
	if canonical.TooDeep(map[string]any{"extensions": decoded}) {
		return nil, fmt.Errorf("%w: extensions are nested more than %d levels deep in the bundle", ErrInvalid, canonical.MaxNestingDepth)
	}
	return decoded, nil
}

// revealsEveryMember enforces payloads=all on the disclosed records: each
// eligible, unsuppressed member a record's capsule commits must be revealed
// in the overlay. agent-action-capsule's producer refuses that only for a
// record with an overlay entry; the book refuses it for every disclosed
// record, naming what would make the request build.
func revealsEveryMember(disclosed []string, capsules map[string]json.RawMessage, overlay map[string]map[string]json.RawMessage, bound map[string]map[string][]string, suppress []string) error {
	members := make([]string, 0, len(registries.DisclosureEligibleFields))
	for member := range registries.DisclosureEligibleFields {
		members = append(members, member)
	}
	sort.Strings(members)
	for _, id := range disclosed {
		value, err := decodeJSON(capsules[id])
		if err != nil {
			return fmt.Errorf("%w: capsule %s: %v", ErrCorrupt, id, err)
		}
		capsule, _ := value.(map[string]any)
		for _, member := range members {
			if slices.Contains(suppress, member) {
				continue
			}
			if _, committed := committedDigest(capsule, registries.DisclosureEligibleFields[member]); !committed {
				continue
			}
			if _, revealed := overlay[id][member]; revealed {
				continue
			}
			// An original bound to the member that is still unrevealed is not
			// JSON: suppressing the member would withhold it, which all
			// refuses. With no original bound, suppressing withholds nothing.
			advice := fmt.Sprintf("use payloads=selected, or suppress %s", member)
			for _, names := range bound[id] {
				if slices.Contains(names, member) {
					advice = "its original is not JSON, so it cannot be revealed as that member: use payloads=selected"
				}
			}
			return fmt.Errorf("%w: payloads=all but record %s commits %s, which the bundle does not reveal; %s", ErrInvalid, id, member, advice)
		}
	}
	return nil
}

// committedDigest reads the string at a dotted path of a capsule.
func committedDigest(capsule map[string]any, path string) (string, bool) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := capsule[part].(map[string]any)
		if !ok {
			return "", false
		}
		capsule = next
	}
	digest, ok := capsule[parts[len(parts)-1]].(string)
	return digest, ok
}

// produceLocked builds the bundle with agent-action-capsule's producer over
// snapshot, carrying the overlay, the extensions and the producer report the
// book assembled. It returns the bundle's canonical bytes.
func (b *Book) produceLocked(ctx context.Context, snapshot *proofSnapshot, req BundleRequest, first uint64, wire bundleWire, callerExtensions map[string]any) ([]byte, error) {
	disclosures := make(map[string]map[string]any, len(wire.Disclosures))
	for id, members := range wire.Disclosures {
		disclosures[id] = make(map[string]any, len(members))
		for member, raw := range members {
			value, err := decodeJSON(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: disclosed %s of %s: %v", ErrCorrupt, member, id, err)
			}
			disclosures[id][member] = value
		}
	}
	var extensions map[string]any
	for kind, raw := range wire.Extensions {
		if extensions == nil {
			extensions = make(map[string]any, len(wire.Extensions))
		}
		if value, ok := callerExtensions[kind]; ok {
			extensions[kind] = value
			continue
		}
		value, err := decodeJSON(raw)
		if err != nil {
			return nil, err
		}
		extensions[kind] = value
	}
	checks := make([]any, len(wire.Verification.Checks))
	for i, check := range wire.Verification.Checks {
		checks[i] = check
	}
	depth := req.ClosureDepth
	built, err := aacbundle.BuildEvidenceBundle(ctx, bookProofSource{snapshot: snapshot, store: b.store}, aacbundle.BuildOptions{
		Root: req.Root, IntervalFrom: first, ClosureDepth: &depth, Include: req.Include,
		Disclosures: disclosures, SuppressedFields: req.Suppress, PayloadsMode: string(req.Payloads),
		ProducerKey: req.ProducerKey, Extensions: extensions,
		Verification: map[string]any{"producer": wire.Verification.Producer, "checks": checks},
	})
	var refused *aacbundle.BuildError
	if errors.As(err, &refused) {
		// The request was checked first (checkBundleRequest), so a refusal
		// here is about what the book holds.
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, refused)
	}
	if err != nil {
		return nil, err
	}
	return canonical.JCS(built)
}

// bookProofSource is the book's log at one checkpoint as
// agent-action-capsule's producer reads it: the snapshot's checkpoint, leaves
// and proofs, and the stored record (capsule and Producer Envelope) for each
// record id.
type bookProofSource struct {
	snapshot *proofSnapshot
	store    Store
}

func (s bookProofSource) Checkpoint(context.Context) ([]byte, error) {
	return append([]byte(nil), s.snapshot.at.Statement...), nil
}

func (s bookProofSource) Leaves(context.Context) ([][]byte, error) {
	out := make([][]byte, len(s.snapshot.leaves))
	for i, leaf := range s.snapshot.leaves {
		out[i] = append([]byte(nil), leaf...)
	}
	return out, nil
}

func (s bookProofSource) Record(ctx context.Context, capsuleID string) (aacbundle.StoredRecord, bool, error) {
	stored, err := s.store.GetRecord(ctx, capsuleID)
	if errors.Is(err, ErrNotFound) {
		return aacbundle.StoredRecord{}, false, nil
	}
	if err != nil {
		return aacbundle.StoredRecord{}, false, err
	}
	return aacbundle.StoredRecord{Capsule: stored.Capsule, ProducerEnvelope: stored.Envelope}, true, nil
}

func (s bookProofSource) InclusionProof(_ context.Context, seq uint64) (aacbundle.InclusionProof, error) {
	return s.snapshot.inclusionProof(seq)
}

func (s bookProofSource) RangeProof(_ context.Context, firstSeq uint64) (aacbundle.RangeProof, error) {
	return s.snapshot.rangeProof(firstSeq)
}
