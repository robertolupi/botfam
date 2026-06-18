package ops

import (
	"io"

	"github.com/spf13/cobra"
	"github.com/robertolupi/botfam/internal/cli/cmdutil"
)

// SessionCmd is the thin args/io entry point retained for tests; it builds the
// Cobra command and runs it against args.
func SessionCmd(args []string, out io.Writer) error {
	return cmdutil.RunCobra(NewSessionCmd(), args, out)
}

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
