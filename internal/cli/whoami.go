package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// WhoamiCmd is the thin args/io entry point retained for tests; it builds the
// Cobra command and runs it against args.
func WhoamiCmd(args []string, out io.Writer) error {
	return runCobra(NewWhoamiCmd(), args, out)
}

// NewWhoamiCmd builds the `botfam whoami` Cobra command.
func NewWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "whoami",
		Short:         "Print the resolved actor name for the current worktree",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := (GitResolver{}).ResolveIdentity(wd)
			if err != nil {
				return err
			}
			if info.Actor == "" {
				return fmt.Errorf("could not resolve actor for worktree: %s", wd)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), info.Actor+"\n")
			return err
		},
	}
}
