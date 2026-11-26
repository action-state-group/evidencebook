// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// Status is the design's deal.status: the status value of the holder's deal
// book of dealID as it stands now (design §8.1, xdealv1.BuildStatus), each
// row's status included, read through xdealv1.StatusProvider with the
// store's offer pairs, the same reading a stamp's status fact makes. It
// writes nothing. A deal the holder has not agreed has no deal book yet.
func (s *Store) Status(ctx context.Context, dealID string) (xdealv1.StatusValue, error) {
	d, _, err := s.agreedDeal(ctx, dealID)
	if err != nil {
		return xdealv1.StatusValue{}, err
	}
	p := &xdealv1.StatusProvider{Shelf: s.cfg.Shelf, OfferPair: s.OfferPair}
	return p.Status(ctx, d.OwnLogID)
}
