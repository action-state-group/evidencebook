// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	evidencebook "github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// Countersigning an x-deal-v1 deal (evidencebook#865). A countersigner is
// shown the withheld form of the deal book's bundle (`deal report --bundle
// FILE --withheld`): the book's Capsules up to the report's checkpoint, with
// the checkpoint, its witness receipts and the proofs that tie them, and no
// payload, disclosure or extension. capsule-cli's `countersign request`
// submits only such a bundle. `deal countersign` then checks the bundle is
// this deal's (its log is the deal's own book, its checkpoint one that book
// holds, its root a record of it) and keeps every countersignature that
// verifies with it, as for an x-deal-v0 deal (storeCountersigned); `deal
// report` states the stored ones (v1StoredCountersign).

// withheldBundleMembers are the members the withheld form keeps: the
// Evidence Bundle v2 core a reader verifies.
var withheldBundleMembers = []string{"bundle_version", "bundle_kind", "root", "records", "checkpoint", "completeness", "completeness_certificate", "verification"}

// producerKeyExtension is agent-action-capsule's extension that declares a
// bundle's producer key. The withheld form keeps it: capsule-cli counts a
// countersignature by that key as the producer's own (not independent).
const producerKeyExtension = "producer-key/v1"

// v1WithheldBundle is the deal book's bundle at checkpoint at in its
// withheld form: built as the report bundle's book is, declaring the
// profile's signing key as its producer key, then reduced to
// withheldBundleMembers and the producer-key/v1 extension. No payload,
// disclosure, report or other side's record is in it. Its completeness
// states payloads_mode selected, as capsule-cli's digests-only bundle does
// (the draft has no "none" mode; cli/bundle.go), and it is checked as a
// reader checks it before it is returned.
func (s *dealSession) v1WithheldBundle(ctx context.Context, p cli.Profile, logID string, at dealprofile.Checkpoint) ([]byte, error) {
	key, err := cli.PrivateKey(p.Signing)
	if err != nil || key == nil {
		return nil, cli.InputError("--withheld declares your profile's signing key as the bundle's producer key, so a countersignature by it is shown as your own, and this profile has none")
	}
	raw, err := s.v1BookBundle(ctx, logID, at, nil, key.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, err
	}
	full, err := cli.DecodeBundleJSON(raw)
	if err != nil {
		return nil, err
	}
	withheld := make(map[string]interface{}, len(withheldBundleMembers)+1)
	for _, m := range withheldBundleMembers {
		if v, ok := full[m]; ok {
			withheld[m] = v
		}
	}
	exts, _ := full["extensions"].(map[string]interface{})
	if pk, ok := exts[producerKeyExtension]; ok {
		withheld["extensions"] = map[string]interface{}{producerKeyExtension: pk}
	} else {
		return nil, errors.New("the deal book's bundle does not declare its producer key")
	}
	completeness, _ := withheld["completeness"].(map[string]interface{})
	if completeness == nil {
		return nil, errors.New("the deal book's bundle carries no completeness member")
	}
	completeness["payloads_mode"] = string(evidencebook.PayloadsSelected)
	out, err := json.Marshal(withheld)
	if err != nil {
		return nil, err
	}
	decoded, err := cli.DecodeBundleJSON(out)
	if err != nil {
		return nil, err
	}
	if err := cli.VerifyProducedBundle(decoded, true); err != nil {
		return nil, err
	}
	return out, nil
}

// writeV1Withheld is `deal report --bundle FILE --withheld`: the withheld
// bundle at the report's checkpoint, which leaves this device for a
// countersigner, so it passes the share gate as a copy for another party
// does before it is written. It seals no report: the file carries none.
func (s *dealSession) writeV1Withheld(c *cobra.Command, dealID string, d *v1Deal, snap xdealv1.ReportSnapshot, out string, result map[string]any) error {
	ctx := c.Context()
	p, err := cli.Selected(c)
	if err != nil {
		return err
	}
	file, err := s.v1WithheldBundle(ctx, p, d.OwnLogID, snap.Report.DealCheckpoint)
	if err != nil {
		return err
	}
	file = append(file, '\n')
	events, err := s.v1Events(ctx, dealID)
	if err != nil {
		return err
	}
	if err := v1ShareGate(scrubPaymentRefs(file), events, dealprofile.AudienceCounterparty, v1OwnIDs(dealID, snap.Book)...); err != nil {
		return err
	}
	if err := writeNewFile(out, file); err != nil {
		return err
	}
	result["bundle"], result["file"], result["withheld"] = out, out, true
	return cli.Output(c, result)
}

// v1BundleCheck reads a bundle's bytes as this deal's: it verifies, its log
// is the deal's own book, its checkpoint is one that book holds (the same
// root, signed by the same key), and its root is a record of the book under
// that checkpoint. It returns the bundle and the root's place in the book
// (step 1 to the book's size). The bundle may be the withheld form or the
// full report bundle: a countersignature covers whichever was signed.
func (s *dealSession) v1BundleCheck(ctx context.Context, raw []byte, d *v1Deal, notThis func(why string) error) (map[string]interface{}, int, error) {
	b, err := cli.DecodeBundleJSON(raw)
	if err != nil {
		return nil, 0, notThis("it is not an Evidence Bundle")
	}
	if err = cli.VerifyProducedBundle(b, true); err != nil {
		return nil, 0, notThis("it does not verify")
	}
	cert, _ := b["completeness_certificate"].(map[string]interface{})
	if fmt.Sprint(cert["log_id"]) != d.OwnLogID {
		return nil, 0, notThis("its log is not this deal's book")
	}
	book, err := s.shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return nil, 0, err
	}
	cp, _ := b["checkpoint"].(map[string]interface{})
	statement, err := base64.RawURLEncoding.DecodeString(fmt.Sprint(cp["cose"]))
	if err != nil || len(statement) == 0 {
		return nil, 0, notThis("its checkpoint is unreadable")
	}
	cps, err := book.Checkpoints(ctx)
	if err != nil {
		return nil, 0, err
	}
	// The bundle's checkpoint is one the book holds when it is the same
	// signed statement; VerifyProducedBundle tied the bundle's records to it.
	var anchor *evidencebook.Checkpoint
	for i := range cps {
		if cps[i].LogID == d.OwnLogID && bytes.Equal(cps[i].Statement, statement) {
			anchor = &cps[i]
		}
	}
	if anchor == nil {
		return nil, 0, notThis("its checkpoint is not one this deal's book holds")
	}
	records, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: anchor.Entries})
	if err != nil {
		return nil, 0, err
	}
	root := fmt.Sprint(b["root"])
	for _, r := range records {
		if r.RecordID == root {
			return b, int(r.Seq), nil
		}
	}
	return nil, 0, notThis("its root is not a record of this deal's book")
}

// v1BookSize is how many records the deal's own book holds now.
func (s *dealSession) v1BookSize(ctx context.Context, d *v1Deal) (int, error) {
	book, err := s.shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return 0, err
	}
	records, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1})
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// v1StoredCountersign is the countersign stamp of an x-deal-v1 deal from its
// stored countersignatures (storedCountersignOf): countersigned only when one
// resolves against the profile's countersigner directory, in part when the
// book has records after that bundle's root.
func (s *dealSession) v1StoredCountersign(ctx context.Context, d *v1Deal) (dealGradeCountersign, error) {
	total, err := s.v1BookSize(ctx, d)
	if err != nil {
		return dealGradeCountersign{}, err
	}
	out, err := s.storedCountersignOf(ctx, d.DealID, total, func(raw []byte) (map[string]interface{}, int, error) {
		return s.v1BundleCheck(ctx, raw, d, func(why string) error {
			return fmt.Errorf("%w: a stored countersigned bundle is not this deal's: %s", cli.ErrConflict, why)
		})
	})
	if err == nil && out.Rung != "" {
		out.Covers = fmt.Sprintf("The countersignature covers your deal book through record %d of %d, as the records' digests: not this report, and not the other side's records.", out.ThroughStep, out.Steps)
	}
	return out, err
}

// runV1Countersign is `deal countersign` on an x-deal-v1 deal.
func runV1Countersign(c *cobra.Command, bundlePath, entryPath, directory string) (err error) {
	dealID, _ := c.Flags().GetString("deal")
	p, err := cli.Selected(c)
	if err != nil {
		return err
	}
	ctx := c.Context()
	s, err := openDealSession(ctx, p)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return err
	}
	if d == nil {
		return cli.InputError("unknown deal: " + dealID)
	}
	if directory == "" {
		directory = profileCountersignDirectory(s.p)
	}
	if directory == "" {
		return errNoCountersignDirectory
	}
	raw, err := cli.ReadInput(bundlePath)
	if err != nil {
		return err
	}
	b, _, err := s.v1BundleCheck(ctx, raw, d, func(why string) error {
		return cli.InputError("--bundle " + bundlePath + " is not this deal's bundle: " + why + " (write one with `deal report --deal " + dealID + " --bundle FILE --withheld`)")
	})
	if err != nil {
		return err
	}
	return s.countersignBundle(c, dealID, b, bundlePath, entryPath, directory)
}
