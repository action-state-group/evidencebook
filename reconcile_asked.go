// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"slices"
)

// AskedState is what one of the requester's own halves comes to after it
// asked the provider for that exchange (evidencebook#206 row 9).
type AskedState string

const (
	// AskedLogged: a verified answer carried a record of the provider's that
	// correlates with the half.
	AskedLogged AskedState = "LOGGED"
	// AskedOmitted: the provider confirmed it holds no record of the
	// exchange. Either a verified answer that should have carried the record
	// carried none that correlates, or a verified refusal with the reason
	// no_such_subject.
	AskedOmitted AskedState = "OMITTED"
	// AskedRefused: the provider refused, verified and signed, with a reason
	// other than no_such_subject. It says nothing about whether the record
	// exists (a uniform policy_declined reads the same either way), but it is a
	// finding of its own: an honest provider does not refuse its counterparty
	// about their own exchange.
	AskedRefused AskedState = "REFUSED"
	// AskedUnresolved: nothing says either way: an absence, an answer that
	// failed verification, or an answer that cannot be about this exchange.
	AskedUnresolved AskedState = "UNRESOLVED"
)

// AskedVerdict is the reconcile's verdict over one pair. It is never
// COMPLETE: a remote answer is the provider's selection (#206 row 7), so the
// most the requester can say is that none of its own halves was left out.
type AskedVerdict string

const (
	// OmissionsFound: at least one half is AskedOmitted.
	OmissionsFound AskedVerdict = "OMISSIONS_FOUND"
	// NoOmissionFound: every half is AskedLogged.
	NoOmissionFound AskedVerdict = "NO_OMISSION_FOUND"
	// AskedInconclusive: no half is AskedOmitted and at least one is
	// AskedRefused or AskedUnresolved.
	AskedInconclusive AskedVerdict = "UNRESOLVED"
)

// AskOutcome is how the provider answered the request for one exchange.
type AskOutcome string

const (
	// AskAnswered: a verified answer, with the records it carried.
	AskAnswered AskOutcome = "answered"
	// AskRefused: a verified, signed refusal, with its reason.
	AskRefused AskOutcome = "refused"
	// AskAbsent: nothing came back.
	AskAbsent AskOutcome = "absent"
	// AskFailed: something came back and did not verify.
	AskFailed AskOutcome = "failed"
)

// AskedHalf is one of the requester's own sealed halves of an exchange with
// the provider, named by the exchange's two digests. Those are what the ask
// names (the exchange binding `exchange-digests:<request>:<response>`) and
// what both halves of one exchange commit to. An exchange_id is not: each
// side's host assigns its own.
type AskedHalf struct {
	RecordID       string `json:"record_id"`
	RequestDigest  string `json:"request_digest"`
	ResponseDigest string `json:"response_digest"`
}

// AskedRecord is one record a verified answer carried: the provider's half of
// an exchange with the requester, with its per-pair sequence number and the
// one before it (nil for the first record of the pair).
type AskedRecord struct {
	RecordID       string  `json:"record_id"`
	RequestDigest  string  `json:"request_digest"`
	ResponseDigest string  `json:"response_digest"`
	Seq            uint64  `json:"seq"`
	PrevSeq        *uint64 `json:"prev_seq"`
}

// AskedExchange is one of the requester's own halves and what came back when
// it asked the provider for that exchange.
type AskedExchange struct {
	Own     AskedHalf  `json:"own"`
	Outcome AskOutcome `json:"outcome"`
	// Covers is the caller's statement that the answer or refusal is made
	// at an anchor that would hold the provider's record of this exchange if
	// the provider had sealed it: the anchor postdates the exchange and every
	// record the provider sealed for it. Without it, an answer missing the
	// record, or a no_such_subject, may only mean the anchor is too old, so
	// the half is UNRESOLVED.
	Covers bool `json:"covers"`
	// Records holds what an AskAnswered answer carried.
	Records []AskedRecord `json:"records,omitempty"`
	// Reason holds an AskRefused refusal's reason, as signed.
	Reason string `json:"reason,omitempty"`
}

// AskedResult is one half's reconcile result.
type AskedResult struct {
	State AskedState `json:"state"`
	// Own is the requester's half (its RecordID).
	Own string `json:"own"`
	// Record is the provider's record it matched (its RecordID), when LOGGED.
	Record string `json:"record,omitempty"`
	// Why says what decided OMITTED or UNRESOLVED.
	Why string `json:"why,omitempty"`
}

// SeqRun is the provider's per-pair numbering across the records it matched,
// in sequence order.
type SeqRun struct {
	Seqs []uint64 `json:"seqs"`
	// Gapless: each number is one more than the one before, and each
	// record's PrevSeq names the one before it. An empty or one-record run
	// is gapless when its first PrevSeq is unset or one less than its Seq.
	Gapless bool `json:"gapless"`
}

// AskedReport is the reconcile over one pair.
type AskedReport struct {
	Verdict AskedVerdict  `json:"verdict"`
	Results []AskedResult `json:"results"`
	// Omitted names the requester's halves the provider confirmed it holds
	// no record of, in input order.
	Omitted []string `json:"omitted"`
	// Run is the numbering of the provider's matched records. A gapless run
	// says no sealed record of theirs was removed between the first and the
	// last; it says nothing about exchanges they never sealed, which only
	// Omitted shows.
	Run SeqRun `json:"run"`
}

// ReconcileAsked checks the provider's answers about a pair against the
// requester's own sealed halves of that pair, one ask per half, and names
// every half the provider confirmed it holds no record of
// (evidencebook#206 row 9: an exchange left out before sealing, whose
// answers still verify and number without a gap).
//
// It trusts the caller's verification and does not repeat it: Outcome must
// be AskAnswered or AskRefused only for an answer whose signature, anchor and
// inclusion proofs the caller checked under the provider's key, Records must
// hold only records that answer proved, and Covers must be set only when the
// answer's anchor would hold the record had it been sealed. A half is OMITTED
// only on such a covering answer: a verified answer without the record, or a
// signed no_such_subject. A record matches a half when
// both its request and response digests are the half's; only the records of
// that half's own answer are candidates, and each record matches at most one
// half, so two exchanges with the same request and answer never both match
// one record. The matching is a maximum one, so how many halves are OMITTED
// never depends on the order of the input; which of several equal halves is,
// when there are fewer records than halves, does.
//
// It works only on bilateral traffic: a half the requester never sealed
// cannot be asked about, so an exchange both sides left out stays invisible
// (Mesh-LLM/mesh-llm#1965 §6). The verdict strings are local results, never
// written into a sealed record or onto the wire.
func ReconcileAsked(asked []AskedExchange) AskedReport {
	report := AskedReport{Results: make([]AskedResult, 0, len(asked)), Omitted: []string{}}
	match := matchAsked(asked)
	var matched []AskedRecord
	unresolved := false
	for i, a := range asked {
		r := AskedResult{Own: a.Own.RecordID}
		switch {
		case a.Own.RequestDigest == "" || a.Own.ResponseDigest == "":
			// The ask names the exchange by these two digests, so with either
			// missing no answer can be about this exchange.
			r.State, r.Why = AskedUnresolved, "the requester's half names no request or response digest to ask by"
		case a.Outcome == AskAnswered:
			switch j := match[i]; {
			case j >= 0:
				rec := a.Records[j]
				matched = append(matched, rec)
				r.State, r.Record = AskedLogged, rec.RecordID
			case unnamedMatch(a):
				r.State, r.Why = AskedUnresolved, "a record in the answer matches but carries no record id"
			case !a.Covers:
				r.State, r.Why = AskedUnresolved, "the answer's anchor is not stated to cover this exchange"
			default:
				r.State, r.Why = AskedOmitted, "the verified answer carries no record of this exchange"
			}
		case a.Outcome == AskRefused && a.Reason == string(ReasonNoSuchSubject):
			if a.Covers {
				r.State, r.Why = AskedOmitted, "the provider signed no_such_subject for this exchange"
			} else {
				r.State, r.Why = AskedUnresolved, "no_such_subject at an anchor not stated to cover this exchange"
			}
		case a.Outcome == AskRefused:
			r.State, r.Why = AskedRefused, "refused, signed, reason "+a.Reason+": it does not say whether the record exists"
		case a.Outcome == AskAbsent:
			r.State, r.Why = AskedUnresolved, "no answer came back"
		default:
			r.State, r.Why = AskedUnresolved, "the answer did not verify"
		}
		switch r.State {
		case AskedOmitted:
			report.Omitted = append(report.Omitted, r.Own)
		case AskedUnresolved, AskedRefused:
			unresolved = true
		}
		report.Results = append(report.Results, r)
	}
	switch {
	case len(report.Omitted) > 0:
		report.Verdict = OmissionsFound
	case unresolved || len(asked) == 0:
		report.Verdict = AskedInconclusive
	default:
		report.Verdict = NoOmissionFound
	}
	report.Run = seqRun(matched)
	return report
}

// matchAsked pairs answered halves with records, one record (by RecordID) to
// at most one half and only from the half's own answer, and returns, per
// half, the index in its Records of the record it got, or -1. It finds a
// maximum matching (augmenting paths), so no half is left unmatched while a
// reassignment could match it: how many halves match never depends on the
// order of the input. Halves are matched in input order, so when equal
// exchanges outnumber the records, the earlier halves get them.
func matchAsked(asked []AskedExchange) []int {
	cands := make([][]int, len(asked))
	for i, a := range asked {
		if a.Outcome != AskAnswered || a.Own.RequestDigest == "" || a.Own.ResponseDigest == "" {
			continue
		}
		for j, rec := range a.Records {
			if rec.RecordID != "" && rec.RequestDigest == a.Own.RequestDigest && rec.ResponseDigest == a.Own.ResponseDigest {
				cands[i] = append(cands[i], j)
			}
		}
	}
	match := make([]int, len(asked))
	for i := range match {
		match[i] = -1
	}
	owner := map[string]int{} // RecordID -> half
	var augment func(i int, seen map[string]bool) bool
	augment = func(i int, seen map[string]bool) bool {
		for _, j := range cands[i] {
			id := asked[i].Records[j].RecordID
			if seen[id] {
				continue
			}
			seen[id] = true
			if k, taken := owner[id]; !taken || augment(k, seen) {
				owner[id], match[i] = i, j
				return true
			}
		}
		return false
	}
	for i := range asked {
		augment(i, map[string]bool{})
	}
	return match
}

// unnamedMatch reports whether a's answer carries a record with a's two
// digests and no RecordID, which no matching can use.
func unnamedMatch(a AskedExchange) bool {
	for _, rec := range a.Records {
		if rec.RecordID == "" && rec.RequestDigest == a.Own.RequestDigest && rec.ResponseDigest == a.Own.ResponseDigest {
			return true
		}
	}
	return false
}

func seqRun(records []AskedRecord) SeqRun {
	sorted := slices.Clone(records)
	slices.SortFunc(sorted, func(a, b AskedRecord) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		}
		return 0
	})
	run := SeqRun{Seqs: make([]uint64, 0, len(sorted)), Gapless: true}
	for i, rec := range sorted {
		run.Seqs = append(run.Seqs, rec.Seq)
		if i == 0 {
			if rec.PrevSeq != nil && *rec.PrevSeq+1 != rec.Seq {
				run.Gapless = false
			}
			continue
		}
		prev := sorted[i-1].Seq
		if rec.Seq != prev+1 || rec.PrevSeq == nil || *rec.PrevSeq != prev {
			run.Gapless = false
		}
	}
	return run
}
