package xdealv1

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

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

// soStamp is the stamped time of the ruling in these tests.
var soStamp = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

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
			assert.Equal(t, NotIndependentSharedOperator, SharedOperator(soRuling(soRulingAt), ag, soStamp, [][]byte{b}, nil))
		})
	}
	t.Run("record is taken by pointer too", func(t *testing.T) {
		r := soRuling(soRulingAt)
		body := r.Body.(RulingBody)
		r.Body = &body
		b := soJCS(t, soBinding(t, soAdj, soBuyer))
		assert.Equal(t, NotIndependentSharedOperator, SharedOperator(r, ag, soStamp, [][]byte{b}, nil))
	})
}

// Acceptance 1: the binding is judged at the ruling's stamped time, not at
// the block.at the adjudicator wrote.
func TestSharedOperatorJudgedAtStampedTime(t *testing.T) {
	ag := soAgreement(soAdj.id)
	rec := soBinding(t, soAdj, soBuyer)
	b := soJCS(t, rec)
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return v
	}
	t.Run("a revocation after the stamp keeps the label", func(t *testing.T) {
		rev := soRevocation(t, soBuyer, rec, "2026-11-01T00:00:00Z")
		assert.Equal(t, NotIndependentSharedOperator, SharedOperator(soRuling(soRulingAt), ag, soStamp, [][]byte{b}, [][]byte{rev}))
	})
	t.Run("a revocation before the stamp removes it", func(t *testing.T) {
		rev := soRevocation(t, soAdj, rec, "2026-10-10T00:00:00Z")
		assert.Empty(t, SharedOperator(soRuling(soRulingAt), ag, soStamp, [][]byte{b}, [][]byte{rev}))
	})
	t.Run("a revocation at exactly the stamp removes it", func(t *testing.T) {
		rev := soRevocation(t, soAdj, rec, soRulingAt)
		assert.Empty(t, SharedOperator(soRuling(soRulingAt), ag, soStamp, [][]byte{b}, [][]byte{rev}))
	})
	t.Run("a stamp before the window shows nothing", func(t *testing.T) {
		assert.Empty(t, SharedOperator(soRuling(soRulingAt), ag, at("2026-09-01T00:00:00Z"), [][]byte{b}, nil))
	})
	t.Run("a stamp after the window shows nothing", func(t *testing.T) {
		assert.Empty(t, SharedOperator(soRuling(soRulingAt), ag, at("2027-11-01T00:00:00Z"), [][]byte{b}, nil))
	})
	t.Run("block.at outside the window does not escape the label", func(t *testing.T) {
		for _, author := range []string{"2026-09-01T00:00:00Z", "2027-11-01T00:00:00Z", "not a time", ""} {
			assert.Equal(t, NotIndependentSharedOperator, SharedOperator(soRuling(author), ag, soStamp, [][]byte{b}, nil), author)
		}
	})
	t.Run("block.at inside the window does not rescue a stamp outside it", func(t *testing.T) {
		assert.Empty(t, SharedOperator(soRuling(soRulingAt), ag, at("2027-11-01T00:00:00Z"), [][]byte{b}, nil))
	})
	t.Run("a ruling no stamp covers yet shows nothing", func(t *testing.T) {
		assert.Empty(t, SharedOperator(soRuling(soRulingAt), ag, time.Time{}, [][]byte{b}, nil))
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
		assert.Empty(t, SharedOperator(ruling, ag, soStamp, [][]byte{soJCS(t, rec)}, nil))
	})
	t.Run("signed by only one key", func(t *testing.T) {
		rec := soBinding(t, soAdj, soBuyer)
		rec["sigs"].([]any)[1].(map[string]any)["key_id"] = soOther.id
		assert.Empty(t, SharedOperator(ruling, ag, soStamp, [][]byte{soJCS(t, rec)}, nil))
	})
	t.Run("unknown method (NOT_CHECKED) shows nothing", func(t *testing.T) {
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
		assert.Empty(t, SharedOperator(ruling, ag, soStamp, [][]byte{soJCS(t, rec)}, nil))
	})
	t.Run("not JSON", func(t *testing.T) {
		assert.Empty(t, SharedOperator(ruling, ag, soStamp, [][]byte{[]byte("nope")}, nil))
	})
	t.Run("another claim, delegated_by, fully valid", func(t *testing.T) {
		rec := soBinding(t, soAdj, soBuyer)
		delete(rec, "sigs")
		rec["claim"] = controllerbinding.ClaimDelegatedBy
		rec["delegator"] = soAdj.id
		first, second := soAdj, soBuyer
		if first.id > second.id {
			first, second = second, first
		}
		rec["sigs"] = []any{
			map[string]any{"key_id": first.id, "sig": first.sign(t, rec)},
			map[string]any{"key_id": second.id, "sig": second.sign(t, rec)},
		}
		// The binding library applies it to the pair; only the claim filter excludes it.
		require.True(t, controllerbinding.Verify(soJCS(t, rec), []time.Time{soStamp}, controllerbinding.Options{}).AppliesTo(soAdj.id, soBuyer.id))
		assert.Empty(t, SharedOperator(ruling, ag, soStamp, [][]byte{soJCS(t, rec)}, nil))
	})
}

// Shown nothing when no valid binding joins the adjudicator to a party, and
// never a positive state.
func TestSharedOperatorNothingOtherwise(t *testing.T) {
	ag := soAgreement(soAdj.id)
	ruling := soRuling(soRulingAt)
	valid := soJCS(t, soBinding(t, soAdj, soBuyer))
	assert.Empty(t, SharedOperator(ruling, ag, soStamp, nil, nil), "no bindings")
	assert.Empty(t, SharedOperator(ruling, ag, soStamp, [][]byte{soJCS(t, soBinding(t, soBuyer, soSeller))}, nil), "binding between the parties only")
	assert.Empty(t, SharedOperator(ruling, ag, soStamp, [][]byte{soJCS(t, soBinding(t, soAdj, soOther))}, nil), "binding to a third key")
	assert.Empty(t, SharedOperator(ruling, soAgreement(soOther.id), soStamp, [][]byte{valid}, nil), "another adjudicator")
	assert.Equal(t, NotIndependentSharedOperator, SharedOperator(ruling, soAgreement(soBuyer.id), soStamp, nil, nil), "adjudicator key is the buyer's")
	assert.Equal(t, NotIndependentSharedOperator, SharedOperator(ruling, soAgreement(soSeller.id), soStamp, nil, nil), "adjudicator key is the seller's")
	assert.Empty(t, SharedOperator(ruling, AgreeBody{}, soStamp, [][]byte{valid}, nil), "no adjudicator member")
	assert.Empty(t, SharedOperator(ruling, AgreeBody{Adjudicator: NullOf[Adjudicator]()}, soStamp, [][]byte{valid}, nil), "null adjudicator")
	assert.Empty(t, SharedOperator(Record{Block: Block{At: soRulingAt}, Body: DisputeBody{}}, ag, soStamp, [][]byte{valid}, nil), "not a ruling")
	assert.Equal(t, NotIndependentSharedOperator, SharedOperator(ruling, ag, soStamp, [][]byte{[]byte("x"), valid}, nil), "one valid among bad ones")
}

// An adjudicator key equal to a party key is one operator at every time: it
// is labeled with no binding and with no stamped time, which only a
// binding's window needs (#822 review).
func TestSharedOperatorSameKeyNeedsNoTime(t *testing.T) {
	for _, party := range []soKey{soBuyer, soSeller} {
		ag := soAgreement(party.id)
		assert.Equal(t, NotIndependentSharedOperator, SharedOperator(soRuling(soRulingAt), ag, soStamp, nil, nil))
		assert.Equal(t, NotIndependentSharedOperator, SharedOperator(soRuling(soRulingAt), ag, time.Time{}, nil, nil))
	}
	// A binding still needs the time.
	b := soJCS(t, soBinding(t, soAdj, soBuyer))
	assert.Empty(t, SharedOperator(soRuling(soRulingAt), soAgreement(soAdj.id), time.Time{}, [][]byte{b}, nil))
}
