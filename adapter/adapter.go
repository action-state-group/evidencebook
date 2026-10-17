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
type PropertyResult struct {
	Property string `json:"property"`
	Status   Status `json:"status"`
	Method   string `json:"method"`
	Detail   string `json:"detail,omitempty"`
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
}

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
