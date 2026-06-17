package cli

import (
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

// WithFamCtx wraps a RunE that requires the resolved agent runtime context.
// It resolves once from os.Getwd() and injects the result; commands with an
// explicit --work-dir flag should use WithFamCtxDir instead.
func WithFamCtx(fn func(*cobra.Command, []string, famctx.Context) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		ctx, err := famctx.ResolveAgentRuntime(wd)
		if err != nil {
			return err
		}
		return fn(cmd, args, ctx)
	}
}

// WithFamCtxDir is like WithFamCtx but reads the work directory from workDir
// at call time (useful for commands that expose a --work-dir flag; the pointer
// is evaluated lazily so flag parsing has already run).
func WithFamCtxDir(workDir *string, fn func(*cobra.Command, []string, famctx.Context) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		wd := *workDir
		if wd == "" {
			var err error
			wd, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		ctx, err := famctx.ResolveAgentRuntime(wd)
		if err != nil {
			return err
		}
		return fn(cmd, args, ctx)
	}
}
