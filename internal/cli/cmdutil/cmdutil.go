// Package cmdutil provides shared helpers for botfam CLI subpackages.
package cmdutil

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/spf13/cobra"
)

// RunCobra wires an args slice and an output writer into a freshly-built Cobra
// command and executes it. It is the bridge used by the legacy
// XxxCmd(args, out) entry points — retained for the unit tests and the MCP
// layer — now that argument parsing lives in Cobra. Errors and usage are
// silenced on the commands themselves so the caller (cmd/botfam/main) renders
// the legacy error envelope.
func RunCobra(c *cobra.Command, args []string, out io.Writer) error {
	c.SetArgs(args)
	c.SetOut(out)
	c.SetErr(out)
	return c.Execute()
}

// Command access categories
//
// Every botfam CLI command has an audience. The DEFAULT is agents+human
// (fail-open): a command should be runnable by a human operator unless there is
// a concrete reason to restrict it. We restrict deliberately and later — not by
// accident of which context helper a command happened to use.
//
//   - agents+human (default): RunWithRegistryCtx. Resolves the forge registry via
//     the non-strict ModeRegistry, so the command runs in agent ([agent.<name>]),
//     human ([user.<name>]), and base checkouts alike, still failing loudly when
//     no [repo.<k>] stanza matches. Use for read / analysis / authoring tools
//     (forge lint/graph, external-review, mangle lint, session-extract,
//     metareview, memory, ...).
//   - agents-only: RunWithFamCtx. The strict agent-runtime gate
//     (famctx.ResolveAgentRuntime) that refuses human/base checkouts. Reserve for
//     the agent runtime itself and commands that act AS the agent identity.
//   - human-only: no wrapper yet — add one if a command ever warrants it.
//
// Commands that need no fam context at all (doctor/setup/whoami/version) use
// neither helper.

// RunWithFamCtx wraps a cobra RunE with the strict agent-runtime context
// (famctx.WithFamCtx): the **agents-only** category. It refuses human/base
// checkouts, so use it only for commands that act as the agent identity — most
// commands want the agents+human default (RunWithRegistryCtx) instead.
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

// RunWithRegistryCtx is like RunWithFamCtx but uses the non-strict registry
// resolver (famctx.WithRegistryCtx) instead of the agent-runtime gate. Use it
// for general forge/utility commands that should run in human ([user.<name>])
// and base checkouts, not only agent worktrees — they still fail loudly when no
// [repo.<k>] stanza matches.
func RunWithRegistryCtx(fn func(context.Context, *cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		ctx, err := famctx.WithRegistryCtx(cmd.Context(), wd)
		if err != nil {
			return err
		}
		return fn(ctx, cmd, args)
	}
}

// Unique returns a deduplicated copy of xs, preserving order.
func Unique(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// SplitCSV splits a comma-separated string into trimmed, non-empty parts.
func SplitCSV(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
