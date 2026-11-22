package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-engine/go/referee"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// `deal node referee receive` seals a referee's Result into a node book as
// received (evidencebook#624; the receive of evidencebook's Goose node,
// goose_node.py receive_dispute_result). The check is capsule-engine's Go
// referee package: BindOpenings (each half authentic, each opening recomputing
// its half's action_digest, the halves not signed by one key) then VerifyResult
// (the Result verifies, is signed by the referee both halves' terms name, names
// exactly these halves, and is the document it commits to). This node adds
// what needs its own key and the transport: it must have sealed one of the
// halves (not_a_party), and the document must be the referee's JCS bytes
// (result_mismatch). A Result that fails is refused and the refusal sealed with
// its code, never silence.
//
// The node's own record type names are its vocabulary, given as flags.

// refereeTypes are the node's names for the three record types the receive writes.
type refereeTypes struct{ Received, Refused, Half string }

type refereeOutcome struct {
	Existing bool
	Record   nodeRecord
	Refusal  *referee.Error // set when the Result was refused (the refusal is sealed)
	Refused  nodeRecord
}

// notAResult is the refusal for input that cannot be read as the documents it should be.
func notAResult(format string, args ...any) *referee.Error {
	return &referee.Error{Code: referee.CodeNotAResult, Detail: fmt.Sprintf(format, args...)}
}

type refereeParty struct {
	Half   json.RawMessage
	Action string
	Terms  json.RawMessage
}

// readParties reads the delivery's parties, loosely: a shape that is not both
// parties' half, action and terms is a delivery that is no Result
// (not_a_result), which is sealed, as the Python node seals it.
func readParties(raw []byte) (map[string]refereeParty, *referee.Error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return nil, notAResult("the delivery is not a Result with both parties' openings")
	}
	parties := map[string]refereeParty{}
	for _, role := range []string{"buyer", "seller"} {
		var members map[string]json.RawMessage
		if err := json.Unmarshal(top[role], &members); err != nil || members == nil {
			return nil, notAResult("the delivery is not a Result with both parties' openings")
		}
		half, terms, action := members["half"], members["terms"], members["action"]
		var actionText *string // a pointer: JSON null decodes to nil, and null is not text
		if len(half) == 0 || len(terms) == 0 || len(action) == 0 || json.Unmarshal(action, &actionText) != nil || actionText == nil {
			return nil, notAResult("the %s's half, action and terms are not all present", role)
		}
		parties[role] = refereeParty{Half: half, Action: *actionText, Terms: terms}
	}
	return parties, nil
}

type halfInfo struct{ capsuleID, keyID string }

// readHalf reads the ids of a half that BindOpenings has authenticated.
func readHalf(raw json.RawMessage) (halfInfo, *referee.Error) {
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		return halfInfo{}, notAResult("a half cannot be read: %v", err)
	}
	id, _ := h["capsule_id"].(string)
	key, _ := h["key_id"].(string)
	if id == "" {
		return halfInfo{}, notAResult("a half carries no capsule_id")
	}
	return halfInfo{capsuleID: id, keyID: key}, nil
}

// checkRefereeResult is the whole receive check, in the order the Python node
// makes it (_open_parties, then _check_dispute_result), returning nil or the
// refusal and the judged halves' ids.
func checkRefereeResult(selfKeyID string, capsule json.RawMessage, octets []byte, parties map[string]refereeParty) (*referee.Error, map[string]halfInfo) {
	buyer, seller := parties["buyer"], parties["seller"]
	bindErr := referee.BindOpenings(
		referee.Party{Half: buyer.Half, Action: buyer.Action, Terms: buyer.Terms},
		referee.Party{Half: seller.Half, Action: seller.Action, Terms: seller.Terms})
	var re *referee.Error
	if bindErr != nil {
		if !errors.As(bindErr, &re) {
			return notAResult("the openings cannot be checked: %v", bindErr), nil
		}
		// The Python node names a node that signed neither half before it says the halves share a key.
		if re.Code != referee.CodeHalvesUnpaired {
			return re, nil
		}
	}
	// Both halves are authentic here, so their ids are present.
	infos := map[string]halfInfo{}
	for _, role := range []string{"buyer", "seller"} {
		info, refusal := readHalf(parties[role].Half)
		if refusal != nil {
			return refusal, nil
		}
		infos[role] = info
	}
	if selfKeyID != infos["buyer"].keyID && selfKeyID != infos["seller"].keyID {
		return &referee.Error{Code: "not_a_party", Detail: "this node's key sealed neither half the Result judges"}, nil
	}
	if re != nil {
		return re, nil
	}
	// The octets are the referee's JCS bytes, so every party's book holds one digest.
	want, err := referee.ResultDigest(octets)
	if err != nil {
		return notAResult("the delivery is not a Result document: %v", err), nil
	}
	sum := sha256.Sum256(octets)
	if want != hex.EncodeToString(sum[:]) {
		return &referee.Error{Code: referee.CodeResultMismatch, Detail: "the Result octets are not the referee's JCS bytes"}, nil
	}
	if err := referee.VerifyResult(capsule, octets, referee.Inputs{
		BuyerHalf: buyer.Half, SellerHalf: seller.Half, BuyerTerms: buyer.Terms, SellerTerms: seller.Terms}); err != nil {
		if errors.As(err, &re) {
			return re, nil
		}
		return notAResult("the Result cannot be checked: %v", err), nil
	}
	return nil, infos
}

// receiveRefereeResult runs the check and seals what follows from it into b,
// each record once: a repeat of the same delivery finds its records and seals
// nothing, so a retry after an interruption resumes. rawParties is the
// delivery's parties as the file holds them.
func receiveRefereeResult(ctx context.Context, b *evidencebook.Book, selfKeyID string, types refereeTypes, capsule json.RawMessage, octets []byte, rawParties []byte) (refereeOutcome, error) {
	sum := sha256.Sum256(octets)
	digest := hex.EncodeToString(sum[:])
	parties, refusal := readParties(rawParties)
	var infos map[string]halfInfo
	if refusal == nil {
		refusal, infos = checkRefereeResult(selfKeyID, capsule, octets, parties)
	}
	if refusal != nil {
		record, err := sealOnce(ctx, b, types.Refused, map[string]string{"result_digest": digest, "code": refusal.Code}, digest, nil, nil)
		if err != nil {
			return refereeOutcome{}, err
		}
		return refereeOutcome{Refusal: refusal, Refused: record.Record}, nil
	}
	var held []string
	ids := map[string]string{}
	for _, role := range []string{"buyer", "seller"} {
		octetsOfHalf, err := halfJCS(parties[role].Half)
		if err != nil {
			return refereeOutcome{}, err
		}
		record, err := sealOnce(ctx, b, types.Half, map[string]string{"capsule_id": infos[role].capsuleID},
			infos[role].capsuleID, nil, [][]byte{octetsOfHalf}, "role", role, "key_id", infos[role].keyID)
		if err != nil {
			return refereeOutcome{}, err
		}
		held = append(held, record.Record.RecordID)
		ids[role] = infos[role].capsuleID
	}
	var c map[string]any
	if err := json.Unmarshal(capsule, &c); err != nil {
		return refereeOutcome{}, err
	}
	received, err := sealOnce(ctx, b, types.Received, map[string]string{"result_digest": digest}, digest, held, nil,
		"result_capsule_id", fmt.Sprint(c["capsule_id"]), "referee", fmt.Sprint(c["key_id"]), "buyer_half", ids["buyer"], "seller_half", ids["seller"])
	if err != nil {
		return refereeOutcome{}, err
	}
	return refereeOutcome{Existing: received.Existing, Record: received.Record}, nil
}

// halfJCS is a half's canonical (JCS) bytes.
func halfJCS(raw json.RawMessage) ([]byte, error) {
	var v any
	if err := decodeKeepingNumbers(raw, &v); err != nil {
		return nil, err
	}
	return canonical.JCS(v)
}

type sealed struct {
	Existing bool
	Record   nodeRecord
}

// sealOnce appends a producer-claim record of recordType unless one already
// holds every member of match: a repeat seals nothing and returns the record it
// finds. members are further statement members, as name/value string pairs, written
// with the record.
func sealOnce(ctx context.Context, b *evidencebook.Book, recordType string, match map[string]string, subject string, cites []string, payloads [][]byte, members ...string) (sealed, error) {
	records, err := nodeRecordsOf(ctx, b, recordType)
	if err != nil {
		return sealed{}, err
	}
	for _, r := range records {
		all := true
		for name, value := range match {
			if !sameMember(r.Statement, name, json.RawMessage(quote(value))) {
				all = false
			}
		}
		if all {
			return sealed{Existing: true, Record: r}, nil
		}
	}
	statement := map[string]any{}
	for name, value := range match {
		statement[name] = value
	}
	for i := 0; i+1 < len(members); i += 2 {
		statement[members[i]] = members[i+1]
	}
	raw, err := json.Marshal(statement)
	if err != nil {
		return sealed{}, err
	}
	entry := evidencebook.Entry{RecordType: recordType, EpistemicType: evidencebook.ProducerClaim, SubjectRef: subject, Statement: raw, Payloads: payloads}
	for _, c := range cites {
		entry.Links = append(entry.Links, evidencebook.Link{Type: evidencebook.Cites, Target: c})
	}
	record, err := b.Append(ctx, entry)
	if err != nil {
		return sealed{}, err
	}
	return sealed{Record: nodeRecordOf(record)}, nil
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func dealNodeRefereeCommands() *cobra.Command {
	group := &cobra.Command{Use: "referee", Short: "A referee's Result, received into a node book", Args: cli.NoArgs}
	group.AddCommand(dealNodeRefereeReceiveCommand())
	return group
}

func dealNodeRefereeReceiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "receive",
		Short: "Check a referee's Result against both parties' halves and seal it into a node book as received, or seal why it was refused (signs)",
		Long: `Check a referee's Result and seal it into the node book --book names as received.

--result is a JSON file {"capsule": <the referee's sealed Result Capsule>, "document": <the base64 of the Result
document exactly as the referee wrote it, its JCS bytes>}. --parties is a JSON file
{"buyer": {"half", "action", "terms"}, "seller": {...}}: each party's signed half Capsule and the opening of its
commitment, the action and the terms its action_digest commits to.

The Result is received only when each half verifies and its opening recomputes the half's action_digest, the halves
are signed by two keys and this node's key signed one of them, the Result verifies, is signed by the referee both
parties' terms name, names exactly these two halves, and is the document it commits to. Anything else is refused:
the refusal is sealed with its code (never silence) and the verb exits with it. Receiving is safe to repeat: a
Result already received, and a half already held, seal nothing again. What the check shows is what the Result
and the halves say; it does not show the referee's judgment is right.`,
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
			var types refereeTypes
			types.Received, _ = c.Flags().GetString("received-type")
			types.Refused, _ = c.Flags().GetString("refused-type")
			types.Half, _ = c.Flags().GetString("half-type")
			for _, t := range []string{types.Received, types.Refused, types.Half} {
				if t == "" {
					return cli.InputError("--received-type, --refused-type and --half-type are required: the node's names for the three record types")
				}
				if libraryRecordTypes[t] {
					return cli.InputError("record type " + t + " is one the evidencebook library writes itself; a node's types are its own names")
				}
			}
			if types.Received == types.Refused || types.Received == types.Half || types.Refused == types.Half {
				return cli.InputError("--received-type, --refused-type and --half-type must be three different names: a refusal must never be taken for a receipt")
			}
			resultPath, _ := c.Flags().GetString("result")
			partiesPath, _ := c.Flags().GetString("parties")
			if resultPath == "" || partiesPath == "" {
				return cli.InputError("--result and --parties are required")
			}
			rawResult, err := readNodeInput(c, resultPath)
			if err != nil {
				return err
			}
			var container struct {
				Capsule  json.RawMessage `json:"capsule"`
				Document string          `json:"document"`
			}
			if err = json.Unmarshal(rawResult, &container); err != nil || len(container.Capsule) == 0 || container.Document == "" {
				return cli.InputError("--result must be {\"capsule\": ..., \"document\": <base64 of the Result's exact bytes>}")
			}
			octets, err := base64.StdEncoding.DecodeString(container.Document)
			if err != nil {
				return cli.InputError("--result document is not base64: " + err.Error())
			}
			rawParties, err := cli.ReadInput(partiesPath)
			if err != nil {
				return err
			}
			if !json.Valid(rawParties) {
				return cli.InputError("--parties is not JSON")
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
			selfKeyID := hex.EncodeToString(s.key.Public().(ed25519.PublicKey))
			outcome, err := receiveRefereeResult(ctx, b, selfKeyID, types, container.Capsule, octets, rawParties)
			if err != nil {
				return err
			}
			if outcome.Refusal != nil {
				return cli.InputError(fmt.Sprintf("the Result is refused: %s; the refusal is sealed as record %s", outcome.Refusal, outcome.Refused.RecordID))
			}
			return cli.Output(c, map[string]any{"received": true, "existing": outcome.Existing, "record": outcome.Record})
		},
	}
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Flags().String("result", "", "The delivery: {\"capsule\", \"document\" (base64)} (required)")
	cmd.Flags().String("parties", "", "Both parties' halves and openings: {\"buyer\": {half, action, terms}, \"seller\": {...}} (required)")
	cmd.Flags().String("received-type", "", "The node's name for the record of a received Result (required)")
	cmd.Flags().String("refused-type", "", "The node's name for the record of a refused Result (required)")
	cmd.Flags().String("half-type", "", "The node's name for the record that holds a judged half (required)")
	return cmd
}
