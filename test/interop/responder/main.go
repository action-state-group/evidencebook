// SPDX-License-Identifier: Apache-2.0

// Command responder answers the responder-side requests of the evidence
// interoperability profile's conformance vectors with a fresh book, and
// writes every request and reply as carried, for checking by an independent
// verifier (test/interop/python-profile/verify_responder.py runs the AAC
// reference verifier over them).
//
//	go run ./test/interop/responder <dir> <out.json>
//
// The book signs with the vectors' test keys (each an Ed25519 seed that is
// the SHA-256 of a public label), so the vectors' trust pins name it. The
// requests are the vectors' requests made against this book's own log: the
// same subject form, coverage form, nonces and profile member, with this
// book's record digests and anchors in place of the vectors'.
//
// The carriage vectors' request R (request C in deterministic CBOR) is sent
// over the native stream (Respond), over A2A (the book's A2AServer) and over
// MCP (the book's MCPServer), each as JSON-RPC octets, and the A2A Message and
// Task and the MCP call and result are written as carried.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/evidencebook"
)

const (
	responderLabel = "draft-mih-agent-evidence-request-00 conformance vectors: primary responder key"
	logLabel       = "evidence-interop/0 worked example: CLL log key"
	logID          = "evidence-stream-1"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: responder <dir> <out.json>")
		os.Exit(2)
	}
	if err := run(context.Background(), os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seeded(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(label))
	return ed25519.NewKeyFromSeed(seed[:])
}

type limitNone struct{}

func (limitNone) Allow(string, time.Time) bool { return false }

func open(ctx context.Context, dir string, policy evidencebook.ResponderPolicy, clock *time.Time) (*evidencebook.Book, error) {
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return nil, err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), logID, seeded(logLabel))
	if err != nil {
		return nil, err
	}
	signer, err := evidencebook.NewEd25519Signer(seeded(responderLabel))
	if err != nil {
		return nil, err
	}
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), "interop", signer.PublicKey())
	if err != nil {
		return nil, err
	}
	return evidencebook.Open(ctx, evidencebook.Config{
		BookID: "responder", Operator: "responder-operator", Store: store, Substrate: substrate, Originals: originals, Signer: signer,
		Now:       func() time.Time { *clock = clock.Add(time.Second); return *clock },
		Responder: policy,
	})
}

// carried is octets as the vectors write them: JSON as text, CBOR as hex.
type carried struct {
	MediaType  string `json:"media_type"`
	OctetsText string `json:"octets_text,omitempty"`
	OctetsHex  string `json:"octets_hex,omitempty"`
}

func carry(m evidencebook.Message) carried {
	if m.MediaType == evidencebook.MediaTypeJSON {
		return carried{MediaType: m.MediaType, OctetsText: string(m.Octets)}
	}
	return carried{MediaType: m.MediaType, OctetsHex: hex.EncodeToString(m.Octets)}
}

type exchange struct {
	Requests      []carried `json:"requests"`
	Replies       []carried `json:"replies"`
	AbsenceRecord *carried  `json:"absence_record,omitempty"`
}

// carriage is one request sent over the native stream, A2A and MCP.
type carriage struct {
	Request carried         `json:"request"`
	Native  *nativeCarriage `json:"native,omitempty"`
	A2A     *a2aCarriage    `json:"a2a,omitempty"`
	MCP     *mcpCarriage    `json:"mcp,omitempty"`
	// AbsenceRecord is the requester's own absence record for Request.
	AbsenceRecord *carried `json:"absence_record,omitempty"`
}

type nativeCarriage struct {
	RequestFrameHex  string `json:"request_frame_hex"`
	ResponseFrameHex string `json:"response_frame_hex"`
}

// mcpCarriage is the tools/call params and its result, as JSON values, and
// the JSON-RPC octets exactly as sent and received.
type mcpCarriage struct {
	Call        json.RawMessage `json:"call"`
	Result      json.RawMessage `json:"result"`
	JSONRPCSent string          `json:"jsonrpc_sent"`
	JSONRPCRecv string          `json:"jsonrpc_received"`
}

// a2aCarriage is the SendMessage Message and the Task it returned, as JSON
// values, the service parameters sent, and the JSON-RPC octets exactly as
// sent and received.
type a2aCarriage struct {
	Message     json.RawMessage `json:"message"`
	Task        json.RawMessage `json:"task"`
	Version     string          `json:"a2a_version"`
	Extensions  []string        `json:"a2a_extensions"`
	JSONRPCSent string          `json:"jsonrpc_sent"`
	JSONRPCRecv string          `json:"jsonrpc_received"`
}

type output struct {
	ResponderKey string              `json:"responder_key"`
	LogKeys      []map[string]string `json:"log_keys"`
	Exchanges    map[string]exchange `json:"exchanges"`
	Invariance   map[string]string   `json:"invariance_artifacts_hex"`
	RecordSet    map[string]string   `json:"invariance_record_set_artifacts_hex"`
	Requests     map[string]carried  `json:"other_requests"`
	Manifests    []json.RawMessage   `json:"manifests"`
	// Carriage holds the carriage vectors' exchanges: "invariance" (R over
	// the native stream, A2A and MCP), and "a2a-absence" and "mcp-absence"
	// (a requester's request over A2A or MCP, and its absence record once
	// its window closed).
	Carriage map[string]carriage `json:"carriage"`
	// AgentCard is the A2A Agent Card, its extension params the manifest.
	AgentCard json.RawMessage `json:"agent_card"`
	// MCPManifest is the evidence_manifest tools/call result.
	MCPManifest json.RawMessage `json:"mcp_manifest"`
	// MCPTools is the tools/list result.
	MCPTools json.RawMessage `json:"mcp_tools"`
}

// mcpCall sends one tools/call over the MCP carriage and returns the params
// sent (with the _meta MCP 2026-07-28 requires on every request, which is
// carriage, as the profile's vectors write the call), the result received,
// and both JSON-RPC messages as octets.
func mcpCall(ctx context.Context, server *evidencebook.MCPServer, id int, method string, params map[string]any, authenticated string) (mcpCarriage, error) {
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    evidencebook.MCPProtocolVersion,
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "interop-harness", "version": "0"},
	}
	sent, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return mcpCarriage{}, err
	}
	received := server.Handle(ctx, sent, authenticated)
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(received, &response); err != nil || response.Result == nil {
		return mcpCarriage{}, fmt.Errorf("%s: no result: %s (%v)", method, received, err)
	}
	call, err := json.Marshal(params)
	if err != nil {
		return mcpCarriage{}, err
	}
	return mcpCarriage{Call: call, Result: response.Result, JSONRPCSent: string(sent), JSONRPCRecv: string(received)}, nil
}

// a2aRequest sends request m over the A2A carriage as a SendMessage with
// the §11.4.4 service parameters, and returns the reply octets the Task's
// one Artifact carries.
func a2aRequest(ctx context.Context, server *evidencebook.A2AServer, id int, messageID string, m evidencebook.Message, authenticated string) (a2aCarriage, evidencebook.Message, error) {
	message := map[string]any{
		"messageId": messageID, "role": "ROLE_USER", "extensions": []any{evidencebook.A2AExtensionURI},
		"parts": []any{map[string]any{"mediaType": m.MediaType, "raw": base64.StdEncoding.EncodeToString(m.Octets)}},
	}
	sent, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "SendMessage", "params": map[string]any{"message": message}})
	if err != nil {
		return a2aCarriage{}, evidencebook.Message{}, err
	}
	params := evidencebook.A2ARequest{Version: evidencebook.A2AProtocolVersion, Extensions: []string{evidencebook.A2AExtensionURI}, Body: sent}
	received := server.Handle(ctx, params, authenticated).Body
	var response struct {
		Result struct {
			Task json.RawMessage `json:"task"`
		} `json:"result"`
	}
	if err := json.Unmarshal(received, &response); err != nil || response.Result.Task == nil {
		return a2aCarriage{}, evidencebook.Message{}, fmt.Errorf("SendMessage: no task: %s (%v)", received, err)
	}
	carried, err := json.Marshal(message)
	if err != nil {
		return a2aCarriage{}, evidencebook.Message{}, err
	}
	c := a2aCarriage{Message: carried, Task: response.Result.Task, Version: params.Version, Extensions: params.Extensions,
		JSONRPCSent: string(sent), JSONRPCRecv: string(received)}
	var task struct {
		Artifacts []struct {
			Parts []struct {
				Raw       string `json:"raw"`
				MediaType string `json:"mediaType"`
			} `json:"parts"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(c.Task, &task); err != nil || len(task.Artifacts) != 1 || len(task.Artifacts[0].Parts) != 1 {
		return c, evidencebook.Message{}, fmt.Errorf("SendMessage: the task does not hold one reply Artifact: %s (%v)", c.Task, err)
	}
	part := task.Artifacts[0].Parts[0]
	octets, err := base64.StdEncoding.Strict().DecodeString(part.Raw)
	return c, evidencebook.Message{Octets: octets, MediaType: part.MediaType}, err
}

// mcpRequest sends request m over the MCP carriage.
func mcpRequest(ctx context.Context, server *evidencebook.MCPServer, id int, m evidencebook.Message, authenticated string) (mcpCarriage, evidencebook.Message, error) {
	c, err := mcpCall(ctx, server, id, "tools/call", map[string]any{"name": evidencebook.MCPToolRequest,
		"arguments": map[string]any{"request": base64.RawURLEncoding.EncodeToString(m.Octets), "media_type": m.MediaType}}, authenticated)
	if err != nil {
		return c, evidencebook.Message{}, err
	}
	var result struct {
		StructuredContent struct {
			Reply     string `json:"reply"`
			MediaType string `json:"media_type"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(c.Result, &result); err != nil {
		return c, evidencebook.Message{}, err
	}
	octets, err := base64.RawURLEncoding.Strict().DecodeString(result.StructuredContent.Reply)
	return c, evidencebook.Message{Octets: octets, MediaType: result.StructuredContent.MediaType}, err
}

func run(ctx context.Context, dir, out string) error {
	clock := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	book, err := open(ctx, filepath.Join(dir, "responder"), evidencebook.ResponderPolicy{}, &clock)
	if err != nil {
		return err
	}
	defer func() { _ = book.Release() }()
	var records []evidencebook.Record
	for i := range 3 {
		r, err := book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: fmt.Sprintf("order-%d", i)})
		if err != nil {
			return err
		}
		records = append(records, r)
	}
	earlier, err := book.Checkpoint(ctx)
	if err != nil {
		return err
	}
	for i := 3; i < 7; i++ {
		r, err := book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: fmt.Sprintf("order-%d", i)})
		if err != nil {
			return err
		}
		records = append(records, r)
	}
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		return err
	}
	pin := hexDigest(anchor.Statement)
	manifest := evidencebook.ManifestOptions{Bindings: []string{evidencebook.BindingStream, evidencebook.BindingHTTP, evidencebook.BindingA2A, evidencebook.BindingMCP}}
	server, err := book.MCP(evidencebook.MCPOptions{Manifest: manifest, Policy: evidencebook.DefaultSharePolicy()})
	if err != nil {
		return err
	}
	agent, err := book.A2A(evidencebook.A2AOptions{Manifest: manifest, Policy: evidencebook.DefaultSharePolicy(),
		Name: "evidencebook interop responder", URL: "https://responder.example/a2a"})
	if err != nil {
		return err
	}
	first, err := book.Manifest(manifest)
	if err != nil {
		return err
	}
	subject := evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: records[2].RecordID}
	request := func(s evidencebook.Subject, pin, nonce, profile, mediaType string) (evidencebook.Message, error) {
		octets, err := evidencebook.EncodeRequest(evidencebook.EvidenceRequest{Subject: s, Coverage: evidencebook.Coverage{ExpectedPin: pin}, Nonce: nonce, Profile: profile}, mediaType)
		return evidencebook.Message{Octets: octets, MediaType: mediaType}, err
	}
	respond := func(m evidencebook.Message, requester string) (evidencebook.Reply, error) {
		reply, _, err := book.Respond(ctx, m, evidencebook.RespondOptions{AuthenticatedRequester: requester, Policy: evidencebook.DefaultSharePolicy()})
		return reply, err
	}
	o := output{
		ResponderKey: hex.EncodeToString(seeded(responderLabel).Public().(ed25519.PublicKey)),
		Exchanges:    map[string]exchange{}, Invariance: map[string]string{}, RecordSet: map[string]string{}, Requests: map[string]carried{},
		Carriage: map[string]carriage{},
	}
	checkpoints, err := book.Checkpoints(ctx)
	if err != nil {
		return err
	}
	for _, cp := range checkpoints {
		o.LogKeys = append(o.LogKeys, map[string]string{"iss": cp.LogID, "sub": fmt.Sprintf("%s#%d", cp.LogID, cp.TreeSize), "kid": cp.KeyID})
	}

	// Request C (JSON) by alice and request D (CBOR, another nonce) by bob.
	reqC, err := request(subject, pin, "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	reqD, err := request(subject, pin, "2026-10-12T00:00:20Z/d", evidencebook.ProfileID, evidencebook.MediaTypeCBOR)
	if err != nil {
		return err
	}
	replyC, err := respond(reqC, "alice")
	if err != nil {
		return err
	}
	replyD, err := respond(reqD, "bob")
	if err != nil {
		return err
	}
	if replyC.Envelope == nil || replyD.Envelope == nil {
		return fmt.Errorf("requests C and D were refused")
	}
	o.Exchanges["valid-exchange"] = exchange{Requests: []carried{carry(reqC)}, Replies: []carried{carry(replyC.Message)}}
	o.Exchanges["invariance-two-nonces-two-transports"] = exchange{Requests: []carried{carry(reqC), carry(reqD)}, Replies: []carried{carry(replyC.Message), carry(replyD.Message)}}
	o.Invariance["alice-json-nonce-c"] = hex.EncodeToString(replyC.Envelope.Artifact)
	o.Invariance["bob-cbor-nonce-d"] = hex.EncodeToString(replyD.Envelope.Artifact)
	if err := os.WriteFile(filepath.Join(dir, "artifact-alice-json-nonce-c.bin"), replyC.Envelope.Artifact, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact-bob-cbor-nonce-d.bin"), replyD.Envelope.Artifact, 0o600); err != nil {
		return err
	}

	// The carriage vectors' R: request C in deterministic CBOR, sent once over
	// the native stream, once over A2A and once over MCP, by the same
	// authenticated requester. Only the carriage differs; the canonical
	// Artifact must not.
	r, err := request(subject, pin, "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeCBOR)
	if err != nil {
		return err
	}
	native, err := respond(r, "alice")
	if err != nil {
		return err
	}
	overMCP, mcpReply, err := mcpRequest(ctx, server, 1, r, "alice")
	if err != nil {
		return err
	}
	carriedReply, err := evidencebook.ParseReply(mcpReply)
	if err != nil {
		return err
	}
	overA2A, a2aReply, err := a2aRequest(ctx, agent, 1, "m-r", r, "alice")
	if err != nil {
		return err
	}
	a2aCarried, err := evidencebook.ParseReply(a2aReply)
	if err != nil {
		return err
	}
	if native.Envelope == nil || carriedReply.Envelope == nil || a2aCarried.Envelope == nil {
		return fmt.Errorf("request R was refused over a carriage")
	}
	o.Carriage["invariance"] = carriage{Request: carry(r), MCP: &overMCP, A2A: &overA2A,
		Native: &nativeCarriage{RequestFrameHex: hex.EncodeToString(r.Octets), ResponseFrameHex: hex.EncodeToString(native.Octets)}}
	if err := os.WriteFile(filepath.Join(dir, "artifact-a2a-cbor.bin"), a2aCarried.Envelope.Artifact, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact-native-cbor.bin"), native.Envelope.Artifact, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact-mcp-cbor.bin"), carriedReply.Envelope.Artifact, 0o600); err != nil {
		return err
	}
	if o.MCPTools, err = rawResult(ctx, server, 2, "tools/list", map[string]any{}); err != nil {
		return err
	}

	// The same two variations for a record-set subject, whose Artifact is
	// built by this responder rather than being a record's own octets.
	for _, v := range []struct{ name, nonce, mediaType, requester string }{
		{"full_history-alice-json-nonce-c", "2026-10-12T00:00:10Z/c", evidencebook.MediaTypeJSON, "alice"},
		{"full_history-bob-cbor-nonce-d", "2026-10-12T00:00:20Z/d", evidencebook.MediaTypeCBOR, "bob"},
	} {
		m, err := request(evidencebook.Subject{Kind: evidencebook.SubjectFullHistory}, pin, v.nonce, evidencebook.ProfileID, v.mediaType)
		if err != nil {
			return err
		}
		reply, err := respond(m, v.requester)
		if err != nil {
			return err
		}
		if reply.Envelope == nil {
			return fmt.Errorf("full_history request %s was refused", v.name)
		}
		o.RecordSet[v.name] = hex.EncodeToString(reply.Envelope.Artifact)
	}

	// A record this book does not hold: no_such_subject.
	missing, err := request(evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: hexDigest([]byte("never recorded"))}, pin, "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	replyMissing, err := respond(missing, "alice")
	if err != nil {
		return err
	}
	o.Exchanges["missing-evidence-no-such-subject"] = exchange{Requests: []carried{carry(missing)}, Replies: []carried{carry(replyMissing.Message)}}

	// A profile this book does not implement: request_malformed.
	v9, err := request(subject, pin, "2026-10-12T00:00:40Z/v9", "evidence-interop/9", evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	replyV9, err := respond(v9, "alice")
	if err != nil {
		return err
	}
	o.Exchanges["unsupported-version-refused"] = exchange{Requests: []carried{carry(v9)}, Replies: []carried{carry(replyV9.Message)}}

	// The requests the envelope mutations are paired with: another record,
	// and the same record pinned at the earlier checkpoint.
	other, err := request(evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: records[1].RecordID}, pin, "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	otherPin, err := request(subject, hexDigest(earlier.Statement), "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	o.Requests["another-record"], o.Requests["another-pin"] = carry(other), carry(otherPin)

	// The same request C to a responder whose cost policy declines it.
	declining, err := open(ctx, filepath.Join(dir, "declining"), evidencebook.ResponderPolicy{Limiter: limitNone{}}, &clock)
	if err != nil {
		return err
	}
	defer func() { _ = declining.Release() }()
	replyDeclined, _, err := declining.Respond(ctx, reqC, evidencebook.RespondOptions{AuthenticatedRequester: "alice", Policy: evidencebook.DefaultSharePolicy()})
	if err != nil {
		return err
	}
	o.Exchanges["declined"] = exchange{Requests: []carried{carry(reqC)}, Replies: []carried{carry(replyDeclined.Message)}}

	// A requester's own absence record: request C sent from another book,
	// and no reply in a one-minute window.
	requester, err := open(ctx, filepath.Join(dir, "requester"), evidencebook.ResponderPolicy{}, &clock)
	if err != nil {
		return err
	}
	defer func() { _ = requester.Release() }()
	sent, err := requester.Request(ctx, evidencebook.EvidenceRequest{Subject: subject, Coverage: evidencebook.Coverage{ExpectedPin: pin}, Nonce: "2026-10-12T00:00:10Z/c", Route: "peer:example-responder"}, "responder")
	if err != nil {
		return err
	}
	clock = clock.Add(2 * time.Minute)
	absence, err := requester.RecordAbsence(ctx, sent.RecordID, clock.Add(-time.Second), "")
	if err != nil {
		return err
	}
	absenceRecord := carry(evidencebook.Message{Octets: absence.Header.Statement, MediaType: evidencebook.MediaTypeJSON})
	o.Exchanges["missing-evidence-recorded-absence"] = exchange{Requests: []carried{carry(sent.Message())}, Replies: []carried{}, AbsenceRecord: &absenceRecord}

	// A requester's request sent over MCP, and its own absence record once
	// its window closed with no reply recorded. The MCP vectors replace the
	// result with a transport condition (an isError result, no reply) or
	// mark this one isError; the absence record is what the requester keeps.
	viaMCP, err := requester.Request(ctx, evidencebook.EvidenceRequest{Subject: subject, Coverage: evidencebook.Coverage{ExpectedPin: pin}, Nonce: "2026-10-12T00:00:30Z/m", Route: "mcp:example-responder"}, "responder")
	if err != nil {
		return err
	}
	absentCall, _, err := mcpRequest(ctx, server, 3, viaMCP.Message(), "alice")
	if err != nil {
		return err
	}
	clock = clock.Add(2 * time.Minute)
	mcpAbsence, err := requester.RecordAbsence(ctx, viaMCP.RecordID, clock.Add(-time.Second), "")
	if err != nil {
		return err
	}
	mcpAbsenceRecord := carry(evidencebook.Message{Octets: mcpAbsence.Header.Statement, MediaType: evidencebook.MediaTypeJSON})
	o.Carriage["mcp-absence"] = carriage{Request: carry(viaMCP.Message()), MCP: &absentCall, AbsenceRecord: &mcpAbsenceRecord}

	// The same over A2A: a requester's request sent as a SendMessage, and its
	// own absence record once its window closed with no reply recorded. The
	// A2A vector replaces the Task with one that ended TASK_STATE_REJECTED
	// with no reply Artifact; the absence record is what the requester keeps.
	viaA2A, err := requester.Request(ctx, evidencebook.EvidenceRequest{Subject: subject, Coverage: evidencebook.Coverage{ExpectedPin: pin}, Nonce: "2026-10-12T00:00:30Z/a", Route: "a2a:example-responder"}, "responder")
	if err != nil {
		return err
	}
	absentTask, _, err := a2aRequest(ctx, agent, 5, "m-absent", viaA2A.Message(), "alice")
	if err != nil {
		return err
	}
	clock = clock.Add(2 * time.Minute)
	a2aAbsence, err := requester.RecordAbsence(ctx, viaA2A.RecordID, clock.Add(-time.Second), "")
	if err != nil {
		return err
	}
	a2aAbsenceRecord := carry(evidencebook.Message{Octets: a2aAbsence.Header.Statement, MediaType: evidencebook.MediaTypeJSON})
	o.Carriage["a2a-absence"] = carriage{Request: carry(viaA2A.Message()), A2A: &absentTask, AbsenceRecord: &a2aAbsenceRecord}

	// The manifest after every answer above is the one served before them:
	// it carries nothing that grows with history.
	last, err := book.Manifest(manifest)
	if err != nil {
		return err
	}
	if o.MCPManifest, err = rawResult(ctx, server, 4, "tools/call", map[string]any{"name": evidencebook.MCPToolManifest, "arguments": map[string]any{}}); err != nil {
		return err
	}
	o.Manifests = []json.RawMessage{first, last}
	o.AgentCard = agent.AgentCard()
	data, err := json.MarshalIndent(o, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o600)
}

func rawResult(ctx context.Context, server *evidencebook.MCPServer, id int, method string, params map[string]any) (json.RawMessage, error) {
	c, err := mcpCall(ctx, server, id, method, params, "")
	return c.Result, err
}

func hexDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
