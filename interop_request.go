// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"fmt"
	"regexp"
	"slices"
	"time"
)

// ProfileID identifies the revision of the Minimum Evidence
// Interoperability Profile this package implements (profile §3.1).
const ProfileID = "evidence-interop/0"

// RequestDraft is the Internet-Draft whose request map this package accepts
// (profile §11.4.2 `drafts`).
const RequestDraft = "draft-mih-agent-evidence-request-00"

// DerivationHistoryCard is the one derivation the request draft registers
// (ER §8.1).
const DerivationHistoryCard = "history_card/1"

// SubjectKind is one of the request draft's six subject forms (ER §3.1).
type SubjectKind string

const (
	SubjectFullHistory SubjectKind = "full_history"
	SubjectCheckpoints SubjectKind = "checkpoints"
	SubjectRecord      SubjectKind = "record"
	SubjectRange       SubjectKind = "range"
	SubjectCorrelation SubjectKind = "correlation"
	SubjectExchange    SubjectKind = "exchange"
)

// SubjectForms lists the six forms in code-point order.
var SubjectForms = []SubjectKind{SubjectCheckpoints, SubjectCorrelation, SubjectExchange, SubjectFullHistory, SubjectRange, SubjectRecord}

// Refusal reason tokens: the request draft's registry, verbatim (ER §13,
// profile §9.1). No other token is ever emitted.
const (
	ReasonNotAuthorized         = "not_authorized"
	ReasonNoSuchSubject         = "no_such_subject"
	ReasonCoverageUnsatisfiable = "coverage_unsatisfiable"
	ReasonDerivationUnsupported = "derivation_unsupported"
	ReasonPolicyDeclined        = "policy_declined"
	ReasonDeadlineUnmet         = "deadline_unmet"
	ReasonRequestMalformed      = "request_malformed"
	ReasonRetentionExpired      = "retention_expired"
)

// RefusalReasons is the registry, in the draft's order.
var RefusalReasons = []string{
	ReasonNotAuthorized, ReasonNoSuchSubject, ReasonCoverageUnsatisfiable, ReasonDerivationUnsupported,
	ReasonPolicyDeclined, ReasonDeadlineUnmet, ReasonRequestMalformed, ReasonRetentionExpired,
}

// Subject names what is asked for, in one of the six forms. Digest serves
// record and exchange, Range serves range (two zero-based log positions,
// a <= b), Correlation serves correlation. On the wire it is a map with
// exactly one member named by the form (ERV A1).
type Subject struct {
	Kind        SubjectKind
	Digest      string
	Range       [2]uint64
	Correlation string
}

// value is the subject's wire value (profile §5.2).
func (s Subject) value() map[string]any {
	switch s.Kind {
	case SubjectFullHistory, SubjectCheckpoints:
		return map[string]any{string(s.Kind): nil}
	case SubjectRecord, SubjectExchange:
		return map[string]any{string(s.Kind): s.Digest}
	case SubjectRange:
		return map[string]any{string(s.Kind): []any{s.Range[0], s.Range[1]}}
	case SubjectCorrelation:
		return map[string]any{string(s.Kind): s.Correlation}
	}
	return map[string]any{}
}

func (s Subject) valid() bool {
	switch s.Kind {
	case SubjectFullHistory, SubjectCheckpoints:
		return s.Digest == "" && s.Correlation == "" && s.Range == [2]uint64{}
	case SubjectRecord, SubjectExchange:
		return hex64.MatchString(s.Digest) && s.Correlation == "" && s.Range == [2]uint64{}
	case SubjectRange:
		return s.Range[0] <= s.Range[1] && s.Range[1] <= maxSafeInteger && s.Digest == "" && s.Correlation == ""
	case SubjectCorrelation:
		return s.Correlation != "" && s.Digest == "" && s.Range == [2]uint64{}
	}
	return false
}

// parseSubject reads a wire subject: a map with exactly one member, a known
// form, and content of that form's type (ERV A1, A2, A6).
func parseSubject(v any) (Subject, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return Subject{}, false
	}
	for name, content := range m {
		s := Subject{Kind: SubjectKind(name)}
		switch s.Kind {
		case SubjectFullHistory, SubjectCheckpoints:
			return s, content == nil
		case SubjectRecord, SubjectExchange:
			digest, ok := content.(string)
			s.Digest = digest
			return s, ok && hex64.MatchString(digest)
		case SubjectRange:
			pair, ok := content.([]any)
			if !ok || len(pair) != 2 {
				return Subject{}, false
			}
			a, okA := uintValue(pair[0])
			b, okB := uintValue(pair[1])
			s.Range = [2]uint64{a, b}
			return s, okA && okB && a <= b
		case SubjectCorrelation:
			id, ok := content.(string)
			s.Correlation = id
			return s, ok && id != ""
		}
	}
	return Subject{}, false
}

// Freshness is a min_freshness floor: exactly one of a log size or a time
// (ERV A3).
type Freshness struct {
	Size *uint64
	Time string
}

// Coverage carries exactly one of ExpectedPin (the digest of an anchor the
// requester holds) or MinFreshness (ER §3.2).
type Coverage struct {
	ExpectedPin  string
	MinFreshness *Freshness
}

// EvidenceRequest is the request map (ER §3, profile §6.1).
type EvidenceRequest struct {
	Subject    Subject
	Coverage   Coverage
	Derivation string
	Deadline   string
	Nonce      string
	Route      string
	// Profile is the profile identifier the request names. Request sets it
	// to ProfileID; a received request without one is answered as ProfileID
	// (profile §3.3 step 2).
	Profile string
}

// value is the request's wire value. Optional members are omitted when empty.
func (r EvidenceRequest) value() (map[string]any, error) {
	if !r.Subject.valid() {
		return nil, fmt.Errorf("%w: subject is not one of the six forms with conforming content", ErrInvalid)
	}
	coverage := map[string]any{}
	if r.Coverage.ExpectedPin != "" {
		if !hex64.MatchString(r.Coverage.ExpectedPin) {
			return nil, fmt.Errorf("%w: expected_pin is not a digest", ErrInvalid)
		}
		coverage["expected_pin"] = r.Coverage.ExpectedPin
	}
	if f := r.Coverage.MinFreshness; f != nil {
		switch {
		case f.Size != nil && f.Time == "" && *f.Size <= maxSafeInteger:
			coverage["min_freshness"] = *f.Size
		case f.Size == nil && f.Time != "":
			if _, ok := parseRequestTime(f.Time); !ok {
				return nil, fmt.Errorf("%w: min_freshness time %q is not an RFC 3339 UTC time", ErrInvalid, f.Time)
			}
			coverage["min_freshness"] = f.Time
		default:
			return nil, fmt.Errorf("%w: min_freshness is exactly one of a size or a time", ErrInvalid)
		}
	}
	if len(coverage) != 1 {
		return nil, fmt.Errorf("%w: coverage carries exactly one of expected_pin or min_freshness", ErrInvalid)
	}
	out := map[string]any{"subject": r.Subject.value(), "coverage": coverage}
	if r.Derivation != "" {
		if !validDerivation(r.Derivation) {
			return nil, fmt.Errorf("%w: derivation %q is neither a registered token form nor a definition digest", ErrInvalid, r.Derivation)
		}
		out["derivation"] = r.Derivation
	}
	if r.Deadline != "" {
		if _, ok := parseRequestTime(r.Deadline); !ok {
			return nil, fmt.Errorf("%w: deadline %q is not an RFC 3339 UTC time", ErrInvalid, r.Deadline)
		}
		out["deadline"] = r.Deadline
	}
	for name, text := range map[string]string{"nonce": r.Nonce, "route": r.Route, "profile": r.Profile} {
		if text != "" {
			out[name] = text
		}
	}
	return out, nil
}

// EncodeRequest returns the request octets in the encoding mediaType names:
// JCS for JSON, deterministic CBOR for CBOR (profile §5.1, P-9).
func EncodeRequest(r EvidenceRequest, mediaType string) ([]byte, error) {
	value, err := r.value()
	if err != nil {
		return nil, err
	}
	return encodeIn(mediaType, value)
}

var derivationToken = regexp.MustCompile(`^[a-z0-9_]+/[0-9]+$`)

// validDerivation: a registered token with a /N suffix, or a 64-hex
// definition digest (ERV A14, profile §5.2).
func validDerivation(s string) bool {
	return derivationToken.MatchString(s) || hex64.MatchString(s)
}

// requestCheck is the outcome of reading a received request: the request,
// or the refusal reason the first failing rule of profile §6.1 assigns.
type requestCheck struct {
	request EvidenceRequest
	// floorTime is min_freshness as a time, when the request gave one.
	floorTime time.Time
	reason    string
	// why is the rule that failed, for the caller's diagnostics; it is not
	// sent. detail is sent: only for an unknown profile, where it lists the
	// identifiers supported (§3.3 step 4). A refusal carries no other
	// detail, so it stays the bare signed object every implementation reads.
	why    string
	detail string
}

// readRequest validates received request octets in the order of profile
// §6.1 steps 1 to 5. Members it does not define are ignored (ER §3).
// Member names match exactly: a case variant is an unknown member.
func readRequest(m Message) requestCheck {
	malformed := func(why string) requestCheck {
		return requestCheck{reason: ReasonRequestMalformed, why: why}
	}
	root, err := decodeMessage(m)
	if err != nil {
		return malformed("not a well-formed map")
	}
	fields, ok := root.(map[string]any)
	if !ok {
		return malformed("not a map")
	}
	var out requestCheck
	// Step 2: subject.
	raw, present := fields["subject"]
	if !present {
		return malformed("subject is missing")
	}
	if out.request.Subject, ok = parseSubject(raw); !ok {
		return malformed("subject is not one form with conforming content")
	}
	// Step 3: each coverage member present has a conforming value.
	members := 0
	if raw, present := fields["coverage"]; present {
		coverage, ok := raw.(map[string]any)
		if !ok {
			return malformed("coverage is not a map")
		}
		if pin, present := coverage["expected_pin"]; present {
			digest, ok := pin.(string)
			if !ok || !hex64.MatchString(digest) {
				return malformed("expected_pin is not a digest")
			}
			out.request.Coverage.ExpectedPin = digest
			members++
		}
		if floor, present := coverage["min_freshness"]; present {
			switch value := floor.(type) {
			case string:
				t, ok := parseRequestTime(value)
				if !ok {
					return malformed("min_freshness is not an RFC 3339 UTC time")
				}
				out.request.Coverage.MinFreshness, out.floorTime = &Freshness{Time: value}, t
			default:
				size, ok := uintValue(value)
				if !ok {
					return malformed("min_freshness is neither an unsigned size nor a time")
				}
				out.request.Coverage.MinFreshness = &Freshness{Size: &size}
			}
			members++
		}
	}
	// The other defined members: a value that does not conform is malformed
	// (ER §3).
	for _, name := range []string{"derivation", "deadline", "nonce", "route", "profile"} {
		raw, present := fields[name]
		if !present {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			return malformed(name + " is not a text string")
		}
		switch name {
		case "derivation":
			if !validDerivation(text) {
				return malformed("derivation is neither a registered token form nor a definition digest")
			}
			out.request.Derivation = text
		case "deadline":
			if _, ok := parseRequestTime(text); !ok {
				return malformed("deadline is not an RFC 3339 UTC time")
			}
			out.request.Deadline = text
		case "nonce":
			out.request.Nonce = text
		case "route":
			out.request.Route = text
		case "profile":
			out.request.Profile = text
		}
	}
	// Step 4: exactly one coverage member (ER §3.2; ERV A4).
	if members != 1 {
		out.reason = ReasonCoverageUnsatisfiable
		return out
	}
	// Step 5: a profile this responder does not implement (§3.3 step 4).
	if out.request.Profile != "" && !slices.Contains(supportedProfiles, out.request.Profile) {
		return requestCheck{reason: ReasonRequestMalformed, detail: "profiles supported: " + ProfileID}
	}
	if out.request.Profile == "" {
		out.request.Profile = ProfileID
	}
	return out
}

var supportedProfiles = []string{ProfileID}
