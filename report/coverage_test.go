// SPDX-License-Identifier: Apache-2.0

package report

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A report over a holder's time windows states the range its caller's
// coverage check found answered, and what that check does not show
// (evidencebook#689).
func TestBuildStatesTheCoverageOfAReportOverWindows(t *testing.T) {
	page, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role", Coverage: &Coverage{From: "2026-01", To: "2026-12", Windows: 12}})
	require.NoError(t, err)
	html := string(page.HTML)
	assert.Contains(t, html, `<div class="aac-banner" data-state="verified" role="status"><strong>Covers 2026-01 to 2026-12.</strong> Each of the 12 windows has its answer`)
	assert.Contains(t, html, "no check shows that an answer holds every row of its window")
	assert.Equal(t, 1, strings.Count(html, `data-state="verified" role="status"`))
	_, err = page.Permalink("")
	require.ErrorIs(t, err, ErrRefused, "the permalink carries the bundle alone and would state no range")

	one, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role", Coverage: &Coverage{From: "2026", To: "2026", Windows: 1}})
	require.NoError(t, err)
	assert.Contains(t, string(one.HTML), "Each of the 1 window has its answer")

	escaped, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role", Coverage: &Coverage{From: "<b>", To: "x", Windows: 1}})
	require.NoError(t, err)
	assert.NotContains(t, string(escaped.HTML), "Covers <b>")
	assert.Contains(t, string(escaped.HTML), "Covers &lt;b&gt; to x.")

	plain, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role"})
	require.NoError(t, err)
	assert.NotContains(t, string(plain.HTML), `role="status"><strong>Covers`)
}

func TestBuildRefusesAnIncompleteCoverage(t *testing.T) {
	for _, c := range []Coverage{{To: "2026-12", Windows: 1}, {From: "2026-01", Windows: 1}, {From: "2026-01", To: "2026-12"}} {
		page, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role", Coverage: &c})
		require.ErrorIs(t, err, ErrRefused)
		assert.Nil(t, page)
	}
	page, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role", Diagnostic: true, Coverage: &Coverage{From: "2026-01", To: "2026-12", Windows: 12}})
	require.ErrorIs(t, err, ErrRefused)
	assert.Nil(t, page)
}
