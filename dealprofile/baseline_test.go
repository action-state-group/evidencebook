// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/baseline-vectors.json is the deal plugin's file, frozen from its
// builders before the move (evidencebook-deal, TestBaselineVectors). This
// test makes every step and report again from what the plugin hands this
// package, and seals each Capsule as the plugin does, so the move changes
// no byte: payload, digest, capsule_id and Capsule bytes alike.

type baselineFile struct {
	Deals []struct {
		Flow        string `json:"flow"`
		DealID      string `json:"deal_id"`
		SigningSeed string `json:"signing_seed"`
		Operator    string `json:"operator"`
		Steps       []struct {
			N         int64  `json:"n"`
			Payload   string `json:"payload"`
			Digest    string `json:"digest"`
			CapsuleID string `json:"capsule_id"`
			Capsule   string `json:"capsule"`
			Header    struct {
				RecordType    string            `json:"record_type"`
				EpistemicType string            `json:"epistemic_type"`
				CommittedAt   string            `json:"committed_at"`
				SubjectRef    string            `json:"subject_ref"`
				Correlation   map[string]string `json:"correlation"`
				Links         []struct {
					Type   string `json:"type"`
					Target string `json:"target"`
				} `json:"links"`
				Fields map[string]string `json:"fields"`
			} `json:"header"`
		} `json:"steps"`
		Reports []struct {
			Form       string `json:"form"`
			Timestamp  string `json:"timestamp"`
			AgentInput string `json:"agent_input"`
			Payload    string `json:"payload"`
			CapsuleID  string `json:"capsule_id"`
			Capsule    string `json:"capsule"`
		} `json:"reports"`
	} `json:"deals"`
}

// capsuleFacts are the members of a sealed step Capsule the plugin decided:
// what StepInput carries.
type capsuleFacts struct {
	ActionType string          `json:"action_type"`
	Timestamp  string          `json:"timestamp"`
	Settlement json.RawMessage `json:"settlement"`
	Chain      *struct {
		ParentCapsuleID string `json:"parent_capsule_id"`
		Relation        string `json:"relation"`
	} `json:"chain"`
	Disposition *struct {
		Decision      string `json:"decision"`
		Approver      string `json:"approver"`
		HumanDisposed bool   `json:"human_disposed"`
		VerdictClass  string `json:"verdict_class"`
	} `json:"disposition"`
	Effect *struct {
		Type                 string `json:"type"`
		IrreversibilityClass string `json:"irreversibility_class"`
	} `json:"effect"`
}

func loadBaseline(t *testing.T) baselineFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/baseline-vectors.json")
	require.NoError(t, err)
	var f baselineFile
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Deals)
	return f
}

func identityOf(t *testing.T, seedHex string) emit.SigningIdentity {
	t.Helper()
	seed, err := hex.DecodeString(seedHex)
	require.NoError(t, err)
	id, err := emit.NewEd25519SigningIdentity(ed25519.NewKeyFromSeed(seed))
	require.NoError(t, err)
	return id
}

func TestBaselineSteps(t *testing.T) {
	for _, d := range loadBaseline(t).Deals {
		t.Run(d.Flow, func(t *testing.T) {
			identity := identityOf(t, d.SigningSeed)
			for _, s := range d.Steps {
				payload, digest, err := Encode([]byte(s.Payload), nil)
				require.NoError(t, err, "step %d", s.N)
				assert.Equal(t, s.Payload, string(payload), "step %d payload", s.N)
				assert.Equal(t, s.Digest, digest, "step %d digest", s.N)

				var facts capsuleFacts
				require.NoError(t, json.Unmarshal([]byte(s.Capsule), &facts))
				at, err := time.Parse(time.RFC3339, facts.Timestamp)
				require.NoError(t, err)
				in := StepInput{
					DealID: d.DealID, Seq: s.N, Operator: d.Operator, At: at, Settlement: facts.Settlement,
					Local: s.Header.Fields[LocalField], Amount: s.Header.Fields[AmountField], Currency: s.Header.Fields[CurrencyField],
				}
				if c := facts.Chain; c != nil {
					in.Parent = &emit.Chain{ParentCapsuleID: c.ParentCapsuleID, Relation: emit.ChainRelation(c.Relation)}
				}
				if facts.ActionType == string(emit.ActionTypeDecide) {
					require.NotNil(t, facts.Disposition)
					require.NotNil(t, facts.Effect)
					in.Decide = &Decide{
						Disposition: emit.Disposition{
							Decision: emit.Decision(facts.Disposition.Decision), Approver: emit.Approver(facts.Disposition.Approver),
							HumanDisposed: facts.Disposition.HumanDisposed, VerdictClass: emit.VerdictClass(facts.Disposition.VerdictClass),
						},
						EffectType: facts.Effect.Type, Irreversibility: emit.IrreversibilityClass(facts.Effect.IrreversibilityClass),
					}
				}
				capsule, header, err := StepCapsule(in)
				require.NoError(t, err, "step %d", s.N)
				result, err := emit.Seal(emit.SealInput{Capsule: capsule, Identity: identity, Payload: json.RawMessage(payload)})
				require.NoError(t, err, "step %d", s.N)
				assert.Equal(t, s.CapsuleID, result.CapsuleID, "step %d capsule_id", s.N)
				assert.Equal(t, s.Capsule, string(result.Payload), "step %d Capsule", s.N)
				assert.Equal(t, s.Header.RecordType, header.RecordType)
				assert.Equal(t, s.Header.EpistemicType, header.EpistemicType)
				assert.Equal(t, s.Header.CommittedAt, header.CommittedAt.Format(time.RFC3339Nano))
				assert.Equal(t, s.Header.SubjectRef, header.SubjectRef)
				assert.Empty(t, s.Header.Correlation, "a step names no correlation")
				var cites []string
				for _, l := range s.Header.Links {
					require.Equal(t, "cites", l.Type)
					cites = append(cites, l.Target)
				}
				assert.Equal(t, cites, header.Cites)
				assert.Equal(t, s.Header.Fields, header.Fields)
			}
		})
	}
}

func TestBaselineReports(t *testing.T) {
	forms := map[string]int{}
	for _, d := range loadBaseline(t).Deals {
		for _, r := range d.Reports {
			forms[r.Form]++
		}
		t.Run(d.Flow, func(t *testing.T) {
			identity := identityOf(t, d.SigningSeed)
			for _, r := range d.Reports {
				var in struct {
					DealID     string         `json:"deal_id"`
					Audience   string         `json:"audience"`
					Nonce      string         `json:"nonce"`
					Report     map[string]any `json:"report"`
					Checkpoint *Checkpoint    `json:"deal_checkpoint"`
				}
				dec := json.NewDecoder(strings.NewReader(r.AgentInput))
				dec.UseNumber()
				require.NoError(t, dec.Decode(&in))
				payload, err := ReportPayload(Report{DealID: in.DealID, Audience: in.Audience, Nonce: in.Nonce, Body: in.Report, Checkpoint: in.Checkpoint})
				require.NoError(t, err)
				assert.Equal(t, r.Payload, string(payload), "%s payload", r.Form)
				assert.Equal(t, r.Form == "standalone", in.Checkpoint != nil, "only a standalone report binds a checkpoint")
				at, err := time.Parse(time.RFC3339, r.Timestamp)
				require.NoError(t, err)
				result, err := emit.Seal(emit.SealInput{Capsule: ReportCapsule(d.Operator, at), Identity: identity, Payload: json.RawMessage(payload)})
				require.NoError(t, err)
				assert.Equal(t, r.CapsuleID, result.CapsuleID, "%s capsule_id", r.Form)
				assert.Equal(t, r.Capsule, string(result.Payload), "%s Capsule", r.Form)
			}
		})
	}
	assert.Positive(t, forms["standalone"], "a standalone report is frozen")
	assert.Positive(t, forms["in_log"], "an in-log report is frozen")
}
