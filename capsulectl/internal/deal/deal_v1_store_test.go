// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The states a deal_sent row takes, as the deal store writes them
// (evidencebook's package deal): stored as sealing before the record is
// sealed, sealed once its pair is stored, written once the file is out.
const (
	sentSealing = "sealing"
	sentSealed  = "sealed"
	sentWritten = "written"
)

func refOf(digest string) *xdealv1.Ref {
	if digest == "" {
		return nil
	}
	r := xdealv1.RefTo(digest)
	return &r
}

// admitPayloadOf is the admit record `deal admit` seals for capsule-engine's
// output raw at the deal clock's time, for a test that stores a binding by
// hand as a stopped run would leave it.
func admitPayloadOf(raw []byte) ([]byte, error) {
	if _, err := dealapi.ParseAdmission(raw, cli.DealClock()); err != nil {
		return nil, err
	}
	var out struct {
		Admit json.RawMessage `json:"admit"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	doc := []byte(`{"x-deal-v1":{"profile":"` + xdealv1.Profile + `","canonicalization":"` + xdealv1.Canonicalization + `","record_type":"` + xdealv1.TypeAdmit + `","at":"` + v1Now() + `"},"body":`)
	doc = append(append(doc, out.Admit...), '}')
	payload, _, err := xdealv1.Encode(doc)
	return payload, err
}

// dealAdmit is the admit `deal admit` bound to dealID, or "".
func (s *dealSession) dealAdmit(ctx context.Context, dealID string) (string, error) {
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT admit_digest FROM deal_v1_admits WHERE deal_id=?`, dealID).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return digest, err
}

// journalRecord is the x-deal-v1 record the journal holds under payload
// digest digest (nil when the journal's record there is not one), and
// whether the journal holds one.
func (s *dealSession) journalRecord(ctx context.Context, digest string) (rec *xdealv1.Record, held bool, err error) {
	j, records, err := s.journalRecords(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, r := range records {
		for _, c := range r.Header.PayloadCommitments {
			if c != digest {
				continue
			}
			payload, err := j.ResolvePayload(ctx, r.RecordID, digest)
			if err != nil {
				return nil, false, err
			}
			r, err := xdealv1.Decode(payload)
			if err != nil {
				return nil, true, nil
			}
			return &r, true, nil
		}
	}
	return nil, false, nil
}

func oppositeRole(role string) string {
	if role == xdealv1.RoleBuyer {
		return xdealv1.RoleSeller
	}
	return xdealv1.RoleBuyer
}

// libErr names a store refusal's argument by its flag, keeps a conflict a
// conflict (exit code and all), and passes any other error as it is.
func TestLibErrMapsTheStoresErrors(t *testing.T) {
	plain := errors.New("disk full")
	for name, tc := range map[string]struct {
		in       error
		want     string
		conflict bool
	}{
		"input":            {in: &dealapi.InputError{Reason: "this offer is cancelled"}, want: "this offer is cancelled"},
		"input with field": {in: &dealapi.InputError{Field: "admit", Reason: " 11 is not a record in your journal"}, want: "--admit 11 is not a record in your journal"},
		"conflict":         {in: &dealapi.ConflictError{Reason: "the deal book kept changing"}, want: "the deal book kept changing", conflict: true},
		"other":            {in: plain, want: "disk full"},
	} {
		got := libErr(tc.in)
		assert.Contains(t, got.Error(), tc.want, name)
		assert.Equal(t, tc.conflict, errors.Is(got, cli.ErrConflict), name)
		if !tc.conflict && tc.in != plain {
			assert.Equal(t, cli.ExitCode(cli.InputError("x")), cli.ExitCode(got), name)
		}
	}
	assert.Equal(t, cli.ExitCode(fmt.Errorf("%w: x", cli.ErrConflict)), cli.ExitCode(libErr(&dealapi.ConflictError{Reason: "x"})))
	assert.Same(t, plain, libErr(plain))
	assert.NoError(t, libErr(nil))
}

// An offer whose rows did not commit after the store took it is recovered by
// receiving the same file again: it is already kept, its rows are written,
// and the deal can be agreed.
func TestAnOfferWhoseRowsAreGoneIsRecoveredByReceivingItAgain(t *testing.T) {
	seller := newV1Side(t)
	newV1Side(t)
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", offerFile)
	seller.act(t)
	require.Equal(t, "kept, and counted", dealRun(t, "receive", offerFile)["record"])
	// As a stop before the rows committed leaves it: the store holds the
	// offer, and none of the deal's rows exist.
	_, err := openWritableDB(t).Exec(`DELETE FROM deal_versions; DELETE FROM deal_v1; DELETE FROM deal_v1_offers`)
	require.NoError(t, err)
	got := dealRun(t, "receive", offerFile)
	assert.Equal(t, "already kept", got["record"], "the store kept it the first time")
	out := dealRun(t, "agree", res["offer"].(string), "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "a.json"))
	assert.Equal(t, "acceptor", out["side"])
}

// A read session (deal list) writes nothing to open the deal store: a profile
// with no cadence log id keeps none.
func TestAReadSessionSavesNoLogID(t *testing.T) {
	h := newHandshakeV1(t)
	h.seller.act(t)
	p := v1Profile(t)
	p.LogID = ""
	require.NoError(t, cli.SaveProfile(p, true))
	out, err := invoke(t, "", "--profile", "deal", "deal", "list")
	require.NoError(t, err, out)
	require.Contains(t, out, h.dealID)
	assert.Empty(t, v1Profile(t).LogID, "deal list saved a log id")
}
