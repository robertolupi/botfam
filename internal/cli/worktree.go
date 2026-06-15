package cli

import (
	"io"
	"path/filepath"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/provision"
	"github.com/spf13/cobra"
)

// The worktree lifecycle operations now live in the dependency-free
// internal/provision leaf (#311). This file keeps the `botfam worktree` command
// builder (moves to internal/cli in phase 3) wired to the leaf, and re-exports
// EnsureMembership for internal/mcp.

// WorktreeCmd is the thin args/io entry point retained for tests and the MCP
// layer; it builds the Cobra command and runs it against args.
func WorktreeCmd(args []string, out io.Writer) error {
	return runCobra(NewWorktreeCmd(), args, out)
}

// NewWorktreeCmd builds the `botfam worktree` Cobra command and its
// init/sync/register subcommands.
func NewWorktreeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "worktree",
		Short:         "Manage agent git worktrees (init/sync/register)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	sub := func(use, short string, fn func([]string, io.Writer) error) *cobra.Command {
		return &cobra.Command{
			Use:           use,
			Short:         short,
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return fn(args, cmd.OutOrStdout())
			},
		}
	}
	c.AddCommand(
		sub("init <actor> [path]", "Initialize a worktree's git identity", provision.InitWorktree),
		sub("sync [path]", "Sync a worktree with the fam object stores", provision.SyncWorktree),
		sub("register [path]", "Register all worktrees of this repo into the fam registry", provision.RegisterWorktrees),
	)
	return c
}

// EnsureMembership re-exports provision.EnsureMembership.
func EnsureMembership(root string, workDir string) error {
	return provision.EnsureMembership(famconfig.FamIdentity{
		FamDir:      root,
		FamTOMLPath: filepath.Join(root, "fam.toml"),
	}, workDir)
}
