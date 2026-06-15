package ingest

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

// ircTexts drains every irc message body in the spool (new/ and cur/). Nothing
// in these ingester tests acks, so delivered messages stay in new/, but reading
// both boxes keeps the helper robust.
func ircTexts(t *testing.T, spoolDir string) []string {
	t.Helper()
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, list := range []func() ([]mailbox.Entry, error){sp.ListNew, sp.ListCur} {
		ents, err := list()
		if err != nil {
			continue
		}
		for _, e := range ents {
			m, err := sp.Read(e)
			if err != nil {
				continue
			}
			if m.Source == mailbox.SourceIRC {
				out = append(out, m.Body)
			}
		}
	}
	return out
}

// waitForIRCEvent polls the spool until an irc message whose body contains substr
// appears, or fails after the deadline.
func waitForIRCEvent(t *testing.T, spoolDir, substr string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, txt := range ircTexts(t, spoolDir) {
			if strings.Contains(txt, substr) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("irc event containing %q did not appear within %s", substr, within)
}

func spoolHasText(t *testing.T, spoolDir, substr string) bool {
	t.Helper()
	for _, txt := range ircTexts(t, spoolDir) {
		if strings.Contains(txt, substr) {
			return true
		}
	}
	return false
}

func TestIngesterTailsIRCToSpool(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	spoolDir := filepath.Join(dir, "spool")

	// A line that already exists before the ingester starts: a fresh spool seeds
	// to EOF, so this must NOT be ingested (no stale backlog dump).
	appendToLog(t, logPath, "[00:00:00] #botfam <agy-botfam> claude: OLD pre-start\n")

	ing := NewIngester(spoolDir, 20*time.Millisecond, NewIRCPoller(logPath, "claude-botfam"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ing.Run(ctx) }()

	time.Sleep(60 * time.Millisecond) // let it acquire the lock and seed to EOF

	appendToLog(t, logPath, "[00:00:01] #botfam <agy-botfam> claude: NEW after-start\n")
	waitForIRCEvent(t, spoolDir, "NEW after-start", 2*time.Second)

	if spoolHasText(t, spoolDir, "OLD pre-start") {
		t.Error("fresh ingester ingested pre-start backlog (should seed to EOF)")
	}
}

func TestIngesterColdStartCatchUp(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	spoolDir := filepath.Join(dir, "spool")

	// Event that arrived "while the host was down".
	appendToLog(t, logPath, "[00:00:00] #botfam <agy-botfam> claude: MISSED while down\n")

	// Pre-existing cursors.json at IRC offset 0 => resume from the start of the
	// log, not EOF (a non-fresh spool is not re-seeded).
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.WriteCursors(mailbox.Cursors{IRCOffset: 0}); err != nil {
		t.Fatal(err)
	}

	// A deliberately long interval: if the event shows up quickly it can only be
	// from the one-shot synchronous cold-start poll, not the ticker.
	ing := NewIngester(spoolDir, 10*time.Second, NewIRCPoller(logPath, "claude-botfam"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ing.Run(ctx) }()

	waitForIRCEvent(t, spoolDir, "MISSED while down", 1*time.Second)
}

// TestIngesterFailsFastOnMissingFamRoot: a spoolDir whose fam root (grandparent)
// is absent must error immediately rather than MkdirAll a bogus spool tree the
// reader never watches (#263).
func TestIngesterFailsFastOnMissingFamRoot(t *testing.T) {
	// Grandparent of spoolDir is <tmp>/no-fam-root, which is never created.
	spoolDir := filepath.Join(t.TempDir(), "no-fam-root", "spool", "claude")
	ing := NewIngester(spoolDir, 20*time.Millisecond)
	err := ing.Run(context.Background())
	if err == nil {
		t.Fatal("expected a fail-fast error for a missing fam root")
	}
	if !strings.Contains(err.Error(), "fam root") {
		t.Errorf("error should name the missing fam root, got: %v", err)
	}
	if _, statErr := os.Stat(spoolDir); !os.IsNotExist(statErr) {
		t.Errorf("ingester fabricated a spool tree at %s despite the missing fam root", spoolDir)
	}
}

func TestWriterLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.lock")

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
