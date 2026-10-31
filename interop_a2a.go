// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// The A2A carriage of the exchange (profile §11.4.4), over A2A 1.0 in its
// JSON-RPC binding.
const (
	// A2AProtocolVersion is the one A2A-Version this carriage speaks.
	A2AProtocolVersion = "1.0"
	// A2AExtensionURI is the extension the exchange is advertised and
	// activated under (P-37): the request draft's Datatracker URL.
	A2AExtensionURI = "https://datatracker.ietf.org/doc/draft-mih-agent-evidence-request/"
	// A2AProtocolBinding is the Agent Card protocolBinding this carriage
	// serves.
	A2AProtocolBinding = "JSONRPC"
)

// A2A error codes in the JSON-RPC binding (A2A §5.4). Like the JSON-RPC
// codes, every one of them is a transport condition to the requester, never
// a refusal (profile §11.4.1 rules 5 and 6).
const (
	a2aTaskNotFound            = -32001
	a2aPushNotSupported        = -32003
	a2aUnsupportedOperation    = -32004
	a2aContentTypeNotSupported = -32005
	a2aVersionNotSupported     = -32009
)

const (
	a2aDescription             = "Answers evidence requests (draft-mih-agent-evidence-request, profile evidence-interop/0 or evidence-interop/1). A request is one Message with one raw Part holding the request octets; the reply is one task Artifact with one raw Part holding a signed response envelope or a signed refusal, in the request's encoding. It says what can be asked, never what exists."
	a2aDefaultAgentDescription = "An evidence responder: it answers evidence requests about its own records with signed, verifiable replies."
	a2aTaskStateCompleted      = "TASK_STATE_COMPLETED"
	a2aRoleUser                = "ROLE_USER"
)

// a2aBase64 is ProtoJSON bytes as A2A's JSON bindings write them (A2A §5.5):
// base64 in the standard or URL-safe alphabet, padded or not.
var a2aBase64 = regexp.MustCompile(`^[A-Za-z0-9+/_-]*={0,2}$`)

// A2AOptions configures the A2A carriage of a book's responder.
type A2AOptions struct {
	// Manifest is what the host states about its carriers; Bindings must
	// list BindingA2A. The Agent Card's extension params are
	// Book.Manifest(Manifest), byte for byte the manifest served elsewhere.
	Manifest ManifestOptions
	// Policy is the sharing policy every request is answered under, as for
	// RespondOptions.Policy.
	Policy SharePolicy
	// Name and URL are the Agent Card's name and the JSON-RPC endpoint the
	// host serves Handle at. Both are required.
	Name, URL string
	// Description is the Agent Card's description; empty is a fixed default.
	Description string
	// Version is the Agent Card's version; empty is "unversioned".
	Version string
}

// A2ARequest is one JSON-RPC message received over A2A with the service
// parameters it arrived with (A2A §3.2.6): the A2A-Version value ("" when
// absent) and the A2A-Extensions URIs, which the host splits from their
// comma-separated header or metadata entry.
type A2ARequest struct {
	Version    string
	Extensions []string
	Body       []byte
}

// A2AResponse is the JSON-RPC message to send back, and the extensions this
// carriage activated, to send as the response's A2A-Extensions. Body is nil
// for a notification.
type A2AResponse struct {
	Body       []byte
	Extensions []string
}

// A2AServer carries the evidence request exchange over A2A (profile
// §11.4.4): the Agent Card extension entry, and SendMessage answered with a
// completed Task holding the reply in one Artifact. It is a carriage only.
// It moves the request and reply octets as opaque bytes and adds nothing to
// what Respond answers: the canonical Artifact is the one the native stream
// and MCP get for the same request (FR-8, ER §5).
//
// It keeps no task. The Task is returned whole, completed, in the
// SendMessage response; no other client can list or fetch it (§11.4.3), and
// GetTask and CancelTask find nothing. Its identifiers are random, encoding
// no count and no time. It does not stream (capabilities.streaming false)
// and offers no push notifications and no extended Agent Card.
//
// It is transport-neutral: the host owns the HTTP endpoint, the Agent Card
// URL and their authentication.
type A2AServer struct {
	book     *Book
	manifest json.RawMessage
	policy   SharePolicy
	card     []byte
}

// A2A returns the A2A carriage of this book's responder.
func (b *Book) A2A(opts A2AOptions) (*A2AServer, error) {
	if !slices.Contains(opts.Manifest.Bindings, BindingA2A) {
		return nil, fmt.Errorf("%w: a manifest served over A2A lists the %q binding", ErrInvalid, BindingA2A)
	}
	if opts.Name == "" || opts.URL == "" {
		return nil, fmt.Errorf("%w: an Agent Card names the agent and its endpoint URL", ErrInvalid)
	}
	if err := opts.Policy.Validate(); err != nil {
		return nil, err
	}
	manifest, err := b.Manifest(opts.Manifest)
	if err != nil {
		return nil, err
	}
	description, version := opts.Description, opts.Version
	if description == "" {
		description = a2aDefaultAgentDescription
	}
	if version == "" {
		version = "unversioned"
	}
	modes := []string{MediaTypeCBOR, MediaTypeJSON}
	card := map[string]any{
		"name":        opts.Name,
		"description": description,
		"version":     version,
		"supportedInterfaces": []any{map[string]any{
			"url": opts.URL, "protocolBinding": A2AProtocolBinding, "protocolVersion": A2AProtocolVersion,
		}},
		"capabilities": map[string]any{
			"streaming": false, "pushNotifications": false, "extendedAgentCard": false,
			"extensions": []any{map[string]any{
				"uri": A2AExtensionURI, "description": a2aDescription, "required": false, "params": json.RawMessage(manifest),
			}},
		},
		"defaultInputModes":  modes,
		"defaultOutputModes": modes,
		// No AgentSkill: the extension entry advertises the exchange (P-37).
		"skills": []any{},
	}
	return &A2AServer{book: b, manifest: manifest, policy: opts.Policy, card: mcpMarshal(card)}, nil
}

// AgentCard returns the Agent Card to serve at the host's well-known URL. It
// is the same for every caller (§11.4.2, §11.4.3): it carries the manifest
// as the extension's params and nothing about what the book holds. A host
// that adds security schemes adds them to this JSON.
func (s *A2AServer) AgentCard() []byte { return slices.Clone(s.card) }

// Handle answers one JSON-RPC message received over A2A.
//
// authenticated is the requester identity the host's transport established
// through the A2A security scheme it enforces, exactly as for
// RespondOptions.AuthenticatedRequester, or "" when the caller is not
// authenticated. It is the only identity Respond sees. Nothing inside the
// message is ever read as one: not the Agent Card the client presents or
// claims, not the Message or request metadata, messageId, contextId, tenant
// or extensions, not the JSON-RPC id. As in Respond, authentication decides
// whether a subject is answered, never what is answered (profile §11.4.1
// rule 5).
//
// Every reply, envelope or refusal, is a completed Task holding one Artifact
// with the extension URI and one raw Part of the request's media type (P-39).
// Anything that is not a request under §11.4.4 gets a JSON-RPC or A2A error,
// which is a transport condition and never a refusal.
func (s *A2AServer) Handle(ctx context.Context, req A2ARequest, authenticated string) A2AResponse {
	if len(req.Body) > s.maxMessage() {
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
		return A2AResponse{} // a notification: never answered
	}
	id, ok := mcpRequestID(rawID)
	if !ok || msg["jsonrpc"] != "2.0" || !isMethod {
		return A2AResponse{Body: mcpError(nil, mcpInvalidRequest, "invalid JSON-RPC request", nil)}
	}
	fail := func(code int, message string, data any) A2AResponse {
		return A2AResponse{Body: mcpError(id, code, message, data)}
	}
	// Without A2A-Version a request is read as protocol 0.3 (A2A §3.6.2),
	// which this carriage does not speak.
	if req.Version != A2AProtocolVersion {
		return fail(a2aVersionNotSupported, "unsupported A2A version",
			map[string]any{"supported": []any{A2AProtocolVersion}, "requested": req.Version})
	}
	params := map[string]any{}
	if p, present := msg["params"]; present {
		if params, ok = p.(map[string]any); !ok {
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
		// No task is kept, so none is visible to anyone (§11.4.3).
		return A2AResponse{Body: mcpMarshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
			"tasks": []any{}, "nextPageToken": "", "pageSize": 0, "totalSize": 0}})}
	case "CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig",
		"ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
		return fail(a2aPushNotSupported, "push notifications are not supported", nil)
	default:
		return fail(mcpMethodNotFound, "method not found", nil)
	}
	// The exchange is opted into per request: the client activates the
	// extension in A2A-Extensions (P-38). Without it there is no request
	// under this profile to answer.
	if !slices.Contains(req.Extensions, A2AExtensionURI) {
		return fail(mcpInvalidParams, "the evidence request extension is not activated in A2A-Extensions", nil)
	}
	message, ok := params["message"].(map[string]any)
	if !ok {
		return fail(mcpInvalidParams, "params has no message", nil)
	}
	if _, continues := message["taskId"]; continues {
		// A request starts its own task (P-38); a follow-up carries no
		// request octets, and no task is kept to continue.
		return fail(a2aTaskNotFound, "task not found", nil)
	}
	request, code := a2aRequestMessage(message)
	if code != 0 {
		// Not a request under §11.4.4: no request octets, so no request
		// digest for a reply to bind. Never an envelope or a refusal.
		return fail(code, "the message is not an evidence request under the extension", nil)
	}
	// configuration and metadata are carriage (rule 4). Of the
	// configuration, only what would stop the reply being delivered is read.
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
			// The reply is in the request's encoding (rule 3) and nothing
			// else; a client that will not accept it gets no reply.
			if len(list) > 0 && !slices.Contains(list, any(request.MediaType)) {
				return fail(a2aContentTypeNotSupported, "the reply is only in the request's media type", nil)
			}
		}
	}
	reply, _, err := s.book.Respond(ctx, request, RespondOptions{AuthenticatedRequester: authenticated, Policy: s.policy})
	if err != nil {
		// The responder sends no reply; to the requester this is a transport
		// condition (ER §4.4), never a refusal.
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
			"extensions": []any{A2AExtensionURI},
			"parts": []any{map[string]any{
				"raw":       base64.StdEncoding.EncodeToString(reply.Octets),
				"mediaType": reply.MediaType,
			}},
		}},
	}
	return A2AResponse{
		Body:       mcpMarshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"task": task}}),
		Extensions: []string{A2AExtensionURI},
	}
}

// a2aRequestMessage reads a SendMessage Message under §11.4.4 (P-38): role
// ROLE_USER, the extension URI in its extensions, exactly one Part whose
// content is raw and whose mediaType is one of the two. The request is the
// decoded octets exactly; nothing is parsed or re-encoded here (profile
// §11.4.1 rule 1). It returns the A2A error code for anything else.
func a2aRequestMessage(message map[string]any) (Message, int) {
	extensions, _ := message["extensions"].([]any)
	parts, _ := message["parts"].([]any)
	if message["role"] != a2aRoleUser || !slices.Contains(extensions, any(A2AExtensionURI)) || len(parts) != 1 {
		return Message{}, mcpInvalidParams
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		return Message{}, mcpInvalidParams
	}
	for _, other := range []string{"text", "url", "data"} {
		if _, present := part[other]; present {
			return Message{}, mcpInvalidParams // a Part holds one content
		}
	}
	text, ok := part["raw"].(string)
	if !ok {
		return Message{}, mcpInvalidParams
	}
	mediaType, _ := part["mediaType"].(string)
	if mediaType != MediaTypeCBOR && mediaType != MediaTypeJSON {
		// An unknown media type leaves the encoding unknown (P-38).
		return Message{}, a2aContentTypeNotSupported
	}
	octets, ok := a2aDecodeBytes(text)
	if !ok || len(octets) == 0 {
		return Message{}, mcpInvalidParams
	}
	return Message{Octets: octets, MediaType: mediaType}, 0
}

// a2aDecodeBytes decodes ProtoJSON bytes (A2A §5.5): base64 in either
// alphabet, not mixed, padded correctly or not at all.
func a2aDecodeBytes(text string) ([]byte, bool) {
	if !a2aBase64.MatchString(text) {
		return nil, false
	}
	body := strings.TrimRight(text, "=")
	if body != text && len(text)%4 != 0 {
		return nil, false
	}
	if strings.ContainsAny(body, "+/") && strings.ContainsAny(body, "-_") {
		return nil, false
	}
	octets, err := base64.RawURLEncoding.Strict().DecodeString(strings.NewReplacer("+", "-", "/", "_").Replace(body))
	return octets, err == nil
}

// a2aIdentifier mints a task, context or artifact identifier: 128 random
// bits, so it encodes no count and no time (§11.4.3).
func a2aIdentifier() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b[:])
}

// maxMessage bounds a received message as for MCP: the responder's largest
// request in base64, with room for the JSON-RPC wrapping.
func (s *A2AServer) maxMessage() int {
	limit := s.book.responder.MaxRequestBytes
	if limit == 0 {
		limit = DefaultMaxRequestBytes
	}
	return 2*limit + 64<<10
}
