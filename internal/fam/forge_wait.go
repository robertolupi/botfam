package fam

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
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

const forgeWaitHelp = `Usage:
  botfam forge-wait [--once] [--interval S] [--timeout S] [--mark-read]

Block until this agent has unread forge notifications — a review requested, a
comment, a mention, or a new issue/PR assigned to you (all subject types, not
just PRs) — then print them and exit. The forge analogue of "botfam irc-wait",
so the harness can wake the agent on forge activity instead of a human nudging
it.

  --once        check once; print the result and return (don't block).
  (default)     poll every --interval until there's activity, then return.
  --interval S  poll interval in seconds (default 30).
  --timeout S   give up after S seconds with nothing (error exit).
  --mark-read   mark the surfaced notifications read (needs write:notification).

Requires the token to carry the notification scope (forge-login.sh requests it).
`

// ForgeWaitCmd handles "botfam forge-wait [flags]" (issue #17).
func ForgeWaitCmd(args []string, out io.Writer) error {
	once := false
	markRead := false
	interval := 30 * time.Second
	var timeout time.Duration
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-h", "--help", "help":
			fmt.Fprint(out, forgeWaitHelp)
			return nil
		case "--once":
			once = true
		case "--mark-read":
			markRead = true
		case "--interval", "--timeout":
			i++
			if i >= len(args) {
				return fmt.Errorf("%s requires a value in seconds", a)
			}
			s, err := strconv.Atoi(args[i])
			if err != nil || s < 0 {
				return fmt.Errorf("%s: invalid seconds %q", a, args[i])
			}
			if a == "--interval" {
				interval = time.Duration(s) * time.Second
			} else {
				timeout = time.Duration(s) * time.Second
			}
		default:
			return fmt.Errorf("unknown argument %q", a)
		}
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		if info, err := (Resolver{WorkDir: "."}).Resolve(); err == nil {
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
