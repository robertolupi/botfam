package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// HistoryEntry moved to the internal/irc leaf (#311); re-exported in irc.go.

// ScribeCmd is the thin args/io entry point retained for tests; it builds the
// Cobra command and runs it against args.
func ScribeCmd(args []string, out io.Writer) error {
	return runCobra(NewScribeCmd(), args, out)
}

// NewScribeCmd builds the `botfam scribe` Cobra command (Go IRC scribe bot).
func NewScribeCmd() *cobra.Command {
	mainChannel, ccrepChannel := FamChannels(LoadFamRegistry("."))
	server := "localhost:6667"
	channel := mainChannel + "," + ccrepChannel
	nick := "scribe"
	historyFile := os.Getenv("COLLAB_HISTORY")

	c := &cobra.Command{
		Use:           "scribe",
		Short:         "Run the channel scribe bot (logs IRC events to the ledger)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScribe(server, channel, nick, historyFile, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&server, "server", server, "IRC server host:port")
	c.Flags().StringVar(&channel, "channel", channel, "comma-separated channels to log")
	c.Flags().StringVar(&nick, "nick", nick, "scribe nick")
	c.Flags().StringVar(&historyFile, "file", historyFile, "history ledger path (default $COLLAB_HISTORY)")
	return c
}

func runScribe(server, channel, nick, historyFile string, out io.Writer) error {
	if nick == "" {
		return errors.New("--nick requires a non-empty value")
	}

	if historyFile == "" {
		var err error
		historyFile, err = DefaultHistoryPath(".")
		if err != nil {
			return errors.New("COLLAB_HISTORY is unset and family root could not be resolved")
		}
	}

	// Validate history file path
	if err := ValidateHistoryPath(historyFile); err != nil {
		return err
	}

	// Create directories for history file if they don't exist
	if err := os.MkdirAll(filepath.Dir(historyFile), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Open history file in append mode
	logFile, err := os.OpenFile(historyFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open history file: %w", err)
	}
	defer logFile.Close()

	fmt.Fprintf(out, "* Scribe bot starting. Server: %s, Nick: %s, Channel: %s, File: %s\n", server, nick, channel, historyFile)

	// Connect to IRC server
	conn, err := net.DialTimeout("tcp", server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("scribe connection failed: %w", err)
	}
	defer conn.Close()

	// Send initial commands (nick is stable per fam: bare "scribe" for the
	// original botfam deployment, scribe-<slug> for additional fams)
	_, _ = fmt.Fprintf(conn, "NICK %s\r\n", nick)
	_, _ = fmt.Fprintf(conn, "USER %s 0 * :botfam scribe bot\r\n", nick)
	_, _ = fmt.Fprintf(conn, "JOIN %s\r\n", channel)

	privRe := regexp.MustCompile(`^:([^!\s]+)\S*\s+PRIVMSG\s+(\S+)\s+:(.*)$`)
	eventRe := regexp.MustCompile(`^:([^!\s]+)\S*\s+(JOIN|PART|QUIT|NICK)\b\s*:?(\S*)`)
	inviteRe := regexp.MustCompile(`^(?i):\S+\s+INVITE\s+\S+\s+:?(\S+)$`)

	// Read from socket
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(out, "* Scribe bot disconnected (EOF)")
				break
			}
			return fmt.Errorf("scribe read error: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "PING") {
			pong := "PONG" + strings.TrimPrefix(line, "PING") + "\r\n"
			_, _ = conn.Write([]byte(pong))
			continue
		}

		if m := inviteRe.FindStringSubmatch(line); m != nil {
			invitedChan := m[1]
			_, _ = fmt.Fprintf(conn, "JOIN %s\r\n", invitedChan)
			var entry HistoryEntry
			entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
			entry.Sender = "server"
			entry.Type = "INVITE"
			entry.Target = invitedChan
			entry.Body = line
			bytes, err := json.Marshal(entry)
			if err == nil {
				_, _ = logFile.Write(append(bytes, '\n'))
				_ = logFile.Sync()
			}
			continue
		}

		var entry HistoryEntry
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)

		if m := privRe.FindStringSubmatch(line); m != nil {
			entry.Sender = m[1]
			entry.Type = "PRIVMSG"
			entry.Target = m[2]
			entry.Body = m[3]
		} else if m := eventRe.FindStringSubmatch(line); m != nil {
			entry.Sender = m[1]
			entry.Type = m[2]
			entry.Target = m[3]
			if entry.Type == "JOIN" && entry.Sender == nick {
				// Announce version on join
				announcement := fmt.Sprintf("[scribe] version %s joined.", GetVersion())
				cmd := fmt.Sprintf("PRIVMSG %s :%s\r\n", entry.Target, announcement)
				_, _ = conn.Write([]byte(cmd))
			}
		} else {
			entry.Sender = "server"
			entry.Type = "RAW"
			entry.Body = line
		}

		// Write to JSONL
		bytes, err := json.Marshal(entry)
		if err == nil {
			_, _ = logFile.Write(append(bytes, '\n'))
			_ = logFile.Sync()
		}

		// Handle !version requests on the channel
		if entry.Type == "PRIVMSG" {
			if strings.HasPrefix(strings.TrimSpace(entry.Body), "!version") {
				replyTarget := entry.Target
				if !strings.HasPrefix(replyTarget, "#") {
					replyTarget = entry.Sender
				}
				replyBody := fmt.Sprintf("[scribe] version: %s", GetVersion())
				cmd := fmt.Sprintf("PRIVMSG %s :%s\r\n", replyTarget, replyBody)
				_, _ = conn.Write([]byte(cmd))
			}
		}
	}

	return nil
}
