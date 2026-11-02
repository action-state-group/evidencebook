// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"testing"

	"github.com/action-state-group/capsule-viewer/go/chartspec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func chartDataset(windows int) *chartspec.Dataset {
	nodes := []interface{}{}
	for i := 0; i < windows; i++ {
		nodes = append(nodes, map[string]interface{}{"operator": "traditional.window", "params": map[string]interface{}{
			"field": "event_time_claim", "from": "2026-09-01", "to": "2026-09-03"}})
	}
	row := func(seq int64, typ, at string, statement map[string]interface{}) chartspec.Row {
		return chartspec.Row{Seq: seq, RecordType: typ, Header: map[string]interface{}{
			"record_type": typ, "event_time_claim": at, "statement": statement}}
	}
	return &chartspec.Dataset{Plan: map[string]interface{}{"nodes": nodes}, Rows: []chartspec.Row{
		row(1, "daily_run", "2026-09-01T02:00:00Z", map[string]interface{}{}),
		row(2, "case", "2026-09-01T05:00:00Z", map[string]interface{}{"verdict": "resolved"}),
		row(3, "case", "2026-09-01T06:00:00Z", map[string]interface{}{"verdict": "missed"}),
		row(4, "daily_run", "2026-09-02T02:00:00Z", map[string]interface{}{}),
		// 2026-09-02 at 23:30-05:00 is 09-03 UTC, a day with no run record.
		row(5, "case", "2026-09-02T23:30:00-05:00", map[string]interface{}{"verdict": "resolved"}),
	}}
}

const calendarEntry = `{"view":"calendar","lanes":[
 {"id":"cases","role":"activity","chart":{"$schema":"https://vega.github.io/schema/vega-lite/v5.json","data":{"name":"dataset"},"mark":"bar",
  "transform":[{"filter":{"field":"record_type","equal":"case"}}],
  "encoding":{"x":{"field":"event_time_claim","type":"temporal","timeUnit":"utcyearmonthdate"},"y":{"aggregate":"count","type":"quantitative"}}}},
 {"id":"verdicts","role":"judgment","chart":{"$schema":"https://vega.github.io/schema/vega-lite/v5.json","data":{"name":"dataset"},"mark":"rect",
  "transform":[{"filter":{"and":[{"field":"record_type","equal":"case"},{"field":"statement.verdict","oneOf":["resolved","missed"]}]}}],
  "encoding":{"x":{"field":"event_time_claim","type":"temporal","timeUnit":"utcyearmonthdate"},"y":{"field":"statement.verdict","type":"nominal"},"color":{"aggregate":"count","type":"quantitative"}}}}]}`

func TestFillChartsStatesWindowRunsAndValues(t *testing.T) {
	out, err := fillCharts([]json.RawMessage{json.RawMessage(calendarEntry)}, chartDataset(1))
	require.NoError(t, err)
	var entry struct {
		Lanes []struct {
			Chart struct {
				Transform []map[string]interface{} `json:"transform"`
				Usermeta  struct {
					Capsule struct {
						Population struct{ Count int } `json:"population"`
						Values     []map[string]interface{}
					} `json:"capsule"`
				} `json:"usermeta"`
			} `json:"chart"`
		} `json:"lanes"`
	}
	require.NoError(t, json.Unmarshal(out[0], &entry))
	activity := entry.Lanes[0].Chart
	assert.Equal(t, 3, activity.Usermeta.Capsule.Population.Count)
	assert.Len(t, activity.Transform, 2, "the window range filter is added before the lane's own filter")
	assert.Contains(t, activity.Transform[0], "filter")
	assert.Equal(t, []map[string]interface{}{
		{"x": "2026-09-01", "y": float64(2)},
		{"x": "2026-09-02", "y": float64(0)},
		{"x": "2026-09-03", "not_run": true},
	}, activity.Usermeta.Capsule.Values)
	// The keyed lane lists every day by every verdict seen, a not-run day not run.
	assert.Len(t, entry.Lanes[1].Chart.Usermeta.Capsule.Values, 6)
}

func TestFillChartsRefusals(t *testing.T) {
	entry := []json.RawMessage{json.RawMessage(calendarEntry)}
	_, err := fillCharts(entry, nil)
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "dataset")

	_, err = fillCharts(entry, chartDataset(2))
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "2 window nodes")

	unsupported := `{"view":"timeline","lanes":[{"id":"a","mark":"tick","chart":{"data":{"name":"dataset"},"mark":"bar",
	 "transform":[{"filter":{"field":"record_type","range":[1,2]}}],
	 "encoding":{"x":{"field":"event_time_claim","type":"temporal","timeUnit":"utcyearmonthdate"},"y":{"aggregate":"count","type":"quantitative"}}}}]}`
	_, err = fillCharts([]json.RawMessage{json.RawMessage(unsupported)}, chartDataset(1))
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "only those are filled")

	_, err = fillCharts([]json.RawMessage{json.RawMessage(`[1]`)}, chartDataset(1))
	assert.ErrorIs(t, err, ErrRefused)

	withheld := chartDataset(1)
	withheld.Rows[1].Header = nil
	_, err = fillCharts(entry, withheld)
	assert.ErrorContains(t, err, "withheld")
}
