// Package irc is the dependency-free leaf holding the reusable IRC log domain:
// reading and waiting on the client log, replaying durable history, and parsing
// channel lists. Both internal/cli (the irc-client/irc-wait/scribe commands) and
// internal/mcp (the irc_read/irc_wait/irc_replay tools) depend on it, so neither
// has to import the other (#311).
package irc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

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
				if IsMatchingLine(line, nick) {
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

func IsMatchingLine(line, nick string) bool {
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

// ParseChannels splits a comma-separated channel list into a normalized slice
// and returns its primary (first) channel, falling back to fallback when empty.
func ParseChannels(channelStr, fallback string) (channels []string, primary string) {
	for _, ch := range strings.Split(channelStr, ",") {
		ch = strings.TrimSpace(ch)
		if ch != "" {
			channels = append(channels, ch)
		}
	}
	if len(channels) == 0 {
		channels = []string{fallback}
	}
	return channels, channels[0]
}
