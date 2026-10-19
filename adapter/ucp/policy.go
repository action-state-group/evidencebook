// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"crypto"
	"errors"
	"fmt"
	"slices"

	"github.com/action-state-group/evidencebook/adapter"
)

// Trust policy names this adapter reads (adapter.TrustPolicy is shared data).
const (
	// KeySetName names, under the trust policy's key_sets, the relying
	// party's retained copy of the business profile's keys[]: a JWK Set
	// ({"keys": [...]}) or a bare array. The adapter never fetches
	// /.well-known/ucp.
	KeySetName = "ucp.keys"
	// PinKeysOrigin is the business origin (https://host[:port]) whose
	// /.well-known/ucp served that copy. For a checkout it is also the origin
	// the relying party's policy is asked about: the checkout does not name
	// its business.
	PinKeysOrigin = "ucp.keys.origin"
	// PinKeysRetainedAt is when that copy was taken (RFC 3339). It is named
	// in the key row's method; without it the method says the time is not
	// stated.
	PinKeysRetainedAt = "ucp.keys.retained_at"
	// PinKeysSource names where the retained key set came from (for
	// example a file name or the profile URL), in the key row's method:
	// "retained keys[] (<source>)". Without it the source is the profile URL
	// of ucp.keys.origin, or the trust policy itself.
	PinKeysSource = "ucp.keys.source"
	// PinOperation names the UCP operation whose response a checkout is
	// (create, read, update, complete, cancel), for the schema check's
	// method. Without it the operation is inferred from status.
	PinOperation = "ucp.operation"
	// KeyBusinessOrigin lists, under the trust policy's keys, the business
	// origins the relying party accepts as the merchant it means.
	KeyBusinessOrigin = "ucp.business_origin"
	// KeyBusinessOriginRejected lists the business origins the relying
	// party rejects.
	KeyBusinessOriginRejected = "ucp.business_origin.rejected"
)

// rows accumulates check results in declaration order. Each is keyed by its
// check name while the object is checked; asReports turns them into the
// reports Check returns.
type rows struct{ out []adapter.PropertyResult }

func newRows() *rows { return &rows{} }

func (r *rows) add(prop string, st adapter.Status, method, detail string) {
	r.out = append(r.out, adapter.PropertyResult{Property: prop, Status: st, Method: method, Detail: detail})
}

// businessTrust appends ucp.business_trust: does the relying party's policy
// accept origin as the business it means? It is reported beside
// native_check, never inside it.
func (r *rows) businessTrust(policy adapter.TrustPolicy, origin string) {
	id := policy.Pin(adapter.PinPolicyID)
	if id == "" {
		id = "(unnamed)"
	}
	method := "relying party policy " + id
	accepted, rejected := policy.Keys[KeyBusinessOrigin], policy.Keys[KeyBusinessOriginRejected]
	switch {
	case len(accepted) == 0 && len(rejected) == 0:
		r.add(PropBusinessTrust, adapter.NotChecked, method, "no business-origin policy was supplied")
	case origin == "":
		r.add(PropBusinessTrust, adapter.Inconclusive, method, "the business origin is not known: the object does not name it and the policy gives no "+PinKeysOrigin)
	case slices.Contains(rejected, origin):
		r.add(PropBusinessTrust, adapter.Fail, method, origin+" is an origin the policy rejects")
	case slices.Contains(accepted, origin):
		r.add(PropBusinessTrust, adapter.Pass, method, origin)
	default:
		r.add(PropBusinessTrust, adapter.Inconclusive, method, origin+" is not listed by the policy")
	}
}

// retainedMethod is the key row's method for a retained key set (ucp-v0
// §3(e)): "retained keys[] (<key-set source>)", with " as of <time>" when the
// retention time is known.
func retainedMethod(policy adapter.TrustPolicy) string {
	src := policy.Pin(PinKeysSource)
	if src == "" {
		if o := policy.Pin(PinKeysOrigin); o != "" {
			src = o + "/.well-known/ucp"
		} else {
			src = "trust policy " + KeySetName
		}
	}
	m := "retained keys[] (" + src + ")"
	if at := policy.Pin(PinKeysRetainedAt); at != "" {
		m += " as of " + at
	}
	return m
}

// resolveKey reports the key row (§3(e) row 5, §4(e) row 5) for kid and
// returns the key when the row is PASS. signerOrigin, when not empty, is the
// origin the object names as its signer; a key set retained for another
// origin is not the signer's.
func resolveKey(policy adapter.TrustPolicy, kid, signerOrigin, prop string) (crypto.PublicKey, adapter.PropertyResult) {
	row := adapter.PropertyResult{Property: prop}
	text := policy.KeySets[KeySetName]
	if len(text) == 0 {
		row.Status, row.Method, row.Detail = adapter.NotChecked, methodKeyLive, "no key set was retained, and the verifier is offline"
		return nil, row
	}
	row.Method = retainedMethod(policy)
	if o := policy.Pin(PinKeysOrigin); signerOrigin != "" && o != "" && o != signerOrigin {
		row.Status, row.Detail = adapter.NotChecked, fmt.Sprintf("the retained key set is %s's; the signer names %s, for which no key set was retained", o, signerOrigin)
		return nil, row
	}
	ks, err := parseKeySet(text)
	if err != nil {
		row.Status, row.Detail = adapter.NotChecked, "the trust policy's "+KeySetName+" is unreadable: "+err.Error()
		return nil, row
	}
	k, ok, skipped := ks.lookup(kid)
	if !ok {
		row.Status, row.Detail = adapter.Fail, "kid "+quote(kid)+" is not listed among the keys usable for verification"
		if skipped != "" {
			row.Detail += " (a key with that kid is " + skipped + ")"
		}
		return nil, row
	}
	pub, err := k.publicKey()
	switch {
	case errors.Is(err, errUnsupportedKey):
		row.Status, row.Detail = adapter.NotChecked, "kid "+quote(kid)+": "+err.Error()
		return nil, row
	case err != nil:
		row.Status, row.Detail = adapter.Fail, "kid "+quote(kid)+" is not a usable key: "+err.Error()
		return nil, row
	}
	row.Status, row.Detail = adapter.Pass, "kid "+quote(kid)
	return pub, row
}

// asReports returns the results as reports. Every native check is a
// native_check report whose detail is {"check": <name>, "object": <object>}
// (adapter.NativeCheckReport; ruling S2 and the adapter declarations).
// ucp.business_trust is not a native check and keeps its own property, as
// responder_trust does beside the envelope signature (profile §7.2).
func asReports(object string, rs []adapter.PropertyResult) []adapter.PropertyResult {
	out := make([]adapter.PropertyResult, len(rs))
	for i, r := range rs {
		if r.Property == PropBusinessTrust {
			out[i] = r
			continue
		}
		out[i] = adapter.NativeCheckReport(r.Property, object, r.Status, r.Method, r.Detail, nil)
	}
	return out
}
