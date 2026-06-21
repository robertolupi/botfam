package ops

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/eventdelivery/store"
)

// seedInspectSession creates a session.db under dir with one scope generation,
// one run (with the given status), and one completed work item + dispatch +
// artifact, for the inspection commands to read.
func seedInspectSession(t *testing.T, dir, runStatus, repo string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.ApplyMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO scope_generations (repo, milestone_id, scope_hash, source_query) VALUES (?, NULL, 'h', 'issues:1')`, repo)
	exec(`INSERT INTO runs (id, session_id, status) VALUES ('run-1', 'sid', ?)`, runStatus)
	exec(`INSERT INTO work_items (id, kind, source_id, title, scope_generation, state) VALUES ('wi-1','resolve_issue','1','Fix the thing',1,'completed')`)
	exec(`INSERT INTO dispatches (id, work_item_id, worker_id, scope_generation) VALUES ('d1','wi-1','worker-xyz',1)`)
	exec(`INSERT INTO artifacts (id, work_item_id, kind, uri, sha256) VALUES ('a1','wi-1','run_capture','artifacts/wi-1','')`)
}

func TestDeriveSessionState(t *testing.T) {
	for _, tc := range []struct {
		lastStatus string
		live       bool
		ended      bool
		want       string
	}{
		{"", false, false, "new"},
		{"running", true, false, "running"},
		{"running", false, false, "crashed"},
		{"completed", false, false, "completed"},
		{"completed", true, false, "completed"},
		{"completed", false, true, "ended"}, // ended overrides
		{"running", true, true, "ended"},    // ended overrides even a live run
	} {
		if got := deriveSessionState(tc.lastStatus, tc.live, tc.ended); got != tc.want {
			t.Errorf("deriveSessionState(%q, %v, %v) = %q, want %q", tc.lastStatus, tc.live, tc.ended, got, tc.want)
		}
	}
}

func TestRunSprintEnd(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s-end")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// `end` commits a snapshot, so the session dir must be a git repo.
	for _, args := range [][]string{{"init"}, {"config", "user.name", "t"}, {"config", "user.email", "t@t.invalid"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	seedInspectSession(t, dir, "completed", "repoA") // no live supervisor for repoA

	var buf bytes.Buffer
	if err := runSprintEnd(context.Background(), &buf, dir, "s-end", 2*time.Second); err != nil {
		t.Fatalf("runSprintEnd: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("ended")) {
		t.Errorf("end output missing 'ended': %s", buf.String())
	}

	db, err := store.Open(filepath.Join(dir, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var endedAt string
	if err := db.QueryRow(`SELECT value FROM session_meta WHERE key = 'ended_at'`).Scan(&endedAt); err != nil {
		t.Fatalf("ended_at not recorded: %v", err)
	}
	if endedAt == "" {
		t.Error("ended_at is empty")
	}

	// ls + show now reflect the ended state.
	var lsBuf bytes.Buffer
	if err := runSprintLs(context.Background(), &lsBuf, filepath.Dir(dir), func(context.Context, string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(lsBuf.Bytes(), []byte("ended")) {
		t.Errorf("ls missing ended state:\n%s", lsBuf.String())
	}
	var showBuf bytes.Buffer
	if err := runSprintShow(context.Background(), &showBuf, dir, "s-end"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(showBuf.Bytes(), []byte("Ended:")) {
		t.Errorf("show missing Ended line:\n%s", showBuf.String())
	}
}

func TestRunSprintLs(t *testing.T) {
	root := t.TempDir()
	seedInspectSession(t, filepath.Join(root, "s-done"), "completed", "repoA")
	seedInspectSession(t, filepath.Join(root, "s-live"), "running", "repoB")
	seedInspectSession(t, filepath.Join(root, "s-crash"), "running", "repoC")

	// Only repoB has a live supervisor.
	live := func(_ context.Context, repo string) bool { return repo == "repoB" }

	var buf bytes.Buffer
	if err := runSprintLs(context.Background(), &buf, root, live); err != nil {
		t.Fatalf("runSprintLs: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"s-done", "completed",
		"s-live", "running",
		"s-crash", "crashed",
		"issues:1",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("ls output missing %q:\n%s", want, out)
		}
	}
}

func TestRunSprintLsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := runSprintLs(context.Background(), &buf, filepath.Join(t.TempDir(), "nope"), func(context.Context, string) bool { return false }); err != nil {
		t.Fatalf("runSprintLs: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("No sprint sessions")) {
		t.Errorf("expected empty message, got: %s", buf.String())
	}
}

func TestRunSprintShow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s1")
	seedInspectSession(t, dir, "completed", "repoA")

	var buf bytes.Buffer
	if err := runSprintShow(context.Background(), &buf, dir, "s1"); err != nil {
		t.Fatalf("runSprintShow: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Session: s1", "Repo:    repoA", "issues:1",
		"Runs:", "run-1", "completed",
		"Work items:", "resolve_issue", "Fix the thing",
		"Dispatches:", "worker-xyz",
		"Artifacts:", "run_capture", "artifacts/wi-1",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}
