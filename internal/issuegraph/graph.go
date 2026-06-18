// Package issuegraph builds an issue-dependency DAG from forge issues and
// renders it (Mermaid / DOT / interactive HTML). Building (this file) is kept
// separate from rendering (render.go). It reuses forge.SelectIssues / forge
// task-list parsing, so the graph, the forge linter, and sprint scoping agree
// on what an "epic closure" is.
package issuegraph

import (
	"context"
	"fmt"
	"sort"

	"github.com/robertolupi/botfam/internal/forge"
)

// Options selects the scope (forge.Scope) and which edge kinds to include.
type Options struct {
	forge.Scope
	WithMentions bool // also draw prose `#N` mention edges (dashed), not just task-list subtasks
}

// Graph is an issue DAG: nodes are issues in scope, edges are issue→issue.
type Graph struct {
	Nodes []Node
	Edges []Edge
}

// Node is one issue.
type Node struct {
	Number int
	Title  string
	State  string // "open" | "closed"
	IsEpic bool   // has at least one in-scope subtask child
}

// Edge is a directed issue→issue dependency.
type Edge struct {
	From, To int
	Kind     string // "subtask" (task-list `- [ ] #N`) | "mention" (prose `#N`)
}

// Build fetches the forge issues and extracts the DAG for the selected scope.
func Build(ctx context.Context, c *forge.Client, opt Options) (Graph, error) {
	issues, err := c.ListAllIssues(ctx)
	if err != nil {
		return Graph{}, fmt.Errorf("list issues: %w", err)
	}
	return build(issues, opt), nil
}

// build is the pure core (no forge I/O), exercised by tests. Pull requests are
// excluded (this is an issue graph); edges are kept only when both endpoints
// are in scope.
func build(issues []*forge.Issue, opt Options) Graph {
	var g Graph
	target := forge.SelectIssues(issues, opt.Scope) // nil => all
	inScope := func(n int) bool { return target == nil || target[n] }

	byNum := make(map[int]*forge.Issue, len(issues))
	for _, iss := range issues {
		byNum[iss.Number] = iss
	}

	nodes := map[int]*Node{}
	for _, iss := range issues {
		if iss.PullRequest != nil || !inScope(iss.Number) {
			continue
		}
		nodes[iss.Number] = &Node{Number: iss.Number, Title: iss.Title, State: iss.State}
	}

	seen := map[string]bool{}
	addEdge := func(from, to int, kind string) {
		if from == to || nodes[from] == nil || nodes[to] == nil {
			return
		}
		key := fmt.Sprintf("%d->%d:%s", from, to, kind)
		if seen[key] {
			return
		}
		seen[key] = true
		g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: kind})
		if kind == "subtask" {
			nodes[from].IsEpic = true
		}
	}
	for n := range nodes {
		iss := byNum[n]
		if iss == nil {
			continue
		}
		for child := range forge.TaskRefs(iss.Body) {
			addEdge(n, child, "subtask")
		}
		if opt.WithMentions {
			subtasks := forge.TaskRefs(iss.Body)
			for ref := range forge.MentionRefs(iss.Body) {
				if !subtasks[ref] { // a subtask is not also a bare mention
					addEdge(n, ref, "mention")
				}
			}
		}
	}

	for _, node := range nodes {
		g.Nodes = append(g.Nodes, *node)
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].Number < g.Nodes[j].Number })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		if g.Edges[i].To != g.Edges[j].To {
			return g.Edges[i].To < g.Edges[j].To
		}
		return g.Edges[i].Kind < g.Edges[j].Kind
	})
	return g
}
