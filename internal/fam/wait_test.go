package fam

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/mailbox"
)

func seedMailbox(t *testing.T) (path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "claude.mailbox")
	w, err := mailbox.OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(mailbox.Event{Source: mailbox.SourceIRC, Target: "#botfam", Nick: "agy", Text: "claude: ping"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(mailbox.Event{Source: mailbox.SourceForge, Repo: "botfam/botfam", Number: 230, Title: "feat: x"}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return path
}

// parseWaitOutput splits JSONL `wait` output into surfaced events and the
// trailing meta summary.
func parseWaitOutput(t *testing.T, out string) (events []mailbox.Event, summary waitSummary) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var probe struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("non-JSON output line %q: %v", line, err)
		}
		if probe.Source == mailbox.SourceMeta {
			if err := json.Unmarshal([]byte(line), &summary); err != nil {
				t.Fatal(err)
			}
			continue
		}
		var ev mailbox.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	return events, summary
}

func TestWaitDrainsExistingEvents(t *testing.T) {
	path := seedMailbox(t)
	var out bytes.Buffer
	if err := runWait(&out, path, 0, parseSources("irc,forge"), 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	events, summary := parseWaitOutput(t, out.String())
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (drain-both)", len(events))
	}
	if summary.Woke != mailbox.SourceIRC {
		t.Errorf("woke = %q, want irc (first arrival)", summary.Woke)
	}
	if summary.TimedOut {
		t.Error("timed_out = true, want false")
	}
	if summary.Offset <= 0 {
		t.Errorf("summary offset = %d, want EOF > 0 for re-arm", summary.Offset)
	}
	if summary.Seqs[mailbox.SourceForge] != 1 {
		t.Errorf("forge seq in summary = %d, want 1", summary.Seqs[mailbox.SourceForge])
	}
}

func TestWaitSourceFilter(t *testing.T) {
	path := seedMailbox(t)
	var out bytes.Buffer
	if err := runWait(&out, path, 0, parseSources("forge"), 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	events, summary := parseWaitOutput(t, out.String())
	if len(events) != 1 || events[0].Source != mailbox.SourceForge {
		t.Fatalf("source filter surfaced %d events, want only forge", len(events))
	}
	if summary.Woke != mailbox.SourceForge {
		t.Errorf("woke = %q, want forge", summary.Woke)
	}
}

func TestWaitDefaultOffsetIsEOF(t *testing.T) {
	path := seedMailbox(t)
	var out bytes.Buffer
	// from<0 => start at EOF, so the pre-existing events are NOT surfaced; with
	// nothing new it must time out cleanly.
	if err := runWait(&out, path, -1, parseSources("irc,forge"), 200*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	events, summary := parseWaitOutput(t, out.String())
	if len(events) != 0 {
		t.Fatalf("EOF default surfaced %d stale events, want 0", len(events))
	}
	if !summary.TimedOut {
		t.Error("timed_out = false, want true (nothing new)")
	}
}

func TestWaitResumeFromOffset(t *testing.T) {
	path := seedMailbox(t)
	// First drain from 0, capture the re-arm offset.
	var first bytes.Buffer
	if err := runWait(&first, path, 0, parseSources("irc,forge"), 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	_, summary := parseWaitOutput(t, first.String())

	// Append a new event past that offset.
	w, _ := mailbox.OpenWriter(path)
	w.Append(mailbox.Event{Source: mailbox.SourceIRC, Text: "later"})
	w.Close()

	// Resume from the captured offset: only the new event should appear.
	var second bytes.Buffer
	if err := runWait(&second, path, summary.Offset, parseSources("irc,forge"), 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	events, _ := parseWaitOutput(t, second.String())
	if len(events) != 1 || events[0].Text != "later" {
		t.Fatalf("resume surfaced %d events, want only the new one", len(events))
	}
}

func TestWaitBlocksThenWakes(t *testing.T) {
	path := seedMailbox(t)
	fi := mailboxSize(t, path)

	done := make(chan string, 1)
	go func() {
		var out bytes.Buffer
		_ = runWait(&out, path, fi, parseSources("irc,forge"), 3*time.Second, 10*time.Millisecond)
		done <- out.String()
	}()

	// Give the waiter a beat to start blocking, then append.
	time.Sleep(50 * time.Millisecond)
	w, _ := mailbox.OpenWriter(path)
	w.Append(mailbox.Event{Source: mailbox.SourceForge, Number: 999, Title: "woke you"})
	w.Close()

	select {
	case out := <-done:
		events, summary := parseWaitOutput(t, out)
		if len(events) != 1 || events[0].Number != 999 {
			t.Fatalf("woke with %d events, want the appended one", len(events))
		}
		if summary.TimedOut {
			t.Error("timed_out = true, want false (woke on append)")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("wait did not wake on append")
	}
}

func mailboxSize(t *testing.T, path string) int64 {
	t.Helper()
	_, next, err := mailbox.ReadFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
