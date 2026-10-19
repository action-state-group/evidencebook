// SPDX-License-Identifier: Apache-2.0

package acp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
	"github.com/action-state-group/evidencebook/adapter/internal/ijson"
)

// Wrapped entry types for ACP objects in a settlement-records leg. These are
// the names acp-v0 open point A-3 proposes. They are NOT registered in the
// Wrapped Object Types registry, and this adapter reads them only so that a
// leg can carry ACP octets with their digest while A-3 is open.
const (
	WrappedCheckoutSession = "acp.checkout-session"
	WrappedOrderEvent      = "acp.order-event"
	WrappedDelegatePayment = "acp.delegate-payment-response"
)

var wrappedKinds = map[string][]kind{
	WrappedCheckoutSession: {kindCheckoutSession, kindCheckoutSessionWithOrder},
	WrappedOrderEvent:      {kindOrderEvent},
	WrappedDelegatePayment: {kindDelegatePayment},
}

// legRoot reports whether raw is a settlement-records leg: one JSON object
// with a settlement member.
func legRoot(raw []byte) (ijson.Obj, bool) {
	v, err := ijson.Parse(raw)
	if err != nil {
		return nil, false
	}
	root, ok := ijson.AsObj(v)
	return root, ok && root.Has("settlement")
}

// unwrapLeg finds the leg's single wrapped ACP entry and checks its content
// against the digest the leg states for it. It returns the content octets
// (nil when there are none to read), the object kinds the entry type admits,
// and the octets_digest result. The relying party's pin is not read: the
// wrapping record carries the digest.
func unwrapLeg(root ijson.Obj) (input, []kind, adapter.Status, string) {
	in := input{carrier: "settlement-records leg wrapped content"}
	settlement, _ := root.Object("settlement")
	wrapped, ok := settlement["wrapped"].([]any)
	if !ok {
		return in, nil, adapter.Fail, "settlement.wrapped is missing or not an array"
	}
	var entries []ijson.Obj
	for _, e := range wrapped {
		if eo, ok := ijson.AsObj(e); ok {
			if t, _ := eo.Str("type"); wrappedKinds[t] != nil {
				entries = append(entries, eo)
			}
		}
	}
	names := slices.Sorted(maps.Keys(wrappedKinds))
	switch len(entries) {
	case 0:
		return in, nil, adapter.NotPresent, "no wrapped entry of type " + strings.Join(names, ", ") + " (types proposed by acp-v0 A-3, not registered)"
	case 1:
	default:
		return in, nil, adapter.Fail, fmt.Sprintf("%d wrapped ACP entries; which one is meant is ambiguous", len(entries))
	}
	e := entries[0]
	in.entryType, _ = e.Str("type")
	want := wrappedKinds[in.entryType]
	prefix := in.entryType + ": "
	if alg, _ := e.Str("digest_alg"); alg != "SHA-256" {
		return in, nil, adapter.NotChecked, prefix + fmt.Sprintf("digest_alg %q is not SHA-256", alg)
	}
	digest, _ := e.Str("digest")
	if !digestRE.MatchString(digest) {
		return in, nil, adapter.Fail, prefix + fmt.Sprintf("digest %q is not 64 lowercase hex digits", digest)
	}
	if !e.Has("content") {
		return in, nil, adapter.NotPresent, prefix + "the entry carries the digest only: possession of a digest, no octets to read"
	}
	content, ok := e.Str("content")
	if !ok {
		return in, nil, adapter.Fail, prefix + "content is not a string"
	}
	octets, err := base64.RawURLEncoding.Strict().DecodeString(content)
	if err != nil {
		return in, nil, adapter.Fail, prefix + "content is not base64url without padding: " + err.Error()
	}
	in.octets = octets
	sum := sha256.Sum256(octets)
	if got := hex.EncodeToString(sum[:]); got != digest {
		// The octets held are not the ones the leg commits to. They are
		// still read, and both results are reported.
		return in, want, adapter.Fail, prefix + fmt.Sprintf("wrapped_digest_mismatch: content hashes to %s, digest is %s", got, digest)
	}
	return in, want, adapter.Pass, prefix + fmt.Sprintf("%d octets, SHA-256 %s", len(octets), digest)
}
