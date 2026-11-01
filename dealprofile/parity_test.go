// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/parity-vectors.json is what the Python implementation
// (python/evidencebook/deal) is checked against, beside the profile's own
// fixtures and testdata/baseline-vectors.json, which it passes too: Go's
// answers to identifier normalization, the record-level stages and the
// standard_history/1 deal entry for inputs the fixtures do not reach
// (Unicode edges, the number forms, the member types the entry reads).
// Regenerate it only for an intended change:
//
//	EB_WRITE_DEAL_PARITY_VECTORS=1 go test ./dealprofile -run TestParityVectors
//
// A case holds characters Unicode 13 (Python 3.10) already assigns, so every
// Python the package supports reads it as Go does.

const parityVectorsPath = "testdata/parity-vectors.json"

type parityNormalize struct {
	Kind       string  `json:"kind"`
	Raw        string  `json:"raw"`
	Normalized *string `json:"normalized"`
	FP         *string `json:"fp"`
}

type parityCheck struct {
	Name        string   `json:"name"`
	Record      string   `json:"record"`
	LocalValues []string `json:"local_values,omitempty"`
	// Stage is the stage that refused the record, "" for a record that passes.
	Stage   string `json:"stage"`
	Payload string `json:"payload,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

type parityRecord struct {
	Payload     string `json:"payload"`
	CommittedAt string `json:"committed_at"`
}

type parityResult struct {
	Result   string `json:"result"`
	Verified bool   `json:"verified"`
}

type parityHistory struct {
	Name                     string                `json:"name"`
	Records                  []parityRecord        `json:"records"`
	Results                  []parityResult        `json:"results,omitempty"`
	CounterpartyHalfVerified bool                  `json:"counterparty_half_verified,omitempty"`
	Entry                    *standardhistory.Deal `json:"entry"`
	// Error is "unpriced" for ErrUnpriced, "other" for any other refusal.
	Error string `json:"error,omitempty"`
}

// parityCard is the card standardhistory.Derive makes over deal entries
// (agent-action-capsule), canonicalized: what the Python derive_card makes of
// the same entries is the same bytes.
type parityCard struct {
	Name   string                 `json:"name"`
	AsOf   standardhistory.AsOf   `json:"as_of"`
	Policy standardhistory.Policy `json:"policy"`
	Deals  []string               `json:"deals"`
	Card   string                 `json:"card"`
}

type parityFile struct {
	Note string `json:"note"`
	// Versions are the tables the answers come from, so that a bump of one
	// that rewrites the vectors shows in their diff.
	Versions       map[string]string `json:"versions"`
	StoreSecretHex string            `json:"store_secret_hex"`
	DealID         string            `json:"deal_id"`
	Normalize      []parityNormalize `json:"normalize"`
	Check          []parityCheck     `json:"check"`
	History        []parityHistory   `json:"history"`
	Cards          []parityCard      `json:"cards"`
	MinorUnits     map[string]*int   `json:"minor_units"`
}

var normalizeCases = []struct{ kind, raw string }{
	// names
	{"name", "Coastal Jet Rentals LLC"}, {"name", "The Example Company, Inc."}, {"name", "   Straße GmbH "},
	{"name", "ÉCOLE d'été"}, {"name", "école"}, {"name", "ΣΊΣΥΦΟΣ ΟΔΟΣ"}, {"name", "İstanbul Co"},
	{"name", "ǅungla"}, {"name", "ﬁnance corp"}, {"name", "A\x1cB"}, {"name", "A\u0085B C"}, {"name", "A\x0bB"},
	{"name", "---"}, {"name", "the"}, {"name", "llc inc"}, {"name", "The The Co"},
	{"name", "O'Brien & Sons, Ltd."}, {"name", "日本語 株式会社"}, {"name", "Ａｂｃ"}, {"name", "a­b"}, {"name", "x_y¿z¡"},
	// phones
	{"phone", "(415) 555-0199"}, {"phone", "+1 415 555 0199 ext. 12"}, {"phone", "415-555-0199 x7"}, {"phone", "415-555-0199 #7"},
	{"phone", "1 415 555 0199"}, {"phone", "+44 20 7946 0958"}, {"phone", "+4"}, {"phone", "12345"}, {"phone", "+1234567"},
	{"phone", "+123456789012345"}, {"phone", "+1234567890123456"}, {"phone", "０１２３４５６７８９０"}, {"phone", "+٤٤٢٠٧٩٤٦٠٩٥٨"},
	{"phone", "415 555 0199 EXT 9"}, {"phone", " +1 (415) 555-0199 "}, {"phone", "415.555.0199\n"},
	// emails
	{"email", "Alice@Example.COM"}, {"email", " a@b "}, {"email", "a@@b"}, {"email", "@b"}, {"email", "a@"}, {"email", "É@É.fr"},
	{"email", "é@x.org"}, {"email", "ΑΣ@x.org"}, {"email", "İ@x.org"}, {"email", "a @b.c"},
	// relay addresses
	{"relay_address", " Relay-ABC@Privaterelay.Example "}, {"relay_address", "İ"}, {"relay_address", "ΑΣ "}, {"relay_address", ""},
	// profile ids
	{"profile_id", " Pr-1 "}, {"profile_id", "é"}, {"profile_id", "   "}, {"profile_id", ""}, {"profile_id", "\x1cA\x1c"},
	// payees route by shape
	{"payee", "Alice@Example.com"}, {"payee", "+1 (415) 555-0199"}, {"payee", "415-555-0199"}, {"payee", "Coastal Jet Rentals LLC"},
	{"payee", "  1234567  "}, {"payee", "12-34"}, {"payee", "+٤٤٢٠٧٩٤٦٠٩٥٨"}, {"payee", "1234567 "},
	// domains
	{"domain", "https://www.Example.COM/path?x=1#f"}, {"domain", "user:pw@shop.example.co.uk:8080"}, {"domain", "a.b.c.example.com."},
	{"domain", "example.com..."}, {"domain", "münchen.de"}, {"domain", "xn--mnchen-3ya.de"}, {"domain", "WWW.XN--MNCHEN-3YA.DE"},
	{"domain", "faß.de"}, {"domain", "www.bücher.example"}, {"domain", "例え.jp"}, {"domain", "www.例え.テスト.jp"}, {"domain", "ex.ample"},
	{"domain", "foo.github.io"}, {"domain", "a.b.kobe.jp"}, {"domain", "city.kobe.jp"}, {"domain", "www.city.kobe.jp"},
	{"domain", "uk.com"}, {"domain", "com"}, {"domain", "localhost"}, {"domain", "192.0.2.1"}, {"domain", "a.cromulent"},
	{"domain", "a..b.com"}, {"domain", ".a.com"}, {"domain", "a_b.com"}, {"domain", "ab--c.com"}, {"domain", "a--b.com"},
	{"domain", "-a.com"}, {"domain", "a-.com"}, {"domain", "xn--.com"}, {"domain", "xn--a.com"}, {"domain", "xn--a-.com"},
	{"domain", "xn--zca.com"}, {"domain", "xn--ls8h.la"}, {"domain", "💩.la"}, {"domain", "ａｂｃ.ｃｏｍ"}, {"domain", "a。b。com"},
	{"domain", "ab­.com"}, {"domain", "́a.com"}, {"domain", "a‍b.com"}, {"domain", "ab‌.com"},
	{"domain", "אבג.example"}, {"domain", "אבג1.example"}, {"domain", "a.אבג.example"}, {"domain", "ß.com"}, {"domain", "ς.com"},
	{"domain", "ﬁ.com"}, {"domain", "a b.com"}, {"domain", "x@y@z.com"}, {"domain", "http://"}, {"domain", ""},
	{"domain", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.com"},
	{"domain", "1a.שלום.co.il"}, {"domain", "www.1a.שלום.com"}, {"domain", "שלום.1a.com"}, {"domain", "a.שלום.com"}, {"domain", "xn--ls8héa-.example"},
	{"domain", "xn--zcaé-.com"}, {"domain", "x.xn--o1ach.xn--с-btb8b"}, {"domain", "x.xn--o1ach.xn--90a3ac"}, {"domain", "[::1]"}, {"domain", "a.b/c@d.example"},
	// a kind outside the closed set
	{"nickname", "x"},
}

// checkTexts are the strings the personal-data and wording scans read.
var checkTexts = []string{
	"hello", "call 415 555 0199", "call 4155550199", "call +1 (415) 555-0199", "call +١٢١٢٥٥٥٠١٢٣", "a 123456 b", "a 1234567 b",
	"1-2-3-4-5-6-7", "7 4 5 5 5 0 1", "7\x1c4\x1c5\x1c5\x1c5\x1c0\x1c1", "a@b.co", "a@b", "a @ b.co",
	"a@b.c o", "x y@z.co", "contact me: ALICE@EXAMPLE.ORG!", "deal-0123456789abcdef", "deal-0123456789abcdef\n",
	"deal-0123456789abcdef\n\n", "sale-ff", "0123456789abcdef", "0123456789abcde", "0123456789ABCDEF", "2026-10-06", "2026-10-06\n",
	"2026-10-06T09:00:00Z", "٢٠٢٦-١٠-٠٦", "2026-10-06T09:00:00", "acme/tool/1.2.3", "acme/tool/1.2", "acme/tool/١.٢.٣",
	"This is a scam", "A Scammer!", "scam-free", "Scamp", "scamming", "FRAUDULENT", "fraud­ster", "rating: 5", "ratings", "reputation",
	"Blacklist", "block list", "trustworthiness", "trustworthy", "ſcam", "ſcore", "İscam", "Kscam", "SCORE",
	"the city of Boston", "Ünïcödé text ok",
}

var checkLocalValues = [][]string{nil, {"Alice Example"}, {"alice example", ""}, {"STRASSE"}, {"straße"}, {"İ"}, {"ǆ"}}

func parityRecordText(s string) string {
	raw, _ := json.Marshal(s)
	return `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":` + string(raw) + `}}`
}

func parityChecks(t *testing.T) []parityCheck {
	fixture, err := os.ReadFile("x-deal-v0/fixtures/positive/01-baseline.json")
	require.NoError(t, err)
	var fx struct {
		Record json.RawMessage `json:"record"`
	}
	require.NoError(t, json.Unmarshal(fixture, &fx))
	baseline := string(fx.Record)
	payFixture, err := os.ReadFile("x-deal-v0/fixtures/positive/13-check-pay.json")
	require.NoError(t, err)
	var pfx struct {
		Record map[string]any `json:"record"`
	}
	require.NoError(t, json.Unmarshal(payFixture, &pfx))
	var baselineMap map[string]any
	require.NoError(t, json.Unmarshal(fx.Record, &baselineMap))
	withClass := func(rec map[string]any, class any) string {
		c := maps.Clone(rec)
		c["counterparty_class"] = class
		out, err := json.Marshal(c)
		require.NoError(t, err)
		return string(out)
	}

	records := []struct{ name, text string }{
		{"a positive fixture as written", baseline},
		{"a pay check with a counterparty_class", withClass(pfx.Record, "shop_preauth")},
		{"a counterparty_class not one of the three", withClass(pfx.Record, "shop")},
		{"a counterparty_class that is not a string", withClass(pfx.Record, 1)},
		{"a counterparty_class on a baseline", withClass(baselineMap, "shop_preauth")},
		{"no block", `[1]`}, {"not an object", `"x"`}, {"a null block", `{"x-deal-v0":null,"body":{}}`},
		{"another canonicalization", `{"x-deal-v0":{"canonicalization":"jcs-n"},"body":{}}`},
		{"canonicalization is a number", `{"x-deal-v0":{"canonicalization":5},"body":{}}`},
		{"canonicalization is missing", `{"x-deal-v0":{},"body":{}}`},
		{"a float", `{"x-deal-v0":{"canonicalization":"jcs","seq":1.5},"body":{}}`},
		{"a whole float", `{"x-deal-v0":{"canonicalization":"jcs","seq":1.0},"body":{}}`},
		{"an exponent", `{"x-deal-v0":{"canonicalization":"jcs","seq":1e2},"body":{}}`},
		{"a negative zero", `{"x-deal-v0":{"canonicalization":"jcs","seq":-0},"body":{}}`},
		{"the largest safe integer", `{"x-deal-v0":{"canonicalization":"jcs","seq":9007199254740991},"body":{}}`},
		{"an unsafe integer", `{"x-deal-v0":{"canonicalization":"jcs","seq":9007199254740992},"body":{}}`},
		{"a huge integer", `{"x-deal-v0":{"canonicalization":"jcs","seq":12345678901234567890123},"body":{}}`},
		{"a lone surrogate", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\ud800"}}`},
		{"a low surrogate first", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\udc00x"}}`},
		{"a surrogate then a letter", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\ud800A"}}`},
		{"a surrogate pair", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"😀"}}`},
		{"an escaped backslash before a u", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\\ud800"}}`},
		{"a lone surrogate in a member name", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"\ud800":1}}`},
		{"an unsafe integer beside a phone", `{"x-deal-v0":{"canonicalization":"jcs","seq":9007199254740993},"body":{"text":"call 415 555 0199"}}`},
		{"off the schema", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{}}`},
		{"an unknown typed record", `{"type":"payment/v0","canonicalization":"jcs"}`},
		{"a typed record without a body", `{"type":"task-authority/v0","canonicalization":"jcs"}`},
		{"a typed type that is not a string", `{"type":5,"canonicalization":"jcs"}`},
		{"a phone in a member name", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"call 415 555 0199":1}}`},
		{"a grading word in a member name", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"a scam":1}}`},
		{"a phone and an email", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"b":"a@b.co","a":"415 555 0199"}}`},
		{"members in other orders", `{"body":{"b":1,"a":2},"x-deal-v0":{"seq":3,"canonicalization":"jcs"}}`},
		{"keys that sort by UTF-16 code unit", `{"x-deal-v0":{"canonicalization":"jcs"},"body":{"😀":1,"～":2,"ö":3}}`},
		{"duplicate members", `{"x-deal-v0":{"canonicalization":"jcs"},"x-deal-v0":{"canonicalization":"jcs-n"},"body":{}}`},
		{"white space around the text", " \n" + baseline + "\n "},
	}
	var out []parityCheck
	add := func(name, text string, local []string) {
		c := parityCheck{Name: name, Record: text, LocalValues: local}
		payload, digest, err := Encode([]byte(text), local)
		var se *StageError
		switch {
		case err == nil:
			c.Payload, c.Digest = string(payload), digest
		case errors.As(err, &se):
			c.Stage = se.Stage
		default:
			c.Stage = "error"
		}
		out = append(out, c)
	}
	for _, r := range records {
		add(r.name, r.text, nil)
	}
	for _, s := range checkTexts {
		add("text "+fmt.Sprintf("%q", s), parityRecordText(s), nil)
	}
	for _, local := range checkLocalValues {
		add(fmt.Sprintf("local values %q", local), parityRecordText("Alice Example is in the STRASSE, ǅ İx"), local)
	}
	return out
}

func parityHistories(t *testing.T) []parityHistory {
	var out []parityHistory
	add := func(name string, records []HistoryRecord, results []HistoryResult, half bool) {
		h := parityHistory{Name: name, CounterpartyHalfVerified: half}
		for _, r := range records {
			h.Records = append(h.Records, parityRecord{Payload: string(r.Payload), CommittedAt: r.CommittedAt.Format(time.RFC3339Nano)})
		}
		for _, r := range results {
			h.Results = append(h.Results, parityResult{Result: string(r.Result), Verified: r.Verified})
		}
		deal, ok, err := StandardHistoryDeal(HistoryInput{Records: records, Results: results, CounterpartyHalfVerified: half})
		switch {
		case errors.Is(err, ErrUnpriced):
			h.Error = "unpriced"
		case err != nil:
			h.Error = "other"
		case ok:
			h.Entry = &deal
		}
		out = append(out, h)
	}
	typed := fixtureHistory(t, "positive-typed")
	add("a typed deal", typed, nil, false)
	add("a typed deal stamped by both", typed, nil, true)
	resultsCases := map[string][]HistoryResult{
		"held": {result(true, "met", "met")}, "did not hold": {result(true, "met", "not_met")}, "met and undecided": {result(true, "met", "undecided")},
		"no claim": {result(true)}, "the newest counts": {result(true, "not_met"), result(true, "met")}, "unverified": {result(false, "met")},
		"skips the unverified": {result(true, "not_met"), result(false, "met")}, "unreadable": {{Result: []byte("{"), Verified: true}},
		"claims is null": {{Result: []byte(`{"claims":null}`), Verified: true}}, "a claim of the wrong type": {{Result: []byte(`{"claims":[{"verdict":5}]}`), Verified: true}},
		"a claim that is not an object": {{Result: []byte(`{"claims":[1]}`), Verified: true}}, "a null claim": {{Result: []byte(`{"claims":[null,{"verdict":"met"}]}`), Verified: true}},
		"claims is an object": {{Result: []byte(`{"claims":{}}`), Verified: true}}, "not an object": {{Result: []byte(`[]`), Verified: true}},
		"a float in a claim": {{Result: []byte(`{"claims":[{"verdict":"met","n":1.5}]}`), Verified: true}},
	}
	for _, name := range slices.Sorted(maps.Keys(resultsCases)) {
		add("typed results: "+name, typed, resultsCases[name], false)
	}
	var cut []HistoryRecord
	for i, r := range typed {
		if i != 4 {
			cut = append(cut, r)
		}
	}
	add("a typed deal without its proposal", cut, nil, false)
	for _, dir := range []string{"positive", "positive-typed-seller", "positive-sale"} {
		add("not a deal: "+dir, fixtureHistory(t, dir), nil, false)
	}
	add("no records", nil, nil, false)
	add("a record that is not JSON", []HistoryRecord{{Payload: []byte("not json"), CommittedAt: historyStart}}, nil, false)
	add("a record that is an array", []HistoryRecord{{Payload: []byte("[]"), CommittedAt: historyStart}}, nil, false)
	add("a block of the wrong type", []HistoryRecord{{Payload: []byte(`{"x-deal-v0":5}`), CommittedAt: historyStart}}, nil, false)
	add("a deal id of the wrong type", []HistoryRecord{{Payload: []byte(`{"x-deal-v0":{"deal_id":5,"record_type":"baseline"},"body":{}}`), CommittedAt: historyStart}}, nil, false)
	add("a ref member of the wrong type", []HistoryRecord{{Payload: []byte(`{"type":"x","refs":[{"rel":"a","digest":"d","type":5}]}`), CommittedAt: historyStart}}, nil, false)
	add("a block ref member of the wrong type", []HistoryRecord{{Payload: []byte(`{"x-deal-v0":{"record_type":"note","deal_id":"d","refs":[{"rel":"a","digest_alg":7}]}}`), CommittedAt: historyStart}}, nil, false)
	add("a byte order mark", []HistoryRecord{{Payload: []byte("\ufeff{\"type\":\"x\"}"), CommittedAt: historyStart}}, nil, false)
	add("a record with a float", []HistoryRecord{{Payload: []byte(`{"x-deal-v0":{"deal_id":"deal-0123456789abcdef","record_type":"baseline"},"body":{"n":1.5}}`), CommittedAt: historyStart}}, nil, false)
	add("a report passed over", []HistoryRecord{{Payload: []byte(`{"type":"deal_report","profile":"x-deal-v0"}`), CommittedAt: historyStart}}, nil, false)

	// Not pinned: an amount_minor of the wrong type (a string, a boolean). Go
	// reads it as a present amount of 0 (json.Unmarshal into a *int64 allocates
	// the pointer before it fails), where the profile's schema refuses it; the
	// Python reader refuses it as unpriced.
	commit := map[string]any{"action": "commit", "amount_minor": 100, "currency": "USD"}
	for _, role := range []string{"", "seller", "buyer", "other"} {
		add("classic role "+role, classicDeal(t, role, commit).records, nil, false)
	}
	for _, c := range []string{"USD", "JPY", "KWD", "CLF", "XAU", "ZZZ", "usd", "", "XCG", "ANG"} {
		add("classic currency "+c, classicDeal(t, "", map[string]any{"action": "commit", "amount_minor": 1500, "currency": c}).records, nil, false)
	}
	checkCases := map[string]map[string]any{
		"no amount": {"action": "commit", "currency": "USD"}, "no currency": {"action": "commit", "amount_minor": 100},
		"a negative amount": {"action": "commit", "amount_minor": -1, "currency": "USD"}, "a zero amount": {"action": "commit", "amount_minor": 0, "currency": "USD"},
		"a null amount":      {"action": "commit", "amount_minor": nil, "currency": "USD"},
		"a huge amount":      {"action": "commit", "amount_minor": int64(9007199254740991), "currency": "USD"},
		"a numeric currency": {"action": "commit", "amount_minor": 100, "currency": 5},
	}
	for _, name := range slices.Sorted(maps.Keys(checkCases)) {
		add("classic check: "+name, classicDeal(t, "", checkCases[name]).records, nil, false)
	}
	pay := classicDeal(t, "", map[string]any{"action": "pay", "amount_minor": 100, "currency": "USD"})
	add("classic pay", pay.records, nil, false)
	payCheck := map[string]any{"action": "pay", "amount_minor": 100, "currency": "USD"}
	classCases := map[string]struct {
		check map[string]any
		top   map[string]map[string]any
	}{
		"person_agent":         {payCheck, map[string]map[string]any{"check": {"counterparty_class": "person_agent"}}},
		"business_agent":       {payCheck, map[string]map[string]any{"check": {"counterparty_class": "business_agent"}}},
		"shop_preauth":         {payCheck, map[string]map[string]any{"check": {"counterparty_class": "shop_preauth"}}},
		"an unknown value":     {payCheck, map[string]map[string]any{"check": {"counterparty_class": "shop"}}},
		"a number":             {payCheck, map[string]map[string]any{"check": {"counterparty_class": 1}}},
		"a null":               {payCheck, map[string]map[string]any{"check": {"counterparty_class": nil}}},
		"an empty string":      {payCheck, map[string]map[string]any{"check": {"counterparty_class": ""}}},
		"on a commit check":    {commit, map[string]map[string]any{"check": {"counterparty_class": "shop_preauth"}}},
		"on an action record":  {payCheck, map[string]map[string]any{"action": {"counterparty_class": "shop_preauth"}}},
		"on two records":       {payCheck, map[string]map[string]any{"check": {"counterparty_class": "shop_preauth"}, "action": {"counterparty_class": "shop_preauth"}}},
		"two records, one bad": {payCheck, map[string]map[string]any{"check": {"counterparty_class": "shop_preauth"}, "action": {"counterparty_class": 1}}},
	}
	for _, name := range slices.Sorted(maps.Keys(classCases)) {
		tc := classCases[name]
		add("counterparty_class: "+name, rebuildClassic(t, tc.check, tc.top).records, nil, false)
	}
	{
		c := classicDeal(t, "", commit)
		c.records[len(c.records)-1].CommittedAt = time.Date(2026, 10, 6, 12, 0, 30, 123456000, time.UTC)
		add("agreed within a minute", c.records, nil, false)
		c.records[len(c.records)-1].CommittedAt = time.Date(2026, 10, 6, 12, 0, 30, 0, time.FixedZone("x", 3600))
		add("agreed in another zone", c.records, nil, false)
	}
	happenedCases := map[string]struct {
		outcomeType string
		closes      []string
	}{
		"received then open": {TypeOutcome, []string{"received", ""}}, "received then not received": {TypeOutcome, []string{"received", "not_received"}},
		"typed received": {TypeActionOutcome, []string{"received"}}, "only open": {TypeOutcome, []string{""}},
		"not received then received": {TypeOutcome, []string{"not_received", "received"}},
	}
	for _, name := range slices.Sorted(maps.Keys(happenedCases)) {
		tc := happenedCases[name]
		c := classicDeal(t, "", commit)
		for i, status := range tc.closes {
			if status == "" {
				c.add(fmt.Sprintf("close%d", i), TypeClose, nil, map[string]any{"outcome": "open"})
				continue
			}
			o := fmt.Sprintf("outcome%d", i)
			c.add(o, tc.outcomeType, nil, map[string]any{"status": status, "outcome": "completed"})
			c.add(fmt.Sprintf("close%d", i), TypeClose, map[string]string{"outcome": o}, map[string]any{"outcome": "completed"})
		}
		add("happened: "+name, c.records, nil, false)
	}
	{
		c := classicDeal(t, "seller", map[string]any{"action": "commit", "amount_minor": 1500, "currency": "JPY"})
		c.add("outcome", TypeOutcome, nil, map[string]any{"status": "received", "outcome": "completed"})
		c.records = append(c.records, HistoryRecord{Payload: []byte(`{"type":"deal_report","profile":"x-deal-v0","deal_id":"deal-0123456789abcdef"}`), CommittedAt: historyStart.Add(10 * time.Hour)})
		c.add("close", TypeClose, map[string]string{"outcome": "outcome"}, map[string]any{"outcome": "completed"})
		add("a seller's received close", c.records, []HistoryResult{result(true, "met")}, false)
	}
	return out
}

func parityCards(t *testing.T, histories []parityHistory) []parityCard {
	var entries []standardhistory.Deal
	for _, h := range histories {
		if h.Entry != nil {
			entries = append(entries, *h.Entry)
		}
	}
	require.NotEmpty(t, entries)
	var out []parityCard
	for _, tc := range []struct {
		name       string
		anchorTime string
		window     standardhistory.Window
		deals      []standardhistory.Deal
	}{
		{"every entry, all time", "2026-10-31T00:00:00Z", standardhistory.Window{All: true}, entries},
		{"every entry, three months", "2026-10-31T00:00:00Z", standardhistory.Window{Months: 3}, entries},
		{"every entry, an early anchor", "2026-10-06T12:00:30Z", standardhistory.Window{All: true}, entries},
		{"one entry", "2026-10-31T00:00:00Z", standardhistory.Window{Months: 12}, entries[:1]},
		{"no entry", "2026-10-31T00:00:00Z", standardhistory.Window{All: true}, nil},
	} {
		c := parityCard{
			Name:   tc.name,
			AsOf:   standardhistory.AsOf{Anchor: strings.Repeat("ab", 32), AnchorTime: tc.anchorTime, FirstCheckpointTime: "2026-09-30T08:00:00Z"},
			Policy: standardhistory.Policy{Digest: strings.Repeat("cd", 32), Window: tc.window},
		}
		for _, d := range tc.deals {
			raw, err := json.Marshal(d)
			require.NoError(t, err)
			c.Deals = append(c.Deals, string(raw))
		}
		card, err := standardhistory.Derive("person", "holder", c.AsOf, c.Policy, tc.deals)
		require.NoError(t, err)
		doc, err := decode(card)
		require.NoError(t, err)
		jcs, err := canonical.JCS(doc)
		require.NoError(t, err)
		c.Card = string(jcs)
		out = append(out, c)
	}
	return out
}

func parityVersions() map[string]string {
	v := map[string]string{"go_unicode": unicode.Version}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			switch d.Path {
			case "golang.org/x/net":
				v["x_net"] = d.Version
			case "golang.org/x/text":
				v["x_text"] = d.Version
			}
		}
	}
	return v
}

func buildParityFile(t *testing.T) parityFile {
	secret, err := hex.DecodeString("fe97e8dba94984ff1fbdc35647d9cad779866e3abb4ff1cb690352c9cdc6b4fd")
	require.NoError(t, err)
	const dealID = "deal-3f9c2a7be4d15068"
	f := parityFile{
		Versions:       parityVersions(),
		Note:           "Generated by TestParityVectors (EB_WRITE_DEAL_PARITY_VECTORS=1) from Go's dealprofile; the Python package passes the same file.",
		StoreSecretHex: hex.EncodeToString(secret), DealID: dealID, MinorUnits: map[string]*int{},
	}
	key := DealKey(secret, dealID)
	for _, c := range normalizeCases {
		v := parityNormalize{Kind: c.kind, Raw: c.raw}
		if n, err := Normalize(c.kind, c.raw); err == nil {
			fp, err := Fingerprint(key, c.kind, c.raw)
			require.NoError(t, err)
			v.Normalized, v.FP = &n, &fp
		}
		f.Normalize = append(f.Normalize, v)
	}
	f.Check = parityChecks(t)
	f.History = parityHistories(t)
	f.Cards = parityCards(t, f.History)
	for _, code := range []string{"USD", "EUR", "JPY", "KRW", "BHD", "CLF", "XCG", "ANG", "XAU", "BTC", "", "usd", "US", "USDX", "AED AFN", "UYW", "ZWG"} {
		if s, ok := MinorUnits(code); ok {
			f.MinorUnits[code] = &s
		} else {
			f.MinorUnits[code] = nil
		}
	}
	return f
}

func TestParityVectors(t *testing.T) {
	got, err := json.MarshalIndent(buildParityFile(t), "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.FromSlash(parityVectorsPath)
	if os.Getenv("EB_WRITE_DEAL_PARITY_VECTORS") == "1" {
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got), "testdata/parity-vectors.json is Go's answers (regenerate with EB_WRITE_DEAL_PARITY_VECTORS=1)")
}
