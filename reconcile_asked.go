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
	// AskedUnresolved: nothing says either way. Any other refusal (a uniform
	// policy_declined or a not_authorized reads the same whether the record
	// exists or not), an absence, or an answer that failed verification.
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
	// AskedUnresolved.
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
// inclusion proofs the caller checked under the provider's key, and Records
// must hold only records that answer proved. A record matches a half when
// both its request and response digests are the half's; only the records of
// that half's own answer are candidates, and each record matches at most one
// half, so two exchanges with the same request and answer never both match
// one record.
//
// It works only on bilateral traffic: a half the requester never sealed
// cannot be asked about, so an exchange both sides left out stays invisible
// (Mesh-LLM/mesh-llm#1965 §6). The verdict strings are local results, never
// written into a sealed record or onto the wire.
func ReconcileAsked(asked []AskedExchange) AskedReport {
	report := AskedReport{Results: make([]AskedResult, 0, len(asked)), Omitted: []string{}}
	used := map[string]bool{}
	var matched []AskedRecord
	unresolved := false
	for _, a := range asked {
		r := AskedResult{Own: a.Own.RecordID}
		switch a.Outcome {
		case AskAnswered:
			if a.Own.RequestDigest == "" || a.Own.ResponseDigest == "" {
				r.State, r.Why = AskedUnresolved, "the requester's half names no request or response digest to match on"
			} else if i := matchAsked(a.Own, a.Records, used); i >= 0 {
				rec := a.Records[i]
				used[rec.RecordID] = true
				matched = append(matched, rec)
				r.State, r.Record = AskedLogged, rec.RecordID
			} else {
				r.State, r.Why = AskedOmitted, "the verified answer carries no record of this exchange"
			}
		case AskRefused:
			if a.Reason == string(ReasonNoSuchSubject) {
				r.State, r.Why = AskedOmitted, "the provider signed no_such_subject for this exchange"
			} else {
				r.State, r.Why = AskedUnresolved, "refused, reason "+a.Reason+": it reads the same whether the record exists or not"
			}
		case AskAbsent:
			r.State, r.Why = AskedUnresolved, "no answer came back"
		default:
			r.State, r.Why = AskedUnresolved, "the answer did not verify"
		}
		switch r.State {
		case AskedOmitted:
			report.Omitted = append(report.Omitted, r.Own)
		case AskedUnresolved:
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

// matchAsked returns the index of the first record not yet used whose
// request and response digests are own's, or -1.
func matchAsked(own AskedHalf, records []AskedRecord, used map[string]bool) int {
	for i, rec := range records {
		if !used[rec.RecordID] && rec.RequestDigest == own.RequestDigest && rec.ResponseDigest == own.ResponseDigest {
			return i
		}
	}
	return -1
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
