package deal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-engine/go/referee"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testRefereeTypes = refereeTypes{Received: "t_received", Refused: "t_refused", Half: "t_half"}

// refereeFixture is a valid Result sealed live by capsule-engine's referee
// vectors (cases ok and bind-ok): the referee's Capsule, its document, and both
// parties' halves with their openings. The halves are signed by keys the test
// does not hold, so the receive path is run with a stated key id, and the verb
// itself with the profile's key, which signed neither.
type refereeFixture struct {
	Capsule json.RawMessage
	Doc     string
	Parties map[string]map[string]json.RawMessage
}

func loadRefereeFixture(t *testing.T) refereeFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/referee-result.json")
	require.NoError(t, err)
	var f struct {
		Capsule json.RawMessage            `json:"capsule"`
		Doc     string                     `json:"doc"`
		Buyer   map[string]json.RawMessage `json:"buyer"`
		Seller  map[string]json.RawMessage `json:"seller"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	return refereeFixture{Capsule: f.Capsule, Doc: f.Doc, Parties: map[string]map[string]json.RawMessage{"buyer": f.Buyer, "seller": f.Seller}}
}

// parties is the delivery's parties as the file holds them, with edit applied to a copy.
func (f refereeFixture) parties(t *testing.T, edit func(p map[string]map[string]json.RawMessage)) []byte {
	t.Helper()
	copyOf := map[string]map[string]json.RawMessage{}
	for role, members := range f.Parties {
		copyOf[role] = map[string]json.RawMessage{}
		for k, v := range members {
			copyOf[role][k] = v
		}
	}
	if edit != nil {
		edit(copyOf)
	}
	raw, err := json.Marshal(copyOf)
	require.NoError(t, err)
	return raw
}

func (f refereeFixture) keyID(t *testing.T, role string) string {
	t.Helper()
	var half map[string]any
	require.NoError(t, json.Unmarshal(f.Parties[role]["half"], &half))
	return half["key_id"].(string)
}

// withBook opens the deal profile's shelf and the node book for one call.
func withBook(t *testing.T, fn func(ctx context.Context, b *evidencebook.Book)) {
	t.Helper()
	withSessionBook(t, func(ctx context.Context, _ *dealSession, b *evidencebook.Book) { fn(ctx, b) })
}

func withSessionBook(t *testing.T, fn func(ctx context.Context, s *dealSession, b *evidencebook.Book)) {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	b, err := s.shelf.Open(t.Context(), "goose/node")
	require.NoError(t, err)
	fn(t.Context(), s, b)
}

func nodeBookRecords(t *testing.T, b *evidencebook.Book, recordType string) []nodeRecord {
	t.Helper()
	got, err := nodeRecordsOf(t.Context(), b, recordType)
	require.NoError(t, err)
	return got
}

func jsonMember(t *testing.T, raw json.RawMessage, name string) any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m[name]
}

func TestRefereeReceiveSealsTheHalvesThenTheResult(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	withSessionBook(t, func(ctx context.Context, s *dealSession, b *evidencebook.Book) {
		out, err := receiveRefereeResult(ctx, b, f.keyID(t, "buyer"), testRefereeTypes, f.Capsule, []byte(f.Doc), f.parties(t, nil))
		require.NoError(t, err)
		require.Nil(t, out.Refusal)
		assert.False(t, out.Existing)

		halves, received := nodeBookRecords(t, b, "t_half"), nodeBookRecords(t, b, "t_received")
		require.Len(t, halves, 2)
		require.Len(t, received, 1)
		assert.Equal(t, []string{halves[0].RecordID, halves[1].RecordID}, received[0].Cites, "the received Result cites the halves it judges")
		var capsule map[string]any
		require.NoError(t, json.Unmarshal(f.Capsule, &capsule))
		assert.Equal(t, capsule["capsule_id"], jsonMember(t, received[0].Statement, "result_capsule_id"))
		assert.Equal(t, capsule["key_id"], jsonMember(t, received[0].Statement, "referee"))
		assert.Regexp(t, `^[0-9a-f]{64}$`, jsonMember(t, received[0].Statement, "result_digest"))
		assert.Equal(t, jsonMember(t, received[0].Statement, "result_digest"), received[0].SubjectRef)
		assert.Empty(t, nodeBookRecords(t, b, "t_refused"))

		// Each held half keeps the half's canonical bytes as its payload, byte for byte.
		for i, role := range []string{"buyer", "seller"} {
			want, err := halfJCS(f.Parties[role]["half"])
			require.NoError(t, err)
			assert.Equal(t, role, jsonMember(t, halves[i].Statement, "role"))
			held, err := s.originals.Get(ctx, halves[i].RecordID)
			require.NoError(t, err)
			require.NotEmpty(t, held.Artifacts, role)
			assert.Equal(t, want, held.Artifacts[0].Content, role)
		}
	})
}

// A repeat of one delivery, or a retry after an interruption, seals nothing twice.
func TestRefereeReceiveIsSafeToRepeat(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	withBook(t, func(ctx context.Context, b *evidencebook.Book) {
		self := f.keyID(t, "seller")
		_, err := receiveRefereeResult(ctx, b, self, testRefereeTypes, f.Capsule, []byte(f.Doc), f.parties(t, nil))
		require.NoError(t, err)
		again, err := receiveRefereeResult(ctx, b, self, testRefereeTypes, f.Capsule, []byte(f.Doc), f.parties(t, nil))
		require.NoError(t, err)
		assert.True(t, again.Existing)
		assert.Len(t, nodeBookRecords(t, b, "t_half"), 2)
		assert.Len(t, nodeBookRecords(t, b, "t_received"), 1)
	})
}

// An interruption after the first half was held: the retry holds the second
// and seals the Result, and the first half is not held twice.
func TestRefereeReceiveResumesAfterAnInterruption(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	withBook(t, func(ctx context.Context, b *evidencebook.Book) {
		var buyer map[string]any
		require.NoError(t, json.Unmarshal(f.Parties["buyer"]["half"], &buyer))
		half, err := halfJCS(f.Parties["buyer"]["half"])
		require.NoError(t, err)
		_, err = sealOnce(ctx, b, testRefereeTypes.Half, map[string]string{"capsule_id": buyer["capsule_id"].(string)},
			buyer["capsule_id"].(string), nil, [][]byte{half}, "role", "buyer", "key_id", buyer["key_id"].(string))
		require.NoError(t, err)

		out, err := receiveRefereeResult(ctx, b, f.keyID(t, "buyer"), testRefereeTypes, f.Capsule, []byte(f.Doc), f.parties(t, nil))
		require.NoError(t, err)
		require.Nil(t, out.Refusal)
		assert.Len(t, nodeBookRecords(t, b, "t_half"), 2, "the half already held is not held again")
		assert.Len(t, nodeBookRecords(t, b, "t_received"), 1)
	})
}

func TestRefereeReceiveRefusalsAreSealedOnceWithTheirCode(t *testing.T) {
	f := loadRefereeFixture(t)
	self := f.keyID(t, "buyer")
	other := self[:63] + "0"
	if other == self {
		other = self[:63] + "1"
	}
	bothBuyer := func(p map[string]map[string]json.RawMessage) { p["seller"] = p["buyer"] }
	setBuyer := func(member, value string) []byte {
		return f.parties(t, func(p map[string]map[string]json.RawMessage) { p["buyer"][member] = json.RawMessage(value) })
	}
	tamperedHalf := f.parties(t, func(p map[string]map[string]json.RawMessage) {
		var half map[string]any
		require.NoError(t, json.Unmarshal(p["buyer"]["half"], &half))
		half["operator"] = "someone-else"
		raw, err := json.Marshal(half)
		require.NoError(t, err)
		p["buyer"]["half"] = raw
	})
	cases := []struct {
		name    string
		self    string
		octets  []byte
		parties []byte
		code    string
	}{
		{"a node that signed neither half", other, []byte(f.Doc), f.parties(t, nil), "not_a_party"},
		{"a node that signed neither half, halves of one key: not_a_party comes first", other, []byte(f.Doc), f.parties(t, bothBuyer), "not_a_party"},
		{"halves of one key, signed by this node", self, []byte(f.Doc), f.parties(t, bothBuyer), referee.CodeHalvesUnpaired},
		{"a half that does not verify", self, []byte(f.Doc), tamperedHalf, referee.CodeHalfUnverified},
		{"a half with no members", self, []byte(f.Doc), setBuyer("half", `{}`), referee.CodeHalfUnverified},
		{"an opening that does not recompute", self, []byte(f.Doc), setBuyer("action", `"edited"`), referee.CodeOpeningMismatch},
		{"octets with trailing whitespace are not the JCS bytes", self, append([]byte(f.Doc), '\n'), f.parties(t, nil), referee.CodeResultMismatch},
		{"octets with trailing junk are not a Result", self, append([]byte(f.Doc), 'x'), f.parties(t, nil), referee.CodeNotAResult},
		{"octets with a second value are not a Result", self, append([]byte(f.Doc), []byte("{}")...), f.parties(t, nil), referee.CodeNotAResult},
		{"a delivery that is no Result", self, []byte("nope"), f.parties(t, nil), referee.CodeNotAResult},
		{"parties that are not an object", self, []byte(f.Doc), []byte(`[]`), referee.CodeNotAResult},
		{"one party missing", self, []byte(f.Doc), f.parties(t, func(p map[string]map[string]json.RawMessage) { delete(p, "seller") }), referee.CodeNotAResult},
		{"a party that is not an object", self, []byte(f.Doc), []byte(`{"buyer":[],"seller":{}}`), referee.CodeNotAResult},
		{"a party with no action", self, []byte(f.Doc), f.parties(t, func(p map[string]map[string]json.RawMessage) { delete(p["buyer"], "action") }), referee.CodeNotAResult},
		{"an action that is not text", self, []byte(f.Doc), setBuyer("action", `7`), referee.CodeNotAResult},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dealFixture(t)
			withBook(t, func(ctx context.Context, b *evidencebook.Book) {
				out, err := receiveRefereeResult(ctx, b, c.self, testRefereeTypes, f.Capsule, c.octets, c.parties)
				require.NoError(t, err)
				require.NotNil(t, out.Refusal, "refused")
				assert.Equal(t, c.code, out.Refusal.Code)
				refused := nodeBookRecords(t, b, "t_refused")
				require.Len(t, refused, 1, "the refusal is sealed, never silence")
				assert.Equal(t, c.code, jsonMember(t, refused[0].Statement, "code"))
				assert.Empty(t, nodeBookRecords(t, b, "t_received"))
				assert.Empty(t, nodeBookRecords(t, b, "t_half"), "no half is held for a refused Result")

				again, err := receiveRefereeResult(ctx, b, c.self, testRefereeTypes, f.Capsule, c.octets, c.parties)
				require.NoError(t, err)
				require.NotNil(t, again.Refusal)
				assert.Len(t, nodeBookRecords(t, b, "t_refused"), 1, "the same refusal is sealed once")
			})
		})
	}
}

// One delivery refused for two reasons in turn is two refusal records, each once.
func TestRefereeReceiveSealsEachRefusalCodeOnce(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	other := f.keyID(t, "buyer")[:63] + "0"
	withBook(t, func(ctx context.Context, b *evidencebook.Book) {
		for i := 0; i < 2; i++ {
			out, err := receiveRefereeResult(ctx, b, other, testRefereeTypes, f.Capsule, []byte(f.Doc), f.parties(t, nil))
			require.NoError(t, err)
			require.Equal(t, "not_a_party", out.Refusal.Code)
			out, err = receiveRefereeResult(ctx, b, f.keyID(t, "buyer"), testRefereeTypes, f.Capsule, []byte(f.Doc), []byte(`[]`))
			require.NoError(t, err)
			require.Equal(t, referee.CodeNotAResult, out.Refusal.Code)
		}
		assert.Len(t, nodeBookRecords(t, b, "t_refused"), 2, "two different codes for one digest are two records, each sealed once")
	})
}

func TestDealNodeRefereeReceiveVerb(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	container, err := json.Marshal(map[string]any{"capsule": json.RawMessage(f.Capsule), "document": base64.StdEncoding.EncodeToString([]byte(f.Doc))})
	require.NoError(t, err)
	result, partiesFile := writeJSON(t, string(container)), writeJSON(t, string(f.parties(t, nil)))
	args := []string{"--profile", "deal", "deal", "node", "referee", "receive", "--book", "goose/node",
		"--received-type", "t_received", "--refused-type", "t_refused", "--half-type", "t_half"}

	// The profile's key signed neither half, so the Result is refused and the refusal sealed.
	out, err := invoke(t, "", append(args, "--result", result, "--parties", partiesFile)...)
	require.ErrorIs(t, err, cli.ErrInput, out)
	assert.Contains(t, err.Error(), "not_a_party")
	assert.Contains(t, err.Error(), "sealed as record")
	require.Len(t, nodeRecords(t, "--book", "goose/node", "--type", "t_refused"), 1)
	assert.Empty(t, nodeRecords(t, "--book", "goose/node", "--type", "t_received"))

	_, err = invoke(t, "", append(args, "--result", result, "--parties", partiesFile)...)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Len(t, nodeRecords(t, "--book", "goose/node", "--type", "t_refused"), 1, "a repeat seals the refusal once")

	// A parties file of the wrong shape is a delivery that is no Result: sealed, not an input error.
	_, err = invoke(t, "", append(args, "--result", result, "--parties", writeJSON(t, `[]`))...)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "not_a_result")
	assert.Len(t, nodeRecords(t, "--book", "goose/node", "--type", "t_refused"), 2)

	for name, bad := range map[string][]string{
		"no result":           {"--parties", partiesFile},
		"no parties":          {"--result", result},
		"result not base64":   {"--result", writeJSON(t, `{"capsule":{},"document":"!!"}`), "--parties", partiesFile},
		"result no document":  {"--result", writeJSON(t, `{"capsule":{}}`), "--parties", partiesFile},
		"parties not JSON":    {"--result", result, "--parties", writeJSON(t, `[`)},
		"a library type name": {"--result", result, "--parties", partiesFile, "--received-type", "index_root"},
		"one name for two":    {"--result", result, "--parties", partiesFile, "--received-type", "t_refused"},
	} {
		_, err := invoke(t, "", append(append([]string{}, args...), bad...)...)
		require.ErrorIs(t, err, cli.ErrInput, name)
	}
	assert.Len(t, nodeRecords(t, "--book", "goose/node", "--type", "t_refused"), 2, "input errors seal nothing")
}
