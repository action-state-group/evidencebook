// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"testing"

	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A step a program wrote through package deal directly, with no local form
// of this command line's, is refused by the card's readers rather than read
// short (evidencebook#899 phase 3).
func TestAStepWrittenWithoutTheLocalFormIsRefused(t *testing.T) {
	h := openedV1(t)
	withSession(t, func(s *dealSession) {
		st, err := s.dealStore(t.Context())
		require.NoError(t, err)
		_, err = st.SealJournal(t.Context(), h.dealID, dealapi.JournalStep{Kind: "note", Build: func(dealapi.StepFacts) (dealapi.StepParts, error) {
			return dealapi.StepParts{}, nil
		}})
		require.NoError(t, err)
		_, err = s.v1Events(t.Context(), h.dealID)
		assert.ErrorContains(t, err, "was written by a program that calls evidencebook's package deal directly")
	})
}
