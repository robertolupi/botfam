package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/mailbox"
	"github.com/spf13/cobra"
)

// MailboxPath returns the per-agent mailbox file ($FAMROOT/$AGENT.mailbox) for
// the agent owning workDir, resolved through famconfig so the writer (ingester)
// and the reader (`botfam wait`) always agree on the path.
func MailboxPath(workDir string) (string, error) {
	rf, err := famconfig.ResolveFam(workDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(rf.FamDir, rf.Actor+".mailbox"), nil
}

// WaitCmd is the thin args/io entry point retained for tests and the MCP layer.
func WaitCmd(args []string, out io.Writer) error { return runCobra(NewWaitCmd(), args, out) }

// NewWaitCmd builds the `botfam wait` Cobra command — the single wake point that
// blocks on the per-agent mailbox and prints whatever arrives (#229).
func NewWaitCmd() *cobra.Command {
	var (
		from     int64
		timeoutS int
		sources  string
		mboxPath string
		workDir  string
		pollMs   int
	)
	c := &cobra.Command{
		Use:   "wait",
		Short: "Block on the per-agent mailbox until new IRC/forge events arrive",
		Long: `Block on this agent's mailbox ($FAMROOT/$AGENT.mailbox) and print the events
that wake it, then exit — the single wake point unifying irc-wait and forge-wait.

Output is JSONL: one object per surfaced event, then a trailing
{"source":"meta", ...} cursor line carrying the byte "offset" to pass back as
--from on the next call (and "timed_out"). With no --from, a fresh session starts
at the current end of the mailbox, so it never replays stale backlog.

This command only reads the mailbox; a background ingester (hosted in the botfam
MCP server) is what fills it.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pollMs <= 0 {
				return fmt.Errorf("--poll-ms: must be > 0")
			}
			if timeoutS < 0 {
				return fmt.Errorf("--timeout: invalid seconds %d", timeoutS)
			}
			if mboxPath == "" {
				p, err := MailboxPath(workDir)
				if err != nil {
					return fmt.Errorf("wait: %w", err)
				}
				mboxPath = p
			}
			return runWait(cmd.OutOrStdout(), mboxPath, from, parseSources(sources),
				time.Duration(timeoutS)*time.Second, time.Duration(pollMs)*time.Millisecond)
		},
	}
	c.Flags().Int64Var(&from, "from", -1, "resume from this byte offset (default: current end of mailbox)")
	c.Flags().IntVar(&timeoutS, "timeout", 0, "give up after N seconds and exit 0 (0 = block forever)")
	c.Flags().StringVar(&sources, "sources", "irc,forge", "comma-separated event sources to surface")
	c.Flags().StringVar(&mboxPath, "mailbox", "", "path to the mailbox file (overrides fam resolution)")
	c.Flags().StringVar(&workDir, "work-dir", ".", "worktree to resolve the agent/mailbox from")
	c.Flags().IntVar(&pollMs, "poll-ms", 500, "poll interval in milliseconds")
	return c
}

func parseSources(s string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

// waitSummary is the trailing cursor line every `wait` invocation emits (even on
// timeout) so the caller persists the byte offset and re-arms cleanly.
type waitSummary struct {
	Source   string           `json:"source"` // always "meta"
	Woke     string           `json:"woke,omitempty"`
	Offset   int64            `json:"offset"`
	Epoch    int64            `json:"epoch,omitempty"`
	Seqs     map[string]int64 `json:"seqs,omitempty"`
	TimedOut bool             `json:"timed_out"`
}

func runWait(out io.Writer, path string, from int64, want map[string]bool, timeout, poll time.Duration) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	expired := func() bool { return timeout > 0 && !time.Now().Before(deadline) }

	// Wait for the mailbox to exist (it may not yet on a cold start).
	for {
		if _, err := os.Stat(path); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("wait: %w", err)
		}
		if expired() {
			return emitSummary(out, waitSummary{Offset: max64(from, 0), TimedOut: true})
		}
		time.Sleep(poll)
	}

	// Default start offset (from<0) is the current end: no stale backlog.
	offset := from
	if offset < 0 {
		fi, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("wait: %w", err)
		}
		offset = fi.Size()
	}

	seqs := map[string]int64{}
	var epoch int64
	for {
		evs, next, err := mailbox.ReadFrom(path, offset)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("wait: %w", err)
		}
		offset = next

		var surfaced []mailbox.Event
		for _, ev := range evs {
			if ev.Source == mailbox.SourceMeta {
				continue // writer checkpoints are internal, not surfaced
			}
			epoch = ev.Epoch
			if len(want) > 0 && !want[ev.Source] {
				continue
			}
			seqs[ev.Source] = ev.Seq
			surfaced = append(surfaced, ev)
		}

		if len(surfaced) > 0 {
			for _, ev := range surfaced {
				if err := emitEvent(out, ev); err != nil {
					return err
				}
			}
			return emitSummary(out, waitSummary{
				Woke: surfaced[0].Source, Offset: offset, Epoch: epoch, Seqs: seqs,
			})
		}
		if expired() {
			return emitSummary(out, waitSummary{Offset: offset, Epoch: epoch, Seqs: seqs, TimedOut: true})
		}
		time.Sleep(poll)
	}
}

func emitEvent(out io.Writer, ev mailbox.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", b)
	return err
}

func emitSummary(out io.Writer, s waitSummary) error {
	s.Source = mailbox.SourceMeta
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", b)
	return err
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
