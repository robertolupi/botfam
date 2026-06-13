package main

import (
	"io"
	"os"

	"github.com/robertolupi/botfam/internal/fam"
	"github.com/robertolupi/botfam/internal/mcp"
	"github.com/spf13/cobra"
)

// Execute builds the Cobra command tree and runs it against args (os.Args[1:]).
//
// The global --json/-j flag is honoured in any position (matching the legacy
// hand-rolled parser): it is stripped here and recorded via fam.SetJSONOutput
// before Cobra dispatches, so it works even for subcommands that pass their
// arguments straight through to the underlying handler.
func Execute(args []string) error {
	var rest []string
	jsonOut := false
	for _, a := range args {
		if a == "--json" || a == "-j" {
			jsonOut = true
			continue
		}
		rest = append(rest, a)
	}
	fam.SetJSONOutput(jsonOut)

	root := newRootCmd()
	root.SetArgs(rest)
	return root.Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "botfam",
		Short: "Coordinate a family of AI agents over IRC and a self-hosted forge",
		Long: `botfam is a single-binary CLI that lets a "family" of AI agents coordinate:
agents talk over a local IRC server, durable state lives on a self-hosted
Gitea/Forgejo forge, and consensus is enforced by native branch protection.

Run with no subcommand over a pipe (no TTY) to start the stdio MCP server.`,
		// We render errors ourselves in main() (legacy envelope), and don't want
		// Cobra to dump usage on every runtime error.
		SilenceUsage:  true,
		SilenceErrors: true,
		// No subcommand: serve MCP over stdio when piped, otherwise print help.
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTerminal(os.Stdin) && !isTerminal(os.Stdout) {
				return mcp.Serve(os.Stdin, os.Stdout, os.Stderr)
			}
			return cmd.Help()
		},
	}

	// Documented for `--help`; the actual value is parsed out-of-band in Execute
	// so it works in any position, including after a passthrough subcommand.
	root.PersistentFlags().BoolP("json", "j", false, "output results as structured JSON lines")

	// Keep the command surface lean — no generated completion subcommand.
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(
		newVersionCmd(),
		newServeCmd(),
		newWorktreeCmd(),
		newSetupCmd(),
		newNewfamCmd(),
		newSessionCmd(),
		fam.NewVerifyCmd(),
		fam.NewAgentDocsCmd(),
		newIrcClientCmd(),
		fam.NewIrcWaitCmd(),
		fam.NewForgeWaitCmd(),
		newExternalReviewCmd(),
		fam.NewScribeCmd(),
		newIrclog2SessionsCmd(),
	)
	return root
}

// passthrough wraps an existing fam handler as a Cobra leaf command. Flag
// parsing is disabled so the handler's own argument parser receives the raw
// arguments unchanged — this keeps behaviour (and the handlers' --help output)
// identical while migrating the dispatch layer to Cobra. Per-command flags can
// be promoted to pflags incrementally without touching the handlers.
func passthrough(use, short string, handler func([]string, io.Writer) error) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return handler(args, cmd.OutOrStdout())
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version/SHA",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := io.WriteString(cmd.OutOrStdout(), fam.GetVersion()+"\n")
			return err
		},
	}
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the stdio MCP server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcp.Serve(os.Stdin, os.Stdout, os.Stderr)
		},
	}
}

func newWorktreeCmd() *cobra.Command {
	return passthrough("worktree <init|sync|register> [args]",
		"Manage agent git worktrees", fam.WorktreeCmd)
}

func newSetupCmd() *cobra.Command {
	return passthrough("setup <project> --agents alice,bob [--force]",
		"Configure an existing botfam project", fam.Setup)
}

func newNewfamCmd() *cobra.Command {
	return passthrough("newfam <project> --agents alice,bob",
		"Initialize a new botfam project (worktrees, registry, docs)", fam.NewfamCmd)
}

func newSessionCmd() *cobra.Command {
	return passthrough("session <subcommand>",
		"Manage coordination sessions (open/list/render/close/extract)", fam.SessionCmd)
}

func newIrcClientCmd() *cobra.Command {
	return passthrough("irc-client <nick> [flags]",
		"Run the FIFO-driven IRC client", fam.IrcClientCmd)
}

func newExternalReviewCmd() *cobra.Command {
	return passthrough("external-review [flags] [MATERIAL...]",
		"Fan a review prompt across one or more LLMs", fam.ExternalReviewCmd)
}

func newIrclog2SessionsCmd() *cobra.Command {
	return passthrough("irclog2sessions <chat.log>... [flags]",
		"Compile IRC channel logs into session transcripts", fam.IrcLog2SessionsCmd)
}

func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
