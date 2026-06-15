package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/mailbox"
)

func seedSpool(t *testing.T) (dir string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&mailbox.Message{
		Source: mailbox.SourceIRC, From: "agy", To: "#botfam", Subject: "claude: ping", Body: "claude: ping",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&mailbox.Message{
		Source: mailbox.SourceForge, Subject: `issue: botfam/botfam#230 "feat: x"`, Body: "http://gitea:3000/botfam/botfam/issues/230",
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// parseWaitOutput splits JSONL `wait` output into surfaced messages and the
// trailing meta summary.
func parseWaitOutput(t *testing.T, out string) (msgs []emittedMessage, summary waitSummary) {
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
		if probe.Source == "meta" {
			if err := json.Unmarshal([]byte(line), &summary); err != nil {
				t.Fatal(err)
			}
			continue
		}
		var m emittedMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	return msgs, summary
}

func TestWaitDrainsExistingEvents(t *testing.T) {
	dir := seedSpool(t)
	var out bytes.Buffer
	if err := runWait(&out, dir, parseSources("irc,forge"), 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	msgs, summary := parseWaitOutput(t, out.String())
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (drain-both)", len(msgs))
	}
	if summary.Woke != mailbox.SourceIRC {
		t.Errorf("woke = %q, want irc (first arrival)", summary.Woke)
	}
	if summary.TimedOut {
		t.Error("timed_out = true, want false")
	}
	if summary.Count != 2 {
		t.Errorf("count = %d, want 2", summary.Count)
	}
}

func TestWaitSourceFilter(t *testing.T) {
	dir := seedSpool(t)
	var out bytes.Buffer
	if err := runWait(&out, dir, parseSources("forge"), 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	msgs, summary := parseWaitOutput(t, out.String())
	if len(msgs) != 1 || msgs[0].Source != mailbox.SourceForge {
		t.Fatalf("source filter surfaced %d messages, want only forge", len(msgs))
	}
	if summary.Woke != mailbox.SourceForge {
		t.Errorf("woke = %q, want forge", summary.Woke)
	}
}

// TestWaitAcksDrainedMessages: a read moves new/->cur/ (the ack), so a second
// wait with nothing new must time out cleanly rather than re-surface the batch.
func TestWaitAcksDrainedMessages(t *testing.T) {
	dir := seedSpool(t)
	var first bytes.Buffer
	if err := runWait(&first, dir, parseSources("irc,forge"), 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := parseWaitOutput(t, first.String()); len(msgs) != 2 {
		t.Fatalf("first drain surfaced %d, want 2", len(msgs))
	}

	var second bytes.Buffer
	if err := runWait(&second, dir, parseSources("irc,forge"), 200*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	msgs, summary := parseWaitOutput(t, second.String())
	if len(msgs) != 0 {
		t.Fatalf("re-drain surfaced %d already-acked messages, want 0", len(msgs))
	}
	if !summary.TimedOut {
		t.Error("timed_out = false, want true (nothing new)")
	}
}

func TestWaitBlocksThenWakes(t *testing.T) {
	dir := seedSpool(t)
	// Drain the seeded backlog so new/ is empty before we block.
	if err := runWait(io.Discard, dir, parseSources("irc,forge"), 2*time.Second, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	done := make(chan string, 1)
	go func() {
		var out bytes.Buffer
		_ = runWait(&out, dir, parseSources("irc,forge"), 3*time.Second, 10*time.Millisecond)
		done <- out.String()
	}()

	// Give the waiter a beat to start blocking, then deliver.
	time.Sleep(50 * time.Millisecond)
	sp, err := mailbox.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&mailbox.Message{Source: mailbox.SourceForge, Subject: "woke you", Body: "woke you"}); err != nil {
		t.Fatal(err)
	}

	select {
	case out := <-done:
		msgs, summary := parseWaitOutput(t, out)
		if len(msgs) != 1 || !strings.Contains(msgs[0].Subject, "woke you") {
			t.Fatalf("woke with %d messages, want the delivered one", len(msgs))
		}
		if summary.TimedOut {
			t.Error("timed_out = true, want false (woke on delivery)")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("wait did not wake on delivery")
	}
}
