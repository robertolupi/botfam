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
)

// IrcWaitCmd implements the native wake-watcher command.
func IrcWaitCmd(args []string, out io.Writer) error {
	var nick, logPath string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--file="):
			logPath = strings.TrimPrefix(arg, "--file=")
		case arg == "--file":
			i++
			if i < len(args) {
				logPath = args[i]
			}
		case strings.HasPrefix(arg, "--nick="):
			nick = strings.TrimPrefix(arg, "--nick=")
		case arg == "--nick":
			i++
			if i < len(args) {
				nick = args[i]
			}
		default:
			return fmt.Errorf("unknown argument %q", arg)
		}
	}

	if logPath == "" {
		if nick == "" {
			return errors.New("missing required argument: --nick <name> or --file <path>")
		}
		logPath = filepath.Join("scratch", "irc", nick, "log")
	}

	// Wait for file to exist
	for {
		_, err := os.Stat(logPath)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	stat, err := os.Stat(logPath)
	if err != nil {
		return fmt.Errorf("failed to stat log file: %w", err)
	}
	currentSize := stat.Size()

	for {
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
				for _, line := range matchedLines {
					fmt.Fprintln(out, line)
				}
				return nil
			}
		} else if stat.Size() < currentSize {
			// File was truncated/rotated
			currentSize = stat.Size()
		}
	}
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
