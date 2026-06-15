package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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
		replay   bool
		sinceStr string
		all      bool
	)
	c := &cobra.Command{
		Use:   "wait",
		Short: "Block on the per-agent spool until new IRC/forge events arrive",
		Long: `Block on this agent's spool ($FAMROOT/spool/$AGENT) and print the messages
that wake it, then exit — the single wake point unifying irc-wait and forge-wait.

By default it runs in do-not-disturb: forge events wake you only when they are
directed at you (you are an assignee, or @-mentioned in the latest comment).
Non-directed forge events are still drained to cur/ (see --replay) but do not
disturb you. IRC is always relayed — you control exposure by joining/parting the
channel. Pass --all to surface every forge event regardless.

It drains the spool's new/ box, prints each surfaced message verbatim (RFC-822
headers + body) under a banner, and moves the batch to cur/ — the move is the
ack. With --replay it instead dumps the cur/ replay buffer (no ack) for gap
recovery.

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
			var since time.Duration
			if sinceStr != "" {
				d, err := time.ParseDuration(sinceStr)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				since = d
			}

			out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if replay {
				return runReplay(out, errw, spoolDir, parseSources(sources), since)
			}
			// Cancellable: SIGINT/SIGTERM unblock the wait loop cleanly (#276).
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runWait(ctx, out, errw, spoolDir, parseSources(sources), !all,
				time.Duration(timeoutS)*time.Second, time.Duration(pollMs)*time.Millisecond)
		},
	}
	c.Flags().IntVar(&timeoutS, "timeout", 0, "give up after N seconds and exit 0 (0 = block forever)")
	c.Flags().StringVar(&sources, "sources", "irc,forge", "comma-separated event sources to surface")
	c.Flags().StringVar(&spoolDir, "spool", "", "path to the spool directory (overrides fam resolution)")
	c.Flags().StringVar(&workDir, "work-dir", ".", "worktree to resolve the agent/spool from")
	c.Flags().IntVar(&pollMs, "poll-ms", 500, "poll interval in milliseconds")
	c.Flags().BoolVar(&replay, "replay", false, "dump the cur/ replay buffer (read messages) instead of waiting; no ack")
	c.Flags().BoolVar(&all, "all", false, "disable do-not-disturb: surface every forge event, not just those directed at you (assignee/@-mention)")
	c.Flags().StringVar(&sinceStr, "since", "", "with --replay, only messages newer than this duration ago (e.g. 1h); default all")
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

// drainedMsg is one message read from the spool: the verbatim file bytes plus
// the parsed Source (for the banner / source filter) and its spool entry.
type drainedMsg struct {
	entry    mailbox.Entry
	raw      []byte
	source   string
	directed bool
}

// readEntry reads an entry's verbatim bytes and parses its Source. A parse error
// is non-fatal (the raw bytes are still emitted) — a message is never dropped to
// a header parse miss.
func readEntry(sp *mailbox.Spool, e mailbox.Entry) (drainedMsg, error) {
	raw, err := os.ReadFile(e.Path())
	if err != nil {
		return drainedMsg{}, err
	}
	d := drainedMsg{entry: e, raw: raw}
	if m, err := mailbox.ParseMessage(raw); err == nil {
		d.source = m.Source
		d.directed = m.Directed
	}
	return d, nil
}

// runWait blocks until new/ has at least one message in a wanted source, drains
// all of new/ (acking each to cur/), and prints the wanted messages under
// banners. It fails fast if the spool itself is absent (#263 — a missing spool
// is a misconfiguration, never something to wait on forever), and unblocks on
// ctx cancellation or the timeout.
func runWait(ctx context.Context, out, errw io.Writer, spoolDir string, want map[string]bool, directedOnly bool, timeout, poll time.Duration) error {
	if err := ensureSpool(spoolDir); err != nil {
		return err
	}
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	fmt.Fprintf(errw, "wait: watching %s (timeout=%s, poll=%s)\n",
		filepath.Join(sp.Dir(), "new"), durOrBlock(timeout), poll)

	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	expired := func() bool { return timeout > 0 && !time.Now().Before(deadline) }

	for {
		ents, err := sp.ListNew()
		if err != nil {
			return fmt.Errorf("wait: %w", err)
		}

		// Read the whole new/ snapshot first (no ack yet) so a crash mid-drain
		// re-delivers the batch rather than losing it (at-least-once).
		var shown []drainedMsg
		for _, e := range ents {
			d, rerr := readEntry(sp, e)
			if rerr != nil {
				_ = sp.Ack(e) // unreadable: drop rather than spin on it
				continue
			}
			if len(want) != 0 && !want[d.source] {
				continue // source not selected
			}
			// Do-not-disturb (the default): forge events only wake me when they're
			// directed at me (assignee / @-mention). IRC is always relayed — I
			// control exposure by joining/parting the channel. Non-directed forge
			// events are still drained below (kept in cur/ for --replay), just not
			// surfaced. --all turns this off.
			if directedOnly && d.source == mailbox.SourceForge && !d.directed {
				continue
			}
			shown = append(shown, d)
		}

		if len(shown) > 0 {
			// Write all surfaced output BEFORE acking: if a write fails (broken
			// stdout / errored redirect), return without acking so the batch stays
			// in new/ and the next wait re-delivers it — at-least-once, dup over
			// loss. Acking before a confirmed write would silently drop the wake.
			for i, d := range shown {
				if err := emitBanner(out, i+1, len(shown), d.source, d.raw); err != nil {
					return fmt.Errorf("wait: %w", err)
				}
			}
			if _, err := fmt.Fprintf(out, "===== woke: %d %s =====\n", len(shown), plural(len(shown))); err != nil {
				return fmt.Errorf("wait: %w", err)
			}
			// Output is durably written: now consume the whole drained batch
			// (wanted + filtered-out) so coalesced traffic isn't re-surfaced.
			for _, e := range ents {
				if err := sp.Ack(e); err != nil {
					return fmt.Errorf("wait: %w", err)
				}
			}
			return nil
		}
		// Nothing wanted surfaced: consume any filtered-out drained entries (no
		// output was written, so there's nothing to lose) and keep waiting.
		for _, e := range ents {
			_ = sp.Ack(e)
		}

		if expired() {
			if _, err := fmt.Fprintln(out, "===== timed out ====="); err != nil {
				return fmt.Errorf("wait: %w", err)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// runReplay dumps the cur/ replay buffer (already-acked messages) for gap
// recovery: it never acks and never blocks. With since > 0 it skips messages
// whose file is older than that.
func runReplay(out, errw io.Writer, spoolDir string, want map[string]bool, since time.Duration) error {
	if err := ensureSpool(spoolDir); err != nil {
		return err
	}
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	fmt.Fprintf(errw, "wait: replaying %s\n", filepath.Join(sp.Dir(), "cur"))

	ents, err := sp.ListCur()
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	var cutoff time.Time
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}

	var shown []drainedMsg
	for _, e := range ents {
		if !cutoff.IsZero() {
			fi, err := os.Stat(e.Path())
			if err != nil || fi.ModTime().Before(cutoff) {
				continue
			}
		}
		d, rerr := readEntry(sp, e)
		if rerr != nil {
			continue
		}
		if len(want) == 0 || want[d.source] {
			shown = append(shown, d)
		}
	}
	for i, d := range shown {
		if err := emitBanner(out, i+1, len(shown), d.source, d.raw); err != nil {
			return fmt.Errorf("wait: %w", err)
		}
	}
	if _, err := fmt.Fprintf(out, "===== replayed: %d %s =====\n", len(shown), plural(len(shown))); err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	return nil
}

// ensureSpool fail-fasts (#263) when the spool directory is absent: that is a
// misconfiguration (wrong fam, ingester never ran), not something to block on.
// The error names the resolved absolute path so the silent-hang class is
// diagnosable at a glance.
func ensureSpool(spoolDir string) error {
	abs, err := filepath.Abs(spoolDir)
	if err != nil {
		abs = spoolDir
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("wait: spool does not exist: %s (the MCP-hosted ingester has not created it yet; ensure the botfam MCP server is running for this agent)", abs)
		}
		return fmt.Errorf("wait: %w", err)
	}
	return nil
}

// emitBanner prints one message: a legible banner naming its position and source,
// then the verbatim spool bytes (with a guaranteed trailing newline + blank
// separator so concatenated messages stay readable). It returns the first write
// error so the caller can refuse to ack output that never reached the consumer.
func emitBanner(out io.Writer, n, total int, source string, raw []byte) error {
	if source == "" {
		source = "?"
	}
	if _, err := fmt.Fprintf(out, "===== message %d/%d · %s =====\n", n, total, source); err != nil {
		return err
	}
	if _, err := out.Write(raw); err != nil {
		return err
	}
	if !bytes.HasSuffix(raw, []byte("\n")) {
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return "message"
	}
	return "messages"
}

func durOrBlock(d time.Duration) string {
	if d <= 0 {
		return "block"
	}
	return d.String()
}
