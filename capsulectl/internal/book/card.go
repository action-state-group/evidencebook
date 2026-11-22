package book

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// `capsulectl book card register|check` register a Standard History card on a
// transparency service the holder names, and check card_registered over the
// statements kept beside a card (agent-action-capsule
// spec/standard-history-v0.md §13). Registration is opt-in and has no default:
// without --service nothing is posted. What a pass shows is existence at a
// time and nothing else (§13.3): never that the counts are right, never
// anchor_inclusion (NOT_CHECKED for a card), never that the holder registered
// no other card. The log is append-only, so a registration cannot be
// withdrawn, or the link it makes between the holder key and the anchor
// (§13.5): the Long text says so before the post.

const (
	// cardServiceMaxResponse bounds what a service may send back.
	cardServiceMaxResponse = 1 << 20
	// cardServiceTimeout ends the whole registration, connect to last byte.
	cardServiceTimeout = 60 * time.Second
)

// cardService is the service file: the one service the holder chose, and the
// relying party's transparency policy for it (its keys), never read from the
// card or the service's own answer.
type cardService struct {
	Name     string   `json:"name"`
	Operator string   `json:"operator"`
	URL      string   `json:"url"`
	Keys     []string `json:"keys"`
}

func (s cardService) policy() aacwitness.AnchorWitnessPolicy {
	return aacwitness.AnchorWitnessPolicy{Witnesses: []aacwitness.AnchorWitness{{Name: s.Name, Keys: s.Keys}}}
}

// loadCardService reads a service file; every member is required, so nothing
// is assumed. An empty path is the opt-in refusal.
func loadCardService(path string) (cardService, error) {
	var s cardService
	if path == "" {
		return s, cli.InputError("no transparency service is named: registration is opt-in and has no default; pass --service FILE naming the one you chose")
	}
	raw, err := cli.ReadInput(path)
	if err != nil {
		return s, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, cli.InputError("service file " + path + ": " + err.Error())
	}
	if s.Name == "" || s.Operator == "" || s.URL == "" {
		return s, cli.InputError("the service file needs name, operator and url, each a non-empty string")
	}
	if len(s.Keys) == 0 {
		return s, cli.InputError("the service file needs keys: the hex DER public keys its Receipts verify under")
	}
	for _, k := range s.Keys {
		if !policyKeyShape(k) {
			return s, cli.InputError("the service file's keys must each be lowercase hex of a DER SubjectPublicKeyInfo for an Ed25519 (44 bytes) or P-256 (91 bytes) key: a key of any other shape is ignored by the check, and every Receipt would then fail after the statement was posted for good")
		}
	}
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return s, cli.InputError("the service url is not an http or https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return s, cli.InputError("the service url holds no credentials, query or fragment: the client adds the route itself")
	}
	s.URL = strings.TrimRight(s.URL, "/")
	return s, nil
}

var (
	ed25519SPKIPrefix = []byte{0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00}
	p256SPKIPrefix    = []byte{0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01, 0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00}
)

// policyKeyShape is the two shapes a transparency policy lists a key in
// (agent-action-capsule go witness, AnchorWitnessPolicy): lowercase hex of an
// Ed25519 or a P-256 SubjectPublicKeyInfo. The library ignores any other
// entry, so a mistyped key would reach the service and fail only afterwards.
func policyKeyShape(k string) bool {
	if k != strings.ToLower(k) {
		return false
	}
	der, err := hex.DecodeString(k)
	if err != nil {
		return false
	}
	return (len(der) == 44 && bytes.HasPrefix(der, ed25519SPKIPrefix)) || (len(der) == 91 && bytes.HasPrefix(der, p256SPKIPrefix))
}

// anchorRegistrar is an evidencebook.CardStatementRegistrar for a
// capsule-anchor service: POST /transparency/register-statement with
// {"signed_statement_b64"}, answered {"receipt_b64"}. A redirect is not
// followed (the one URL the host named); the answer is bounded in size and
// time; the Receipt it returns is checked by evidencebook.RegisterCard under
// the policy before it is kept, whatever the service says about its entry.
type anchorRegistrar struct {
	service cardService
	client  *http.Client
	// posted is set once the request is about to leave: from then on the
	// service may have logged the statement for good, whatever follows.
	posted bool
}

func newAnchorRegistrar(s cardService) *anchorRegistrar {
	return &anchorRegistrar{service: s, client: &http.Client{
		Timeout:       cardServiceTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (r *anchorRegistrar) RegisterStatement(ctx context.Context, statement []byte) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"signed_statement_b64": base64.StdEncoding.EncodeToString(statement)})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cardServiceTimeout)
	defer cancel()
	base, err := url.Parse(r.service.URL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.JoinPath("transparency", "register-statement").String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	r.posted = true
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.service.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", r.service.URL, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, cardServiceMaxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.service.URL, err)
	}
	if len(raw) > cardServiceMaxResponse {
		return nil, fmt.Errorf("%s: the answer is over %d bytes", r.service.URL, cardServiceMaxResponse)
	}
	var answer struct {
		Receipt string `json:"receipt_b64"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Receipt == "" {
		return nil, fmt.Errorf("%s: the answer holds no receipt_b64", r.service.URL)
	}
	receipt, err := base64.StdEncoding.DecodeString(answer.Receipt)
	if err != nil {
		return nil, fmt.Errorf("%s: receipt_b64 is not base64: %w", r.service.URL, err)
	}
	return receipt, nil
}

// cardStatementFile is the file `card register` writes and `card check`
// reads: the statement with its Receipts, hex, as the host keeps it beside the
// card (§13.3).
type cardStatementFile struct {
	Statement string `json:"statement"`
}

// registerError words a failed registration by what is true of the service:
// before the post nothing left this machine; after it the service may have
// logged the statement, and the log is append-only (§13.5), so a bad Receipt
// or a timeout leaves no statement file and the card unchanged, but not a
// service that has seen nothing.
func registerError(err error, posted bool) error {
	if !posted {
		if errors.Is(err, evidencebook.ErrInvalid) {
			return cli.InputError("nothing was posted: " + err.Error())
		}
		return fmt.Errorf("nothing was posted: %w", err)
	}
	return fmt.Errorf("the service was sent the statement and may have logged it (the log is append-only, so it cannot be withdrawn); no statement file was written and the card is unchanged: %w", err)
}

func cardCommands() *cobra.Command {
	card := &cobra.Command{Use: "card", Short: "Register a History card on a transparency service you name, and check card_registered", Args: cli.NoArgs}
	card.AddCommand(cardRegisterCommand(), cardCheckCommand())
	return card
}

// cardInputs reads the flags `register` and `check` share.
func cardInputs(c *cobra.Command) (card []byte, holder, anchor string, err error) {
	path, _ := c.Flags().GetString("card")
	holder, _ = c.Flags().GetString("holder")
	anchor, _ = c.Flags().GetString("anchor")
	if path == "" || holder == "" || anchor == "" {
		return nil, "", "", cli.InputError("--card, --holder and --anchor are required")
	}
	if card, err = cli.ReadInput(path); err != nil {
		return nil, "", "", err
	}
	return card, holder, anchor, nil
}

func cardFlags(cmd *cobra.Command) {
	cmd.Flags().String("card", "", "The card's octets (the History card file) (required)")
	cmd.Flags().String("holder", "", "The holder key: the 64-hex Ed25519 key id the card's answer was signed under (required)")
	cmd.Flags().String("anchor", "", "The digest of the anchor the card was derived under (required)")
	cmd.Flags().String("service", "", "The service file: name, operator, url (a service answering POST /transparency/register-statement) and the keys its Receipts verify under. No default")
}

func cardRegisterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register the card on the service you name; keep the statement and its Receipt beside the card (signs; posts to the service)",
		Long: `Register a History card on the transparency service --service names, as the holder.
There is no default service: without --service nothing is posted. The client speaks one
protocol, capsule-anchor's statement registration (POST /transparency/register-statement,
answered with receipt_b64); a service must answer it.

The post is permanent. The log is append-only, so a registration, and the link it makes
between the holder key and the anchor, can never be withdrawn. The service and anyone who
reads its log learn which holder key registered a card for which anchor, and when. A
statement carries the card's digest, not the card, but a digest does not hide a small card:
a party that holds the anchor can test guesses of its counts against it. A service the holder
operates confers nothing. Register only on a service whose published policy you accept; not
registering is no finding against a card or its holder.

A pass shows that this exact card existed by the Receipt's registration time, when it carries
one (without one, only that the service logged the statement, not by when): not that its counts
are right, not anchor_inclusion (it stays NOT_CHECKED), not that this is the holder's only card.
A failed registration changes nothing about the card; if the post was made, the service may have
logged the statement anyway, and the error says so. The profile's signing key must be the holder key.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			service, err := loadCardService(mustString(c, "service"))
			if err != nil {
				return err
			}
			cardOctets, holder, anchor, err := cardInputs(c)
			if err != nil {
				return err
			}
			private, err := cli.PrivateKey(p.Signing)
			if err != nil {
				return err
			}
			signer, err := evidencebook.NewEd25519Signer(ed25519.PrivateKey(private))
			if err != nil {
				return err
			}
			// The destination is claimed before anything is posted: a registration
			// is permanent, and its statement must have somewhere to go.
			out, _ := c.Flags().GetString("out")
			var dest *statementDest
			if out != "" {
				if dest, err = claimStatementDest(out); err != nil {
					return err
				}
				defer dest.release()
			}
			env := evidencebook.Envelope{Responder: holder, Anchor: evidencebook.Anchor{Digest: anchor}}
			registrar := newAnchorRegistrar(service)
			statement, err := evidencebook.RegisterCard(c.Context(), signer, cardOctets, env, registrar, service.policy())
			if err != nil {
				return registerError(err, registrar.posted)
			}
			file, err := json.Marshal(cardStatementFile{Statement: hex.EncodeToString(statement)})
			if err != nil {
				return err
			}
			saved := func(err error) error {
				return fmt.Errorf("the card is registered, but its statement was not saved: %w; the statement, to keep beside the card: %s", err, file)
			}
			if dest != nil {
				if err := dest.commit(file); err != nil {
					return saved(err)
				}
				return nil
			}
			if _, err := c.OutOrStdout().Write(append(file, '\n')); err != nil {
				return saved(err)
			}
			return nil
		},
	}
	cardFlags(cmd)
	cmd.Flags().String("out", "", "Where to write the statement file: a new file, never an existing one (default: standard output)")
	return cmd
}

func cardCheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check card_registered over the statement files kept beside a card, under the service's keys (seals nothing)",
		Long: `Check card_registered (SH §13.3) over the statement files kept beside a card, under the holder
key and anchor and the policy the --service file's keys state, never one read from a statement.
--service is your own service file, listing only services you hold independent of the holder,
not one the holder gave you: the policy is the relying party's.

NOT_PRESENT when there is no statement. A pass says the holder key registered this exact card
with the service named, at the registration time: a Receipt that carries no time shows only that
the statement was logged, not by when. It does not say the counts are right, it is not
anchor_inclusion (it stays NOT_CHECKED), and it does not say this is the holder's only card. A
FAIL is no finding against the card or the holder: anyone can place octets beside a card.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			service, err := loadCardService(mustString(c, "service"))
			if err != nil {
				return err
			}
			cardOctets, holder, anchor, err := cardInputs(c)
			if err != nil {
				return err
			}
			paths, _ := c.Flags().GetStringArray("statement")
			var statements [][]byte
			for _, path := range paths {
				raw, err := cli.ReadInput(path)
				if err != nil {
					return err
				}
				var file cardStatementFile
				if err := json.Unmarshal(raw, &file); err != nil {
					return cli.InputError("statement file " + filepath.Base(path) + ": " + err.Error())
				}
				statement, err := hex.DecodeString(file.Statement)
				if err != nil || len(statement) == 0 {
					return cli.InputError("statement file " + filepath.Base(path) + ": the statement is not hex")
				}
				statements = append(statements, statement)
			}
			env := evidencebook.Envelope{Responder: holder, Anchor: evidencebook.Anchor{Digest: anchor}}
			return cli.Output(c, evidencebook.CheckCardRegistered(cardOctets, env, statements, service.policy()))
		},
	}
	cardFlags(cmd)
	cmd.Flags().StringArray("statement", nil, "A statement file `card register` wrote (repeatable)")
	return cmd
}

func mustString(c *cobra.Command, name string) string {
	v, _ := c.Flags().GetString(name)
	return v
}

// statementDest is the new file the statement is written to: claimed before
// the post (the directory must take a file, and the path must not exist, so
// neither the card nor an earlier statement is replaced), filled by a hard link
// that fails if the path appeared since, so a reader never sees a half-written
// statement.
type statementDest struct {
	path string
	tmp  *os.File
}

func claimStatementDest(path string) (*statementDest, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, cli.InputError("--out " + path + " exists: the statement is written to a new file, never over the card or an earlier statement")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".card-*")
	if err != nil {
		return nil, cli.InputError("--out cannot be written, and nothing was posted: " + err.Error())
	}
	return &statementDest{path: path, tmp: tmp}, nil
}

func (d *statementDest) commit(data []byte) error {
	if _, err := d.tmp.Write(data); err != nil {
		return err
	}
	if err := d.tmp.Close(); err != nil {
		return err
	}
	return os.Link(d.tmp.Name(), d.path)
}

func (d *statementDest) release() {
	_ = d.tmp.Close()
	_ = os.Remove(d.tmp.Name())
}
