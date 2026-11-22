// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal report` on an x-deal-v1 deal (evidencebook#626 B4b; PROFILE §10). A v1
// deal seals no Evidence Result: its report is a snapshot of each row's
// status the holder computes now, from what it holds, through
// xdealv1.StatusProvider.ReportSnapshot. The command runs on a read session:
// computing a report writes nothing, the store and the journal included. By
// default it prints the holder's own copy; --out also seals the report
// Capsule (standalone, appended to no book) and writes its pair. A shared
// copy (--share) and the bundle (--bundle) are later parts of B4.

// v1ReportFlagRefusal refuses the v0 flags `deal report` takes that a v1 deal
// has no form for, each with why.
func v1ReportFlagRefusal(c *cobra.Command) error {
	f := c.Flags()
	readOnly, _ := f.GetBool("read-only")
	switch {
	case f.Changed("html"):
		return cli.InputError("--html renders an x-deal-v0 deal's page; an x-deal-v1 report has no page yet: use --out for the sealed report")
	case f.Changed("email"):
		return cli.InputError("--email writes an x-deal-v0 deal's receipt email; it does not apply to an x-deal-v1 report")
	case f.Changed("from-bundle") || f.Changed("directory"):
		return cli.InputError("--from-bundle and --directory render an x-deal-v0 bundle with its countersignatures; they do not apply to an x-deal-v1 report")
	case f.Changed(includeAgentClaimsFlag) || f.Changed(includeUnauthorizedFlag):
		return cli.InputError("--" + includeAgentClaimsFlag + " chooses what an x-deal-v0 adjudicator's copy carries; it does not apply to an x-deal-v1 report")
	case f.Changed("share") || f.Changed("to"):
		return cli.InputError("a shared copy of an x-deal-v1 report is not served yet: deal report --out writes your own copy")
	case f.Changed("bundle"):
		return cli.InputError("the Evidence Bundle of an x-deal-v1 report is not served yet: deal report --out writes the sealed report")
	case readOnly && f.Changed("out"):
		return cli.InputError("--read-only writes nothing: it takes no --out")
	}
	return nil
}

// v1DealOfFlag says whether the --deal of c is an x-deal-v1 deal this profile
// holds, read without writing anything.
func v1DealOfFlag(c *cobra.Command) (bool, error) {
	dealID, _ := c.Flags().GetString("deal")
	if !cli.DealIDPattern.MatchString(dealID) {
		return false, nil
	}
	p, err := cli.Selected(c)
	if err != nil {
		return false, err
	}
	s, err := openDealStoreRead(c.Context(), p)
	if errors.Is(err, cli.ErrInput) {
		// No store, or one that is not on its shelf yet: the x-deal-v0
		// path says so the same way.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer s.close()
	return s.isV1Deal(c.Context(), dealID)
}

// runV1Report is `deal report` on an x-deal-v1 deal.
func runV1Report(c *cobra.Command) (err error) {
	if err := v1ReportFlagRefusal(c); err != nil {
		return err
	}
	dealID, _ := c.Flags().GetString("deal")
	out, _ := c.Flags().GetString("out")
	p, err := cli.Selected(c)
	if err != nil {
		return err
	}
	ctx := c.Context()
	s, err := openDealStoreRead(ctx, p)
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
	if d.OwnAgreementID == "" {
		return cli.InputError("deal " + dealID + " has no agreement of yours recorded yet, so no report: your deal book opens at your agreement (`deal agree`)")
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	at := cli.DealClock().UTC().Truncate(time.Second)
	prov := &xdealv1.StatusProvider{Shelf: s.shelf, OfferPair: s.sentOfferPair}
	snap, err := prov.ReportSnapshot(ctx, d.OwnLogID, xdealv1.ReportCopy{Audience: dealprofile.AudienceKeep, Nonce: hex.EncodeToString(nonce), AsOf: at.Format("2006-01-02T15:04:05Z")})
	if err != nil {
		return v1ReportError(dealID, err)
	}
	result := map[string]any{"deal_id": dealID, "report": snap.Report}
	if out != "" {
		pair, digest, err := s.sealV1Report(p, snap.Report, at)
		if err != nil {
			return err
		}
		// A report a reader cannot verify is never written: it is signed by
		// the key that signed the deal's agreement, or refused.
		if _, err := xdealv1.VerifyReport(pair, snap.Book.Agreement); err != nil {
			return cli.InputError("the report sealed with this profile's signing key does not verify against your agreement (" + err.Error() + "): the key that signed the agreement must sign the report")
		}
		if err := writeNewFile(out, pair); err != nil {
			return err
		}
		result["sealed_report"], result["file"] = digest, out
	}
	return cli.Output(c, result)
}

// v1ReportError says in plain words why a report could not be computed.
func v1ReportError(dealID string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "has no checkpoint at its size"):
		return cli.InputError("your deal book of " + dealID + " has records after its last checkpoint, and a report rests on a checkpoint: the next `deal tick` covers it")
	case strings.Contains(msg, "after as_of"):
		return cli.InputError("a record of deal " + dealID + " is timed after now, so no report can be computed: this computer's clock or the time the other side's witness gave is off")
	}
	return err
}

// sealV1Report seals the report as PROFILE §10 gives it: v0's report Capsule
// (capsulectl-deal-report, payload deal_report) with profile x-deal-v1,
// standalone, signed by the profile's key, the key that signed the deal's
// first record (VerifyReport). It stores nothing and returns the pair and
// its Capsule id.
func (s *dealSession) sealV1Report(p cli.Profile, r xdealv1.Report, at time.Time) ([]byte, string, error) {
	key, err := cli.PrivateKey(p.Signing)
	if err != nil || key == nil {
		return nil, "", cli.InputError("--out seals the report with your profile's signing key, and this profile has none")
	}
	payload, err := xdealv1.ReportPayload(r)
	if err != nil {
		return nil, "", err
	}
	capsule, err := dealprofile.ReportCapsule(p.Name, "capsulectl-deal", at)
	if err != nil {
		return nil, "", err
	}
	record, err := cli.Seal(cli.Request{Version: "capsule-seal-request/v1", Capsule: capsule, Payload: payload}, key)
	if err != nil {
		return nil, "", err
	}
	pair, err := xdealv1.ReportPair(record.Capsule, record.ProducerEnvelope, payload)
	if err != nil {
		return nil, "", err
	}
	return pair, record.CapsuleID, nil
}

// writeNewFile writes data to a file that must not exist yet.
func writeNewFile(path string, data []byte) error {
	if _, err := os.Lstat(path); err == nil {
		return cli.InputError(path + " exists: --out names a new file")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return cli.AtomicFile(path, append(append([]byte{}, data...), '\n'), false)
}
