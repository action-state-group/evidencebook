// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/action-state-group/capsule-viewer/go/chartspec"
)

// runRecordType is the record type of a daily run, which dates a day as run
// (capsule-viewer docs/design/chart-spec.md section 4).
const runRecordType = "daily_run"

// fillCharts completes the view entries of a pack's chart-views/v1 `charts`
// from the sealed dataset (capsule-viewer docs/design/chart-spec.md section
// 3): every chart spec in them gets the `range` filter on the sealed window
// and its usermeta.capsule statement (population, window, runs, values),
// which the pack compiler cannot state because it never sees the dataset.
// chartspec.Check then recomputes every statement from the same rows, so a
// wrong fill refuses the build and the viewer's own check is the only judge.
//
// It fills the charts the compiler emits: day-unit bar and rect counts whose
// filters are equal, oneOf and and. Any other chart is refused by name.
func fillCharts(entries []json.RawMessage, dataset *chartspec.Dataset) ([]json.RawMessage, error) {
	if dataset == nil {
		return nil, refusal("charts: the Result names no dataset (plan_ref and dataset_ref), or the bundle does not disclose it; the chart statements cannot be stated")
	}
	window, err := sealedWindow(dataset)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, len(entries))
	for i, raw := range entries {
		var entry map[string]interface{}
		if err := json.Unmarshal(raw, &entry); err != nil || entry == nil {
			return nil, refusef("charts[%d] is not a JSON object", i)
		}
		for _, spec := range chartSpecsOf(entry) {
			if err := fillChart(spec, dataset, window); err != nil {
				return nil, refusef("charts[%d]: %s", i, err)
			}
			// Check reads the spec as decoded JSON, so numbers are float64.
			encoded, err := json.Marshal(spec)
			if err != nil {
				return nil, err
			}
			var decoded interface{}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				return nil, err
			}
			if _, err := chartspec.Check(decoded, dataset); err != nil {
				return nil, refusef("charts[%d]: the filled chart is refused: %s", i, err)
			}
		}
		if out[i], err = json.Marshal(entry); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// chartSpecsOf returns the chart specs a view entry holds: each lane's
// `chart` (calendar, timeline) and the `count` chart of a co-failure.
func chartSpecsOf(entry map[string]interface{}) []map[string]interface{} {
	var specs []map[string]interface{}
	lanes, _ := entry["lanes"].([]interface{})
	for _, raw := range lanes {
		if lane, ok := raw.(map[string]interface{}); ok {
			if spec, ok := lane["chart"].(map[string]interface{}); ok {
				specs = append(specs, spec)
			}
		}
	}
	if count, ok := entry["count"].(map[string]interface{}); ok {
		if spec, ok := count["chart"].(map[string]interface{}); ok {
			specs = append(specs, spec)
		}
	}
	return specs
}

// sealedWindow is the dataset plan's one traditional.window node: the sealed
// window a chart states. A plan with none or several has no window a chart
// can state without choosing, so it is refused.
func sealedWindow(dataset *chartspec.Dataset) (chartspec.Window, error) {
	var found []chartspec.Window
	nodes, _ := dataset.Plan["nodes"].([]interface{})
	for _, raw := range nodes {
		node, _ := raw.(map[string]interface{})
		if node["operator"] != "traditional.window" {
			continue
		}
		p, _ := node["params"].(map[string]interface{})
		field, _ := p["field"].(string)
		from, _ := p["from"].(string)
		to, _ := p["to"].(string)
		found = append(found, chartspec.Window{Field: field, From: from, To: to})
	}
	if len(found) != 1 {
		return chartspec.Window{}, refusef("charts: the dataset plan has %d window nodes; the chart statements need exactly one sealed window", len(found))
	}
	return found[0], nil
}

// fillChart adds the window filter and statement to one chart spec.
func fillChart(spec map[string]interface{}, dataset *chartspec.Dataset, window chartspec.Window) error {
	encoding, _ := spec["encoding"].(map[string]interface{})
	var temporal string
	var aggregate string
	var keys []string
	for _, name := range sortedKeys(encoding) {
		channel, _ := encoding[name].(map[string]interface{})
		if channel["aggregate"] != nil {
			if channel["aggregate"] != "count" {
				return fmt.Errorf("channel %s aggregates %v; only count is filled", name, channel["aggregate"])
			}
			aggregate = name
			continue
		}
		keys = append(keys, name)
		if channel["type"] == "temporal" {
			if channel["timeUnit"] != "utcyearmonthdate" || channel["field"] != window.Field {
				return fmt.Errorf("the time channel %s must read %s by utcyearmonthdate", name, window.Field)
			}
			temporal = name
		}
	}
	if aggregate == "" || temporal == "" {
		return fmt.Errorf("a chart needs a count channel and a day axis on %s", window.Field)
	}
	transform, _ := spec["transform"].([]interface{})
	preds := make([]pred, 0, len(transform))
	for _, raw := range transform {
		item, _ := raw.(map[string]interface{})
		p, err := readPred(item["filter"])
		if err != nil {
			return err
		}
		preds = append(preds, p)
	}
	spec["transform"] = append([]interface{}{map[string]interface{}{"filter": map[string]interface{}{
		"field": window.Field, "timeUnit": "utcyearmonthdate",
		"range": []interface{}{dateTime(window.From), dateTime(window.To)},
	}}}, transform...)

	runs := map[string]bool{}
	var selected []*chartspec.Row
	for i := range dataset.Rows {
		row := &dataset.Rows[i]
		if row.Header == nil {
			return fmt.Errorf("seq %d is withheld; its record is not disclosed to this audience", row.Seq)
		}
		day, hasDay := dayOf(row.Header, window.Field)
		inWindow := hasDay && day >= window.From && day <= window.To
		if row.RecordType == runRecordType && inWindow {
			runs[day] = true
		}
		if !inWindow {
			continue
		}
		pass := true
		for _, p := range preds {
			pass = pass && p.eval(row)
		}
		if pass {
			selected = append(selected, row)
		}
	}

	days, err := daysOf(window)
	if err != nil {
		return err
	}
	// group: day, then the other key channel's values in code point order.
	type cell struct {
		keys map[string]interface{}
		n    int
	}
	var others []string
	for _, k := range keys {
		if k != temporal {
			others = append(others, k)
		}
	}
	if len(others) > 1 {
		return fmt.Errorf("a chart has at most one key channel besides its day axis")
	}
	counts := map[string]int{}
	domain := map[string]bool{}
	for _, row := range selected {
		day, _ := dayOf(row.Header, window.Field)
		other := ""
		if len(others) == 1 {
			v, ok := resolve(row.Header, channelField(encoding, others[0]))
			if !ok {
				return fmt.Errorf("seq %d has no %s", row.Seq, channelField(encoding, others[0]))
			}
			other = textOf(v)
			domain[other] = true
		}
		counts[day+"\x00"+other]++
	}
	var domainValues []string
	if len(others) == 1 {
		for v := range domain {
			domainValues = append(domainValues, v)
		}
		sort.Strings(domainValues)
	} else {
		domainValues = []string{""}
	}
	var values []interface{}
	for _, day := range days {
		for _, other := range domainValues {
			v := map[string]interface{}{temporal: day}
			if len(others) == 1 {
				v[others[0]] = otherValue(selected, window.Field, encoding, others[0], other)
			}
			if runs[day] {
				v[aggregate] = counts[day+"\x00"+other]
			} else {
				v["not_run"] = true
			}
			values = append(values, v)
		}
	}
	spec["usermeta"] = map[string]interface{}{"capsule": map[string]interface{}{
		"population": map[string]interface{}{"count": len(selected)},
		"window":     map[string]interface{}{"field": window.Field, "from": window.From, "to": window.To},
		"runs": map[string]interface{}{
			"filter": map[string]interface{}{"field": "record_type", "equal": runRecordType},
			"field":  window.Field,
		},
		"values": values,
	}}
	return nil
}

// otherValue returns the key channel's value whose text is text, with its
// JSON type (a string stays a string, a number a number).
func otherValue(rows []*chartspec.Row, windowField string, encoding map[string]interface{}, name, text string) interface{} {
	field := channelField(encoding, name)
	for _, row := range rows {
		if v, ok := resolve(row.Header, field); ok && textOf(v) == text {
			return v
		}
	}
	return text
}

func channelField(encoding map[string]interface{}, name string) string {
	channel, _ := encoding[name].(map[string]interface{})
	field, _ := channel["field"].(string)
	return field
}

func textOf(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolve is a header path: the first name is a header member and a payload
// member is statement.<name>.
func resolve(header map[string]interface{}, path string) (interface{}, bool) {
	var at interface{} = header
	for _, name := range strings.Split(path, ".") {
		m, ok := at.(map[string]interface{})
		if !ok {
			return nil, false
		}
		if at, ok = m[name]; !ok {
			return nil, false
		}
	}
	return at, true
}

// dayOf is the UTC calendar day of an RFC 3339 timestamp field.
func dayOf(header map[string]interface{}, field string) (string, bool) {
	v, ok := resolve(header, field)
	s, isString := v.(string)
	if !ok || !isString {
		return "", false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return "", false
	}
	return t.UTC().Format("2006-01-02"), true
}

func daysOf(w chartspec.Window) ([]string, error) {
	from, err := time.Parse("2006-01-02", w.From)
	if err != nil {
		return nil, err
	}
	to, err := time.Parse("2006-01-02", w.To)
	if err != nil {
		return nil, err
	}
	var days []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
	}
	return days, nil
}

func dateTime(day string) map[string]interface{} {
	y, _ := strconv.Atoi(day[0:4])
	m, _ := strconv.Atoi(day[5:7])
	d, _ := strconv.Atoi(day[8:10])
	return map[string]interface{}{"year": y, "month": m, "date": d, "utc": true}
}

// pred is the filter predicates the pack compiler emits.
type pred struct {
	field  string
	equal  interface{}
	oneOf  []interface{}
	hasEq  bool
	hasOne bool
	and    []pred
}

func readPred(raw interface{}) (pred, error) {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return pred{}, fmt.Errorf("a transform entry is not a filter predicate")
	}
	if items, ok := m["and"].([]interface{}); ok {
		var p pred
		for _, item := range items {
			sub, err := readPred(item)
			if err != nil {
				return pred{}, err
			}
			p.and = append(p.and, sub)
		}
		return p, nil
	}
	field, _ := m["field"].(string)
	p := pred{field: field}
	if v, ok := m["equal"]; ok {
		p.equal, p.hasEq = v, true
		return p, nil
	}
	if v, ok := m["oneOf"].([]interface{}); ok {
		p.oneOf, p.hasOne = v, true
		return p, nil
	}
	return pred{}, fmt.Errorf("filter on %q is not equal, oneOf or and; only those are filled", field)
}

func (p pred) eval(row *chartspec.Row) bool {
	if p.and != nil {
		for _, sub := range p.and {
			if !sub.eval(row) {
				return false
			}
		}
		return true
	}
	v, ok := resolve(row.Header, p.field)
	if !ok {
		return false
	}
	if p.hasEq {
		return textOf(v) == textOf(p.equal)
	}
	for _, w := range p.oneOf {
		if textOf(v) == textOf(w) {
			return true
		}
	}
	return false
}
