package irc

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReplayHistory(t *testing.T) {
	tempDir := t.TempDir()
	historyPath := filepath.Join(tempDir, "history.jsonl")

	// Helper to write a history entry
	writeEntry := func(sender, evType, target, body string) {
		entry := HistoryEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Sender:    sender,
			Type:      evType,
			Target:    target,
			Body:      body,
		}
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(historyPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, _ = f.Write(append(data, '\n'))
	}

	// Write mock events
	writeEntry("other", "PRIVMSG", "#botfam", "hello from other")
	writeEntry("agy", "JOIN", "#botfam", "")
	writeEntry("agy", "PRIVMSG", "#botfam", "hello from agy")
	writeEntry("other2", "PRIVMSG", "#botfam", "hello from other2")
	writeEntry("agy", "PART", "#botfam", "")
	writeEntry("other", "PRIVMSG", "#botfam", "hello after agy parted")

	// Case 1: lines mode (last 2 matching lines, excluding agy's own messages/events)
	lines, nextOffset, err := ReplayHistory(historyPath, "agy", "agy-botfam", "lines:2", []string{"#botfam"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "hello from other2") {
		t.Errorf("expected line to contain 'hello from other2', got %q", lines[0])
	}
	if !strings.Contains(lines[1], "hello after agy parted") {
		t.Errorf("expected line to contain 'hello after agy parted', got %q", lines[1])
	}

	// Case 2: last_part mode
	lines, _, err = ReplayHistory(historyPath, "agy", "agy-botfam", "last_part", []string{"#botfam"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line after last part, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "hello after agy parted") {
		t.Errorf("expected line to contain 'hello after agy parted', got %q", lines[0])
	}

	// Case 3: last_seen mode
	lines, _, err = ReplayHistory(historyPath, "agy", "agy-botfam", "last_seen", []string{"#botfam"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line after last seen, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "hello after agy parted") {
		t.Errorf("expected line to contain 'hello after agy parted', got %q", lines[0])
	}

	// Case 4: offset mode (replay from byte offset just before 'hello from other2')
	// Let's get the offset by parsing the file
	f, err := os.Open(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var offset int64 = 0
	scanner := bufio.NewScanner(f)
	for i := 0; i < 3 && scanner.Scan(); i++ {
		offset += int64(len(scanner.Text()) + 1)
	}

	lines, nextOffset2, err := ReplayHistory(historyPath, "agy", "agy-botfam", "offset:"+strconv.FormatInt(offset, 10), []string{"#botfam"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 lines from offset, got %d: %v", len(lines), lines)
	}
	if nextOffset2 != nextOffset {
		t.Errorf("expected nextOffset to be %d, got %d", nextOffset, nextOffset2)
	}

	// Case 5: empty since (default to last 100 lines)
	lines, _, err = ReplayHistory(historyPath, "agy", "agy-botfam", "", []string{"#botfam"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Errorf("expected 3 lines for empty since, got %d: %v", len(lines), lines)
	}

	// Case 6: unrecognized since (should return error)
	_, _, err = ReplayHistory(historyPath, "agy", "agy-botfam", "24h", []string{"#botfam"})
	if err == nil {
		t.Error("expected error for unrecognized since '24h', got nil")
	}
}
