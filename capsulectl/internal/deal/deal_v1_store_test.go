// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
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
