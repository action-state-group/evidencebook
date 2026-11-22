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
	Parties map[string]refereeParty
}

func loadRefereeFixture(t *testing.T) refereeFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/referee-result.json")
	require.NoError(t, err)
	var f struct {
		Capsule json.RawMessage `json:"capsule"`
		Doc     string          `json:"doc"`
		Buyer   refereeParty    `json:"buyer"`
		Seller  refereeParty    `json:"seller"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	return refereeFixture{Capsule: f.Capsule, Doc: f.Doc, Parties: map[string]refereeParty{"buyer": f.Buyer, "seller": f.Seller}}
}

func (f refereeFixture) keyID(t *testing.T, role string) string {
	t.Helper()
	var half map[string]any
	require.NoError(t, json.Unmarshal(f.Parties[role].Half, &half))
	return half["key_id"].(string)
}

// withBook opens the deal profile's shelf and the node book for one call.
func withBook(t *testing.T, fn func(ctx context.Context, b *evidencebook.Book)) {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	b, err := s.shelf.Open(t.Context(), "goose/node")
	require.NoError(t, err)
	fn(t.Context(), b)
}

func nodeBookRecords(t *testing.T, b *evidencebook.Book, recordType string) []nodeRecord {
	t.Helper()
	got, err := nodeRecordsOf(t.Context(), b, recordType)
	require.NoError(t, err)
	return got
}

func TestRefereeReceiveSealsTheHalvesThenTheResult(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	withBook(t, func(ctx context.Context, b *evidencebook.Book) {
		out, err := receiveRefereeResult(ctx, b, f.keyID(t, "buyer"), testRefereeTypes, f.Capsule, []byte(f.Doc), f.Parties)
		require.NoError(t, err)
		require.Nil(t, out.Refusal)
		assert.False(t, out.Existing)

		halves, received := nodeBookRecords(t, b, "t_half"), nodeBookRecords(t, b, "t_received")
		require.Len(t, halves, 2)
		require.Len(t, received, 1)
		assert.Equal(t, []string{halves[0].RecordID, halves[1].RecordID}, received[0].Cites, "the received Result cites the halves it judges")
		var statement map[string]any
		require.NoError(t, json.Unmarshal(received[0].Statement, &statement))
		var capsule map[string]any
		require.NoError(t, json.Unmarshal(f.Capsule, &capsule))
		assert.Equal(t, capsule["capsule_id"], statement["result_capsule_id"])
		assert.Equal(t, capsule["key_id"], statement["referee"])
		assert.Regexp(t, `^[0-9a-f]{64}$`, statement["result_digest"])
		assert.Equal(t, statement["result_digest"], received[0].SubjectRef)
		assert.Empty(t, nodeBookRecords(t, b, "t_refused"))
	})
}

// A repeat of one delivery, or a retry after an interruption, seals nothing twice.
func TestRefereeReceiveIsSafeToRepeat(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	withBook(t, func(ctx context.Context, b *evidencebook.Book) {
		self := f.keyID(t, "seller")
		_, err := receiveRefereeResult(ctx, b, self, testRefereeTypes, f.Capsule, []byte(f.Doc), f.Parties)
		require.NoError(t, err)
		again, err := receiveRefereeResult(ctx, b, self, testRefereeTypes, f.Capsule, []byte(f.Doc), f.Parties)
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
		require.NoError(t, json.Unmarshal(f.Parties["buyer"].Half, &buyer))
		half, err := halfJCS(f.Parties["buyer"].Half)
		require.NoError(t, err)
		_, err = sealOnce(ctx, b, testRefereeTypes.Half, "capsule_id", json.RawMessage(quote(buyer["capsule_id"].(string))),
			map[string]any{"role": "buyer", "capsule_id": buyer["capsule_id"], "key_id": buyer["key_id"]}, buyer["capsule_id"].(string), nil, [][]byte{half})
		require.NoError(t, err)

		out, err := receiveRefereeResult(ctx, b, f.keyID(t, "buyer"), testRefereeTypes, f.Capsule, []byte(f.Doc), f.Parties)
		require.NoError(t, err)
		require.Nil(t, out.Refusal)
		assert.Len(t, nodeBookRecords(t, b, "t_half"), 2, "the half already held is not held again")
		assert.Len(t, nodeBookRecords(t, b, "t_received"), 1)
	})
}

func TestRefereeReceiveRefusalsAreSealedOnceWithTheirCode(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	other := f.keyID(t, "buyer")[:63] + "0"
	tamperedHalf := func() map[string]refereeParty {
		var half map[string]any
		require.NoError(t, json.Unmarshal(f.Parties["buyer"].Half, &half))
		half["operator"] = "someone-else"
		raw, err := json.Marshal(half)
		require.NoError(t, err)
		p := map[string]refereeParty{"buyer": f.Parties["buyer"], "seller": f.Parties["seller"]}
		b := p["buyer"]
		b.Half = raw
		p["buyer"] = b
		return p
	}()
	cases := []struct {
		name    string
		self    string
		octets  []byte
		parties map[string]refereeParty
		code    string
	}{
		{"a node that signed neither half", other, []byte(f.Doc), f.Parties, "not_a_party"},
		{"octets that are not the referee's JCS bytes", f.keyID(t, "buyer"), append([]byte(f.Doc), '\n'), f.Parties, referee.CodeResultMismatch},
		{"a half that does not verify", f.keyID(t, "buyer"), []byte(f.Doc), tamperedHalf, referee.CodeHalfUnverified},
		{"a delivery that is no Result", f.keyID(t, "buyer"), []byte("nope"), f.Parties, referee.CodeNotAResult},
		{"one party missing", f.keyID(t, "buyer"), []byte(f.Doc), map[string]refereeParty{"buyer": f.Parties["buyer"]}, referee.CodeNotAResult},
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
				assert.Contains(t, string(refused[0].Statement), c.code)
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

func TestDealNodeRefereeReceiveVerb(t *testing.T) {
	dealFixture(t)
	f := loadRefereeFixture(t)
	container, err := json.Marshal(map[string]any{"capsule": json.RawMessage(f.Capsule), "document": base64.StdEncoding.EncodeToString([]byte(f.Doc))})
	require.NoError(t, err)
	parties, err := json.Marshal(f.Parties)
	require.NoError(t, err)
	result, partiesFile := writeJSON(t, string(container)), writeJSON(t, string(parties))
	args := []string{"--profile", "deal", "deal", "node", "referee", "receive", "--book", "goose/node",
		"--received-type", "t_received", "--refused-type", "t_refused", "--half-type", "t_half"}

	// The profile's key signed neither half, so the Result is refused and the refusal sealed.
	out, err := invoke(t, "", append(args, "--result", result, "--parties", partiesFile)...)
	require.ErrorIs(t, err, cli.ErrInput, out)
	assert.Contains(t, err.Error(), "not_a_party")
	assert.Contains(t, err.Error(), "sealed as record")
	refused := nodeRecords(t, "--book", "goose/node", "--type", "t_refused")
	require.Len(t, refused, 1)
	assert.Empty(t, nodeRecords(t, "--book", "goose/node", "--type", "t_received"))

	_, err = invoke(t, "", append(args, "--result", result, "--parties", partiesFile)...)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Len(t, nodeRecords(t, "--book", "goose/node", "--type", "t_refused"), 1, "a repeat seals the refusal once")

	for name, bad := range map[string][]string{
		"no result":           {"--parties", partiesFile},
		"no parties":          {"--result", result},
		"result not base64":   {"--result", writeJSON(t, `{"capsule":{},"document":"!!"}`), "--parties", partiesFile},
		"result no document":  {"--result", writeJSON(t, `{"capsule":{}}`), "--parties", partiesFile},
		"parties not JSON":    {"--result", result, "--parties", writeJSON(t, `[`)},
		"a library type name": {"--result", result, "--parties", partiesFile, "--received-type", "index_root"},
	} {
		_, err := invoke(t, "", append(append([]string{}, args...), bad...)...)
		require.ErrorIs(t, err, cli.ErrInput, name)
	}
}
