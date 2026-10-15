// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"regexp"
	"slices"
)

// The MCP carriage of the exchange (profile §11.4.5), over MCP 2026-07-28.
const (
	// MCPProtocolVersion is the one MCP revision this carriage speaks.
	MCPProtocolVersion = "2026-07-28"
	// MCPToolManifest returns the manifest (profile §11.4.2) as its
	// structuredContent.
	MCPToolManifest = "evidence_manifest"
	// MCPToolRequest carries one request and returns its reply.
	MCPToolRequest = "evidence_request"
)

// JSON-RPC and MCP error codes this carriage answers with. Every one of them
// is a transport condition to the requester, never a refusal (profile
// §11.4.1 rules 5 and 6).
const (
	mcpParseError                 = -32700
	mcpInvalidRequest             = -32600
	mcpMethodNotFound             = -32601
	mcpInvalidParams              = -32602
	mcpInternalError              = -32603
	mcpUnsupportedProtocolVersion = -32022
)

const (
	mcpMetaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	mcpMetaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	mcpMetaServerInfo         = "io.modelcontextprotocol/serverInfo"
	// mcpListTTL is the caching hint on the tool list and on discovery: both
	// are fixed for the life of the server.
	mcpListTTL = 3_600_000
)

// mcpOpaque is the evidence_request pattern for request and reply: base64url
// without padding (profile §4, §11.4.5).
var mcpOpaque = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// mcpTools are the two tool definitions, in the order tools/list returns
// them. They are discovery objects (profile §11.4.3): fixed text and the
// §11.4.5 schemas, the same for every caller, carrying nothing about what
// the book holds. No property carries x-mcp-header (P-40).
var mcpTools = []any{
	map[string]any{
		"name":        MCPToolManifest,
		"title":       "Evidence request manifest",
		"description": "Returns this responder's evidence request manifest (profile evidence-interop/0): the profiles, request drafts, subject forms, derivations and bindings it answers. It says what can be asked, never what exists.",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": false},
	},
	map[string]any{
		"name":         MCPToolRequest,
		"title":        "Evidence request",
		"description":  "Carries one evidence request (draft-mih-agent-evidence-request, profile evidence-interop/0) as opaque octets in base64url without padding, and returns the responder's reply octets: a signed response envelope or a signed refusal, in the request's encoding.",
		"inputSchema":  mcpOpaqueSchema("request"),
		"outputSchema": mcpOpaqueSchema("reply"),
	},
}

func mcpOpaqueSchema(member string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			member:       map[string]any{"type": "string", "pattern": mcpOpaque.String()},
			"media_type": map[string]any{"enum": []any{MediaTypeCBOR, MediaTypeJSON}},
		},
		"required":             []any{member, "media_type"},
		"additionalProperties": false,
	}
}

// MCPOptions configures the MCP carriage of a book's responder.
type MCPOptions struct {
	// Manifest is what the host states about its carriers; Bindings must
	// list BindingMCP. The manifest served by evidence_manifest is
	// Book.Manifest(Manifest), byte for byte the one served elsewhere.
	Manifest ManifestOptions
	// Policy is the sharing policy every request is answered under, as for
	// RespondOptions.Policy.
	Policy SharePolicy
	// ServerVersion is the version in the serverInfo each result carries;
	// empty is "unversioned".
	ServerVersion string
}

// MCPServer carries the evidence request exchange over MCP (profile
// §11.4.5): the tools evidence_manifest and evidence_request, and the
// server/discover and tools/list requests MCP requires around them. It is a
// carriage only. It moves the request and reply octets as opaque bytes and
// adds nothing to what Respond answers: the canonical Artifact is the one
// the native stream gets for the same request (FR-8, ER §5).
//
// It is transport-neutral: Handle takes one received JSON-RPC message and
// returns the message to send back. The host owns the transport and its
// authentication.
type MCPServer struct {
	book       *Book
	manifest   json.RawMessage
	policy     SharePolicy
	serverInfo map[string]any
}

// MCP returns the MCP carriage of this book's responder.
func (b *Book) MCP(opts MCPOptions) (*MCPServer, error) {
	if !slices.Contains(opts.Manifest.Bindings, BindingMCP) {
		return nil, fmt.Errorf("%w: a manifest served over MCP lists the %q binding", ErrInvalid, BindingMCP)
	}
	if err := opts.Policy.Validate(); err != nil {
		return nil, err
	}
	manifest, err := b.Manifest(opts.Manifest)
	if err != nil {
		return nil, err
	}
	version := opts.ServerVersion
	if version == "" {
		version = "unversioned"
	}
	return &MCPServer{book: b, manifest: manifest, policy: opts.Policy,
		serverInfo: map[string]any{"name": "evidencebook", "version": version}}, nil
}

// Handle answers one JSON-RPC message received over MCP and returns the
// JSON-RPC message to send back, or nil for a notification.
//
// authenticated is the requester identity the host's transport established
// by its own authentication of this message (MCP authorization over HTTP, or
// the credentials of a stdio process), exactly as for
// RespondOptions.AuthenticatedRequester, or "" when the caller is not
// authenticated. It is the only identity Respond sees. Nothing inside the
// message is ever read as one: not the MCP _meta (clientInfo included), not
// a tool argument, not the JSON-RPC id, and not the connection the message
// arrived on. MCP 2026-07-28 has no session to read one from either. As in
// Respond, authentication decides whether a subject is answered, never what
// is answered (profile §11.4.1 rule 5).
//
// Every reply, envelope or refusal, is a tool result with isError false and
// structuredContent {"reply", "media_type"} (P-40). Anything that is not a
// request under §11.4.5 gets a JSON-RPC error, which is a transport
// condition and never a refusal.
func (s *MCPServer) Handle(ctx context.Context, message []byte, authenticated string) []byte {
	if len(message) > s.maxMessage() {
		return mcpError(nil, mcpInvalidRequest, "message too large", nil)
	}
	root, err := evidencerequest.DecodeJSON(message)
	if err != nil {
		return mcpError(nil, mcpParseError, "parse error", nil)
	}
	msg, ok := root.(map[string]any)
	if !ok {
		// MCP 2026-07-28 has no JSON-RPC batches.
		return mcpError(nil, mcpInvalidRequest, "not a JSON-RPC request object", nil)
	}
	rawID, hasID := msg["id"]
	method, isMethod := msg["method"].(string)
	if !hasID && isMethod {
		return nil // a notification: never answered
	}
	id, ok := mcpRequestID(rawID)
	if !ok || msg["jsonrpc"] != "2.0" || !isMethod {
		return mcpError(nil, mcpInvalidRequest, "invalid JSON-RPC request", nil)
	}
	params := map[string]any{}
	if p, present := msg["params"]; present {
		if params, ok = p.(map[string]any); !ok {
			return mcpError(id, mcpInvalidParams, "params is not an object", nil)
		}
	}
	if method == "server/discover" {
		return s.result(id, map[string]any{
			"supportedVersions": []any{MCPProtocolVersion},
			"capabilities":      map[string]any{"tools": map[string]any{"listChanged": false}},
			"ttlMs":             mcpListTTL,
			"cacheScope":        "public",
		})
	}
	if method != "tools/list" && method != "tools/call" {
		return mcpError(id, mcpMethodNotFound, "method not found", nil)
	}
	// Every request carries its protocol version and client capabilities
	// (MCP Overview §_meta). They are carriage: read to check the version,
	// never passed on and never an identity (profile §11.4.1 rule 4).
	meta, _ := params["_meta"].(map[string]any)
	version, versioned := meta[mcpMetaProtocolVersion].(string)
	if _, declared := meta[mcpMetaClientCapabilities].(map[string]any); !versioned || !declared {
		return mcpError(id, mcpInvalidParams, "_meta lacks the protocol version or client capabilities", nil)
	}
	if version != MCPProtocolVersion {
		return mcpError(id, mcpUnsupportedProtocolVersion, "unsupported protocol version",
			map[string]any{"supported": []any{MCPProtocolVersion}, "requested": version})
	}
	if method == "tools/list" {
		return s.result(id, map[string]any{"tools": mcpTools, "ttlMs": mcpListTTL, "cacheScope": "public"})
	}
	name, _ := params["name"].(string)
	arguments := map[string]any{}
	if a, present := params["arguments"]; present {
		if arguments, ok = a.(map[string]any); !ok {
			return mcpError(id, mcpInvalidParams, "arguments is not an object", nil)
		}
	}
	// inputResponses and requestState are carriage (rule 4): a retry is the
	// same request, answered from its arguments alone.
	switch name {
	case MCPToolManifest:
		if len(arguments) != 0 {
			return mcpError(id, mcpInvalidParams, "evidence_manifest takes no arguments", nil)
		}
		return s.result(id, map[string]any{"content": []any{}, "isError": false, "structuredContent": s.manifest})
	case MCPToolRequest:
		request, ok := mcpRequestArguments(arguments)
		if !ok {
			// Not a request under §11.4.5: no request octets, so no request
			// digest for a reply to bind. Never an envelope or a refusal.
			return mcpError(id, mcpInvalidParams, "arguments do not match the evidence_request input schema", nil)
		}
		reply, _, err := s.book.Respond(ctx, request, RespondOptions{AuthenticatedRequester: authenticated, Policy: s.policy})
		if err != nil {
			// The responder sends no reply; to the requester this is a
			// transport condition (ER §4.4), never a refusal.
			return mcpError(id, mcpInternalError, "internal error", nil)
		}
		return s.result(id, map[string]any{
			"content": []any{},
			"isError": false,
			"structuredContent": map[string]any{
				"reply":      base64.RawURLEncoding.EncodeToString(reply.Octets),
				"media_type": reply.MediaType,
			},
		})
	}
	return mcpError(id, mcpInvalidParams, "unknown tool", nil)
}

// mcpRequestArguments reads the evidence_request arguments under its input
// schema: exactly request and media_type, request base64url without padding.
// The request is the decoded octets exactly; nothing is parsed or
// re-encoded here (profile §11.4.1 rule 1).
func mcpRequestArguments(arguments map[string]any) (Message, bool) {
	if len(arguments) != 2 {
		return Message{}, false
	}
	text, ok := arguments["request"].(string)
	mediaType, typed := arguments["media_type"].(string)
	if !ok || !typed || !mcpOpaque.MatchString(text) || (mediaType != MediaTypeCBOR && mediaType != MediaTypeJSON) {
		return Message{}, false
	}
	octets, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil {
		return Message{}, false
	}
	return Message{Octets: octets, MediaType: mediaType}, true
}

// maxMessage bounds a received message: a request of the responder's
// largest size in base64url, with room for the JSON-RPC wrapping and _meta.
// A larger request inside that bound still reaches Respond, which refuses it.
func (s *MCPServer) maxMessage() int {
	limit := s.book.responder.MaxRequestBytes
	if limit == 0 {
		limit = DefaultMaxRequestBytes
	}
	return 2*limit + 64<<10
}

// mcpRequestID accepts a JSON-RPC id that MCP allows: a string or an
// integer, never null. An integer id is echoed as its token text.
func mcpRequestID(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case evidencerequest.JSONNumber:
		if mcpInteger.MatchString(string(x)) {
			return json.Number(x), true
		}
	}
	return nil, false
}

var mcpInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,17})$`)

func (s *MCPServer) result(id any, result map[string]any) []byte {
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{mcpMetaServerInfo: s.serverInfo}
	return mcpMarshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func mcpError(id any, code int, message string, data any) []byte {
	e := map[string]any{"code": code, "message": message}
	if data != nil {
		e["data"] = data
	}
	return mcpMarshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func mcpMarshal(v any) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		// Every value marshalled here is built from strings, maps, slices,
		// integers and the book's own JCS manifest, so this is unreachable;
		// it is still never an answer.
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error"}}`)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
