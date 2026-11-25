package deal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// The canary is a scripted DEMO deal run on a schedule, and a watcher that
// reads only the public witness, signing its read with the log's checkpoint
// key: the witness answers a log's size, root and time to that key alone
// (capsule-anchor#110, ruling u226).
//
// A deal profile's cadence log grows by one entry per tick whether or not
// anything happened (see deal_cadence.go), so on a profile with its own tick
// timer the public log advances even when every deal fails. `canary run`
// therefore runs on a profile of its own whose only ticker it is: it ticks
// only after the whole flow (open, note, check with its approval, report)
// has succeeded. A run that stops anywhere publishes nothing, the public log
// stops advancing, and `canary watch`, run anywhere, raises the alarm from
// the witness alone. Absence is the alarm.

// errCanaryAlarm is `canary watch` finding the log stale or its history
// rewritten. It reads "canary alarm: ..." as it always did, and is the core's
// alarm class (exit code 6) for the exit code.
var errCanaryAlarm error = canaryAlarm{}

type canaryAlarm struct{}

func (canaryAlarm) Error() string        { return "canary alarm" }
func (canaryAlarm) Is(target error) bool { return target == cli.ErrAlarm }

// errWitnessUnread is `canary watch` unable to read the witness at all: it
// cannot tell whether the log advanced, so it says so instead of alarming.
var errWitnessUnread = errors.New("witness not readable")

// errReadKeyRefused is the witness refusing the read key for the log: its
// checkpoints are signed by another key. An alarm, as a key change always was.
var errReadKeyRefused = errors.New("the witness refused this key for the log")

// errUnsignedFork is an answer without the checkpoint that still lists the
// log's flagged equivocations: a fork, so an alarm, though nothing else was read.
var errUnsignedFork = errors.New("history was rewritten")

func canaryCommands() *cobra.Command {
	canary := &cobra.Command{Use: "canary", Short: "A scheduled DEMO deal, and a watcher that alarms from the public witness when it stops",
		// An operator's monitor, never run by an agent: a failure keeps
		// its one plain line and gets no error object.
		Annotations: map[string]string{cli.NarrationExempt: "true"}}
	canary.AddCommand(canaryRunCommand(), canaryWatchCommand())
	return canary
}

// The scripted deal: a synthetic merchant, a demo purchase, never real money.
const (
	canaryOpen = `{"type":"purchase","demo":true,"channel":"web",
"intent":{"verbatim":"Canary: order the cat sticker in my cart at Example Stickers (demo)","asked":{"item":"cat sticker","quantity":1},"allowed":["pay"]},
"who":{"name":"Example Stickers","domain":"stickers.example"},
"terms":{"item":"cat sticker","quantity":1,"price_minor":627,"currency":"USD"},
"recourse":{"rail":"card","refundable":true}}`
	canaryCheck = `{"action":"pay","description":"Place order: 1 cat sticker, $6.27 total, saved card","amount_minor":627,"authorized_max_minor":627,
"who":{"name":"Example Stickers","domain":"stickers.example"},
"terms":{"item":"cat sticker","quantity":1,"price_minor":627,"currency":"USD"},
"recourse":{"rail":"card","refundable":true}}`
)

func canaryRunCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Run the scripted DEMO deal on this canary profile and tick its cadence log only if every step succeeded", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		skill, _ := c.Flags().GetString("skill")
		wantVersion, _ := c.Flags().GetString("expect-version")
		wantSkill, _ := c.Flags().GetString("expect-skill-sha256")
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		if p.Checkpoint.Endpoint == "" {
			return cli.Hint(cli.ErrInput, "canary: this profile has no witness, so nothing the canary does can be seen from outside; nothing was sealed")
		}
		if wantVersion != "" && wantVersion != binaryVersion() {
			return cli.Hint(cli.ErrConflict, fmt.Sprintf("canary: this binary is %s, expected %s; nothing was sealed", binaryVersion(), wantVersion))
		}
		skillSum := ""
		if skill != "" {
			data, err := os.ReadFile(skill)
			if err != nil {
				return cli.Hint(cli.ErrInput, fmt.Sprintf("canary: the skill file %s cannot be read (%s); nothing was sealed", skill, describeFileError(err)))
			}
			sum := sha256.Sum256(data)
			skillSum = hex.EncodeToString(sum[:])
		}
		if wantSkill != "" {
			if skill == "" {
				return cli.InputError("--expect-skill-sha256 needs --skill")
			}
			if !strings.EqualFold(wantSkill, skillSum) {
				return cli.Hint(cli.ErrConflict, fmt.Sprintf("canary: %s has sha256 %s, expected %s; nothing was sealed", skill, skillSum, strings.ToLower(wantSkill)))
			}
		}

		work, err := os.MkdirTemp("", "capsulectl-canary-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(work)
		file := func(name, body string) (string, error) {
			path := filepath.Join(work, name)
			return path, os.WriteFile(path, []byte(body), 0o600)
		}
		step := func(args ...string) (map[string]any, error) {
			return canaryStep(c.Context(), p.Name, args...)
		}

		// The canary's scripted deal is an x-deal-v0 purchase from a merchant
		// with no key, which x-deal-v1 has no form for: a profile with no
		// version setting is pinned to the legacy x-deal-v0 here, so a
		// deployed canary keeps ticking through the default's change
		// (evidencebook#626).
		pinned, err := pinCanaryLegacy(p)
		if err != nil {
			return err
		}
		openPath, err := file("open.json", canaryOpen)
		if err != nil {
			return err
		}
		opened, err := step("open", "--input", openPath)
		if err != nil {
			return err
		}
		dealID, _ := opened["deal_id"].(string)
		if dealID == "" {
			return cli.Hint(cli.ErrPartial, "canary: `deal open` gave no deal_id; stopped before the tick")
		}
		skillDetail := "no skill file given"
		if skillSum != "" {
			skillDetail = "skill sha256 " + skillSum
		}
		noteBody, _ := json.Marshal(map[string]string{
			"about":  "canary self-check",
			"source": "capsulectl deal canary run",
			"detail": fmt.Sprintf("binary %s (commit %s, capsulectl core %s); %s", binaryVersion(), binaryCommit(), cli.CLIVersion, skillDetail),
		})
		notePath, err := file("note.json", string(noteBody))
		if err != nil {
			return err
		}
		if _, err = step("note", "--deal", dealID, "--kind", "evidence", "--input", notePath); err != nil {
			return err
		}
		checkPath, err := file("check.json", canaryCheck)
		if err != nil {
			return err
		}
		checked, err := step("check", "--deal", dealID, "--input", checkPath)
		if err != nil {
			return err
		}
		approvalID, _ := checked["approval_id"].(string)
		if checked["verdict"] != "pass" || approvalID == "" {
			return cli.Hint(cli.ErrPartial, fmt.Sprintf("canary: the demo check did not pass (verdict %v), so no approval was sealed; stopped before the tick", checked["verdict"]))
		}
		if _, err = step("report", "--deal", dealID); err != nil {
			return err
		}
		ticked, err := step("tick")
		if err != nil {
			return err
		}
		out := map[string]any{
			"canary":         "ok",
			"deal_id":        dealID,
			"check_id":       checked["check_id"],
			"approval_id":    approvalID,
			"binary_version": binaryVersion(),
			"binary_commit":  binaryCommit(),
			"core_version":   cli.CLIVersion,
			"cadence_log":    ticked["cadence_log"],
			"tick":           ticked["state"],
		}
		if skillSum != "" {
			out["skill_sha256"] = skillSum
		}
		for _, k := range []string{"checkpoint", "due", "delivered", "pending", "witness"} {
			if v, ok := ticked[k]; ok {
				out[k] = v
			}
		}
		if pinned {
			out["version_pinned"] = "this canary profile had no deal version setting; it is set to the legacy x-deal-v0, the version its scripted deal uses"
		}
		return cli.Output(c, out)
	}}
	cmd.Flags().String("skill", "", "The SKILL.md the agent loads; its sha256 is sealed in the run, and a missing file stops the run")
	cmd.Flags().String("expect-version", "", "Stop before sealing unless this binary (capsulectl-deal) reports exactly this version, the release version (the capsulectl/vX.Y.Z tag without its capsulectl/ prefix)")
	cmd.Flags().String("expect-skill-sha256", "", "Stop before sealing unless --skill has exactly this sha256")
	return cmd
}

// canaryStep runs one `deal` verb on the profile exactly as an agent would,
// and returns its JSON result. A failing verb stops the run before the tick
// and names the verb, with the verb's own (already non-secret) message.
func canaryStep(ctx context.Context, profile string, args ...string) (map[string]any, error) {
	root := cli.NewCommandWith(DealGroup())
	var out, stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--profile", profile, "deal"}, args...))
	if err := root.ExecuteContext(ctx); err != nil {
		class := cli.ErrPartial
		for _, known := range []error{cli.ErrInput, cli.ErrConflict, cli.ErrPending, cli.ErrPartial} {
			if errors.Is(err, known) {
				class = known
				break
			}
		}
		reason := cli.SafeError(err)
		return nil, cli.Hint(class, fmt.Sprintf("canary: `deal %s` failed: %s; stopped before the tick", args[0], reason))
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, cli.Hint(cli.ErrPartial, fmt.Sprintf("canary: `deal %s` printed no result object; stopped before the tick", args[0]))
	}
	return result, nil
}

func describeFileError(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "no such file"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	default:
		return "unreadable"
	}
}

// witnessCheckpoint is the witness's read-only answer for one log:
// GET {witness}/checkpoints/{log_id}, the last checkpoint it accepted.
type witnessCheckpoint struct {
	LogID           string            `json:"log_id"`
	MMRSize         uint64            `json:"mmr_size"`
	Root            string            `json:"root"`
	PrevSize        uint64            `json:"prev_size"`
	PrevRoot        string            `json:"prev_root"`
	KeyID           string            `json:"key_id"`
	Timestamp       string            `json:"timestamp"`
	Equivocations   []json.RawMessage `json:"equivocations"`
	ContinuityGrade string            `json:"continuity_grade"`
}

// canaryObservation is what `canary watch` keeps between runs.
type canaryObservation struct {
	LogID     string `json:"log_id"`
	MMRSize   uint64 `json:"mmr_size"`
	Root      string `json:"root"`
	KeyID     string `json:"key_id"`
	Timestamp string `json:"timestamp"`
	// SizeSince is when this watcher first saw the log at MMRSize.
	SizeSince string `json:"size_since"`
}

var canaryHTTP = canaryClient(nil)

// canaryClient reads the witness with transport (nil: the default), and
// follows no redirect: the read carries a signature that anyone holding it
// can replay for a few minutes, so it goes to the witness named and nowhere
// else.
func canaryClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("redirected; a signed read follows no redirect")
		},
	}
}

// The signed read of GET /checkpoints/{log_id} (capsule-anchor
// checkpoint_read.py): Ed25519 over "capsule-anchor/checkpoint-read/v1" LF
// log_id LF unix seconds, the signature base64url without padding.
const (
	readTimeHeader      = "Checkpoint-Read-Time"
	readSignatureHeader = "Checkpoint-Read-Signature"
	readDomain          = "capsule-anchor/checkpoint-read/v1"
)

func signRead(key ed25519.PrivateKey, logID string, at time.Time) (string, string) {
	t := strconv.FormatInt(at.Unix(), 10)
	sig := ed25519.Sign(key, []byte(readDomain+"\n"+logID+"\n"+t))
	return t, base64.RawURLEncoding.EncodeToString(sig)
}

// canaryReadKey is the key `canary watch` signs its read with: the selected
// profile's checkpoint key, or a seed from --read-key-file / --read-key-env
// when it runs off the canary's host. It also settles the log id: a profile's
// own cadence log unless --log-id names it.
func canaryReadKey(c *cobra.Command, logID string) (ed25519.PrivateKey, string, error) {
	file, _ := c.Flags().GetString("read-key-file")
	env, _ := c.Flags().GetString("read-key-env")
	profile, _ := c.Flags().GetString("profile")
	sources := 0
	for _, v := range []string{file, env, profile} {
		if v != "" {
			sources++
		}
	}
	if sources > 1 {
		return nil, "", cli.InputError("give one of --profile, --read-key-file or --read-key-env")
	}
	if file != "" || env != "" {
		if logID == "" {
			return nil, "", cli.InputError("--log-id is required with --read-key-file or --read-key-env")
		}
		key, err := cli.PrivateKey(cli.Secret{File: file, Env: env})
		return key, logID, err
	}
	p, err := cli.Selected(c)
	if err != nil {
		return nil, "", cli.Hint(err, "the witness answers a log's checkpoint only to the log's own key: pass --profile with the canary's profile, or --read-key-file / --read-key-env with its checkpoint seed")
	}
	if p.Checkpoint.Signing == (cli.Secret{}) {
		return nil, "", cli.InputError("profile " + p.Name + " has no checkpoint signing key to sign the read with")
	}
	switch {
	case logID == "":
		logID = p.LogID
	case logID != p.LogID:
		return nil, "", cli.InputError("--log-id " + logID + " is not profile " + p.Name + "'s cadence log " + p.LogID + ": the witness answers a log only to its own key")
	}
	if logID == "" {
		return nil, "", cli.InputError("profile " + p.Name + " has no cadence log yet: run `deal canary run` on it first")
	}
	key, err := cli.PrivateKey(p.Checkpoint.Signing)
	return key, logID, err
}

// witnessURLAllowed: https, or plain http to a loopback address (a witness
// run on this machine, as the tests do).
func witnessURLAllowed(u *url.URL) bool {
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return false
}

func canaryWatchCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "watch", Short: "Read a log's last checkpoint from the public witness and alarm when it is stale or its history was rewritten", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		logID, _ := c.Flags().GetString("log-id")
		witness, _ := c.Flags().GetString("witness")
		every, _ := c.Flags().GetDuration("expect-every")
		statePath, _ := c.Flags().GetString("state")
		if every <= 0 {
			return cli.InputError("a positive --expect-every is required")
		}
		if u, err := url.Parse(witness); err != nil || u.Host == "" || !witnessURLAllowed(u) {
			return cli.Hint(cli.ErrInput, "--witness must be an https URL (plain http only for a witness on this machine)")
		}
		key, logID, err := canaryReadKey(c, logID)
		if err != nil {
			return err
		}
		if statePath == "" {
			dir, err := cli.ProfilesDir()
			if err != nil {
				return err
			}
			statePath = filepath.Join(filepath.Dir(dir), "canary", canaryStateName(logID)+".json")
		}
		now := cli.DealClock().UTC()
		// The read is signed with the real clock: the witness checks it
		// against its own, not against this command's deal clock.
		cp, found, err := readWitnessCheckpoint(c.Context(), witness, logID, key, time.Now())
		if errors.Is(err, errUnsignedFork) {
			return cli.Hint(errCanaryAlarm, fmt.Sprintf("%s %s", logID, err))
		}
		if errors.Is(err, errReadKeyRefused) {
			// The last good observation is kept, so every later run alarms too.
			return cli.Hint(errCanaryAlarm, fmt.Sprintf("%s: %s (its checkpoints are now signed by another key, or this is not the log's key)", logID, err))
		}
		if err != nil {
			return cli.Hint(errWitnessUnread, fmt.Sprintf("could not read %s for %s (%s); cannot tell whether it advanced", witness, logID, err))
		}
		if !found {
			return cli.Hint(errCanaryAlarm, fmt.Sprintf("%s has never been witnessed at %s", logID, witness))
		}
		prev, hadPrev, err := readCanaryState(statePath)
		if err != nil {
			return err
		}
		if why := canaryRewritten(cp, prev, hadPrev); why != "" {
			// The last good observation is kept, so every later run alarms too.
			return cli.Hint(errCanaryAlarm, fmt.Sprintf("%s history was rewritten: %s", logID, why))
		}
		next := canaryObservation{LogID: logID, MMRSize: cp.MMRSize, Root: cp.Root, KeyID: cp.KeyID, Timestamp: cp.Timestamp, SizeSince: now.Format(time.RFC3339)}
		if hadPrev && prev.MMRSize == cp.MMRSize && prev.SizeSince != "" {
			next.SizeSince = prev.SizeSince
		}
		if err := writeCanaryState(statePath, next); err != nil {
			return err
		}
		stamped, err := time.Parse(time.RFC3339, cp.Timestamp)
		if err != nil {
			return cli.Hint(errCanaryAlarm, fmt.Sprintf("%s: the witness's last checkpoint has no readable time (%q)", logID, cp.Timestamp))
		}
		since, _ := time.Parse(time.RFC3339, next.SizeSince)
		if age := now.Sub(stamped); age > every {
			return cli.Hint(errCanaryAlarm, fmt.Sprintf("%s has not advanced since %s (%s ago; expected every %s)", logID, cp.Timestamp, age.Truncate(time.Minute), every))
		}
		if held := now.Sub(since); held > every {
			return cli.Hint(errCanaryAlarm, fmt.Sprintf("%s has stayed at size %d since %s (%s; expected every %s)", logID, cp.MMRSize, next.SizeSince, held.Truncate(time.Minute), every))
		}
		return cli.Output(c, map[string]any{
			"state":            "advancing",
			"log_id":           logID,
			"mmr_size":         cp.MMRSize,
			"root":             cp.Root,
			"timestamp":        cp.Timestamp,
			"age_seconds":      int64(now.Sub(stamped) / time.Second),
			"continuity_grade": cp.ContinuityGrade,
		})
	}}
	cmd.Flags().String("log-id", "", "The canary profile's cadence log id (deal-cadence/...); default: the --profile's own")
	cmd.Flags().String("read-key-file", "", "Off the canary's host: a file (mode 0600) holding the canary profile's 32-byte checkpoint seed in hex, to sign the read with")
	cmd.Flags().String("read-key-env", "", "Off the canary's host: an environment variable holding that seed")
	cmd.Flags().String("witness", DealDefaultWitness, "The public witness to read")
	cmd.Flags().Duration("expect-every", 0, "Alarm when the log has not advanced for longer than this: the canary's schedule plus the tick interval and jitter")
	cmd.Flags().String("state", "", "Where this watcher keeps its last observation (default: under the config directory)")
	return cmd
}

// canaryRewritten names how cp contradicts the last observation, or "".
func canaryRewritten(cp witnessCheckpoint, prev canaryObservation, hadPrev bool) string {
	if n := len(cp.Equivocations); n > 0 {
		return fmt.Sprintf("the witness has flagged %d conflicting checkpoint(s) for this log", n)
	}
	if !hadPrev {
		return ""
	}
	switch {
	case cp.KeyID != prev.KeyID:
		return fmt.Sprintf("its checkpoints are now signed by key %s, not %s", cp.KeyID, prev.KeyID)
	case cp.MMRSize < prev.MMRSize:
		return fmt.Sprintf("it went back from size %d to %d", prev.MMRSize, cp.MMRSize)
	case cp.MMRSize == prev.MMRSize && cp.Root != prev.Root:
		return fmt.Sprintf("size %d now has root %s, not %s", cp.MMRSize, cp.Root, prev.Root)
	case cp.PrevSize == prev.MMRSize && cp.PrevRoot != prev.Root:
		return fmt.Sprintf("the checkpoint after size %d says that size had root %s, not %s", prev.MMRSize, cp.PrevRoot, prev.Root)
	}
	return ""
}

func readWitnessCheckpoint(ctx context.Context, witness, logID string, key ed25519.PrivateKey, at time.Time) (witnessCheckpoint, bool, error) {
	base, err := url.Parse(witness)
	if err != nil || base.Host == "" || !witnessURLAllowed(base) {
		return witnessCheckpoint{}, false, errors.New("not an https URL")
	}
	segments := strings.Split(logID, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	target := strings.TrimRight(witness, "/") + "/checkpoints/" + strings.Join(segments, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return witnessCheckpoint{}, false, err
	}
	t, sig := signRead(key, logID, at)
	req.Header.Set(readTimeHeader, t)
	req.Header.Set(readSignatureHeader, sig)
	resp, err := canaryHTTP.Do(req)
	if err != nil {
		var redirect *url.Error
		if errors.As(err, &redirect) && strings.Contains(redirect.Err.Error(), "redirect") {
			return witnessCheckpoint{}, false, redirect.Err
		}
		return witnessCheckpoint{}, false, errors.New("no answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return witnessCheckpoint{}, false, nil
	}
	if resp.StatusCode == http.StatusForbidden {
		return witnessCheckpoint{}, false, readRefusal(resp.Body)
	}
	if resp.StatusCode != http.StatusOK {
		return witnessCheckpoint{}, false, fmt.Errorf("status %d", resp.StatusCode)
	}
	var cp witnessCheckpoint
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cp); err != nil {
		return witnessCheckpoint{}, false, errors.New("not a checkpoint")
	}
	if cp.LogID != logID {
		return witnessCheckpoint{}, false, fmt.Errorf("answered for log %q", cp.LogID)
	}
	if cp.Root == "" || cp.Timestamp == "" {
		// The answer an unsigned read gets: the read's headers did not reach
		// the witness, so nothing here says whether the log advanced. Its
		// equivocations are still public, and a fork is still an alarm.
		if n := len(cp.Equivocations); n > 0 {
			return witnessCheckpoint{}, false, fmt.Errorf("%w: the witness has flagged %d conflicting checkpoint(s) for this log", errUnsignedFork, n)
		}
		return witnessCheckpoint{}, false, errors.New("answered without the checkpoint, as to an unsigned read")
	}
	return cp, true, nil
}

// readRefusal names a 403 from the witness by its code. A key mismatch is
// errReadKeyRefused (an alarm); anything else leaves the log unread.
func readRefusal(body io.Reader) error {
	var refusal struct {
		Detail struct {
			Code string `json:"code"`
		} `json:"detail"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<16)).Decode(&refusal); err != nil {
		// A body that does not decode names no code: a partly decoded one
		// must not read as a key change.
		return errors.New("status 403")
	}
	switch refusal.Detail.Code {
	case "read_key_mismatch":
		return errReadKeyRefused
	case "read_time_skew":
		return errors.New("refused the read: this machine's clock is more than 5 minutes from the witness's")
	case "read_signature_malformed":
		return errors.New("refused the read as malformed")
	}
	return errors.New("status 403")
}

var canaryUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func canaryStateName(logID string) string { return canaryUnsafe.ReplaceAllString(logID, "_") }

func readCanaryState(path string) (canaryObservation, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return canaryObservation{}, false, nil
	}
	if err != nil {
		return canaryObservation{}, false, cli.Hint(cli.ErrInput, fmt.Sprintf("canary state %s is unreadable", path))
	}
	var o canaryObservation
	if err := json.Unmarshal(data, &o); err != nil {
		return canaryObservation{}, false, cli.Hint(cli.ErrInput, fmt.Sprintf("canary state %s is not a canary observation; move it aside to start over", path))
	}
	return o, true, nil
}

func writeCanaryState(path string, o canaryObservation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// binaryVersion and binaryCommit name the capsulectl-deal binary that runs the
// canary: its own release tag and commit, and, in a build that sets neither,
// the capsulectl core's. The core's version is sealed beside them.
func binaryVersion() string { return cli.FirstNonEmpty(cli.PluginVersion, cli.CLIVersion) }
func binaryCommit() string  { return cli.FirstNonEmpty(cli.PluginCommit, cli.CLICommit) }

// pinCanaryLegacy sets a canary profile with no version setting to the
// legacy x-deal-v0 and says whether it did.
func pinCanaryLegacy(p cli.Profile) (bool, error) {
	dir, err := shDir(p)
	if err != nil {
		return false, err
	}
	if _, err := readSetting(filepath.Join(dir, dealVersionFile)); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, putDealVersionSetting(p, dealVersionV0)
}
