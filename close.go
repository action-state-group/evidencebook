// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CloseRecordVersion is the record_version of the Close body Book.Close
// seals: agent-action-capsule's close/v1 (schemas/judge/close-v1.json).
const CloseRecordVersion = "close/v1"

// ClosePeriod is the period a Close states, as RFC 3339 instants.
type ClosePeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// CloseReconcile is a close/v1 Close's reconcile block. Status is the
// Close's own belief when sealed, always UNILATERAL here; a Close's status
// is read from the counterparty's links (StatusOfClose), never from it.
type CloseReconcile struct {
	Tallies ResultTallies `json:"tallies"`
	Status  CloseStatus   `json:"status"`
}

// CloseStatement is the close/v1 body of a Close record.
//
// RecordID is a locator, "<book_id>#<seq>" (the record capsule's action_id):
// the record's identity is its capsule id, a digest over the header that
// carries this body, so the body cannot name it. Head and every link target
// are record ids, never locators.
//
// Head and CountsByKind describe the own window the Close binds: Head is the
// record at the window's last position, CountsByKind every record in the
// window by record_type. The window can begin before Period.Start when a
// caller chose it so (capsulectl's --since-last); Window recovers it.
type CloseStatement struct {
	RecordVersion string         `json:"record_version"`
	RecordID      string         `json:"record_id"`
	EpistemicType EpistemicType  `json:"epistemic_type"`
	ClosedAt      string         `json:"closed_at"`
	Period        ClosePeriod    `json:"period"`
	CountsByKind  map[string]int `json:"counts_by_kind"`
	Head          EvidenceRef    `json:"head"`
	// Reconcile is present exactly when the Close cites the peer's Close,
	// the one cites link in EvidenceLinks.
	Reconcile     *CloseReconcile `json:"reconcile,omitempty"`
	EvidenceLinks []Link          `json:"x-evidence-links,omitempty"`
}

// LegacyCloseStatement is the body Closes carried before close/v1: no
// record_version, the book's whole reconciliation, and the profile string
// the caller named.
type LegacyCloseStatement struct {
	Counterparty   string         `json:"counterparty"`
	Profile        string         `json:"profile"`
	Reconciliation Reconciliation `json:"reconciliation"`
	Reason         string         `json:"reason,omitempty"`
}

// CloseInput states what a Close binds. The book computes the
// reconciliation itself from Reconcile; a caller never supplies tallies.
// Reconcile's own window (FromSeq, ToSeq, zero leaving an end open) is the
// window the Close binds. PeerClose names the counterparty's Close for the
// same period, which the reconciliation answers; without it the Close seals
// counts only, with no reconcile block. Supersedes names a prior Close this
// one adjusts; the prior Close is never rewritten.
type CloseInput struct {
	Reconcile    ReconcileInput
	Counterparty string
	Period       ClosePeriod
	PeerClose    string
	Supersedes   string
}

// Close reconciles, then seals a close/v1 Close record: a closes link to
// every own record the reconciliation placed, the period, the window's
// counts by record type and its head, and, when a peer Close is named, the
// tallies and a cites link to that Close. Its status is read later from the
// links other records make to it.
func (b *Book) Close(ctx context.Context, in CloseInput) (Record, error) {
	if in.Counterparty == "" {
		return Record{}, fmt.Errorf("%w: a Close names its counterparty", ErrInvalid)
	}
	if in.Period.Start.IsZero() || !in.Period.Start.Before(in.Period.End) {
		return Record{}, fmt.Errorf("%w: a Close states a period whose start is before its end", ErrInvalid)
	}
	if in.PeerClose != "" && !hex64.MatchString(in.PeerClose) {
		return Record{}, fmt.Errorf("%w: peer Close %q is not a record id", ErrInvalid, in.PeerClose)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return Record{}, ErrReleased
	}
	// The window, head, counts and closes links are taken against the log
	// the Close lands in, so a stored record awaiting its append goes first.
	if err := b.flushPendingLocked(ctx); err != nil {
		return Record{}, err
	}
	size := uint64(len(b.order))
	if size == 0 {
		return Record{}, fmt.Errorf("%w: a Close needs a record before it to name as its head", ErrInvalid)
	}
	// The resolved window feeds the counts and the head only: the
	// reconciliation takes its input as given, so Close and Reconcile keep
	// one completeness rule for an open window.
	from, to := max(in.Reconcile.FromSeq, 1), in.Reconcile.ToSeq
	if to == 0 {
		to = size
	}
	if to > size || from > to+1 {
		return Record{}, fmt.Errorf("%w: window %d to %d is not within this book's %d records", ErrInvalid, in.Reconcile.FromSeq, in.Reconcile.ToSeq, size)
	}
	links := []Link{}
	if in.Supersedes != "" {
		prior, ok := b.records[in.Supersedes]
		if !ok || prior.Header.RecordType != RecordTypeClose {
			return Record{}, fmt.Errorf("%w: %s is not a Close in this book", ErrNotFound, in.Supersedes)
		}
		links = append(links, Link{Type: Supersedes, Target: in.Supersedes})
	}
	recon, err := b.reconcileLocked(ctx, in.Reconcile)
	if err != nil {
		return Record{}, err
	}
	for _, pair := range recon.Pairs {
		if pair.A != "" {
			links = append(links, Link{Type: Closes, Target: pair.A})
		}
	}
	statement := CloseStatement{
		RecordVersion: CloseRecordVersion, EpistemicType: ProducerClaim,
		Period:       ClosePeriod{Start: in.Period.Start.UTC(), End: in.Period.End.UTC()},
		CountsByKind: map[string]int{},
		Head:         EvidenceRef{Digest: b.order[to-1], DigestAlg: "SHA-256"},
	}
	for seq := from; seq <= to; seq++ {
		statement.CountsByKind[b.records[b.order[seq-1]].Header.RecordType]++
	}
	if in.PeerClose != "" {
		statement.Reconcile = &CloseReconcile{Tallies: recon.Tallies.result(), Status: Unilateral}
		statement.EvidenceLinks = []Link{{Type: Cites, Target: in.PeerClose}}
	}
	entry := Entry{RecordType: RecordTypeClose, EpistemicType: ProducerClaim, Links: links, CounterpartyRef: in.Counterparty}
	return b.commitEntryLocked(ctx, entry, nil, nil, func(seq uint64, committedAt string) (json.RawMessage, error) {
		statement.RecordID, statement.ClosedAt = closeLocator(b.id, seq), committedAt
		return json.Marshal(statement)
	})
}

func closeLocator(bookID string, seq uint64) string {
	return fmt.Sprintf("%s#%d", bookID, seq)
}

// ReadClose reads a Close record's body, dispatching on record_version: a
// close/v1 body fills v1, a body without record_version (a Close sealed
// before close/v1) fills legacy, and any other version is refused. A v1 body
// is checked against its own header: record_id is the header's
// book_id#seq, closed_at its committed_at, and both epistemic types are
// producer_claim.
func ReadClose(h Header) (v1 *CloseStatement, legacy *LegacyCloseStatement, err error) {
	if h.RecordType != RecordTypeClose {
		return nil, nil, fmt.Errorf("%w: record %d is a %s, not a Close", ErrInvalid, h.Seq, h.RecordType)
	}
	var probe struct {
		RecordVersion *string `json:"record_version"`
	}
	if err := json.Unmarshal(h.Statement, &probe); err != nil {
		return nil, nil, fmt.Errorf("%w: Close %d statement: %v", ErrInvalid, h.Seq, err)
	}
	if probe.RecordVersion == nil {
		legacy = &LegacyCloseStatement{}
		if err := json.Unmarshal(h.Statement, legacy); err != nil {
			return nil, nil, fmt.Errorf("%w: Close %d statement: %v", ErrInvalid, h.Seq, err)
		}
		return nil, legacy, nil
	}
	if *probe.RecordVersion != CloseRecordVersion {
		return nil, nil, fmt.Errorf("%w: Close %d has record_version %q, not %s", ErrInvalid, h.Seq, *probe.RecordVersion, CloseRecordVersion)
	}
	// close/v1 is a closed object: an unknown member is refused, not dropped.
	decoder := json.NewDecoder(bytes.NewReader(h.Statement))
	decoder.DisallowUnknownFields()
	v1 = &CloseStatement{}
	if err := decoder.Decode(v1); err != nil {
		return nil, nil, fmt.Errorf("%w: Close %d statement: %v", ErrInvalid, h.Seq, err)
	}
	if err := v1.check(h); err != nil {
		return nil, nil, fmt.Errorf("%w: Close %d: %v", ErrInvalid, h.Seq, err)
	}
	return v1, nil, nil
}

func (s CloseStatement) check(h Header) error {
	switch {
	case s.RecordID != closeLocator(h.BookID, h.Seq):
		return fmt.Errorf("record_id %q is not %q", s.RecordID, closeLocator(h.BookID, h.Seq))
	case s.ClosedAt != h.CommittedAt:
		return fmt.Errorf("closed_at %q is not its committed_at %q", s.ClosedAt, h.CommittedAt)
	case s.EpistemicType != ProducerClaim || h.EpistemicType != ProducerClaim:
		return fmt.Errorf("a close/v1 Close is a producer_claim")
	case !s.Period.Start.Before(s.Period.End):
		return fmt.Errorf("period start is not before its end")
	case s.CountsByKind == nil:
		return fmt.Errorf("counts_by_kind is missing")
	case s.Head.DigestAlg != "SHA-256" || !hex64.MatchString(s.Head.Digest):
		return fmt.Errorf("head is not a SHA-256 record id")
	}
	for kind, n := range s.CountsByKind {
		if n < 0 {
			return fmt.Errorf("counts_by_kind[%q] is negative", kind)
		}
	}
	cites := 0
	for _, link := range s.EvidenceLinks {
		if !link.Type.Valid() || !hex64.MatchString(link.Target) {
			return fmt.Errorf("x-evidence-links holds %+v, not a typed link to a record id", link)
		}
		if link.Type == Cites {
			cites++
		}
	}
	if s.Reconcile != nil && cites != 1 {
		return fmt.Errorf("a reconcile block needs exactly one cites link to the peer's Close, not %d", cites)
	}
	return nil
}

// Window recovers the own window [from, to] a v1 Close binds, given the seq
// of its head record and the Close's own seq: to is the head's seq, and the
// counts cover every position from from to to. A body whose counts do not
// fit under its head (a negative count, more records than the head's
// position, or a head at or after the Close) is refused: a signed body is
// not proof that its counts are consistent. An empty window has from = to+1.
func (s CloseStatement) Window(headSeq, closeSeq uint64) (from, to uint64, err error) {
	if headSeq == 0 || headSeq >= closeSeq {
		return 0, 0, fmt.Errorf("%w: head at position %d is not before the Close at %d", ErrInvalid, headSeq, closeSeq)
	}
	var total uint64
	for kind, n := range s.CountsByKind {
		if n < 0 {
			return 0, 0, fmt.Errorf("%w: counts_by_kind[%q] is negative", ErrInvalid, kind)
		}
		// Checked per kind, so the sum never passes headSeq and cannot overflow.
		if total += uint64(n); total > headSeq {
			return 0, 0, fmt.Errorf("%w: counts_by_kind sums past the head's position %d", ErrInvalid, headSeq)
		}
	}
	return headSeq - total + 1, headSeq, nil
}

// result returns the tallies under the lowercase keys close/v1 and Result v0
// use.
func (t Tallies) result() ResultTallies {
	return ResultTallies{Matched: t.Matched, AOnly: t.AOnly, BOnly: t.BOnly, Conflicting: t.Conflicting, Insufficient: t.Insufficient, Unresolved: t.Unresolved}
}
