package issuegraph

import (
	"fmt"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
)

func iss(n int, state, body string) *forge.Issue {
	return &forge.Issue{Index: int64(n), State: forge.StateType(state), Title: "issue " + body, Body: body}
}

// fixture: #1 epic decomposes into #2, #3 (task list); #2 mentions #4 in prose;
// #5 is a PR (excluded); #3 is closed.
func graphFixture() []*forge.Issue {
	epic := iss(1, "open", "Epic\n- [ ] #2\n- [x] #3\n")
	child2 := iss(2, "open", "see #4 for context")
	child3 := iss(3, "closed", "done")
	other4 := iss(4, "open", "standalone")
	pr5 := iss(5, "open", "a PR that mentions #1")
	pr5.PullRequest = &forge.PullRequestMeta{}
	return []*forge.Issue{epic, child2, child3, other4, pr5}
}

func TestBuildSubtaskEdgesAndPRExclusion(t *testing.T) {
	g := build(graphFixture(), Options{})

	for _, n := range g.Nodes {
		if n.Number == 5 {
			t.Fatalf("PR #5 should be excluded from the issue graph")
		}
	}
	if len(g.Nodes) != 4 {
		t.Errorf("expected 4 issue nodes (1-4), got %d", len(g.Nodes))
	}

	want := map[string]bool{"1->2:subtask": true, "1->3:subtask": true}
	got := map[string]bool{}
	for _, e := range g.Edges {
		got[fmt.Sprintf("%d->%d:%s", e.From, e.To, e.Kind)] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing edge %s; got %v", k, got)
		}
	}
	if len(g.Edges) != 2 {
		t.Errorf("expected 2 subtask edges, got %d: %v", len(g.Edges), got)
	}

	for _, n := range g.Nodes {
		if n.Number == 1 && !n.IsEpic {
			t.Errorf("#1 should be flagged IsEpic")
		}
		if n.Number == 3 && n.State != "closed" {
			t.Errorf("#3 should be closed")
		}
	}
}

func TestBuildWithMentions(t *testing.T) {
	g := build(graphFixture(), Options{WithMentions: true})
	var hasMention bool
	for _, e := range g.Edges {
		if e.From == 2 && e.To == 4 && e.Kind == "mention" {
			hasMention = true
		}
	}
	if !hasMention {
		t.Errorf("expected a dashed mention edge 2->4 with WithMentions; edges: %v", g.Edges)
	}
}

func TestBuildEpicScope(t *testing.T) {
	// Epic 1 closes over the task list → {1,2,3}; #4 falls out of scope.
	g := build(graphFixture(), Options{Scope: forge.Scope{Epic: 1}})
	for _, n := range g.Nodes {
		if n.Number == 4 {
			t.Errorf("#4 is not in epic #1's closure and should be excluded")
		}
	}
	if len(g.Nodes) != 3 {
		t.Errorf("epic #1 closure should be 3 issues, got %d", len(g.Nodes))
	}
}

func TestRenderMermaidAndDOT(t *testing.T) {
	g := build(graphFixture(), Options{})

	var mm strings.Builder
	if err := RenderMermaid(g, &mm); err != nil {
		t.Fatal(err)
	}
	m := mm.String()
	for _, want := range []string{"graph TD", `i1["#1`, "i1 --> i2", "class i3 closed", "class i1 epic"} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid missing %q:\n%s", want, m)
		}
	}

	var dot strings.Builder
	if err := RenderDOT(g, &dot); err != nil {
		t.Fatal(err)
	}
	d := dot.String()
	for _, want := range []string{"digraph issues", "i1 -> i2;", "fillcolor=\"#eeeeee\""} {
		if !strings.Contains(d, want) {
			t.Errorf("dot missing %q:\n%s", want, d)
		}
	}
}

func TestRenderHTML(t *testing.T) {
	g := build(graphFixture(), Options{})
	var h strings.Builder
	if err := RenderHTML(g, "http://gitea:3000/botfam/botfam/issues/", &h); err != nil {
		t.Fatal(err)
	}
	out := h.String()
	for _, want := range []string{
		"d3js.org/d3.v7", "ISSUE_BASE=\"http://gitea:3000/botfam/botfam/issues/\"",
		"\"id\":\"i1\"", "\"source\":\"i1\",\"target\":\"i2\"", "forceSimulation",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q", want)
		}
	}
	if strings.Contains(out, "__NODES__") || strings.Contains(out, "__LINKS__") || strings.Contains(out, "__ISSUEBASE__") {
		t.Errorf("unsubstituted placeholder remains in HTML output")
	}
}
