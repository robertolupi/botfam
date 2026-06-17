package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/robertolupi/botfam/internal/famconfig"
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
	cmd.AddCommand(newMangleExportCmd(), newMangleEvalCmd(), newMangleLintCmd())
	return cmd
}

func newMangleLintCmd() *cobra.Command {
	var maxViolations int
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Run the curated forge-linter over a forge snapshot (process-hazard detection)",
		Long: `Materialize a forge snapshot for the selected scope and evaluate the curated
rule set (misattributed work, double-close, merged-but-open). Exits non-zero
when the violation count exceeds --max (default 0) — usable as a CI gate.`,
	}
	build := exportSelectors(cmd, nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opt, err := build()
		if err != nil {
			return err
		}
		c, err := newForgeClient()
		if err != nil {
			return err
		}
		results, ls, err := mangle.Lint(c, opt, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		total := 0
		for _, r := range results {
			total += len(r.Rows)
		}
		if IsJSONOutput() {
			obj := map[string]any{"total": total, "rules": results,
				"acquire_ms": ls.Export.Duration.Milliseconds(), "eval_ms": ls.EvalTime.Milliseconds()}
			b, _ := json.MarshalIndent(obj, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
		} else {
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
		}
		if total > maxViolations {
			return fmt.Errorf("forge-lint: %d violations exceed --max=%d", total, maxViolations)
		}
		return nil
	}
	cmd.Flags().IntVar(&maxViolations, "max", 0, "exit non-zero when violations exceed this count")
	return cmd
}

func newForgeClient() (*forge.Client, error) {
	actor := ""
	if id, err := (famconfig.GitResolver{}).ResolveIdentity("."); err == nil {
		actor = id.Actor
	}
	return forge.NewClient(".", actor)
}

// exportSelectors adds the scope flags shared by `export` and `eval --all` and
// returns a builder that validates them into ExportOptions.
func exportSelectors(cmd *cobra.Command, withCommits *bool) func() (mangle.ExportOptions, error) {
	var all bool
	var milestone, label string
	var epic int
	cmd.Flags().BoolVar(&all, "all", false, "export the full forge history")
	cmd.Flags().StringVar(&milestone, "milestone", "", "only issues in this milestone (by title)")
	cmd.Flags().StringVar(&label, "label", "", "only issues carrying this label")
	cmd.Flags().IntVar(&epic, "epic", 0, "only this issue and its transitive #N closure")
	return func() (mangle.ExportOptions, error) {
		n := 0
		for _, set := range []bool{all, milestone != "", label != "", epic > 0} {
			if set {
				n++
			}
		}
		if n == 0 {
			return mangle.ExportOptions{}, fmt.Errorf("specify a scope: --all | --milestone | --label | --epic")
		}
		if n > 1 {
			return mangle.ExportOptions{}, fmt.Errorf("scope selectors are mutually exclusive")
		}
		return mangle.ExportOptions{
			WithCommits: withCommits == nil || *withCommits,
			Milestone:   milestone,
			Label:       label,
			Epic:        epic,
		}, nil
	}
}

func newMangleExportCmd() *cobra.Command {
	var noCommits bool
	var store string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write forge history as Mangle facts",
	}
	withCommits := true
	build := exportSelectors(cmd, &withCommits)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opt, err := build()
		if err != nil {
			return err
		}
		opt.WithCommits = !noCommits
		c, err := newForgeClient()
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
		st, err := mangle.Export(c, opt, w)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"exported %d issues, %d pulls, %d commits in %s\n",
			st.Issues, st.Pulls, st.Commits, st.Duration.Round(time.Millisecond))
		return nil
	}
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
	build := exportSelectors(cmd, nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if ruleFile == "" {
			return fmt.Errorf("--file RULES.mg is required")
		}
		store := fromStore
		if store == "" {
			// no prior store: a scope selector must materialize one first
			opt, err := build()
			if err != nil {
				return err
			}
			c, err := newForgeClient()
			if err != nil {
				return err
			}
			f, err := os.CreateTemp("", "botfam-mangle-*.mg")
			if err != nil {
				return err
			}
			defer os.Remove(f.Name())
			st, err := mangle.Export(c, opt, f)
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
	}
	cmd.Flags().StringVar(&fromStore, "from-store", "", "evaluate against a previously exported fact file")
	cmd.Flags().StringVar(&ruleFile, "file", "", "Mangle rule file to evaluate")
	cmd.Flags().StringVar(&prefix, "prefix", "violation", "query head predicates with this name prefix")
	return cmd
}
