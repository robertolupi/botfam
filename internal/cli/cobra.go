package cli

import (
	"context"
	"io"
	"os"

	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/spf13/cobra"
)

// runCobra wires an args slice and an output writer into a freshly-built Cobra
// command and executes it. It is the bridge used by the legacy
// XxxCmd(args, out) entry points — retained for the unit tests and the MCP
// layer — now that argument parsing lives in Cobra. Errors and usage are
// silenced on the commands themselves so the caller (cmd/botfam/main) renders
// the legacy error envelope.
func runCobra(c *cobra.Command, args []string, out io.Writer) error {
	c.SetArgs(args)
	c.SetOut(out)
	c.SetErr(out)
	return c.Execute()
}

// RunWithFamCtx wraps a cobra RunE: it calls famctx.WithFamCtx to resolve and
// embed the agent runtime context into the command's context.Context, then
// passes the enriched context to fn. Commands with an explicit --work-dir flag
// should use RunWithFamCtxDir instead.
func RunWithFamCtx(fn func(context.Context, *cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		ctx, err := famctx.WithFamCtx(cmd.Context(), wd)
		if err != nil {
			return err
		}
		return fn(ctx, cmd, args)
	}
}

// RunWithFamCtxDir is like RunWithFamCtx but reads the work directory from
// workDir at call time (pointer evaluated lazily after flag parsing).
func RunWithFamCtxDir(workDir *string, fn func(context.Context, *cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		wd := *workDir
		if wd == "" {
			var err error
			wd, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		ctx, err := famctx.WithFamCtx(cmd.Context(), wd)
		if err != nil {
			return err
		}
		return fn(ctx, cmd, args)
	}
}
