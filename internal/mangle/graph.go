package mangle

// Issue-dependency DAG extraction and rendering. Reuses the same issue
// selection (selectIssues) and task-list edge parsing (taskRefRe) as the
// Mangle exporter, so `botfam forge graph` and `botfam forge lint` agree on
// what an "epic closure" is. Backs wiki CattleEpicLedger / sprint scoping.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/robertolupi/botfam/internal/forge"
)

// GraphOptions selects the scope (embeds the exporter's selectors) and tunes
// which edge kinds to include.
type GraphOptions struct {
	ExportOptions
	WithMentions bool // also draw prose `#N` mention edges (dashed), not just task-list subtasks
}

// Graph is an issue DAG: nodes are issues in scope, edges are issue→issue.
type Graph struct {
	Nodes []GraphNode
	Edges []GraphEdge
}

// GraphNode is one issue.
type GraphNode struct {
	Number int
	Title  string
	State  string // "open" | "closed"
	IsEpic bool   // has at least one in-scope subtask child
}

// GraphEdge is a directed issue→issue dependency.
type GraphEdge struct {
	From, To int
	Kind     string // "subtask" (task-list `- [ ] #N`) | "mention" (prose `#N`)
}

// BuildGraph extracts the issue DAG for the selected scope. Pull requests are
// excluded (this is an issue graph); edges are kept only when both endpoints
// are in scope.
func BuildGraph(c *forge.Client, opt GraphOptions) (Graph, error) {
	issues, err := c.ListAllIssues()
	if err != nil {
		return Graph{}, fmt.Errorf("list issues: %w", err)
	}
	return buildGraph(issues, opt), nil
}

// buildGraph is the pure core of BuildGraph (no forge I/O), exercised by tests.
func buildGraph(issues []*forge.Issue, opt GraphOptions) Graph {
	var g Graph
	target := selectIssues(issues, opt.ExportOptions) // nil => all
	inScope := func(n int) bool { return target == nil || target[n] }

	byNum := make(map[int]*forge.Issue, len(issues))
	for _, iss := range issues {
		byNum[iss.Number] = iss
	}

	// Nodes: in-scope issues that are not PRs.
	nodes := map[int]*GraphNode{}
	for _, iss := range issues {
		if iss.PullRequest != nil || !inScope(iss.Number) {
			continue
		}
		nodes[iss.Number] = &GraphNode{Number: iss.Number, Title: iss.Title, State: iss.State}
	}

	// Edges: subtask (task-list) always; mention (prose #N) optionally. Both
	// endpoints must be nodes.
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
		g.Edges = append(g.Edges, GraphEdge{From: from, To: to, Kind: kind})
		if kind == "subtask" {
			nodes[from].IsEpic = true
		}
	}
	for n := range nodes {
		iss := byNum[n]
		if iss == nil {
			continue
		}
		for child := range refs(taskRefRe, iss.Body) {
			addEdge(n, child, "subtask")
		}
		if opt.WithMentions {
			subtasks := refs(taskRefRe, iss.Body)
			for ref := range refs(mentionRe, iss.Body) {
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

// truncTitle clamps a title to n runes for legibility in graph labels.
func truncTitle(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// RenderMermaid writes the graph as a Mermaid `graph TD` block. Closed issues
// and epics get CSS classes; mention edges are dashed.
func RenderMermaid(g Graph, w io.Writer) error {
	b := &strings.Builder{}
	b.WriteString("graph TD\n")
	for _, n := range g.Nodes {
		label := fmt.Sprintf("#%d %s", n.Number, truncTitle(n.Title, 48))
		label = strings.NewReplacer(`"`, "'", "[", "(", "]", ")").Replace(label)
		fmt.Fprintf(b, "  i%d[\"%s\"]\n", n.Number, label)
	}
	for _, e := range g.Edges {
		if e.Kind == "mention" {
			fmt.Fprintf(b, "  i%d -.-> i%d\n", e.From, e.To)
		} else {
			fmt.Fprintf(b, "  i%d --> i%d\n", e.From, e.To)
		}
	}
	// Styling: closed = greyed, epic = bold border.
	var closed, epics []string
	for _, n := range g.Nodes {
		if n.State == "closed" {
			closed = append(closed, fmt.Sprintf("i%d", n.Number))
		}
		if n.IsEpic {
			epics = append(epics, fmt.Sprintf("i%d", n.Number))
		}
	}
	b.WriteString("  classDef closed fill:#eee,stroke:#999,color:#777;\n")
	b.WriteString("  classDef epic stroke-width:3px,stroke:#36c;\n")
	if len(closed) > 0 {
		fmt.Fprintf(b, "  class %s closed;\n", strings.Join(closed, ","))
	}
	if len(epics) > 0 {
		fmt.Fprintf(b, "  class %s epic;\n", strings.Join(epics, ","))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// RenderDOT writes the graph as Graphviz DOT (`dot -Tsvg`). Closed issues are
// filled grey; mention edges are dashed.
func RenderDOT(g Graph, w io.Writer) error {
	b := &strings.Builder{}
	b.WriteString("digraph issues {\n  rankdir=TB;\n  node [shape=box, style=rounded, fontname=\"sans-serif\"];\n")
	for _, n := range g.Nodes {
		label := fmt.Sprintf("#%d %s", n.Number, truncTitle(n.Title, 48))
		label = strings.ReplaceAll(label, `"`, `\"`)
		attrs := fmt.Sprintf("label=\"%s\"", label)
		if n.State == "closed" {
			attrs += ", style=\"rounded,filled\", fillcolor=\"#eeeeee\", fontcolor=\"#777777\""
		}
		if n.IsEpic {
			attrs += ", penwidth=2, color=\"#3366cc\""
		}
		fmt.Fprintf(b, "  i%d [%s];\n", n.Number, attrs)
	}
	for _, e := range g.Edges {
		if e.Kind == "mention" {
			fmt.Fprintf(b, "  i%d -> i%d [style=dashed, color=\"#999999\"];\n", e.From, e.To)
		} else {
			fmt.Fprintf(b, "  i%d -> i%d;\n", e.From, e.To)
		}
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}
