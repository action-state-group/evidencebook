package book

import (
	"errors"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// policyCommands is `capsulectl book policy`: the disclosure policy the
// book holds (evidencebook DisclosurePolicy). adopt and change seal policy
// records; show reads.
func policyCommands() *cobra.Command {
	policy := &cobra.Command{Use: "policy", Short: "The disclosure policy this book holds: adopt the default, show what it sends, change it", Args: cli.NoArgs}
	policy.AddCommand(policyAdoptCommand(), policyShowCommand(), policyChangeCommand())
	return policy
}

// policyError maps the policy refusals bookError does not know to input
// errors: a book with no policy, a change composed against another round,
// and a change naming a never-list item. None of them seals anything.
func policyError(err error) error {
	if errors.Is(err, evidencebook.ErrNoPolicy) || errors.Is(err, evidencebook.ErrStaleRound) || errors.Is(err, evidencebook.ErrNeverList) {
		return errors.Join(cli.InputError("the evidence book refused this policy request, and sealed nothing: "+err.Error()), err)
	}
	return bookError(err)
}

func writablePolicyProfile(c *cobra.Command, verb string) (cli.Profile, error) {
	p, err := cli.Selected(c)
	if err != nil {
		return p, err
	}
	if p.Type == "sqlite" {
		return p, cli.InputError("this is a deal profile: " + verb + " applies to an evidence book")
	}
	if !isBook(p) || p.ReadOnly {
		return p, cli.InputError(verb + " applies to a writable book profile")
	}
	return p, refuseUnderReproduction(verb)
}

func policyAdoptCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "adopt", Short: "Seal the shipped default disclosure policy for a holder kind, once (signs)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := writablePolicyProfile(c, "book policy adopt")
		if e != nil {
			return e
		}
		holder, _ := c.Flags().GetString("holder")
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		adopted, record, e := opened.book.AdoptPolicy(c.Context(), holder)
		if e != nil {
			return policyError(e)
		}
		return cli.Output(c, map[string]any{"policy": adopted, "record": record, "settings": adopted.Rows()})
	}}
	cmd.Flags().String("holder", "", "Who holds the book: `person` or `node` (required)")
	_ = cmd.MarkFlagRequired("holder")
	return cmd
}

func policyShowCommand() *cobra.Command {
	return &cobra.Command{Use: "show", Short: "Show each disclosure setting and what it sends (seals nothing)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := cli.Selected(c)
		if e != nil {
			return e
		}
		if p.Type == "sqlite" {
			return cli.InputError("this is a deal profile: book policy show reads an evidence book")
		}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		held, e := opened.book.DisclosurePolicy()
		if e != nil {
			return policyError(e)
		}
		return cli.Output(c, map[string]any{"policy": held, "settings": held.Rows()})
	}}
}

func policyChangeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "change", Short: "Apply a policy-change/v1 block: one policy record per changed setting (signs)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := writablePolicyProfile(c, "book policy change")
		if e != nil {
			return e
		}
		path, _ := c.Flags().GetString("change")
		raw, e := cli.ReadInput(path)
		if e != nil {
			return e
		}
		change, e := evidencebook.ParsePolicyChange(raw)
		if e != nil {
			return bookError(e)
		}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		now, records, e := opened.book.ApplyPolicyChange(c.Context(), change)
		if e != nil {
			return policyError(e)
		}
		return cli.Output(c, map[string]any{"policy": now, "records": records, "then": change.Then, "settings": now.Rows()})
	}}
	cmd.Flags().String("change", "", "A policy-change/v1 file, or `-` for stdin (required)")
	_ = cmd.MarkFlagRequired("change")
	return cmd
}
