// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/veraison/go-cose"
)

// limitVectors is testdata/asker-limits/limits.json, which the Python port
// reads too.
type limitVectors struct {
	Limits struct {
		ReplyBytes int `json:"reply_bytes"`
		Depth      int `json:"depth"`
		Items      int `json:"items"`
		Receipts   int `json:"receipts"`
	} `json:"limits"`
	Reply        []limitCase `json:"reply"`
	Artifact     []limitCase `json:"artifact"`
	Verification []limitCase `json:"verification"`
	Receipts     []limitCase `json:"receipts"`
}

type limitCase struct {
	Name  string `json:"name"`
	Build struct {
		UTF16 bool   `json:"utf16le"`
		Nest  int    `json:"nest"`
		PadTo int    `json:"pad_to"`
		Text  string `json:"text"`
		List  string `json:"list"`
		Count int    `json:"count"`
	} `json:"build"`
	Count int     `json:"count"`
	Error *string `json:"error"`
}

// octets builds the case's JSON text.
func (c limitCase) octets() []byte {
	b := c.Build
	switch {
	case b.UTF16:
		text := `{"x":"\"","y":` + strings.Repeat("[", b.Nest) + strings.Repeat("]", b.Nest) + `}`
		out := make([]byte, 0, 2*len(text))
		for _, c := range []byte(text) {
			out = append(out, c, 0)
		}
		return out
	case b.Nest > 0:
		return []byte(`{"x":` + strings.Repeat("[", b.Nest-1) + strings.Repeat("]", b.Nest-1) + `}`)
	case b.PadTo > 0:
		return []byte(`{"x":"` + strings.Repeat("a", b.PadTo-len(`{"x":""}`)) + `"}`)
	case b.List != "":
		return []byte(`{"` + b.List + `":[` + strings.TrimSuffix(strings.Repeat("0,", b.Count), ",") + `]}`)
	}
	return []byte(b.Text)
}

// check asserts err against the case's expected error, and that the check
// finished within a second.
func (c limitCase) check(t *testing.T, err error, took time.Duration) {
	t.Helper()
	if took > time.Second {
		t.Errorf("%s: the limit took %s", c.Name, took)
	}
	switch {
	case c.Error == nil && err != nil:
		t.Errorf("%s: refused within the limit: %v", c.Name, err)
	case c.Error != nil && (err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), *c.Error)):
		t.Errorf("%s: got %v, want %q", c.Name, err, *c.Error)
	}
}

func readLimitVectors(t *testing.T) limitVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "asker-limits", "limits.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v limitVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLimitVectors(t *testing.T) {
	v := readLimitVectors(t)
	if v.Limits.ReplyBytes != MaxReplyBytes || v.Limits.Depth != MaxReplyDepth || v.Limits.Items != MaxReplyItems || v.Limits.Receipts != MaxReplyReceipts {
		t.Fatalf("the vectors state other limits: %+v", v.Limits)
	}
	for _, c := range v.Reply {
		octets := c.octets()
		start := time.Now()
		err := checkReplyOctets(Message{Octets: octets, MediaType: MediaTypeJSON})
		c.check(t, err, time.Since(start))
		if c.Error == nil {
			if _, err := evidencerequest.DecodeJSON(octets); err != nil {
				t.Errorf("%s: within the limit but the decoder refuses it: %v", c.Name, err)
			}
		}
	}
	req := EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}}
	for _, c := range v.Artifact {
		start := time.Now()
		c.check(t, checkEnvelopeLimits(req, Envelope{Artifact: c.octets()}), time.Since(start))
	}
	for _, c := range v.Verification {
		start := time.Now()
		c.check(t, checkEnvelopeLimits(req, Envelope{Artifact: []byte(`{}`), Verification: make([]VerificationEntry, c.Count)}), time.Since(start))
	}
	for _, c := range v.Receipts {
		entries := make([]any, c.Count)
		for i := range entries {
			entries[i] = map[string]any{}
		}
		reply, err := json.Marshal(map[string]any{WitnessReceiptsMember: entries})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		_, err = carriedReceipts(Message{Octets: reply, MediaType: MediaTypeJSON}, "anchor")
		c.check(t, err, time.Since(start))
	}
}

// An artifact with a duplicate member is not JSON to the decoder, so no
// limit is charged for it; the content checks report it (the Python port
// agrees).
func TestAnArtifactWithADuplicateMemberIsLeftToTheContentChecks(t *testing.T) {
	data := `{"checkpoints":[],"checkpoints":[` + strings.TrimSuffix(strings.Repeat("0,", MaxReplyItems+1), ",") + `]}`
	if err := checkEnvelopeLimits(EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}}, Envelope{Artifact: []byte(data)}); err != nil {
		t.Fatal(err)
	}
}

// A bare record's artifact is the record's own octets: they are hashed, never
// decoded, so no artifact limit applies to them.
func TestABareRecordArtifactIsNotDecodedForLimits(t *testing.T) {
	deep := limitCase{}
	deep.Build.Nest = MaxReplyDepth + 1
	if err := checkEnvelopeLimits(EvidenceRequest{Subject: Subject{Kind: SubjectRecord}}, Envelope{Artifact: deep.octets()}); err != nil {
		t.Fatal(err)
	}
}

// witnessVectors is testdata/asker-limits/witness.json: one signed
// checkpoint statement, a requester's directory, and carried receipts with
// the standing each must give. EB_WRITE_ASKER_VECTORS=1 regenerates it.
type witnessVectors struct {
	Description string        `json:"description"`
	Statement   string        `json:"statement"`
	Directory   []WitnessRow  `json:"directory"`
	Cases       []witnessCase `json:"cases"`
}

type witnessCase struct {
	Name     string            `json:"name"`
	NoAnchor bool              `json:"no_anchor,omitempty"`
	Carried  []json.RawMessage `json:"carried"`
	Want     string            `json:"want"`
}

func writeWitnessVectors(t *testing.T, path string) {
	ctx := context.Background()
	book, _ := linkedBook(t)
	if _, err := book.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("s", "later"))
	if _, err := book.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	history, err := book.store.Checkpoints(ctx)
	if err != nil || len(history) < 2 {
		t.Fatalf("checkpoints: %d %v", len(history), err)
	}
	statement, earlier := history[len(history)-1].Statement, history[0].Statement
	witnessKey, directory := testWitness()
	holderKey := ed25519.NewKeyFromSeed(seededKey(78)[:32])
	entry := func(m map[string]any) json.RawMessage {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	good0 := mintReceipt(t, statement, witnessKey)
	good := entry(good0)
	holders := mintReceipt(t, statement, holderKey)
	holders["ts_url"] = "https://holder.test"
	forged := mintReceipt(t, statement, holderKey)
	malformed := mintReceipt(t, statement, witnessKey)
	delete(malformed, "receipt_b64")
	// The same genuine receipt with its one inclusion proof listed twice:
	// the proofs are unprotected, so the signature still verifies, but the
	// verifier accepts exactly one proof.
	doubled := mintReceipt(t, statement, witnessKey)
	raw, err := base64.StdEncoding.DecodeString(doubled["receipt_b64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var message cose.Sign1Message
	if err := message.UnmarshalCBOR(raw); err != nil {
		t.Fatal(err)
	}
	vdp := message.Headers.Unprotected[int64(396)].(map[any]any)
	proofs := vdp[int64(-1)].([]any)
	vdp[int64(-1)] = []any{proofs[0], proofs[0]}
	message.Headers.RawUnprotected = nil // re-encode the edited header, not the decoded bytes
	if raw, err = message.MarshalCBOR(); err != nil {
		t.Fatal(err)
	}
	doubled["receipt_b64"] = base64.StdEncoding.EncodeToString(raw)
	tampered := mintReceipt(t, statement, witnessKey)
	tampered["receipt_b64"] = base64.StdEncoding.EncodeToString([]byte("not a receipt"))
	v := witnessVectors{
		Description: "Witness standing of a verified answer (evidencebook#209): witnessed only when a carried receipt covers the anchor's signed checkpoint statement under a witness in the requester's own directory. Each case's carried entries are the witness members of a reply's witness_receipts, each for the checkpoint whose digest is SHA-256 of statement; a test reads them through the carrier and then checks them. Generated by request_limits_test.go (EB_WRITE_ASKER_VECTORS=1).",
		Statement:   hex.EncodeToString(statement),
		Directory:   directory,
		Cases: []witnessCase{
			{Name: "no-receipt", Want: Unwitnessed},
			{Name: "directory-witness", Carried: []json.RawMessage{good}, Want: Witnessed},
			{Name: "holder-run-witness", Carried: []json.RawMessage{entry(holders)}, Want: Unwitnessed},
			{Name: "forged-under-directory-url", Carried: []json.RawMessage{entry(forged)}, Want: Unwitnessed},
			{Name: "another-checkpoint", Carried: []json.RawMessage{entry(mintReceipt(t, earlier, witnessKey))}, Want: Unwitnessed},
			{Name: "malformed", Carried: []json.RawMessage{entry(malformed)}, Want: Unwitnessed},
			{Name: "bogus-receipt-bytes", Carried: []json.RawMessage{entry(tampered)}, Want: Unwitnessed},
			{Name: "two-inclusion-proofs", Carried: []json.RawMessage{entry(doubled)}, Want: Unwitnessed},
			{Name: "non-ascii-digit-position", Carried: []json.RawMessage{entry(map[string]any{
				"ts_url": testWitnessURL, "entry_hash": good0["entry_hash"], "receipt_b64": good0["receipt_b64"], "leaf_index": "\uff10", "tree_size": "1",
			})}, Want: Unwitnessed},
			{Name: "text-positions", Carried: []json.RawMessage{entry(map[string]any{
				"ts_url": testWitnessURL, "entry_hash": good0["entry_hash"], "receipt_b64": good0["receipt_b64"], "leaf_index": "0", "tree_size": "1",
			})}, Want: Witnessed},
			{Name: "case-varied-member-names", Carried: []json.RawMessage{entry(map[string]any{
				"TS_URL": testWitnessURL, "Entry_Hash": good0["entry_hash"], "RECEIPT_B64": good0["receipt_b64"], "leaf_index": 0, "tree_size": 1,
			})}, Want: Unwitnessed},
			{Name: "forged-then-directory-witness", Carried: []json.RawMessage{entry(forged), good}, Want: Witnessed},
			{Name: "no-anchor-statement", NoAnchor: true, Carried: []json.RawMessage{good}, Want: Unwitnessed},
		},
	}
	if raw, err = json.MarshalIndent(v, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWitnessVectors(t *testing.T) {
	path := filepath.Join("testdata", "asker-limits", "witness.json")
	if os.Getenv("EB_WRITE_ASKER_VECTORS") == "1" {
		writeWitnessVectors(t, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v witnessVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	statement, err := hex.DecodeString(v.Statement)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range v.Cases {
		anchor := statement
		if c.NoAnchor {
			anchor = nil
		}
		entries := make([]any, len(c.Carried))
		for i, w := range c.Carried {
			entries[i] = map[string]any{"checkpoint": digestBytes(statement), "witness": json.RawMessage(w)}
		}
		reply, err := json.Marshal(map[string]any{WitnessReceiptsMember: entries})
		if err != nil {
			t.Fatal(err)
		}
		carried, err := carriedReceipts(Message{Octets: reply, MediaType: MediaTypeJSON}, digestBytes(statement))
		if err != nil {
			t.Fatal(err)
		}
		if got := witnessStanding(anchor, carried, v.Directory); got != c.Want {
			t.Errorf("%s: %s, want %s", c.Name, got, c.Want)
		}
		if got := witnessStanding(anchor, carried, nil); got != Unwitnessed {
			t.Errorf("%s: %s with no directory", c.Name, got)
		}
	}
}

func TestJSONDepthOver(t *testing.T) {
	for text, over := range map[string]bool{
		`[]`:                        false,
		`[[]]`:                      true,
		`{"a":[]}`:                  true,
		`"[[[[["`:                   false,
		`["\"[[[["]`:                false,
		`["\\",[]]`:                 true,
		`[1,[]],[2]`:                true,
		`{"a":{"b":0},"c":{"d":1}}`: true,
		`[],{}`:                     false,
	} {
		if got := jsonDepthOver([]byte(text), 1); got != over {
			t.Errorf("%s: over 1 is %v, want %v", text, got, over)
		}
	}
}

// askFor has requester ask responder for req and returns the request and
// the responder's honest reply.
func askFor(t *testing.T, requester, responder *Book, req EvidenceRequest) (SentRequest, Reply) {
	t.Helper()
	sent, err := requester.Request(context.Background(), req, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	return sent, respondAs(t, responder, sent.Message(), "requester")
}

// withMember returns reply's JSON octets with member set to value.
func withMember(t *testing.T, reply Message, member string, value any) Message {
	t.Helper()
	var wire map[string]any
	if err := json.Unmarshal(reply.Octets, &wire); err != nil {
		t.Fatal(err)
	}
	wire[member] = value
	octets, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return Message{Octets: octets, MediaType: reply.MediaType}
}

func statementOf(t *testing.T, record Record) ResponseStatement {
	t.Helper()
	var statement ResponseStatement
	if err := json.Unmarshal(record.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	return statement
}

// historyBook is a responder with three checkpoints, and a requester.
func historyBook(t *testing.T) (responder, requester *Book) {
	t.Helper()
	responder, _ = linkedBook(t)
	for i := range 3 {
		mustAppend(t, responder, observation("s", fmt.Sprint(i)))
		if _, err := responder.Checkpoint(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	return responder, newFixture(t, "requester", 30).open(t)
}

var historyCard = EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard, Coverage: fresh()}

// A reply over a limit on its unauthenticated octets is refused and records
// nothing, so the request stays open and the genuine answer is still
// recorded afterwards.
func TestAReplyOverAnOctetLimitIsRefusedAndTheRequestStaysOpen(t *testing.T) {
	ctx := context.Background()
	responder, requester := historyBook(t)
	keys := responderKeys(responder, 1)
	sent, honest := askFor(t, requester, responder, historyCard)
	deep := `[` + strings.Repeat("[", 100_000) + strings.Repeat("]", 100_000) + `]`
	receipts := make([]any, MaxReplyReceipts+1)
	for i := range receipts {
		receipts[i] = map[string]any{}
	}
	for name, hostile := range map[string]Message{
		"bytes":    withMember(t, honest.Message, "x", strings.Repeat("a", MaxReplyBytes)),
		"depth":    {Octets: append(bytes.TrimSuffix(bytes.Clone(honest.Message.Octets), []byte("}")), []byte(`,"x":`+deep+`}`)...), MediaType: MediaTypeJSON},
		"receipts": withMember(t, honest.Message, WitnessReceiptsMember, receipts),
	} {
		before := len(requester.order)
		start := time.Now()
		_, err := requester.RecordResponse(ctx, sent.RecordID, hostile, keys)
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("%s: refusing took %s", name, took)
		}
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "limit: ") {
			t.Fatalf("%s: got %v, want a refusal naming the limit", name, err)
		}
		if len(requester.order) != before {
			t.Fatalf("%s: a refused reply was recorded", name)
		}
	}
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, honest.Message, keys)
	if err != nil {
		t.Fatalf("the genuine answer after the refusals: %v", err)
	}
	if st := statementOf(t, recorded); st.Outcome != OutcomeArtifact || st.Witness != Unwitnessed {
		t.Fatalf("the genuine answer: %+v", st)
	}
}

// A signed envelope whose artifact is over a count limit is received and
// failed, naming the limit, before any of its checkpoints is checked.
func TestASignedEnvelopeOverACountLimitIsReceivedAndFailed(t *testing.T) {
	ctx := context.Background()
	responder, requester := historyBook(t)
	sent, honest := askFor(t, requester, responder, historyCard)
	e := mustEnvelope(t, honest)
	var card map[string]any
	if err := json.Unmarshal(e.Artifact, &card); err != nil {
		t.Fatal(err)
	}
	// The entries need not be well formed: the count is checked first.
	padded := make([]any, MaxReplyItems+1)
	for i := range padded {
		padded[i] = map[string]any{}
	}
	card["history"] = padded
	artifact, err := evidencerequest.EncodeJCS(card)
	if err != nil {
		t.Fatal(err)
	}
	e.Artifact, e.ArtifactDigest = artifact, digestBytes(artifact)
	start := time.Now()
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, resign(t, responder, e).Message, responderKeys(responder, 1))
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("failing the envelope took %s", took)
	}
	if err != nil {
		t.Fatal(err)
	}
	st := statementOf(t, recorded)
	if st.Outcome != OutcomeArtifactFailed || !strings.Contains(st.Failure, fmt.Sprintf("limit: history: %d over %d", MaxReplyItems+1, MaxReplyItems)) || st.Witness != "" {
		t.Fatalf("an envelope over the history limit: %+v", st)
	}
}

// The witness standing recorded for a verified answer: witnessed only by a
// receipt for the anchor from a witness in the requester's own directory. A
// receipt in the unsigned carrier that does not verify never fails the
// answer: anyone on the path could add one.
func TestRecordResponseWitnessStanding(t *testing.T) {
	ctx := context.Background()
	witnessKey, directory := testWitness()
	holderKey := ed25519.NewKeyFromSeed(seededKey(78)[:32])
	for name, tc := range map[string]struct {
		receipts  func(anchor string, statement []byte) []any
		directory []WitnessRow
		want      string
	}{
		"none": {nil, directory, Unwitnessed},
		"directory witness": {func(anchor string, statement []byte) []any {
			return []any{map[string]any{"checkpoint": anchor, "witness": mintReceipt(t, statement, witnessKey)}}
		}, directory, Witnessed},
		"directory witness, requester lists none": {func(anchor string, statement []byte) []any {
			return []any{map[string]any{"checkpoint": anchor, "witness": mintReceipt(t, statement, witnessKey)}}
		}, nil, Unwitnessed},
		"holder's own witness": {func(anchor string, statement []byte) []any {
			receipt := mintReceipt(t, statement, holderKey)
			receipt["ts_url"] = "https://holder.test"
			return []any{map[string]any{"checkpoint": anchor, "witness": receipt}}
		}, append(directory[:1:1], WitnessRow{Name: "holder", Endpoint: "https://elsewhere.test", KeyIDs: []string{hex.EncodeToString(holderKey.Public().(ed25519.PublicKey))}}), Unwitnessed},
		"forged": {func(anchor string, statement []byte) []any {
			return []any{map[string]any{"checkpoint": anchor, "witness": mintReceipt(t, statement, holderKey)}}
		}, directory, Unwitnessed},
		"named for another checkpoint": {func(_ string, statement []byte) []any {
			return []any{map[string]any{"checkpoint": strings.Repeat("0", 64), "witness": mintReceipt(t, statement, witnessKey)}}
		}, directory, Unwitnessed},
		"malformed carrier": {func(string, []byte) []any {
			return []any{"x", map[string]any{"checkpoint": 1}, map[string]any{"witness": "y"}}
		}, directory, Unwitnessed},
	} {
		t.Run(name, func(t *testing.T) {
			responder, requester := historyBook(t)
			keys := responderKeys(responder, 1)
			keys.Witnesses = tc.directory
			sent, honest := askFor(t, requester, responder, historyCard)
			reply := honest.Message
			if tc.receipts != nil {
				e := mustEnvelope(t, honest)
				var card struct {
					History []struct {
						Checkpoint string `json:"checkpoint"`
					} `json:"history"`
				}
				if err := json.Unmarshal(e.Artifact, &card); err != nil {
					t.Fatal(err)
				}
				statement, err := hex.DecodeString(card.History[len(card.History)-1].Checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				reply = withMember(t, reply, WitnessReceiptsMember, tc.receipts(e.Anchor.Digest, statement))
			}
			recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply, keys)
			if err != nil {
				t.Fatal(err)
			}
			if st := statementOf(t, recorded); st.Outcome != OutcomeArtifact || st.Witness != tc.want {
				t.Fatalf("recorded %+v, want %s", st, tc.want)
			}
		})
	}
}

// An answer whose material carries no anchor checkpoint (a correlation
// subject here) reads unwitnessed, whatever receipts come with it: there is
// no checkpoint in hand for a receipt to cover.
func TestAnAnswerWithNoAnchorCheckpointIsUnwitnessed(t *testing.T) {
	ctx := context.Background()
	responder, requester := historyBook(t)
	sent, honest := askFor(t, requester, responder, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "order-17"}, Coverage: fresh()})
	e := mustEnvelope(t, honest)
	witnessKey, directory := testWitness()
	keys := responderKeys(responder, 1)
	keys.Witnesses = directory
	history, err := responder.store.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reply := withMember(t, honest.Message, WitnessReceiptsMember, []any{map[string]any{"checkpoint": e.Anchor.Digest, "witness": mintReceipt(t, history[len(history)-1].Statement, witnessKey)}})
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply, keys)
	if err != nil {
		t.Fatal(err)
	}
	if st := statementOf(t, recorded); st.Outcome != OutcomeArtifact || st.Witness != Unwitnessed {
		t.Fatalf("recorded %+v", st)
	}
}

// A bundle that carries no receipt verifies, but reads unwitnessed; one
// whose receipt verifies under the requester's directory is witnessed.
func TestBundleWitnessStanding(t *testing.T) {
	witnessKey, directory := testWitness()
	for name, tc := range map[string]struct {
		carry bool
		want  bool
	}{"no receipt": {false, false}, "directory witness": {true, true}} {
		t.Run(name, func(t *testing.T) {
			c := newReceiptCase(t)
			c.keys.Witnesses = directory
			bundle, err := c.holder.Bundle(c.ctx, BundleRequest{Root: c.holder.order[len(c.holder.order)-1]})
			if err != nil {
				t.Fatal(err)
			}
			data := bundle.JSON
			if tc.carry {
				var wire map[string]any
				if err := json.Unmarshal(data, &wire); err != nil {
					t.Fatal(err)
				}
				member := wire["checkpoint"].(map[string]any)
				statement, err := base64.RawURLEncoding.DecodeString(member["cose"].(string))
				if err != nil {
					t.Fatal(err)
				}
				member["witnesses"] = []any{mintReceipt(t, statement, witnessKey)}
				if data, err = json.Marshal(wire); err != nil {
					t.Fatal(err)
				}
			}
			verified, err := VerifyBundleAnswer(data, c.keys)
			if err != nil {
				t.Fatal(err)
			}
			unwitnessed := false
			for _, f := range verified.Findings {
				unwitnessed = unwitnessed || f == Unwitnessed
			}
			if verified.Witnessed != tc.want || unwitnessed == tc.want {
				t.Fatalf("witnessed %v, findings %v", verified.Witnessed, verified.Findings)
			}
		})
	}
}

// A bundle over a limit is refused naming it, before the bundle verifier
// runs.
func TestBundleLimits(t *testing.T) {
	c := newReceiptCase(t)
	bundle, err := c.holder.Bundle(c.ctx, BundleRequest{Root: c.holder.order[len(c.holder.order)-1]})
	if err != nil {
		t.Fatal(err)
	}
	encode := func(change func(map[string]any)) []byte {
		var copied map[string]any
		if err := json.Unmarshal(bundle.JSON, &copied); err != nil {
			t.Fatal(err)
		}
		change(copied)
		data, err := json.Marshal(copied)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for name, tc := range map[string]struct {
		data []byte
		want string
	}{
		"bytes": {encode(func(m map[string]any) { m["x"] = strings.Repeat("a", MaxReplyBytes) }), "limit: bundle bytes"},
		"depth": {append(bytes.TrimSuffix(bytes.Clone(bundle.JSON), []byte("}")), []byte(`,"x":`+strings.Repeat("[", MaxReplyDepth)+strings.Repeat("]", MaxReplyDepth)+`}`)...), "limit: bundle depth"},
		"records": {encode(func(m map[string]any) {
			many := make([]any, MaxReplyItems+1)
			for i := range many {
				many[i] = map[string]any{}
			}
			m["records"] = many
		}), "limit: records"},
		"receipts": {encode(func(m map[string]any) {
			m["checkpoint"].(map[string]any)["witnesses"] = make([]any, MaxReplyReceipts+1)
		}), "limit: witness receipts"},
	} {
		start := time.Now()
		_, err := VerifyBundle(tc.data)
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("%s: refusing took %s", name, took)
		}
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

// The receipt carrier is unsigned on a refusal too: over its limit, a
// genuinely signed refusal is refused unrecorded, and the request stays
// open for it.
func TestARefusalOverTheReceiptLimitIsRefusedUnrecorded(t *testing.T) {
	ctx := context.Background()
	responder, requester := historyBook(t)
	keys := responderKeys(responder, 1)
	sent, honest := askFor(t, requester, responder, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()})
	if honest.Refusal == nil {
		t.Fatal("full_history should be refused")
	}
	receipts := make([]any, MaxReplyReceipts+1)
	for i := range receipts {
		receipts[i] = map[string]any{}
	}
	before := len(requester.order)
	if _, err := requester.RecordResponse(ctx, sent.RecordID, withMember(t, honest.Message, WitnessReceiptsMember, receipts), keys); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "limit: witness receipts") {
		t.Fatalf("got %v", err)
	}
	if len(requester.order) != before {
		t.Fatal("a refused reply was recorded")
	}
	if _, err := requester.RecordResponse(ctx, sent.RecordID, honest.Message, keys); err != nil {
		t.Fatalf("the genuine refusal after: %v", err)
	}
}

// An artifact swapped in on the path, beside a genuine signature that covers
// only the artifact's digest, fails on that digest: no limit is charged to
// the responder for a shape it never signed.
func TestASubstitutedArtifactFailsOnItsDigestNotOnALimit(t *testing.T) {
	ctx := context.Background()
	responder, requester := historyBook(t)
	sent, honest := askFor(t, requester, responder, historyCard)
	e := mustEnvelope(t, honest)
	e.Artifact = []byte(`{"history":[` + strings.TrimSuffix(strings.Repeat("{},", MaxReplyItems+1), ",") + `]}`)
	reply, err := evidencerequest.EncodeReply(MediaTypeJSON, &e, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 1))
	if err != nil {
		t.Fatal(err)
	}
	if st := statementOf(t, recorded); st.Outcome != OutcomeArtifactFailed || !strings.Contains(st.Failure, "artifact_digest") || strings.Contains(st.Failure, "limit") {
		t.Fatalf("recorded %+v", st)
	}
}
