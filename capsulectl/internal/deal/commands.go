package deal

import (
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Vendor is what both first-party plugins name in their metadata.
const Vendor = "action-state-group"

// Name is the plugin's name: the launcher capsulectl-deal, the command
// `capsulectl deal`, and the log owner a deal profile names (log: deal).
const Name = "deal"

// DealMetadata is capsulectl-deal's cli-plugin-metadata answer.
var DealMetadata = cli.PluginMetadata{Name: Name, Vendor: Vendor, Subcommands: []string{"init", "open", "check", "note", "close", "report", "export", "list", "history", "deadlines", "settlement", "terms-leg", "audit-host", "reconcile", "countersign", "verify-email", "verify-object", "verifier-statement", "verify-statement", "tick", "checkpoint", "sale", "compose", "request", "respond", "terms-result", "canary"}, Presentations: dealViewPresentations()}

// DealCommands are capsulectl-deal's own commands: `capsulectl deal ...`.
func DealCommands() []*cobra.Command {
	return dealCommands().Commands()
}

// DealGroup is the deal commands under one `deal` command, as the core tree
// holds a plugin's: what tests run `deal ...` against.
func DealGroup() *cobra.Command {
	return dealCommands()
}

// dealCommands' own request, respond and compose: a deal profile's copies.
func dealEvidenceCommands() []*cobra.Command {
	notDeal := func(p cli.Profile) error {
		if p.Type != "sqlite" {
			return cli.InputError("this is not a deal profile: an evidence book's requests are `capsulectl book request` and `capsulectl book respond`")
		}
		return nil
	}
	return []*cobra.Command{onProfiles(requestCommand(), notDeal), onProfiles(respondCommand(), notDeal), composeCommand()}
}

// onProfiles runs check on the selected profile before cmd's own RunE.
func onProfiles(cmd *cobra.Command, check func(cli.Profile) error) *cobra.Command {
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		if err := check(p); err != nil {
			return err
		}
		return run(c, args)
	}
	return cmd
}

// InstallDealHooks gives the core tree the deal commands it reaches from its
// own: `bundle --deal`, `cll checkpoint cadence`, and the deal verbs `canary
// run` drives. Call it before the tree is built.
func InstallDealHooks() {
	cli.ExtendBundle(cli.BundleExtension{Flags: dealBundleFlags, Run: dealBundleExtension})
	cli.GraftCommand([]string{"cll", "checkpoint"}, cadenceCommand)
	cli.ErrorPhase = dealErrorPhase
}

// cadenceCommand is `deal tick` under the core's checkpoint verbs.
func cadenceCommand() *cobra.Command {
	cadence := dealTickCommand()
	cadence.Use = "cadence"
	return cadence
}

// dealBundleFlags defines the flags the plugin adds to bundle, disclose and
// permalink. A flag the core still defines is left to it.
func dealBundleFlags(f *pflag.FlagSet, use string) {
	if f.Lookup("deal") == nil {
		f.String("deal", "", "A deal on a deal profile: the whole deal from its own log, built as `deal report` builds it (disclose also needs --share and --to; a deal is not shared as a link)")
	}
	if f.Lookup("share") == nil {
		f.String("share", cli.DealAudienceKeep, "With --deal: who the copy is for: keep (bundle only: your own copy), counterparty or adjudicator (disclose only: a shared copy, put on record first)")
	}
	if f.Lookup("to") == nil {
		f.String("to", "", "With --deal and --share: who the shared copy is for, as sealed in the disclosure record")
	}
}

// dealBundleExtension runs bundle, disclose and permalink with --deal.
func dealBundleExtension(c *cobra.Command, use string) (bool, error) {
	if deal, _ := c.Flags().GetString("deal"); deal == "" {
		if c.Flags().Changed("share") || c.Flags().Changed("to") {
			return true, cli.InputError("--share and --to go with --deal: they choose what a deal's shared copy withholds")
		}
		// On a deal profile the core refuses the store (it is the plugin's);
		// the plugin says what to pass instead.
		if p, err := cli.Selected(c); err == nil && p.LogOwner() == Name {
			if c.Flags().Changed("log-id") {
				return true, cli.InputError("a deal's logs are read with --deal DEAL_ID, which applies the deal's share rules; --log-id does not")
			}
			return true, cli.InputError("this is a deal profile: each deal is its own log; name the deal with --deal DEAL_ID")
		}
		return false, nil
	}
	if html, _ := c.Flags().GetString("html"); html != "" {
		return true, cli.InputError("a deal's page is its receipt: `deal report --deal ID --html FILE` (or `--share ... --html FILE` for someone else)")
	}
	return true, dealBundleRun(c, use)
}
