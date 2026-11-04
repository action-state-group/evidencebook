package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pack that judged a check is named on the receipt (evidencebook-deal#138): on the user's own
// copy always, and on a counterparty's copy only when the profile pins that pack as the party's
// deal terms (kind "deal"). A conduct pack, or a pin that states no kind, reaches no one.

const (
	packID     = "sticker-sale/1"
	packDigest = "a81f3c9d2e10b4c7d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c"
	packShown  = "a81f3c9d2e10"
)

// packChecked pins a stub checker that reports the sticker pack as evaluated, with the given
// kind in the pin ("" states none), and runs one check on a new seller or buyer deal.
func packChecked(t *testing.T, kind, role string) string {
	t.Helper()
	return packCheckedUnder(t, role, func(pin map[string]any) {
		if kind != "" {
			pin["kind"] = kind
		}
	})
}

// packCheckedUnder is packChecked with the pin shaped by shape (which may add roles, a kind, other digests).
func packCheckedUnder(t *testing.T, role string, shape func(pin map[string]any)) string {
	t.Helper()
	dealFixture(t)
	out := `printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"` + packID + `","definition_digest":"` + packDigest +
		`","verdict":"allow","tier":"recomputed","findings":[{"id":"price_as_offered","check":"price equals the offer","verdict":"pass"},` + declaredFindings + `]}'`
	pin := map[string]any{"command": []string{stubChecker(t, "pack", out)}, "definition_digest": packDigest}
	shape(pin)
	pinChecker(t, pin)
	if role == dealRoleSeller {
		id := openTyped(t, sellerTyped)
		dealRun(t, "check", "--deal", id, "--input", writeJSON(t,
			`{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`))
		return id
	}
	id := openTypedSticker(t)
	checkPay(t, id, 600)
	return id
}

// receiptText is every word of a copy's report, own or shared: the page's embedded bundle
// as the viewer draws it.
func receiptText(t *testing.T, id string, shared bool) string {
	t.Helper()
	page := filepath.Join(t.TempDir(), "copy.html")
	args := []string{"report", "--deal", id, "--html", page}
	if shared {
		args = append(args, "--share", "counterparty", "--to", "the other side")
	}
	dealRun(t, args...)
	raw, err := os.ReadFile(page)
	require.NoError(t, err)
	text, err := json.Marshal(dealReportOf(embeddedBundle(t, string(raw))))
	require.NoError(t, err)
	return string(text)
}

func TestTheOwnReceiptNamesThePackThatJudgedTheCheck(t *testing.T) {
	for _, kind := range []string{"", "conduct", "deal"} {
		id := packChecked(t, kind, dealRoleSeller)
		assert.Contains(t, receiptText(t, id, false), "under "+packID+", pack "+packShown, "kind %q", kind)
	}
}

// The seller's copy goes to the buyer, so the buyer's receipt reads the digest from the seller's
// half and names it as the seller's terms, never as a check the buyer ran.
func TestAPartysDealTermsReachTheCounterpartyAsTheirTerms(t *testing.T) {
	copyText := receiptText(t, packChecked(t, "deal", dealRoleSeller), true)
	assert.Contains(t, copyText, "under the seller's terms, pack "+packShown)
	assert.NotContains(t, copyText, "under "+packID, "the ruleset id is the owner's own wording")
	assert.NotContains(t, copyText, packDigest, "only the shown prefix")

	buyer := receiptText(t, packChecked(t, "deal", dealRoleBuyer), true)
	assert.Contains(t, buyer, "under the buyer's terms, pack "+packShown)
}

// Steven's PRD rule: a conduct pack never appears in a copy sent to a counterparty. Nor does a
// pack whose pin states no kind, or any other kind.
func TestAPackThatIsNotDealTermsNeverReachesTheCounterparty(t *testing.T) {
	for _, kind := range []string{"", "conduct", "admission", "live", "after_the_fact"} {
		id := packChecked(t, kind, dealRoleSeller)
		copyText := receiptText(t, id, true)
		for _, leak := range []string{packID, packShown, packDigest, "pack ", "terms, pack"} {
			assert.NotContains(t, copyText, leak, "kind %q", kind)
		}
		assert.True(t, strings.Contains(receiptText(t, id, false), packShown), "the owner's own copy still names it (kind %q)", kind)
	}
}

// The adjudicator rules by the deal's terms, so a deal-kind pack is named to them as it is to the
// counterparty; a pack that is not deal terms is not, and the ruleset id never is (#145).
func TestTheAdjudicatorCopyNamesADealPackAndNoOther(t *testing.T) {
	adjudicator := func(id string) string {
		page := filepath.Join(t.TempDir(), "adj.html")
		dealRun(t, "report", "--deal", id, "--html", page, "--share", "adjudicator", "--to", "a referee")
		raw, err := os.ReadFile(page)
		require.NoError(t, err)
		text, err := json.Marshal(dealReportOf(embeddedBundle(t, string(raw))))
		require.NoError(t, err)
		return string(text)
	}
	deal := adjudicator(packChecked(t, "deal", dealRoleSeller))
	// Right after the verdict, before the rest of the line (the rules declared but not measured).
	assert.Contains(t, deal, "no differences, under the seller's terms, pack "+packShown+" (you asked for")
	assert.Contains(t, deal, "not yet checked: r14, r24, r25")
	for _, leak := range []string{packID, packDigest} {
		assert.NotContains(t, deal, leak)
	}
	for _, kind := range []string{"", "conduct", "admission"} {
		other := adjudicator(packChecked(t, kind, dealRoleSeller))
		for _, leak := range []string{packID, packShown, packDigest, "terms, pack"} {
			assert.NotContains(t, other, leak, "kind %q", kind)
		}
	}
}

// A copy names the pack only while the check's digest is still one the profile pins as the party's
// deal terms: re-pinning another digest after the check stops the counterparty copy naming it, and
// the owner's copy still says what the check ran under.
func TestACounterpartyCopyDoesNotNameAPackThePinNoLongerHolds(t *testing.T) {
	id := packChecked(t, "deal", dealRoleSeller)
	other := strings.Repeat("c", 64)
	out := `printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"` + packID + `","definition_digest":"` + other + `","verdict":"allow","findings":[]}'`
	pinChecker(t, map[string]any{"command": []string{stubChecker(t, "pack2", out)}, "definition_digest": other, "kind": "deal"})
	assert.NotContains(t, receiptText(t, id, true), "terms, pack")
	assert.Contains(t, receiptText(t, id, false), "pack "+packShown)
}

// A pin by role: the seller's own digest is named on a seller's deal. The share decision reads the
// acting role's pin (ForRole), not the flat default: after a re-pin that leaves the digest in the flat
// default but gives the seller another one, it is no longer named.
func TestAPackPinnedPerRoleIsNamedOnlyForThatRole(t *testing.T) {
	flat := strings.Repeat("e", 64)
	id := packCheckedUnder(t, dealRoleSeller, func(pin map[string]any) {
		pin["kind"], pin["definition_digest"] = "deal", flat
		pin["roles"] = map[string]any{"seller": map[string]any{"pack": packID, "definition_digest": packDigest}}
	})
	assert.Contains(t, receiptText(t, id, true), "under the seller's terms, pack "+packShown)

	other := strings.Repeat("c", 64)
	out := `printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"` + packID + `","definition_digest":"` + other + `","verdict":"allow","findings":[]}'`
	pinChecker(t, map[string]any{"command": []string{stubChecker(t, "pack3", out)}, "kind": "deal", "definition_digests": []string{packDigest, other},
		"roles": map[string]any{"seller": map[string]any{"pack": packID, "definition_digest": other}}})
	assert.NotContains(t, receiptText(t, id, true), "terms, pack", "the flat default still holds the digest, the seller's own pin does not")
}

// The sealed check carries the pack's kind, and a copy reads that, not the pin as it is now
// (evidencebook-deal#146). Re-pinning the same pack as another kind after the sale changes
// no older copy, in either direction; a check sealed with no kind is never shared.
func TestACopyReadsTheKindTheCheckSealedNotTheCurrentPin(t *testing.T) {
	repin := func(kind string) {
		out := `printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"` + packID + `","definition_digest":"` + packDigest + `","verdict":"allow","findings":[]}'`
		pin := map[string]any{"command": []string{stubChecker(t, "repin", out)}, "definition_digest": packDigest}
		if kind != "" {
			pin["kind"] = kind
		}
		pinChecker(t, pin)
	}

	sold := packChecked(t, "deal", dealRoleSeller)
	repin("conduct")
	assert.Contains(t, receiptText(t, sold, true), "under the seller's terms, pack "+packShown, "sealed deal stays shared after a re-pin to conduct")

	private := packChecked(t, "conduct", dealRoleSeller)
	repin("deal")
	assert.NotContains(t, receiptText(t, private, true), "terms, pack", "sealed conduct stays private after a re-pin to deal")

	unkinded := packChecked(t, "", dealRoleSeller)
	repin("deal")
	copyText := receiptText(t, unkinded, true)
	for _, leak := range []string{packID, packShown, packDigest, "terms, pack"} {
		assert.NotContains(t, copyText, leak, "a check sealed without a kind is never shared, whatever the pin says now")
	}
	assert.Contains(t, receiptText(t, unkinded, false), packShown)
}

// Role pins carry no kind of their own: a role-pinned check seals the checker pin's kind.
func TestARolePinnedCheckSealsTheCheckerPinsKind(t *testing.T) {
	id := packCheckedUnder(t, dealRoleSeller, func(pin map[string]any) {
		pin["kind"] = "deal"
		pin["roles"] = map[string]any{"seller": map[string]any{"pack": packID, "definition_digest": packDigest}}
	})
	assert.Contains(t, receiptText(t, id, true), "under the seller's terms, pack "+packShown)

	id = packCheckedUnder(t, dealRoleSeller, func(pin map[string]any) {
		pin["kind"] = "conduct"
		pin["roles"] = map[string]any{"seller": map[string]any{"pack": packID, "definition_digest": packDigest}}
	})
	assert.NotContains(t, receiptText(t, id, true), "terms, pack")
}
