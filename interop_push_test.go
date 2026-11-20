// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
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
