package cli

import (
	"io"

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
