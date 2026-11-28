// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditPersonal is one value of each personal detail the holder's own
// store holds about the other side: who they are (name, domain), how to reach
// them (phone, email, relay address, profile), and where the money goes
// (payee).
var auditPersonal = map[string]string{
	"name":          "Kettle Shop Ltd",
	"domain":        "kettle-shop.example",
	"phone":         "+1 415 555 0134",
	"email":         "orders@kettle-shop.example",
	"payee":         "Kettle Payee Account",
	"relay_address": "relay-kettle-shop-77",
	"profile_id":    "kettle-shop-profile-91",
}

// auditOpening is an opening whose who carries every value of auditPersonal.
func auditOpening(t *testing.T) string {
	t.Helper()
	who, err := json.Marshal(auditPersonal)
	require.NoError(t, err)
	return `{"type": "purchase", "who": ` + string(who) + `, "recourse": {"rail": "card", "refundable": true}}`
}

// auditDeal is a started deal whose holder, the buyer, holds every personal
// value of auditPersonal in its own store, and has stated a row met.
func auditDeal(t *testing.T) *handshakeV1 {
	t.Helper()
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, auditOpening(t)))
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	// A report rests on a checkpoint: the next tick covers the statement.
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	return h
}

// An audit copy (evidencebook#1058) is for an outside reader who is not a
// party. It verifies as the holder's copy does, it is put on record as an
// audit copy for its reader, its report says audit and carries no witness
// evidence, and none of the personal values the holder holds is anywhere in
// the file.
func TestAV1AuditCopy(t *testing.T) {
	h := auditDeal(t)
	file := filepath.Join(t.TempDir(), "audit.json")
	out := dealRun(t, "report", "--deal", h.dealID, "--share", "audit", "--to", "Outside Auditor", "--bundle", file)
	share := out["share"].(map[string]any)
	assert.Equal(t, dealprofile.AudienceAudit, share["audience"])
	assert.Equal(t, "Outside Auditor", share["recipient"])

	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	for class, v := range auditPersonal {
		assert.NotContains(t, strings.ToLower(string(raw)), strings.ToLower(v), "the %s the holder holds", class)
	}
	assert.NotContains(t, string(raw), "Outside Auditor", "the reader is named on the holder's record only")

	res, err := verifyBundleOutput(t, file)
	require.NoError(t, err, "%v", res)
	assert.NotContains(t, v1Entry(t, res), "counterparty_half")

	value := v1BundleOf(t, file)
	exts := value["extensions"].(map[string]interface{})
	report := exts[dealprofile.ReportExtension].(map[string]interface{})["agent_input"].(map[string]interface{})
	assert.Equal(t, dealprofile.AudienceAudit, report["audience"])
	assert.NotContains(t, report, "referee")
	assert.NotContains(t, report["rests_on"].(map[string]interface{}), "witnesses")
	require.NotEmpty(t, report["rows"])
	for _, row := range report["rows"].([]interface{}) {
		for _, src := range row.(map[string]interface{})["relied_on"].([]interface{}) {
			s := src.(map[string]interface{})
			assert.Nil(t, s["time"], "no witnessed time")
			assert.Equal(t, false, s["witnessed"])
		}
	}
	assert.NotContains(t, exts, xdealv1.CounterpartyHalfExtension)

	// The other side's half, moved into an audit copy, makes it INVALID: only
	// the holder's own copy carries it.
	keep := filepath.Join(t.TempDir(), "keep.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", keep)
	k := v1BundleOf(t, keep)
	half, ok := k["extensions"].(map[string]interface{})[xdealv1.CounterpartyHalfExtension]
	require.True(t, ok, "the holder's own copy carries the other side's half")
	moved := rewriteBundle(t, file, func(exts map[string]interface{}) { exts[xdealv1.CounterpartyHalfExtension] = half })
	res, err = verifyBundleOutput(t, moved)
	assert.Error(t, err)
	assert.Equal(t, "INVALID", res["verdict"])
	assert.Equal(t, []interface{}{DealV1CounterpartyHalfFailed}, v1Entry(t, res)["findings"])

	// capsulectl's offers name no ask, so the audit copy carries the offer:
	// the item and quantity (Sherlock2's ruling on evidencebook#1058).
	book := exts[xdealv1.Profile].(map[string]interface{})["book"].(map[string]interface{})
	assert.NotNil(t, book["offer"])
}

// The audit copy's gate reads the exact bytes written, payment references
// not blanked, and refuses a copy that would carry any of the personal values
// the holder holds; a refused copy is neither written nor recorded.
func TestTheAuditGateReadsTheWholeCopy(t *testing.T) {
	h := auditDeal(t)
	var gotEvents []sealedEvent
	var gotBytes []byte
	v1ShareGate = func(data []byte, events []sealedEvent, audience string, own ...string) error {
		require.Equal(t, dealprofile.AudienceAudit, audience)
		gotEvents, gotBytes = events, data
		return dealShareGate(data, events, audience, own...)
	}
	t.Cleanup(func() { v1ShareGate = dealShareGate })
	file := filepath.Join(t.TempDir(), "audit.json")
	dealRun(t, "report", "--deal", h.dealID, "--share", "audit", "--to", "Outside Auditor", "--bundle", file)
	written, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, written, gotBytes, "the gate read the bytes written, nothing blanked")
	for class, v := range auditPersonal {
		leaked := []byte(`{"note":"` + v + `"}`)
		assert.Error(t, dealShareGate(leaked, gotEvents, dealprofile.AudienceAudit), "the %s refuses an audit copy", class)
	}

	v1ShareGate = func([]byte, []sealedEvent, string, ...string) error {
		return assert.AnError
	}
	refused := filepath.Join(t.TempDir(), "refused.json")
	assert.NotEmpty(t, v1Err(t, "report", "--deal", h.dealID, "--share", "audit", "--to", "Outside Auditor", "--bundle", refused))
	_, err = os.Lstat(refused)
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing written")
}

// --share audit, like every shared copy, needs a file and a reader.
func TestAV1AuditCopyNeedsAFileAndAReader(t *testing.T) {
	h := auditDeal(t)
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "audit", "--bundle", filepath.Join(t.TempDir(), "a.json")), "and --to")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "audit", "--to", "Outside Auditor"), "needs --out FILE or --bundle FILE")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "auditor", "--to", "x", "--out", filepath.Join(t.TempDir(), "a.json")), "keep, counterparty, adjudicator or audit")
}
