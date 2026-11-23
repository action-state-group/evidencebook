package xdealv1

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/controllerbinding"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type soKey struct {
	id   string
	priv ed25519.PrivateKey
}

func newSOKey(label string) soKey {
	seed := sha256.Sum256([]byte("shared operator test " + label))
	priv := ed25519.NewKeyFromSeed(seed[:])
	return soKey{hex.EncodeToString(priv.Public().(ed25519.PublicKey)), priv}
}

func (k soKey) sign(t *testing.T, v map[string]any) string {
	t.Helper()
	data, err := evidencerequest.EncodeJCS(v)
	require.NoError(t, err)
	return hex.EncodeToString(ed25519.Sign(k.priv, data))
}

func soJCS(t *testing.T, v any) []byte {
	t.Helper()
	data, err := evidencerequest.EncodeJCS(v)
	require.NoError(t, err)
	return data
}

var soAdj, soBuyer, soSeller, soOther = newSOKey("adj"), newSOKey("buyer"), newSOKey("seller"), newSOKey("other")

const soRulingAt = "2026-10-15T00:00:00Z"

// soBinding is a same_controller_asserted record over a and b, signed by both,
// valid from 2026-10-01 to 2027-10-01.
func soBinding(t *testing.T, a, b soKey) map[string]any {
	t.Helper()
	first, second := a, b
	if first.id > second.id {
		first, second = second, first
	}
	r := map[string]any{
		"binding_version": controllerbinding.Version, "claim": controllerbinding.ClaimSameController,
		"method": controllerbinding.MethodDualSignature, "keys": []any{first.id, second.id},
		"issued_at": "2026-10-01T00:00:00Z", "not_before": "2026-10-01T00:00:00Z",
		"not_after": "2027-10-01T00:00:00Z", "revocation": controllerbinding.RevocationMethod, "nonce": "n1",
	}
	r["sigs"] = []any{
		map[string]any{"key_id": first.id, "sig": first.sign(t, r)},
		map[string]any{"key_id": second.id, "sig": second.sign(t, r)},
	}
	return r
}

func soRevocation(t *testing.T, k soKey, binding map[string]any, at string) []byte {
	t.Helper()
	unsigned := map[string]any{}
	for name, v := range binding {
		if name != "sigs" {
			unsigned[name] = v
		}
	}
	s := map[string]any{"revocation_version": controllerbinding.RevocationVersion,
		"binding": evidencerequest.Digest(soJCS(t, unsigned)), "key_id": k.id, "revoked_at": at}
	s["sig"] = k.sign(t, s)
	return soJCS(t, s)
}

func soRuling(at string) Record {
	return Record{
		Block: Block{At: at},
		Body:  RulingBody{Keys: RulingKeys{Buyer: soBuyer.id, Seller: soSeller.id}},
	}
}

func soAgreement(adj string) AgreeBody {
	return AgreeBody{Adjudicator: Some(Adjudicator{KeyID: adj})}
}

func TestSharedOperator(t *testing.T) {
	ag := soAgreement(soAdj.id)
	for name, party := range map[string]soKey{"buyer": soBuyer, "seller": soSeller} {
		t.Run("valid binding with the "+name, func(t *testing.T) {
			b := soJCS(t, soBinding(t, soAdj, party))
			assert.Equal(t, NotIndependentSharedOperator, SharedOperator(soRuling(soRulingAt), ag, [][]byte{b}, nil))
		})
	}
	t.Run("record is taken by pointer too", func(t *testing.T) {
		r := soRuling(soRulingAt)
		body := r.Body.(RulingBody)
		r.Body = &body
		b := soJCS(t, soBinding(t, soAdj, soBuyer))
		assert.Equal(t, NotIndependentSharedOperator, SharedOperator(r, ag, [][]byte{b}, nil))
	})
}

// Acceptance 1: the binding is judged at the ruling's stamped time.
func TestSharedOperatorJudgedAtRulingTime(t *testing.T) {
	ag := soAgreement(soAdj.id)
	rec := soBinding(t, soAdj, soBuyer)
	b := soJCS(t, rec)
	t.Run("a revocation after the ruling keeps the label", func(t *testing.T) {
		rev := soRevocation(t, soBuyer, rec, "2026-11-01T00:00:00Z")
		assert.Equal(t, NotIndependentSharedOperator, SharedOperator(soRuling(soRulingAt), ag, [][]byte{b}, [][]byte{rev}))
	})
	t.Run("a revocation before the ruling removes it", func(t *testing.T) {
		rev := soRevocation(t, soAdj, rec, "2026-10-10T00:00:00Z")
		assert.Empty(t, SharedOperator(soRuling(soRulingAt), ag, [][]byte{b}, [][]byte{rev}))
	})
	t.Run("a ruling before the window shows nothing", func(t *testing.T) {
		assert.Empty(t, SharedOperator(soRuling("2026-09-01T00:00:00Z"), ag, [][]byte{b}, nil))
	})
	t.Run("a ruling after the window shows nothing", func(t *testing.T) {
		assert.Empty(t, SharedOperator(soRuling("2027-11-01T00:00:00Z"), ag, [][]byte{b}, nil))
	})
}

// Acceptance 2: only a valid binding counts.
func TestSharedOperatorOnlyValidBinding(t *testing.T) {
	ag := soAgreement(soAdj.id)
	ruling := soRuling(soRulingAt)
	t.Run("bad signature", func(t *testing.T) {
		rec := soBinding(t, soAdj, soBuyer)
		sigs := rec["sigs"].([]any)
		sigs[0].(map[string]any)["sig"] = hex.EncodeToString(make([]byte, 64))
		assert.Empty(t, SharedOperator(ruling, ag, [][]byte{soJCS(t, rec)}, nil))
	})
	t.Run("signed by only one key", func(t *testing.T) {
		rec := soBinding(t, soAdj, soBuyer)
		rec["sigs"].([]any)[1].(map[string]any)["key_id"] = soOther.id
		assert.Empty(t, SharedOperator(ruling, ag, [][]byte{soJCS(t, rec)}, nil))
	})
	t.Run("unknown method is INCONCLUSIVE", func(t *testing.T) {
		rec := soBinding(t, soAdj, soBuyer)
		delete(rec, "sigs")
		rec["method"] = "dual_signature/9"
		first, second := soAdj, soBuyer
		if first.id > second.id {
			first, second = second, first
		}
		rec["sigs"] = []any{
			map[string]any{"key_id": first.id, "sig": first.sign(t, rec)},
			map[string]any{"key_id": second.id, "sig": second.sign(t, rec)},
		}
		assert.Empty(t, SharedOperator(ruling, ag, [][]byte{soJCS(t, rec)}, nil))
	})
	t.Run("not JSON", func(t *testing.T) {
		assert.Empty(t, SharedOperator(ruling, ag, [][]byte{[]byte("nope")}, nil))
	})
	t.Run("another claim", func(t *testing.T) {
		rec := soBinding(t, soAdj, soBuyer)
		delete(rec, "sigs")
		rec["claim"] = controllerbinding.ClaimSameOrganization
		first, second := soAdj, soBuyer
		if first.id > second.id {
			first, second = second, first
		}
		rec["sigs"] = []any{
			map[string]any{"key_id": first.id, "sig": first.sign(t, rec)},
			map[string]any{"key_id": second.id, "sig": second.sign(t, rec)},
		}
		assert.Empty(t, SharedOperator(ruling, ag, [][]byte{soJCS(t, rec)}, nil))
	})
}

// Shown nothing when no valid binding joins the adjudicator to a party, and
// never a positive state.
func TestSharedOperatorNothingOtherwise(t *testing.T) {
	ag := soAgreement(soAdj.id)
	ruling := soRuling(soRulingAt)
	valid := soJCS(t, soBinding(t, soAdj, soBuyer))
	assert.Empty(t, SharedOperator(ruling, ag, nil, nil), "no bindings")
	assert.Empty(t, SharedOperator(ruling, ag, [][]byte{soJCS(t, soBinding(t, soBuyer, soSeller))}, nil), "binding between the parties only")
	assert.Empty(t, SharedOperator(ruling, ag, [][]byte{soJCS(t, soBinding(t, soAdj, soOther))}, nil), "binding to a third key")
	assert.Empty(t, SharedOperator(ruling, soAgreement(soOther.id), [][]byte{valid}, nil), "another adjudicator")
	assert.Empty(t, SharedOperator(ruling, AgreeBody{}, [][]byte{valid}, nil), "no adjudicator member")
	assert.Empty(t, SharedOperator(ruling, AgreeBody{Adjudicator: NullOf[Adjudicator]()}, [][]byte{valid}, nil), "null adjudicator")
	assert.Empty(t, SharedOperator(soRuling("not a time"), ag, [][]byte{valid}, nil), "unreadable ruling time")
	assert.Empty(t, SharedOperator(Record{Block: Block{At: soRulingAt}, Body: DisputeBody{}}, ag, [][]byte{valid}, nil), "not a ruling")
	assert.Equal(t, NotIndependentSharedOperator, SharedOperator(ruling, ag, [][]byte{[]byte("x"), valid}, nil), "one valid among bad ones")
}
