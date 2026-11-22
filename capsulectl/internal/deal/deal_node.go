package deal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// `capsulectl deal node ...` is a node's own book on the deal profile's
// shelf (evidencebook#624): the records a node keeps outside any deal, such as
// joining, admitting a peer, a counterparty's half it verified, and the
// requests it sent. They sit on the one shelf the deals are on, so one
// cadence covers them and the History card counts from one anchor.
//
// The record types are the node's own vocabulary, never this plugin's: a
// type is a name the caller picks, and every record is a producer claim. A
// deal's own books and the cadence stay out of reach of these verbs: only the
// deal verbs write them.

// nodeBookID checks that id names a node book, not one of the shelf's deal,
// sale, cadence or journal logs, which only the deal verbs write.
func nodeBookID(p cli.Profile, id string) error {
	switch {
	case id == "":
		return cli.InputError("--book is required: the node's book on the shelf, such as goose/node")
	case id == evidencebook.JournalLogID, id == p.LogID, strings.HasPrefix(id, "deal/"), strings.HasPrefix(id, "deal-cadence/"), strings.HasPrefix(id, "sale/"), strings.HasPrefix(id, "sale-"):
		return cli.InputError("--book " + id + " is one of the shelf's own logs (a deal, a sale, the cadence or the journal): only the deal verbs write them")
	}
	return nil
}

func dealNodeCommands() *cobra.Command {
	group := &cobra.Command{Use: "node", Short: "A node's own book on the deal profile's shelf: append a record, list records", Args: cli.NoArgs}
	group.AddCommand(dealNodeAppendCommand(), dealNodeRecordsCommand())
	return group
}

// nodeRecord is one record as the node verbs print it.
type nodeRecord struct {
	RecordID    string          `json:"record_id"`
	Seq         uint64          `json:"seq"`
	RecordType  string          `json:"record_type"`
	SubjectRef  string          `json:"subject_ref,omitempty"`
	CommittedAt string          `json:"committed_at"`
	Statement   json.RawMessage `json:"statement,omitempty"`
	Cites       []string        `json:"cites,omitempty"`
}

func nodeRecordOf(r evidencebook.Record) nodeRecord {
	out := nodeRecord{RecordID: r.RecordID, Seq: r.Seq, RecordType: r.Header.RecordType, SubjectRef: r.Header.SubjectRef, CommittedAt: r.Header.CommittedAt, Statement: r.Header.Statement}
	for _, l := range r.Header.Links {
		if l.Type == evidencebook.Cites {
			out.Cites = append(out.Cites, l.Target)
		}
	}
	return out
}

func dealNodeAppendCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "append",
		Short: "Seal one record of a node book: a type, a statement, what it cites and its payloads (signs)",
		Long: `Seal one record of the node book --book names, as a producer claim: the node's own word that
something happened, signed under the profile's key. --type is the node's own name for the record;
--statement is a JSON object file (or - for standard input).

--once MEMBER makes the call safe to repeat: when the book already holds a record of this --type whose
statement has the same value for MEMBER, nothing is sealed and that record is returned with "existing":
true. The check and the seal are one step under the shelf's writer lock, after any append an interrupted
run left half written has been recovered, so a retry after a crash neither doubles nor loses a record.
It is not a transaction: a verb that seals several records is resumed by repeating each with --once.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) (err error) {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			book, _ := c.Flags().GetString("book")
			if err = nodeBookID(p, book); err != nil {
				return err
			}
			recordType, _ := c.Flags().GetString("type")
			if recordType == "" {
				return cli.InputError("--type is required: the node's name for this kind of record")
			}
			path, _ := c.Flags().GetString("statement")
			if path == "" {
				return cli.InputError("--statement is required: a JSON object file, or - for standard input")
			}
			raw, err := readNodeInput(c, path)
			if err != nil {
				return err
			}
			var statement map[string]json.RawMessage
			if err = json.Unmarshal(raw, &statement); err != nil || statement == nil {
				return cli.InputError("--statement must be a JSON object")
			}
			once, _ := c.Flags().GetString("once")
			var onceValue json.RawMessage
			if once != "" {
				if onceValue = statement[once]; onceValue == nil {
					return cli.InputError("--once " + once + ": the statement has no member of that name")
				}
			}
			cites, _ := c.Flags().GetStringArray("cite")
			subject, _ := c.Flags().GetString("subject-ref")
			var payloads [][]byte
			payloadPaths, _ := c.Flags().GetStringArray("payload")
			for _, pp := range payloadPaths {
				octets, e := cli.ReadInput(pp)
				if e != nil {
					return e
				}
				payloads = append(payloads, octets)
			}
			entry := evidencebook.Entry{RecordType: recordType, EpistemicType: evidencebook.ProducerClaim, SubjectRef: subject, Statement: raw, Payloads: payloads}
			for _, target := range cites {
				entry.Links = append(entry.Links, evidencebook.Link{Type: evidencebook.Cites, Target: target})
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
			if once != "" {
				existing, e := nodeRecordsOf(ctx, b, recordType)
				if e != nil {
					return e
				}
				for _, r := range existing {
					if sameMember(r.Statement, once, onceValue) {
						return cli.Output(c, map[string]any{"existing": true, "record": r})
					}
				}
			}
			record, err := b.Append(ctx, entry)
			if err != nil {
				return err
			}
			return cli.Output(c, map[string]any{"existing": false, "record": nodeRecordOf(record)})
		},
	}
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Flags().String("type", "", "The node's own name for this record type (required)")
	cmd.Flags().String("statement", "", "The record's statement: a JSON object file, or - for standard input (required)")
	cmd.Flags().String("subject-ref", "", "What the record is about")
	cmd.Flags().StringArray("cite", nil, "A record id this record cites (repeatable)")
	cmd.Flags().StringArray("payload", nil, "A file whose octets the record keeps as a payload (repeatable)")
	cmd.Flags().String("once", "", "Seal nothing when the book already holds a record of this --type with the same value for this statement member")
	return cmd
}

func dealNodeRecordsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "records",
		Short: "List a node book's records, oldest first, with their statements (seals nothing)",
		Args:  cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) (err error) {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			book, _ := c.Flags().GetString("book")
			if err = nodeBookID(p, book); err != nil {
				return err
			}
			recordType, _ := c.Flags().GetString("type")
			ctx := c.Context()
			s, err := openDealSession(ctx, p)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, s.close()) }()
			if books, e := s.shelf.Books(ctx); e != nil {
				return e
			} else if !contains(books, book) {
				return cli.Output(c, map[string]any{"book": book, "records": []nodeRecord{}})
			}
			b, err := s.shelf.Open(ctx, book)
			if err != nil {
				return err
			}
			records, err := nodeRecordsOf(ctx, b, recordType)
			if err != nil {
				return err
			}
			return cli.Output(c, map[string]any{"book": book, "records": records})
		},
	}
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Flags().String("type", "", "Only records of this type")
	return cmd
}

// nodeRecordsOf is the book's records of recordType (all when empty), oldest first.
func nodeRecordsOf(ctx context.Context, b *evidencebook.Book, recordType string) ([]nodeRecord, error) {
	found, err := b.Query(ctx, evidencebook.Filter{RecordType: recordType})
	if err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Seq < found[j].Seq })
	out := make([]nodeRecord, 0, len(found))
	for _, r := range found {
		out = append(out, nodeRecordOf(r))
	}
	return out, nil
}

// sameMember reports whether statement's member name holds the same JSON
// value as want, compared as values so key order and spacing do not matter.
func sameMember(statement json.RawMessage, name string, want json.RawMessage) bool {
	var members map[string]json.RawMessage
	if json.Unmarshal(statement, &members) != nil {
		return false
	}
	have, ok := members[name]
	if !ok {
		return false
	}
	var a, b any
	if json.Unmarshal(have, &a) != nil || json.Unmarshal(want, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// readNodeInput reads a file, or standard input for "-", within the input limit.
func readNodeInput(c *cobra.Command, path string) ([]byte, error) {
	if path != "-" {
		return cli.ReadInput(path)
	}
	raw, err := io.ReadAll(io.LimitReader(c.InOrStdin(), cli.MaxInput+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > cli.MaxInput {
		return nil, cli.InputError("the input exceeds the input size limit")
	}
	return raw, nil
}
