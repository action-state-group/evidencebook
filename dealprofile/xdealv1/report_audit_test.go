// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asAudit is a counterparty copy's report cut as an audit copy is: the same
// rows, with no witnesses and no witnessed time (evidencebook#1058).
func asAudit(r Report) Report {
	r.Audience, r.RestsOn.Witnesses = dealprofile.AudienceAudit, nil
	rows := make([]ReportRow, len(r.Rows))
	for i, row := range r.Rows {
		row.ReliedOn = append([]ReportSource{}, row.ReliedOn...)
		for j := range row.ReliedOn {
			row.ReliedOn[j].Time, row.ReliedOn[j].Witnessed = nil, false
		}
		rows[i] = row
	}
	r.Rows = rows
	return r
}

// An audit copy is the counterparty's copy with no witness evidence, and it
// carries the same records; it leaves out an offer that names an ask, a way
// to reach the offerer (Sherlock2's ruling on evidencebook#1058), and
// carries one that names none.
func TestAuditCopy(t *testing.T) {
	d := newBundleDeal(t)
	of, ok := asPropose(d.offer.Body)
	require.True(t, ok)
	require.NotNil(t, of.Ask, "the fixture's offer names an ask")
	cp, _, cx := d.bundle(t, copyAt(d.at, dealprofile.AudienceCounterparty))
	audit, pair, ax := d.bundle(t, copyAt(d.at, dealprofile.AudienceAudit))
	require.Equal(t, RowsListed, audit.RowsState)
	assert.Equal(t, asAudit(cp), audit)
	assert.Equal(t, cx.Book.Agreement, ax.Book.Agreement)
	assert.Equal(t, cx.Book.ReliedOn, ax.Book.ReliedOn)
	require.NotNil(t, cx.Book.Offer, "the counterparty's copy carries the offer")
	assert.Nil(t, ax.Book.Offer, "an audit copy leaves out an offer that names an ask")
	read, err := VerifyReportBundle(pair, extJSON(t, ax))
	require.NoError(t, err)
	assert.Equal(t, dealprofile.AudienceAudit, read.Report.Audience)

	noAsk := newBundleDealWith(t, func(o *ProposeBody) { o.Ask = nil })
	_, pair, nx := noAsk.bundle(t, copyAt(noAsk.at, dealprofile.AudienceAudit))
	assert.Equal(t, string(noAsk.offerPair), string(nx.Book.Offer), "an audit copy carries an offer that names no ask")
	_, err = VerifyReportBundle(pair, extJSON(t, nx))
	require.NoError(t, err)
}

// The audit copy's report rules hold on the report alone, as a copy written
// with --out carries it, not only inside a bundle.
func TestVerifyReportRefusesAnAuditCopyWithWitnessEvidence(t *testing.T) {
	d := newBundleDeal(t)
	audit, _, ax := d.bundle(t, copyAt(d.at, dealprofile.AudienceAudit))
	require.NotEmpty(t, audit.Rows)
	require.NotEmpty(t, audit.Rows[0].ReliedOn)
	at := d.at
	for name, edit := range map[string]func(*Report){
		"an audit copy names no witnesses": func(r *Report) {
			r.RestsOn.Witnesses = []Witness{{KeyID: label("witness"), URL: "https://witness.example"}}
		},
		"an audit copy gives no witnessed time": func(r *Report) {
			r.Rows[0].ReliedOn[0].Time, r.Rows[0].ReliedOn[0].Witnessed = &at, true
		},
		"an audit copy names no referee": func(r *Report) { r.Referee = "Lamp Court" },
	} {
		r := asAudit(audit)
		edit(&r)
		payload, err := ReportPayload(r)
		require.NoError(t, err, name)
		_, err = VerifyReport(sealReport(t, "buyer", payload, ""), ax.Book.Agreement)
		require.ErrorContains(t, err, name)
	}
}
