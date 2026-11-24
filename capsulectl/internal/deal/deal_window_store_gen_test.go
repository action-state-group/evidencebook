package deal

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// windowStoreFixture is the committed windowed deal store (evidencebook#852):
// two ended calendar months, one placed deal in each. A process that runs the
// released binaries cannot move capsulectl's clock, so the store is written
// here, in process, under cli.DealClock; PROVENANCE.md beside it says how.
const windowStoreFixture = "testdata/deal/window-store"

// windowStoreSeptember and windowStoreOctober are the instants the generator
// agrees its two deals at; the store is rolled at windowStoreRolled.
var (
	windowStoreSeptember = time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)
	windowStoreOctober   = time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	windowStoreRolled    = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
)

// TestWriteWindowStoreFixture regenerates the fixture. It runs only with
// EB_WRITE_WINDOW_STORE=1, for an intended change:
//
//	EB_WRITE_WINDOW_STORE=1 go test -run TestWriteWindowStoreFixture ./internal/deal
//
// (from capsulectl/). The keys it writes are made for the fixture and protect
// nothing.
func TestWriteWindowStoreFixture(t *testing.T) {
	if os.Getenv("EB_WRITE_WINDOW_STORE") != "1" {
		t.Skip("set EB_WRITE_WINDOW_STORE=1 to regenerate " + windowStoreFixture)
	}
	seller, _ := newWindowedV1Side(t)
	buyer, _ := newWindowedV1Side(t)
	config := seller.party.config
	profile, err := os.ReadFile(filepath.Join(config, "capsule", "profiles", "deal.yaml"))
	require.NoError(t, err)
	match := regexp.MustCompile(`(?m)^\s+database: (.+)/deal\.db$`).FindSubmatch(profile)
	require.NotNil(t, match, "the profile names the store: %s", profile)
	storeDir := string(match[1])

	for i, at := range []time.Time{windowStoreSeptember, windowStoreOctober} {
		setWindowClock(t, at)
		buyer.act(t)
		offerFile := filepath.Join(t.TempDir(), "offer.json")
		input := strings.Replace(offerTo(seller.party.signer), `"item": "lamp"`, `"item": "`+[]string{"lamp", "desk"}[i]+`"`, 1)
		res := dealRun(t, "offer", "--input", writeJSON(t, input), "--out", offerFile)
		seller.act(t)
		got := dealRun(t, "receive", offerFile)
		require.Equal(t, "kept, and counted", got["record"], "%v", got)
		agreed := dealRun(t, "agree", res["offer"].(string), "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "agreement.json"))
		require.Equal(t, true, agreed["stamp_proof"], "%v", agreed)
	}
	setWindowClock(t, windowStoreRolled)
	seller.act(t)
	dealRun(t, "window", "roll")

	dest := filepath.Join(windowStoreFixture, "store")
	require.NoError(t, os.RemoveAll(windowStoreFixture))
	require.NoError(t, os.MkdirAll(dest, 0o755))
	copyStore(t, storeDir, dest)
	// The profile names the materiality predicate by path; the fixture keeps
	// a copy beside the store, so no path of this machine is committed.
	predicate, err := os.ReadFile(neutralMateriality)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dest, "materiality.json"), predicate, 0o644))
	profile = bytes.ReplaceAll(profile, []byte(neutralMateriality), []byte("@STORE@/materiality.json"))
	require.NoError(t, os.WriteFile(filepath.Join(windowStoreFixture, "deal.yaml"), []byte(strings.ReplaceAll(string(profile), storeDir, "@STORE@")), 0o644))

	version, err := exec.Command("go", "list", "-m", "-f", "{{.Version}}", "github.com/action-state-group/capsule-cli").Output()
	require.NoError(t, err)
	commit, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	provenance := []byte(strings.NewReplacer(
		"@CAPSULE_CLI@", strings.TrimSpace(string(version)),
		"@COMMIT@", strings.TrimSpace(string(commit)),
	).Replace(windowStoreProvenance))
	require.NoError(t, os.WriteFile(filepath.Join(windowStoreFixture, "PROVENANCE.md"), provenance, 0o644))
}

// copyStore copies the store's files, skipping SQLite's lock and journal
// companions (the generator has closed the store, so none holds data).
func copyStore(t *testing.T, from, to string) {
	t.Helper()
	require.NoError(t, filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(rel, "-wal") || strings.HasSuffix(rel, "-shm") || strings.HasSuffix(rel, ".lock") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, bytes.Clone(raw), 0o600)
	}))
}

const windowStoreProvenance = "" +
	"# window-store\n\n" +
	"A windowed deal store (`deal init --window month`) with two ended calendar months and one\n" +
	"deal placed in each: 2026-09 (agreed 2026-09-14T15:00:00Z) and 2026-10 (agreed\n" +
	"2026-10-12T15:00:00Z), rolled at 2026-11-02T09:00:00Z. Made for evidencebook#852, so a test\n" +
	"outside Go can run `deal window chain`, `deal window respond`, `book report derive --chain`\n" +
	"and `book report coverage` on released binaries: capsulectl has no clock flag, and a store\n" +
	"made in real time has only its open window.\n\n" +
	"- Generator: `TestWriteWindowStoreFixture` in `capsulectl/internal/deal/deal_window_store_gen_test.go`,\n" +
	"  which sets `cli.DealClock` and runs the commands in process.\n" +
	"- Command, from `capsulectl/`: `EB_WRITE_WINDOW_STORE=1 go test -run TestWriteWindowStoreFixture ./internal/deal`\n" +
	"- capsulectl: built from evidencebook commit `@COMMIT@` against capsule-cli `@CAPSULE_CLI@`.\n" +
	"- Files: `deal.yaml` is the deal profile with the store directory written as `@STORE@`;\n" +
	"  `store/` holds the SQLite store and its seeds. The seeds are made for this fixture and protect\n" +
	"  nothing.\n" +
	"- Use a copy: `deal window chain` and `deal window respond` write to the store. Copy `store/`\n" +
	"  to a new directory, replace `@STORE@` in `deal.yaml` with it, and save the result as\n" +
	"  `$XDG_CONFIG_HOME/capsule/profiles/deal.yaml`.\n" +
	"- Fetch at a tag: `git archive <tag> capsulectl/internal/deal/testdata/deal/window-store | tar -x`\n" +
	"  (a clone of action-state-group/evidencebook; the module's own tags are `capsulectl/v<version>`).\n" +
	"- The test that uses it: `capsulectl/cmd/window_store_cli_test.go`.\n"
