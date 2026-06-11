package fam

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
)

// HistoryEntry represents a single parsed event recorded by the scribe bot.
type HistoryEntry struct {
	Timestamp string `json:"timestamp"`
	Sender    string `json:"sender"`
	Type      string `json:"type"`
	Target    string `json:"target"`
	Body      string `json:"body"`
}

// ScribeCmd executes the Go-based Scribe IRC bot.
func ScribeCmd(args []string, out io.Writer) error {
	var server, channel, historyFile string
	server = "localhost:6667"
	channel = "#botfam,#ccrep"

	historyFile = os.Getenv("COLLAB_HISTORY")
	if historyFile == "" {
		info, err := (Resolver{WorkDir: "."}).Resolve()
		if err == nil && info.Root != "" {
			historyFile = filepath.Join(info.Root, "botfam-collab", "history.jsonl")
		} else {
			return errors.New("COLLAB_HISTORY is unset and family root could not be resolved")
		}
	}

	// Parse arguments
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--server="):
			server = strings.TrimPrefix(arg, "--server=")
		case arg == "--server":
			i++
			if i < len(args) {
				server = args[i]
			}
		case strings.HasPrefix(arg, "--channel="):
			channel = strings.TrimPrefix(arg, "--channel=")
		case arg == "--channel":
			i++
			if i < len(args) {
				channel = args[i]
			}
		case strings.HasPrefix(arg, "--file="):
			historyFile = strings.TrimPrefix(arg, "--file=")
		case arg == "--file":
			i++
			if i < len(args) {
				historyFile = args[i]
			}
		default:
			return fmt.Errorf("unknown scribe argument %q", arg)
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

	fmt.Fprintf(out, "* Scribe bot starting. Server: %s, Channel: %s, File: %s\n", server, channel, historyFile)

	// Connect to IRC server
	conn, err := net.DialTimeout("tcp", server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("scribe connection failed: %w", err)
	}
	defer conn.Close()

	// Send initial commands (using stable nick)
	nick := "scribe"
	_, _ = fmt.Fprintf(conn, "NICK %s\r\n", nick)
	_, _ = fmt.Fprintf(conn, "USER %s 0 * :botfam scribe bot\r\n", nick)
	_, _ = fmt.Fprintf(conn, "JOIN %s\r\n", channel)

	privRe := regexp.MustCompile(`^:([^!\s]+)\S*\s+PRIVMSG\s+(\S+)\s+:(.*)$`)
	eventRe := regexp.MustCompile(`^:([^!\s]+)\S*\s+(JOIN|PART|QUIT|NICK)\b\s*:?(\S*)`)
	tallyRe := regexp.MustCompile(`^!tally\s+(?:id=)?(\S+)`)
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

		// Handle !tally requests on the channel
		if entry.Type == "PRIVMSG" {
			if m := tallyRe.FindStringSubmatch(entry.Body); m != nil {
				proposalID := m[1]
				summary, err := TallyProposal(historyFile, proposalID)
				if err != nil {
					summary = fmt.Sprintf("Error calculating tally for %q: %v", proposalID, err)
				}
				replyTarget := entry.Target
				if !strings.HasPrefix(replyTarget, "#") {
					replyTarget = entry.Sender
				}
				cmd := fmt.Sprintf("PRIVMSG %s :%s\r\n", replyTarget, summary)
				_, _ = conn.Write([]byte(cmd))
			}
		}
	}

	return nil
}
