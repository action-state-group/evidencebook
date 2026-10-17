// SPDX-License-Identifier: Apache-2.0

// Package adapter defines the interface every foreign-evidence adapter
// implements, and the bridge from its per-property results to a
// requirement's sufficiency and verdict.
//
// An adapter checks one foreign protocol's objects under that protocol's own
// verification method, over the octets as the protocol delivered them. It
// never re-signs or re-encodes them. It declares, as data, what it checks and
// what a passing check supports (PRD-EVIDENCE-INTEROP FR-19), and it reports
// each property separately with the method that produced it (FR-27), using
// the five statuses of agent-action-capsule spec/interop-profile-v0.md §7.1.
//
// Carrying a foreign object by digest proves possession of its octets only
// (FR-20). A property is PASS only when the adapter ran the native check and
// it succeeded.
package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Status is one of the five per-property statuses (interop-profile-v0 §7.1).
type Status string

const (
	// Pass: the check ran and succeeded.
	Pass Status = "PASS"
	// Fail: the check ran and definitively failed.
	Fail Status = "FAIL"
	// NotPresent: the input the check needs is absent from what was received.
	NotPresent Status = "NOT_PRESENT"
	// NotChecked: the input is present and the check was not run (unsupported
	// method or version, an online check made offline, or excluded by policy).
	NotChecked Status = "NOT_CHECKED"
	// Inconclusive: the check ran and could not decide.
	Inconclusive Status = "INCONCLUSIVE"
)

// PropertyResult is one property report: {property, status, method}, with an
// optional detail (interop-profile-v0 §7.1).
//
// detail is either the text in Detail or, when DetailFields is set, a JSON
// object of those members (an adapter declaration's structured detail, for
// example {"check": …, "object": …}). With DetailFields set, Detail holds a
// human-readable explanation that is not part of the report's JSON.
type PropertyResult struct {
	Property     string            `json:"property"`
	Status       Status            `json:"status"`
	Method       string            `json:"method"`
	Detail       string            `json:"-"`
	DetailFields map[string]string `json:"-"`
}

// MarshalJSON writes {property, status, method, detail}, detail being the
// object of DetailFields when set, else the Detail text when non-empty.
func (r PropertyResult) MarshalJSON() ([]byte, error) {
	type plain struct {
		Property string `json:"property"`
		Status   Status `json:"status"`
		Method   string `json:"method"`
		Detail   any    `json:"detail,omitempty"`
	}
	p := plain{Property: r.Property, Status: r.Status, Method: r.Method}
	if r.DetailFields != nil {
		p.Detail = r.DetailFields
	} else if r.Detail != "" {
		p.Detail = r.Detail
	}
	return json.Marshal(p)
}

// UnmarshalJSON reads a report written by MarshalJSON.
func (r *PropertyResult) UnmarshalJSON(b []byte) error {
	var p struct {
		Property string          `json:"property"`
		Status   Status          `json:"status"`
		Method   string          `json:"method"`
		Detail   json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*r = PropertyResult{Property: p.Property, Status: p.Status, Method: p.Method}
	if len(p.Detail) == 0 || string(p.Detail) == "null" {
		return nil
	}
	if p.Detail[0] == '{' {
		return json.Unmarshal(p.Detail, &r.DetailFields)
	}
	return json.Unmarshal(p.Detail, &r.Detail)
}

// TrustPolicy is the relying party's input to a check. It is data, so the
// same file serves every adapter; each adapter documents the pins and key
// roles it reads.
type TrustPolicy struct {
	// Now is the relying party's clock for time-window checks. The zero
	// value means no clock was supplied, and window checks are NOT_CHECKED.
	Now time.Time `json:"now,omitzero"`
	// Pins are values the relying party fixes in advance (a chain, a token
	// contract, a payee). A check compares what was transmitted to the pin;
	// it never takes the pin from the transmitted object.
	Pins map[string]string `json:"pins,omitempty"`
	// Keys lists, per issuer role, the key identifiers the relying party
	// accepts for that role.
	Keys map[string][]string `json:"keys,omitempty"`
	// Recorded holds the relying party's recorded observations, keyed by a
	// name the adapter documents (for example the result of an on-chain
	// transaction lookup made earlier through a source the relying party
	// trusts). An adapter reads a recording instead of making an online
	// call; without one, the online check is NOT_CHECKED.
	Recorded map[string]json.RawMessage `json:"recorded,omitempty"`
}

// PinPolicyID is the pin naming the relying party's policy, for methods
// that report "relying party policy <id>".
const PinPolicyID = "policy.id"

// Pin returns the pinned value for name, or "".
func (p TrustPolicy) Pin(name string) string { return p.Pins[name] }

// LoadTrustPolicy reads a TrustPolicy from a JSON file.
func LoadTrustPolicy(path string) (TrustPolicy, error) {
	var p TrustPolicy
	data, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("trust policy %s: %w", path, err)
	}
	return p, nil
}

// NativeObject is an adapter's FR-19 declaration, as data.
type NativeObject struct {
	// Protocol names the foreign protocol and the revision the adapter reads.
	Protocol string `json:"protocol"`
	// Objects declares (a) each native object and its format, (b) its issuer
	// and how the issuer is resolved, and (c) its native verification method.
	Objects []Object `json:"objects"`
	// Predicate is (d): the exact statement a PASS on every signature
	// property supports, and nothing more.
	Predicate string `json:"predicate"`
	// NotSupported lists statements the checks do not support, even when
	// every property passes.
	NotSupported []string `json:"not_supported"`
	// Properties is (e): the per-property results the adapter reports, each
	// with its method.
	Properties []Property `json:"properties"`
}

// Object is one native object an adapter reads.
type Object struct {
	Name             string `json:"name"`
	Format           string `json:"format"`
	Octets           string `json:"octets"`
	Issuer           string `json:"issuer"`
	IssuerResolution string `json:"issuer_resolution"`
	Method           string `json:"method"`
	// NativelySigned is false when the protocol defines no signature over
	// the object; such an object proves possession of its octets only.
	NativelySigned bool `json:"natively_signed"`
}

// Property declares one property an adapter reports.
type Property struct {
	Name     string `json:"name"`
	Object   string `json:"object"`
	Method   string `json:"method"`
	Supports string `json:"supports"`
}

// Adapter checks one foreign protocol's objects under its native method.
type Adapter interface {
	// Name is the adapter's short name, as used on the command line.
	Name() string
	// NativeObject returns the FR-19 declaration.
	NativeObject() NativeObject
	// Check parses raw, the octets as received, and reports every declared
	// property that applies to the input's form, in declaration order. It makes no network calls; an online
	// check is reported NOT_CHECKED.
	Check(raw []byte, policy TrustPolicy) []PropertyResult
}

// NativeCheck is the property name of every report of a foreign object's
// native check (interop-profile-v0 §7.2). An adapter that decomposes the
// native check into named checks reports each as a native_check report and
// puts the check's name in detail (the declaration's open point A-2).
const NativeCheck = "native_check"

// NativeCheckResult builds a native_check report for the named check. detail
// is the check's name, followed by ": " and the explanation when there is one.
func NativeCheckResult(check string, status Status, method, explanation string) PropertyResult {
	detail := check
	if explanation != "" {
		detail += ": " + explanation
	}
	return PropertyResult{Property: NativeCheck, Status: status, Method: method, Detail: detail}
}

// NativeCheckReport builds a native_check report whose detail is the object
// {"check": check, "object": object, …extra}, the shape of the adapter
// declarations (for example agent-action-capsule spec/adapters/x402-v0.md
// §7). explanation is kept in Detail and is not part of the report's JSON.
func NativeCheckReport(check, object string, status Status, method, explanation string, extra map[string]string) PropertyResult {
	fields := map[string]string{"check": check}
	if object != "" {
		fields["object"] = object
	}
	for k, v := range extra {
		fields[k] = v
	}
	return PropertyResult{Property: NativeCheck, Status: status, Method: method, Detail: explanation, DetailFields: fields}
}

// CheckName is the name a result is known by: for a native_check report,
// detail's check member, or the check's name at the start of a text detail;
// otherwise the property.
func CheckName(r PropertyResult) string {
	if r.Property != NativeCheck {
		return r.Property
	}
	if c, ok := r.DetailFields["check"]; ok {
		return c
	}
	name, _, _ := strings.Cut(r.Detail, ": ")
	return name
}

// FindCheck returns the first result whose CheckName is name.
func FindCheck(results []PropertyResult, name string) (PropertyResult, bool) {
	for _, r := range results {
		if CheckName(r) == name {
			return r, true
		}
	}
	return PropertyResult{}, false
}

// AnyFail reports whether any result is FAIL.
func AnyFail(results []PropertyResult) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

// Find returns the result for property, and whether it was reported.
func Find(results []PropertyResult, property string) (PropertyResult, bool) {
	for _, r := range results {
		if r.Property == property {
			return r, true
		}
	}
	return PropertyResult{}, false
}
