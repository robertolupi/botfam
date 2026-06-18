package forge

import (
	"reflect"
	"sort"
	"testing"
)

func scopeKeys(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func TestIssueRefs(t *testing.T) {
	body := "Fixes #12 and resolves #34. See also #56.\n- [ ] #7\n- [x] #8\nprose #9"
	if got := scopeKeys(ClosesRefs(body)); !reflect.DeepEqual(got, []int{12, 34}) {
		t.Errorf("ClosesRefs = %v, want [12 34]", got)
	}
	if got := scopeKeys(TaskRefs(body)); !reflect.DeepEqual(got, []int{7, 8}) {
		t.Errorf("TaskRefs = %v, want [7 8]", got)
	}
	if got := scopeKeys(MentionRefs(body)); !reflect.DeepEqual(got, []int{7, 8, 9, 12, 34, 56}) {
		t.Errorf("MentionRefs = %v, want all", got)
	}
}

func TestSelectEpicClosure(t *testing.T) {
	iss := func(n int, body string) *Issue { return &Issue{Index: int64(n), Body: body} }
	issues := []*Issue{
		iss(1, "epic\n- [ ] #2\n- [ ] #3\nsee #99 in prose"), // prose #99 must NOT be followed
		iss(2, "- [x] #4"),
		iss(3, ""),
		iss(4, ""),
		iss(99, "unrelated"),
	}
	got := scopeKeys(SelectIssues(issues, Scope{Epic: 1}))
	if !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Errorf("epic closure = %v, want [1 2 3 4]", got)
	}
}

func TestSelectMilestoneAndLabel(t *testing.T) {
	mk := func(n int, ms string, label string) *Issue {
		i := &Issue{Index: int64(n)}
		if ms != "" {
			i.Milestone = &Milestone{Title: ms}
		}
		if label != "" {
			i.Labels = []*Label{{Name: label}}
		}
		return i
	}
	issues := []*Issue{mk(1, "M7", ""), mk(2, "M7", "bug"), mk(3, "", "bug")}
	if got := scopeKeys(SelectIssues(issues, Scope{Milestone: "M7"})); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("milestone = %v, want [1 2]", got)
	}
	if got := scopeKeys(SelectIssues(issues, Scope{Label: "bug"})); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Errorf("label = %v, want [2 3]", got)
	}
	if SelectIssues(issues, Scope{}) != nil {
		t.Errorf("no selector should return nil (all)")
	}
}
