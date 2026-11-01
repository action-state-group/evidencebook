package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/action-state-group/capsule-cli/cli"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/capsule-emit/go/artifact"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/spf13/cobra"
)

// The History card (standard_history/1, agent-action-capsule's
// spec/standard-history-v0.md, cited as SH) over this profile's shelf
// (evidencebook-deal#85). The library derives the card and maps a deal's sealed
// records to an SH §5.2 entry (evidencebook.DeriveStandardHistory,
// dealprofile.StandardHistoryDeal, x-deal-v0 PROFILE.md §11); this file reads
// the shelf under a cadence tick, keeps the holder's declaration and policy,
// and writes the card's bundle.

// Settings sit beside the deal database: the holder's declaration, and the
// standard-history policy, which is never part of the share policy.
const (
	shSettingsFile = "standard-history.json"
	shPolicyFile   = "standard-history-policy.json"
	// shDefaultPolicy is the policy a holder who sets none has: the last 3
	// months (9b frame 4, F4.8: "default 3").
	shDefaultPolicy = `{"months":3}`
	// shLogPrefix names the log a card's Capsule is sealed on: one log per
	// card, as a History's.
	shLogPrefix  = "standard-history/"
	shActionID   = "capsulectl-deal-standard-history"
	shExtension  = "x-standard-history/v0"
	shSetupNotes = "This node answers History card requests from any requester you name to `deal respond`: the card counts your deals and carries no deal's content. Turn that off with `deal standard-history declare --holder-kind node --no-answer`."
)

// shSettings is the holder's declaration. HolderKind is explicit and set
// once: a profile that never declared one has no card, never a default.
// Answer is the holder's own choice of whether `deal respond` answers the
// card; unset, a node answers (Steven u56) and a person does not.
//
// ChangedAt and PolicyChangedAt are when the declaration and the policy last
// changed (the deal clock). SH §3 reads both at the anchor, so a card is only
// built as of a tick cut after the latest change: an older anchor would carry
// today's policy, not the one then in force.
type shSettings struct {
	HolderKind      string `json:"holder_kind"`
	Answer          *bool  `json:"answer,omitempty"`
	ChangedAt       string `json:"changed_at,omitempty"`
	PolicyChangedAt string `json:"policy_changed_at,omitempty"`
}

// changedAfter is whether a declaration or policy change is not before t.
func (s shSettings) changedAfter(t time.Time) bool {
	for _, at := range []string{s.ChangedAt, s.PolicyChangedAt} {
		if c, err := time.Parse(time.RFC3339Nano, at); err == nil && !c.Before(t) {
			return true
		}
	}
	return false
}

// answers is whether `deal respond` answers a card request.
func (s shSettings) answers() bool {
	if s.Answer != nil {
		return *s.Answer
	}
	return s.HolderKind == "node"
}

// errNoHolderKind is the refusal of a card with no declaration.
func errNoHolderKind() error {
	return cli.InputError("no holder kind is declared, so there is no History card: run `deal standard-history declare --holder-kind person|node`")
}

func shDir(p cli.Profile) (string, error) {
	db, err := filepath.Abs(p.Connection.Database)
	if err != nil {
		return "", err
	}
	return filepath.Dir(db), nil
}

// readSHSettings reads the declaration; ok is false before the first
// `declare`.
func readSHSettings(p cli.Profile) (s shSettings, ok bool, err error) {
	dir, err := shDir(p)
	if err != nil {
		return s, false, err
	}
	raw, err := readSetting(filepath.Join(dir, shSettingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if err = cli.DecodeJSON(raw, &s); err != nil || (s.HolderKind != "person" && s.HolderKind != "node") {
		return shSettings{}, false, cli.InputError("the " + shSettingsFile + " beside the deal store is not a declaration (holder_kind person or node): declare again")
	}
	return s, true, nil
}

func writeSHSettings(p cli.Profile, s shSettings) error {
	dir, err := shDir(p)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return putSetting(filepath.Join(dir, shSettingsFile), raw)
}

// readSHPolicy reads the policy file's octets as held, and its window. The
// card's policy digest is the SHA-256 of these octets (SH §5.1), so a
// holder who never set one has the default octets, not a missing policy.
func readSHPolicy(p cli.Profile) (standardhistory.Policy, error) {
	dir, err := shDir(p)
	if err != nil {
		return standardhistory.Policy{}, err
	}
	raw, err := readSetting(filepath.Join(dir, shPolicyFile))
	if errors.Is(err, os.ErrNotExist) {
		raw = []byte(shDefaultPolicy)
	} else if err != nil {
		return standardhistory.Policy{}, err
	}
	var doc struct {
		Months *int  `json:"months"`
		All    *bool `json:"all"`
	}
	if err = cli.DecodeJSON(raw, &doc); err != nil {
		return standardhistory.Policy{}, cli.InputError("the " + shPolicyFile + " beside the deal store is not JSON: " + err.Error())
	}
	var window standardhistory.Window
	switch {
	case doc.Months != nil && doc.All == nil && *doc.Months > 0:
		window.Months = *doc.Months
	case doc.All != nil && *doc.All && doc.Months == nil:
		window.All = true
	default:
		return standardhistory.Policy{}, cli.InputError(`the ` + shPolicyFile + ` beside the deal store must be {"months": N} with N a positive whole number, or {"all": true}`)
	}
	sum := sha256.Sum256(raw)
	return standardhistory.Policy{Digest: hex.EncodeToString(sum[:]), Window: window}, nil
}

// shPolicyOctets is the policy file's octets for a window.
func shPolicyOctets(months int, all bool) ([]byte, error) {
	doc := map[string]any{"months": months}
	if all {
		doc = map[string]any{"all": true}
	}
	return json.Marshal(doc)
}

// currentSHPolicyOctets are the octets in force: the file's, or the default's.
func currentSHPolicyOctets(p cli.Profile) ([]byte, error) {
	dir, err := shDir(p)
	if err != nil {
		return nil, err
	}
	raw, err := readSetting(filepath.Join(dir, shPolicyFile))
	if errors.Is(err, os.ErrNotExist) {
		return []byte(shDefaultPolicy), nil
	}
	return raw, err
}

func writeSHPolicy(p cli.Profile, months int, all bool) error {
	dir, err := shDir(p)
	if err != nil {
		return err
	}
	raw, err := shPolicyOctets(months, all)
	if err != nil {
		return err
	}
	return putSetting(filepath.Join(dir, shPolicyFile), raw)
}

func dealStandardHistoryCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "standard-history", Short: "The History card: a count of your deals as of a checkpoint, that carries no deal's content (declare who holds it, set its window, write its bundle)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	cmd.AddCommand(shDeclareCommand(), shPolicyCommand(), shCardCommand())
	return cmd
}

func shDeclareCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "declare", Short: "Declare once whether this profile is a person's or a node's, and whether `deal respond` answers a History card request", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		kind, _ := c.Flags().GetString("holder-kind")
		answer, _ := c.Flags().GetBool("answer")
		noAnswer, _ := c.Flags().GetBool("no-answer")
		if kind != "person" && kind != "node" {
			return cli.InputError("--holder-kind must be person or node: it is declared, never defaulted")
		}
		if answer && noAnswer {
			return cli.InputError("--answer and --no-answer are opposites")
		}
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		s := shSettings{HolderKind: kind}
		if old, declared, err := readSHSettings(p); err != nil {
			return err
		} else if declared {
			if old.HolderKind != kind {
				return cli.InputError("this profile declared itself a " + old.HolderKind + ": the holder kind is set once, and a card's holder_kind does not change between anchors")
			}
			// Declaring again confirms; it never turns answering back on.
			s = old
		}
		// Only the first declaration changes what a card says (its holder kind);
		// whether `deal respond` answers is not in the card.
		if s.ChangedAt == "" {
			s.ChangedAt = cli.DealClock().UTC().Format(time.RFC3339Nano)
		}
		switch {
		case answer:
			s.Answer = new(bool)
			*s.Answer = true
		case noAnswer:
			s.Answer = new(bool)
		}
		if err = writeSHSettings(p, s); err != nil {
			return err
		}
		result := map[string]any{"holder_kind": kind, "answers": s.answers()}
		if kind == "node" && s.answers() {
			result["notice"] = shSetupNotes
		}
		return cli.Output(c, result)
	}}
	cmd.Flags().String("holder-kind", "", "person or node (required)")
	cmd.Flags().Bool("answer", false, "Answer History card requests (a person's default is not to)")
	cmd.Flags().Bool("no-answer", false, "Do not answer History card requests (a node's default is to)")
	return cmd
}

func shPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "policy", Short: "Set the History card's window: the last N months (default 3) or all deals; with no flag, show it", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		months, _ := c.Flags().GetInt("months")
		all, _ := c.Flags().GetBool("all")
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		switch {
		case months != 0 && all:
			return cli.InputError("--months and --all are opposites")
		case months < 0:
			return cli.InputError("--months is a positive whole number")
		case months > 0 || all:
			raw, err := shPolicyOctets(months, all)
			if err != nil {
				return err
			}
			// The same policy again changes nothing, so it makes no anchor stale.
			if current, err := currentSHPolicyOctets(p); err != nil {
				return err
			} else if string(current) == string(raw) {
				break
			}
			// The change time is written before the policy: a failure between
			// the two leaves an anchor refused as stale, never a card that
			// carries a policy newer than its anchor. A policy set before
			// declaring is stamped by the declaration.
			if old, declared, err := readSHSettings(p); err != nil {
				return err
			} else if declared {
				old.PolicyChangedAt = cli.DealClock().UTC().Format(time.RFC3339Nano)
				if err = writeSHSettings(p, old); err != nil {
					return err
				}
			}
			if err = writeSHPolicy(p, months, all); err != nil {
				return err
			}
		}
		policy, err := readSHPolicy(p)
		if err != nil {
			return err
		}
		return cli.Output(c, map[string]any{"window": policy.Window, "policy_digest": policy.Digest})
	}}
	cmd.Flags().Int("months", 0, "Count deals from the last N months")
	cmd.Flags().Bool("all", false, "Count every deal (only when you choose it)")
	return cmd
}

func shCardCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "card", Short: "Write the History card as a bundle `verify --bundle` checks and the History page draws, as of the newest checkpoint of the profile's cadence", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		out, _ := c.Flags().GetString("out")
		shared, _ := c.Flags().GetBool("shared")
		if out == "" {
			return cli.InputError("--out is required")
		}
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		s, err := openDealSession(c.Context(), p)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.close()) }()
		b, err := s.standardHistoryBundle(c.Context(), shared)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			return err
		}
		// Decoded as the verifier decodes the file, and checked before it is written.
		value, err := cli.DecodeBundleJSON(encoded)
		if err != nil {
			return err
		}
		if err = cli.VerifyProducedBundle(value, true); err != nil {
			return err
		}
		if err = cli.AtomicFile(out, encoded, false); err != nil {
			return err
		}
		return cli.Output(c, map[string]any{"out": out, "shared": shared})
	}}
	cmd.Flags().String("out", "", "New file for the bundle")
	cmd.Flags().Bool("shared", false, "The copy to send: the card, its policy and its anchor, without the deal entries it counts (your own copy has them)")
	return cmd
}

// standardHistoryCard is the card as of the newest finished tick of the
// profile's cadence, in its JCS octets, with the block a receiver holds
// beside it.
type standardHistoryCard struct {
	Card   []byte
	Policy standardhistory.Policy
	AsOf   standardhistory.AsOf
	Deals  []standardhistory.Deal
}

// errNoTick is a card with no checkpoint to be as of.
var errNoTick = errors.New("no finished cadence tick")

// errStaleAnchor is a card as of a tick cut before the declaration or the
// policy last changed.
var errStaleAnchor = errors.New("the declaration or policy changed after that checkpoint (run `deal tick`, then ask again)")

// errLegacyDeal is a deal still on the old SQLite index, whose records the
// card does not read: it is refused, never counted as no deal.
var errLegacyDeal = fmt.Errorf("%w: a deal kept on the old index cannot be counted by the card", cli.ErrConflict)

// standardHistory derives the card as of tick n, or the newest finished tick when n is 0. The anchor is the profile's cadence
// checkpoint, never one deal book's: it commits every book's checkpoint
// and is the one a witness sees. A deal's records, and the Results on its
// results book, are read only up to the extent that anchor commits for
// their book, so the card is as of the anchor and the same whenever it is
// derived (x-deal-v0 PROFILE.md §11).
func (s *dealSession) standardHistory(ctx context.Context, tick uint64) (standardHistoryCard, error) {
	settings, ok, err := readSHSettings(s.p)
	if err != nil {
		return standardHistoryCard{}, err
	}
	if !ok {
		return standardHistoryCard{}, errNoHolderKind()
	}
	policy, err := readSHPolicy(s.p)
	if err != nil {
		return standardHistoryCard{}, err
	}
	cadence, _, err := s.cadenceOf(ctx)
	if err != nil {
		return standardHistoryCard{}, err
	}
	anchor, err := cadence.StandardHistoryAnchor(ctx, tick)
	if errors.Is(err, evidencebook.ErrNotFound) {
		return standardHistoryCard{}, fmt.Errorf("%w (run `deal tick` first)", errNoTick)
	}
	if err != nil {
		return standardHistoryCard{}, err
	}
	if settings.changedAfter(anchor.Tick.At) {
		return standardHistoryCard{}, errStaleAnchor
	}
	x := s.books()
	var deals []standardhistory.Deal
	for logID, extent := range anchor.Extents {
		dealID, ok := dealOfBook(logID)
		if !ok || !cli.DealIDPattern.MatchString(dealID) {
			continue
		}
		in, err := x.historyInput(ctx, s.keys, dealID, extent, anchor.Extents[dealResultsLogID(dealID)])
		if err != nil {
			return standardHistoryCard{}, err
		}
		deal, isDeal, err := dealprofile.StandardHistoryDeal(in)
		if err != nil {
			return standardHistoryCard{}, err
		}
		if isDeal {
			deals = append(deals, deal)
		}
	}
	// The extents are a map: the deals are in deal id order, so the own copy
	// is the same bytes whenever it is made.
	slices.SortFunc(deals, func(a, b standardhistory.Deal) int { return strings.Compare(a.Deal, b.Deal) })
	card, err := evidencebook.DeriveStandardHistory(settings.HolderKind, anchor.AsOf, policy, deals)
	if err != nil {
		return standardHistoryCard{}, err
	}
	return standardHistoryCard{Card: card, Policy: policy, AsOf: anchor.AsOf, Deals: deals}, nil
}

// historyInput is one deal's sealed records through the extent its book's
// checkpoint under the anchor commits, and its Results through the
// extent of its results book.
func (x shelfDealIndex) historyInput(ctx context.Context, keys []ed25519.PublicKey, dealID string, extent, resultsExtent uint64) (dealprofile.HistoryInput, error) {
	var in dealprofile.HistoryInput
	if legacy, err := x.isLegacy(ctx, dealID); err != nil {
		return in, err
	} else if legacy {
		return in, fmt.Errorf("deal %s: %w", dealID, errLegacyDeal)
	}
	records, err := x.records(ctx, dealID)
	if err != nil {
		return in, err
	}
	for _, r := range records {
		if r.Seq > extent {
			continue
		}
		payload, verified, err := x.sealedPayload(ctx, keys, r)
		if err != nil {
			return in, err
		}
		if !verified {
			return in, fmt.Errorf("%w: record %s of %s does not verify under the profile's keys", cli.ErrConflict, r.RecordID, dealID)
		}
		at, err := time.Parse(time.RFC3339Nano, r.Header.CommittedAt)
		if err != nil {
			return in, fmt.Errorf("%w: record %s has no commit time", cli.ErrConflict, r.RecordID)
		}
		in.Records = append(in.Records, dealprofile.HistoryRecord{Payload: payload, CommittedAt: at})
	}
	if resultsExtent == 0 {
		return in, nil
	}
	read, err := x.shelf.Records(ctx, dealResultsLogID(dealID))
	if err != nil {
		return in, err
	}
	for _, r := range read.Records {
		if r.Seq > resultsExtent || r.Header.RecordType != evidencebook.RecordTypeEvidenceResult {
			continue
		}
		payload, verified, err := x.sealedPayload(ctx, keys, r)
		if err != nil {
			return in, err
		}
		doc, err := termsPayloadResult(payload)
		if err != nil {
			// A Result the card cannot read counts as none.
			in.Results = append(in.Results, dealprofile.HistoryResult{})
			continue
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return in, err
		}
		in.Results = append(in.Results, dealprofile.HistoryResult{Result: raw, Verified: verified})
	}
	return in, nil
}

// sealedPayload is a record's payload from its held Capsule, and whether
// the Capsule verifies under the profile's keys with its payload bound.
func (x shelfDealIndex) sealedPayload(ctx context.Context, keys []ed25519.PublicKey, r evidencebook.ShelfRecord) (payload []byte, verified bool, err error) {
	held, err := x.held(ctx, r)
	if err != nil {
		return nil, false, err
	}
	return payloadOf(held, keys)
}

// standardHistoryBundle seals the card as a Capsule on a log of its own,
// cuts that log's checkpoint, and assembles the Evidence Bundle around it,
// disclosed, with the x-standard-history/v0 block a receiver holds beside
// the card. The deal entries are in the holder's own copy only.
func (s *dealSession) standardHistoryBundle(ctx context.Context, shared bool) (_ map[string]interface{}, err error) {
	if s.p.Checkpoint.Signing == (cli.Secret{}) {
		return nil, cli.InputError("a History card needs the profile's checkpoint key (see `deal init`)")
	}
	h, err := s.standardHistory(ctx, 0)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	request := cli.Request{
		Version: "capsule-seal-request/v1",
		Capsule: emit.Input{
			ActionID: shActionID, ActionType: emit.ActionTypeFYI, Operator: s.p.Name, Developer: "capsulectl-deal",
			Timestamp: cli.DealClock().UTC().Truncate(time.Second),
		},
		Payload: h.Card,
	}
	s.dp = s.p
	s.dp.LogID = shLogPrefix + hex.EncodeToString(nonce)
	if s.t, err = openOwnedTarget(ctx, s.dp, cli.UseInitialization, s); err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, s.t.Close())
		s.t = nil
	}()
	pub, err := s.t.Publish(ctx, request, s.key)
	if err != nil {
		return nil, err
	}
	if _, err = cli.CutCheckpoint(ctx, localOnly(s.dp), s.t.Log); err != nil {
		return nil, err
	}
	b, err := cli.AssembleBundle(ctx, s.t.Artifacts, s.t.Log, s.dp.LogID, cli.BundleOptions{
		Root: pub.CapsuleID, Payloads: "all", WithDisclosure: true,
	})
	if err != nil {
		return nil, err
	}
	block := map[string]interface{}{"policy": h.Policy, "as_of": h.AsOf}
	if !shared {
		block["deals"] = h.Deals
	}
	extensions, _ := b["extensions"].(map[string]interface{})
	if extensions == nil {
		extensions = map[string]interface{}{}
	}
	extensions[shExtension] = block
	b["extensions"] = extensions
	return b, nil
}

// payloadOf is a held Capsule's payload, and whether the Capsule verifies
// under keys with that payload bound.
func payloadOf(held artifact.Record, keys []ed25519.PublicKey) ([]byte, bool, error) {
	checks, err := artifact.Verify(held, keys)
	if err != nil {
		return nil, false, fmt.Errorf("%w: a held Capsule: %v", cli.ErrConflict, err)
	}
	for _, a := range held.Artifacts {
		if a.Binding == artifact.PayloadDigest && a.State == artifact.Present {
			return a.Content, checks["payload"].Verified, nil
		}
	}
	return nil, false, fmt.Errorf("%w: a record's payload is not held", cli.ErrConflict)
}

// putSetting writes a settings file, creating it or replacing the one there
// (AtomicFile admits either, never both).
func putSetting(path string, raw []byte) error {
	_, err := os.Lstat(path)
	if err == nil {
		if err = checkSettingFile(path); err != nil {
			return err
		}
	}
	return cli.AtomicFile(path, raw, err == nil)
}

// checkSettingFile refuses a settings file that is not an owner-protected
// regular file, as a profile file is: these files decide what `deal respond`
// discloses, so one another user can write, or a link, is not trusted.
func checkSettingFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return cli.InputError(path + " is not an owner-protected regular file: run `chmod 600 " + path + "` (it must not be a link)")
	}
	return nil
}

// readSetting reads a settings file after checkSettingFile.
func readSetting(path string) ([]byte, error) {
	if err := checkSettingFile(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
