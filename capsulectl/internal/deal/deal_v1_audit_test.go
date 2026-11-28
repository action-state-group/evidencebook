// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// auditNoted are personal values of the other classes of spokenClassOrder
// (deal_card.go), as the holder's store keeps them in a disclosure note.
var auditNoted = map[string]string{
	"home_address":      "12 Larkspur Lane",
	"address":           "Unit 9, 40 Quarry Road",
	"pickup_location":   "Locker 4471 at Quarry Mall",
	"pickup_area":       "Quarryside North",
	"other_contact":     "@kettle_shop_desk",
	"credential":        "hunter2-kettle-9931",
	"verification_code": "739142",
	"payment_card":      "4111 1111 1111 1111",
	"id_document":       "passport X93817265",
}

// noteDisclosure seals one disclosure note per field given: each class is
// covered by its own check.
func noteDisclosure(t *testing.T, dealID string, fields map[string]string) {
	t.Helper()
	for _, class := range slices.Sorted(maps.Keys(fields)) {
		// The user approves telling it first, as the check asks.
		action := "share_contact"
		if slices.Contains([]string{"credential", "verification_code", "payment_card", "id_document"}, class) {
			action = "share_credentials"
		}
		share := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, `{"action":"`+action+`","disclosing_to":"counterparty","disclosing":["`+class+`"]}`))
		if share["verdict"] == "pause" {
			_, err := answer(t, dealID, share["check_id"].(string), "yes", shownCard(t, share["card"].(string)))
			require.NoError(t, err)
		}
		in, err := json.Marshal(map[string]any{"channel": "web", "fields": []map[string]string{{"class": class, "value": fields[class]}}})
		require.NoError(t, err)
		dealRun(t, "note", "--deal", dealID, "--kind", "disclosure", "--input", writeJSON(t, string(in)))
	}
}

// auditDeal is a started deal whose holder, the buyer, holds every personal
// value of auditPersonal and auditNoted in its own store, and has stated a
// row met.
func auditDeal(t *testing.T) *handshakeV1 {
	t.Helper()
	return auditDealWith(t, oneForum, auditOpening(t))
}

func auditDealWith(t *testing.T, offer func(string) string, opening string) *handshakeV1 {
	t.Helper()
	h := agreedBoth(t, offer)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, opening))
	noteDisclosure(t, h.dealID, auditNoted)
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
	for _, held := range []map[string]string{auditPersonal, auditNoted} {
		for class, v := range held {
			assert.NotContains(t, strings.ToLower(string(raw)), strings.ToLower(v), "the %s the holder holds", class)
		}
	}
	covered := map[string]bool{"name": true, "email": true, "phone": true, "other_contact": true}
	for class := range auditNoted {
		covered[class] = true
	}
	for _, class := range spokenClassOrder {
		assert.True(t, covered[class], "the fixture holds a value of class %s", class)
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
	for _, held := range []map[string]string{auditPersonal, auditNoted} {
		for class, v := range held {
			leaked := []byte(`{"note":"` + v + `"}`)
			assert.Error(t, dealShareGate(leaked, gotEvents, dealprofile.AudienceAudit), "the %s refuses an audit copy", class)
		}
	}

	v1ShareGate = func([]byte, []sealedEvent, string, ...string) error {
		return assert.AnError
	}
	refused := filepath.Join(t.TempDir(), "refused.json")
	assert.NotEmpty(t, v1Err(t, "report", "--deal", h.dealID, "--share", "audit", "--to", "Outside Auditor", "--bundle", refused))
	_, err = os.Lstat(refused)
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing written")
}

// A short name the holder holds, such as "Amy", in a signed record the copy
// would carry (here the offer's item) refuses the audit copy, which is then
// neither written nor recorded; the counterparty's copy is gated as before.
func TestAnAuditCopyCarryingAShortNameIsRefused(t *testing.T) {
	offer := func(key string) string {
		return strings.Replace(oneForum(key), `"item": "lamp"`, `"item": "lamp for Amy"`, 1)
	}
	h := auditDealWith(t, offer, `{"type": "purchase", "who": {"name": "Amy"}, "recourse": {"rail": "card", "refundable": true}}`)
	disclosures := func() int {
		var n int
		require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_disclosures WHERE deal_id=?`, h.dealID).Scan(&n))
		return n
	}
	before := disclosures()
	file := filepath.Join(t.TempDir(), "audit.json")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "audit", "--to", "Outside Auditor", "--bundle", file), "private value")
	_, err := os.Lstat(file)
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing written")
	assert.Equal(t, before, disclosures(), "nothing recorded")
	// The counterparty, who made this deal with Amy's side, gets the offer
	// it already holds.
	dealRun(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--bundle", filepath.Join(t.TempDir(), "cp.json"))
}

// Each class's value, held in the holder's store and carried in a signed
// record the copy would hold (the offer's item), refuses the real audit
// export: no file, no disclosure record. A short name held as a disclosed
// field is read as a whole word too.
func TestAnAuditCopyCarryingAHeldValueOfAnyClassIsRefused(t *testing.T) {
	// The control: the same offer, with nothing held about the value, is
	// written, so each refusal below is the held value's.
	control := noLayerAgreedWith(t, func(o string) string {
		return strings.Replace(o, `"item": "lamp"`, `"item": "lamp, `+auditNoted["address"]+`"`, 1)
	})
	dealRun(t, "report", "--deal", control, "--share", "audit", "--to", "Outside Auditor", "--bundle", filepath.Join(t.TempDir(), "control.json"))
	values := maps.Clone(auditNoted)
	values["name"] = "Bo"
	for _, class := range slices.Sorted(maps.Keys(values)) {
		v := values[class]
		t.Run(class, func(t *testing.T) {
			// A deal with the shop's own website (no layer on the other
			// side): one book, the cheapest real deal to run per class.
			dealID := noLayerAgreedWith(t, func(o string) string {
				return strings.Replace(o, `"item": "lamp"`, `"item": "lamp, `+v+`"`, 1)
			})
			noteDisclosure(t, dealID, map[string]string{class: v})
			var before int
			require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_disclosures WHERE deal_id=?`, dealID).Scan(&before))
			file := filepath.Join(t.TempDir(), "audit.json")
			assert.Contains(t, v1Err(t, "report", "--deal", dealID, "--share", "audit", "--to", "Outside Auditor", "--bundle", file), "private value")
			_, err := os.Lstat(file)
			assert.ErrorIs(t, err, os.ErrNotExist, "nothing written")
			var after int
			require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_disclosures WHERE deal_id=?`, dealID).Scan(&after))
			assert.Equal(t, before, after, "nothing recorded")
		})
	}
}

// --share audit, like every shared copy, needs a file and a reader.
func TestAV1AuditCopyNeedsAFileAndAReader(t *testing.T) {
	h := auditDeal(t)
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "audit", "--bundle", filepath.Join(t.TempDir(), "a.json")), "and --to")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "audit", "--to", "Outside Auditor"), "needs --out FILE or --bundle FILE")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--share", "auditor", "--to", "x", "--out", filepath.Join(t.TempDir(), "a.json")), "keep, counterparty, adjudicator or audit")
}
