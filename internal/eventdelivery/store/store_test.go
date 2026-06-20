package store

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMigrationsCreateInitialSchema(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t, ctx)
	defer db.Close()

	for _, table := range []string{
		"runs",
		"raw_observations",
		"work_items",
		"work_item_state_transitions",
		"dispatches",
		"forge_action_outbox",
		"action_attempts",
		"git_action_log",
		"scope_generations",
		"scope_membership",
		"relation_predicates",
		"artifacts",
		"session_registry",
	} {
		t.Run(table, func(t *testing.T) {
			var name string
			if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
				t.Fatalf("table %s missing: %v", table, err)
			}
		})
	}

	var predicates int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM relation_predicates`).Scan(&predicates); err != nil {
		t.Fatal(err)
	}
	if predicates != 6 {
		t.Fatalf("relation predicates = %d, want 6", predicates)
	}
}

func TestForgeActionOutboxDedupsWithoutFencingToken(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t, ctx)
	defer db.Close()

	insertWorkItem(t, ctx, db)
	first, err := EnqueueForgeAction(ctx, db, "outbox-1", "work-1", "close-issue", "forge_issue_write", `{"state":"closed"}`, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnqueueForgeAction(ctx, db, "outbox-2", "work-1", "close-issue", "forge_issue_write", `{"state":"closed"}`, 8)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "outbox-1" || first.Deduped {
		t.Fatalf("first insert = %+v, want non-deduped outbox-1", first)
	}
	if second.ID != "outbox-1" || !second.Deduped {
		t.Fatalf("second insert = %+v, want deduped outbox-1", second)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM forge_action_outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("outbox rows = %d, want 1", count)
	}
}

func TestDumpRoundTripIncludesWALCommittedTransaction(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	livePath := filepath.Join(dir, "session.db")
	restoredPath := filepath.Join(dir, "restored.db")

	db, err := Open(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_autocheckpoint = 0`); err != nil {
		t.Fatal(err)
	}
	insertWorkItem(t, ctx, db)
	if _, err := EnqueueForgeAction(ctx, db, "outbox-1", "work-1", "close-issue", "forge_issue_write", `{"state":"closed"}`, 7); err != nil {
		t.Fatal(err)
	}
	walPath := livePath + "-wal"
	if info, err := os.Stat(walPath); err != nil {
		t.Fatalf("expected WAL file before dump: %v", err)
	} else if info.Size() == 0 {
		t.Fatalf("expected non-empty WAL before dump")
	}
	defer db.Close()

	dump, err := Dump(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(restoredPath, dump); err != nil {
		t.Fatal(err)
	}

	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var toolName string
	if err := restored.QueryRowContext(ctx, `SELECT tool_name FROM forge_action_outbox WHERE work_item_id = ? AND action_key = ?`, "work-1", "close-issue").Scan(&toolName); err != nil {
		t.Fatal(err)
	}
	if toolName != "forge_issue_write" {
		t.Fatalf("tool name = %q, want forge_issue_write", toolName)
	}
}

func TestOpenSessionRepoCapturesCrashBeforeMigrations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	if err := WriteCompleteFile(filepath.Join(dir, "artifacts", "transcript.jsonl"), []byte("{\"event\":\"before\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := Open(filepath.Join(dir, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE crashed_state (id TEXT PRIMARY KEY); INSERT INTO crashed_state (id) VALUES ('before-migration');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var events []string
	previousHook := observeStoreEvent
	observeStoreEvent = func(event string) { events = append(events, event) }
	defer func() { observeStoreEvent = previousHook }()
	runner := &recordingRunner{t: t, events: &events}
	opened, err := OpenSessionRepo(ctx, SessionRepoOptions{
		Dir:       dir,
		RunNumber: 41,
		Runner:    runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	dumpIndex := slices.Index(events, "dump session.sql")
	statusIndex := slices.Index(events, "git status --porcelain")
	addIndex := slices.Index(events, "git add .gitignore session.sql artifacts")
	commitIndex := slices.Index(events, "git commit -m crashed-run: run 41 auto-captured state")
	migrationIndex := slices.Index(events, "migration 1")
	if dumpIndex < 0 || addIndex < 0 || commitIndex < 0 {
		t.Fatalf("events = %#v", events)
	}
	if !(dumpIndex < statusIndex && statusIndex < addIndex && addIndex < commitIndex && commitIndex < migrationIndex) {
		t.Fatalf("startup events out of order: %#v", events)
	}

	log := runGit(t, dir, "log", "--oneline", "-1")
	if !strings.Contains(log, "crashed-run: run 41 auto-captured state") {
		t.Fatalf("latest commit = %q", log)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.sql")); err != nil {
		t.Fatalf("session.sql not captured: %v", err)
	}
}

func TestEnsureSessionGitignoreIgnoresSQLiteSessionFiles(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureSessionGitignore(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{
		"session.db",
		"session.db-wal",
		"session.db-shm",
	} {
		if !strings.Contains(string(got), pattern+"\n") {
			t.Fatalf(".gitignore missing %q:\n%s", pattern, got)
		}
	}
}

func TestEnsureSessionGitignoreAppendsBindingPatterns(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureSessionGitignore(dir, "*.binding-a", "*.binding-b"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"session.db", "*.binding-a", "*.binding-b"} {
		if !strings.Contains(string(got), pattern+"\n") {
			t.Fatalf(".gitignore missing %q:\n%s", pattern, got)
		}
	}
}

func TestArtifactFilesystemDurabilityContract(t *testing.T) {
	dir := t.TempDir()
	completePath := filepath.Join(dir, "artifacts", "final.txt")
	if err := WriteCompleteFile(completePath, []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(completePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "complete\n" {
		t.Fatalf("complete artifact = %q", got)
	}
	tmpMatches, err := filepath.Glob(filepath.Join(dir, "artifacts", ".final.txt.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpMatches) != 0 {
		t.Fatalf("temporary artifact files left behind: %v", tmpMatches)
	}

	streamPath := filepath.Join(dir, "artifacts", "events.jsonl")
	if err := AppendAndSync(streamPath, []byte("{\"n\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendAndSync(streamPath, []byte("{\"n\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stream, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stream) != "{\"n\":1}\n{\"n\":2}\n" {
		t.Fatalf("stream artifact = %q", stream)
	}
}

func openMigrated(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertWorkItem(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO runs (id, session_id) VALUES ('run-1', 'session-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO work_items (id, kind, source_id, title, scope_generation) VALUES ('work-1', 'reply_to_issue', 'forge:botfam/botfam:issue:483', 'Issue 483', 1)`); err != nil {
		t.Fatal(err)
	}
}

type recordingRunner struct {
	t      *testing.T
	events *[]string
}

func (r *recordingRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	r.t.Helper()
	*r.events = append(*r.events, name+" "+strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
