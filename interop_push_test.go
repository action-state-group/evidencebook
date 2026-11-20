// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func openPushShelf(t *testing.T, v *receiveVectors) (*Shelf, *shelfFixture) {
	t.Helper()
	f := newShelfFixture(t)
	f.cfg.Receive = ReceiveConfig{Profile: v.Profile, Bounds: v.Bounds.bounds(), Retention: time.Hour}
	return f.open(t), f
}

func pushVectors(t *testing.T) *receiveVectors {
	t.Helper()
	b, err := os.ReadFile("testdata/shelf/receive/script.json")
	require.NoError(t, err)
	var v receiveVectors
	require.NoError(t, json.Unmarshal(b, &v))
	return &v
}

func errorCode(t *testing.T, response []byte) float64 {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(response, &m))
	e, ok := m["error"].(map[string]any)
	require.True(t, ok, string(response))
	require.NotContains(t, m, "result")
	return e["code"].(float64)
}

// A receiver fault is an internal error and no answer, on both carriages
// (RP §2.3 rule 4): a released shelf cannot complete a push.
func TestPushReceiverFaultIsInternalErrorNotAnAnswer(t *testing.T) {
	v := pushVectors(t)
	s, _ := openPushShelf(t, v)
	require.NoError(t, s.Release())
	args := map[string]any{"record": b64([]byte(v.Records["a1"]))}
	require.EqualValues(t, mcpInternalError, errorCode(t, s.PushMCP("").Handle(context.Background(), mcpPushRequest(1, MCPToolRecordPush, args))))
	res := s.PushA2A().Handle(context.Background(), A2ARequest{Version: A2AProtocolVersion, Extensions: []string{A2APushExtensionURI},
		Body: a2aPushRequest(2, a2aPushMessageOf(args, mediaTypeApplicationJSON))})
	require.EqualValues(t, mcpInternalError, errorCode(t, res.Body))
	require.Empty(t, res.Extensions)
}

// A shelf with no receive profile cannot take a push: no answer.
func TestPushWithoutReceiveProfileIsInternalError(t *testing.T) {
	f := newShelfFixture(t)
	s := f.open(t)
	args := map[string]any{"record": b64([]byte("x"))}
	require.EqualValues(t, mcpInternalError, errorCode(t, s.PushMCP("").Handle(context.Background(), mcpPushRequest(1, MCPToolRecordPush, args))))
}

// A message over the bound is -32600 before it is parsed, and a record at
// the record bound still reaches Receive (RP §2.3 rule 6).
func TestPushMessageBound(t *testing.T) {
	v := pushVectors(t)
	s, _ := openPushShelf(t, v)
	over := make([]byte, 0)
	over = append(over, mcpPushRequest(1, MCPToolRecordPush, map[string]any{"record": strings.Repeat("A", 2*int(v.Bounds.MaxRecordBytes)+64<<10)})...)
	require.EqualValues(t, mcpInvalidRequest, errorCode(t, s.PushMCP("").Handle(context.Background(), over)))
	res := s.PushA2A().Handle(context.Background(), A2ARequest{Version: A2AProtocolVersion, Extensions: []string{A2APushExtensionURI}, Body: over})
	require.EqualValues(t, mcpInvalidRequest, errorCode(t, res.Body))

	// A record one byte over the receiver's bound is answered over_limit, not met with a transport condition.
	big := b64(make([]byte, v.Bounds.MaxRecordBytes+1))
	out := s.PushMCP("").Handle(context.Background(), mcpPushRequest(2, MCPToolRecordPush, map[string]any{"record": big}))
	var m struct {
		Result struct {
			StructuredContent map[string]map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(out, &m), string(out))
	require.Equal(t, "over_limit", m.Result.StructuredContent["record"]["refused"])
}

// The tool list and the extension entry are fixed text, the same for every
// caller, and say nothing about what the shelf holds (RP §2.3 rule 7).
func TestPushDiscoveryIsFixed(t *testing.T) {
	v := pushVectors(t)
	s, _ := openPushShelf(t, v)
	list := mcpPushList(t, s)
	s2, _ := openPushShelf(t, v)
	require.Equal(t, list, mcpPushList(t, s2))
	var ext map[string]any
	require.NoError(t, json.Unmarshal(PushA2AExtension(), &ext))
	require.Equal(t, A2APushExtensionURI, ext["uri"])
	require.Equal(t, false, ext["required"])
	require.NotContains(t, ext, "params")
}

func mcpPushList(t *testing.T, s *Shelf) string {
	t.Helper()
	req := mustJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": map[string]any{"_meta": map[string]any{mcpMetaProtocolVersion: MCPProtocolVersion, mcpMetaClientCapabilities: map[string]any{}}}})
	return string(s.PushMCP("v").Handle(context.Background(), req))
}

// The same push over MCP and then A2A is answered by the same Receive: the
// second finds a duplicate (RP §5).
func TestPushBothCarriagesOneShelf(t *testing.T) {
	v := pushVectors(t)
	s, _ := openPushShelf(t, v)
	args := map[string]any{"record": b64([]byte(v.Records["a1"]))}
	first := s.PushMCP("").Handle(context.Background(), mcpPushRequest(1, MCPToolRecordPush, args))
	require.JSONEq(t, `{"record":{"digest":"03122616bfe44c7b8dedc9f2899dec73e6d892e8a6036178160fe3d9d147e549","stored":true,"duplicate":false,"deleted":false,"counted":true,"ask_for_book":false,"refused":""}}`,
		string(answerData(t, "mcp", first)))
	res := s.PushA2A().Handle(context.Background(), A2ARequest{Version: A2AProtocolVersion, Extensions: []string{A2APushExtensionURI},
		Body: a2aPushRequest(2, a2aPushMessageOf(args, mediaTypeApplicationJSON))})
	require.Equal(t, []string{A2APushExtensionURI}, res.Extensions)
	require.JSONEq(t, `{"record":{"digest":"03122616bfe44c7b8dedc9f2899dec73e6d892e8a6036178160fe3d9d147e549","stored":false,"duplicate":true,"deleted":false,"counted":true,"ask_for_book":false,"refused":""}}`,
		string(answerData(t, "a2a", res.Body)))
}

// Nothing the carriage adds is an identity (RP §2.3 rule 2): the same push
// answered on fresh shelves, with MCP _meta client information or A2A sender
// fields set, gets the same answer.
func TestPushCarriageFieldsNeverChangeTheOutcome(t *testing.T) {
	v := pushVectors(t)
	args := map[string]any{"record": b64([]byte(v.Records["a1"]))}
	answerOn := func(carriage string, decorate func(map[string]any)) string {
		s, _ := openPushShelf(t, v)
		var out []byte
		if carriage == "mcp" {
			var m map[string]any
			require.NoError(t, json.Unmarshal(mcpPushRequest(1, MCPToolRecordPush, args), &m))
			decorate(m["params"].(map[string]any))
			out = s.PushMCP("").Handle(context.Background(), mustJSON(m))
			return string(answerData(t, "mcp", out))
		}
		msg := a2aPushMessageOf(args, mediaTypeApplicationJSON)
		decorate(msg)
		res := s.PushA2A().Handle(context.Background(), A2ARequest{Version: A2AProtocolVersion,
			Extensions: []string{A2APushExtensionURI}, Body: a2aPushRequest(1, msg)})
		return string(answerData(t, "a2a", res.Body))
	}
	plain := func(map[string]any) {}
	require.JSONEq(t, answerOn("mcp", plain), answerOn("mcp", func(p map[string]any) {
		p["_meta"].(map[string]any)["clientInfo"] = map[string]any{"name": "alice", "version": "1"}
		p["_meta"].(map[string]any)["io.modelcontextprotocol/clientCapabilities"] = map[string]any{"sampling": map[string]any{}}
	}))
	require.JSONEq(t, answerOn("a2a", plain), answerOn("a2a", func(m map[string]any) {
		m["contextId"], m["referenceTaskIds"] = "alice-ctx", []any{"t-9"}
		m["metadata"] = map[string]any{"sender": "alice"}
		m["messageId"] = "alice-1"
	}))
}

// faultProfile is the vector profile with failures a test switches on: the
// proof part's lookup and the after-receive hook.
type faultProfile struct {
	vectorProfileSpec
	failProof, failAfter bool
}

func (p *faultProfile) ProofSender(ctx context.Context, keyID, logID string) (string, bool, error) {
	if p.failProof {
		return "", false, errors.New("injected profile failure")
	}
	return p.vectorProfileSpec.ProofSender(ctx, keyID, logID)
}

func (p *faultProfile) AfterReceive(context.Context, ReceiveResult, time.Time) error {
	if p.failAfter {
		return errors.New("injected after-receive failure")
	}
	return nil
}

// A push the receiver completes only in part, one part already committed, is
// an internal error and no answer on both carriages; sending it again is
// safe and answers the committed part `duplicate` (RP §2.3 rule 4).
func TestPushPartialFaultIsNoAnswerAndRetryIsSafe(t *testing.T) {
	for _, fault := range []string{"proof", "after"} {
		for _, carriage := range []string{"mcp", "a2a"} {
			t.Run(fault+"/"+carriage, func(t *testing.T) {
				v := pushVectors(t)
				profile := &faultProfile{vectorProfileSpec: v.Profile}
				f := newShelfFixture(t)
				f.cfg.Receive = ReceiveConfig{Profile: profile, Bounds: v.Bounds.bounds(), Retention: time.Hour}
				s := f.open(t)
				args := pushArguments(t, v, vectorStep{Record: "a1", Proof: "p1"})
				push := func() (json.RawMessage, bool) {
					if carriage == "mcp" {
						out := s.PushMCP("").Handle(context.Background(), mcpPushRequest(1, MCPToolRecordPush, args))
						return out, strings.Contains(string(out), `"error"`)
					}
					res := s.PushA2A().Handle(context.Background(), A2ARequest{Version: A2AProtocolVersion,
						Extensions: []string{A2APushExtensionURI}, Body: a2aPushRequest(1, a2aPushMessageOf(args, mediaTypeApplicationJSON))})
					return res.Body, strings.Contains(string(res.Body), `"error"`)
				}
				profile.failProof, profile.failAfter = fault == "proof", fault == "after"
				out, isError := push()
				require.True(t, isError, string(out))
				require.EqualValues(t, mcpInternalError, errorCode(t, out))
				profile.failProof, profile.failAfter = false, false
				out, isError = push()
				require.False(t, isError, string(out))
				var answer map[string]map[string]any
				require.NoError(t, json.Unmarshal(answerData(t, carriage, out), &answer))
				require.Equal(t, true, answer["record"]["duplicate"], "the committed record part is a duplicate")
				require.Equal(t, false, answer["record"]["stored"])
			})
		}
	}
}
