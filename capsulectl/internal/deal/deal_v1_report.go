// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// xdealv1.StatusProvider.ReportSnapshot. Computing a report writes nothing,
// the store and the journal included. By default it prints the holder's own
// copy on a read session; --out also seals the report Capsule (standalone,
// appended to no book) and writes it. A shared copy (--share) is a disclose
// act: it opens the writable store, passes the share gate, is put on record
// with the digest of the exact file, and only then is written. The bundle
// (--bundle) is a later part of B4.

// v1ReportFlagRefusal refuses the v0 flags `deal report` takes that a v1 deal
// has no form for, each with why.
func v1ReportFlagRefusal(c *cobra.Command) error {
	f := c.Flags()
	readOnly, _ := f.GetBool("read-only")
	switch {
	case f.Changed("html"):
		return cli.InputError("--html renders an x-deal-v0 deal's page; an x-deal-v1 report has no page yet: deal report prints it, and --out seals it to a file")
	case f.Changed("email"):
		return cli.InputError("--email writes an x-deal-v0 deal's receipt email; it does not apply to an x-deal-v1 report")
	case f.Changed("from-bundle") || f.Changed("directory"):
		return cli.InputError("--from-bundle and --directory render an x-deal-v0 bundle with its countersignatures; they do not apply to an x-deal-v1 report")
	case f.Changed(includeAgentClaimsFlag) || f.Changed(includeUnauthorizedFlag):
		given := includeAgentClaimsFlag
		if f.Changed(includeUnauthorizedFlag) {
			given = includeUnauthorizedFlag
		}
		return cli.InputError("--" + given + " chooses what an x-deal-v0 adjudicator's copy carries; it does not apply to an x-deal-v1 report")
	case f.Changed("bundle"):
		return cli.InputError("the Evidence Bundle of an x-deal-v1 report is not served yet: deal report --out writes the sealed report")
	case readOnly && (f.Changed("out") || f.Changed("share") || f.Changed("to")):
		return cli.InputError("--read-only writes nothing: it takes no --out, --share or --to")
	}
	if out, _ := f.GetString("out"); f.Changed("out") && strings.TrimSpace(out) == "" {
		return cli.InputError("--out names the file to write; it cannot be empty")
	}
	share, _ := f.GetString("share")
	to, _ := f.GetString("to")
	switch share {
	case dealprofile.AudienceKeep:
		if strings.TrimSpace(to) != "" {
			return cli.InputError("--to names who a shared copy is for; use it with --share counterparty or adjudicator")
		}
	case dealprofile.AudienceCounterparty, dealprofile.AudienceAdjudicator:
		out, _ := f.GetString("out")
		if strings.TrimSpace(out) == "" || strings.TrimSpace(to) == "" {
			return cli.InputError("--share writes a copy for someone else: it needs --out FILE and --to (who it is for; for an adjudicator, the referee's name)")
		}
	default:
		return cli.InputError("--share must be keep, counterparty or adjudicator")
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
	share, _ := c.Flags().GetString("share")
	to, _ := c.Flags().GetString("to")
	to = strings.TrimSpace(to)
	if share == dealprofile.AudienceAdjudicator {
		// The referee is sealed and recorded spelled one way.
		to = dealprofile.NormalizeReferee(to)
	}
	// A file already at --out is refused before anything is sealed or
	// recorded: a shared copy put on record is never left without its file.
	if out != "" {
		if _, err := os.Lstat(out); err == nil {
			return cli.InputError(out + " exists: --out names a new file")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	shared := share != dealprofile.AudienceKeep
	ctx := c.Context()
	// A shared copy is a disclose act, put on record before the file is
	// written, so it needs the writable store; your own copy reads only.
	open := openDealStoreRead
	if shared {
		open = openDealSession
	}
	s, err := open(ctx, p)
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
	// An adjudicator's copy goes only to a deal that chose one: the
	// agreement names its adjudicator by key, and the record carries that key
	// beside the referee's name.
	adjKey := ""
	if share == dealprofile.AudienceAdjudicator {
		book, err := s.ownBookV1(ctx, d)
		if err != nil {
			return err
		}
		var ag *xdealv1.AgreeBody
		ok := len(book) > 0
		if ok {
			ag, ok = agreeOf(book[0])
		}
		if !ok || ag.Kind != xdealv1.KindAgreement {
			return cli.InputError("the first record of your deal book is not your agreement")
		}
		if ag.Adjudicator.IsNull() || !ag.Adjudicator.Present {
			return cli.InputError("the agreement of deal " + dealID + " names no adjudicator, so no adjudicator's copy: use --share counterparty or your own copy")
		}
		adjKey = ag.Adjudicator.V.KeyID
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	at := cli.DealClock().UTC().Truncate(time.Second)
	prov := &xdealv1.StatusProvider{Shelf: s.shelf, OfferPair: s.sentOfferPair}
	rc := xdealv1.ReportCopy{Audience: share, Nonce: hex.EncodeToString(nonce), AsOf: at.Format("2006-01-02T15:04:05Z")}
	if share == dealprofile.AudienceAdjudicator {
		rc.Referee = to
	}
	snap, err := prov.ReportSnapshot(ctx, d.OwnLogID, rc)
	if err != nil {
		return v1ReportError(dealID, p, err)
	}
	// Until the person's signature has a carrier (agent-action-capsule#627,
	// evidencebook#729), no v1 act carries one, so every act is one PROFILE
	// §8 says must not be relied on: the report says so beside settled.
	result := map[string]any{"deal_id": dealID, "report": snap.Report, "settled_note": personSignatureNote}
	if out != "" {
		pair, digest, err := s.sealV1Report(p, snap.Report, at)
		if err != nil {
			return err
		}
		// A report a reader cannot verify is never written: it is signed by
		// the key that signed the deal's agreement, or refused.
		// The file's exact bytes: what the gate reads, the record digests and
		// the file holds.
		file := append(append([]byte{}, pair...), '\n')
		if _, err := xdealv1.VerifyReport(pair, snap.Book.Agreement); err != nil {
			return cli.InputError("the report sealed with this profile's signing key does not verify against your agreement (" + err.Error() + "): the key that signed the agreement must sign the report; run it with the profile you agreed with (--profile)")
		}
		var record func() error
		if shared {
			// The exact bytes are checked to contain none of the deal's own
			// plain values (the person's words, the bounds, the
			// counterparty's details, kept only on this device), then put on
			// record, and only then written.
			events, err := s.v1Events(ctx, dealID)
			if err != nil {
				return err
			}
			if err := v1ShareGate(file, events, share); err != nil {
				return err
			}
			record = func() error {
				rec, err := s.recordV1Share(ctx, p, dealID, share, to, adjKey, file)
				if err != nil {
					return err
				}
				result["share"] = rec
				return nil
			}
		}
		write := func() error { return writeNewFile(out, file) }
		if record != nil {
			write = func() error { return writeNewFileAfter(out, file, record) }
		}
		if err := write(); err != nil {
			return err
		}
		result["sealed_report"], result["file"] = digest, out
	}
	return cli.Output(c, result)
}

// personSignatureNote is the report's fixed interim line beside settled
// (Sherlock2, evidencebook#727): no act carries the person's own signature
// until its format exists. evidencebook#729 replaces it with the check per act.
const personSignatureNote = "No act on this deal carries the person's own signature yet (the format is still being defined: agent-action-capsule#627). \"Settled\" here is computed from the agent's records, which the person has not signed."

// v1ReportError says in plain words why a report could not be computed.
func v1ReportError(dealID string, p cli.Profile, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "has no checkpoint at its size"):
		every := "about every 5m"
		if cfg, cerr := p.DealCadence(); cerr == nil {
			every = "about every " + cli.ShortDuration(cfg.Interval)
		}
		return cli.InputError("your deal book of " + dealID + " has records after its last checkpoint, and a report rests on a checkpoint: the next cadence tick covers it (" + every + "; deal tick runs one once it is due), then run deal report again")
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

// writeNewFile writes data, exactly, to a file that must not exist yet.
func writeNewFile(path string, data []byte) error {
	if _, err := os.Lstat(path); err == nil {
		return cli.InputError(path + " exists: --out names a new file")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return cli.AtomicFile(path, data, false)
}

// writeNewFileAfter writes data, exactly, to a file that must not exist yet,
// running before between staging the bytes and putting them in place (a
// shared copy's record). Everything that can fail on its own fails before
// before runs: the bytes are written and synced to a temporary file beside
// path, and the folder is shown to take a hard link, since the copy is
// linked into place and a link, unlike a rename, never replaces a file. Only
// the link (path taken meanwhile) and the folder's sync come after it, and
// each error says what is on record and what is on disk.
func writeNewFileAfter(path string, data []byte, before func() error) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".capsulectl-deal-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if e := os.Remove(tmp); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, e)
		}
	}()
	if err = f.Chmod(0o600); err == nil {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	probe := tmp + ".link"
	if err = os.Link(tmp, probe); err != nil {
		return cli.InputError("the folder of " + path + " does not take a hard link (" + err.Error() + "), which a shared copy is put in place with, so it is never written over another file: choose --out on another drive")
	}
	if err = os.Remove(probe); err != nil {
		return err
	}
	if _, err = os.Lstat(path); err == nil {
		return cli.InputError(path + " exists: --out names a new file")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = before(); err != nil {
		return err
	}
	if err = os.Link(tmp, path); err != nil {
		return fmt.Errorf("the shared copy is on record, but %s could not be written (%w): run deal report again to write and record a new copy", path, err)
	}
	// The directory entry is made durable too, so a crash cannot keep the
	// record and lose the file.
	if err = v1SyncDir(dir); err != nil {
		return fmt.Errorf("the shared copy is on record and written to %s, but its folder could not be synced (%w): the file may not survive a crash until the system flushes it", path, err)
	}
	return nil
}

// syncDir syncs a directory, making the entries in it durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err = d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// v1SyncDir is syncDir, a seam for tests.
var v1SyncDir = syncDir

// v1ShareGate is dealShareGate, a seam for tests.
var v1ShareGate = dealShareGate

// recordV1Share puts a shared copy of an x-deal-v1 report on record before
// it is written (persistShare): the deal, the audience, the recipient, the
// digest of the exact bytes, the time and a fresh nonce. It carries none of
// an x-deal-v0 copy's receipt-page fields: a report pair is no bundle page.
func (s *dealSession) recordV1Share(ctx context.Context, p cli.Profile, dealID, audience, recipient, adjudicatorKey string, data []byte) (map[string]any, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	record := map[string]interface{}{
		"profile": xdealv1.Profile, "kind": "report", "deal_id": dealID, "audience": audience, "recipient": recipient,
		"receipt_sha256": hex.EncodeToString(sum[:]),
		"at":             cli.DealClock().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z"),
		"nonce":          hex.EncodeToString(nonce),
	}
	if adjudicatorKey != "" {
		record["adjudicator_key_id"] = adjudicatorKey
	}
	s.dp = p
	s.dp.LogID = cli.DealLogID(dealID)
	return s.persistShare(ctx, dealID, record, audience, recipient)
}
