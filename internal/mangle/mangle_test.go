package mangle

import (
	"reflect"
	"sort"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
)

func keys(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func TestRefs(t *testing.T) {
	body := "Fixes #12 and resolves #34. See also #56.\n- [ ] #7\n- [x] #8\nprose #9"
	if got := keys(refs(closesRe, body)); !reflect.DeepEqual(got, []int{12, 34}) {
		t.Errorf("closesRe = %v, want [12 34]", got)
	}
	if got := keys(refs(taskRefRe, body)); !reflect.DeepEqual(got, []int{7, 8}) {
		t.Errorf("taskRefRe = %v, want [7 8]", got)
	}
	if got := keys(refs(mentionRe, body)); !reflect.DeepEqual(got, []int{7, 8, 9, 12, 34, 56}) {
		t.Errorf("mentionRe = %v, want all", got)
	}
}

func TestSelectEpicClosure(t *testing.T) {
	iss := func(n int, body string) *forge.Issue { return &forge.Issue{Number: n, Body: body} }
	issues := []*forge.Issue{
		iss(1, "epic\n- [ ] #2\n- [ ] #3\nsee #99 in prose"), // prose #99 must NOT be followed
		iss(2, "- [x] #4"),
		iss(3, ""),
		iss(4, ""),
		iss(99, "unrelated"),
	}
	got := keys(selectIssues(issues, ExportOptions{Epic: 1}))
	if !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Errorf("epic closure = %v, want [1 2 3 4]", got)
	}
}

func TestSelectMilestoneAndLabel(t *testing.T) {
	mk := func(n int, ms string, label string) *forge.Issue {
		i := &forge.Issue{Number: n}
		if ms != "" {
			i.Milestone = &struct {
				ID    int64  `json:"id"`
				Title string `json:"title"`
			}{Title: ms}
		}
		if label != "" {
			i.Labels = []forge.Label{{Name: label}}
		}
		return i
	}
	issues := []*forge.Issue{mk(1, "M7", ""), mk(2, "M7", "bug"), mk(3, "", "bug")}
	if got := keys(selectIssues(issues, ExportOptions{Milestone: "M7"})); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("milestone = %v, want [1 2]", got)
	}
	if got := keys(selectIssues(issues, ExportOptions{Label: "bug"})); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Errorf("label = %v, want [2 3]", got)
	}
	if selectIssues(issues, ExportOptions{}) != nil {
		t.Errorf("no selector should return nil (all)")
	}
}

func TestNameC(t *testing.T) {
	cases := map[string]string{
		"claude-bot":   "/claude_bot",
		"rlupi":        "/rlupi",
		"a/b c":        "/a_b_c",
		"":             "/unknown",
		"  spaced   ":  "/spaced",
		"cAFE1234beef": "/cAFE1234beef",
	}
	for in, want := range cases {
		if got := nameC(in); got != want {
			t.Errorf("nameC(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMTime(t *testing.T) {
	// RFC3339 with offset -> UTC, no offset
	if got := mTime("2026-06-17T08:47:00+02:00"); got != "2026-06-17T06:47:00" {
		t.Errorf("mTime offset = %q, want 2026-06-17T06:47:00", got)
	}
	// empty / zero-value timestamps -> ""
	for _, in := range []string{"", "   ", "0001-01-01T00:00:00Z", "not-a-time"} {
		if got := mTime(in); got != "" {
			t.Errorf("mTime(%q) = %q, want empty", in, got)
		}
	}
}

func TestIDs(t *testing.T) {
	if issueID(272) != "/i272" || prID(385) != "/pr385" {
		t.Errorf("id helpers: %s %s", issueID(272), prID(385))
	}
}
