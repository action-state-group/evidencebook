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

// Ruling S1 over native_check reports, matched by the name in detail.
func TestEvaluateNativeCheckReports(t *testing.T) {
	res := []PropertyResult{
		NativeCheckResult("sig", Pass, "m", "recovers"),
		NativeCheckResult("unsigned", NotPresent, "m", "there is no signature to check"),
		NativeCheckResult("online", NotChecked, "m", ""),
	}
	if CheckName(res[0]) != "sig" || CheckName(res[2]) != "online" || res[0].Property != NativeCheck {
		t.Fatalf("%+v", res)
	}
	cases := []struct {
		dependsOn                []string
		status, suff, verd, row0 string
	}{
		{[]string{"sig"}, "SATISFIED", "SATISFIED", "met", "7"},
		{[]string{"sig", "unsigned"}, "INSUFFICIENT", "INSUFFICIENT", "not_evaluable", "3"},
		{[]string{"sig", "online"}, "UNKNOWN", "UNKNOWN", "not_evaluable", "4"},
	}
	for _, c := range cases {
		ev := Evaluate(res, Requirement{ID: "r", Relation: IsTheCheck, DependsOn: c.dependsOn})
		if ev.Status != c.status || ev.Sufficiency != c.suff || ev.Verdict != c.verd || ev.Row[:1] != c.row0 {
			t.Errorf("%v: %+v", c.dependsOn, ev)
		}
	}
	// The unsigned object's NOT_PRESENT never counts against a requirement
	// that does not rest on it.
	if ev := Evaluate(res, Requirement{ID: "r", Relation: IsTheCheck, DependsOn: []string{"sig"}}); ev.Verdict != "met" {
		t.Errorf("%+v", ev)
	}
}
