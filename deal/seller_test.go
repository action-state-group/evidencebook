// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
)

// An agreement cites an intent that names the seller only when it is the
// buyer's and names the same counterparty (evidencebook#1031).
func TestCitableSeller(t *testing.T) {
	key := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	intent := func(seller *xdealv1.Seller) xdealv1.Record {
		return xdealv1.Record{Body: &xdealv1.IntentBody{IntentBody: dealprofile.IntentBody{PartyRole: xdealv1.RoleBuyer}, Seller: seller}}
	}
	named := func(k xdealv1.Null[string]) *xdealv1.Seller {
		return &xdealv1.Seller{Key: k, NameCommitment: strings.Repeat("c", 64)}
	}
	for name, c := range map[string]struct {
		intent xdealv1.Record
		role   string
		key    xdealv1.Null[string]
		err    string
	}{
		"no seller":               {intent(nil), xdealv1.RoleSeller, xdealv1.Some(key), ""},
		"the buyer, same key":     {intent(named(xdealv1.Some(key))), xdealv1.RoleBuyer, xdealv1.Some(key), ""},
		"the buyer, both null":    {intent(named(xdealv1.NullOf[string]())), xdealv1.RoleBuyer, xdealv1.NullOf[string](), ""},
		"the seller's agreement":  {intent(named(xdealv1.Some(key))), xdealv1.RoleSeller, xdealv1.Some(key), "this agreement is the seller's"},
		"the seller's, both null": {intent(named(xdealv1.NullOf[string]())), xdealv1.RoleSeller, xdealv1.NullOf[string](), "this agreement is the seller's"},
		"another key":             {intent(named(xdealv1.Some(other))), xdealv1.RoleBuyer, xdealv1.Some(key), "by another key"},
		"a key where null":        {intent(named(xdealv1.Some(key))), xdealv1.RoleBuyer, xdealv1.NullOf[string](), "by another key"},
		"null where a key":        {intent(named(xdealv1.NullOf[string]())), xdealv1.RoleBuyer, xdealv1.Some(key), "by another key"},
	} {
		err := citableSeller(c.intent, c.role, c.key)
		if c.err == "" {
			assert.NoError(t, err, name)
		} else {
			assert.ErrorContains(t, err, c.err, name)
		}
	}
}
