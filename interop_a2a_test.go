// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// Tests for the A2A carriage (profile §11.4.4): the Agent Card extension
// entry, one raw Part in and one Artifact out, the canonical Artifact
// byte-identical to the native stream's and MCP's, and authorization only
// from the host's authentication, never from anything A2A carries.

var a2aBindings = []string{BindingStream, BindingA2A, BindingMCP}

func a2aServer(t *testing.T, book *Book, policy SharePolicy) *A2AServer {
	t.Helper()
	server, err := book.A2A(A2AOptions{Manifest: ManifestOptions{Bindings: a2aBindings}, Policy: policy,
		Name: "responder", URL: "https://responder.example/a2a"})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// a2aMessage is the §11.4.4 request Message for m.
func a2aMessage(m Message) map[string]any {
	return map[string]any{
		"messageId":  "m-1",
		"role":       "ROLE_USER",
		"extensions": []any{A2AExtensionURI},
		"parts":      []any{map[string]any{"mediaType": m.MediaType, "raw": base64.StdEncoding.EncodeToString(m.Octets)}},
	}
}

// a2aSend sends one SendMessage with the §11.4.4 service parameters and
// decodes the response.
func a2aSend(t *testing.T, server *A2AServer, params map[string]any, authenticated string) (map[string]any, A2AResponse) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "SendMessage", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	response := server.Handle(context.Background(), A2ARequest{Version: "1.0", Extensions: []string{A2AExtensionURI}, Body: data}, authenticated)
	return mcpDecode(t, response.Body), response
}

var a2aID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// a2aReplyOf reads the reply out of a SendMessage response after checking
// it is the §11.4.4 shape: a completed Task, one Artifact with the
// extension URI, one raw Part of the request's media type.
func a2aReplyOf(t *testing.T, response map[string]any, mediaType string) (Reply, map[string]any) {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok || len(result) != 1 {
		t.Fatalf("no result holding only a task: %v", response)
	}
	task, ok := result["task"].(map[string]any)
	if !ok {
		t.Fatalf("a reply is a Task, never a direct Message: %v", result)
	}
	status, _ := task["status"].(map[string]any)
	artifacts, _ := task["artifacts"].([]any)
	if status["state"] != "TASK_STATE_COMPLETED" || len(artifacts) != 1 {
		t.Fatalf("a reply is one Artifact in a completed task: %v", task)
	}
	artifact := artifacts[0].(map[string]any)
	parts, _ := artifact["parts"].([]any)
	if !jsonEqual(t, artifact["extensions"], `["`+A2AExtensionURI+`"]`) || len(parts) != 1 {
		t.Fatalf("the reply Artifact names the extension and holds one Part: %v", artifact)
	}
	// The identifiers this carriage mints (a contextId the client sent is
	// echoed, and checked where none was sent).
	for _, id := range []any{task["id"], artifact["artifactId"]} {
		if s, _ := id.(string); !a2aID.MatchString(s) {
			t.Fatalf("identifier %v is not 128 random bits", id)
		}
	}
	part := parts[0].(map[string]any)
	text, _ := part["raw"].(string)
	if len(part) != 2 || part["mediaType"] != mediaType {
		t.Fatalf("the reply Part is raw, of the request's media type: %v", part)
	}
	octets, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := ParseReply(Message{Octets: octets, MediaType: mediaType})
	if err != nil {
		t.Fatal(err)
	}
	return reply, task
}

func a2aReply(t *testing.T, server *A2AServer, m Message, authenticated string) Reply {
	t.Helper()
	response, _ := a2aSend(t, server, map[string]any{"message": a2aMessage(m)}, authenticated)
	reply, _ := a2aReplyOf(t, response, m.MediaType)
	return reply
}

// §11.4.4 declaration: one AgentExtension under exactly the draft's URI,
// required false, params the manifest byte for byte (the one MCP serves),
// both media types as default modes, and the same card for everyone before
// and after any history (§11.4.2, §11.4.3).
func TestA2AAgentCardDeclaresTheExtension(t *testing.T) {
	book, records := linkedBook(t)
	server := a2aServer(t, book, DefaultSharePolicy())
	before := server.AgentCard()
	var card map[string]any
	if err := json.Unmarshal(before, &card); err != nil {
		t.Fatal(err)
	}
	capabilities := card["capabilities"].(map[string]any)
	extensions := capabilities["extensions"].([]any)
	if len(extensions) != 1 {
		t.Fatalf("extensions %v, want exactly the evidence request extension", extensions)
	}
	entry := extensions[0].(map[string]any)
	if entry["uri"] != "https://datatracker.ietf.org/doc/draft-mih-agent-evidence-request/" || entry["required"] != false {
		t.Fatalf("extension entry %v", entry)
	}
	params, err := evidencerequest.EncodeJCS(jsonAny(t, entry["params"]))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := book.Manifest(ManifestOptions{Bindings: a2aBindings})
	if err != nil || !bytes.Equal(params, direct) {
		t.Fatalf("extension params %s, the book's manifest is %s", params, direct)
	}
	mcp, err := book.MCP(MCPOptions{Manifest: ManifestOptions{Bindings: a2aBindings}, Policy: DefaultSharePolicy()})
	if err != nil {
		t.Fatal(err)
	}
	served := mcpCall(t, mcp, "tools/call", map[string]any{"name": MCPToolManifest, "arguments": map[string]any{}, "_meta": mcpMeta}, "")
	viaMCP, err := evidencerequest.EncodeJCS(jsonAny(t, served["result"].(map[string]any)["structuredContent"]))
	if err != nil || !bytes.Equal(viaMCP, params) {
		t.Fatalf("the A2A params %s and the evidence_manifest result %s differ", params, viaMCP)
	}
	for _, field := range []string{"defaultInputModes", "defaultOutputModes"} {
		if !jsonEqual(t, card[field], `["application/cbor", "application/json"]`) {
			t.Fatalf("%s %v", field, card[field])
		}
	}
	if capabilities["streaming"] != false || capabilities["extendedAgentCard"] != false || !jsonEqual(t, card["skills"], `[]`) {
		t.Fatalf("capabilities %v, skills %v", capabilities, card["skills"])
	}
	a2aReply(t, server, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}), "peer-1")
	if !bytes.Equal(server.AgentCard(), before) {
		t.Fatal("the Agent Card changed with history")
	}
	if _, err := book.A2A(A2AOptions{Manifest: ManifestOptions{Bindings: []string{BindingMCP}}, Policy: DefaultSharePolicy(), Name: "r", URL: "https://r.example"}); err == nil {
		t.Fatal("an A2A carriage whose manifest does not list a2a")
	}
}

// FR-8, ER §5, profile §11.4.1 rules 1-3 and §11.4.8: one request's octets
// sent over the native stream, A2A and MCP give the same request digest,
// the reply in the request's encoding, and a byte-identical canonical
// Artifact. Only the carriage differs.
func TestA2ACanonicalArtifactIsTheNativeOne(t *testing.T) {
	book, records := linkedBook(t)
	server := a2aServer(t, book, DefaultSharePolicy())
	mcp := mcpServer(t, book, DefaultSharePolicy())
	pin := anchorPin(t, book)
	for _, subject := range []Subject{{Kind: SubjectRecord, Digest: records[0].RecordID}, {Kind: SubjectFullHistory}} {
		for _, mediaType := range []string{MediaTypeCBOR, MediaTypeJSON} {
			t.Run(fmt.Sprintf("%s %s", subject.Kind, mediaType), func(t *testing.T) {
				r := requestIn(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: pin}, Nonce: "2026-10-12T00:00:10Z/c"}, mediaType)
				native := mustEnvelope(t, respondAs(t, book, r, "peer-1"))
				reply := a2aReply(t, server, r, "peer-1")
				carried := mustEnvelope(t, reply)
				overMCP := mustEnvelope(t, mcpReply(t, mcp, r, "peer-1"))
				if reply.MediaType != mediaType || carried.RequestDigest != digestBytes(r.Octets) || carried.RequestDigest != native.RequestDigest {
					t.Fatal("the A2A reply is bound to the request octets as received, in their encoding")
				}
				if !bytes.Equal(carried.Artifact, native.Artifact) || carried.ArtifactDigest != native.ArtifactDigest ||
					!bytes.Equal(carried.Artifact, overMCP.Artifact) {
					t.Fatal("the canonical Artifact differs between the native stream, A2A and MCP")
				}
				if !reflect.DeepEqual(carried.Anchor, native.Anchor) || !reflect.DeepEqual(carried.Subject, native.Subject) {
					t.Fatal("the anchor or subject differs between the native stream and A2A")
				}
			})
		}
	}
}

// ProtoJSON bytes (A2A §5.5): the request's raw part in either base64
// alphabet, padded or not, is the same octets, so the same request digest.
func TestA2ARawPartIsTheDecodedOctets(t *testing.T) {
	book, records := linkedBook(t)
	server := a2aServer(t, book, DefaultSharePolicy())
	pin := anchorPin(t, book)
	// A request whose base64 needs padding.
	var r Message
	for i := 0; ; i++ {
		r = requestIn(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: pin},
			Nonce: fmt.Sprintf("2026-10-12T00:00:10Z/%d", i)}, MediaTypeCBOR)
		if len(r.Octets)%3 != 0 {
			break
		}
	}
	// The decoder over octets every base64 character appears in.
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if got, ok := a2aDecodeBytes(encoding.EncodeToString(all)); !ok || !bytes.Equal(got, all) {
			t.Fatalf("%v does not decode to the octets", encoding)
		}
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		message := a2aMessage(r)
		message["parts"] = []any{map[string]any{"mediaType": r.MediaType, "raw": encoding.EncodeToString(r.Octets)}}
		response, _ := a2aSend(t, server, map[string]any{"message": message}, "peer-1")
		reply, _ := a2aReplyOf(t, response, r.MediaType)
		if mustEnvelope(t, reply).RequestDigest != digestBytes(r.Octets) {
			t.Fatal("the request digest is not over the decoded octets")
		}
	}
}

// §11.4.1 rules 4 and 5, NFR-4: an A2A identity is never authorization. A
// protected subject is answered only to the identity the host
// authenticated; an Agent Card name, metadata, a tenant, a contextId or a
// messageId naming a counterparty grants nothing. Whatever A2A carries, the
// reply is the one Respond gives the same authenticated requester.
func TestA2ADeclaredIdentityIsNeverAuthorization(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	pin := anchorPin(t, book)
	server := a2aServer(t, book, policyWith("counterparties"))
	r := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: pin}})
	message := a2aMessage(r)
	message["messageId"], message["contextId"] = "known-peer", "known-peer"
	message["metadata"] = map[string]any{"requester": "known-peer", "agentCard": map[string]any{"name": "known-peer"}}
	claimed := map[string]any{"message": message, "tenant": "known-peer",
		"metadata": map[string]any{"authenticated_requester": "known-peer", "requester_id": "known-peer"}}
	for name, authenticated := range map[string]string{"unauthenticated": "", "authenticated as another": "someone-else"} {
		t.Run(name, func(t *testing.T) {
			before := book.Size()
			for range 2 {
				response, _ := a2aSend(t, server, claimed, authenticated)
				reply, _ := a2aReplyOf(t, response, r.MediaType)
				mustRefusal(t, reply, ReasonNotAuthorized)
			}
			if authenticated == "" && book.Size() != before {
				t.Fatal("an unauthenticated A2A request grew the log")
			}
		})
	}
	response, _ := a2aSend(t, server, claimed, "known-peer")
	reply, _ := a2aReplyOf(t, response, r.MediaType)
	native := mustEnvelope(t, respondReply(t, book, r, RespondOptions{AuthenticatedRequester: "known-peer", Policy: policyWith("counterparties")}))
	if !bytes.Equal(mustEnvelope(t, reply).Artifact, native.Artifact) {
		t.Fatal("the authenticated counterparty gets another Artifact over A2A")
	}
	checkpoints := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{ExpectedPin: pin}})
	mustEnvelope(t, a2aReply(t, server, checkpoints, ""))
}

// §11.4.4 and rules 5 and 6: what is not a request under the profile gets a
// JSON-RPC or A2A error, never a result, so never an envelope, a refusal or
// a rejected task a requester could mistake for one. Nothing grows the log.
func TestA2ANonRequestsAreTransportConditions(t *testing.T) {
	book, records := linkedBook(t)
	server := a2aServer(t, book, DefaultSharePolicy())
	r := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}})
	good := base64.StdEncoding.EncodeToString(r.Octets)
	with := func(change func(m map[string]any)) map[string]any {
		m := a2aMessage(r)
		change(m)
		return map[string]any{"message": m}
	}
	part := func(p map[string]any) map[string]any {
		return with(func(m map[string]any) { m["parts"] = []any{p} })
	}
	send := func(method string, params map[string]any) string {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "x", "method": method, "params": params})
		return string(data)
	}
	sendMessage := func(params map[string]any) string { return send("SendMessage", params) }
	type call struct {
		body       string
		version    string
		extensions []string
		code       float64
	}
	ok := func(body string, code float64) call {
		return call{body, "1.0", []string{A2AExtensionURI}, code}
	}
	cases := map[string]call{
		"data part":               ok(sendMessage(part(map[string]any{"mediaType": r.MediaType, "data": map[string]any{"request": good}})), mcpInvalidParams),
		"text part":               ok(sendMessage(part(map[string]any{"mediaType": r.MediaType, "text": string(r.Octets)})), mcpInvalidParams),
		"raw and text":            ok(sendMessage(part(map[string]any{"mediaType": r.MediaType, "raw": good, "text": "x"})), mcpInvalidParams),
		"request in metadata":     ok(sendMessage(with(func(m map[string]any) { m["parts"] = []any{}; m["metadata"] = map[string]any{"request": good} })), mcpInvalidParams),
		"two parts":               ok(sendMessage(with(func(m map[string]any) { m["parts"] = append(m["parts"].([]any), m["parts"].([]any)[0]) })), mcpInvalidParams),
		"other media type":        ok(sendMessage(part(map[string]any{"mediaType": "application/octet-stream", "raw": good})), a2aContentTypeNotSupported),
		"media type parameter":    ok(sendMessage(part(map[string]any{"mediaType": r.MediaType + "; charset=utf-8", "raw": good})), a2aContentTypeNotSupported),
		"no media type":           ok(sendMessage(part(map[string]any{"raw": good})), a2aContentTypeNotSupported),
		"mixed alphabets":         ok(sendMessage(part(map[string]any{"mediaType": r.MediaType, "raw": "ab+_"})), mcpInvalidParams),
		"wrong padding":           ok(sendMessage(part(map[string]any{"mediaType": r.MediaType, "raw": "QQ="})), mcpInvalidParams),
		"empty raw":               ok(sendMessage(part(map[string]any{"mediaType": r.MediaType, "raw": ""})), mcpInvalidParams),
		"agent role":              ok(sendMessage(with(func(m map[string]any) { m["role"] = "ROLE_AGENT" })), mcpInvalidParams),
		"no extension on message": ok(sendMessage(with(func(m map[string]any) { delete(m, "extensions") })), mcpInvalidParams),
		"other extension URI":     ok(sendMessage(with(func(m map[string]any) { m["extensions"] = []any{"https://example.com/evidence"} })), mcpInvalidParams),
		"follow-up with taskId":   ok(sendMessage(with(func(m map[string]any) { m["taskId"] = "9a4627ada183be3ef16d81248033b008" })), a2aTaskNotFound),
		"output mode refused":     ok(sendMessage(map[string]any{"message": a2aMessage(r), "configuration": map[string]any{"acceptedOutputModes": []any{"text/plain"}}}), a2aContentTypeNotSupported),
		"push notifications":      ok(sendMessage(map[string]any{"message": a2aMessage(r), "configuration": map[string]any{"taskPushNotificationConfig": map[string]any{"url": "https://x.example"}}}), a2aPushNotSupported),
		"no message":              ok(sendMessage(map[string]any{}), mcpInvalidParams),
		"no A2A-Version":          {sendMessage(map[string]any{"message": a2aMessage(r)}), "", []string{A2AExtensionURI}, a2aVersionNotSupported},
		"A2A-Version 0.3":         {sendMessage(map[string]any{"message": a2aMessage(r)}), "0.3", []string{A2AExtensionURI}, a2aVersionNotSupported},
		"extension not activated": {sendMessage(map[string]any{"message": a2aMessage(r)}), "1.0", nil, mcpInvalidParams},
		"streaming":               ok(send("SendStreamingMessage", map[string]any{"message": a2aMessage(r)}), a2aUnsupportedOperation),
		"get task":                ok(send("GetTask", map[string]any{"id": "9a4627ada183be3ef16d81248033b008"}), a2aTaskNotFound),
		"cancel task":             ok(send("CancelTask", map[string]any{"id": "9a4627ada183be3ef16d81248033b008"}), a2aTaskNotFound),
		"extended card":           ok(send("GetExtendedAgentCard", map[string]any{}), a2aUnsupportedOperation),
		"v0.3 method name":        ok(send("message/send", map[string]any{"message": a2aMessage(r)}), mcpMethodNotFound),
		"duplicate member":        ok(`{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"role":"ROLE_USER","role":"ROLE_USER"}}}`, mcpParseError),
		"batch":                   ok(`[`+sendMessage(map[string]any{"message": a2aMessage(r)})+`]`, mcpInvalidRequest),
		"request octets as body":  ok(string(r.Octets), mcpInvalidRequest),
	}
	before := book.Size()
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := server.Handle(context.Background(), A2ARequest{Version: c.version, Extensions: c.extensions, Body: []byte(c.body)}, "peer-1")
			response := mcpDecode(t, out.Body)
			e, _ := response["error"].(map[string]any)
			if _, isResult := response["result"]; isResult || e == nil || e["code"] != c.code || out.Extensions != nil {
				t.Fatalf("want error %v, got %v", c.code, response)
			}
		})
	}
	if book.Size() != before {
		t.Fatal("a non-request grew the log")
	}
	if out := server.Handle(context.Background(), A2ARequest{Version: "1.0", Body: []byte(`{"jsonrpc":"2.0","method":"SendMessage","params":{}}`)}, "peer-1"); out.Body != nil {
		t.Fatalf("a notification was answered: %s", out.Body)
	}
}

// Rule 4 and §11.4.3: messageId, contextId, referenceTaskIds, tenant,
// configuration and metadata are carriage. The reply binds the same request
// digest and carries the same Artifact; the Task's identifiers are fresh
// each time; no task is kept for anyone to list or fetch.
func TestA2ACarriageMembersNeverReachTheAnswer(t *testing.T) {
	book, records := linkedBook(t)
	server := a2aServer(t, book, DefaultSharePolicy())
	r := requestIn(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[1].RecordID}, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}, MediaTypeCBOR)
	plainResponse, out := a2aSend(t, server, map[string]any{"message": a2aMessage(r)}, "peer-1")
	plainReply, plainTask := a2aReplyOf(t, plainResponse, r.MediaType)
	plain := mustEnvelope(t, plainReply)
	if s, _ := plainTask["contextId"].(string); !a2aID.MatchString(s) {
		t.Fatalf("minted contextId %v is not 128 random bits", plainTask["contextId"])
	}
	if !reflect.DeepEqual(out.Extensions, []string{A2AExtensionURI}) {
		t.Fatalf("activated extensions %v", out.Extensions)
	}
	message := a2aMessage(r)
	message["messageId"], message["contextId"] = "another", "ctx-1"
	message["referenceTaskIds"] = []any{"9a4627ada183be3ef16d81248033b008"}
	message["metadata"] = map[string]any{"trace": "x"}
	message["parts"].([]any)[0].(map[string]any)["filename"] = "request.cbor"
	response, _ := a2aSend(t, server, map[string]any{"message": message, "tenant": "t", "metadata": map[string]any{"k": "v"},
		"configuration": map[string]any{"acceptedOutputModes": []any{MediaTypeCBOR}, "historyLength": 3, "returnImmediately": true}}, "peer-1")
	reply, task := a2aReplyOf(t, response, r.MediaType)
	if again := mustEnvelope(t, reply); again.RequestDigest != plain.RequestDigest || !bytes.Equal(again.Artifact, plain.Artifact) {
		t.Fatal("carriage members changed the request digest or the Artifact")
	}
	if task["contextId"] != "ctx-1" || task["id"] == plainTask["id"] {
		t.Fatalf("task identifiers: %v then %v", plainTask["id"], task["id"])
	}
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ListTasks", "params": map[string]any{}})
	list := mcpDecode(t, server.Handle(context.Background(), A2ARequest{Version: "1.0", Body: data}, "peer-1").Body)
	if !jsonEqual(t, list["result"].(map[string]any)["tasks"], `[]`) {
		t.Fatalf("ListTasks: %v", list)
	}
	if strings.Contains(string(server.AgentCard()), plainTask["id"].(string)) {
		t.Fatal("the Agent Card names a task")
	}
}

// The profile's informative example (§11.4.4): request B in a JSON-RPC
// SendMessage. Whatever this book answers, the reply binds the digest of
// the 228 decoded octets, the same as over the native stream and MCP.
func TestA2AProfileExampleBindsRequestB(t *testing.T) {
	book, _ := linkedBook(t)
	server := a2aServer(t, book, DefaultSharePolicy())
	const example = `{"jsonrpc": "2.0", "id": 1, "method": "SendMessage", "params": {"message": {
  "messageId": "m-1", "role": "ROLE_USER",
  "extensions": ["https://datatracker.ietf.org/doc/draft-mih-agent-evidence-request/"],
  "parts": [{"mediaType": "application/cbor",
    "raw": "pGVub25jZXYyMDI2LTEwLTEyVDAwOjAwOjA1Wi9iZ3Byb2ZpbGVyZXZpZGVuY2UtaW50ZXJvcC8wZ3N1YmplY3ShZnJlY29yZHhAMmFiMzUwODk1Zjg2N2Q1NGE1Y2ZkYzFkZDFhYTIxNGNiYzcyM2M2NTk2NjM0NjBlNzUxMWRmZjE2YTVjY2M4NWhjb3ZlcmFnZaFsZXhwZWN0ZWRfcGlueEA2NDQxMzcwOTU1ZDJiNDA4ODg0NzkzOTYyYTY2NGRhZWM2YmIyNWUwNmQ3NmEyZjUzZDhhODBmNjZhNjc1MjAy"}]}}}`
	out := server.Handle(context.Background(), A2ARequest{Version: "1.0", Extensions: []string{A2AExtensionURI}, Body: []byte(example)}, "")
	reply, _ := a2aReplyOf(t, mcpDecode(t, out.Body), MediaTypeCBOR)
	digest := ""
	switch {
	case reply.Envelope != nil:
		digest = reply.Envelope.RequestDigest
	case reply.Refusal != nil:
		digest = reply.Refusal.RequestDigest
	}
	if digest != "ba7fee76b7afe9f3ec5ce46975865e06040492f0da733b28cbc86bd2be609d5e" {
		t.Fatalf("the reply binds %q, not request B's digest", digest)
	}
}
