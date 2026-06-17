package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/robertolupi/botfam/internal/mangle"
	"github.com/spf13/cobra"
)

// NewForgeCmd builds `botfam forge` — operations over the forge itself.
// Currently: `forge lint`, the process-hazard linter (run curated invariants
// over a forge snapshot). Distinct from `botfam mangle lint`, which lints
// Mangle rule *source* for authoring pitfalls.
func NewForgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Operations over the forge (process-hazard linting)",
	}
	cmd.AddCommand(newForgeLintCmd())
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
