// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testdata/shelf/receive-carriage: the MCP and A2A entries of Shelf.Receive
// (evidencebook#577; agent-action-capsule spec/bindings/record-push-v0.md,
// RP §3 and §4). script.json holds, for each carriage, a track of calls run
// on a fresh shelf under the receive vectors' profile, bounds and retention
// (testdata/shelf/receive/script.json), each with the message sent and the
// message the entry returns. The pushes of the first part of each track are
// the receive script's own receive steps, so the answers are its `expect`
// objects; the rest are the edge cases of RP-1 a receiver answers.
// EB_WRITE_SHELF_VECTORS=1 rewrites the file; otherwise it must match.

var pushIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type carriageCase struct {
	At string `json:"at"`
	// Op is "call" (send Request), or "forget" and "reopen" as in the
	// receive script.
	Op      string `json:"op"`
	Note    string `json:"note,omitempty"`
	Deal    string `json:"deal,omitempty"`
	Settled *bool  `json:"settled,omitempty"`
	// Version and Extensions are A2A's service parameters.
	Version    *string         `json:"a2a_version,omitempty"`
	Extensions []string        `json:"a2a_extensions,omitempty"`
	Request    json.RawMessage `json:"request,omitempty"`
	// Expect is the message the entry returns, with the identifiers an A2A
	// task, context and artifact carry replaced by "<id>" (each is 32
	// lowercase hex digits). Absent when the entry returns nothing.
	Expect json.RawMessage `json:"expect,omitempty"`
}

type carriageTrack struct {
	Carriage string         `json:"carriage"`
	Cases    []carriageCase `json:"cases"`
}

type carriageVectors struct {
	About  string          `json:"about"`
	Tracks []carriageTrack `json:"tracks"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mcpPushRequest(id int, name string, arguments any) json.RawMessage {
	params := map[string]any{
		"_meta": map[string]any{mcpMetaProtocolVersion: MCPProtocolVersion, mcpMetaClientCapabilities: map[string]any{}},
		"name":  name,
	}
	if arguments != nil {
		params["arguments"] = arguments
	}
	return mustJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": params})
}

func a2aPushRequest(id int, message map[string]any) json.RawMessage {
	return mustJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": "SendMessage", "params": map[string]any{"message": message}})
}

func a2aPushMessageOf(arguments any, mediaType string) map[string]any {
	return map[string]any{"messageId": "m-1", "role": "ROLE_USER", "extensions": []any{A2APushExtensionURI},
		"parts": []any{map[string]any{"mediaType": mediaType, "data": arguments}}}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// pushArguments is the push object of a receive step.
func pushArguments(t *testing.T, v *receiveVectors, step vectorStep) map[string]any {
	t.Helper()
	args := map[string]any{}
	if step.Record != "" {
		args["record"] = b64([]byte(v.Records[step.Record]))
	}
	if step.Proof != "" {
		p := v.Proofs[step.Proof]
		octets, err := json.Marshal(p)
		require.NoError(t, err)
		args["stamp_proof"] = b64(octets)
	}
	return args
}

// buildCarriageTracks makes the tracks' calls (without their expectations).
func buildCarriageTracks(t *testing.T, v *receiveVectors) []carriageTrack {
	t.Helper()
	const edgeAt = "2026-10-21T00:00:00Z"
	good := b64([]byte(v.Records["a1"]))
	var tracks []carriageTrack
	for _, carriage := range []string{"mcp", "a2a"} {
		track := carriageTrack{Carriage: carriage}
		id := 0
		send := func(arguments any) json.RawMessage {
			id++
			if carriage == "mcp" {
				return mcpPushRequest(id, MCPToolRecordPush, arguments)
			}
			return a2aPushRequest(id, a2aPushMessageOf(arguments, mediaTypeApplicationJSON))
		}
		version := A2AProtocolVersion
		call := func(at, note string, request json.RawMessage) carriageCase {
			c := carriageCase{At: at, Op: "call", Note: note, Request: request}
			if carriage == "a2a" {
				c.Version, c.Extensions = &version, []string{A2APushExtensionURI}
			}
			return c
		}
		for _, step := range v.Steps {
			switch step.Op {
			case "receive":
				track.Cases = append(track.Cases, call(step.At, "receive step", send(pushArguments(t, v, step))))
			case "forget", "reopen":
				track.Cases = append(track.Cases, carriageCase{At: step.At, Op: step.Op, Deal: step.Deal, Settled: step.Settled})
			}
		}
		add := func(note string, request json.RawMessage) {
			track.Cases = append(track.Cases, call(edgeAt, note, request))
		}
		add("a record whose bytes the profile cannot read: refused invalid, no digest", send(map[string]any{"record": b64([]byte("not a record"))}))
		add("a stamp proof that is not a stamp proof: its part refused invalid", send(map[string]any{"stamp_proof": b64([]byte("not json"))}))
		add("a record and a proof that is not one: each part answered on its own", send(map[string]any{"record": good, "stamp_proof": b64([]byte("{}"))}))
		add("no member: invalid params, no answer", send(map[string]any{}))
		add("an extra member: invalid params", send(map[string]any{"record": good, "sender": "alice"}))
		add("padding is not base64url without padding: invalid params", send(map[string]any{"record": good + "="}))
		add("not base64url: invalid params", send(map[string]any{"record": "a+b/"}))
		add("trailing bits set (not the canonical encoding): invalid params", send(map[string]any{"record": "AB"}))
		add("one character left over (length 1 mod 4): invalid params", send(map[string]any{"record": "A"}))
		add("a member that is not a string: invalid params", send(map[string]any{"record": 7}))
		if carriage == "mcp" {
			track.Cases = append(track.Cases,
				call(edgeAt, "an unknown tool: invalid params", mcpPushRequest(90, "record_pull", map[string]any{"record": good})),
				call(edgeAt, "arguments that are not an object: invalid params", mcpPushRequest(91, MCPToolRecordPush, []any{})),
				call(edgeAt, "the tool list: one tool, fixed text", mustJSON(map[string]any{"jsonrpc": "2.0", "id": 92, "method": "tools/list",
					"params": map[string]any{"_meta": map[string]any{mcpMetaProtocolVersion: MCPProtocolVersion, mcpMetaClientCapabilities: map[string]any{}}}})),
				call(edgeAt, "an id above 2^53 is echoed as written", json.RawMessage(`{"jsonrpc":"2.0","id":9007199254740993,"method":"server/discover"}`)),
				call(edgeAt, "discovery", mustJSON(map[string]any{"jsonrpc": "2.0", "id": 93, "method": "server/discover"})),
				call(edgeAt, "another protocol version", mustJSON(map[string]any{"jsonrpc": "2.0", "id": 94, "method": "tools/call", "params": map[string]any{
					"_meta": map[string]any{mcpMetaProtocolVersion: "2025-06-18", mcpMetaClientCapabilities: map[string]any{}},
					"name":  MCPToolRecordPush, "arguments": map[string]any{"record": good}}})),
			)
		} else {
			raw := a2aPushMessageOf(map[string]any{}, mediaTypeApplicationJSON)
			raw["parts"] = []any{map[string]any{"mediaType": "application/octet-stream", "raw": base64.StdEncoding.EncodeToString([]byte(v.Records["a1"]))}}
			text := a2aPushMessageOf(nil, mediaTypeApplicationJSON)
			text["parts"] = []any{map[string]any{"mediaType": "text/plain", "text": "hello"}}
			other := a2aPushMessageOf(map[string]any{"record": good}, "text/plain")
			cont := a2aPushMessageOf(map[string]any{"record": good}, mediaTypeApplicationJSON)
			cont["taskId"] = "t-1"
			two := a2aPushMessageOf(map[string]any{"record": good}, mediaTypeApplicationJSON)
			two["parts"] = append(two["parts"].([]any), two["parts"].([]any)...)
			asUser := a2aPushMessageOf(map[string]any{"record": good}, mediaTypeApplicationJSON)
			asUser["role"] = "ROLE_AGENT"
			track.Cases = append(track.Cases,
				call(edgeAt, "the push in a raw part: invalid params, no answer", a2aPushRequest(80, raw)),
				call(edgeAt, "the push in a text part: invalid params", a2aPushRequest(81, text)),
				call(edgeAt, "a data part of another media type: content type not supported", a2aPushRequest(82, other)),
				call(edgeAt, "a message that names a task: task not found", a2aPushRequest(83, cont)),
				call(edgeAt, "two parts: invalid params", a2aPushRequest(84, two)),
				call(edgeAt, "a message from the agent: invalid params", a2aPushRequest(85, asUser)),
			)
			noExt := call(edgeAt, "the extension not activated in A2A-Extensions: invalid params", a2aPushRequest(86, a2aPushMessageOf(map[string]any{"record": good}, mediaTypeApplicationJSON)))
			noExt.Extensions = nil
			noVersion := call(edgeAt, "no A2A-Version: version not supported", a2aPushRequest(87, a2aPushMessageOf(map[string]any{"record": good}, mediaTypeApplicationJSON)))
			empty := ""
			noVersion.Version = &empty
			getTask := call(edgeAt, "GetTask finds nothing", mustJSON(map[string]any{"jsonrpc": "2.0", "id": 88, "method": "GetTask", "params": map[string]any{"id": "t-1"}}))
			withConfig := func(id int, modes string) json.RawMessage {
				m := a2aPushMessageOf(map[string]any{"record": good}, mediaTypeApplicationJSON)
				body, err := json.Marshal(m)
				require.NoError(t, err)
				return json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"SendMessage","params":{"configuration":{"acceptedOutputModes":%s},"message":%s}}`, id, modes, body))
			}
			bigID := a2aPushMessageOf(map[string]any{"record": good}, mediaTypeApplicationJSON)
			bigID["metadata"] = map[string]any{"score": 0.5}
			bigBody, err := json.Marshal(bigID)
			require.NoError(t, err)
			track.Cases = append(track.Cases, noExt, noVersion, getTask,
				call(edgeAt, "acceptedOutputModes that is empty: content type not supported, nothing stored", withConfig(95, `[]`)),
				call(edgeAt, "acceptedOutputModes without application/json: content type not supported", withConfig(96, `["text/plain"]`)),
				call(edgeAt, "acceptedOutputModes with application/json: answered", withConfig(97, `["text/plain","application/json"]`)),
				call(edgeAt, "an id above 2^53 and a fraction in metadata are read and the id echoed", json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":9007199254740993,"method":"SendMessage","params":{"message":%s}}`, bigBody))))
		}
		tracks = append(tracks, track)
	}
	return tracks
}

// maskPushIDs replaces the identifiers an A2A Task carries with "<id>",
// checking each is 32 lowercase hex digits.
func maskPushIDs(t *testing.T, message []byte) json.RawMessage {
	t.Helper()
	m := decodeNumbers(t, message)
	if result, ok := m["result"].(map[string]any); ok {
		if task, ok := result["task"].(map[string]any); ok {
			mask := func(o map[string]any, key string) {
				id, _ := o[key].(string)
				require.Regexp(t, pushIDPattern, id)
				o[key] = "<id>"
			}
			mask(task, "id")
			mask(task, "contextId")
			for _, a := range task["artifacts"].([]any) {
				mask(a.(map[string]any), "artifactId")
			}
		}
	}
	return mustJSON(m)
}

// runCarriageTrack runs a track on a fresh shelf and returns what each call
// returned.
func runCarriageTrack(t *testing.T, v *receiveVectors, track carriageTrack) []json.RawMessage {
	t.Helper()
	ctx := context.Background()
	f := newShelfFixture(t)
	var now time.Time
	f.cfg.Now = func() time.Time { return now }
	f.cfg.Receive = ReceiveConfig{Profile: v.Profile, Bounds: v.Bounds.bounds(), Retention: time.Duration(v.RetentionSeconds) * time.Second}
	s := f.open(t)
	var out []json.RawMessage
	for _, c := range track.Cases {
		var err error
		now, err = time.Parse(time.RFC3339, c.At)
		require.NoError(t, err)
		var got json.RawMessage
		switch c.Op {
		case "call":
			if track.Carriage == "mcp" {
				got = s.PushMCP("v").Handle(ctx, c.Request)
				if got != nil {
					m := decodeNumbers(t, got)
					if result, ok := m["result"].(map[string]any); ok {
						delete(result, "_meta") // the server info is the host's version
					}
					got = mustJSON(m)
				}
			} else {
				req := A2ARequest{Extensions: c.Extensions, Body: c.Request}
				if c.Version != nil {
					req.Version = *c.Version
				}
				got = maskPushIDs(t, s.PushA2A().Handle(ctx, req).Body)
			}
		case "forget":
			settled := c.Settled != nil && *c.Settled
			// A refusal (a deal not yet settled) is part of the script, as in the
			// receive vectors; only the store's state matters here.
			_, _ = s.Forget(ctx, c.Deal, now, func(context.Context, string) (bool, error) { return settled, nil })
		case "reopen":
			require.NoError(t, s.Release())
			s = f.open(t)
		}
		out = append(out, got)
	}
	return out
}

func TestShelfReceiveCarriageVectors(t *testing.T) {
	path := filepath.Join("testdata", "shelf", "receive-carriage", "script.json")
	b, err := os.ReadFile(filepath.Join("testdata", "shelf", "receive", "script.json"))
	require.NoError(t, err)
	var v receiveVectors
	require.NoError(t, json.Unmarshal(b, &v))
	write := os.Getenv("EB_WRITE_SHELF_VECTORS") == "1"
	var vectors carriageVectors
	if write {
		vectors = carriageVectors{
			About:  "The MCP and A2A entries of Shelf.Receive (evidencebook#577; agent-action-capsule spec/bindings/record-push-v0.md §3, §4), written by interop_push_vectors_test.go (EB_WRITE_SHELF_VECTORS=1). See README.md.",
			Tracks: buildCarriageTracks(t, &v),
		}
	} else {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &vectors))
	}
	for ti := range vectors.Tracks {
		track := &vectors.Tracks[ti]
		got := runCarriageTrack(t, &v, *track)
		for i := range track.Cases {
			c := &track.Cases[i]
			if write {
				c.Expect = got[i]
				continue
			}
			if c.Op != "call" {
				continue
			}
			require.JSONEq(t, string(c.Expect), string(got[i]), "%s case %d: %s", track.Carriage, i, c.Note)
		}
		// A receive step's answer is the receive script's own expect (RP §2.2).
		var steps []vectorStep
		for _, step := range v.Steps {
			if step.Op == "receive" {
				steps = append(steps, step)
			}
		}
		n := 0
		for _, c := range track.Cases {
			if c.Note != "receive step" {
				continue
			}
			want, err := json.Marshal(answerOfExpect(steps[n].Expect))
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(answerData(t, track.Carriage, c.Expect)), "%s receive step %d", track.Carriage, n)
			n++
		}
		require.Equal(t, len(steps), n)
	}
	out, err := json.MarshalIndent(vectors, "", "  ")
	require.NoError(t, err)
	out = append(out, '\n')
	if write {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, out, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	if !bytes.Equal(out, want) {
		t.Fatalf("%s differs from this build's entries; regenerate only for an intended change (EB_WRITE_SHELF_VECTORS=1)", path)
	}
}

// answerOfExpect is the answer object of a receive script step.
func answerOfExpect(e *vectorExpect) map[string]any {
	a := map[string]any{}
	if e.Record != nil {
		a["record"] = e.Record
	}
	if e.Proof != nil {
		a["stamp_proof"] = e.Proof
	}
	return a
}

// answerData is the answer object a carriage's response holds.
func answerData(t *testing.T, carriage string, response json.RawMessage) json.RawMessage {
	t.Helper()
	var m struct {
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
			Task              struct {
				Artifacts []struct {
					Parts []struct {
						Data json.RawMessage `json:"data"`
					} `json:"parts"`
				} `json:"artifacts"`
			} `json:"task"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(response, &m))
	if carriage == "mcp" {
		return m.Result.StructuredContent
	}
	require.Len(t, m.Result.Task.Artifacts, 1)
	require.Len(t, m.Result.Task.Artifacts[0].Parts, 1)
	return m.Result.Task.Artifacts[0].Parts[0].Data
}

// decodeNumbers reads a JSON object keeping numbers as written, so an id
// above 2^53 survives the round trip through the harness.
func decodeNumbers(t *testing.T, message []byte) map[string]any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(message))
	d.UseNumber()
	var m map[string]any
	require.NoError(t, d.Decode(&m))
	return m
}
