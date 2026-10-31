package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	aaccanonical "github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/capsule-emit/go/artifact"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A SYNTHETIC Mesh composition, shaped exactly as the design note's D4 says
// (docs/design/two-sided-receipt.md, "D4. A Mesh half as their member"), until
// the plugin builds the real one (Mesh-LLM/capsules-plugin). Nothing here is
// Mesh's own output: the payer's x-deal-v0 steps are capsulectl-deal's, and
// the x-mesh-poc-v1 halves carry only the members the page reads
// (model_attestation.compute_attestation["x-mesh-poc-v1"].client_nonce and
// client_nonce_source, and effect.request_digest).
type meshHalf struct {
	nonce, source, requestDigest string
}

// meshHalfRecord builds and signs one x-mesh-poc-v1 half under key.
func meshHalfRecord(t *testing.T, key ed25519.PrivateKey, role string, h meshHalf, at time.Time) artifact.Record {
	t.Helper()
	return meshRecord(t, key, "mesh-exchange/"+role, h.requestDigest, "x-mesh-poc-v1", map[string]any{"client_nonce": h.nonce, "client_nonce_source": h.source, "role": role}, at)
}

// meshCitingRecord is the payer's citing record for a received half: its
// compute attestation's received_half block (Mesh-LLM/capsules-plugin
// producer/capsule.rs seal_citing_record), with only the members the page
// reads.
func meshCitingRecord(t *testing.T, key ed25519.PrivateKey, cited, sender string, sameController bool, at time.Time) artifact.Record {
	t.Helper()
	block := map[string]any{"cited_capsule_id": cited, "sender_key_id": sender, "signature_ok": true}
	if sameController {
		block["same_controller_as_receiver"] = true
	}
	return meshRecord(t, key, "mesh-poc/received-half-citation/"+cited, cli.BytesDigest([]byte(cited)), "received_half", block, at)
}

// meshRecord builds and signs a Mesh-shaped record under key: an fyi Capsule
// with an effect binding the request, whose compute attestation carries
// block under blockName. The block is added after Build and the Capsule's id
// computed again by AAC's own rule (canonical.ComputeCapsuleID).
func meshRecord(t *testing.T, key ed25519.PrivateKey, actionID, requestDigest, blockName string, block map[string]any, at time.Time) artifact.Record {
	t.Helper()
	built, err := emit.Build(emit.Input{
		ActionID: actionID, ActionType: emit.ActionTypeFYI, Operator: "synthetic-mesh-node", Developer: "synthetic (two-sided-receipt.md D4)",
		Timestamp: at,
		Effect:    &emit.Effect{Type: "inference", Status: emit.EffectDispatched, IrreversibilityClass: emit.IrreversibilityTwoWay, EffectAttestation: emit.AttestationRuntimeClaimed, RequestDigest: requestDigest},
		Compute:   &emit.ComputeAttestation{AgentInputDigest: requestDigest, Runtime: "synthetic"},
	})
	require.NoError(t, err)
	value := built.Value
	delete(value, "capsule_id")
	compute := value["model_attestation"].(map[string]any)["compute_attestation"].(map[string]any)
	compute[blockName] = block
	id, err := aaccanonical.ComputeCapsuleID(value)
	require.NoError(t, err)
	value["capsule_id"] = id
	encoded, err := aaccanonical.JCS(value)
	require.NoError(t, err)
	identity, err := emit.NewEd25519SigningIdentity(key)
	require.NoError(t, err)
	envelope, err := emit.Sign(emit.BuiltPayload{CapsuleID: id, Value: value, JSON: encoded}, identity)
	require.NoError(t, err)
	return artifact.Record{CapsuleID: id, Capsule: encoded, ProducerEnvelope: envelope}
}

// meshLedger is one node's single ledger: a fresh log of this profile's
// store holding records, checkpointed under the node's own key (on Mesh the
// checkpoint key is the announced signing key), and the bundle of it rooted
// at root, every retained payload disclosed.
// Records sealed under another key are stored when that key is in also.
func meshLedger(t *testing.T, key ed25519.PrivateKey, records []artifact.Record, root string, also ...ed25519.PrivateKey) map[string]any {
	t.Helper()
	p := dealProfile(t)
	p.LogID = "synthetic-mesh/" + hex.EncodeToString(key.Public().(ed25519.PublicKey))[:16]
	p.Namespace = "synthetic-mesh"
	p.Log = cli.LogCLL
	// The node's own store, not this profile's deal store, which the deal
	// plugin owns.
	p.Connection.Database = filepath.Join(t.TempDir(), "node.db")
	seed := filepath.Join(t.TempDir(), "node.seed")
	require.NoError(t, os.WriteFile(seed, []byte(hex.EncodeToString(key.Seed())), 0o600))
	p.Signing = cli.Secret{File: seed}
	p.Checkpoint.Signing = cli.Secret{File: seed}
	p.TrustedKeys = append(p.TrustedKeys, hex.EncodeToString(key.Public().(ed25519.PublicKey)))
	p.Checkpoint.TrustedKeys = append(p.Checkpoint.TrustedKeys, hex.EncodeToString(key.Public().(ed25519.PublicKey)))
	for _, k := range also {
		p.TrustedKeys = append(p.TrustedKeys, hex.EncodeToString(k.Public().(ed25519.PublicKey)))
	}
	target, err := cli.OpenTarget(t.Context(), p, cli.UseInitialization)
	require.NoError(t, err)
	defer func() { _ = target.Close() }()
	for _, r := range records {
		require.NoError(t, target.Artifacts.Put(t.Context(), r))
		raw, err := hex.DecodeString(r.CapsuleID)
		require.NoError(t, err)
		_, err = target.Log.Append(t.Context(), cll.AppendInput{Value: raw, AppendedAt: time.Now().UTC()})
		require.NoError(t, err)
	}
	_, err = cli.CutCheckpoint(t.Context(), localOnly(p), target.Log)
	require.NoError(t, err)
	b, err := cli.AssembleBundle(t.Context(), target.Artifacts, target.Log, p.LogID, cli.BundleOptions{Root: root, Payloads: "selected", WithDisclosure: true})
	require.NoError(t, err)
	return b
}

// meshPayer is the payer's side of one paid exchange: its own deal (sealed
// by capsulectl-deal, its records copied as signed), its exchange half, and
// a keep report whose did[].steps cite that half.
type meshPayer struct {
	key    ed25519.PrivateKey
	dealID string
	steps  []artifact.Record
	report map[string]any // the sealed report's payload
}

func newMeshPayer(t *testing.T) meshPayer {
	t.Helper()
	inLogReports(t)
	dealFixture(t)
	id := retailDeal(t)
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, received))
	keepPath := filepath.Join(t.TempDir(), "keep.json")
	dealRun(t, "report", "--deal", id, "--bundle", keepPath)
	keep := exactBundle(t, string(mustRead(t, keepPath)))
	sealed := keep["extensions"].(map[string]any)["x-deal-v0"].(map[string]any)["sealed_report"].(string)
	p := dealProfile(t)
	key, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	payer := meshPayer{key: key, dealID: id}
	payer.report = keep["disclosures"].(map[string]any)[sealed].(map[string]any)["agent_input"].(map[string]any)
	withSession(t, func(s *dealSession) {
		lp := s.p
		lp.LogID = cli.DealLogID(id)
		target, err := openOwnedTarget(t.Context(), lp, cli.UseRead, s)
		require.NoError(t, err)
		defer func() { _ = target.Close() }()
		for _, raw := range keep["records"].([]any) {
			r := raw.(map[string]any)
			if r["action_id"] == "capsulectl-deal-report" {
				continue
			}
			record, err := target.Artifacts.Get(t.Context(), r["capsule_id"].(string))
			require.NoError(t, err)
			payer.steps = append(payer.steps, record)
		}
	})
	return payer
}

// meshComposition is the payer's composition (D4): the payer ledger holds
// its deal steps, its exchange half, its keep report citing that half, and
// the composition record, which is the root; the provider's member is the
// provider's own ledger rooted at its half, with its signed response.
// meshOptions vary the synthetic composition: the payer's citing record for
// the provider's half, a response in neither form V3 reads, a second member
// (from the same provider, or another), the provider's custody declared as
// the payer's key, a report that cites no payer half, a report sealed under
// another key, and a root that is not a deal composition.
type meshOptions struct {
	citing         func(theirs artifact.Record, provider string) *artifact.Record
	otherForm      bool
	second         *meshHalf
	secondProvider bool
	custodyIsPayer bool
	uncited        bool
	reportKey      ed25519.PrivateKey
	rootActionID   string
}

type ed25519Signer struct{ key ed25519.PrivateKey }

func (s ed25519Signer) Sign(_ context.Context, message []byte) ([]byte, error) {
	return ed25519.Sign(s.key, message), nil
}

// meshMember is one provider answer: the provider's own ledger rooted at its
// served half, answered under the evidence-interop profile (§6.2), whose
// envelope names the anchor by the digest of the checkpoint's COSE octets;
// the member carries it as received, minus the artifact.
func meshMember(t *testing.T, providerKey ed25519.PrivateKey, half meshHalf, id string, otherForm bool) (map[string]any, artifact.Record) {
	t.Helper()
	provider := hex.EncodeToString(providerKey.Public().(ed25519.PublicKey))
	theirHalf := meshHalfRecord(t, providerKey, "served", half, time.Date(2026, 10, 9, 16, 10, 0, 0, time.UTC))
	memberBundle := meshLedger(t, providerKey, []artifact.Record{theirHalf}, theirHalf.CapsuleID)
	digest, err := aacbundle.BundleDigest(memberBundle)
	require.NoError(t, err)
	cp := memberBundle["checkpoint"].(map[string]any)
	requestDigest := cli.BytesDigest([]byte("synthetic ask " + id))
	cose, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(cp["cose"].(string), "="))
	require.NoError(t, err)
	profile := "evidence-interop/1"
	if otherForm {
		profile = "example-response/1"
	}
	envelope := evidencerequest.Envelope{
		Profile: profile, RequestDigest: requestDigest,
		Subject: evidencerequest.Subject{Kind: evidencerequest.SubjectRecord, Digest: theirHalf.CapsuleID},
		Anchor:  evidencerequest.Anchor{Digest: cli.BytesDigest(cose)}, ArtifactDigest: digest, Responder: provider, IssuedAt: "2026-10-09T16:20:00Z",
	}
	envelope, err = envelope.Sign(t.Context(), ed25519Signer{providerKey})
	require.NoError(t, err)
	response := envelope.Value()
	delete(response, "artifact")
	return map[string]any{
		"id": id, "observer": provider, "request_digest": requestDigest, "outcome": "artifact", "digest": digest, "bundle": memberBundle,
		"response": response,
	}, theirHalf
}

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return key
}

func meshComposition(t *testing.T, payer meshPayer, own, theirs meshHalf, opts meshOptions) map[string]any {
	t.Helper()
	at := time.Date(2026, 10, 9, 16, 10, 0, 0, time.UTC)
	ownHalf := meshHalfRecord(t, payer.key, "requested", own, at)
	providerKey := newKey(t)
	provider := hex.EncodeToString(providerKey.Public().(ed25519.PublicKey))
	payerKey := hex.EncodeToString(payer.key.Public().(ed25519.PublicKey))
	member, theirHalf := meshMember(t, providerKey, theirs, strings.Repeat("ab", 32), opts.otherForm)
	custody := provider
	if opts.custodyIsPayer {
		custody = payerKey
	}
	members := []any{member}
	observers := []any{map[string]any{"id": provider, "role": dealObserverRole, "custody_domain": custody}}
	if opts.second != nil {
		key := providerKey
		if opts.secondProvider {
			key = newKey(t)
			other := hex.EncodeToString(key.Public().(ed25519.PublicKey))
			observers = append(observers, map[string]any{"id": other, "role": dealObserverRole, "custody_domain": other})
		}
		second, _ := meshMember(t, key, *opts.second, strings.Repeat("cd", 32), false)
		members = append(members, second)
	}
	block := map[string]any{"members": members, "observers": observers, "joins": []any{}}
	composed, err := composedDigest(block)
	require.NoError(t, err)
	block["composed_digest"] = composed

	// The keep report, sealed again with its last did item citing the
	// payer's own exchange half (deal_flow seals it that way on Mesh).
	report := map[string]any{}
	raw, err := json.Marshal(payer.report)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &report))
	if !opts.uncited {
		did := report["report"].(map[string]any)["did"].([]any)
		last := did[len(did)-1].(map[string]any)
		steps, _ := last["steps"].([]any)
		last["steps"] = append(steps, ownHalf.CapsuleID)
	}
	reportKey := payer.key
	if opts.reportKey != nil {
		reportKey = opts.reportKey
	}
	reportRecord := sealSynthetic(t, reportKey, "capsulectl-deal-report", report)
	rootAction := dealCompositionActionID
	if opts.rootActionID != "" {
		rootAction = opts.rootActionID
	}
	ids := []any{}
	for _, m := range members {
		ids = append(ids, m.(map[string]any)["id"])
	}
	composition := sealSynthetic(t, payer.key, rootAction, map[string]any{
		"type": "deal_composition", "profile": "x-deal-v0", "deal_id": payer.dealID, "composed_digest": composed, "request_records": ids,
	})
	records := append(append([]artifact.Record{}, payer.steps...), ownHalf)
	if opts.citing != nil {
		if c := opts.citing(theirHalf, provider); c != nil {
			records = append(records, *c)
		}
	}
	records = append(records, reportRecord, composition)
	var also []ed25519.PrivateKey
	if opts.reportKey != nil {
		also = append(also, opts.reportKey)
	}
	b := meshLedger(t, payer.key, records, composition.CapsuleID, also...)
	require.Equal(t, payerKey, b["checkpoint"].(map[string]any)["key_id"], "the payer's checkpoint key is its signing key")
	extensions, _ := b["extensions"].(map[string]any)
	if extensions == nil {
		extensions = map[string]any{}
	}
	extensions["x-deal-v0"] = map[string]any{"sealed_report": reportRecord.CapsuleID}
	extensions["composed/v1"] = block
	b["extensions"] = extensions
	return b
}

func sealSynthetic(t *testing.T, key ed25519.PrivateKey, actionID string, payload map[string]any) artifact.Record {
	t.Helper()
	generic, err := genericJSON(payload)
	require.NoError(t, err)
	data, err := aaccanonical.JCS(generic)
	require.NoError(t, err)
	record, err := cli.Seal(cli.Request{Version: "capsule-seal-request/v1", Capsule: emit.Input{
		ActionID: actionID, ActionType: emit.ActionTypeFYI, Operator: "synthetic-mesh-node", Developer: "capsulectl-deal",
		Timestamp: time.Date(2026, 10, 9, 16, 30, 0, 0, time.UTC),
	}, Payload: data}, key)
	require.NoError(t, err)
	return record
}

// The Mesh receipt (D4), from the synthetic composition, in Chrome at 390px:
// both halves name the same exchange by the client's nonce and request, so
// it is stamped by both; a placeholder nonce, or another nonce over the same
// request, fails stamp 3 and gets the failed box.
func TestPresentationMeshReceiptWithTheirHalf(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	chrome := startHeadlessChrome(t, binary)
	payer := newMeshPayer(t)
	request := cli.BytesDigest([]byte("the request body"))
	read := func(t *testing.T, own, theirs meshHalf, opts ...meshOptions) (string, string, []string, string) {
		t.Helper()
		var o meshOptions
		if len(opts) > 0 {
			o = opts[0]
		}
		b := meshComposition(t, payer, own, theirs, o)
		page, err := composedPage(b, nil, dealVerifyCommand("page.html"), cli.BuiltinWording())
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "page.html")
		require.NoError(t, os.WriteFile(path, []byte(page), 0o600))
		p := chrome.open(t, "file://"+path, presentationViews[0])
		var got struct {
			Banner, Their, Refusal string
			Stamps                 []string
		}
		p.eval(t, `(() => {
  const q = (s) => document.querySelector("#deal " + s);
  return {
    Banner: (q(".deal-banner") || {}).textContent || "",
    Their: (q("[data-their]") || { dataset: {} }).dataset.their || "",
    Refusal: (q("p.deal-bad") || {}).textContent || "",
    Stamps: [...document.querySelectorAll("#deal [data-stamp]")].map((s) => s.dataset.stamp + " | " + s.textContent),
  };
})()`, &got)
		return got.Banner, got.Their, got.Stamps, got.Refusal
	}
	t.Run("the same exchange", func(t *testing.T) {
		half := meshHalf{nonce: "client-nonce-1", source: "client_supplied", requestDigest: request}
		banner, their, stamps, refusal := read(t, half, meshHalf{nonce: "client-nonce-1", source: "host_forwarded_nonce", requestDigest: request})
		require.Empty(t, refusal)
		// A page a viewer opens carries no openings, so the words the payer
		// asked are not checked here and its half reads "not fully checked".
		assert.Contains(t, banner, "Stamped by both sides · not fully checked")
		assert.Equal(t, "verified", their)
		require.Len(t, stamps, 5)
		assert.Contains(t, stamps[2], "passed | ✓Their record and yours are about the same exchange")
		assert.Contains(t, stamps[2], "both records name the same request; neither points at the other")
	})
	same := meshHalf{nonce: "client-nonce-1", source: "client_supplied", requestDigest: request}
	at := time.Date(2026, 10, 9, 16, 15, 0, 0, time.UTC)
	t.Run("a pushed half the payer accepted under their key", func(t *testing.T) {
		banner, their, _, _ := read(t, same, same, meshOptions{citing: func(h artifact.Record, provider string) *artifact.Record {
			c := meshCitingRecord(t, payer.key, h.CapsuleID, provider, false, at)
			return &c
		}})
		assert.Contains(t, banner, "Stamped by both sides")
		assert.Equal(t, "verified", their)
	})
	for name, cite := range map[string]func(artifact.Record, string) *artifact.Record{
		"a pushed half accepted under another key": func(h artifact.Record, _ string) *artifact.Record {
			c := meshCitingRecord(t, payer.key, h.CapsuleID, strings.Repeat("cd", 32), false, at)
			return &c
		},
		"one operator on both sides": func(h artifact.Record, provider string) *artifact.Record {
			c := meshCitingRecord(t, payer.key, h.CapsuleID, provider, true, at)
			return &c
		},
	} {
		t.Run(name, func(t *testing.T) {
			banner, their, stamps, _ := read(t, same, same, meshOptions{citing: cite})
			assert.Contains(t, banner, "Your receipt only")
			assert.Equal(t, "failed", their)
			assert.Contains(t, stamps[1], "failed | —Each side's record is stamped into its own book")
		})
	}
	other := meshHalf{nonce: "client-nonce-2", source: "client_supplied", requestDigest: request}
	for name, c := range map[string]struct {
		opts    meshOptions
		refusal string
	}{
		"two other parties":                  {meshOptions{second: &same, secondProvider: true}, "more than one other party"},
		"their custody domain is your key":   {meshOptions{custodyIsPayer: true}, "yours under another label"},
		"your half not cited by your report": {meshOptions{uncited: true}, "not cited by your receipt"},
		"your report under another key":      {meshOptions{reportKey: newKey(t)}, "not the composer's own copy"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, refusal := read(t, same, same, c.opts)
			assert.Contains(t, refusal, c.refusal)
		})
	}
	t.Run("a failing copy beside a passing one", func(t *testing.T) {
		banner, their, _, refusal := read(t, same, same, meshOptions{second: &other})
		require.Empty(t, refusal)
		assert.Contains(t, banner, "Your receipt only", "a passing copy beside a failing one is never stamped by both")
		assert.Equal(t, "failed", their)
	})
	t.Run("not a deal composition", func(t *testing.T) {
		banner, their, stamps, refusal := read(t, same, same, meshOptions{rootActionID: "example-composition"})
		assert.Empty(t, banner+their+refusal, "the deal view does not draw it")
		assert.Empty(t, stamps)
	})
	t.Run("a response in neither form", func(t *testing.T) {
		banner, their, _, _ := read(t, same, same, meshOptions{otherForm: true})
		assert.Contains(t, banner, "Your receipt only")
		assert.Equal(t, "failed", their)
	})
	for name, theirs := range map[string]meshHalf{
		"a placeholder nonce":            {nonce: "plugin_generated", source: "plugin_generated_fallback", requestDigest: request},
		"the host-served placeholder":    {nonce: "host-served-no-nonce", source: "host_served_observed", requestDigest: request},
		"another nonce, the same prompt": {nonce: "client-nonce-2", source: "client_supplied", requestDigest: request},
	} {
		t.Run(name, func(t *testing.T) {
			own := meshHalf{nonce: theirs.nonce, source: theirs.source, requestDigest: request}
			if name == "another nonce, the same prompt" {
				own.nonce = "client-nonce-1"
			}
			banner, their, stamps, refusal := read(t, own, theirs)
			require.Empty(t, refusal)
			assert.Contains(t, banner, "Your receipt only")
			assert.Equal(t, "failed", their)
			assert.Contains(t, stamps[2], "failed | —Their record and yours are about the same exchange")
		})
	}
}
