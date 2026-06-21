package cli

import (
	"io"
	"os"

	"github.com/robertolupi/botfam/internal/cli/ops"
	"github.com/robertolupi/botfam/internal/cli/review"
	"github.com/robertolupi/botfam/internal/cli/setup"
	"github.com/robertolupi/botfam/internal/mcp"
	"github.com/spf13/cobra"
)

const (
	groupSetup  = "setup"
	groupOps    = "ops"
	groupReview = "review"
	groupServer = "server"
)

// NewRootCmd builds the full `botfam` command tree: the CLI command builders
// plus the MCP serve/mcp subcommands. cmd/botfam is a thin wrapper over this.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "botfam",
		Short: "Coordinate a family of AI agents over Gitea/Forgejo",
		Long: `botfam is a single-binary CLI that lets a "family" of AI agents coordinate:
durable state lives on a self-hosted Gitea/Forgejo forge, and consensus
is enforced by native branch protection.

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

	// Keep the command surface lean — no generated completion subcommand.
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddGroup(
		&cobra.Group{ID: groupSetup, Title: "Setup & provisioning:"},
		&cobra.Group{ID: groupOps, Title: "Runtime & ops:"},
		&cobra.Group{ID: groupReview, Title: "Review & analysis:"},
		&cobra.Group{ID: groupServer, Title: "Server:"},
	)

	addTo := func(groupID string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = groupID
			root.AddCommand(c)
		}
	}

	addTo(groupSetup,
		setup.NewNewfamCmd(),
		setup.NewCloneCmd(),
		setup.NewInitCmd(),
		setup.NewSetupCmd(),
		setup.NewWorktreeCmd(),
		setup.NewMintCmd(),
		setup.NewCredentialCmd(),
		setup.NewDoctorCmd(),
		setup.NewAgentDocsCmd(),
	)
	addTo(groupOps,
		ops.NewWaitCmd(),
		ops.NewForgeWaitCmd(),
		ops.NewSessionCmd(),
		ops.NewRunCmd(),
		ops.NewWhoamiCmd(),
		ops.NewMemoryCmd(),
		ops.NewSprintCmd(),
	)
	addTo(groupReview,
		review.NewExternalReviewCmd(),
		review.NewMetaReviewCmd(),
		review.NewVerifyCmd(),
		review.NewMangleCmd(),
		review.NewForgeCmd(),
	)
	addTo(groupServer,
		newServeCmd(),
		mcp.NewMcpCmd(),
	)

	// version has no group — floats to "Additional Commands" alongside help.
	root.AddCommand(newVersionCmd())

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version/SHA",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := io.WriteString(cmd.OutOrStdout(), GetVersion()+"\n")
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

func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
