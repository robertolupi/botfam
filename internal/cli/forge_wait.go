package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
)

// indentTruncate prefixes every line of s and caps the total length so a long
// issue/PR body doesn't flood the wake output (the URL is printed for the rest).
func indentTruncate(s, prefix string, max int) string {
	if len(s) > max {
		s = s[:max] + "\n… (truncated — see URL)"
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// ForgeWaitCmd is the thin args/io entry point retained for tests and the MCP
// layer; it builds the Cobra command and runs it against args.
func ForgeWaitCmd(args []string, out io.Writer) error {
	return runCobra(NewForgeWaitCmd(), args, out)
}

// NewForgeWaitCmd builds the `botfam forge-wait` Cobra command (issue #17).
func NewForgeWaitCmd() *cobra.Command {
	var (
		once      bool
		markRead  bool
		intervalS int
		timeoutS  int
	)
	c := &cobra.Command{
		Use:   "forge-wait",
		Short: "Wait for new Gitea notifications (review requests, comments, mentions)",
		Long: `Block until this agent has unread forge notifications — a review requested,
a comment, a mention, or a new issue/PR assigned to you (all subject types,
not just PRs) — then print them and exit. The forge analogue of
"botfam irc-wait", so the harness can wake the agent on forge activity.

Requires the token to carry the notification scope (forge-login.sh requests it).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if intervalS < 0 {
				return fmt.Errorf("--interval: invalid seconds %d", intervalS)
			}
			if timeoutS < 0 {
				return fmt.Errorf("--timeout: invalid seconds %d", timeoutS)
			}
			return runForgeWait(cmd.OutOrStdout(), once, markRead,
				time.Duration(intervalS)*time.Second, time.Duration(timeoutS)*time.Second)
		},
	}
	c.Flags().BoolVar(&once, "once", false, "check once and return (don't block)")
	c.Flags().BoolVar(&markRead, "mark-read", false, "mark the surfaced notifications read (needs write:notification)")
	c.Flags().IntVar(&intervalS, "interval", 30, "poll interval in seconds")
	c.Flags().IntVar(&timeoutS, "timeout", 0, "give up after this many seconds with nothing (0 = wait forever)")
	return c
}

func runForgeWait(out io.Writer, once, markRead bool, interval, timeout time.Duration) error {
	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		if info, err := (GitResolver{}).ResolveIdentity("."); err == nil {
			actor = info.Actor
		}
	}
	client, err := forge.NewClient(".", actor)
	if err != nil {
		return fmt.Errorf("forge-wait: %w", err)
	}
	who := actor
	if who == "" {
		who = "agent"
	}

	start := time.Now()
	for {
		ns, err := client.ListUnreadNotifications()
		if err != nil {
			return fmt.Errorf("forge-wait: fetch notifications failed "+
				"(token may lack the 'notification' scope — re-mint with forge-login.sh): %w", err)
		}
		if len(ns) > 0 {
			fmt.Fprintf(out, "forge-wait: %s has %d unread notification(s):\n", who, len(ns))
			for _, n := range ns {
				url := n.Subject.HTMLURL
				if url == "" {
					url = n.Subject.URL
				}
				fmt.Fprintf(out, "\n• [%s] %s: %s\n  %s\n",
					n.Subject.Type, n.Repository.FullName, n.Subject.Title, url)
				// Fetch the content inline so the agent doesn't have to round-trip.
				if sc, err := client.GetSubject(n.Subject.URL); err == nil {
					if sc.State != "" {
						fmt.Fprintf(out, "  state: %s\n", sc.State)
					}
					if body := strings.TrimSpace(sc.Body); body != "" {
						fmt.Fprintln(out, indentTruncate(body, "  | ", 2000))
					}
				}
			}
			if markRead {
				for _, n := range ns {
					_ = client.MarkNotificationRead(n.ID)
				}
			}
			return nil
		}
		if once {
			fmt.Fprintln(out, "forge-wait: nothing unread.")
			return nil
		}
		if timeout > 0 && time.Since(start) >= timeout {
			return fmt.Errorf("forge-wait: timed out after %s with nothing unread", timeout)
		}
		time.Sleep(interval)
	}
}
