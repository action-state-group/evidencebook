// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// ExchangeState is the per-exchange reconciliation state.
type ExchangeState string

const (
	Matched      ExchangeState = "MATCHED"
	AOnly        ExchangeState = "A_ONLY"
	BOnly        ExchangeState = "B_ONLY"
	Conflicting  ExchangeState = "CONFLICTING"
	Insufficient ExchangeState = "INSUFFICIENT"
	Unresolved   ExchangeState = "UNRESOLVED"
)

// Join keys a pair can be correlated by, in preference order.
const (
	JoinExchangeID    = "exchange_id"
	JoinRequestDigest = "request_digest"
)

// Half is one side's record of one exchange as the reconciliation sees it.
// Covered means inside an authenticated checkpoint; Trusted means the record
// verifies and its header matches what it committed.
type Half struct {
	RecordID           string
	Seq                uint64
	Correlation        Correlation
	PayloadCommitments []string
	Covered            bool
	Trusted            bool
}

// HalfSet is one side's account of a period. Complete means that account is
// checkpoint-covered, so a missing counterpart on this side is a provable
// absence rather than an unknown.
type HalfSet struct {
	Halves   []Half
	Complete bool
}

// Comparator is the content test a declared profile supplies. It runs only on
// covered, trusted, correlated pairs and returns whether the contents agree.
type Comparator func(a, b Half) bool

// SamePayloadCommitments is a Comparator for deterministic content: the two
// halves committed identical payload digests. Content produced by sampling
// needs a comparator over the disclosed content instead.
func SamePayloadCommitments(a, b Half) bool {
	return len(a.PayloadCommitments) > 0 && slices.Equal(a.PayloadCommitments, b.PayloadCommitments)
}

// PairResult classifies one exchange.
type PairResult struct {
	State         ExchangeState `json:"state"`
	JoinKey       string        `json:"join_key,omitempty"`
	A             string        `json:"a,omitempty"`
	B             string        `json:"b,omitempty"`
	TwinBracketID string        `json:"twin_bracket_id,omitempty"`
}

// Tallies counts exchanges per state.
type Tallies struct {
	Matched      int `json:"MATCHED"`
	AOnly        int `json:"A_ONLY"`
	BOnly        int `json:"B_ONLY"`
	Conflicting  int `json:"CONFLICTING"`
	Insufficient int `json:"INSUFFICIENT"`
	Unresolved   int `json:"UNRESOLVED"`
}

func (t *Tallies) add(state ExchangeState) {
	switch state {
	case Matched:
		t.Matched++
	case AOnly:
		t.AOnly++
	case BOnly:
		t.BOnly++
	case Conflicting:
		t.Conflicting++
	case Insufficient:
		t.Insufficient++
	case Unresolved:
		t.Unresolved++
	}
}

// ReconcileHalves pairs two sides' halves and classifies every exchange seen
// on either side into exactly one of six states.
//
// Correlation runs in two passes over all halves, so the result does not
// depend on input order: first exchange_id (preferring a counterpart whose
// request_digest also agrees), then request_digest for halves still
// unpaired. An agreeing exchange_id with disagreeing request digests is
// CONFLICTING and is never re-paired on the weaker key. twin_bracket_id is
// recorded when both halves carry the same value and is never a join key.
//
// One half unavailable is not disagreement: an unpaired half is A_ONLY or
// B_ONLY when the other side's account is complete, INSUFFICIENT otherwise,
// and never CONFLICTING.
func ReconcileHalves(a, b HalfSet, compare Comparator) ([]PairResult, Tallies) {
	partner := make([]int, len(a.Halves))
	keys := make([]string, len(a.Halves))
	used := make([]bool, len(b.Halves))
	for i := range partner {
		partner[i] = -1
	}
	pick := func(match func(Half) bool, prefer func(Half) bool) int {
		found := -1
		for j, h := range b.Halves {
			if used[j] || !match(h) {
				continue
			}
			if prefer(h) {
				return j
			}
			if found < 0 {
				found = j
			}
		}
		return found
	}
	for i, own := range a.Halves {
		c := own.Correlation
		if c.ExchangeID == "" {
			continue
		}
		j := pick(func(h Half) bool { return h.Correlation.ExchangeID == c.ExchangeID },
			func(h Half) bool {
				return h.Correlation.RequestDigest == "" || c.RequestDigest == "" || h.Correlation.RequestDigest == c.RequestDigest
			})
		if j >= 0 {
			partner[i], keys[i], used[j] = j, JoinExchangeID, true
		}
	}
	for i, own := range a.Halves {
		c := own.Correlation
		if partner[i] >= 0 || c.RequestDigest == "" {
			continue
		}
		j := pick(func(h Half) bool { return h.Correlation.RequestDigest == c.RequestDigest }, func(Half) bool { return true })
		if j >= 0 {
			partner[i], keys[i], used[j] = j, JoinRequestDigest, true
		}
	}
	var results []PairResult
	for i, own := range a.Halves {
		c := own.Correlation
		if partner[i] < 0 {
			state := Insufficient
			if own.Covered && b.Complete && !c.empty() {
				state = AOnly
			}
			results = append(results, PairResult{State: state, A: own.RecordID})
			continue
		}
		peer := b.Halves[partner[i]]
		pc := peer.Correlation
		result := PairResult{JoinKey: keys[i], A: own.RecordID, B: peer.RecordID}
		if c.TwinBracketID != "" && c.TwinBracketID == pc.TwinBracketID {
			result.TwinBracketID = c.TwinBracketID
		}
		switch {
		case keys[i] == JoinExchangeID && c.RequestDigest != "" && pc.RequestDigest != "" && c.RequestDigest != pc.RequestDigest:
			result.State = Conflicting
		case !own.Covered || !peer.Covered:
			result.State = Insufficient
		case !own.Trusted || !peer.Trusted:
			result.State = Unresolved
		case compare(own, peer):
			result.State = Matched
		default:
			result.State = Conflicting
		}
		results = append(results, result)
	}
	for j, peer := range b.Halves {
		if used[j] {
			continue
		}
		state := Insufficient
		if peer.Covered && a.Complete && !peer.Correlation.empty() {
			state = BOnly
		}
		results = append(results, PairResult{State: state, B: peer.RecordID})
	}
	var tallies Tallies
	for _, r := range results {
		tallies.add(r.State)
	}
	return results, tallies
}

// ReconcileInput names the peer's disclosed account and the content test.
// Each side's window is in that side's own log positions.
type ReconcileInput struct {
	Peer VerifiedBundle
	// RecordType restricts both sides to one record type; empty takes every
	// record that carries correlation keys.
	RecordType string
	// FromSeq and ToSeq bound this book's own window; zero leaves an end open.
	FromSeq, ToSeq uint64
	// PeerFromSeq and PeerToSeq bound the peer's window the same way.
	PeerFromSeq, PeerToSeq uint64
	Compare                Comparator
}

// Reconciliation is the result of comparing this book (side A) with a peer's
// verified bundle (side B). It is computed from bundles alone.
type Reconciliation struct {
	OwnHead      string       `json:"own_head"`
	PeerHead     string       `json:"peer_head"`
	PeerLog      string       `json:"peer_log"`
	PeerComplete bool         `json:"peer_complete"`
	FromSeq      uint64       `json:"from_seq"`
	ToSeq        uint64       `json:"to_seq"`
	PeerFromSeq  uint64       `json:"peer_from_seq"`
	PeerToSeq    uint64       `json:"peer_to_seq"`
	Pairs        []PairResult `json:"pairs"`
	Tallies      Tallies      `json:"states"`
}

func inWindow(seq, from, to uint64) bool {
	return (from == 0 || seq >= from) && (to == 0 || seq <= to)
}

// Reconcile compares this book's halves in its window with the halves a peer
// disclosed in a verified bundle.
func (b *Book) Reconcile(ctx context.Context, in ReconcileInput) (Reconciliation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reconcileLocked(ctx, in)
}

// reconcileLocked builds both HalfSets. The peer's account is complete only
// when its checkpoint is authenticated, its interval starts at or before the
// peer window, and every record it carries in the window has a verified
// header; a withheld or unverifiable header could be any record's
// counterpart, so it makes absence unprovable rather than disappearing.
func (b *Book) reconcileLocked(ctx context.Context, in ReconcileInput) (Reconciliation, error) {
	if in.Compare == nil {
		return Reconciliation{}, fmt.Errorf("%w: a content comparator is required", ErrInvalid)
	}
	head, haveHead, err := b.latestLocked(ctx)
	if err != nil {
		return Reconciliation{}, err
	}
	out := Reconciliation{FromSeq: in.FromSeq, ToSeq: in.ToSeq, PeerFromSeq: in.PeerFromSeq, PeerToSeq: in.PeerToSeq, PeerLog: in.Peer.LogID}
	if haveHead {
		out.OwnHead = head.ID
	}
	if in.Peer.AnchorAuthenticated {
		out.PeerHead = fmt.Sprintf("%s:%d", in.Peer.Anchor.Root, in.Peer.Anchor.MMRSize)
	}
	own := HalfSet{Complete: haveHead && (in.ToSeq == 0 || head.Covers(in.ToSeq))}
	for _, id := range b.order {
		record := b.records[id]
		if !inWindow(record.Seq, in.FromSeq, in.ToSeq) || !isHalf(record.Header, in.RecordType) {
			continue
		}
		own.Halves = append(own.Halves, Half{
			RecordID: id, Seq: record.Seq, Correlation: *record.Header.Correlation,
			PayloadCommitments: record.Header.PayloadCommitments,
			Covered:            haveHead && head.Covers(record.Seq), Trusted: true,
		})
	}
	peer := HalfSet{Complete: in.Peer.AnchorAuthenticated && in.Peer.IntervalFirst <= max(in.PeerFromSeq, 1) &&
		(in.PeerToSeq == 0 || in.Peer.IntervalLast >= in.PeerToSeq)}
	for _, record := range in.Peer.Records {
		if !inWindow(record.Seq, in.PeerFromSeq, in.PeerToSeq) {
			continue
		}
		if record.Header == nil || !record.HeaderVerified || !record.CapsuleOK {
			peer.Complete = false
			if record.Header == nil {
				peer.Halves = append(peer.Halves, Half{RecordID: record.RecordID, Seq: record.Seq, Covered: in.Peer.Covers(record.Seq)})
				continue
			}
		}
		if !isHalf(*record.Header, in.RecordType) {
			continue
		}
		peer.Halves = append(peer.Halves, Half{
			RecordID: record.RecordID, Seq: record.Seq, Correlation: *record.Header.Correlation,
			PayloadCommitments: record.Header.PayloadCommitments,
			Covered:            in.Peer.Covers(record.Seq), Trusted: record.CapsuleOK && record.HeaderVerified,
		})
	}
	out.PeerComplete = peer.Complete
	out.Pairs, out.Tallies = ReconcileHalves(own, peer, in.Compare)
	return out, nil
}

func isHalf(h Header, recordType string) bool {
	return h.Correlation != nil && (recordType == "" || h.RecordType == recordType)
}

// CloseStatement is the body of a Close record.
type CloseStatement struct {
	Counterparty   string         `json:"counterparty"`
	Profile        string         `json:"profile"`
	Reconciliation Reconciliation `json:"reconciliation"`
	Reason         string         `json:"reason,omitempty"`
}

// CloseInput states what a Close binds. The book computes the
// reconciliation itself from Reconcile; a caller never supplies tallies.
// Supersedes names a prior Close this one adjusts; the prior Close is never
// rewritten.
type CloseInput struct {
	Reconcile    ReconcileInput
	Counterparty string
	// Profile names the contract or profile version whose comparator and
	// windows produced the reconciliation.
	Profile    string
	Supersedes string
	Reason     string
}

// Close reconciles, then seals a Close record: a closes link to every own
// record the reconciliation placed, and the full state population in its
// statement. Its status is read later from the links other records make to it.
func (b *Book) Close(ctx context.Context, in CloseInput) (Record, error) {
	if in.Counterparty == "" || in.Profile == "" {
		return Record{}, fmt.Errorf("%w: a Close names its counterparty and profile", ErrInvalid)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
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
	body, err := json.Marshal(CloseStatement{Counterparty: in.Counterparty, Profile: in.Profile, Reconciliation: recon, Reason: in.Reason})
	if err != nil {
		return Record{}, err
	}
	return b.appendLocked(ctx, Entry{RecordType: RecordTypeClose, EpistemicType: DerivedMetric, Links: links, CounterpartyRef: in.Counterparty, Statement: body})
}

// Acknowledge records that this book has seen and holds a counterparty's
// Close, which is what makes that Close AGREED from the counterparty's side.
func (b *Book) Acknowledge(ctx context.Context, peerCloseID, counterparty string) (Record, error) {
	return b.Append(ctx, Entry{RecordType: RecordTypeAcknowledgement, EpistemicType: ProducerClaim, CounterpartyRef: counterparty, Links: []Link{{Type: Acknowledges, Target: peerCloseID}}})
}

// Rebut records that this book disputes a counterparty's Close.
func (b *Book) Rebut(ctx context.Context, peerCloseID, counterparty string, reason json.RawMessage) (Record, error) {
	return b.Append(ctx, Entry{RecordType: RecordTypeRebuttal, EpistemicType: ProducerClaim, CounterpartyRef: counterparty, Links: []Link{{Type: Rebuts, Target: peerCloseID}}, Statement: reason})
}

// CloseStatus is read from links, never from a field the Close sets.
type CloseStatus string

const (
	Agreed     CloseStatus = "AGREED"
	Unilateral CloseStatus = "UNILATERAL"
	Contested  CloseStatus = "CONTESTED"
)

// StatusOfClose derives a Close's status from the counterparty's own
// records: a rebuts link makes it CONTESTED, an acknowledges link AGREED,
// and neither leaves it UNILATERAL. Only records that verified, carry the
// counterparty's book id, and sit under a checkpoint signed by the key the
// caller pinned for that counterparty count; a bundle from anyone else says
// nothing about the Close.
func StatusOfClose(closeID, counterpartyBookID, counterpartyCheckpointKey string, counterparty VerifiedBundle) (CloseStatus, error) {
	if !counterparty.AnchorAuthenticated || counterpartyCheckpointKey == "" || counterparty.AnchorKeyID != counterpartyCheckpointKey {
		return "", fmt.Errorf("%w: the bundle is not under the counterparty's pinned checkpoint key", ErrInvalid)
	}
	status := Unilateral
	for _, record := range counterparty.Records {
		if record.Header == nil || !record.HeaderVerified || !record.CapsuleOK || record.Header.BookID != counterpartyBookID || !counterparty.Covers(record.Seq) {
			continue
		}
		if slices.Contains(record.Header.LinksTo(Rebuts), closeID) {
			return Contested, nil
		}
		if slices.Contains(record.Header.LinksTo(Acknowledges), closeID) {
			status = Agreed
		}
	}
	return status, nil
}
