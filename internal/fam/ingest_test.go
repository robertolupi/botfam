package fam

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/mailbox"
)

func appendToLog(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// waitForIRCEvent polls the mailbox until an irc event whose text contains substr
// appears, or fails after the deadline.
func waitForIRCEvent(t *testing.T, mboxPath, substr string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		evs, _, err := mailbox.ReadFrom(mboxPath, 0)
		if err == nil {
			for _, ev := range evs {
				if ev.Source == mailbox.SourceIRC && strings.Contains(ev.Text, substr) {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("irc event containing %q did not appear within %s", substr, within)
}

func mailboxHasText(t *testing.T, mboxPath, substr string) bool {
	t.Helper()
	evs, _, err := mailbox.ReadFrom(mboxPath, 0)
	if err != nil {
		return false
	}
	for _, ev := range evs {
		if strings.Contains(ev.Text, substr) {
			return true
		}
	}
	return false
}

func TestIngesterTailsIRCToMailbox(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	mboxPath := filepath.Join(dir, "claude.mailbox")

	// A line that already exists before the ingester starts: a fresh mailbox
	// seeds to EOF, so this must NOT be ingested (no stale backlog dump).
	appendToLog(t, logPath, "[00:00:00] #botfam <agy-botfam> claude: OLD pre-start\n")

	ing := NewIngester(mboxPath, 20*time.Millisecond, NewIRCPoller(logPath, "claude-botfam"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ing.Run(ctx) }()

	time.Sleep(60 * time.Millisecond) // let it acquire the lock and seed to EOF

	appendToLog(t, logPath, "[00:00:01] #botfam <agy-botfam> claude: NEW after-start\n")
	waitForIRCEvent(t, mboxPath, "NEW after-start", 2*time.Second)

	if mailboxHasText(t, mboxPath, "OLD pre-start") {
		t.Error("fresh ingester ingested pre-start backlog (should seed to EOF)")
	}
}

func TestIngesterColdStartCatchUp(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	mboxPath := filepath.Join(dir, "claude.mailbox")

	// Event that arrived "while the host was down".
	appendToLog(t, logPath, "[00:00:00] #botfam <agy-botfam> claude: MISSED while down\n")

	// Pre-existing mailbox with a meta cursor at IRC offset 0 => resume from the
	// start of the log, not EOF.
	w, err := mailbox.OpenWriter(mboxPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Checkpoint(mailbox.Cursors{IRCLogOffset: 0}); err != nil {
		t.Fatal(err)
	}
	w.Close()

	// A deliberately long interval: if the event shows up quickly it can only be
	// from the one-shot synchronous cold-start poll, not the ticker.
	ing := NewIngester(mboxPath, 10*time.Second, NewIRCPoller(logPath, "claude-botfam"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ing.Run(ctx) }()

	waitForIRCEvent(t, mboxPath, "MISSED while down", 1*time.Second)
}

func TestWriterLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.mailbox.lock")

	l1, err := acquireWriterLock(context.Background(), path, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	// A second acquisition must block; with a short ctx it fails rather than
	// stealing the lock.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := acquireWriterLock(ctx, path, 10*time.Millisecond); err == nil {
		t.Fatal("second writer acquired the lock while the first held it")
	}

	// After release, it is acquirable again.
	l1.release()
	l2, err := acquireWriterLock(context.Background(), path, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("lock not reacquirable after release: %v", err)
	}
	l2.release()
}

func TestParseIRCLine(t *testing.T) {
	target, nick, text := parseIRCLine("[20:28:38] #botfam <claude-botfam> hello agy")
	if target != "#botfam" {
		t.Errorf("target = %q, want #botfam", target)
	}
	if nick != "claude-botfam" {
		t.Errorf("nick = %q, want claude-botfam", nick)
	}
	if text == "" || !strings.Contains(text, "hello agy") {
		t.Errorf("text = %q, want the full line", text)
	}
}
