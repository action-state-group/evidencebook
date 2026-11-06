// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// Handing a standard_history card's material (agent-action-capsule
// spec/standard-history-v0.md §6 "Handing the material", SH-3): the carriage
// is one ordinary evidence-bundle/v2 per log the anchor commits, with no new
// member. The holder builds each with StandardHistoryBundle; a recipient
// recomputes the card's counts with CheckStandardHistoryMaterial, which
// reports counts_recompute PASS or FAIL only over shown coverage.

// StandardHistoryBundleRequest is what the holder hands one recipient of a
// log, for the log's checkpoint the card's anchor commits.
type StandardHistoryBundleRequest struct {
	// Disclose reports whether the recipient's share gate lets it have the
	// record's header and payloads; nil discloses every record. A record
	// that is not disclosed still travels as a digest-only capsule, which
	// is what shows coverage without showing the record. The policy behind
	// it is the host's (SH §6): the recipient learns nothing it may not see.
	// A record its caller sealed (AppendCapsule) carries its header inside
	// its capsule, and every record of the interval travels as a capsule, so
	// that header cannot be withheld: refusing such a record is ErrInvalid.
	Disclose func(Record) bool
	// Witnesses and WitnessDirectory are BundleRequest's: witness receipts
	// for the anchor, checked against the directory before the bundle is
	// built.
	Witnesses        []json.RawMessage
	WitnessDirectory []WitnessRow
	// ConductPacks is BundleRequest's: a handover goes to a recipient, so a
	// record citing one of these packs is refused if Disclose lets it
	// through, or if its header is inline. One Disclose withholds travels
	// digest-only, which shows no claim.
	ConductPacks []string
}

// StandardHistoryBundle is the evidence-bundle/v2 of this book's records
// from seq 1 to at, a checkpoint the book issued: the bundle for the log at
// the extent the card's anchor commits for it. The book appends the bundle's
// disclosure record, as Bundle does. A book with no record at or below at
// has nothing to hand (ErrNotFound), and a Disclose that lets no record
// through hands none (ErrInvalid).
func (b *Book) StandardHistoryBundle(ctx context.Context, at Checkpoint, req StandardHistoryBundleRequest) (Bundle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	anchor, include, err := b.handoverLocked(ctx, at, req)
	if err != nil {
		return Bundle{}, err
	}
	return b.bundleLocked(ctx, BundleRequest{
		Root: include[len(include)-1], Include: include, Payloads: PayloadsSelected, At: &anchor,
		Witnesses: req.Witnesses, WitnessDirectory: req.WitnessDirectory, ConductPacks: req.ConductPacks, handover: true,
	})
}

// handoverLocked is the checkpoint a handover is as of and the records the
// share gate discloses, or why the book can hand nothing. It writes nothing.
func (b *Book) handoverLocked(ctx context.Context, at Checkpoint, req StandardHistoryBundleRequest) (Checkpoint, []string, error) {
	anchor, err := b.anchorLocked(ctx, &at)
	if err != nil {
		return Checkpoint{}, nil, err
	}
	if anchor.Entries == 0 || uint64(len(b.log)) < anchor.Entries {
		return Checkpoint{}, nil, fmt.Errorf("%w: %s holds no record under checkpoint %s", ErrNotFound, b.id, anchor.ID)
	}
	if err := verifyCarriedReceipts(anchor.Statement, req.Witnesses, req.WitnessDirectory); err != nil {
		return Checkpoint{}, nil, err
	}
	var include []string
	for _, r := range b.log[:anchor.Entries] {
		if req.Disclose == nil || req.Disclose(r.detached()) {
			include = append(include, r.RecordID)
			continue
		}
		// Every record of the interval travels as a capsule, and a record
		// its caller sealed holds its header in the capsule: refusing it
		// would hide nothing, so it is refused outright.
		stored, err := b.storedLocked(ctx, r.RecordID)
		if err != nil {
			return Checkpoint{}, nil, err
		}
		if _, inline, err := inlineHeader(stored.Capsule); err != nil {
			return Checkpoint{}, nil, fmt.Errorf("%w: capsule %s: %v", ErrCorrupt, r.RecordID, err)
		} else if inline {
			return Checkpoint{}, nil, fmt.Errorf("%w: record %s carries its header in its capsule, which travels with the interval; the share gate cannot withhold it", ErrInvalid, r.RecordID)
		}
	}
	if len(include) == 0 {
		return Checkpoint{}, nil, fmt.Errorf("%w: the share gate lets no record of %s through", ErrInvalid, b.id)
	}
	// Refused here, before any book of a tick is written, as the checks above are.
	if err := checkConductPacks(req.ConductPacks); err != nil {
		return Checkpoint{}, nil, err
	}
	for _, id := range include {
		if err := b.refuseConductLocked(id, BundleRequest{ConductPacks: req.ConductPacks}); err != nil {
			return Checkpoint{}, nil, err
		}
	}
	return anchor, include, nil
}

// StandardHistoryBundles is, for every log the anchor's tick committed, the
// bundle of that log at the extent the anchor commits (anchor.Extents),
// keyed by log id. A committed log that is not a book of this shelf (a host
// log, or none) is ErrNotFound or ErrInvalid. A recipient still cannot show coverage from them for
// the tick's anchor, which commits its logs through a salted, sparse tree
// (SH-10).
func (c *Cadence) StandardHistoryBundles(ctx context.Context, anchor StandardHistoryAnchor, req StandardHistoryBundleRequest) (map[string]Bundle, error) {
	books, err := c.shelf.Books(ctx)
	if err != nil {
		return nil, err
	}
	// Every log is resolved, and its share gate applied, before any bundle is built: a bundle appends a
	// disclosure record to its book, which must not happen for a set that
	// then fails.
	type target struct {
		book *Book
		at   Checkpoint
	}
	targets := make(map[string]target, len(anchor.Extents))
	for id, extent := range anchor.Extents {
		if !slices.Contains(books, id) {
			return nil, fmt.Errorf("%w: the shelf holds no book %s", ErrNotFound, id)
		}
		book, err := c.shelf.Open(ctx, id)
		if err != nil {
			return nil, err
		}
		checkpoints, err := book.Checkpoints(ctx)
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(checkpoints, func(cp Checkpoint) bool { return cp.Entries == extent })
		if i < 0 {
			return nil, fmt.Errorf("%w: %s has no checkpoint at %d entries", ErrNotFound, id, extent)
		}
		book.mu.Lock()
		_, _, err = book.handoverLocked(ctx, checkpoints[i], req)
		book.mu.Unlock()
		if err != nil {
			return nil, err
		}
		targets[id] = target{book, checkpoints[i]}
	}
	out := make(map[string]Bundle, len(targets))
	for id, t := range targets {
		bundle, err := t.book.StandardHistoryBundle(ctx, t.at, req)
		if err != nil {
			return nil, err
		}
		out[id] = bundle
	}
	return out, nil
}

// ErrMaterialWithheld is what a DealMapping returns for a member it reads
// that a handed bundle withholds: the check is then NOT_CHECKED, never a FAIL
// that would blame the holder for what it was not shown.
var ErrMaterialWithheld = errors.New("evidencebook: a record member the deal mapping reads is withheld")

// MaterialRecord is one record of a handed bundle whose log's coverage was
// shown.
type MaterialRecord struct {
	PeerRecord
	LogID string
	// payloads are the bundle's carried payloads, by digest.
	payloads map[string][]byte
}

// Payload is the carried payload with this digest. false when the bundle
// withholds it.
func (r MaterialRecord) Payload(digest string) ([]byte, bool) {
	data, ok := r.payloads[digest]
	return data, ok
}

// DealMapping is the host profile's SH-2 mapping from the records of the
// covered logs, in log order within a log, to deal entries. It returns
// ErrMaterialWithheld for a record or payload member it reads that is
// withheld, and an error of its own for a record that is no deal's.
type DealMapping func(records []MaterialRecord) ([]standardhistory.Deal, error)

// CommittedLog is one log an anchor commits: the SHA-256 of the checkpoint
// statement it commits for the log, and the log's record count under it.
type CommittedLog struct {
	Checkpoint string
	Leaves     uint64
}

// StandardHistoryMaterial is what a recipient holds of a standard_history
// card.
type StandardHistoryMaterial struct {
	Card   []byte
	AsOf   standardhistory.AsOf
	Policy *standardhistory.Policy
	// Keys pins the holder's checkpoint key (and the recipient's witness
	// directory) the bundles are verified under.
	Keys ResponderKeys
	// Committed is every log the anchor commits, in a form the recipient
	// checked against the anchor: for a log's own checkpoint, that
	// checkpoint alone. It is empty for an anchor that commits its logs
	// through a salted or sparse tree, such as a cadence tick's (SH-10);
	// coverage is then not shown.
	Committed map[string]CommittedLog
	Bundles   [][]byte
	Deals     DealMapping
}

// StandardHistoryCheck is the card's six properties and, when
// counts_recompute is NOT_CHECKED for want of material, why.
type StandardHistoryCheck struct {
	Properties map[string]string
	// Why is empty unless counts_recompute is NOT_CHECKED because of the
	// handed material or its mapping.
	Why string
}

// CheckStandardHistoryMaterial reports the card's properties (SH §6),
// recomputing the counts from the handed bundles when, and only when,
// coverage is shown for the anchor's own log (a) and every other log the
// anchor commits (b), the mapping reads every member it needs, and the card
// is well formed. Otherwise counts_recompute is NOT_CHECKED: a partial
// handover never yields PASS, and never FAIL. The error is for a malformed
// input or a mapping failure, never for missing material.
func CheckStandardHistoryMaterial(m StandardHistoryMaterial) (StandardHistoryCheck, error) {
	properties, err := standardhistory.Check(m.Card, m.Policy, &m.AsOf, nil)
	if err != nil {
		return StandardHistoryCheck{}, err
	}
	notChecked := func(why string) (StandardHistoryCheck, error) {
		return StandardHistoryCheck{Properties: properties, Why: why}, nil
	}
	for _, p := range []string{"card_form", "counts_integral", "denominator"} {
		if properties[p] != standardhistory.Pass {
			return notChecked("the card's " + p + " is not PASS")
		}
	}
	if m.Deals == nil {
		return notChecked("no deal-entry mapping for the card's host")
	}
	records, why := coveredRecords(m)
	if why != "" {
		return notChecked(why)
	}
	deals, err := m.Deals(records)
	switch {
	case errors.Is(err, ErrMaterialWithheld):
		return notChecked(err.Error())
	case err != nil:
		return StandardHistoryCheck{}, err
	}
	if deals == nil {
		deals = []standardhistory.Deal{}
	}
	// The counts are recomputed over the card's own window (SH §6), so the
	// check runs under a policy made of the card's two members: it must not
	// stand in for the real policy, whose properties are already in
	// properties.
	var stated struct {
		Window standardhistory.Window `json:"window"`
		Policy string                 `json:"policy"`
	}
	if err := json.Unmarshal(m.Card, &stated); err != nil {
		return notChecked("the card's window is unreadable")
	}
	recomputed, err := standardhistory.Check(m.Card, &standardhistory.Policy{Digest: stated.Policy, Window: stated.Window}, &m.AsOf, deals)
	if err != nil {
		return StandardHistoryCheck{}, err
	}
	properties["counts_recompute"] = recomputed["counts_recompute"]
	return StandardHistoryCheck{Properties: properties}, nil
}

// coveredRecords verifies the handed bundles and returns the records of the
// committed logs when coverage is shown for all of them, else why it is not.
func coveredRecords(m StandardHistoryMaterial) ([]MaterialRecord, string) {
	if len(m.Committed) == 0 {
		return nil, "the anchor's committed logs are not in a checkable form (SH-10)"
	}
	anchorLogs := 0
	for _, log := range m.Committed {
		if log.Checkpoint == m.AsOf.Anchor {
			if log.Leaves == 0 {
				return nil, "the anchor's own log is empty, so there is nothing to show coverage of"
			}
			anchorLogs++
		}
	}
	if anchorLogs != 1 {
		return nil, "the anchor is not the checkpoint of exactly one committed log"
	}
	bundles := make(map[string]VerifiedBundle, len(m.Committed))
	for i, data := range m.Bundles {
		verified, err := verifyHandedBundle(data, m.Keys)
		if err != nil {
			return nil, fmt.Sprintf("bundle %d does not verify: %v", i, err)
		}
		var body struct {
			Completeness struct {
				Missing []string `json:"missing"`
			} `json:"completeness"`
			Certificate struct {
				BodyDigests []string `json:"body_digests"`
			} `json:"completeness_certificate"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, fmt.Sprintf("bundle %d: %v", i, err)
		}
		if _, committed := m.Committed[verified.LogID]; !committed {
			continue // not a log the anchor commits: it shows nothing about the card
		}
		if _, dup := bundles[verified.LogID]; dup {
			return nil, fmt.Sprintf("two bundles for %s", verified.LogID)
		}
		// A leaf of the interval, a record or a padding leaf, listed as
		// missing is no coverage (SH-3 (a)).
		for _, leaf := range body.Certificate.BodyDigests {
			if slices.Contains(body.Completeness.Missing, leaf) {
				return nil, fmt.Sprintf("%s lists leaf %s as missing", verified.LogID, leaf)
			}
		}
		for _, r := range verified.Records {
			if slices.Contains(body.Completeness.Missing, r.RecordID) {
				return nil, fmt.Sprintf("%s lists record %s as missing", verified.LogID, r.RecordID)
			}
		}
		bundles[verified.LogID] = verified
	}
	var records []MaterialRecord
	for _, id := range sortedKeys(m.Committed) {
		committed, v := m.Committed[id], bundles[id]
		if committed.Leaves == 0 {
			continue // (b) over no leaf is vacuous, and no bundle can exist for it
		}
		switch {
		case v.LogID == "":
			return nil, fmt.Sprintf("no bundle for %s", id)
		case !v.AnchorAuthenticated || v.Claims.IntervalCoverage != "pass" || v.Claims.PerRecordMembership != "pass":
			return nil, fmt.Sprintf("%s: interval coverage or per-record membership is not established", id)
		case v.IntervalFirst != 1 || v.IntervalLast != committed.Leaves:
			return nil, fmt.Sprintf("%s: the bundle covers seq %d to %d, not 1 to %d", id, v.IntervalFirst, v.IntervalLast, committed.Leaves)
		case digestBytes(v.AnchorStatement) != committed.Checkpoint:
			return nil, fmt.Sprintf("%s: the bundle is not anchored to the checkpoint the anchor commits", id)
		}
		if committed.Checkpoint == m.AsOf.Anchor {
			// The window is read against the anchor's time, which the card's
			// as_of does not bind: it must be the checkpoint's own.
			at, err := checkpointFromStatement(v.AnchorStatement)
			if err != nil || evidencerequest.ProfileTime(at.IssuedAt) != m.AsOf.AnchorTime {
				return nil, fmt.Sprintf("%s: the anchor's time is not the checkpoint's", id)
			}
		}
		for _, r := range slices.SortedFunc(slices.Values(v.Records), func(a, b PeerRecord) int { return cmp.Compare(a.Seq, b.Seq) }) {
			if !r.CapsuleOK {
				return nil, fmt.Sprintf("%s: record %s does not verify as a capsule", id, r.RecordID)
			}
			if r.Seq < 1 || r.Seq > committed.Leaves {
				// A carried capsule with no membership in the interval is
				// not a leaf of the log.
				return nil, fmt.Sprintf("%s: record %s is not a leaf of seq 1 to %d", id, r.RecordID, committed.Leaves)
			}
			if r.Header == nil || !r.HeaderVerified {
				// Without its header no mapping can tell this record from a
				// deal's, and a count that differs would blame the holder
				// for what it was not shown.
				return nil, fmt.Sprintf("%s: the header of record %s is withheld", id, r.RecordID)
			}
			records = append(records, MaterialRecord{PeerRecord: r, LogID: id, payloads: v.Payloads})
		}
	}
	return records, ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// verifyHandedBundle is VerifyBundleAnswer without its graph-closure
// requirement: SH-3 shows coverage, and a Bundle's root and records_mode
// mean nothing there, so a bundle another producer declares incomplete for
// citations outside the log is still a handover. Everything else is kept:
// the bundle verifies, its anchor is signed by the pinned key, and its
// witness receipts verify under the recipient's directory. Interval
// coverage, per-record membership and every capsule are checked by the
// caller.
func verifyHandedBundle(data []byte, keys ResponderKeys) (VerifiedBundle, error) {
	if keys.Checkpoint == "" {
		return VerifiedBundle{}, fmt.Errorf("%w: a pinned checkpoint key is required", ErrInvalid)
	}
	verified, err := VerifyBundle(data)
	if err != nil {
		return verified, err
	}
	if !verified.AnchorAuthenticated || verified.AnchorKeyID != keys.Checkpoint {
		return verified, fmt.Errorf("%w: bundle is not anchored to a checkpoint signed by the pinned key", ErrInvalid)
	}
	return verified, verifyCarriedReceipts(verified.AnchorStatement, verified.Witnesses, keys.Witnesses)
}
