package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mangle"
	"github.com/spf13/cobra"
)

// NewMangleCmd builds `botfam mangle` — export forge history as Mangle
// (temporal Datalog) facts and evaluate rule files against them. Backs the
// Cattle invariants/hazards work (wiki CattleInvariantsAsLogic). Experimental.
func NewMangleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mangle",
		Short: "Export forge history as Datalog facts and evaluate rule files (experimental)",
		Long: `Materialize botfam forge history as Mangle (temporal Datalog) facts and
evaluate rule files against them — the engine for Cattle invariants, crashpoints
and hazard detection (wiki CattleInvariantsAsLogic).

  botfam mangle export --all --store FILE         # forge -> facts
  botfam mangle eval --from-store FILE --file RULES.mg
  botfam mangle eval --all --file RULES.mg        # export to a temp store, then eval

Materialize-then-evaluate: facts are pulled once into a snapshot, never resolved
lazily during evaluation (the engine re-scans relations, so lazy forge calls
would multiply RPC).`,
	}
	cmd.AddCommand(newMangleExportCmd(), newMangleEvalCmd())
	return cmd
}

// exportSelectors adds the scope flags shared by `mangle export/eval`,
// `forge lint`, and `forge graph`, and returns a builder that validates them
// into a forge.Scope.
func exportSelectors(cmd *cobra.Command) func() (forge.Scope, error) {
	var all bool
	var milestone, label string
	var epic int
	cmd.Flags().BoolVar(&all, "all", false, "the full forge history")
	cmd.Flags().StringVar(&milestone, "milestone", "", "only issues in this milestone (by title)")
	cmd.Flags().StringVar(&label, "label", "", "only issues carrying this label")
	cmd.Flags().IntVar(&epic, "epic", 0, "only this issue and its transitive #N closure")
	return func() (forge.Scope, error) {
		n := 0
		for _, set := range []bool{all, milestone != "", label != "", epic > 0} {
			if set {
				n++
			}
		}
		if n == 0 {
			return forge.Scope{}, fmt.Errorf("specify a scope: --all | --milestone | --label | --epic")
		}
		if n > 1 {
			return forge.Scope{}, fmt.Errorf("scope selectors are mutually exclusive")
		}
		return forge.Scope{All: all, Milestone: milestone, Label: label, Epic: epic}, nil
	}
}

func newMangleExportCmd() *cobra.Command {
	var noCommits bool
	var store string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write forge history as Mangle facts",
	}
	build := exportSelectors(cmd)
	cmd.RunE = WithFamCtx(func(cmd *cobra.Command, args []string, fctx famctx.Context) error {
		sc, err := build()
		if err != nil {
			return err
		}
		c, err := forge.NewClientFromCtx(fctx)
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if store != "" {
			f, err := os.Create(store)
			if err != nil {
				return err
			}
			defer f.Close()
			w = f
		}
		st, err := mangle.Export(c, mangle.ExportOptions{Scope: sc, WithCommits: !noCommits}, w)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"exported %d issues, %d pulls, %d commits in %s\n",
			st.Issues, st.Pulls, st.Commits, st.Duration.Round(time.Millisecond))
		return nil
	})
	cmd.Flags().BoolVar(&noCommits, "no-commits", false, "skip per-PR commit/author facts (faster)")
	cmd.Flags().StringVar(&store, "store", "", "write facts to FILE (default stdout)")
	return cmd
}

func newMangleEvalCmd() *cobra.Command {
	var fromStore, ruleFile, prefix string
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluate a Mangle rule file against forge facts",
	}
	build := exportSelectors(cmd)
	cmd.RunE = WithFamCtx(func(cmd *cobra.Command, args []string, fctx famctx.Context) error {
		if ruleFile == "" {
			return fmt.Errorf("--file RULES.mg is required")
		}
		store := fromStore
		if store == "" {
			// no prior store: a scope selector must materialize one first
			sc, err := build()
			if err != nil {
				return err
			}
			c, err := forge.NewClientFromCtx(fctx)
			if err != nil {
				return err
			}
			f, err := os.CreateTemp("", "botfam-mangle-*.mg")
			if err != nil {
				return err
			}
			defer os.Remove(f.Name())
			st, err := mangle.Export(c, mangle.ExportOptions{Scope: sc, WithCommits: true}, f)
			f.Close()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "materialized %d issues, %d pulls, %d commits in %s\n",
				st.Issues, st.Pulls, st.Commits, st.Duration.Round(time.Millisecond))
			store = f.Name()
		}
		results, dur, err := mangle.Eval(ruleFile, store, prefix, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		total := 0
		for _, r := range results {
			fmt.Fprintf(out, "== %s: %d ==\n", r.Predicate, len(r.Rows))
			for _, row := range r.Rows {
				fmt.Fprintf(out, "  %s\n", row)
			}
			total += len(r.Rows)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "evaluated in %s; %d total rows across %d predicates\n",
			dur.Round(time.Millisecond), total, len(results))
		return nil
	})
	cmd.Flags().StringVar(&fromStore, "from-store", "", "evaluate against a previously exported fact file")
	cmd.Flags().StringVar(&ruleFile, "file", "", "Mangle rule file to evaluate")
	cmd.Flags().StringVar(&prefix, "prefix", "violation", "query head predicates with this name prefix")
	return cmd
}
