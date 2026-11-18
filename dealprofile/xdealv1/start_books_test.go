// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bookDeal builds the two deal books of a deal like Appendix A's, the
// buyer offering and the seller accepting, with the witnesses and layer
// given; each record is checked as it is added.
type bookDeal struct {
	t      *testing.T
	offer  Record
	seller []BookRecord
	buyer  []BookRecord
}

func newBookDeal(t *testing.T, witnesses []Witness, layer bool, buyerRows string) *bookDeal {
	t.Helper()
	ob := fxOffer()
	ob.Witnesses = witnesses
	if !layer {
		ob.To = NullOf[string]()
	}
	offer := Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: ob}
	require.NoError(t, Check(offer))
	od, err := Digest(offer)
	require.NoError(t, err)
	d := &bookDeal{t: t, offer: offer}
	agreement := func(side, role, key string) *AgreeBody {
		a := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), nil, side, role, key)
		a.Witnesses = witnesses
		if !layer {
			no := false
			a.CounterpartyLayer, a.CounterpartyKey = &no, NullOf[string]()
		}
		return a
	}
	d.add(&d.seller, TypeAgree, "2026-11-01T09:45:00Z", "", agreement(SideAcceptor, RoleSeller, fxBuyerKey))
	if layer {
		ba := agreement(SideOfferer, RoleBuyer, fxSellerKey)
		if buyerRows != "" {
			ba.RowsDigest = buyerRows
		}
		d.add(&d.buyer, TypeAgree, "2026-11-01T10:00:00Z", d.seller[0].RecordID, ba)
	}
	return d
}

// add seals the next record of book, with seen ("" for null), and returns
// its index.
func (d *bookDeal) add(book *[]BookRecord, t, at, seen string, body Body) int {
	d.t.Helper()
	seq := int64(len(*book) + 1)
	b := journalBlock(t, at, true)
	b.Seq, b.Seen = seq, NullOf[string]()
	if seen != "" {
		b.Seen = Some(seen)
	}
	if seq > 1 {
		prev, first := RefTo((*book)[seq-2].digest(d.t)), RefTo((*book)[0].digest(d.t))
		b.Prev, b.AgreementRef = &prev, &first
	}
	r := Record{Block: b, Body: body}
	require.NoError(d.t, Check(r))
	dg, err := Digest(r)
	require.NoError(d.t, err)
	*book = append(*book, BookRecord{Record: r, RecordID: recordID(dg)})
	return len(*book) - 1
}

func (br BookRecord) digest(t *testing.T) string {
	d, err := Digest(br.Record)
	require.NoError(t, err)
	return d
}

func namesCancellation() *StatementBody {
	c := RefTo(label("the offer's cancellation"))
	return &StatementBody{Statements: statements(), Names: &StatementNames{Cancellation: &c}}
}

func receipt() *StatementBody {
	return &StatementBody{Statements: statements(item("delivered", ValueMet))}
}

// The start vectors at the book level (design §5.4): each case's two books,
// the StartInput StartFromBooks derives from them, and Start's result.
// Written to testdata/deal-v1/start/books.json (EB_WRITE_DEAL_V1_VECTORS=1).
func TestStartFromBooksVectors(t *testing.T) {
	var cases []booksCase
	add := func(name, why string, b StartBooks, want StartResult) {
		t.Helper()
		in, got, err := StartFromBooks(b)
		require.NoError(t, err, name)
		require.Equal(t, want, got, name)
		// Encoded now: a later case may change a record a shared book holds.
		raw, err := json.Marshal(b)
		require.NoError(t, err)
		cases = append(cases, booksCase{name, why, raw, in, want})
	}
	ws := []Witness{fxWitness}
	inTime, late := "2026-11-01T10:05:00Z", "2026-11-03T10:05:00Z"
	timed := func(d *bookDeal, at string) { d.buyer[0].Times = map[string]string{fxWitness.KeyID: at} }
	seller := func(d *bookDeal, at string) StartBooks {
		return StartBooks{At: at, Offer: d.offer, Own: d.seller, Counterpart: d.buyer}
	}
	notStarted := StartResult{State: StateNotStarted}
	startedLocally := StartResult{State: StateStarted, Local: true}

	d := newBookDeal(t, ws, true, "")
	timed(d, inTime)
	add("timed-in-time", "both agree, the offerer's agreement timed before agree_by by a witness the agreements name: started, not local",
		seller(d, stAfter), started(false, ByOffererInTime))

	d = newBookDeal(t, ws, true, "")
	add("untimed-before", "both agree, the offerer's agreement untimed, T before agree_by: started, local", seller(d, stBefore), startedLocally)
	add("untimed-after-not-named", "the same, T after agree_by, never named: not started", seller(d, stAfter), notStarted)
	add("offerer-side-untimed", "the offerer computes the same from its own book and the acceptor's agreement",
		StartBooks{At: stBefore, Offer: d.offer, Own: d.buyer, Counterpart: d.seller}, startedLocally)
	acceptorOnly := StartBooks{At: stBefore, Offer: d.offer, Own: d.seller}
	add("acceptor-only-before", "the acceptor holds only its own agreement, T before agree_by: awaiting agreement",
		acceptorOnly, StartResult{State: StateAwaitingAgreement})
	acceptorOnly.At = stAfter
	add("acceptor-only-after", "the same after agree_by: not started", acceptorOnly, notStarted)

	d = newBookDeal(t, ws, true, "")
	timed(d, late)
	add("late-not-named", "the offerer's agreement timed after agree_by, not named: not started", seller(d, stAfter), notStarted)
	d.add(&d.seller, TypeStatement, "2026-11-03T12:00:00Z", d.buyer[0].RecordID, receipt())
	add("late-named", "the same, and the acceptor's book names the offerer's agreement: started", seller(d, stAfter), started(false, ByAcceptorNamed))

	d = newBookDeal(t, ws, true, "")
	timed(d, late)
	pay := &SettleBody{Leg: LegPayer, Statements: statements(item("paid_as_agreed", ValueMet)), Check: ref('1')}
	d.add(&d.buyer, TypeSettle, "2026-11-03T11:00:00Z", d.seller[0].RecordID, pay)
	d.add(&d.seller, TypeStatement, "2026-11-03T12:00:00Z", d.buyer[1].RecordID, receipt())
	add("late-named-later-record", "the acceptor's book names a later record of the offerer's book: that names the agreement too; started",
		seller(d, stAfter), started(false, ByAcceptorNamed))

	d = newBookDeal(t, ws, true, "")
	timed(d, inTime)
	d.add(&d.seller, TypeStatement, "2026-11-01T11:00:00Z", "", namesCancellation())
	d.add(&d.seller, TypeStatement, "2026-11-01T12:00:00Z", d.buyer[0].RecordID, receipt())
	add("cancellation-named-first", "the acceptor named the offer's cancellation, then the offerer's agreement in time: not started",
		seller(d, stAfter), notStarted)

	d = newBookDeal(t, ws, true, "")
	d.add(&d.seller, TypeStatement, "2026-11-01T11:00:00Z", "", namesCancellation())
	cancelled := StartBooks{At: stBefore, Offer: d.offer, Own: d.seller}
	add("cancellation-no-counterpart-before", "the acceptor agreed, then named the offerer's cancellation, and holds none of the offerer's book: not started, even before agree_by",
		cancelled, notStarted)
	cancelled.At = stAfter
	add("cancellation-no-counterpart-after", "the same after agree_by: not started", cancelled, notStarted)

	d = newBookDeal(t, ws, true, "")
	d.add(&d.seller, TypeStatement, "2026-11-01T11:00:00Z", d.buyer[0].RecordID, receipt())
	d.add(&d.seller, TypeStatement, "2026-11-01T12:00:00Z", d.buyer[0].RecordID, namesCancellation())
	add("named-then-cancelled", "the acceptor named the offerer's agreement, then a cancellation: started", seller(d, stAfter), started(false, ByAcceptorNamed))

	d = newBookDeal(t, ws, true, label("other rows"))
	add("other-rows-before", "an offerer's agreement citing another rows digest is set aside: awaiting before agree_by",
		seller(d, stBefore), StartResult{State: StateAwaitingAgreement})
	add("other-rows-after", "the same after agree_by: not started", seller(d, stAfter), notStarted)

	d = newBookDeal(t, ws, true, "")
	d.buyer[0].Times = map[string]string{label("a witness the agreements do not name"): inTime}
	add("unnamed-witness-before", "the offerer's agreement timed only by a witness the agreements do not name: as untimed; local before agree_by",
		seller(d, stBefore), startedLocally)
	add("unnamed-witness-after", "the same after agree_by, not named: not started", seller(d, stAfter), notStarted)

	d = newBookDeal(t, []Witness{}, true, "")
	timed(d, inTime)
	add("empty-witnesses-local", "an empty witness list gives no time: started only locally before agree_by", seller(d, stBefore), startedLocally)
	add("empty-witnesses-after", "the same after agree_by, not named: not started", seller(d, stAfter), notStarted)
	d.add(&d.seller, TypeStatement, "2026-11-01T12:00:00Z", d.buyer[0].RecordID, receipt())
	add("empty-witnesses-named", "with an empty witness list a deal starts for good only by the acceptor's naming", seller(d, stAfter), started(false, ByAcceptorNamed))

	d = newBookDeal(t, ws, false, "")
	noLayer := StartBooks{At: stAfter, Offer: d.offer, Own: d.seller}
	add("no-layer-before-act", "a counterparty with no layer, before the holder's first act: not started", noLayer, notStarted)
	i := d.add(&d.seller, TypePerform, "2026-11-04T10:00:00Z", "",
		&PerformBody{Act: "deliver", Observed: "carrier_scan", Statements: statements(item("delivered", ValueMet))})
	noLayer.Own = d.seller
	add("no-layer-untimed-act", "no layer, after an untimed act: started, local", noLayer, started(true, ByFirstAct))
	d.seller[i].Times = map[string]string{fxWitness.KeyID: "2026-11-04T10:30:00Z"}
	add("no-layer-timed-act", "no layer, after an act timed by a named witness: started, not local", noLayer, started(false, ByFirstAct))

	for _, c := range cases {
		replayBooksCase(t, c)
	}
	got, err := json.MarshalIndent(map[string]any{
		"description": "start from books (evidencebook#461, docs/design/461-deal-profile.md §5.2 and §5.4): StartFromBooks(books) must derive input from the holder's deal book (own), the counterparty's (counterpart) and the offer, and Start(input) must give expect. A record's time is the earliest a witness the agreements name gave it (times, by witness key id). Record ids stand in for the Capsule ids of sealed book records. Written by dealprofile/xdealv1's TestStartFromBooksVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(startDir, "books.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(startDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "start books.json differs from this build's; regenerate only for an intended change")
}

// booksCase is one book-level start vector, its books as encoded.
type booksCase struct {
	Name   string          `json:"name"`
	Why    string          `json:"why"`
	Books  json.RawMessage `json:"books"`
	Input  StartInput      `json:"input"`
	Expect StartResult     `json:"expect"`
}

// replayBooksCase reads a case's books back as another implementation
// would, and checks they give its input and result.
func replayBooksCase(t *testing.T, c booksCase) {
	t.Helper()
	var b StartBooks
	require.NoError(t, json.Unmarshal(c.Books, &b), c.Name)
	in, got, err := StartFromBooks(b)
	require.NoError(t, err, c.Name)
	assert.Equal(t, c.Input, in, c.Name)
	assert.Equal(t, c.Expect, got, c.Name)
}

// Every case in books.json, read from the file alone, gives its input and
// result.
func TestStartFromBooksVectorsReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(startDir, "books.json"))
	require.NoError(t, err)
	var v struct {
		Cases []booksCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.Cases)
	for _, c := range v.Cases {
		replayBooksCase(t, c)
	}
}

// A time from a witness the agreements do not name is not read, even when
// malformed; the offerer's agreement's time comes from the witnesses it
// names.
func TestStartFromBooksWitnessTimes(t *testing.T) {
	d := newBookDeal(t, []Witness{fxWitness}, true, "")
	d.buyer[0].Times = map[string]string{label("unnamed"): "yesterday", fxWitness.KeyID: "2026-11-01T10:05:00Z"}
	in, _, err := StartFromBooks(StartBooks{At: stAfter, Offer: d.offer, Own: d.seller, Counterpart: d.buyer})
	require.NoError(t, err)
	require.NotNil(t, in.OffererAgreementTime)
	assert.Equal(t, "2026-11-01T10:05:00Z", *in.OffererAgreementTime)

	// The holder's agreement names another witness than the offerer's: the
	// offerer's list decides.
	other := Witness{KeyID: label("the acceptor's other witness")}
	d.seller[0].Record.Body.(*AgreeBody).Witnesses = []Witness{other}
	in, _, err = StartFromBooks(StartBooks{At: stAfter, Offer: d.offer, Own: d.seller, Counterpart: d.buyer})
	require.NoError(t, err)
	require.NotNil(t, in.OffererAgreementTime)
	assert.Equal(t, "2026-11-01T10:05:00Z", *in.OffererAgreementTime)
}

func TestStartFromBooksRefuses(t *testing.T) {
	ws := []Witness{fxWitness}
	for name, f := range map[string]func(d *bookDeal) StartBooks{
		"empty own book": func(d *bookDeal) StartBooks { return StartBooks{At: stBefore, Offer: d.offer} },
		"own book not opened by an agreement": func(d *bookDeal) StartBooks {
			own := append([]BookRecord(nil), d.seller...)
			own[0].Record.Body = receipt()
			return StartBooks{At: stBefore, Offer: d.offer, Own: own}
		},
		"agreement citing another offer": func(d *bookDeal) StartBooks {
			o := d.offer
			ob := *o.Body.(*ProposeBody)
			ob.AgreeBy = "2026-11-03T23:59:59Z"
			o.Body = &ob
			return StartBooks{At: stBefore, Offer: o, Own: d.seller, Counterpart: d.buyer}
		},
		"offer not a propose": func(d *bookDeal) StartBooks {
			return StartBooks{At: stBefore, Offer: d.seller[0].Record, Own: d.seller}
		},
		"book out of order": func(d *bookDeal) StartBooks {
			return StartBooks{At: stBefore, Offer: d.offer, Own: d.seller, Counterpart: []BookRecord{d.buyer[0], d.buyer[0]}}
		},
		"another deal": func(d *bookDeal) StartBooks {
			own := append([]BookRecord(nil), d.seller...)
			own[0].Record.Block.DealID = "deal-0000000000000000"
			return StartBooks{At: stBefore, Offer: d.offer, Own: own}
		},
		"counterpart book not opened by an agreement": func(d *bookDeal) StartBooks {
			cp := append([]BookRecord(nil), d.buyer...)
			cp[0].Record.Body = receipt()
			return StartBooks{At: stBefore, Offer: d.offer, Own: d.seller, Counterpart: cp}
		},
		"malformed time": func(d *bookDeal) StartBooks {
			cp := append([]BookRecord(nil), d.buyer...)
			cp[0].Times = map[string]string{fxWitness.KeyID: "yesterday"}
			return StartBooks{At: stBefore, Offer: d.offer, Own: d.seller, Counterpart: cp}
		},
	} {
		_, _, err := StartFromBooks(f(newBookDeal(t, ws, true, "")))
		assert.Error(t, err, name)
		t.Logf("%s: %v", name, err)
	}
}
