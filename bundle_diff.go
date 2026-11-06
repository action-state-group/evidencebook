// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"fmt"
	"slices"
)

// BundleDiff is what a lower disclosure level drops from a higher one, read
// from two verified bundles of the same history. A level is a scoped bundle,
// not a hidden row, so the difference is exactly what the two bundles
// disclose: nothing here is inferred from a policy.
//
// A bundle also carries every record its interval covers, undisclosed, so
// the checkpoint proves completeness. Those leaves reveal nothing and are
// not compared: a record counts as disclosed in a bundle only when the
// bundle verifies its header. Only records and payloads are compared: a
// bundle's other extensions are the producer's own statements (VerifiedBundle
// .Extensions) and disclosed members other than the header are not read, so a
// caller that gates on Subset gates on records and payloads only.
type BundleDiff struct {
	// DroppedRecords are records whose header the higher bundle discloses
	// and the lower does not.
	DroppedRecords []string
	// DroppedPayloads are payload digests the higher bundle carries and the
	// lower does not.
	DroppedPayloads []string
	// Extra lists what the lower bundle discloses that the higher does not,
	// as "record:ID" or "payload:DIGEST". Empty when the lower bundle is a
	// subset of the higher; otherwise it is not a lower level of that bundle.
	Extra []string
}

// Subset reports whether the lower bundle discloses nothing the higher does not.
func (d BundleDiff) Subset() bool { return len(d.Extra) == 0 }

// Empty reports whether the two bundles disclose the same records and payloads.
func (d BundleDiff) Empty() bool {
	return len(d.DroppedRecords)+len(d.DroppedPayloads)+len(d.Extra) == 0
}

// DiffBundles verifies both bundles (VerifyBundle) and lists what lower drops
// from higher. Each list is sorted. A bundle that does not verify is an
// error, and so are bundles of two different logs, so a difference is never
// drawn from bytes nobody checked or from two histories.
func DiffBundles(higher, lower []byte) (BundleDiff, error) {
	hi, err := VerifyBundle(higher)
	if err != nil {
		return BundleDiff{}, fmt.Errorf("higher bundle: %w", err)
	}
	lo, err := VerifyBundle(lower)
	if err != nil {
		return BundleDiff{}, fmt.Errorf("lower bundle: %w", err)
	}
	// Two logs are two histories: a bundle from another log that discloses
	// less would otherwise pass as a lower level of this one. The anchors may
	// differ (a level can be cut at a later checkpoint), the log may not.
	// Both anchors must verify under the key they name, so the log id is the
	// signed one; that the key belongs to the expected party is the caller's
	// check, as VerifiedBundle.AnchorAuthenticated says, and a fork of one
	// log is not detected here.
	if !hi.AnchorAuthenticated || !lo.AnchorAuthenticated {
		return BundleDiff{}, fmt.Errorf("%w: a bundle's anchor is not authenticated", ErrInvalid)
	}
	if hi.LogID == "" || hi.LogID != lo.LogID {
		return BundleDiff{}, fmt.Errorf("%w: the bundles are not of one log (%q, %q)", ErrInvalid, hi.LogID, lo.LogID)
	}
	hiRecords, loRecords := disclosedRecords(hi), disclosedRecords(lo)
	var d BundleDiff
	for id := range hiRecords {
		if !loRecords[id] {
			d.DroppedRecords = append(d.DroppedRecords, id)
		}
	}
	for id := range loRecords {
		if !hiRecords[id] {
			d.Extra = append(d.Extra, "record:"+id)
		}
	}
	for digest := range hi.Payloads {
		if _, carried := lo.Payloads[digest]; !carried {
			d.DroppedPayloads = append(d.DroppedPayloads, digest)
		}
	}
	for digest := range lo.Payloads {
		if _, carried := hi.Payloads[digest]; !carried {
			d.Extra = append(d.Extra, "payload:"+digest)
		}
	}
	slices.Sort(d.DroppedRecords)
	slices.Sort(d.DroppedPayloads)
	slices.Sort(d.Extra)
	return d, nil
}

// disclosedRecords is the set of records whose header the bundle verifies.
func disclosedRecords(v VerifiedBundle) map[string]bool {
	out := make(map[string]bool, len(v.Records))
	for _, r := range v.Records {
		if r.HeaderVerified {
			out[r.RecordID] = true
		}
	}
	return out
}
