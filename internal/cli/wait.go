package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/mailbox"
	"github.com/spf13/cobra"
)

// SpoolDir returns the per-agent spool directory ($FAMROOT/spool/$AGENT) for the
// agent owning workDir, resolved through famconfig so the writer (ingester) and
// the reader (`botfam wait`) always agree on the path.
func SpoolDir(workDir string) (string, error) {
	rf, err := ResolveFam(workDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(rf.FamDir, "spool", rf.Actor), nil
}

// WaitCmd is the thin args/io entry point retained for tests and the MCP layer.
func WaitCmd(args []string, out io.Writer) error { return runCobra(NewWaitCmd(), args, out) }

// NewWaitCmd builds the `botfam wait` Cobra command — the single wake point that
// blocks on the per-agent spool and prints whatever arrives (#229).
func NewWaitCmd() *cobra.Command {
	var (
		timeoutS int
		sources  string
		spoolDir string
		workDir  string
		pollMs   int
	)
	c := &cobra.Command{
		Use:   "wait",
		Short: "Block on the per-agent spool until new IRC/forge events arrive",
		Long: `Block on this agent's spool ($FAMROOT/spool/$AGENT) and print the messages
that wake it, then exit — the single wake point unifying irc-wait and forge-wait.

It drains the spool's new/ box (undelivered messages), prints each, and moves it
to cur/ — the move is the ack. Output is JSONL: one object per surfaced message,
then a trailing {"source":"meta", ...} summary line.

This command only reads the spool; a background ingester (hosted in the botfam
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
			if spoolDir == "" {
				p, err := SpoolDir(workDir)
				if err != nil {
					return fmt.Errorf("wait: %w", err)
				}
				spoolDir = p
			}
			return runWait(cmd.OutOrStdout(), spoolDir, parseSources(sources),
				time.Duration(timeoutS)*time.Second, time.Duration(pollMs)*time.Millisecond)
		},
	}
	c.Flags().IntVar(&timeoutS, "timeout", 0, "give up after N seconds and exit 0 (0 = block forever)")
	c.Flags().StringVar(&sources, "sources", "irc,forge", "comma-separated event sources to surface")
	c.Flags().StringVar(&spoolDir, "spool", "", "path to the spool directory (overrides fam resolution)")
	c.Flags().StringVar(&workDir, "work-dir", ".", "worktree to resolve the agent/spool from")
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

// waitSummary is the trailing line every `wait` invocation emits (even on
// timeout) so the caller can tell why it returned and re-arm cleanly.
type waitSummary struct {
	Source   string `json:"source"` // always "meta"
	Woke     string `json:"woke,omitempty"`
	Count    int    `json:"count"`
	TimedOut bool   `json:"timed_out"`
}

// emittedMessage is the JSON projection of a surfaced spool message. The body is
// included (unlike the future notification nudge) — `botfam wait` returns the
// whole message so the agent can act without a second lookup.
type emittedMessage struct {
	Source  string `json:"source"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Subject string `json:"subject,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
	Date    string `json:"date,omitempty"`
	Body    string `json:"body,omitempty"`
}

func runWait(out io.Writer, dir string, want map[string]bool, timeout, poll time.Duration) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	expired := func() bool { return timeout > 0 && !time.Now().Before(deadline) }

	// Wait for the spool to exist (it may not yet on a cold start).
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("wait: %w", err)
		}
		if expired() {
			return emitSummary(out, waitSummary{TimedOut: true})
		}
		time.Sleep(poll)
	}

	sp, err := mailbox.Open(dir)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}

	for {
		ents, err := sp.ListNew()
		if err != nil {
			return fmt.Errorf("wait: %w", err)
		}

		var surfaced []*mailbox.Message
		for _, e := range ents {
			m, rerr := sp.Read(e)
			if rerr != nil {
				_ = sp.Ack(e) // drop an unreadable message rather than spin on it
				continue
			}
			// Ack (move new/->cur/) every drained message so coalesced traffic is
			// consumed in one wake; only the wanted sources are surfaced.
			if err := sp.Ack(e); err != nil {
				return fmt.Errorf("wait: %w", err)
			}
			if len(want) > 0 && !want[m.Source] {
				continue
			}
			surfaced = append(surfaced, m)
		}

		if len(surfaced) > 0 {
			for _, m := range surfaced {
				if err := emitMessage(out, m); err != nil {
					return err
				}
			}
			return emitSummary(out, waitSummary{Woke: surfaced[0].Source, Count: len(surfaced)})
		}
		if expired() {
			return emitSummary(out, waitSummary{TimedOut: true})
		}
		time.Sleep(poll)
	}
}

func emitMessage(out io.Writer, m *mailbox.Message) error {
	em := emittedMessage{
		Source:  m.Source,
		From:    m.From,
		To:      m.To,
		Subject: m.Subject,
		Kind:    m.Kind,
		Seq:     m.Seq,
		Body:    m.Body,
	}
	if !m.Date.IsZero() {
		em.Date = m.Date.UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(em)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", b)
	return err
}

func emitSummary(out io.Writer, s waitSummary) error {
	s.Source = "meta"
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", b)
	return err
}
