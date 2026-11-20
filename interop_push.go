// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// The MCP and A2A entries of Shelf.Receive: the carriage of the record push
// and its answers (agent-action-capsule spec/bindings/record-push-v0.md,
// cited as RP; spec/shelf-forms-v0.md, cited as SF), evidencebook#577. Like
// the evidence request carriage (interop_mcp.go, interop_a2a.go) they are
// carriage only: they move a record's octets and a stamp proof's JSON octets
// as opaque bytes, hand them to Receive, and return Receive's answer.
const (
	// MCPToolRecordPush is the one tool a receiver that takes pushes over MCP
	// lists (RP §3.1).
	MCPToolRecordPush = "record_push"
	// A2APushExtensionURI is the extension a push is advertised and activated
	// under (RP §4.1). It is an identifier: it resolves only once the
	// Internet-Draft it names is posted, and none is.
	A2APushExtensionURI = "https://datatracker.ietf.org/doc/draft-mih-agent-record-push/"
	// The media type of the push and answer data parts (RP §4.2, §4.3).
	mediaTypeApplicationJSON = "application/json"
)

const (
	pushToolDescription = "Carries one record push (a pushed record, a stamp proof, or both) as opaque octets in base64url without padding, and returns the receiver's answer for each part pushed. The answer is an acknowledgment, never evidence of what the receiver holds."
	pushA2ADescription  = "Takes record pushes. A push is one Message with one data Part holding {\"record\", \"stamp_proof\"}, each base64url octets without padding; the answer is one task Artifact with one data Part holding the answer for each part pushed. The answer is an acknowledgment, never evidence of what the receiver holds."
)

// pushTool is the record_push tool definition: fixed text and the RP §3.1
// schemas, the same for every caller and carrying nothing about what the
// shelf holds (RP §2.3 rule 7). No property carries x-mcp-header.
var pushTool = map[string]any{
	"name":        MCPToolRecordPush,
	"title":       "Record push",
	"description": pushToolDescription,
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"record":      map[string]any{"type": "string", "pattern": mcpOpaque.String()},
			"stamp_proof": map[string]any{"type": "string", "pattern": mcpOpaque.String()},
		},
		"minProperties":        1,
		"additionalProperties": false,
	},
	"outputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"record": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"digest":       map[string]any{"type": "string"},
					"stored":       map[string]any{"type": "boolean"},
					"duplicate":    map[string]any{"type": "boolean"},
					"deleted":      map[string]any{"type": "boolean"},
					"counted":      map[string]any{"type": "boolean"},
					"ask_for_book": map[string]any{"type": "boolean"},
					"refused":      map[string]any{"enum": []any{"", "invalid", "refused", "over_limit", "busy"}},
				},
				"required":             []any{"digest", "stored", "duplicate", "deleted", "counted", "ask_for_book", "refused"},
				"additionalProperties": false,
			},
			"stamp_proof": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"accepted":  map[string]any{"type": "boolean"},
					"duplicate": map[string]any{"type": "boolean"},
					"refused":   map[string]any{"enum": []any{"", "invalid", "refused"}},
				},
				"required":             []any{"accepted", "duplicate", "refused"},
				"additionalProperties": false,
			},
		},
		"minProperties":        1,
		"additionalProperties": false,
	},
}

// PushMCPServer carries the record push over MCP (RP §3): the tool
// record_push, and the server/discover and tools/list requests MCP requires
// around it. It is transport-neutral: Handle takes one received JSON-RPC
// message and returns the message to send back. It takes no transport
// identity: who may push is the receive profile's push rule (SF-6), which
// reads the sender from the record, never from the carriage (RP §2.3 rule 2).
type PushMCPServer struct {
	shelf      *Shelf
	serverInfo map[string]any
}

// PushMCP returns the MCP entry of Receive for this shelf. serverVersion is
// the version in the serverInfo each result carries; empty is "unversioned".
func (s *Shelf) PushMCP(serverVersion string) *PushMCPServer {
	if serverVersion == "" {
		serverVersion = "unversioned"
	}
	return &PushMCPServer{shelf: s, serverInfo: map[string]any{"name": "evidencebook", "version": serverVersion}}
}

// Handle answers one JSON-RPC message received over MCP and returns the
// JSON-RPC message to send back, or nil for a notification. Anything that is
// not a push under RP §3 gets a JSON-RPC error, a transport condition and
// never an answer. A push the receiver could not complete for a reason of
// its own gets an internal error and no answer (RP §2.3 rule 4).
func (p *PushMCPServer) Handle(ctx context.Context, message []byte) []byte {
	if len(message) > p.shelf.pushMaxMessage() {
		return mcpError(nil, mcpInvalidRequest, "message too large", nil)
	}
	root, err := evidencerequest.DecodeJSON(message)
	if err != nil {
		return mcpError(nil, mcpParseError, "parse error", nil)
	}
	msg, ok := root.(map[string]any)
	if !ok {
		return mcpError(nil, mcpInvalidRequest, "not a JSON-RPC request object", nil)
	}
	rawID, hasID := msg["id"]
	method, isMethod := msg["method"].(string)
	if !hasID && isMethod {
		return nil
	}
	id, ok := mcpRequestID(rawID)
	if !ok || msg["jsonrpc"] != "2.0" || !isMethod {
		return mcpError(nil, mcpInvalidRequest, "invalid JSON-RPC request", nil)
	}
	params := map[string]any{}
	if v, present := msg["params"]; present {
		if params, ok = v.(map[string]any); !ok {
			return mcpError(id, mcpInvalidParams, "params is not an object", nil)
		}
	}
	if method == "server/discover" {
		return p.result(id, map[string]any{
			"supportedVersions": []any{MCPProtocolVersion},
			"capabilities":      map[string]any{"tools": map[string]any{"listChanged": false}},
			"ttlMs":             mcpListTTL,
			"cacheScope":        "public",
		})
	}
	if method != "tools/list" && method != "tools/call" {
		return mcpError(id, mcpMethodNotFound, "method not found", nil)
	}
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
		return p.result(id, map[string]any{"tools": []any{pushTool}, "ttlMs": mcpListTTL, "cacheScope": "public"})
	}
	if name, _ := params["name"].(string); name != MCPToolRecordPush {
		return mcpError(id, mcpInvalidParams, "unknown tool", nil)
	}
	arguments := map[string]any{}
	if a, present := params["arguments"]; present {
		if arguments, ok = a.(map[string]any); !ok {
			return mcpError(id, mcpInvalidParams, "arguments is not an object", nil)
		}
	}
	push, ok := readPush(arguments)
	if !ok {
		return mcpError(id, mcpInvalidParams, "arguments do not match the record_push input schema", nil)
	}
	answer, err := p.shelf.pushAnswer(ctx, push)
	if err != nil {
		return mcpError(id, mcpInternalError, "internal error", nil)
	}
	return p.result(id, map[string]any{"content": []any{}, "isError": false, "structuredContent": answer})
}

func (p *PushMCPServer) result(id any, result map[string]any) []byte {
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{mcpMetaServerInfo: p.serverInfo}
	return mcpMarshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// push is a decoded push object (RP §2.1): the octets of each part pushed.
type push struct {
	record []byte // nil when the push had no record
	proof  []byte // nil when the push had no stamp proof
}

// readPush reads a push object (RP §2.1) under the input schema: at least
// one of record and stamp_proof and no other member, each a base64url string
// without padding that decodes strictly to at least one octet.
func readPush(object map[string]any) (push, bool) {
	var p push
	if len(object) == 0 || len(object) > 2 {
		return p, false
	}
	for name, value := range object {
		text, ok := value.(string)
		if !ok || !mcpOpaque.MatchString(text) {
			return p, false
		}
		octets, err := base64.RawURLEncoding.Strict().DecodeString(text)
		if err != nil || len(octets) == 0 {
			return p, false
		}
		switch name {
		case "record":
			p.record = octets
		case "stamp_proof":
			p.proof = octets
		default:
			return p, false
		}
	}
	return p, true
}

// pushMaxMessage bounds a received message before it is parsed (RP §2.3
// rule 6): twice the receiver's record bound plus 64 KiB, so a record at the
// bound, with a stamp proof, still reaches Receive.
func (s *Shelf) pushMaxMessage() int {
	return int(2*s.cfg.Receive.Bounds.withDefaults().MaxRecordBytes) + 64<<10
}

// pushAnswer hands a push to Receive and builds the answer object (RP §2.2),
// one member for each part pushed. A part's refusal is an answer; anything
// else that went wrong is the receiver's own fault and gets no answer (RP
// §2.3 rule 4).
func (s *Shelf) pushAnswer(ctx context.Context, p push) (map[string]any, error) {
	var proof *StampProof
	if p.proof != nil {
		// The proof is read from the decoded octets by SF §5.2: members it
		// does not know are ignored and an absent or null one is its zero
		// value. Octets that are not that JSON read as an empty proof, which
		// SF §6.3 step 1 refuses as invalid.
		proof = &StampProof{}
		if err := json.Unmarshal(p.proof, proof); err != nil {
			proof = &StampProof{}
		}
	}
	result, err := s.Receive(ctx, p.record, proof)
	if (p.record != nil && result.Record == nil) || (p.proof != nil && result.Proof == nil) {
		return nil, err
	}
	if err != nil && !onlyRefusals(err, result) {
		return nil, err
	}
	answer := map[string]any{}
	if r := result.Record; r != nil {
		class, ok := refusalClass(r.Refused)
		if !ok {
			return nil, r.Refused
		}
		answer["record"] = map[string]any{"digest": r.Digest, "stored": r.Stored, "duplicate": r.Duplicate,
			"deleted": r.Deleted, "counted": r.Counted, "ask_for_book": r.AskForBook, "refused": class}
	}
	if o := result.Proof; o != nil {
		class, ok := refusalClass(o.Refused)
		if !ok || class == "over_limit" || class == "busy" {
			return nil, o.Refused
		}
		answer["stamp_proof"] = map[string]any{"accepted": o.Accepted, "duplicate": o.Duplicate, "refused": class}
	}
	return answer, nil
}

// onlyRefusals reports whether every error Receive joined is one part's
// refusal, as against a store, profile or after-receive failure.
func onlyRefusals(err error, result ReceiveResult) bool {
	var refusals []error
	if result.Record != nil && result.Record.Refused != nil {
		refusals = append(refusals, result.Record.Refused)
	}
	if result.Proof != nil && result.Proof.Refused != nil {
		refusals = append(refusals, result.Proof.Refused)
	}
	leaves := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		leaves = j.Unwrap()
	}
	for _, leaf := range leaves {
		if !slices.ContainsFunc(refusals, func(r error) bool { return errors.Is(r, leaf) }) {
			return false
		}
	}
	return true
}

// refusalClass names a part's refusal by its sentinel (SF §6.5); ok is false
// for an error that is not a refusal class.
func refusalClass(err error) (string, bool) {
	switch {
	case err == nil:
		return "", true
	case errors.Is(err, ErrRefused):
		return "refused", true
	case errors.Is(err, ErrOverLimit):
		return "over_limit", true
	case errors.Is(err, ErrBusy):
		return "busy", true
	case errors.Is(err, ErrInvalid):
		return "invalid", true
	}
	return "", false
}

// PushA2AExtension returns the Agent Card extension entry that advertises a
// push (RP §4.1), the same for every caller: a host that also serves the
// evidence request carriage adds it to the one card's capabilities.extensions.
func PushA2AExtension() []byte {
	return mcpMarshal(map[string]any{"uri": A2APushExtensionURI, "description": pushA2ADescription, "required": false})
}

// PushA2AServer carries the record push over A2A (RP §4): SendMessage
// answered with a completed Task holding the answer in one Artifact. It
// keeps no task, mints unpredictable identifiers, and takes no transport
// identity, as PushMCPServer.
type PushA2AServer struct {
	shelf *Shelf
}

// PushA2A returns the A2A entry of Receive for this shelf.
func (s *Shelf) PushA2A() *PushA2AServer { return &PushA2AServer{shelf: s} }

// Handle answers one JSON-RPC message received over A2A. It speaks the
// methods of A2AServer.Handle; of those only SendMessage carries a push.
func (p *PushA2AServer) Handle(ctx context.Context, req A2ARequest) A2AResponse {
	if len(req.Body) > p.shelf.pushMaxMessage() {
		return A2AResponse{Body: mcpError(nil, mcpInvalidRequest, "message too large", nil)}
	}
	root, err := evidencerequest.DecodeJSON(req.Body)
	if err != nil {
		return A2AResponse{Body: mcpError(nil, mcpParseError, "parse error", nil)}
	}
	msg, ok := root.(map[string]any)
	if !ok {
		return A2AResponse{Body: mcpError(nil, mcpInvalidRequest, "not a JSON-RPC request object", nil)}
	}
	rawID, hasID := msg["id"]
	method, isMethod := msg["method"].(string)
	if !hasID && isMethod {
		return A2AResponse{}
	}
	id, ok := mcpRequestID(rawID)
	if !ok || msg["jsonrpc"] != "2.0" || !isMethod {
		return A2AResponse{Body: mcpError(nil, mcpInvalidRequest, "invalid JSON-RPC request", nil)}
	}
	fail := func(code int, message string, data any) A2AResponse {
		return A2AResponse{Body: mcpError(id, code, message, data)}
	}
	if req.Version != A2AProtocolVersion {
		return fail(a2aVersionNotSupported, "unsupported A2A version",
			map[string]any{"supported": []any{A2AProtocolVersion}, "requested": req.Version})
	}
	params := map[string]any{}
	if v, present := msg["params"]; present {
		if params, ok = v.(map[string]any); !ok {
			return fail(mcpInvalidParams, "params is not an object", nil)
		}
	}
	switch method {
	case "SendMessage":
	case "SendStreamingMessage", "SubscribeToTask", "GetExtendedAgentCard":
		return fail(a2aUnsupportedOperation, "not supported by this agent", nil)
	case "GetTask", "CancelTask":
		return fail(a2aTaskNotFound, "task not found", nil)
	case "ListTasks":
		return A2AResponse{Body: mcpMarshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
			"tasks": []any{}, "nextPageToken": "", "pageSize": 0, "totalSize": 0}})}
	case "CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig",
		"ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
		return fail(a2aPushNotSupported, "push notifications are not supported", nil)
	default:
		return fail(mcpMethodNotFound, "method not found", nil)
	}
	if !slices.Contains(req.Extensions, A2APushExtensionURI) {
		return fail(mcpInvalidParams, "the record push extension is not activated in A2A-Extensions", nil)
	}
	message, ok := params["message"].(map[string]any)
	if !ok {
		return fail(mcpInvalidParams, "params has no message", nil)
	}
	if _, continues := message["taskId"]; continues {
		return fail(a2aTaskNotFound, "task not found", nil)
	}
	pushed, code := a2aPushMessage(message)
	if code != 0 {
		return fail(code, "the message is not a record push under the extension", nil)
	}
	if configuration, present := params["configuration"]; present {
		c, ok := configuration.(map[string]any)
		if !ok {
			return fail(mcpInvalidParams, "configuration is not an object", nil)
		}
		if _, push := c["taskPushNotificationConfig"]; push {
			return fail(a2aPushNotSupported, "push notifications are not supported", nil)
		}
		if modes, present := c["acceptedOutputModes"]; present {
			list, ok := modes.([]any)
			if !ok {
				return fail(mcpInvalidParams, "acceptedOutputModes is not an array", nil)
			}
			if len(list) > 0 && !slices.Contains(list, any(mediaTypeApplicationJSON)) {
				return fail(a2aContentTypeNotSupported, "the answer is only application/json", nil)
			}
		}
	}
	answer, err := p.shelf.pushAnswer(ctx, pushed)
	if err != nil {
		return fail(mcpInternalError, "internal error", nil)
	}
	contextID, _ := message["contextId"].(string)
	if contextID == "" {
		contextID = a2aIdentifier()
	}
	task := map[string]any{
		"id":        a2aIdentifier(),
		"contextId": contextID,
		"status":    map[string]any{"state": a2aTaskStateCompleted},
		"artifacts": []any{map[string]any{
			"artifactId": a2aIdentifier(),
			"extensions": []any{A2APushExtensionURI},
			"parts":      []any{map[string]any{"data": answer, "mediaType": mediaTypeApplicationJSON}},
		}},
	}
	return A2AResponse{
		Body:       mcpMarshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"task": task}}),
		Extensions: []string{A2APushExtensionURI},
	}
}

// a2aPushMessage reads a SendMessage Message under RP §4.2: role ROLE_USER,
// the extension URI in its extensions, exactly one Part whose content is
// data, of media type application/json, holding a push object. It returns
// the A2A error code for anything else.
func a2aPushMessage(message map[string]any) (push, int) {
	extensions, _ := message["extensions"].([]any)
	parts, _ := message["parts"].([]any)
	if message["role"] != a2aRoleUser || !slices.Contains(extensions, any(A2APushExtensionURI)) || len(parts) != 1 {
		return push{}, mcpInvalidParams
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		return push{}, mcpInvalidParams
	}
	for _, other := range []string{"text", "url", "raw"} {
		if _, present := part[other]; present {
			return push{}, mcpInvalidParams
		}
	}
	object, ok := part["data"].(map[string]any)
	if !ok {
		return push{}, mcpInvalidParams
	}
	if mediaType, _ := part["mediaType"].(string); mediaType != mediaTypeApplicationJSON {
		return push{}, a2aContentTypeNotSupported
	}
	pushed, ok := readPush(object)
	if !ok {
		return push{}, mcpInvalidParams
	}
	return pushed, 0
}
