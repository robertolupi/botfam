package ops

import (
	"fmt"
	"io"
	"os"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/spf13/cobra"
)

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
			info, err := (famconfig.GitResolver{}).ResolveIdentity(wd)
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
