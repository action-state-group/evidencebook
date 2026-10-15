// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"reflect"
	"strings"
	"testing"
)

// Tests for the MCP carriage (profile §11.4.5): the profile's two tools
// exactly, the canonical Artifact byte-identical to the native stream's, and
// authorization only from the host's authentication, never from MCP.

// The evidence_request input schema, as profile §11.4.5 writes it.
const profileRequestSchema = `{
 "type": "object",
 "properties": {
  "request": {"type": "string", "pattern": "^[A-Za-z0-9_-]+$"},
  "media_type": {"enum": ["application/cbor", "application/json"]}
 },
 "required": ["request", "media_type"],
 "additionalProperties": false
}`

var mcpMeta = map[string]any{mcpMetaProtocolVersion: MCPProtocolVersion, mcpMetaClientCapabilities: map[string]any{}}

func mcpServer(t *testing.T, book *Book, policy SharePolicy) *MCPServer {
	t.Helper()
	server, err := book.MCP(MCPOptions{Manifest: ManifestOptions{Bindings: []string{BindingStream, BindingMCP}}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// mcpCall sends one JSON-RPC request and decodes the response.
func mcpCall(t *testing.T, server *MCPServer, method string, params map[string]any, authenticated string) map[string]any {
	t.Helper()
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	return mcpDecode(t, server.Handle(context.Background(), data, authenticated))
}

func mcpDecode(t *testing.T, response []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(response, &out); err != nil {
		t.Fatalf("response %s: %v", response, err)
	}
	return out
}

func mcpRequestParams(m Message) map[string]any {
	return map[string]any{"name": MCPToolRequest, "_meta": mcpMeta,
		"arguments": map[string]any{"request": base64.RawURLEncoding.EncodeToString(m.Octets), "media_type": m.MediaType}}
}

// mcpReply sends m over MCP and returns the reply it carries, after checking
// the result is the §11.4.5 shape.
func mcpReply(t *testing.T, server *MCPServer, m Message, authenticated string) Reply {
	t.Helper()
	return mcpReplyOf(t, mcpCall(t, server, "tools/call", mcpRequestParams(m), authenticated), m.MediaType)
}

func mcpReplyOf(t *testing.T, response map[string]any, mediaType string) Reply {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", response)
	}
	content, _ := result["content"].([]any)
	if result["resultType"] != "complete" || result["isError"] != false || content == nil {
		t.Fatalf("a reply is a complete result with isError false and a content array: %v", result)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	text, _ := structured["reply"].(string)
	if len(structured) != 2 || structured["media_type"] != mediaType || !mcpOpaque.MatchString(text) {
		t.Fatalf("structuredContent is not {reply, media_type} of the request's media type: %v", structured)
	}
	octets, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := ParseReply(Message{Octets: octets, MediaType: mediaType})
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func mcpTool(t *testing.T, list map[string]any, name string) map[string]any {
	t.Helper()
	for _, tool := range list["result"].(map[string]any)["tools"].([]any) {
		if m := tool.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("tools/list has no %s", name)
	return nil
}

func jsonEqual(t *testing.T, got any, want string) bool {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(got, w)
}

// §11.4.5 listing: exactly the two tools, under exactly the profile's names,
// with exactly its schemas, no x-mcp-header, and the same list for every
// caller before and after any history (§11.4.3).
func TestMCPListsTheProfilesTwoTools(t *testing.T) {
	book, records := linkedBook(t)
	server := mcpServer(t, book, DefaultSharePolicy())
	params := map[string]any{"_meta": mcpMeta}
	first := mcpCall(t, server, "tools/list", params, "")
	result, ok := first["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list: %v", first)
	}
	if result["resultType"] != "complete" || result["cacheScope"] != "public" || result["ttlMs"] == nil {
		t.Fatalf("tools/list result: %v", result)
	}
	var names []string
	for _, tool := range result["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "evidence_manifest,evidence_request" {
		t.Fatalf("tools %v, want exactly evidence_manifest and evidence_request", names)
	}
	manifestTool, requestTool := mcpTool(t, first, "evidence_manifest"), mcpTool(t, first, "evidence_request")
	if !jsonEqual(t, manifestTool["inputSchema"], `{"type": "object", "additionalProperties": false}`) {
		t.Fatalf("evidence_manifest inputSchema %v", manifestTool["inputSchema"])
	}
	if !jsonEqual(t, requestTool["inputSchema"], profileRequestSchema) {
		t.Fatalf("evidence_request inputSchema %v", requestTool["inputSchema"])
	}
	if !jsonEqual(t, requestTool["outputSchema"], strings.ReplaceAll(profileRequestSchema, `"request"`, `"reply"`)) {
		t.Fatalf("evidence_request outputSchema %v", requestTool["outputSchema"])
	}
	listed, _ := json.Marshal(result["tools"])
	if bytes.Contains(listed, []byte("x-mcp-header")) {
		t.Fatal("a tool property carries x-mcp-header (P-40)")
	}
	pin := anchorPin(t, book)
	mcpReply(t, server, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: pin}}), "peer-1")
	for _, caller := range []string{"", "peer-1", "known-peer"} {
		again := mcpCall(t, server, "tools/list", params, caller)
		if !reflect.DeepEqual(again["result"].(map[string]any)["tools"], result["tools"]) {
			t.Fatalf("the tool list differs for caller %q or after history", caller)
		}
	}
}

// FR-8, ER §5, profile §11.4.1 rules 1-3 and §11.4.8: one request's octets
// sent over the native stream and over MCP give the same request digest,
// the reply in the request's encoding, and a byte-identical canonical
// Artifact. Only the carriage differs. The manifest is the native one byte
// for byte.
func TestMCPCanonicalArtifactIsTheNativeOne(t *testing.T) {
	book, records := linkedBook(t)
	server := mcpServer(t, book, DefaultSharePolicy())
	pin := anchorPin(t, book)
	for _, subject := range []Subject{{Kind: SubjectRecord, Digest: records[0].RecordID}, {Kind: SubjectFullHistory}} {
		for _, mediaType := range []string{MediaTypeCBOR, MediaTypeJSON} {
			t.Run(fmt.Sprintf("%s %s", subject.Kind, mediaType), func(t *testing.T) {
				r := requestIn(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: pin}, Nonce: "2026-10-12T00:00:10Z/c"}, mediaType)
				native := mustEnvelope(t, respondAs(t, book, r, "peer-1"))
				reply := mcpReply(t, server, r, "peer-1")
				carried := mustEnvelope(t, reply)
				if reply.MediaType != mediaType || carried.RequestDigest != digestBytes(r.Octets) || carried.RequestDigest != native.RequestDigest {
					t.Fatal("the MCP reply is bound to the request octets as received, in their encoding")
				}
				if !bytes.Equal(carried.Artifact, native.Artifact) || carried.ArtifactDigest != native.ArtifactDigest {
					t.Fatal("the canonical Artifact differs between the native stream and MCP")
				}
				if !reflect.DeepEqual(carried.Anchor, native.Anchor) || !reflect.DeepEqual(carried.Subject, native.Subject) {
					t.Fatal("the anchor or subject differs between the native stream and MCP")
				}
			})
		}
	}
	manifest := mcpCall(t, server, "tools/call", map[string]any{"name": MCPToolManifest, "arguments": map[string]any{}, "_meta": mcpMeta}, "")
	result := manifest["result"].(map[string]any)
	served, err := evidencerequest.EncodeJCS(jsonAny(t, result["structuredContent"]))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := book.Manifest(ManifestOptions{Bindings: []string{BindingMCP, BindingStream}})
	if err != nil || !bytes.Equal(served, direct) || result["isError"] != false {
		t.Fatalf("evidence_manifest serves %s, the book's manifest is %s", served, direct)
	}
}

// jsonAny converts a value decoded by encoding/json into the profile value
// model evidencerequest.EncodeJCS takes.
func jsonAny(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := evidencerequest.DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// §11.4.1 rule 4 and 5, NFR-4: an MCP connection is never authorization. A
// protected subject is answered only to the identity the host authenticated;
// a client name, a _meta member, an argument or a repeated connection grants
// nothing. Whatever MCP carries, the reply is the one Respond gives the same
// authenticated requester.
func TestMCPConnectionIsNeverAuthorization(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	pin := anchorPin(t, book)
	server := mcpServer(t, book, policyWith("counterparties"))
	r := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: pin}})
	claimed := mcpRequestParams(r)
	claimed["_meta"] = map[string]any{
		mcpMetaProtocolVersion:                MCPProtocolVersion,
		mcpMetaClientCapabilities:             map[string]any{},
		"io.modelcontextprotocol/clientInfo":  map[string]any{"name": "known-peer", "version": "1"},
		"io.modelcontextprotocol/sessionId":   "known-peer",
		"com.example/authenticated_requester": "known-peer",
	}
	claimed["requestState"] = "known-peer"
	for name, authenticated := range map[string]string{"unauthenticated": "", "authenticated as another": "someone-else"} {
		t.Run(name, func(t *testing.T) {
			before := book.Size()
			for range 2 { // a second call on the "same connection" is no different
				reply := mcpReplyOf(t, mcpCall(t, server, "tools/call", claimed, authenticated), r.MediaType)
				mustRefusal(t, reply, ReasonNotAuthorized)
			}
			if authenticated == "" && book.Size() != before {
				t.Fatal("an unauthenticated MCP call grew the log")
			}
		})
	}
	reply := mcpReplyOf(t, mcpCall(t, server, "tools/call", claimed, "known-peer"), r.MediaType)
	e := mustEnvelope(t, reply)
	native := mustEnvelope(t, respondReply(t, book, r, RespondOptions{AuthenticatedRequester: "known-peer", Policy: policyWith("counterparties")}))
	if !bytes.Equal(e.Artifact, native.Artifact) {
		t.Fatal("the authenticated counterparty gets another Artifact over MCP")
	}
	// Public questions stay public over MCP, as natively.
	checkpoints := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{ExpectedPin: pin}})
	mustEnvelope(t, mcpReply(t, server, checkpoints, ""))
}

func respondReply(t *testing.T, book *Book, req Message, opts RespondOptions) Reply {
	t.Helper()
	reply, _ := respondWith(t, book, req, opts)
	return reply
}

// §11.4.5 "Anything else", rules 5 and 6: what is not a request under the
// profile gets a JSON-RPC error, never a result, so never an envelope, a
// refusal or isError text a requester could mistake for one. Nothing grows
// the log. A notification gets no response at all.
func TestMCPNonRequestsAreTransportConditions(t *testing.T) {
	book, records := linkedBook(t)
	server := mcpServer(t, book, DefaultSharePolicy())
	pin := anchorPin(t, book)
	r := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: pin}})
	good := base64.RawURLEncoding.EncodeToString(r.Octets)
	call := func(name string, arguments map[string]any) string {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "x", "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments, "_meta": mcpMeta}})
		return string(data)
	}
	// The request with trailing JSON whitespace until its base64url needs
	// padding, then written with the padding.
	spaced := append([]byte{}, r.Octets...)
	for len(spaced)%3 == 0 {
		spaced = append(spaced, ' ')
	}
	padded := base64.URLEncoding.EncodeToString(spaced)
	cases := map[string]struct {
		message string
		code    float64
	}{
		"unknown tool":             {call("evidence-request", map[string]any{"request": good, "media_type": MediaTypeJSON}), mcpInvalidParams},
		"prefixed tool name":       {call("server__evidence_request", map[string]any{"request": good, "media_type": MediaTypeJSON}), mcpInvalidParams},
		"extra argument":           {call(MCPToolRequest, map[string]any{"request": good, "media_type": MediaTypeJSON, "requester": "known-peer"}), mcpInvalidParams},
		"renamed argument":         {call(MCPToolRequest, map[string]any{"request_octets": good, "media_type": MediaTypeJSON}), mcpInvalidParams},
		"no media type":            {call(MCPToolRequest, map[string]any{"request": good}), mcpInvalidParams},
		"media type parameter":     {call(MCPToolRequest, map[string]any{"request": good, "media_type": MediaTypeJSON + "; charset=utf-8"}), mcpInvalidParams},
		"padded base64url":         {call(MCPToolRequest, map[string]any{"request": padded, "media_type": MediaTypeJSON}), mcpInvalidParams},
		"standard base64":          {call(MCPToolRequest, map[string]any{"request": strings.NewReplacer("-", "+", "_", "/").Replace(good) + "+/", "media_type": MediaTypeJSON}), mcpInvalidParams},
		"not canonical base64url":  {call(MCPToolRequest, map[string]any{"request": "AB", "media_type": MediaTypeJSON}), mcpInvalidParams},
		"empty request":            {call(MCPToolRequest, map[string]any{"request": "", "media_type": MediaTypeJSON}), mcpInvalidParams},
		"request as object":        {call(MCPToolRequest, map[string]any{"request": map[string]any{"subject": "x"}, "media_type": MediaTypeJSON}), mcpInvalidParams},
		"manifest with argument":   {call(MCPToolManifest, map[string]any{"verbose": "yes"}), mcpInvalidParams},
		"duplicate argument":       {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"evidence_request","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"arguments":{"request":"` + good + `","request":"AAAA","media_type":"application/json"}}}`, mcpParseError},
		"no protocol version":      {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"evidence_request","arguments":{"request":"` + good + `","media_type":"application/json"}}}`, mcpInvalidParams},
		"other protocol version":   {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"evidence_request","_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25","io.modelcontextprotocol/clientCapabilities":{}},"arguments":{"request":"` + good + `","media_type":"application/json"}}}`, mcpUnsupportedProtocolVersion},
		"initialize":               {`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, mcpMethodNotFound},
		"batch":                    {`[{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}]`, mcpInvalidRequest},
		"null id":                  {`{"jsonrpc":"2.0","id":null,"method":"tools/list","params":{}}`, mcpInvalidRequest},
		"fractional id":            {`{"jsonrpc":"2.0","id":1.5,"method":"tools/list","params":{}}`, mcpInvalidRequest},
		"not JSON-RPC 2.0":         {`{"jsonrpc":"1.0","id":1,"method":"tools/list","params":{}}`, mcpInvalidRequest},
		"not JSON":                 {`{"jsonrpc":`, mcpParseError},
		"request octets as params": {string(r.Octets), mcpInvalidRequest},
	}
	before := book.Size()
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			response := mcpDecode(t, server.Handle(context.Background(), []byte(c.message), "peer-1"))
			e, _ := response["error"].(map[string]any)
			if _, isResult := response["result"]; isResult || e == nil || e["code"] != c.code {
				t.Fatalf("want JSON-RPC error %v, got %v", c.code, response)
			}
		})
	}
	if book.Size() != before {
		t.Fatal("a non-request grew the log")
	}
	if out := server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`), "peer-1"); out != nil {
		t.Fatalf("a notification was answered: %s", out)
	}
	if _, err := book.MCP(MCPOptions{Manifest: ManifestOptions{Bindings: []string{BindingStream}}, Policy: DefaultSharePolicy()}); err == nil {
		t.Fatal("an MCP carriage whose manifest does not list mcp")
	}
}

// §11.4.5 "pending" and rule 4: a retry carries the same request and
// media_type; inputResponses, requestState, _meta members and the JSON-RPC
// id are carriage. The reply binds the same request digest and carries the
// same Artifact.
func TestMCPCarriageMembersNeverReachTheAnswer(t *testing.T) {
	book, records := linkedBook(t)
	server := mcpServer(t, book, DefaultSharePolicy())
	pin := anchorPin(t, book)
	r := requestIn(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[1].RecordID}, Coverage: Coverage{ExpectedPin: pin}}, MediaTypeCBOR)
	plain := mustEnvelope(t, mcpReply(t, server, r, "peer-1"))
	params := mcpRequestParams(r)
	params["inputResponses"] = map[string]any{"x": map[string]any{"action": "accept"}}
	params["requestState"] = "opaque"
	params["_meta"] = map[string]any{
		mcpMetaProtocolVersion: MCPProtocolVersion, mcpMetaClientCapabilities: map[string]any{"elicitation": map[string]any{}},
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-01", "progressToken": "p",
	}
	retried := mustEnvelope(t, mcpReplyOf(t, mcpCall(t, server, "tools/call", params, "peer-1"), r.MediaType))
	if retried.RequestDigest != plain.RequestDigest || !bytes.Equal(retried.Artifact, plain.Artifact) {
		t.Fatal("carriage members changed the request digest or the Artifact")
	}
	discover := mcpCall(t, server, "server/discover", map[string]any{}, "")["result"].(map[string]any)
	if !jsonEqual(t, discover["supportedVersions"], `["2026-07-28"]`) || discover["cacheScope"] != "public" {
		t.Fatalf("server/discover: %v", discover)
	}
}
