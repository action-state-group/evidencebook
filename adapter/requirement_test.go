// SPDX-License-Identifier: Apache-2.0

package adapter

import "testing"

func TestEvaluate(t *testing.T) {
	res := []PropertyResult{
		{Property: "sig", Status: Pass, Method: "m"},
		{Property: "bad", Status: Fail, Method: "m"},
		{Property: "off", Status: NotChecked, Method: "m"},
		{Property: "undecided", Status: Inconclusive, Method: "m"},
		{Property: "gone", Status: NotPresent, Method: "m"},
	}
	cases := []struct {
		rel        Relation
		dependsOn  []string
		suff, verd string
	}{
		{IsTheCheck, []string{"sig"}, "SATISFIED", "met"},
		{SupportedByTheCheck, []string{"sig"}, "", ""},
		{IsTheCheck, []string{"sig", "bad", "off"}, "SATISFIED", "not_met"},
		{SupportedByTheCheck, []string{"sig", "bad"}, "INSUFFICIENT", "not_evaluable"},
		{IsTheCheck, []string{"sig", "gone", "off"}, "INSUFFICIENT", "not_evaluable"},
		{IsTheCheck, []string{"sig", "never-reported"}, "INSUFFICIENT", "not_evaluable"},
		{IsTheCheck, []string{"sig", "off"}, "UNKNOWN", "not_evaluable"},
		{SupportedByTheCheck, []string{"undecided"}, "UNKNOWN", "not_evaluable"},
	}
	for _, c := range cases {
		ev := Evaluate(res, Requirement{ID: "r", Relation: c.rel, DependsOn: c.dependsOn})
		if ev.Sufficiency != c.suff || ev.Verdict != c.verd {
			t.Errorf("%v %v: %+v, want %s/%s", c.rel, c.dependsOn, ev, c.suff, c.verd)
		}
		if ev.Verdict == "met" && ev.Sufficiency != "SATISFIED" {
			t.Errorf("met without SATISFIED: %+v", ev)
		}
	}
}
