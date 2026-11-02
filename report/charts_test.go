// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/chartspec"
	"github.com/action-state-group/capsule-viewer/go/emitter"
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

// calendarEntry has no "view" key, so only the per-chart fill and check run;
// viewEntry is the same lanes as a calendar view, whose own rules apply too.
const calendarEntry = `{"lanes":[
 {"id":"cases","role":"activity","chart":{"$schema":"https://vega.github.io/schema/vega-lite/v5.json","data":{"name":"dataset"},"mark":"bar",
  "transform":[{"filter":{"field":"record_type","equal":"case"}}],
  "encoding":{"x":{"field":"event_time_claim","type":"temporal","timeUnit":"utcyearmonthdate"},"y":{"aggregate":"count","type":"quantitative"}}}},
 {"id":"verdicts","role":"judgment","chart":{"$schema":"https://vega.github.io/schema/vega-lite/v5.json","data":{"name":"dataset"},"mark":"rect",
  "transform":[{"filter":{"and":[{"field":"record_type","equal":"case"},{"field":"statement.verdict","oneOf":["resolved","missed"]}]}}],
  "encoding":{"x":{"field":"event_time_claim","type":"temporal","timeUnit":"utcyearmonthdate"},"y":{"field":"statement.verdict","type":"nominal"},"color":{"aggregate":"count","type":"quantitative"}}}}]}`

var viewEntry = `{"view":"calendar",` + calendarEntry[1:]

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
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "withheld")

	// The view's own rules refuse as ErrRefused too: a calendar states a
	// whole month, and this window is three days.
	_, err = fillCharts([]json.RawMessage{json.RawMessage(viewEntry)}, chartDataset(1))
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "the filled view is refused")

	for name, mutate := range map[string]func(map[string]interface{}){
		"missing from":      func(p map[string]interface{}) { delete(p, "from") },
		"short date":        func(p map[string]interface{}) { p["from"] = "2026-9-1" },
		"from after to":     func(p map[string]interface{}) { p["from"], p["to"] = "2026-09-05", "2026-09-01" },
		"not a date at all": func(p map[string]interface{}) { p["to"] = "later" },
	} {
		malformed := chartDataset(1)
		mutate(malformed.Plan["nodes"].([]interface{})[0].(map[string]interface{})["params"].(map[string]interface{}))
		_, err = fillCharts(entry, malformed)
		assert.ErrorIs(t, err, ErrRefused, name)
		assert.ErrorContains(t, err, "not a YYYY-MM-DD range", name)
	}

	for name, edit := range map[string][2]string{
		"sum aggregate":    {`"aggregate":"count"`, `"aggregate":"sum"`},
		"wrong time unit":  {`"timeUnit":"utcyearmonthdate"`, `"timeUnit":"utcyearmonth"`},
		"no count channel": {`"y":{"aggregate":"count","type":"quantitative"}`, `"y":{"field":"record_type","type":"nominal"}`},
	} {
		changed := strings.Replace(calendarEntry, edit[0], edit[1], 1)
		require.NotEqual(t, calendarEntry, changed, name)
		_, err = fillCharts([]json.RawMessage{json.RawMessage(changed)}, chartDataset(1))
		assert.ErrorIs(t, err, ErrRefused, name)
	}
}

// Build refuses a charted view whose Result discloses no dataset, and a page
// built with charts carries no permalink.
func TestBuildChartsRefusalAndNoPermalink(t *testing.T) {
	raw := mustRead(t, presentationDir+"/4-monthly-outcome/bundle.json")
	_, err := Build(raw, View{Card: "outcome", Charts: []json.RawMessage{json.RawMessage(calendarEntry)}})
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "charts")

	plain, err := Build(raw, View{Card: "outcome"})
	require.NoError(t, err)
	_, err = plain.Permalink("")
	assert.NoError(t, err)
	plain.charted = true
	_, err = plain.Permalink("")
	assert.ErrorIs(t, err, ErrRefused)
}

// viewsDir holds capsule-viewer's committed month bundle and the five view
// entries its goldens draw (ts/test/testdata/views at go/v0.12.0). The bundle
// is not a verifiable one, so the tests read its dataset and hand the filled
// entries to the viewer's emitter; Build's own verification is covered by the
// presentation fixtures.
const viewsDir = "testdata/views"

func viewFixtures(t *testing.T) (map[string]interface{}, []json.RawMessage) {
	t.Helper()
	bundle, err := cli.DecodeBundleJSON(mustRead(t, viewsDir+"/month.bundle.json"))
	require.NoError(t, err)
	var views []json.RawMessage
	require.NoError(t, json.Unmarshal(mustRead(t, viewsDir+"/views.json"), &views))
	require.Len(t, views, 5)
	return bundle, views
}

// unfilled strips what the builder states: the window range filter and the
// usermeta.capsule statement of every chart spec.
func unfilled(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &entry))
	for _, spec := range chartSpecsOf(entry) {
		delete(spec, "usermeta")
		transform, _ := spec["transform"].([]interface{})
		kept := []interface{}{}
		for _, item := range transform {
			filter, _ := item.(map[string]interface{})["filter"].(map[string]interface{})
			if _, isRange := filter["range"]; !isRange {
				kept = append(kept, item)
			}
		}
		spec["transform"] = kept
	}
	out, err := json.Marshal(entry)
	require.NoError(t, err)
	return out
}

// The builder states what capsule-viewer's Go and TypeScript checks accept: a
// view entry stripped of its statement is filled back to the committed
// entry, and a Go-built page then carries the view for the runtime to draw
// (capsule-viewer#156).
func TestFillChartsRestoresTheViewerFixturesAndAGoPageCarriesThem(t *testing.T) {
	bundle, views := viewFixtures(t)
	dataset, err := chartspec.DatasetOf(bundle)
	require.NoError(t, err)
	require.NotNil(t, dataset)
	stripped := make([]json.RawMessage, len(views))
	for i, raw := range views {
		stripped[i] = unfilled(t, raw)
	}
	filled, err := fillCharts(stripped, dataset)
	require.NoError(t, err)
	for i := range views {
		assert.JSONEq(t, string(views[i]), string(filled[i]), "entry %d", i)
	}
	page, err := emitter.BuildOfflineHTML(bundle, []byte("/*IIFE_MARKER*/"), emitter.OfflineOptions{Audience: "*", Charts: filled})
	require.NoError(t, err)
	for _, kind := range []string{"calendar", "by-period", "timeline", "co-failure"} {
		assert.Contains(t, page, `"view":"`+kind+`"`)
	}
}

// Charts are stated only from a verified Result: a tampered bundle with
// charts fails verification, not the chart fill.
func TestBuildChartsAreFilledAfterVerification(t *testing.T) {
	raw := mustRead(t, bookFormBundle)
	tampered := edited(t, raw, func(v map[string]interface{}) {
		v["records"] = v["records"].([]interface{})[1:]
	})
	_, err := Build(tampered, View{Card: "outcome", Charts: []json.RawMessage{json.RawMessage(calendarEntry)}})
	require.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "does not verify")
	assert.NotContains(t, err.Error(), "charts")
}
