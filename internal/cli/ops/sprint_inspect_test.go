package ops

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

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
		want       string
	}{
		{"", false, "new"},
		{"running", true, "running"},
		{"running", false, "crashed"},
		{"completed", false, "completed"},
		{"completed", true, "completed"},
	} {
		if got := deriveSessionState(tc.lastStatus, tc.live); got != tc.want {
			t.Errorf("deriveSessionState(%q, %v) = %q, want %q", tc.lastStatus, tc.live, got, tc.want)
		}
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
