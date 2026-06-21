package ops

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/eventdelivery/store"
)

func TestWorkerCommandFor(t *testing.T) {
	// Override is used verbatim, with no implied capture dir.
	argv, capture, err := workerCommandFor("my-worker --flag x", "/s", "wi1", "resolve_issue", "42")
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if capture != "" {
		t.Errorf("override captureDir = %q, want empty", capture)
	}
	if got := strings.Join(argv, " "); got != "my-worker --flag x" {
		t.Errorf("override argv = %q", got)
	}

	// Default worker for a resolve_issue item with a numeric source_id.
	argv, capture, err = workerCommandFor("", "/s", "wi1", "resolve_issue", "42")
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	wantCapture := filepath.Join("/s", "artifacts", "wi1")
	wantArgv := []string{"botfam", "run", "--issue", "42", "--capture-dir", wantCapture}
	if strings.Join(argv, " ") != strings.Join(wantArgv, " ") {
		t.Errorf("default argv = %v, want %v", argv, wantArgv)
	}
	if capture != wantCapture {
		t.Errorf("default captureDir = %q, want %q", capture, wantCapture)
	}

	// No default worker for a non-resolve_issue kind without an override.
	if _, _, err := workerCommandFor("", "/s", "wi1", "reply_to_issue", "42"); err == nil {
		t.Error("expected error for non-resolve_issue kind without --worker-command")
	}
	// A resolve_issue item must carry a numeric source_id for the default command.
	if _, _, err := workerCommandFor("", "/s", "wi1", "resolve_issue", "issue-1"); err == nil {
		t.Error("expected error for non-numeric source_id")
	}
}

func TestRecordRunCaptureArtifact(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.ApplyMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO work_items (id, kind, source_id, title, scope_generation, state) VALUES ('wi1','resolve_issue','42','T',1,'running')`); err != nil {
		t.Fatal(err)
	}

	// No-op for an empty capture dir (the override-worker path).
	recordRunCaptureArtifact(context.Background(), db, "wi1", "")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artifacts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("artifacts = %d after empty capture dir, want 0", n)
	}

	// Records a relative run_capture pointer for a real capture dir.
	recordRunCaptureArtifact(context.Background(), db, "wi1", filepath.Join(dir, "artifacts", "wi1"))
	var kind, uri string
	if err := db.QueryRow(`SELECT kind, uri FROM artifacts WHERE work_item_id = 'wi1'`).Scan(&kind, &uri); err != nil {
		t.Fatal(err)
	}
	if kind != "run_capture" {
		t.Errorf("artifact kind = %q, want run_capture", kind)
	}
	if uri != filepath.Join("artifacts", "wi1") {
		t.Errorf("artifact uri = %q, want artifacts/wi1", uri)
	}
}
