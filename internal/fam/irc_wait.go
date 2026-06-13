package fam

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// IrcWaitCmd is the thin args/io entry point retained for tests and the MCP
// layer; it builds the Cobra command and runs it against args.
func IrcWaitCmd(args []string, out io.Writer) error {
	return runCobra(NewIrcWaitCmd(), args, out)
}

// NewIrcWaitCmd builds the `botfam irc-wait` Cobra command (native wake watcher).
func NewIrcWaitCmd() *cobra.Command {
	var nick, logPath string
	c := &cobra.Command{
		Use:   "irc-wait --nick <nick> [--file <path>]",
		Short: "Block until new IRC traffic arrives (wake watcher)",
		Long: `Watch the IRC client log and block until a new line relevant to <nick>
appears (skipping history replays and the agent's own messages), then print
the new lines and exit.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if logPath == "" {
				if nick == "" {
					return errors.New("missing required argument: --nick <name> or --file <path>")
				}
				logPath = filepath.Join("scratch", "irc", nick, "log")
			}
			lines, _, _, err := WaitIrcLines(logPath, nick, -1, 0)
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
	c.Flags().StringVar(&nick, "nick", "", "actor nick whose client log to watch")
	c.Flags().StringVar(&logPath, "file", "", "path to the IRC client log (overrides --nick derivation)")
	return c
}

// WaitIrcLines watches the IRC client log at logPath for new lines relevant
// to nick (per isMatchingLine: skips "(hist)" replays and nick's own
// messages/joins). fromOffset < 0 means "start from the current end of the
// file"; timeout <= 0 means wait forever. It returns the matched lines, the
// new offset (file size after the read), and whether the wait timed out.
// Truncation/rotation resets the offset to the new (smaller) file size.
func WaitIrcLines(logPath, nick string, fromOffset int64, timeout time.Duration) (lines []string, newOffset int64, timedOut bool, err error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	expired := func() bool { return timeout > 0 && time.Now().After(deadline) }

	// Wait for file to exist
	for {
		_, err := os.Stat(logPath)
		if err == nil {
			break
		}
		if expired() {
			return nil, fromOffset, true, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	currentSize := fromOffset
	if currentSize < 0 {
		stat, err := os.Stat(logPath)
		if err != nil {
			return nil, 0, false, fmt.Errorf("failed to stat log file: %w", err)
		}
		currentSize = stat.Size()
	}

	for {
		if expired() {
			return nil, currentSize, true, nil
		}
		time.Sleep(500 * time.Millisecond)
		stat, err := os.Stat(logPath)
		if err != nil {
			continue // file might be temporarily unavailable or rotated
		}

		if stat.Size() > currentSize {
			f, err := os.Open(logPath)
			if err != nil {
				continue
			}
			_, err = f.Seek(currentSize, io.SeekStart)
			if err != nil {
				f.Close()
				continue
			}

			scanner := bufio.NewScanner(f)
			var matchedLines []string
			for scanner.Scan() {
				line := scanner.Text()
				if isMatchingLine(line, nick) {
					matchedLines = append(matchedLines, line)
				}
			}
			f.Close()
			currentSize = stat.Size()

			if len(matchedLines) > 0 {
				return matchedLines, currentSize, false, nil
			}
		} else if stat.Size() < currentSize {
			// File was truncated/rotated
			currentSize = stat.Size()
		}
	}
}

// ReadIrcLog reads lines from the IRC client log at logPath without any nick
// filtering — the log already is the filtered human-readable view.
// fromOffset >= 0 reads from that byte offset toward EOF, returning up to
// maxLines lines and nextOffset pointing just past the last returned line so
// callers can page. fromOffset < 0 returns the last maxLines lines of the
// file (tail behavior) with nextOffset = file size. maxLines <= 0 defaults
// to 50.
func ReadIrcLog(logPath string, fromOffset int64, maxLines int) (lines []string, nextOffset int64, err error) {
	if maxLines <= 0 {
		maxLines = 50
	}

	f, err := os.Open(logPath)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	if fromOffset < 0 {
		stat, err := f.Stat()
		if err != nil {
			return nil, 0, err
		}
		scanner := bufio.NewScanner(f)
		var all []string
		for scanner.Scan() {
			all = append(all, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return nil, 0, err
		}
		if len(all) > maxLines {
			all = all[len(all)-maxLines:]
		}
		return all, stat.Size(), nil
	}

	if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	r := bufio.NewReader(f)
	offset := fromOffset
	for len(lines) < maxLines {
		line, rerr := r.ReadString('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, 0, rerr
		}
		// Only consume complete lines: a trailing fragment without '\n' is
		// a line still being written — leave the cursor before it so the
		// next page rereads it whole.
		if strings.HasSuffix(line, "\n") {
			offset += int64(len(line))
			lines = append(lines, strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		}
		if rerr != nil {
			break
		}
	}
	return lines, offset, nil
}

func isMatchingLine(line, nick string) bool {
	// Ignore replayed history messages
	if strings.Contains(line, "(hist)") {
		return false
	}
	// Must contain either " <" (message) or "JOIN" (channel join event)
	if !strings.Contains(line, " <") && !strings.Contains(line, "JOIN") {
		return false
	}
	// Must not be our own message or join
	if nick != "" {
		if strings.Contains(line, "<"+nick+">") {
			return false
		}
		if strings.Contains(line, "* "+nick+" JOIN") {
			return false
		}
	}
	return true
}
