package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/controllerbinding"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selectedKey is the selected profile's record key.
func selectedKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	k, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	return k
}

func jcs(t *testing.T, v any) []byte {
	t.Helper()
	data, err := evidencerequest.EncodeJCS(v)
	require.NoError(t, err)
	return data
}

// bindingFile writes a same_controller_asserted record over two keys, signed
// by both, valid from 2020 to 2099, and returns its path and the record.
func bindingFile(t *testing.T, a, b ed25519.PrivateKey) (string, map[string]any) {
	t.Helper()
	keys := []ed25519.PrivateKey{a, b}
	id := func(k ed25519.PrivateKey) string { return hex.EncodeToString(k.Public().(ed25519.PublicKey)) }
	if id(keys[0]) > id(keys[1]) {
		keys[0], keys[1] = keys[1], keys[0]
	}
	r := map[string]any{
		"binding_version": controllerbinding.Version, "claim": controllerbinding.ClaimSameController,
		"method": controllerbinding.MethodDualSignature, "keys": []any{id(keys[0]), id(keys[1])},
		"issued_at": "2020-01-01T00:00:00Z", "not_before": "2020-01-01T00:00:00Z",
		"not_after": "2099-01-01T00:00:00Z", "revocation": controllerbinding.RevocationMethod, "nonce": "n1",
	}
	sig := func(k ed25519.PrivateKey) string { return hex.EncodeToString(ed25519.Sign(k, jcs(t, r))) }
	r["sigs"] = []any{map[string]any{"key_id": id(keys[0]), "sig": sig(keys[0])}, map[string]any{"key_id": id(keys[1]), "sig": sig(keys[1])}}
	path := filepath.Join(t.TempDir(), "binding.json")
	require.NoError(t, os.WriteFile(path, jcs(t, r), 0o600))
	return path, r
}

// revocationFile writes a revocation of binding by key k at the time at.
func revocationFile(t *testing.T, k ed25519.PrivateKey, binding map[string]any, at time.Time) string {
	t.Helper()
	unsigned := map[string]any{}
	for name, v := range binding {
		if name != "sigs" {
			unsigned[name] = v
		}
	}
	s := map[string]any{"revocation_version": controllerbinding.RevocationVersion, "binding": evidencerequest.Digest(jcs(t, unsigned)),
		"key_id": hex.EncodeToString(k.Public().(ed25519.PublicKey)), "revoked_at": at.UTC().Format(time.RFC3339)}
	s["sig"] = hex.EncodeToString(ed25519.Sign(k, jcs(t, s)))
	path := filepath.Join(t.TempDir(), "revocation.json")
	require.NoError(t, os.WriteFile(path, jcs(t, s), 0o600))
	return path
}

// A party's deal receive of a ruling says when it was stamped, from the
// adjudicator's receipts from a witness the agreement names, and labels it
// "not independent (shared operator)" when a binding, the party's own or
// the push's, ties the adjudicator's key to a party's at that time. Only
// the party's own revocation counts; with no named receipt the time is
// "unknown" and nothing is labeled (#780, #819).
func TestARulingIsLabeledNotIndependentByABinding(t *testing.T) {
	public, wkey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyID := hex.EncodeToString(public)
	endpoint, _ := countingWitness(t)
	adj := newAdjudicatorSide(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.Checkpoint.Endpoint, p.Checkpoint.PublicKey = endpoint, keyID
	require.NoError(t, cli.SaveProfile(p, true))
	adjKey := selectedKey(t)
	h := agreedBoth(t, func(k string) string {
		o := strings.Replace(disputeOffer(k, "auto:twin"), `"key_id": "adjudicator-key"`, `"key_id": "`+adj.party.signer+`"`, 1)
		return strings.Replace(o, `"witnesses": []`, `"witnesses": [{"key_id": "`+keyID+`"}]`, 1)
	})
	h.buyer.act(t)
	buyerKey := selectedKey(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerCopy := adjudicatorCopy(t, h.buyer, h.dealID)

	adj.act(t)
	ruling := filepath.Join(t.TempDir(), "ruling.json")
	args := []string{"ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
		"--dispute", dispute, "--copy", buyerCopy, "--out", ruling}
	dealRun(t, args...)
	// Before any witness receipt: the seller takes the ruling, whose time is
	// unknown, and nothing is labeled.
	h.seller.act(t)
	early := dealRun(t, "receive", ruling)
	assert.Equal(t, stampedAtUnknown, early["stamped_at"])
	assert.NotContains(t, early, "independence")

	// The named witness registers the adjudicator's stamp; the ruling run
	// again writes the file with that receipt.
	adj.act(t)
	iat := cli.DealClock().Add(time.Minute).UTC().Truncate(time.Second)
	witnessLastStamp(t, endpoint, wkey, iat)
	assert.Equal(t, false, dealRun(t, args...)["sealed"])
	var push v1Push
	require.NoError(t, json.Unmarshal(mustRead(t, ruling), &push))
	require.Len(t, push.WitnessReceipts, 1)

	// The buyer brings a binding between the adjudicator's key and its own.
	binding, record := bindingFile(t, adjKey, buyerKey)
	h.buyer.act(t)
	labeled := dealRun(t, "receive", ruling, "--binding", binding)
	assert.Equal(t, iat.Format(time.RFC3339), labeled["stamped_at"])
	assert.Equal(t, xdealv1.NotIndependentSharedOperator, labeled["independence"])
	// Without it, nothing: there is no positive state.
	plain := dealRun(t, "receive", ruling)
	assert.Equal(t, iat.Format(time.RFC3339), plain["stamped_at"])
	assert.NotContains(t, plain, "independence")
	// A revocation the buyer gives, dated before the stamp, removes it; one
	// dated after does not.
	before := revocationFile(t, adjKey, record, iat.Add(-time.Hour))
	assert.NotContains(t, dealRun(t, "receive", ruling, "--binding", binding, "--revocation", before), "independence")
	after := revocationFile(t, adjKey, record, iat.Add(time.Hour))
	assert.Equal(t, xdealv1.NotIndependentSharedOperator, dealRun(t, "receive", ruling, "--binding", binding, "--revocation", after)["independence"])

	// A binding the adjudicator delivers in its own push counts too.
	adj.act(t)
	withBinding := append(append([]string{}, args[:len(args)-2]...), "--binding", binding, "--out", filepath.Join(t.TempDir(), "ruling-b.json"))
	pushed := dealRun(t, withBinding...)["file"].(string)
	h.seller.act(t)
	assert.Equal(t, xdealv1.NotIndependentSharedOperator, dealRun(t, "receive", pushed)["independence"])

	// --binding goes with a ruling only.
	assert.Contains(t, v1Err(t, "receive", dispute, "--binding", binding), "go with a ruling")
}
