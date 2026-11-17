// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vectorDir = "../../testdata/deal-v1/records"

func stageOf(err error) string {
	var se *StageError
	if errors.As(err, &se) {
		return se.Stage
	}
	return ""
}

// edit marshals r, applies f to its generic JSON, and returns the bytes.
func edit(t *testing.T, r Record, f func(blk, body map[string]any)) []byte {
	t.Helper()
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	f(doc[Profile].(map[string]any), doc["body"].(map[string]any))
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

// deal-id-presence (design §3.1, §11.2; review round 3): each record type
// with deal_id as §3.1 places it passes, and with it flipped (dropped where
// it is required, added where it is absent) is refused at the record stage.
// A journey record, ask or answer in the journal is checked with its
// SealContext (CheckSealed): whether its offer exists, whether it is of an
// admission. The cases are written to
// testdata/deal-v1/records/deal-id-presence.json (EB_WRITE_DEAL_V1_VECTORS=1),
// which the Rust and Python ports replay.
func TestDealIDPresence(t *testing.T) {
	type vcase struct {
		Name    string          `json:"name"`
		Record  json.RawMessage `json:"record"`
		Context *SealContext    `json:"context,omitempty"`
		Valid   bool            `json:"valid"`
		Stage   string          `json:"stage,omitempty"`
	}
	var cases []vcase
	records := validRecords(t)
	names := make([]string, 0, len(records))
	for n := range records {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r := records[name]
		var ctx *SealContext
		if t0 := r.Block.RecordType; PlacementOf(r) == PlacementJournal && (slices.Contains(journeyTypes, t0) || slices.Contains(askTypes, t0)) {
			has := r.Block.DealID != ""
			ctx = &SealContext{OfferExists: has, Admission: !has}
		}
		var sc SealContext
		if ctx != nil {
			sc = *ctx
		}
		raw, err := json.Marshal(r)
		require.NoError(t, err)
		require.NoError(t, CheckSealed(raw, sc), name)
		cases = append(cases, vcase{Name: name, Record: raw, Context: ctx, Valid: true})
		flipped := edit(t, r, func(b, _ map[string]any) {
			if _, ok := b["deal_id"]; ok {
				delete(b, "deal_id")
			} else {
				b["deal_id"] = dealID
			}
		})
		err = CheckSealed(flipped, sc)
		require.Error(t, err, name)
		assert.Equal(t, StageRecord, stageOf(err), "%s: %v", name, err)
		cases = append(cases, vcase{Name: name + "/deal_id-flipped", Record: flipped, Context: ctx, Valid: false, Stage: StageRecord})
		if ctx != nil {
			// Check alone, which cannot see the context, takes either.
			assert.NoError(t, Check(flipped), name)
		}
	}
	got, err := json.MarshalIndent(map[string]any{
		"description": "deal-id-presence (evidencebook#461, docs/design/461-deal-profile.md §3.1, §11.2): each record passes or is refused at the stage given, checked with its context when it has one (CheckSealed: offer_exists, admission). deal_id is required on every book record, on propose and every agree, on check and verdict and on an approval that answers a verdict; absent on intent, admit, sale, thread, thread_opened, sale_unpost, counterparty_profile, disclosure and a confirm_limits approval; on a journal journey record exactly when its offer exists, and on a journal ask or answer exactly when it is not of an admission. Written by dealprofile/xdealv1's TestDealIDPresence (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(vectorDir, "deal-id-presence.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(vectorDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "deal-id-presence.json differs from this build's; regenerate only for an intended change")
}

// Version gives x-deal-v0 for every positive v0 fixture, typed ones
// included (design §11.2).
func TestVersionOfEveryV0Fixture(t *testing.T) {
	files, err := filepath.Glob("../x-deal-v0/fixtures/positive*/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var fx struct {
			Record json.RawMessage `json:"record"`
		}
		require.NoError(t, json.Unmarshal(raw, &fx), f)
		require.NotNil(t, fx.Record, f)
		v, err := dealprofile.Version(fx.Record)
		require.NoError(t, err, f)
		assert.Equal(t, dealprofile.Profile, v, f)
	}
}

func TestPlacementRules(t *testing.T) {
	records := validRecords(t)
	for name, c := range map[string]struct {
		base string
		f    func(b, body map[string]any)
	}{
		"a journal record with a seq":          {"propose", func(b, _ map[string]any) { b["seq"] = 1 }},
		"a journal record with seen":           {"agree/decline", func(b, _ map[string]any) { b["seen"] = nil }},
		"a journal record with prev":           {"check", func(b, _ map[string]any) { b["prev"] = *ref('1') }},
		"a deal-book record with no seq":       {"perform", func(b, _ map[string]any) { delete(b, "seq") }},
		"a deal-book record with no seen":      {"perform", func(b, _ map[string]any) { delete(b, "seen") }},
		"prev at seq 1":                        {"agree/agreement", func(b, _ map[string]any) { b["prev"] = *ref('1') }},
		"no prev past seq 1":                   {"perform", func(b, _ map[string]any) { delete(b, "prev") }},
		"no agreement_ref past seq 1":          {"perform", func(b, _ map[string]any) { delete(b, "agreement_ref") }},
		"an agreement past seq 1":              {"agree/agreement", func(b, _ map[string]any) { b["seq"], b["prev"], b["agreement_ref"] = 2, *ref('1'), *ref('2') }},
		"a deal book opened by another record": {"perform", func(b, _ map[string]any) { b["seq"] = 1; delete(b, "prev"); delete(b, "agreement_ref") }},
		"a ruling with seen":                   {"ruling", func(b, _ map[string]any) { b["seen"] = nil }},
		"an acceptor's agreement that saw":     {"agree/agreement", func(b, _ map[string]any) { b["seen"] = hx('1') }},
		"no layer and a counterparty key":      {"agree/agreement", func(_, body map[string]any) { body["counterparty_layer"] = false }},
		"a row given twice": {"propose", func(_, body map[string]any) {
			tr := body["terms_rows"].(map[string]any)
			rows := tr["rows"].([]any)
			tr["rows"] = append(rows, rows[0])
		}},
		"a date on a record not named":       {"propose", func(_, body map[string]any) { body["terms_rows"].(map[string]any)["named"] = map[string]any{} }},
		"a check body v0 refuses":            {"check", func(_, body map[string]any) { body["action"] = "fly" }},
		"a decline with an agreement member": {"agree/decline", func(_, body map[string]any) { body["rows_digest"] = hx('1') }},
		"a cancellation with a reason":       {"agree/cancellation", func(_, body map[string]any) { body["reason"] = "other" }},
		"a decline with an unknown reason":   {"agree/decline", func(_, body map[string]any) { body["reason"] = "too_slow" }},
		"a payer leg with no check":          {"settle/payer", func(_, body map[string]any) { delete(body, "check") }},
		"an empty statement naming nothing":  {"statement", func(_, body map[string]any) { body["statements"] = []any{} }},
		"a ruling against a third party":     {"ruling", func(_, body map[string]any) { body["against"] = "referee" }},
		"a withdrawal with dispute members":  {"dispute/withdrawal", func(_, body map[string]any) { body["adjudicator"] = "k" }},
		"another profile in the block":       {"propose", func(b, _ map[string]any) { b["profile"] = "x-deal-v0" }},
		"an unknown block member":            {"propose", func(b, _ map[string]any) { b["baseline_ref"] = *ref('1') }},
		"a float":                            {"admit", func(_, body map[string]any) { body["tier"] = 1.5 }},
		"another canonicalization":           {"admit", func(b, _ map[string]any) { b["canonicalization"] = "none" }},
		"a layered counterparty with no key": {"agree/agreement", func(_, body map[string]any) { body["counterparty_key"] = nil }},
		"a decline with marks":               {"agree/decline", func(b, _ map[string]any) { b["marks"] = []any{"paid"} }},
		"a cancellation with a fingerprint": {"agree/cancellation", func(b, _ map[string]any) {
			b["counterparty"] = map[string]any{"fp_alg": FPAlg, "ids": map[string]any{"payee": hx('1')}}
		}},
		"a decline with refs": {"agree/decline", func(b, _ map[string]any) { b["refs"] = []any{} }},
		"a date that does not exist": {"propose", func(_, body map[string]any) {
			body["terms_rows"].(map[string]any)["rows"].([]any)[0].(map[string]any)["due"] = map[string]any{"at": "2026-02-30T10:00:00Z"}
		}},
		"a latest date that does not exist": {"propose", func(_, body map[string]any) {
			body["terms_rows"].(map[string]any)["named"].(map[string]any)["paid"].(map[string]any)["latest"] = "2026-11-31T00:00:00Z"
		}},
		"an hour past 23":                  {"propose", func(_, body map[string]any) { body["agree_by"] = "2026-11-02T24:00:00Z" }},
		"a block time that does not exist": {"admit", func(b, _ map[string]any) { b["at"] = "2026-13-01T00:00:00Z" }},
		"year zero":                        {"admit", func(b, _ map[string]any) { b["at"] = "0000-01-01T00:00:00Z" }},
		"a witness named twice": {"propose", func(_, body map[string]any) {
			w := body["witnesses"].([]any)[0]
			body["witnesses"] = []any{w, map[string]any{"key_id": w.(map[string]any)["key_id"], "url": "https://moved.test"}}
		}},
		"a witness named by URL alone": {"propose", func(_, body map[string]any) { body["witnesses"] = []any{"https://witness.test"} }},
		"a witness key id not hex":     {"agree/agreement", func(_, body map[string]any) { body["witnesses"] = []any{map[string]any{"key_id": "witness"}} }},
		"a dispute with no forum":      {"dispute/dispute", func(_, body map[string]any) { delete(body, "forum") }},
		"a ruling with no forum":       {"ruling", func(_, body map[string]any) { delete(body, "forum") }},
		"an unknown forum class":       {"ruling", func(_, body map[string]any) { body["forum"] = "court:x" }},
		"a forum with no id":           {"dispute/dispute", func(_, body map[string]any) { body["forum"] = "network:" }},
		"a withdrawal with a forum":    {"dispute/withdrawal", func(_, body map[string]any) { body["forum"] = ForumAutoRef }},
	} {
		raw := edit(t, records[c.base], c.f)
		err := Check(raw)
		assert.Error(t, err, name)
		assert.NotEmpty(t, stageOf(err), "%s: %v", name, err)
	}
}

func TestCheckAgreement(t *testing.T) {
	offer := record(blk(TypePropose), offerBody())
	acc := acceptorAgreement(offer)
	require.NoError(t, CheckAgreement(acc, offer, ""))
	accID := hx('9')
	offerer := acceptorAgreement(offer)
	ob := offerer.Body.(*AgreeBody)
	ob.Side, ob.Role, ob.CounterpartyKey = SideOfferer, RoleBuyer, Some("seller-key")
	offerer.Block.Seen = Some(accID)
	require.NoError(t, Check(offerer))
	require.NoError(t, CheckAgreement(offerer, offer, accID))

	for name, f := range map[string]func(a *Record, o *Record){
		"another offer":       func(_ *Record, o *Record) { o.Body.(*ProposeBody).PackDigest = hx('0') },
		"another deal":        func(a *Record, _ *Record) { a.Block.DealID = "deal-fedcba9876543210" },
		"another rows digest": func(a *Record, _ *Record) { a.Body.(*AgreeBody).RowsDigest = hx('0') },
		"other witnesses": func(a *Record, _ *Record) {
			a.Body.(*AgreeBody).Witnesses = []Witness{{KeyID: hx('5'), URL: "https://witness.test"}}
		},
		"no adjudicator": func(a *Record, _ *Record) {
			a.Body.(*AgreeBody).Adjudicator, a.Body.(*AgreeBody).Forums = NullOf[Adjudicator](), []string{}
		},
		"the offer's role": func(a *Record, _ *Record) { a.Body.(*AgreeBody).Role = RoleBuyer },
		"not citing an offer": func(_ *Record, o *Record) {
			o.Block.RecordType = TypeCheck
			o.Body = &CheckBody{CheckBody: dealprofile.CheckBody{Action: "pay"}}
		},
	} {
		a, o := acceptorAgreement(offer), record(blk(TypePropose), offerBody())
		f(&a, &o)
		err := CheckAgreement(a, o, "")
		assert.Equal(t, StageAgreement, stageOf(err), "%s: %v", name, err)
	}
	// A witness is its key id: another URL hint, or another order, is the
	// same list.
	hinted := acceptorAgreement(offer)
	hinted.Body.(*AgreeBody).Witnesses = []Witness{{KeyID: hx('4'), URL: "https://moved.test"}}
	assert.NoError(t, CheckAgreement(hinted, offer, ""))
	unhinted := acceptorAgreement(offer)
	unhinted.Body.(*AgreeBody).Witnesses = []Witness{{KeyID: hx('4')}}
	assert.NoError(t, CheckAgreement(unhinted, offer, ""))

	// The offerer's agreement names the acceptor's agreement it holds.
	assert.Equal(t, StageAgreement, stageOf(CheckAgreement(offerer, offer, hx('8'))))
	offerer.Body.(*AgreeBody).Role = RoleSeller
	assert.Equal(t, StageAgreement, stageOf(CheckAgreement(offerer, offer, accID)), "the offerer's role is the offer's")
}

func TestVersion(t *testing.T) {
	v1, err := json.Marshal(validRecords(t)["propose"])
	require.NoError(t, err)
	for name, c := range map[string]struct {
		raw  string
		want string
	}{
		"v1":             {string(v1), Profile},
		"v0 block":       {`{"x-deal-v0": {"profile": "x-deal-v0"}, "body": {}}`, dealprofile.Profile},
		"typed v0":       {`{"type": "task-authority/v0", "body": {}}`, dealprofile.Profile},
		"a later block":  {`{"x-deal-v7": {"profile": "x-deal-v7"}, "body": {}}`, "x-deal-v7"},
		"two blocks":     {`{"x-deal-v0": {"profile": "x-deal-v0"}, "x-deal-v1": {"profile": "x-deal-v1"}}`, ""},
		"block and type": {`{"x-deal-v1": {"profile": "x-deal-v1"}, "type": "action-record/v0"}`, ""},
		"neither":        {`{"body": {}}`, ""},
		"mismatch":       {`{"x-deal-v1": {"profile": "x-deal-v0"}}`, ""},
		"not an object":  {`[1]`, ""},
		"another type":   {`{"type": "deal_report"}`, ""},
	} {
		got, err := dealprofile.Version([]byte(c.raw))
		if c.want == "" {
			assert.ErrorIs(t, err, dealprofile.ErrUnknownVersion, name)
			continue
		}
		require.NoError(t, err, name)
		assert.Equal(t, c.want, got, name)
	}
}

// A v0 record is never read as v1, and a v1 record never as v0.
func TestVersionsDoNotCross(t *testing.T) {
	v1, err := json.Marshal(validRecords(t)["propose"])
	require.NoError(t, err)
	assert.Error(t, dealprofile.Decode(v1, &dealprofile.Record{}))
	_, err = Decode([]byte(`{"x-deal-v0": {"profile": "x-deal-v0", "canonicalization": "jcs", "deal_id": "deal-0123456789abcdef", "record_type": "intent", "seq": 1, "at": "2026-11-01T10:00:00Z"}, "body": {"verbatim_commitment": "` + hx('a') + `"}}`))
	assert.Error(t, err)
}

// Text a decoder would read other than as written is refused before
// anything is read from it.
func TestRawTextRefused(t *testing.T) {
	offer, err := json.Marshal(validRecords(t)["propose"])
	require.NoError(t, err)
	for name, raw := range map[string]string{
		"a lone surrogate":     strings.Replace(string(offer), `"lamp"`, `"\ud800"`, 1),
		"a lone low surrogate": strings.Replace(string(offer), `"lamp"`, `"\udc00x"`, 1),
		"invalid UTF-8":        strings.Replace(string(offer), `"lamp"`, "\"la\xffmp\"", 1),
		"a member given twice": strings.Replace(string(offer), `"item":"lamp"`, `"item":"lamp","item":"desk"`, 1),
		"a block member twice": strings.Replace(string(offer), `"at":"2026-11-01T10:00:00Z"`, `"at":"2026-11-01T10:00:00Z","at":"2026-11-02T10:00:00Z"`, 1),
	} {
		require.NotEqual(t, string(offer), raw, name)
		err := Check([]byte(raw))
		assert.Equal(t, StageCanonicalization, stageOf(err), "%s: %v", name, err)
		_, _, err = Encode([]byte(raw))
		assert.Error(t, err, name)
		_, err = Digest([]byte(raw))
		assert.Error(t, err, name)
		_, err = Decode([]byte(raw))
		assert.Error(t, err, name, "Decode reads the text as Check does")
	}
	pair := strings.Replace(string(offer), `"lamp"`, `"\ud83d\ude00"`, 1)
	assert.NoError(t, Check([]byte(pair)), "a surrogate pair is one character")
}

// A zero offset and an absent one are two texts, and each round-trips.
func TestZeroSecondsRoundTrip(t *testing.T) {
	r := validRecords(t)["propose"]
	for _, seconds := range []*int64{secs(0), nil} {
		r.Body.(*ProposeBody).TermsRows.Rows[1].Due.Seconds = seconds
		raw, err := json.Marshal(r)
		require.NoError(t, err)
		payload, _, err := Encode(raw)
		require.NoError(t, err)
		got, err := Decode(payload)
		require.NoError(t, err)
		assert.Equal(t, seconds, got.Body.(*ProposeBody).TermsRows.Rows[1].Due.Seconds)
	}
}

// CheckAgreement reads a body held as a value as it reads one held by
// pointer.
func TestCheckAgreementTakesValueBodies(t *testing.T) {
	offer := record(blk(TypePropose), *offerBody())
	acc := acceptorAgreement(record(blk(TypePropose), offerBody()))
	acc.Body = *acc.Body.(*AgreeBody)
	require.NoError(t, Check(offer))
	assert.NoError(t, CheckAgreement(acc, offer, ""))
}

// The recourse clause (#404; Sherlock2 on evidencebook-deal#215): an offer
// and its agreement name the forums both sides accept, none with no
// adjudicator; a dispute and a ruling stay within them.
func TestForums(t *testing.T) {
	records := validRecords(t)
	for name, c := range map[string]struct {
		base string
		f    func(b, body map[string]any)
	}{
		"an adjudicator with no forum":   {"propose", func(_, body map[string]any) { body["forums"] = []any{} }},
		"a forum with no adjudicator":    {"propose", func(_, body map[string]any) { body["adjudicator"] = nil }},
		"an agreement with no forums":    {"agree/agreement", func(_, body map[string]any) { delete(body, "forums") }},
		"an offer with no forums member": {"propose", func(_, body map[string]any) { delete(body, "forums") }},
		"a forum named twice":            {"propose", func(_, body map[string]any) { body["forums"] = []any{ForumAutoRef, ForumAutoRef} }},
	} {
		err := Check(edit(t, records[c.base], c.f))
		assert.Error(t, err, name)
	}
	noAdj := records["propose"]
	nb := *noAdj.Body.(*ProposeBody)
	nb.Adjudicator, nb.Forums = NullOf[Adjudicator](), []string{}
	noAdj.Body = &nb
	assert.NoError(t, Check(noAdj), "no adjudicator and no forum: no dispute is possible")

	offer := record(blk(TypePropose), offerBody())
	acc := acceptorAgreement(offer)
	require.NoError(t, CheckAgreement(acc, offer, ""))
	twoOffer := record(blk(TypePropose), offerBody())
	twoOffer.Body.(*ProposeBody).Forums = []string{ForumAutoRef, "external:court"}
	twoAcc := acceptorAgreement(twoOffer)
	twoAcc.Body.(*AgreeBody).Forums = []string{"external:court", ForumAutoRef}
	assert.NoError(t, CheckAgreement(twoAcc, twoOffer, ""), "forums compare as a set")
	noAdjOffer := record(blk(TypePropose), offerBody())
	nob := noAdjOffer.Body.(*ProposeBody)
	nob.Adjudicator, nob.Forums = NullOf[Adjudicator](), []string{}
	noAdjAcc := acceptorAgreement(noAdjOffer)
	naab := noAdjAcc.Body.(*AgreeBody)
	naab.Adjudicator, naab.Forums = NullOf[Adjudicator](), []string{}
	assert.NoError(t, CheckAgreement(noAdjAcc, noAdjOffer, ""), "no adjudicator and no forum on both sides")
	other := acceptorAgreement(offer)
	other.Body.(*AgreeBody).Forums = []string{"external:court"}
	assert.Equal(t, StageAgreement, stageOf(CheckAgreement(other, offer, "")), "the agreement's forums are the offer's")

	dispute := records["dispute/dispute"]
	ad, err := Digest(acc)
	require.NoError(t, err)
	dispute.Block.AgreementRef = &Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: ad}
	assert.NoError(t, CheckDispute(dispute, acc))
	valueDispute, valueAgreement := dispute, acc
	valueDispute.Body, valueAgreement.Body = *dispute.Body.(*DisputeBody), *acc.Body.(*AgreeBody)
	assert.NoError(t, CheckDispute(valueDispute, valueAgreement), "value bodies read as pointers")
	otherAgreement := acceptorAgreement(offer)
	otherAgreement.Body.(*AgreeBody).ShelfLogID = "another-shelf"
	assert.Equal(t, StageAgreement, stageOf(CheckDispute(dispute, otherAgreement)), "a dispute is held to the agreement it cites")
	assert.Equal(t, StageAgreement, stageOf(CheckDispute(dispute, Record{Block: acc.Block, Body: (*AgreeBody)(nil)})), "a nil body is refused, not a panic")
	ruling := records["ruling"]
	assert.NoError(t, CheckRuling(ruling, acc))
	valueRuling := ruling
	valueRuling.Body = *ruling.Body.(*RulingBody)
	assert.NoError(t, CheckRuling(valueRuling, valueAgreement))
	outside := dispute
	ob := *outside.Body.(*DisputeBody)
	ob.Forum = "rail:card"
	outside.Body = &ob
	assert.Equal(t, StageAgreement, stageOf(CheckDispute(outside, acc)), "no forum shopping at dispute time")
	elsewhere := dispute
	eb := *elsewhere.Body.(*DisputeBody)
	eb.Adjudicator = "another-key"
	elsewhere.Body = &eb
	assert.Equal(t, StageAgreement, stageOf(CheckDispute(elsewhere, acc)))
	rOut := ruling
	rb := *rOut.Body.(*RulingBody)
	rb.Forum = "external:court"
	rOut.Body = &rb
	assert.Equal(t, StageAgreement, stageOf(CheckRuling(rOut, acc)))
	none := acceptorAgreement(offer)
	nab := none.Body.(*AgreeBody)
	nab.Adjudicator, nab.Forums = NullOf[Adjudicator](), []string{}
	nd, err := Digest(none)
	require.NoError(t, err)
	toNone := dispute
	toNone.Block.AgreementRef = &Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: nd}
	err = CheckDispute(toNone, none)
	assert.Equal(t, StageAgreement, stageOf(err))
	assert.ErrorContains(t, err, "names no adjudicator", "no adjudicator, no dispute")
}
