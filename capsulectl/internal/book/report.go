package book

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/report"
	"github.com/spf13/cobra"
)

// readPresentation reads --presentation: one presentation/v1 block, as
// report.DecodePresentation checks it.
func readPresentation(path string) (*report.Presentation, error) {
	raw, err := cli.ReadInput(path)
	if err != nil {
		return nil, err
	}
	block, err := report.DecodePresentation(raw)
	if err != nil {
		return nil, refused(err)
	}
	return &block, nil
}

// refused gives a report package refusal the input class (exit 2), naming
// the flag its subject came from; any other error is returned as it is.
func refused(err error) error {
	if !errors.Is(err, report.ErrRefused) {
		return err
	}
	message := err.Error()
	for _, flag := range [][2]string{{"presentation ", "--presentation "}, {"card ", "--card "}, {"not a report page", "--page is not a report page"}, {"the page's ", "--page's "}} {
		if rest, ok := strings.CutPrefix(message, flag[0]); ok {
			message = flag[1] + rest
			break
		}
	}
	return cli.Hint(cli.ErrInput, message)
}

type reportBuildResult struct {
	Root              string   `json:"root"`
	Form              string   `json:"form"`
	Member            string   `json:"member"`
	Card              string   `json:"card"`
	Claims            int      `json:"claims"`
	UnsupportedClaims int      `json:"unsupported_claims"`
	ContractRefs      []string `json:"contract_refs"`
	Report            string   `json:"report"`
	Permalink         string   `json:"permalink,omitempty"`
	BundleDigest      string   `json:"bundle_digest"`
	Verification      string   `json:"verification"`
	Draft             bool     `json:"draft"`
	// Verdict is `verify --bundle`'s own verdict on the bundle (VALID,
	// INCOMPLETE or INVALID), and UnmetClaims the claims that kept it from
	// VALID. Page says whether the page was written: only for a VALID
	// bundle (pageGate), else "not written".
	Verdict     string   `json:"verdict"`
	UnmetClaims []string `json:"unmet_claims,omitempty"`
	Page        string   `json:"page"`
	// PageSeal is the record sealing the page's bytes (--seal).
	PageSeal *report.Seal `json:"page_seal,omitempty"`
}

func reportCommands() *cobra.Command {
	group := &cobra.Command{Use: "report", Short: "Render a verified Result-root Evidence Bundle into an offline report"}
	build := &cobra.Command{Use: "build", Short: "Verify a held Evidence Bundle rooted at a sealed Result v0 and write report.html (and a permalink); touches no store unless --seal", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		bundlePath, _ := c.Flags().GetString("bundle")
		card, _ := c.Flags().GetString("card")
		out, _ := c.Flags().GetString("out")
		presentationPath, _ := c.Flags().GetString("presentation")
		permalink, _ := c.Flags().GetBool("permalink")
		dryRun, _ := c.Flags().GetBool("dry-run")
		var sealProfile *cli.Profile
		if seal, _ := c.Flags().GetBool("seal"); seal && !dryRun {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			if p.Type == "sqlite" || !p.IsBook() {
				return cli.InputError("--seal needs a book profile: the page's seal is a record of its book")
			}
			sealProfile = &p
		}
		if bundlePath == "" || out == "" {
			return cli.InputError("--bundle and --out are required")
		}
		view := report.View{Card: card, Draft: dryRun}
		if presentationPath != "" {
			var err error
			if view.Presentation, err = readPresentation(presentationPath); err != nil {
				return err
			}
		}
		raw, err := cli.ReadBundleInput(bundlePath)
		if err != nil {
			return err
		}
		// Nothing is written until everything report.Build checks has
		// passed; when only the page gate refuses, the result is still
		// reported, and the refusal says why.
		page, err := report.Build(raw, view)
		if page == nil {
			return refused(err)
		}
		result := reportBuildResult{Root: page.Root, Form: page.Form, Member: page.Member, Card: card, Claims: page.Claims, UnsupportedClaims: page.UnsupportedClaims, ContractRefs: page.ContractRefs, Report: out, BundleDigest: page.BundleDigest, Verification: page.Verification, Draft: dryRun, Verdict: page.Verdict, UnmetClaims: page.UnmetClaims, Page: "written"}
		if err != nil {
			result.Page, result.Report = "not written", ""
			if outErr := cli.Output(c, result); outErr != nil {
				return outErr
			}
			return err
		}
		if permalink && !dryRun {
			base, _ := c.Flags().GetString("base-url")
			if result.Permalink, err = page.Permalink(base); err != nil {
				return err
			}
		}
		if err = cli.AtomicFile(out, page.HTML, false); errors.Is(err, os.ErrExist) {
			return cli.Hint(cli.ErrInput, fmt.Sprintf("--out %s already exists (or was created by another process while this one ran); nothing was overwritten; pass another path", out))
		} else if err != nil {
			return err
		}
		if sealProfile != nil {
			// The page is on disk from here on: a failure sealing it still
			// reports the page, so the caller never loses it.
			sealed, err := sealReport(c.Context(), *sealProfile, page)
			result.PageSeal = &sealed
			if err != nil {
				return errors.Join(err, cli.Output(c, result))
			}
		}
		return cli.Output(c, result)
	}}
	build.Flags().String("bundle", "", "A held, disclosed Evidence Bundle whose root is a sealed Result v0")
	build.Flags().String("card", "", "Renderer card: "+strings.Join(report.Cards(), ", ")+" (recorded in the report's report-card/v1 block)")
	build.Flags().String("presentation", "", "presentation/v1 header block JSON: producer_display_name, logo_data_url (data:image/ only), title")
	build.Flags().String("out", "", "Write the offline report.html here (new file)")
	build.Flags().Bool("permalink", false, "Also mint a viewer permalink over the same embedded bundle")
	build.Flags().String("base-url", cli.DefaultBundleURL, "Bundle viewer base URL for --permalink")
	build.Flags().Bool("dry-run", false, "Write the page marked draft in its report-card/v1 block and mint no permalink")
	build.Flags().Bool("seal", false, "Also seal the written page's SHA-256 in the book of --profile, with the bundle digest and the Result root, and checkpoint it; `report check` checks a page against it. A --dry-run page is never sealed")
	group.AddCommand(build, reportCheckCommand())
	return group
}
