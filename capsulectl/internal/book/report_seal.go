package book

import (
	"context"
	"errors"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/report"
	"github.com/spf13/cobra"
)

type reportCheckResult struct {
	Page         string `json:"page"`
	PageDigest   string `json:"page_digest"`
	BundleDigest string `json:"bundle_digest"`
	RecordID     string `json:"record_id,omitempty"`
	// Verification is the embedded evidence's own check (the one report
	// build applies), kept apart from Page, which says only whether the
	// page's bytes are the bytes the book sealed.
	Verification string `json:"verification"`
}

// reportCheckCommand is `report check`: whether a page file is the page the
// book sealed (report.CheckPage). The output always says both answers. The
// exit code is `verify --bundle`'s (1 INVALID, 3 INCOMPLETE) when the
// embedded evidence is not VALID, which comes first; else 0 for a match, 5
// (conflict) for a mismatch, and 3 (partial) when the book holds no seal, or
// one no checkpoint covers yet. A file that embeds no bundle is not a page:
// exit 2.
func reportCheckCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "check", Short: "Check a report page's bytes against the seal its book holds; the embedded evidence is verified separately", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := cli.Selected(c)
		if e != nil {
			return e
		}
		path, _ := c.Flags().GetString("page")
		page, e := cli.ReadBundleInput(path)
		if e != nil {
			return e
		}
		// A file that is not a report page is refused before the book is
		// opened.
		if _, e = report.InspectPage(page); e != nil {
			return refused(e)
		}
		opened, e := openBookForReading(c.Context(), p)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		check, e := report.CheckPage(c.Context(), opened.book, page)
		if e != nil {
			return refused(e)
		}
		if e = cli.Output(c, reportCheckResult{Page: check.Outcome, PageDigest: check.PageDigest, BundleDigest: check.BundleDigest, RecordID: check.RecordID, Verification: check.Verdict}); e != nil {
			return e
		}
		switch {
		case check.Refusal != nil:
			return check.Refusal
		case check.Outcome == report.PageMismatch:
			return errors.Join(cli.ErrConflict, errors.New("the page's bytes are not the bytes the book sealed for its bundle"))
		case check.Outcome != report.PageMatch:
			return errors.Join(cli.ErrPartial, errors.New("page: "+check.Outcome))
		}
		return nil
	}}
	cmd.Flags().String("page", "", "The report page file to check")
	_ = cmd.MarkFlagRequired("page")
	return cmd
}

// sealReport opens the profile's book and seals a written page.
func sealReport(ctx context.Context, p cli.Profile, page []byte) (sealed report.Seal, err error) {
	opened, err := openBook(ctx, p, false)
	if err != nil {
		return report.Seal{}, err
	}
	defer func() { err = errors.Join(err, opened.release()) }()
	sealed, err = report.SealPage(ctx, opened.book, page)
	if err != nil && sealed.RecordID == "" {
		err = bookError(err)
	}
	return sealed, err
}
