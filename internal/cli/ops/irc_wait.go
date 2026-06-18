package ops

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/robertolupi/botfam/internal/cli/cmdutil"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/irc"
	"github.com/spf13/cobra"
)

// IrcWaitCmd is the thin args/io entry point retained for tests and the MCP
// layer; it builds the Cobra command and runs it against args.
func IrcWaitCmd(args []string, out io.Writer) error {
	return cmdutil.RunCobra(NewIrcWaitCmd(), args, out)
}

// NewIrcWaitCmd builds the `botfam irc-wait` Cobra command (native wake watcher).
func NewIrcWaitCmd() *cobra.Command {
	var nick, logPath string
	var rawNick bool
	c := &cobra.Command{
		Use:   "irc-wait --nick <actor> [--file <path>]",
		Short: "Block until new IRC traffic arrives (wake watcher)",
		Long: `Watch the IRC client log and block until a new line relevant to <actor>
appears (skipping history replays and the agent's own messages), then print
the new lines and exit.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The log dir is keyed by the bare actor (scratch/irc/<actor>), but
			// the agent's own messages appear under the fam-scoped IRC nick
			// (claude-botfam) — so match on the scoped nick or the watcher would
			// wake on its own traffic (#137). --raw-nick / --file opt out.
			matchNick := nick
			if nick != "" && !rawNick {
				matchNick = famconfig.FamScopedNick(nick, famconfig.FamSlug(famconfig.LoadFamRegistry(".")))
			}
			if logPath == "" {
				if nick == "" {
					return errors.New("missing required argument: --nick <name> or --file <path>")
				}
				logPath = filepath.Join("scratch", "irc", nick, "log")
			}
			lines, _, _, err := irc.WaitIrcLines(logPath, matchNick, -1, 0)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, line := range lines {
				fmt.Fprintln(out, line)
			}
			return nil
		},
	}
	c.Flags().StringVar(&nick, "nick", "", "actor whose client log to watch (FIFO dir scratch/irc/<actor>)")
	c.Flags().StringVar(&logPath, "file", "", "path to the IRC client log (overrides --nick derivation)")
	c.Flags().BoolVar(&rawNick, "raw-nick", false, "match <actor> verbatim instead of the fam-scoped <actor>-<fam> nick")
	return c
}
