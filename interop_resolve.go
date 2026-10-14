// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"slices"
	"time"
)

// anchorView is one coverage anchor a responder has issued: its digest, the
// number of log entries it covers, and when it was issued.
type anchorView struct {
	digest   string
	entries  uint64
	issuedAt time.Time
}

// evidenceView is what resolution reads. A Book is one; the request-draft
// resolution vectors declare another, so the decision below is checked
// against them directly (resolution_vectors_test.go).
type evidenceView interface {
	// anchors are the anchors issued, oldest first.
	anchors() []anchorView
	// logEntries is the number of entries the log holds now, covered or not.
	logEntries() uint64
	// leafOf is the zero-based position of a servable record.
	leafOf(digest string) (uint64, bool)
	// servable reports whether the record at leaf may be served at all.
	servable(leaf uint64) bool
	// correlated and citing are the positions of the servable records
	// carrying a correlation identifier, or citing an exchange half.
	correlated(id string) []uint64
	citing(half string) []uint64
	// lapsed reports a retention commitment for exactly this subject whose
	// until has passed (ER §9).
	lapsed(subject Subject, now time.Time) bool
}

// resolution is the decision for one well-formed, authorized request: the
// anchor (an index into anchors()), whether the requester pinned its own
// exchange half, the records served, or the refusal reason. needAnchor
// asks the caller to issue a fresh anchor and resolve again.
type resolution struct {
	anchor     int
	halfPin    bool
	leaves     []uint64
	reason     string
	needAnchor bool
}

// anchorMeets reports whether an anchor attains a min_freshness floor: at
// least size entries, or issued no earlier than floorTime as the envelope
// states it (seconds precision, profile §5.2).
func anchorMeets(a anchorView, floor *Freshness, floorTime time.Time) bool {
	if floor.Size != nil {
		return a.entries >= *floor.Size
	}
	return !a.issuedAt.UTC().Truncate(time.Second).Before(floorTime)
}

// resolveCoverage picks the anchor (ER §3.2, profile §6.1 step 6). A pin
// must name an anchor this responder issued, or, for an exchange subject,
// the requester's own exchange half. A freshness floor is met by the newest
// anchor that meets it; when none does, mayIssue says whether the responder
// will do the work of issuing one.
func resolveCoverage(view evidenceView, req EvidenceRequest, floorTime, now time.Time, mayIssue bool) resolution {
	anchors := view.anchors()
	if pin := req.Coverage.ExpectedPin; pin != "" {
		for i, a := range anchors {
			if a.digest == pin {
				return resolution{anchor: i}
			}
		}
		if req.Subject.Kind == SubjectExchange && pin == req.Subject.Digest {
			return resolution{anchor: -1, halfPin: true}
		}
		return resolution{reason: ReasonCoverageUnsatisfiable}
	}
	floor := req.Coverage.MinFreshness
	for i := len(anchors) - 1; i >= 0; i-- {
		if anchorMeets(anchors[i], floor, floorTime) {
			return resolution{anchor: i}
		}
	}
	// No anchor meets the floor. A floor no anchor could meet yet is refused
	// without doing any work: a time in the future, or more entries than the
	// log holds.
	if floor.Size != nil && *floor.Size > view.logEntries() || floor.Size == nil && floorTime.After(now) {
		return resolution{reason: ReasonCoverageUnsatisfiable}
	}
	if !mayIssue || view.logEntries() == 0 {
		return resolution{reason: ReasonCoverageUnsatisfiable}
	}
	return resolution{needAnchor: true}
}

// resolveSubject finds the servable records the subject names under the
// anchor (ER §3.1). A subject that exists only beyond the anchor is
// coverage_unsatisfiable, never no_such_subject: the responder does hold it.
// Matching is exact, never by prefix or case folding.
func resolveSubject(view evidenceView, req EvidenceRequest, r resolution, now time.Time) resolution {
	anchors := view.anchors()
	if r.halfPin {
		// The requester pinned its own half: the anchor is the earliest one
		// covering every anchored record that cites it, so growth that adds
		// no citing record never changes the artifact (ER §3.2, §5).
		cited := view.citing(req.Subject.Digest)
		if len(cited) == 0 {
			return withRetention(view, req, now, resolution{reason: ReasonNoSuchSubject})
		}
		latest := uint64(0)
		if n := len(anchors); n > 0 {
			latest = anchors[n-1].entries
		}
		var last uint64
		anchored := false
		for _, leaf := range cited {
			if leaf < latest {
				last, anchored = max(last, leaf), true
			}
		}
		if !anchored {
			return resolution{reason: ReasonCoverageUnsatisfiable}
		}
		for i, a := range anchors {
			if a.entries > last {
				r.anchor = i
				break
			}
		}
	}
	covered := anchors[r.anchor].entries
	switch req.Subject.Kind {
	case SubjectCheckpoints:
		return r
	case SubjectFullHistory:
		for leaf := uint64(0); leaf < covered; leaf++ {
			if view.servable(leaf) {
				r.leaves = append(r.leaves, leaf)
			}
		}
		if len(r.leaves) == 0 && req.Derivation == "" {
			r.reason = ReasonNoSuchSubject
		}
		return r
	case SubjectRecord:
		leaf, held := view.leafOf(req.Subject.Digest)
		switch {
		case !held:
			return withRetention(view, req, now, resolution{reason: ReasonNoSuchSubject})
		case leaf >= covered:
			return resolution{reason: ReasonCoverageUnsatisfiable}
		}
		r.leaves = []uint64{leaf}
		return r
	case SubjectRange:
		a, b := req.Subject.Range[0], req.Subject.Range[1]
		if b >= covered {
			if b < view.logEntries() {
				return resolution{reason: ReasonCoverageUnsatisfiable}
			}
			return withRetention(view, req, now, resolution{reason: ReasonNoSuchSubject})
		}
		for leaf := a; leaf <= b; leaf++ {
			if view.servable(leaf) {
				r.leaves = append(r.leaves, leaf)
			}
		}
		if len(r.leaves) == 0 {
			return withRetention(view, req, now, resolution{reason: ReasonNoSuchSubject})
		}
		return r
	case SubjectCorrelation, SubjectExchange:
		var all []uint64
		if req.Subject.Kind == SubjectCorrelation {
			all = view.correlated(req.Subject.Correlation)
		} else {
			all = view.citing(req.Subject.Digest)
		}
		for _, leaf := range all {
			if leaf < covered {
				r.leaves = append(r.leaves, leaf)
			}
		}
		switch {
		case len(r.leaves) > 0:
			slices.Sort(r.leaves)
			r.leaves = slices.Compact(r.leaves)
			return r
		case len(all) > 0:
			return resolution{reason: ReasonCoverageUnsatisfiable}
		}
		return withRetention(view, req, now, resolution{reason: ReasonNoSuchSubject})
	}
	return resolution{reason: ReasonRequestMalformed}
}

// withRetention turns no_such_subject into retention_expired when the
// responder once committed to answer exactly this subject and the
// commitment has lapsed, so a lapsed promise never reads as never having
// held the evidence (ER §4.2 item 2, §9).
func withRetention(view evidenceView, req EvidenceRequest, now time.Time, r resolution) resolution {
	if r.reason == ReasonNoSuchSubject && view.lapsed(req.Subject, now) {
		r.reason = ReasonRetentionExpired
	}
	return r
}
