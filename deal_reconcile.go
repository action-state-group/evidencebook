// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The Deal Reconcile Contract, ec:deal-reconcile@1 (agent-action-capsule
// spec/deal-reconcile-contract-v0.md, cited here as DR): two books' halves of
// a deal are joined on the deal id, and for each of two requirements the
// field's commitments are opened and compared. RecomputeDealReconcile is the
// recompute DR §7.1 requires of a verifier. It decides each pairing with
// ReconcileHalvesWith on halves projected onto the requirement's one field,
// and adds around that call only what DR states beyond it: rule 1, rule 3's
// precedence and the §6.1 set status.

// DealReconcileContractRef is the contract_ref a claim under this contract
// carries.
const DealReconcileContractRef = "ec:deal-reconcile@1"

// The six abstract half members a half mapping fills (DR §4.1).
const (
	HalfMemberDealID             = "deal_id"
	HalfMemberBookID             = "book_id"
	HalfMemberRecordedAt         = "recorded_at"
	HalfMemberCommitAlg          = "commit_alg"
	HalfMemberAmountCommitment   = "amount_commitment"
	HalfMemberCurrencyCommitment = "currency_commitment"
)

var halfMembers = []string{HalfMemberDealID, HalfMemberBookID, HalfMemberRecordedAt, HalfMemberCommitAlg, HalfMemberAmountCommitment, HalfMemberCurrencyCommitment}

// dealRequirement is one of the contract's two requirements: the field it
// compares, the commitment member that holds that field, and the claim id
// the Result gives it.
type dealRequirement struct {
	id, field, commitment, claimID string
}

var dealRequirements = []dealRequirement{
	{"amount-commitment-matched", "amount", HalfMemberAmountCommitment, "reconcile-amount"},
	{"currency-commitment-matched", "currency", HalfMemberCurrencyCommitment, "reconcile-currency"},
}

// HalfMapping is a profile's declared mapping onto the abstract members
// (DR §4.2): each member is a JSON Pointer (RFC 6901) into the half.
type HalfMapping struct {
	Profile string            `json:"profile"`
	Members map[string]string `json:"members"`
}

// DealHalf is one half as the reconcile is given it: its octets as held and
// the book it comes from.
type DealHalf struct {
	Book   string
	Octets []byte
}

// ResultPeriod is a reconcile's period [start, end), in RFC 3339 UTC with Z
// and seconds precision.
type ResultPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// CoverageGiven is a coverage declaration given for one book, with the
// period it is given for and the verifier's report of its coverage
// properties (IP §6.7.2), by property name.
type CoverageGiven struct {
	Period      ResultPeriod      `json:"period"`
	Declaration map[string]any    `json:"declaration"`
	Properties  map[string]string `json:"properties"`
}

// DealReconcileInput is everything a reconcile under the contract reads.
type DealReconcileInput struct {
	// SideA is the book that produces the Result; Peer is side B.
	SideA, Peer string
	Period      ResultPeriod
	// HalfMappings is each book's half mapping, by book id.
	HalfMappings map[string]HalfMapping
	Halves       []DealHalf
	// FieldOpenings are by half digest, then by field name (DR §5).
	FieldOpenings map[string]map[string]Opening
	// Coverage is the coverage given for a book, by book id; a book with
	// none given is NOT_SHOWN.
	Coverage map[string]CoverageGiven
	// GeneratedAt is the Result's generated_at.
	GeneratedAt string
}

// Set statuses (DR §6.1).
const (
	SetComplete  = "COMPLETE"
	SetUndecided = "UNDECIDED"
	SetNotShown  = "NOT_SHOWN"
)

// SetCompleteness is a side's §6.1 status and its declared kind, null when
// no declaration is given.
type SetCompleteness struct {
	Status string  `json:"status"`
	Kind   *string `json:"kind"`
}

// ResultTallies are a reconcile claim's six tallies (RES §4.1).
type ResultTallies struct {
	Matched      int `json:"matched"`
	AOnly        int `json:"a_only"`
	BOnly        int `json:"b_only"`
	Conflicting  int `json:"conflicting"`
	Insufficient int `json:"insufficient"`
	Unresolved   int `json:"unresolved"`
}

// EvidenceRef is evidence by digest only (RES §5).
type EvidenceRef struct {
	Digest    string `json:"digest"`
	DigestAlg string `json:"digest_alg"`
}

// ResultPresentation is a claim's disclosure carrier (RES §2).
type ResultPresentation struct {
	Kind     string        `json:"kind"`
	Status   string        `json:"status"`
	Evidence []EvidenceRef `json:"evidence"`
}

// ReconcileBody is the RES §4.1 reconcile body.
type ReconcileBody struct {
	JoinKey       string        `json:"join_key"`
	Peer          string        `json:"peer"`
	Period        ResultPeriod  `json:"period"`
	Tallies       ResultTallies `json:"tallies"`
	StateOfRecord string        `json:"state_of_record"`
}

// ReconcileClaim is a type: reconcile claim citing the contract. It has no
// member that cites a Close (DR §7.4).
type ReconcileClaim struct {
	ID             string             `json:"id"`
	Type           string             `json:"type"`
	ContractRef    string             `json:"contract_ref"`
	RequirementRef string             `json:"requirement_ref"`
	Tier           string             `json:"tier"`
	Grade          string             `json:"grade"`
	Sufficiency    string             `json:"sufficiency"`
	Verdict        string             `json:"verdict"`
	Evidence       []EvidenceRef      `json:"evidence"`
	Proofs         []EvidenceRef      `json:"proofs"`
	Presentation   ResultPresentation `json:"presentation"`
	Reconcile      ReconcileBody      `json:"reconcile"`
}

// ResultAggregate is the Result's coverage statement and buckets (RES §7).
type ResultAggregate struct {
	Coverage struct {
		EvaluatedPopulation   int `json:"evaluated_population"`
		ExcludedNotApplicable int `json:"excluded_not_applicable"`
		UnknownCount          int `json:"unknown_count"`
	} `json:"coverage"`
	Buckets struct {
		Met          []string `json:"met"`
		NotMet       []string `json:"not_met"`
		NotEvaluable []string `json:"not_evaluable"`
	} `json:"buckets"`
}

// DealReconcileResult is an Evidence Result v0 holding the two claims.
type DealReconcileResult struct {
	ResultVersion string           `json:"result_version"`
	GeneratedAt   string           `json:"generated_at"`
	Claims        []ReconcileClaim `json:"claims"`
	Aggregate     ResultAggregate  `json:"aggregate"`
}

// DealReconcile is the recompute's full outcome: every intermediate DR
// names, by requirement id where it is per requirement.
type DealReconcile struct {
	// FieldOpenings is each carried opening's status, PASS or FAIL, by half
	// digest and field, for the halves in the reconcile.
	FieldOpenings   map[string]map[string]string        `json:"field_openings"`
	SetCompleteness map[string]SetCompleteness          `json:"set_completeness"`
	Pairings        map[string]map[string]ExchangeState `json:"pairings"`
	Tallies         map[string]ResultTallies            `json:"tallies"`
	Sufficiency     map[string]string                   `json:"sufficiency"`
	Verdict         map[string]string                   `json:"verdict"`
	// DealState is DR §7.5's optional per-deal state; a caller may leave it
	// unshown.
	DealState map[string]string   `json:"deal_state"`
	Result    DealReconcileResult `json:"result"`
}

var resultTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

func parseResultTime(s string) (time.Time, bool) {
	if !resultTime.MatchString(s) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// mappedHalf is a half read through its book's mapping.
type mappedHalf struct {
	digest, dealID, book string
	recordedAt           time.Time
	commitAlg            string // "" when absent or not the contract's
	commitments          map[string]string
}

// RecomputeDealReconcile recomputes a reconcile under ec:deal-reconcile@1.
// A malformed input, or a half malformed by DR §4.2, is an error: the claim
// fails, and no Result is produced.
func RecomputeDealReconcile(in DealReconcileInput) (DealReconcile, error) {
	if in.SideA == "" || in.Peer == "" || in.SideA == in.Peer {
		return DealReconcile{}, fmt.Errorf("%w: a deal reconcile names two different books", ErrInvalid)
	}
	start, okStart := parseResultTime(in.Period.Start)
	end, okEnd := parseResultTime(in.Period.End)
	if !okStart || !okEnd || !start.Before(end) {
		return DealReconcile{}, fmt.Errorf("%w: a deal reconcile's period is [start, end) in RFC 3339 UTC seconds, start before end", ErrInvalid)
	}
	if _, ok := parseResultTime(in.GeneratedAt); !ok {
		return DealReconcile{}, fmt.Errorf("%w: generated_at is RFC 3339 UTC seconds", ErrInvalid)
	}
	var halves []mappedHalf
	for i, h := range in.Halves {
		m, err := mapHalf(h, in.HalfMappings[h.Book])
		if err != nil {
			return DealReconcile{}, fmt.Errorf("half %d: %w", i, err)
		}
		// The reconcile's halves are side A's and side B's whose
		// recorded_at lies in the period (DR §6).
		if (m.book != in.SideA && m.book != in.Peer) || m.recordedAt.Before(start) || !m.recordedAt.Before(end) {
			continue
		}
		halves = append(halves, m)
	}
	out := DealReconcile{
		FieldOpenings:   map[string]map[string]string{},
		SetCompleteness: map[string]SetCompleteness{in.SideA: setCompleteness(in.Coverage, in.SideA, in.Period), in.Peer: setCompleteness(in.Coverage, in.Peer, in.Period)},
		Pairings:        map[string]map[string]ExchangeState{},
		Tallies:         map[string]ResultTallies{},
		Sufficiency:     map[string]string{},
		Verdict:         map[string]string{},
		DealState:       map[string]string{},
	}
	for _, h := range halves {
		for field, opening := range in.FieldOpenings[h.digest] {
			commitment, ok := commitmentFor(h, field)
			status := "FAIL"
			if ok && VerifyOpening(commitment, opening) {
				status = "PASS"
			}
			if out.FieldOpenings[h.digest] == nil {
				out.FieldOpenings[h.digest] = map[string]string{}
			}
			out.FieldOpenings[h.digest][field] = status
		}
	}
	deals := map[string][2][]mappedHalf{}
	for _, h := range halves {
		sides := deals[h.dealID]
		if h.book == in.SideA {
			sides[0] = append(sides[0], h)
		} else {
			sides[1] = append(sides[1], h)
		}
		deals[h.dealID] = sides
	}
	dealIDs := slices.Sorted(func(yield func(string) bool) {
		for id := range deals {
			if !yield(id) {
				return
			}
		}
	})
	statusA, statusB := out.SetCompleteness[in.SideA].Status, out.SetCompleteness[in.Peer].Status
	for _, req := range dealRequirements {
		states := map[string]ExchangeState{}
		var tallies ResultTallies
		for _, id := range dealIDs {
			sides := deals[id]
			state := pairState(req, sides[0], sides[1], statusA, statusB, in.FieldOpenings)
			states[id] = state
			tallies.add(state)
		}
		out.Pairings[req.id] = states
		out.Tallies[req.id] = tallies
		out.Sufficiency[req.id], out.Verdict[req.id] = tallies.judge()
	}
	for _, id := range dealIDs {
		out.DealState[id] = "not_reconciled"
		if out.Pairings[dealRequirements[0].id][id] == Matched && out.Pairings[dealRequirements[1].id][id] == Matched {
			out.DealState[id] = "reconciled"
		}
	}
	out.Result = buildDealResult(in, halves, out)
	return out, nil
}

// mapHalf reads a half through its book's mapping (DR §4.2). deal_id,
// book_id and recorded_at must be present and of their form, and book_id
// must be the book the half comes from; an absent or ill-formed commit_alg
// or commitment is read as absent.
func mapHalf(h DealHalf, mapping HalfMapping) (mappedHalf, error) {
	if mapping.Profile == "" || len(mapping.Members) != len(halfMembers) {
		return mappedHalf{}, fmt.Errorf("%w: book %q has no half mapping of the six members", ErrInvalid, h.Book)
	}
	for _, name := range halfMembers {
		if _, ok := mapping.Members[name]; !ok {
			return mappedHalf{}, fmt.Errorf("%w: book %q's half mapping has no %s", ErrInvalid, h.Book, name)
		}
	}
	doc, err := decodeJSON(h.Octets)
	if err != nil {
		return mappedHalf{}, fmt.Errorf("%w: a half is not one JSON value: %v", ErrInvalid, err)
	}
	text := func(member string) (string, bool, error) {
		v, found, err := resolvePointer(doc, mapping.Members[member])
		if err != nil || !found {
			return "", false, err
		}
		s, ok := v.(string)
		return s, ok, nil
	}
	m := mappedHalf{digest: digestBytes(h.Octets), commitments: map[string]string{}}
	var ok bool
	if m.dealID, ok, err = text(HalfMemberDealID); err != nil || !ok || m.dealID == "" {
		return mappedHalf{}, malformedHalf(err, HalfMemberDealID)
	}
	if m.book, ok, err = text(HalfMemberBookID); err != nil || !ok || m.book == "" {
		return mappedHalf{}, malformedHalf(err, HalfMemberBookID)
	}
	if m.book != h.Book {
		return mappedHalf{}, fmt.Errorf("%w: a half's book_id %q is not the book %q it comes from", ErrInvalid, m.book, h.Book)
	}
	recorded, ok, err := text(HalfMemberRecordedAt)
	if err != nil || !ok {
		return mappedHalf{}, malformedHalf(err, HalfMemberRecordedAt)
	}
	if m.recordedAt, ok = parseResultTime(recorded); !ok {
		return mappedHalf{}, malformedHalf(nil, HalfMemberRecordedAt)
	}
	if alg, ok, err := text(HalfMemberCommitAlg); err != nil {
		return mappedHalf{}, malformedHalf(err, HalfMemberCommitAlg)
	} else if ok && alg == CommitAlgNonce256 {
		m.commitAlg = alg
	}
	for _, req := range dealRequirements {
		c, ok, err := text(req.commitment)
		if err != nil {
			return mappedHalf{}, malformedHalf(err, req.commitment)
		}
		if ok && hex64.MatchString(c) {
			m.commitments[req.field] = c
		}
	}
	return m, nil
}

func malformedHalf(err error, member string) error {
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalid, member, err)
	}
	return fmt.Errorf("%w: a half's %s is absent or not of its form", ErrInvalid, member)
}

// commitmentFor is the half's commitment to field, when it carries the
// contract's commit_alg and a well-formed commitment (DR §5: only then can
// an opening open it).
func commitmentFor(h mappedHalf, field string) (string, bool) {
	c, ok := h.commitments[field]
	return c, ok && h.commitAlg == CommitAlgNonce256
}

// pairState decides one deal under one requirement (DR §6, rules 1 to 6).
func pairState(req dealRequirement, a, b []mappedHalf, statusA, statusB string, openings map[string]map[string]Opening) ExchangeState {
	switch {
	case len(a) > 1 || len(b) > 1:
		return Unresolved
	case len(b) == 0:
		return unpaired(statusB, AOnly)
	case len(a) == 0:
		return unpaired(statusA, BOnly)
	}
	// Rule 3, before any opening is checked: a pairing lacking the
	// contract's commit_alg, the field's commitment or its opening is
	// INSUFFICIENT, so a missing opening outranks a failed one.
	var projected [2]Half
	for i, h := range []mappedHalf{a[0], b[0]} {
		commitment, ok := commitmentFor(h, req.field)
		opening, carried := openings[h.digest][req.field]
		if !ok || !carried {
			return Insufficient
		}
		// The half projected onto this requirement's one field: a failed
		// opening makes it untrusted for this requirement only.
		projected[i] = OpenHalf(Half{
			RecordID: h.digest, Correlation: Correlation{ExchangeID: h.dealID},
			FieldCommitments: map[string]string{req.field: commitment}, Covered: true, Trusted: true,
		}, map[string]Opening{req.field: opening})
	}
	results, _ := ReconcileHalvesWith(HalfSet{Halves: projected[:1]}, HalfSet{Halves: projected[1:]}, openedTextsAgree(req.field))
	return results[0].State
}

// unpaired is rule 2: a half with no counterpart is the one-sided state
// only over a COMPLETE set on the other side.
func unpaired(otherSide string, oneSided ExchangeState) ExchangeState {
	switch otherSide {
	case SetComplete:
		return oneSided
	case SetUndecided:
		return Unresolved
	}
	return Insufficient
}

// openedTextsAgree is rules 5 and 6 on one field: an empty opened text
// records no term (Undecided, also when both are empty); otherwise equal
// texts agree, compared code point by code point.
func openedTextsAgree(field string) ContentCheck {
	return func(a, b Half) ContentVerdict {
		ta, tb := a.Opened[field], b.Opened[field]
		switch {
		case ta == "" || tb == "":
			return Undecided
		case ta == tb:
			return Agree
		}
		return Disagree
	}
}

func (t *ResultTallies) add(state ExchangeState) {
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

// judge is DR §7.2: sufficiency from the two non-finding tallies, and the
// verdict, never a ratio.
func (t ResultTallies) judge() (sufficiency, verdict string) {
	switch {
	case t.Insufficient > 0:
		return "GAP", "not_evaluable"
	case t.Unresolved > 0:
		return "UNKNOWN", "not_evaluable"
	case t.Matched > 0 && t.AOnly == 0 && t.BOnly == 0 && t.Conflicting == 0:
		return "SATISFIED", "met"
	}
	return "SATISFIED", "not_met"
}

// coverageProperties are the properties that apply to each kind that can
// make a set complete (DR §6.1).
var coverageProperties = map[string][]string{
	"stream_interval":      {"coverage_declaration_form", "coverage_declaration_anchor", "coverage_declaration_population", "coverage_declaration_commitment"},
	"source_authoritative": {"coverage_declaration_form", "coverage_declaration_population", "coverage_declaration_source"},
}

// setCompleteness is DR §6.1. A property that applies and is not reported
// counts as NOT_PRESENT; any report other than PASS, NOT_CHECKED or
// INCONCLUSIVE leaves the set NOT_SHOWN.
func setCompleteness(given map[string]CoverageGiven, book string, period ResultPeriod) SetCompleteness {
	coverage, ok := given[book]
	if !ok {
		return SetCompleteness{Status: SetNotShown}
	}
	out := SetCompleteness{Status: SetNotShown}
	kind, _ := coverage.Declaration["kind"].(string)
	if kind != "" {
		out.Kind = &kind
	}
	if coverage.Period != period {
		return out
	}
	properties, eligible := coverageProperties[kind]
	if kind == "stream_interval" {
		rule, _ := coverage.Declaration["rule"].(string)
		eligible = rule == "all_records"
	}
	if !eligible {
		return out
	}
	undecided := false
	for _, name := range properties {
		switch coverage.Properties[name] {
		case "PASS":
		case "NOT_CHECKED", "INCONCLUSIVE":
			undecided = true
		default:
			return out
		}
	}
	out.Status = SetComplete
	if undecided {
		out.Status = SetUndecided
	}
	return out
}

// buildDealResult is the Evidence Result of DR §7: one claim per
// requirement, evidence the sorted digests of every half in the reconcile.
func buildDealResult(in DealReconcileInput, halves []mappedHalf, out DealReconcile) DealReconcileResult {
	digests := make([]string, 0, len(halves))
	for _, h := range halves {
		digests = append(digests, h.digest)
	}
	slices.Sort(digests)
	digests = slices.Compact(digests)
	evidence := make([]EvidenceRef, 0, len(digests))
	for _, d := range digests {
		evidence = append(evidence, EvidenceRef{Digest: d, DigestAlg: "SHA-256"})
	}
	result := DealReconcileResult{ResultVersion: "evidence-result-v0", GeneratedAt: in.GeneratedAt}
	result.Aggregate.Buckets.Met, result.Aggregate.Buckets.NotMet, result.Aggregate.Buckets.NotEvaluable = []string{}, []string{}, []string{}
	presentation := map[string]string{"SATISFIED": "SATISFIED", "UNKNOWN": "UNKNOWN", "GAP": "INSUFFICIENT"}
	for _, req := range dealRequirements {
		sufficiency, verdict := out.Sufficiency[req.id], out.Verdict[req.id]
		result.Claims = append(result.Claims, ReconcileClaim{
			ID: req.claimID, Type: "reconcile", ContractRef: DealReconcileContractRef, RequirementRef: req.id,
			Tier: "recomputed", Grade: "self-attested", Sufficiency: sufficiency, Verdict: verdict,
			Evidence: slices.Clone(evidence), Proofs: []EvidenceRef{},
			Presentation: ResultPresentation{Kind: "disclosure", Status: presentation[sufficiency], Evidence: slices.Clone(evidence)},
			Reconcile:    ReconcileBody{JoinKey: "deal_id", Peer: in.Peer, Period: in.Period, Tallies: out.Tallies[req.id], StateOfRecord: "none"},
		})
		result.Aggregate.Coverage.EvaluatedPopulation++
		if sufficiency == "UNKNOWN" {
			result.Aggregate.Coverage.UnknownCount++
		}
		switch verdict {
		case "met":
			result.Aggregate.Buckets.Met = append(result.Aggregate.Buckets.Met, req.claimID)
		case "not_met":
			result.Aggregate.Buckets.NotMet = append(result.Aggregate.Buckets.NotMet, req.claimID)
		default:
			result.Aggregate.Buckets.NotEvaluable = append(result.Aggregate.Buckets.NotEvaluable, req.claimID)
		}
	}
	return result
}

// resolvePointer resolves an RFC 6901 JSON Pointer in decoded JSON. found
// is false when it resolves to nothing; a pointer that is not of RFC 6901's
// form is an error.
func resolvePointer(doc any, pointer string) (value any, found bool, err error) {
	if pointer == "" {
		return doc, true, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false, fmt.Errorf("%w: JSON Pointer %q does not start with /", ErrInvalid, pointer)
	}
	value = doc
	for _, token := range strings.Split(pointer[1:], "/") {
		if strings.Contains(strings.NewReplacer("~0", "", "~1", "").Replace(token), "~") {
			return nil, false, fmt.Errorf("%w: JSON Pointer %q has a bad escape", ErrInvalid, pointer)
		}
		token = strings.NewReplacer("~1", "/", "~0", "~").Replace(token)
		switch node := value.(type) {
		case map[string]any:
			if value, found = node[token]; !found {
				return nil, false, nil
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) || (len(token) > 1 && token[0] == '0') {
				return nil, false, nil
			}
			value = node[index]
		default:
			return nil, false, nil
		}
	}
	return value, true, nil
}
