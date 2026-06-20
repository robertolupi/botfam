package store

import (
	"context"
	"database/sql"
	"path/filepath"
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
	insertWorkItem(t, ctx, db)
	if _, err := EnqueueForgeAction(ctx, db, "outbox-1", "work-1", "close-issue", "forge_issue_write", `{"state":"closed"}`, 7); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

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
