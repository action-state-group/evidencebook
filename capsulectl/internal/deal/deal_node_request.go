package deal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// `deal node request` and `deal node respond` are the core's request and
// respond verbs (cli.EvidenceRequestCommand and cli.EvidenceRespondCommand,
// which `book request` and `book respond` share) on a node's own book of the
// deal profile's shelf (evidencebook#624): the node asks another party for
// evidence and records what came back or that nothing came back, and answers
// the requests addressed to it, each a signed record of the node book. The
// transport (the Nostr mailbox, a relay, a pipe) is the host's; these verbs
// read and write files and record.

// nodeBookError words a book refusal as an input error, as `book` does.
func nodeBookError(err error) error {
	if errors.Is(err, evidencebook.ErrNotFound) {
		return errors.Join(cli.InputError("not found in the node book: "+err.Error()), err)
	}
	if errors.Is(err, evidencebook.ErrInvalid) {
		return errors.Join(cli.InputError("the node book refused this as invalid: "+err.Error()), err)
	}
	return err
}

func dealNodeRequestCommand() *cobra.Command {
	cmd := cli.EvidenceRequestCommand(func(c *cobra.Command, p cli.Profile, in cli.EvidenceRequestInput) (err error) {
		book, _ := c.Flags().GetString("book")
		if err = nodeBookID(p, book); err != nil {
			return err
		}
		// The witnesses whose receipts count on the answer's anchor, read before
		// the shelf opens, so a bad directory records nothing.
		var witnesses []evidencebook.WitnessRow
		if in.WitnessDirectory != "" {
			raw, e := os.ReadFile(in.WitnessDirectory)
			if e != nil {
				return cli.InputError("--witness-directory: " + e.Error())
			}
			if witnesses, e = evidencebook.ParseWitnessDirectory(raw); e != nil {
				return cli.InputError("--witness-directory: " + e.Error())
			}
		}
		var req evidencebook.EvidenceRequest
		if in.Request != nil {
			if in.Responder == "" || in.Output == "" {
				return cli.InputError("--request needs --responder and --output")
			}
			if req, err = parseNodeRequest("--request", in.Request); err != nil {
				return err
			}
		}
		ctx := c.Context()
		s, err := openDealSession(ctx, p)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.close()) }()
		b, err := s.shelf.Open(ctx, book)
		if err != nil {
			return err
		}
		switch {
		case in.Request != nil:
			sent, e := b.Request(ctx, req, in.Responder)
			if e != nil {
				return nodeBookError(e)
			}
			result := cli.EvidenceRequestResult{RecordID: sent.RecordID, RequestDigest: sent.Digest, Output: in.Output}
			if e = cli.AtomicFile(in.Output, sent.Bytes, false); e != nil {
				// The request is on record already: report it, and the bytes to
				// send, rather than lose them with the file.
				result.Output, result.Request = "", sent.Bytes
				return errors.Join(e, cli.Output(c, result))
			}
			return cli.Output(c, result)
		case in.Response != nil:
			keys := evidencebook.ResponderKeys{Signer: in.ResponderSigner, Checkpoint: in.ResponderCheckpoint, Witnesses: witnesses}
			record, e := b.RecordResponse(ctx, in.For, evidencebook.Message{Octets: in.Response, MediaType: evidencebook.MediaTypeJSON}, keys)
			if e != nil {
				return nodeBookError(e)
			}
			var st evidencebook.ResponseStatement
			if e = json.Unmarshal(record.Header.Statement, &st); e != nil {
				return e
			}
			return cli.Output(c, map[string]any{"record_id": record.RecordID, "request_digest": st.RequestDigest, "outcome": st.Outcome, "reason": st.Reason, "witness": st.Witness})
		default:
			record, e := b.RecordAbsence(ctx, in.For, in.WindowEnd, in.Route)
			if e != nil {
				return nodeBookError(e)
			}
			var st evidencebook.AbsenceRecord
			if e = json.Unmarshal(record.Header.Statement, &st); e != nil {
				return e
			}
			return cli.Output(c, cli.EvidenceRequestResult{RecordID: record.RecordID, RequestDigest: st.RequestDigest, Outcome: evidencebook.RecordTypeAbsence})
		}
	})
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Short = "Ask a party for evidence from a node book, or record its answer or its absence (each is a signed record of the node book)"
	return cmd
}

func dealNodeRespondCommand() *cobra.Command {
	cmd := cli.EvidenceRespondCommand(func(c *cobra.Command, p cli.Profile, in cli.EvidenceRespondInput) (err error) {
		book, _ := c.Flags().GetString("book")
		if err = nodeBookID(p, book); err != nil {
			return err
		}
		policy := evidencebook.DefaultSharePolicy()
		if in.PolicyPath != "" {
			raw, e := cli.ReadInput(in.PolicyPath)
			if e != nil {
				return e
			}
			if e = cli.DecodeJSONAs("--policy "+in.PolicyPath, raw, &policy); e != nil {
				return e
			}
		}
		ctx := c.Context()
		s, err := openDealSession(ctx, p)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.close()) }()
		b, err := s.shelf.Open(ctx, book)
		if err != nil {
			return err
		}
		// The operator running respond is the transport: --requester is the
		// identity they authenticated the request's sender as, and the only
		// input to the book's relationship gate. Empty, only public subjects
		// are answered.
		reply, record, e := b.Respond(ctx, evidencebook.Message{Octets: in.Request, MediaType: evidencebook.MediaTypeJSON},
			evidencebook.RespondOptions{AuthenticatedRequester: in.Requester, Policy: policy})
		if e != nil {
			return nodeBookError(e)
		}
		result := cli.EvidenceRespondResult{RecordID: record.RecordID, Output: in.Output}
		switch {
		case record.RecordID != "":
			var st evidencebook.AnsweredStatement
			if e = json.Unmarshal(record.Header.Statement, &st); e != nil {
				return e
			}
			result.RequestDigest, result.Outcome, result.Reason, result.Relationship = st.RequestDigest, st.Outcome, st.Reason, st.Relationship
		case reply.Refusal != nil:
			// A malformed or unauthenticated request is refused without a record:
			// such traffic never grows the book.
			result.RequestDigest, result.Outcome, result.Reason = reply.Refusal.RequestDigest, evidencebook.OutcomeRefusal, reply.Refusal.Reason
		case reply.Envelope != nil:
			result.RequestDigest, result.Outcome = reply.Envelope.RequestDigest, evidencebook.OutcomeArtifact
		}
		if e = cli.AtomicFile(in.Output, reply.Octets, false); e != nil {
			// The reply is signed (and, when record_id is set, on record)
			// already: report it rather than lose it with the file.
			result.Output, result.Response = "", reply.Octets
			return errors.Join(e, cli.Output(c, result))
		}
		return cli.Output(c, result)
	})
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Short = "Answer a request addressed to a node book with a signed reply, and record it"
	return cmd
}

// dealNodeReplyCheckCommand is the signature check a late reply gets: whether
// a reply is an answer, an artifact or a refusal, that the responder signed for
// the request with a given digest. It records nothing; the host records a late
// reply beside an absence with `deal node append`.
func dealNodeReplyCheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reply-check",
		Short: "Whether a reply is an artifact or a refusal the named responder signed for the request with a given digest (seals nothing)",
		Long: `Check a reply file offline: it must parse as an evidence-request reply (an envelope or a refusal), be
signed by --signer, name the request with --request-digest, and verify. The result is {"signed": true|false, "kind":
"artifact"|"refusal"}; a reply that does not qualify is signed: false, not an error. Nothing is recorded: this is the
check a reply gets that arrives after its request's absence was recorded.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			path, _ := c.Flags().GetString("reply")
			digest, _ := c.Flags().GetString("request-digest")
			signer, _ := c.Flags().GetString("signer")
			if path == "" || digest == "" || signer == "" {
				return cli.InputError("--reply, --request-digest and --signer are required")
			}
			raw, err := cli.ReadInput(path)
			if err != nil {
				return err
			}
			reply, err := evidencebook.ParseReply(evidencebook.Message{Octets: raw, MediaType: evidencebook.MediaTypeJSON})
			if err != nil {
				return cli.Output(c, map[string]any{"signed": false})
			}
			switch {
			case reply.Refusal != nil:
				return cli.Output(c, map[string]any{"signed": reply.Refusal.VerifyFor(signer, digest) == nil, "kind": "refusal"})
			case reply.Envelope != nil:
				e := reply.Envelope
				return cli.Output(c, map[string]any{"signed": e.RequestDigest == digest && e.Responder == signer && e.Verify() == nil, "kind": "artifact"})
			}
			return cli.Output(c, map[string]any{"signed": false})
		},
	}
	cmd.Flags().String("reply", "", "The reply file (required)")
	cmd.Flags().String("request-digest", "", "The digest of the request it should answer (required)")
	cmd.Flags().String("signer", "", "The responder's signing key as hex (required)")
	return cmd
}

// nodeRequestInput is the request map of draft-mih-agent-evidence-request-00 in
// JSON, as `request --request` reads it (the same file `book request` reads).
// The book plugin keeps the same reader: the two plugins cannot link each
// other's package (the launcher boundary), and evidencebook exports the
// request as a struct only.
type nodeRequestInput struct {
	Subject  map[string]json.RawMessage `json:"subject"`
	Coverage struct {
		ExpectedPin  string          `json:"expected_pin,omitempty"`
		MinFreshness json.RawMessage `json:"min_freshness,omitempty"`
	} `json:"coverage"`
	Derivation string `json:"derivation,omitempty"`
	Deadline   string `json:"deadline,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	Route      string `json:"route,omitempty"`
	Profile    string `json:"profile,omitempty"`
}

func parseNodeRequest(label string, raw []byte) (evidencebook.EvidenceRequest, error) {
	var in nodeRequestInput
	if err := cli.DecodeJSONAs(label, raw, &in); err != nil {
		return evidencebook.EvidenceRequest{}, err
	}
	req := evidencebook.EvidenceRequest{
		Coverage:   evidencebook.Coverage{ExpectedPin: in.Coverage.ExpectedPin},
		Derivation: in.Derivation, Deadline: in.Deadline, Nonce: in.Nonce, Route: in.Route, Profile: in.Profile,
	}
	if len(in.Subject) != 1 {
		return req, cli.InputError(label + ": subject is a map with exactly one member, its form: full_history, checkpoints, checkpoint_range, record, exchange, range or correlation")
	}
	for form, content := range in.Subject {
		req.Subject.Kind = evidencebook.SubjectKind(form)
		var err error
		switch req.Subject.Kind {
		case evidencebook.SubjectFullHistory, evidencebook.SubjectCheckpoints:
			if string(bytes.TrimSpace(content)) != "null" {
				err = errors.New("its content is null")
			}
		case evidencebook.SubjectRecord, evidencebook.SubjectExchange:
			err = json.Unmarshal(content, &req.Subject.Digest)
		case evidencebook.SubjectRange, evidencebook.SubjectCheckpointRange:
			// *uint64 so a null position is refused rather than read as 0.
			var pair []*uint64
			if err = json.Unmarshal(content, &pair); err == nil && (len(pair) != 2 || pair[0] == nil || pair[1] == nil) {
				err = errors.New("its content is two log positions")
			} else if err == nil {
				req.Subject.Range = [2]uint64{*pair[0], *pair[1]}
			}
		case evidencebook.SubjectCorrelation:
			err = json.Unmarshal(content, &req.Subject.Correlation)
		default:
			err = errors.New("not a subject form")
		}
		if err != nil {
			return req, cli.InputError(fmt.Sprintf("%s: subject %q: %v", label, form, err))
		}
	}
	if floor := bytes.TrimSpace(in.Coverage.MinFreshness); len(floor) > 0 {
		req.Coverage.MinFreshness = &evidencebook.Freshness{}
		var err error
		switch {
		case string(floor) == "null":
			err = errors.New("null")
		case floor[0] == '"':
			err = json.Unmarshal(floor, &req.Coverage.MinFreshness.Time)
		default:
			var size uint64
			err = json.Unmarshal(floor, &size)
			req.Coverage.MinFreshness.Size = &size
		}
		if err != nil {
			return req, cli.InputError(label + ": min_freshness is a log size or an RFC 3339 UTC time")
		}
	}
	return req, nil
}
