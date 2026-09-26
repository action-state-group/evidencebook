// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// HeaderVersion is the version of the record header wire form this package
// writes and accepts.
const HeaderVersion = 1

// EpistemicType states how a record's content came to be known. It is a
// closed set (Evidence Layer §4.1), assigned once at commit and never
// upgraded by anything attached to the record later.
type EpistemicType string

const (
	ObservedEvent       EpistemicType = "observed_event"
	SystemOfRecordFact  EpistemicType = "system_of_record_fact"
	ProducerClaim       EpistemicType = "producer_claim"
	HumanReport         EpistemicType = "human_report"
	SemanticJudgment    EpistemicType = "semantic_judgment"
	DerivedMetric       EpistemicType = "derived_metric"
	Adjudication        EpistemicType = "adjudication"
	ObligationReference EpistemicType = "obligation_reference"
)

// EpistemicTypes returns the closed value set in registry order.
func EpistemicTypes() []EpistemicType {
	return []EpistemicType{ObservedEvent, SystemOfRecordFact, ProducerClaim, HumanReport, SemanticJudgment, DerivedMetric, Adjudication, ObligationReference}
}

// Valid reports whether t is one of the eight registered values.
func (t EpistemicType) Valid() bool {
	for _, known := range EpistemicTypes() {
		if t == known {
			return true
		}
	}
	return false
}

// LinkType is the closed typed-link vocabulary (Evidence Layer §5).
type LinkType string

const (
	Cites        LinkType = "cites"
	Adjudicates  LinkType = "adjudicates"
	Supersedes   LinkType = "supersedes"
	Acknowledges LinkType = "acknowledges"
	Rebuts       LinkType = "rebuts"
	Closes       LinkType = "closes"
)

// LinkTypes returns the closed link vocabulary in registry order.
func LinkTypes() []LinkType {
	return []LinkType{Cites, Adjudicates, Supersedes, Acknowledges, Rebuts, Closes}
}

// Valid reports whether t is a registered link type.
func (t LinkType) Valid() bool {
	for _, known := range LinkTypes() {
		if t == known {
			return true
		}
	}
	return false
}

// RetentionState names whether a record's payload can currently be resolved
// (Evidence Layer §6.1).
type RetentionState string

const (
	Available RetentionState = "AVAILABLE"
	Partial   RetentionState = "PARTIAL"
	Withheld  RetentionState = "WITHHELD"
	Deleted   RetentionState = "DELETED"
	LegalHold RetentionState = "LEGAL_HOLD"
)

// Valid reports whether s is a registered retention state.
func (s RetentionState) Valid() bool {
	switch s {
	case Available, Partial, Withheld, Deleted, LegalHold:
		return true
	}
	return false
}

// Record types this package writes itself. Callers may use any other token;
// record_type is open vocabulary.
const (
	RecordTypeClose           = "close"
	RecordTypeDisclosure      = "disclosure"
	RecordTypeLifecycle       = "lifecycle"
	RecordTypeIndexRoot       = "index_root"
	RecordTypeRequest         = "evidence_request"
	RecordTypeRequestAnswered = "evidence_request_answered"
	RecordTypeResponse        = "evidence_response"
	RecordTypeAbsence         = "recorded_absence"
	RecordTypeAcknowledgement = "acknowledgement"
	RecordTypeRebuttal        = "rebuttal"
)

// Link is a typed, directed reference to another record by its record id.
// A link is committed inside the carrying record and never mutates its target.
type Link struct {
	Type   LinkType `json:"type"`
	Target string   `json:"target"`
}

// Correlation carries the exchange correlation keys used to pair two halves
// of one interaction across independently held books. Join order is
// ExchangeID, then RequestDigest; TwinBracketID is recorded, never joined on.
type Correlation struct {
	ExchangeID    string `json:"exchange_id,omitempty"`
	RequestDigest string `json:"request_digest,omitempty"`
	TwinBracketID string `json:"twin_bracket_id,omitempty"`
}

func (c Correlation) empty() bool {
	return c == Correlation{}
}

// Header is the durable, privacy-minimized record header (Evidence Layer §4).
// Its canonical JSON is committed as the record capsule's agent_input_digest,
// so every field below is fixed at commit.
type Header struct {
	Version            int             `json:"v"`
	BookID             string          `json:"book_id"`
	Seq                uint64          `json:"seq"`
	RecordType         string          `json:"record_type"`
	EpistemicType      EpistemicType   `json:"epistemic_type"`
	CommittedAt        string          `json:"committed_at"`
	EventTimeClaim     string          `json:"event_time_claim,omitempty"`
	PayloadCommitments []string        `json:"payload_commitments,omitempty"`
	RetentionState     RetentionState  `json:"retention_state,omitempty"`
	Links              []Link          `json:"links"`
	SubjectRef         string          `json:"subject_ref,omitempty"`
	PrincipalRef       string          `json:"principal_ref,omitempty"`
	CounterpartyRef    string          `json:"counterparty_ref,omitempty"`
	Correlation        *Correlation    `json:"correlation,omitempty"`
	Statement          json.RawMessage `json:"statement,omitempty"`
}

// Record is one committed entry: its identity, position, and header.
// RecordID is the Capsule ID of the sealed record capsule and is the exact
// 32-byte value appended to the commitment substrate.
type Record struct {
	RecordID string
	Seq      uint64
	Header   Header
}

// Entry is what a caller asks the book to record. Seq, CommittedAt, BookID and
// the record identity are assigned by the book.
type Entry struct {
	RecordType      string
	EpistemicType   EpistemicType
	EventTimeClaim  time.Time
	Payloads        [][]byte
	Links           []Link
	SubjectRef      string
	PrincipalRef    string
	CounterpartyRef string
	Correlation     Correlation
	Statement       json.RawMessage
}

func validateHeader(h Header) error {
	if h.Version != HeaderVersion {
		return fmt.Errorf("%w: header version %d", ErrInvalid, h.Version)
	}
	if h.BookID == "" || h.Seq == 0 || h.RecordType == "" || h.CommittedAt == "" {
		return fmt.Errorf("%w: book_id, seq, record_type and committed_at are required", ErrInvalid)
	}
	if !h.EpistemicType.Valid() {
		return fmt.Errorf("%w: epistemic_type %q is not registered", ErrInvalid, h.EpistemicType)
	}
	if len(h.PayloadCommitments) > 0 && !h.RetentionState.Valid() {
		return fmt.Errorf("%w: retention_state is required with payload_commitments", ErrInvalid)
	}
	for _, digest := range h.PayloadCommitments {
		if !hex64.MatchString(digest) {
			return fmt.Errorf("%w: payload commitment %q is not a lowercase SHA-256 digest", ErrInvalid, digest)
		}
	}
	if h.Links == nil {
		return fmt.Errorf("%w: links must be present, possibly empty", ErrInvalid)
	}
	for _, link := range h.Links {
		if !link.Type.Valid() {
			return fmt.Errorf("%w: link type %q is not registered", ErrInvalid, link.Type)
		}
		if !hex64.MatchString(link.Target) {
			return fmt.Errorf("%w: link target %q is not a record id", ErrInvalid, link.Target)
		}
		if link.Type == Adjudicates && h.EpistemicType != Adjudication {
			return fmt.Errorf("%w: a record carrying an adjudicates link must have epistemic_type adjudication", ErrInvalid)
		}
	}
	return nil
}

// LinksTo returns the targets of every link of type t in h.
func (h Header) LinksTo(t LinkType) []string {
	var out []string
	for _, link := range h.Links {
		if link.Type == t {
			out = append(out, link.Target)
		}
	}
	return out
}
