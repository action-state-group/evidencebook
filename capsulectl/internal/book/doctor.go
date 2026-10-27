package book

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// doctorCommand is `capsulectl book doctor`: the book's list of findings
// about itself (evidencebook Book.Doctor), never a score. It seals nothing.
// With --probe it asks this node's own responder, the path `book respond`
// answers with no --requester, as a stranger would: a profile records no
// responder address, and a stranger's request is the one Respond never
// seals or checkpoints for.
func doctorCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "doctor", Short: "List what this book shows about itself: self-attested, unwitnessed, NOT_CHECKED, unsupported, controller binding, sharing (a list, never a score; seals nothing)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := cli.Selected(c)
		if e != nil {
			return e
		}
		if p.Type == "sqlite" {
			return cli.InputError("this is a deal profile: book doctor reads an evidence book")
		}
		opts := evidencebook.DoctorOptions{Policy: evidencebook.DefaultSharePolicy()}
		if path, _ := c.Flags().GetString("policy"); path != "" {
			raw, e := cli.ReadInput(path)
			if e != nil {
				return e
			}
			if e = cli.DecodeJSONAs("--policy "+path, raw, &opts.Policy); e != nil {
				return e
			}
		}
		bindings, _ := c.Flags().GetStringArray("controller-binding")
		for _, path := range bindings {
			raw, e := cli.ReadInput(path)
			if e != nil {
				return e
			}
			if !json.Valid(raw) {
				return cli.InputError("--controller-binding " + path + " is not JSON")
			}
			opts.ControllerBindings = append(opts.ControllerBindings, raw)
		}
		// The keys the probe verifies under are the profile's own, which
		// this node hands a requester: a book whose served checkpoints are
		// signed under another key (a rotation, say) does not verify.
		for _, k := range []struct {
			secret cli.Secret
			into   *string
		}{{p.Signing, &opts.Keys.Signer}, {p.Checkpoint.Signing, &opts.Keys.Checkpoint}} {
			key, e := cli.PrivateKey(k.secret)
			if e != nil {
				return e
			}
			*k.into = hex.EncodeToString(key.Public().(ed25519.PublicKey))
		}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		// Read under the book's lock, which makes its holder the witness
		// store's only writer (bookWitnessStore).
		deliveries, e := newBookWitnessStore(p).load()
		if e != nil {
			return e
		}
		for _, w := range deliveries {
			opts.Witnesses = append(opts.Witnesses, evidencebook.DoctorWitness{Witness: w.WitnessID, TreeSize: w.CheckpointSize, Receipt: w.Receipt != nil})
		}
		if probe, _ := c.Flags().GetBool("probe"); probe {
			opts.Probe = strangerPath(opened.book, opts.Policy)
		}
		findings, e := opened.book.Doctor(c.Context(), opts)
		if e != nil {
			return bookError(e)
		}
		return cli.Output(c, map[string]any{"findings": findings})
	}}
	cmd.Flags().String("policy", "", "The share policy this node advertises and answers under, as `book respond --policy` reads it (default: the documented defaults)")
	cmd.Flags().StringArray("controller-binding", nil, "A controller binding record (controller-binding/0) this node holds; repeatable")
	cmd.Flags().Bool("probe", false, "Also ask for checkpoints, then history_card/1, as a stranger through the in-process `book respond` path (no --requester, not a network endpoint), and verify both; a match shows what a stranger is served, not that the policy is verified")
	return cmd
}

// strangerPath answers a request as `capsulectl book respond` does with no
// --requester. A reply that sealed a record means the request was
// authenticated after all, which doctor must never do.
func strangerPath(book *evidencebook.Book, policy evidencebook.SharePolicy) evidencebook.DoctorTransport {
	return func(ctx context.Context, request evidencebook.Message) (evidencebook.Message, error) {
		reply, record, err := book.Respond(ctx, request, evidencebook.RespondOptions{Policy: policy})
		if err != nil {
			return evidencebook.Message{}, err
		}
		if record.RecordID != "" {
			return evidencebook.Message{}, errors.New("the responder sealed a record for the probe")
		}
		return reply.Message, nil
	}
}
