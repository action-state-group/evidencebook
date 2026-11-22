package deal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// disputeOffer is the test offer with an adjudicator and the given forums.
func disputeOffer(key string, forums ...string) string {
	quoted := make([]string, len(forums))
	for i, f := range forums {
		quoted[i] = `"` + f + `"`
	}
	withAdj := strings.Replace(offerTo(key), `"adjudicator": null,
  "forums": [],`, `"adjudicator": {"key_id": "adjudicator-key", "rules_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "rules_url": "https://rules.test/r"},
  "forums": [`+strings.Join(quoted, ", ")+`],`, 1)
	return withAdj
}

// agreedBoth runs the handshake of newHandshakeV1 on the offer, and has the
// buyer (the offerer) agree and the seller receive it: both books are open
// and each side holds the other's agreement.
func agreedBoth(t *testing.T, offer func(key string) string) *handshakeV1 {
	t.Helper()
	h := &handshakeV1{}
	h.seller = newV1Side(t)
	h.buyer = newV1Side(t)
	h.offerFile = filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offer(h.seller.party.signer)), "--out", h.offerFile)
	h.dealID, h.offer = res["deal_id"].(string), res["offer"].(string)
	h.seller.act(t)
	dealRun(t, "receive", h.offerFile)
	h.sellerAgree = filepath.Join(t.TempDir(), "agreement.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", h.sellerAgree)
	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	h.buyerAgr = filepath.Join(t.TempDir(), "buyer-agreement.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", h.buyerAgr)
	h.seller.act(t)
	dealRun(t, "receive", h.buyerAgr)
	return h
}

func oneForum(key string) string { return disputeOffer(key, "auto:twin") }

func TestADisputeIsSealedAndTheOtherSideCountsIt(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "dispute.json")
	out := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", file)
	assert.Equal(t, xdealv1.KindDispute, out["kind"])
	assert.Equal(t, "auto:twin", out["forum"], "the only forum the agreement accepts is the default")

	read, err := xdealv1.VerifyBook(readPush(t, file).Record)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), read.Seq, "the agreement is seq 1")
	body, ok := disputeOf(read.Record)
	require.True(t, ok)
	assert.Equal(t, "paid_as_agreed", body.Row)
	assert.Equal(t, "adjudicator-key", body.Adjudicator)
	assert.Equal(t, "auto:twin", body.Forum)
	assert.Empty(t, body.Statements, "no statement on the row to cite")
	assert.Equal(t, out["dispute"], recDigest(t, read.Record))

	// The other side receives and counts it, and a second receive adds nothing.
	h.seller.act(t)
	got := dealRun(t, "receive", file)
	assert.Equal(t, "kept, and counted", got["record"], "%v", got)
	assert.Equal(t, "already kept", dealRun(t, "receive", file)["record"])
}

func recDigest(t *testing.T, r xdealv1.Record) string {
	t.Helper()
	d, err := xdealv1.Digest(r)
	require.NoError(t, err)
	return d
}

func TestADisputeCitesTheHoldersStatementsOnTheRow(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	var stmt string
	withSession(t, func(s *dealSession) {
		sealed, err := s.sealBookV1(t.Context(), h.dealID, bookRecordIn{Body: xdealv1.StatementBody{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "not_met"}}}})
		require.NoError(t, err)
		stmt = sealed.Digest
	})
	file := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", file)
	read, err := xdealv1.VerifyBook(readPush(t, file).Record)
	require.NoError(t, err)
	body, _ := disputeOf(read.Record)
	require.Len(t, body.Statements, 1)
	assert.Equal(t, stmt, body.Statements[0].Digest)
}

func TestADisputeRunTwiceSealsOnce(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	first, second := filepath.Join(t.TempDir(), "a.json"), filepath.Join(t.TempDir(), "b.json")
	a := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", first)
	b := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", second)
	assert.Equal(t, a["dispute"], b["dispute"])
	assert.Equal(t, a["seq"], b["seq"], "the same record, not a second one")
	assert.Equal(t, readPush(t, first).Record, readPush(t, second).Record)
}

func TestAWithdrawalTakesTheDisputeBackAndAnotherMayFollow(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	dir := t.TempDir()
	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw", "--out", filepath.Join(dir, "w0.json")), "no open dispute")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", filepath.Join(dir, "d1.json"))
	w := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw", "--out", filepath.Join(dir, "w1.json"))
	assert.Equal(t, xdealv1.KindWithdrawal, w["kind"])
	again := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw", "--out", filepath.Join(dir, "w2.json"))
	assert.Equal(t, w["dispute"], again["dispute"], "a withdrawal run twice is one record")
	d2 := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", filepath.Join(dir, "d2.json"))
	assert.Equal(t, xdealv1.KindDispute, d2["kind"])
	assert.EqualValues(t, 4, d2["seq"], "agreement, dispute, withdrawal, dispute")
}

func TestADisputeIsRefusedWhereTheDealAllowsNone(t *testing.T) {
	// An agreement with no adjudicator names no forum.
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", filepath.Join(t.TempDir(), "x.json")), "names no adjudicator")

	two := agreedBoth(t, func(k string) string { return disputeOffer(k, "auto:twin", "external:court") })
	two.buyer.act(t)
	dir := t.TempDir()
	assert.Contains(t, v1Err(t, "dispute", "--deal", two.dealID, "--row", "paid_as_agreed", "--out", filepath.Join(dir, "a.json")), "--forum is required")
	assert.Contains(t, v1Err(t, "dispute", "--deal", two.dealID, "--row", "paid_as_agreed", "--forum", "auto:other", "--out", filepath.Join(dir, "b.json")), "not one the agreement accepts")
	assert.Contains(t, v1Err(t, "dispute", "--deal", two.dealID, "--row", "no_such_row", "--forum", "auto:twin", "--out", filepath.Join(dir, "c.json")), "has no row no_such_row")
	assert.Contains(t, v1Err(t, "dispute", "--deal", two.dealID, "--row", "paid_as_agreed", "--withdraw", "--forum", "auto:twin"), "--forum goes with a dispute")
	ok := dealRun(t, "dispute", "--deal", two.dealID, "--row", "paid_as_agreed", "--forum", "external:court", "--out", filepath.Join(dir, "d.json"))
	assert.Equal(t, "external:court", ok["forum"])
}

func TestADisputeBeforeAnAgreementOpensNoBook(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	msg := v1Err(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed")
	assert.Contains(t, msg, "has no agreement of yours yet")
	assert.Contains(t, v1Err(t, "dispute", "--deal", "deal-0000000000000000", "--row", "r"), "is not an x-deal-v1 deal this profile holds")
}

// An old command run again after a later dispute must not seal anything: a
// withdrawal repeated with its old file, after the row was disputed again,
// fails before it seals and leaves the new dispute open.
func TestAnOldWithdrawalRerunSealsNothing(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	dir := t.TempDir()
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", filepath.Join(dir, "d1.json"))
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw", "--out", filepath.Join(dir, "w1.json"))
	d2 := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", filepath.Join(dir, "d2.json"))
	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw", "--out", filepath.Join(dir, "w1.json")), "exists")
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), h.dealID)
		require.NoError(t, err)
		book, err := s.ownBookV1(t.Context(), d)
		require.NoError(t, err)
		assert.Len(t, book, 4, "agreement, dispute, withdrawal, dispute: nothing sealed by the rerun")
		_, open, _, _ := openDisputeOn(book, "paid_as_agreed")
		assert.True(t, open, "the new dispute is still open")
	})
	assert.EqualValues(t, 4, d2["seq"])
}

// A run that sealed the record and stopped before the file was written finds
// the record again: dispute and withdrawal each write the same record, and
// the book does not grow.
func TestARunStoppedBeforeTheFileIsFinishedByRunningAgain(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	seal := func(body xdealv1.DisputeBody) {
		withSession(t, func(s *dealSession) {
			_, err := s.sealBookV1(t.Context(), h.dealID, bookRecordIn{Body: body})
			require.NoError(t, err)
		})
	}
	size := func() int {
		n := 0
		withSession(t, func(s *dealSession) {
			d, err := s.v1DealOf(t.Context(), h.dealID)
			require.NoError(t, err)
			book, err := s.ownBookV1(t.Context(), d)
			require.NoError(t, err)
			n = len(book)
		})
		return n
	}
	// The dispute was sealed (as the verb would), no file written.
	dir := t.TempDir()
	seal(xdealv1.DisputeBody{Kind: xdealv1.KindDispute, Row: "paid_as_agreed", Statements: []xdealv1.Ref{}, Adjudicator: "adjudicator-key", Forum: "auto:twin"})
	dFile := filepath.Join(dir, "d.json")
	got := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dFile)
	assert.EqualValues(t, 2, got["seq"])
	assert.Equal(t, 2, size())
	read, err := xdealv1.VerifyBook(readPush(t, dFile).Record)
	require.NoError(t, err)
	assert.Equal(t, got["dispute"], recDigest(t, read.Record))

	// The withdrawal was sealed, no file written.
	seal(xdealv1.DisputeBody{Kind: xdealv1.KindWithdrawal, Row: "paid_as_agreed"})
	wFile := filepath.Join(dir, "w.json")
	w := dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw", "--out", wFile)
	assert.EqualValues(t, 3, w["seq"])
	assert.Equal(t, 3, size())
	h.seller.act(t)
	for _, f := range []string{dFile, wFile} {
		assert.Equal(t, "kept, and counted", dealRun(t, "receive", f)["record"])
	}
}

// The same command with the same --out is a rerun: it writes the same file
// and the book does not grow; a forum that differs from the open dispute's is
// refused; a link left at --out blocks a new record before it is sealed.
func TestARerunWithTheSameOutIsTheSameFile(t *testing.T) {
	h := agreedBoth(t, func(k string) string { return disputeOffer(k, "auto:twin", "external:court") })
	h.buyer.act(t)
	dir := t.TempDir()
	d, w := filepath.Join(dir, "d.json"), filepath.Join(dir, "w.json")
	args := []string{"dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--forum", "external:court"}
	first := dealRun(t, append(args, "--out", d)...)
	before := mustRead(t, d)
	again := dealRun(t, append(args, "--out", d)...)
	assert.Equal(t, first["dispute"], again["dispute"])
	assert.Equal(t, before, mustRead(t, d), "the file is unchanged")
	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--forum", "auto:twin", "--out", filepath.Join(dir, "o.json")), "take it back with --withdraw first")

	wargs := []string{"dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw"}
	one := dealRun(t, append(wargs, "--out", w)...)
	wbefore := mustRead(t, w)
	two := dealRun(t, append(wargs, "--out", w)...)
	assert.Equal(t, one["dispute"], two["dispute"])
	assert.Equal(t, wbefore, mustRead(t, w))

	link := filepath.Join(dir, "dangling.json")
	require.NoError(t, os.Symlink(filepath.Join(dir, "nowhere.json"), link))
	assert.Contains(t, v1Err(t, append(args, "--out", link)...), "exists")
	withSession(t, func(s *dealSession) {
		dl, err := s.v1DealOf(t.Context(), h.dealID)
		require.NoError(t, err)
		book, err := s.ownBookV1(t.Context(), dl)
		require.NoError(t, err)
		assert.Len(t, book, 3, "agreement, dispute, withdrawal: the link sealed nothing")
	})
}

// A v1 deal takes no v0 dispute note: `deal note` is refused on it, so the
// v0 stand-in cannot seal the wrong kind of record beside this verb.
func TestAV1DealTakesNoV0DisputeNote(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	msg := v1Err(t, "note", "--deal", h.dealID, "--kind", "dispute", "--row", "paid_as_agreed", "--words", "late")
	assert.Contains(t, msg, "x-deal-v1 deal")
	assert.Contains(t, msg, "`deal dispute`", "the refusal names the verb to use")
}
