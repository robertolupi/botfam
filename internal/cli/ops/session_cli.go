package ops

import (
	"github.com/spf13/cobra"
)

// NewSessionCmd builds the `botfam session` Cobra command and its subcommands.
func NewSessionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "session",
		Short:         "Manage coordination sessions (extract)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	c.AddCommand(newSessionExtractCmd())
	return c
}
