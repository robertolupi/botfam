package irc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
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

// ReplayHistory reads the durable shared history log and returns matched lines
// along with the ending byte offset.
func ReplayHistory(historyPath, actor, matchNick, since string, filterChans []string) (lines []string, nextOffset int64, err error) {
	f, err := os.Open(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	fileSize := stat.Size()

	// Parse "since"
	since = strings.TrimSpace(since)
	mode := "default"
	var offsetVal int64 = 0
	var linesVal int = 100 // default to last 100 lines if since is empty

	if since != "" {
		if since == "last_part" {
			mode = "last_part"
		} else if since == "last_seen" {
			mode = "last_seen"
		} else if strings.HasPrefix(since, "offset:") {
			mode = "offset"
			valStr := strings.TrimPrefix(since, "offset:")
			v, err := strconv.ParseInt(valStr, 10, 64)
			if err != nil {
				return nil, 0, fmt.Errorf("invalid offset value %q: %w", valStr, err)
			}
			offsetVal = v
		} else if strings.HasPrefix(since, "lines:") {
			mode = "lines"
			valStr := strings.TrimPrefix(since, "lines:")
			v, err := strconv.Atoi(valStr)
			if err != nil {
				return nil, 0, fmt.Errorf("invalid lines value %q: %w", valStr, err)
			}
			linesVal = v
		} else {
			// Try parsing as number
			if v, err := strconv.ParseInt(since, 10, 64); err == nil {
				if v >= 10000 {
					mode = "offset"
					offsetVal = v
				} else {
					mode = "lines"
					linesVal = int(v)
				}
			} else {
				return nil, 0, fmt.Errorf("unrecognized since option %q (supported: last_part, last_seen, offset:N, lines:N, or a bare integer)", since)
			}
		}
	} else {
		mode = "lines"
	}

	// Helper to check channel filter
	inFilter := func(target string) bool {
		if len(filterChans) == 0 {
			return true
		}
		for _, ch := range filterChans {
			if strings.EqualFold(ch, target) {
				return true
			}
		}
		return false
	}

	// Helper to format history entry
	formatEntry := func(entry HistoryEntry) string {
		t, err := time.Parse(time.RFC3339, entry.Timestamp)
		if err != nil {
			t = time.Now()
		}
		t = t.Local()
		timeStr := t.Format("15:04:05")

		switch entry.Type {
		case "PRIVMSG":
			where := "(pm)"
			if strings.HasPrefix(entry.Target, "#") {
				where = entry.Target
			}
			return fmt.Sprintf("[%s] %s <%s> %s", timeStr, where, entry.Sender, entry.Body)
		case "JOIN", "PART", "QUIT", "NICK":
			return fmt.Sprintf("[%s] * %s %s %s", timeStr, entry.Sender, entry.Type, entry.Target)
		default:
			// RAW or other
			return fmt.Sprintf("[%s] . %s", timeStr, entry.Body)
		}
	}

	// Helper to check if sender is actor
	isOwn := func(sender string) bool {
		return sender == actor || sender == matchNick
	}

	if mode == "last_part" || mode == "last_seen" {
		// Scan file to find the last part/seen event
		var lastOffset int64 = 0
		var currentOffset int64 = 0
		reader := bufio.NewReader(f)
		for {
			line, rerr := reader.ReadString('\n')
			lineLen := int64(len(line))
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				return nil, 0, rerr
			}
			if lineLen > 0 {
				var entry HistoryEntry
				if json.Unmarshal([]byte(line), &entry) == nil {
					match := false
					if mode == "last_part" {
						match = isOwn(entry.Sender) && (entry.Type == "PART" || entry.Type == "QUIT")
					} else {
						// last_seen
						match = isOwn(entry.Sender)
					}
					if match {
						lastOffset = currentOffset + lineLen
					}
				}
				currentOffset += lineLen
			}
			if rerr != nil {
				break
			}
		}
		offsetVal = lastOffset
		mode = "offset"
	}

	if mode == "offset" {
		if offsetVal < 0 {
			offsetVal = 0
		}
		if offsetVal > fileSize {
			offsetVal = fileSize
		}
		_, err = f.Seek(offsetVal, io.SeekStart)
		if err != nil {
			return nil, 0, err
		}

		reader := bufio.NewReader(f)
		currentOffset := offsetVal
		for {
			line, rerr := reader.ReadString('\n')
			lineLen := int64(len(line))
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				return nil, 0, rerr
			}
			if lineLen > 0 && strings.HasSuffix(line, "\n") {
				var entry HistoryEntry
				if json.Unmarshal([]byte(line), &entry) == nil {
					if !isOwn(entry.Sender) && inFilter(entry.Target) {
						lines = append(lines, formatEntry(entry))
					}
				}
				currentOffset += lineLen
			}
			if rerr != nil {
				break
			}
		}
		return lines, currentOffset, nil
	}

	if mode == "lines" {
		if linesVal <= 0 {
			linesVal = 50
		}
		// Read all lines to filter and grab the last linesVal lines
		var allEntries []HistoryEntry
		var currentOffset int64 = 0

		reader := bufio.NewReader(f)
		for {
			line, rerr := reader.ReadString('\n')
			lineLen := int64(len(line))
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				return nil, 0, rerr
			}
			if lineLen > 0 && strings.HasSuffix(line, "\n") {
				var entry HistoryEntry
				if json.Unmarshal([]byte(line), &entry) == nil {
					allEntries = append(allEntries, entry)
				}
				currentOffset += lineLen
			}
			if rerr != nil {
				break
			}
		}

		// Filter entries
		var filteredLines []string
		for _, entry := range allEntries {
			if !isOwn(entry.Sender) && inFilter(entry.Target) {
				filteredLines = append(filteredLines, formatEntry(entry))
			}
		}

		if len(filteredLines) > linesVal {
			filteredLines = filteredLines[len(filteredLines)-linesVal:]
		}
		return filteredLines, currentOffset, nil
	}

	return []string{}, fileSize, nil
}
