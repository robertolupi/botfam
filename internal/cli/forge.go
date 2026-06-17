package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/issuegraph"
	"github.com/robertolupi/botfam/internal/mangle"
	"github.com/spf13/cobra"
)

// NewForgeCmd builds `botfam forge` — operations over the forge itself:
// `forge lint` (process-hazard linter over a snapshot) and `forge graph`
// (issue-dependency DAG as Mermaid/DOT). Distinct from `botfam mangle lint`,
// which lints Mangle rule *source* for authoring pitfalls.
func NewForgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Operations over the forge (lint, issue-dependency graph)",
	}
	cmd.AddCommand(newForgeLintCmd(), newForgeGraphCmd())
	return cmd
}

func newForgeGraphCmd() *cobra.Command {
	var format, out string
	var withMentions bool
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Render the issue-dependency DAG (Mermaid, Graphviz DOT, or interactive HTML)",
		Long: `Extract the issue-dependency graph for the selected scope and render it as
Mermaid (default; renders in the wiki/Obsidian), Graphviz DOT, or a
self-contained interactive d3.js HTML page (zoom/drag, click a node to open the
issue, toggles to hide closed/isolated nodes or show only epics + children).

Nodes are issues; solid edges are task-list subtasks (- [ ] #N), the same
epic-decomposition edges 'forge lint' and sprint scoping use. --with-mentions
adds dashed prose #N edges. Closed issues are greyed; epics get a bold border.

  botfam forge graph --epic 339                        # Mermaid to stdout
  botfam forge graph --all --format html --out g.html  # interactive d3 page
  botfam forge graph --milestone M7 --format dot | dot -Tsvg > m7.svg`,
	}
	build := exportSelectors(cmd)
	cmd.RunE = RunWithFamCtx(func(ctx context.Context, cmd *cobra.Command, args []string) error {
		switch format {
		case "mermaid", "dot", "html":
		default:
			return fmt.Errorf("--format must be 'mermaid', 'dot', or 'html', got %q", format)
		}
		sc, err := build()
		if err != nil {
			return err
		}
		fctx, _ := famctx.FromContext(ctx)
		c, err := forge.NewClientFromCtx(fctx)
		if err != nil {
			return err
		}
		g, err := issuegraph.Build(c, issuegraph.Options{Scope: sc, WithMentions: withMentions})
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if out != "" {
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			defer f.Close()
			w = f
		}
		switch format {
		case "dot":
			err = issuegraph.RenderDOT(g, w)
		case "html":
			issueBase := strings.TrimSuffix(c.BaseURL, "/") + "/" + c.Owner + "/" + c.Repo + "/issues/"
			err = issuegraph.RenderHTML(g, issueBase, w)
		default:
			err = issuegraph.RenderMermaid(g, w)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%d issues, %d edges\n", len(g.Nodes), len(g.Edges))
		return nil
	})
	cmd.Flags().StringVar(&format, "format", "mermaid", "output format: mermaid | dot | html")
	cmd.Flags().StringVar(&out, "out", "", "write to FILE (default stdout)")
	cmd.Flags().BoolVar(&withMentions, "with-mentions", false, "also draw dashed prose #N mention edges")
	return cmd
}

func newForgeLintCmd() *cobra.Command {
	var maxViolations int
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Lint the forge: run curated process-hazard invariants over a snapshot",
		Long: `Materialize a forge snapshot for the selected scope and evaluate the curated
rule set (misattributed work, double-close, merged-but-open). Exits non-zero
when the violation count exceeds --max (default 0) — usable as a CI gate.`,
	}
	build := exportSelectors(cmd)
	cmd.RunE = RunWithFamCtx(func(ctx context.Context, cmd *cobra.Command, args []string) error {
		sc, err := build()
		if err != nil {
			return err
		}
		fctx, _ := famctx.FromContext(ctx)
		c, err := forge.NewClientFromCtx(fctx)
		if err != nil {
			return err
		}
		results, ls, err := mangle.Lint(c, mangle.ExportOptions{Scope: sc, WithCommits: true}, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		total := 0
		for _, r := range results {
			total += len(r.Rows)
		}
		out := cmd.OutOrStdout()
		for _, r := range results {
			fmt.Fprintf(out, "== %s: %d ==\n", r.Predicate, len(r.Rows))
			for _, row := range r.Rows {
				fmt.Fprintf(out, "  %s\n", row)
			}
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"%d violations (acquire %s, eval %s)\n",
			total, ls.Export.Duration.Round(time.Millisecond), ls.EvalTime.Round(time.Millisecond))
		if total > maxViolations {
			return fmt.Errorf("forge-lint: %d violations exceed --max=%d", total, maxViolations)
		}
		return nil
	})
	cmd.Flags().IntVar(&maxViolations, "max", 0, "exit non-zero when violations exceed this count")
	return cmd
}
