package workerchannel

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"github.com/robertolupi/botfam/internal/eventdelivery/store"
)

func TestProposeForgeActionDedupesCommittedResponse(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t, ctx)
	defer db.Close()
	insertWorkItem(t, ctx, db)

	calls := 0
	svc := Service{
		DB: db,
		Executor: ForgeExecutorFunc(func(context.Context, string, string) (string, error) {
			calls++
			return `{"issue":498,"state":"closed"}`, nil
		}),
	}
	req := connect.NewRequest(&pb.ForgeAction{
		WorkItemId:    "work-1",
		ActionKey:     "close-issue",
		ToolName:      "forge_issue_write",
		ArgumentsJson: `{"state":"closed"}`,
	})
	req.Header().Set(FencingTokenHeader, "7")
	first, err := svc.ProposeForgeAction(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req = connect.NewRequest(req.Msg)
	req.Header().Set(FencingTokenHeader, "8")
	second, err := svc.ProposeForgeAction(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("executor calls = %d, want 1", calls)
	}
	if !first.Msg.GetCommitted() || first.Msg.GetDeduped() {
		t.Fatalf("first ack = %+v, want committed non-deduped", first.Msg)
	}
	if !second.Msg.GetCommitted() || !second.Msg.GetDeduped() || second.Msg.GetResponseJson() != `{"issue":498,"state":"closed"}` {
		t.Fatalf("second ack = %+v, want deduped cached response", second.Msg)
	}
	var storedToken uint64
	if err := db.QueryRowContext(ctx, `SELECT fencing_token FROM action_attempts WHERE outbox_id = ?`, first.Msg.GetOutboxId()).Scan(&storedToken); err != nil {
		t.Fatal(err)
	}
	if storedToken != 7 {
		t.Fatalf("attempt fencing token = %d, want 7", storedToken)
	}
}

func TestProposeForgeActionRetriesDedupedUncommittedAction(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t, ctx)
	defer db.Close()
	insertWorkItem(t, ctx, db)

	calls := 0
	svc := Service{
		DB: db,
		Executor: ForgeExecutorFunc(func(context.Context, string, string) (string, error) {
			calls++
			if calls == 1 {
				return "", errors.New("temporary forge failure")
			}
			return `{"retry":true}`, nil
		}),
	}
	req := connect.NewRequest(&pb.ForgeAction{
		WorkItemId:    "work-1",
		ActionKey:     "close-issue",
		ToolName:      "forge_issue_write",
		ArgumentsJson: `{"state":"closed"}`,
	})
	req.Header().Set(FencingTokenHeader, "7")
	if _, err := svc.ProposeForgeAction(ctx, req); err == nil {
		t.Fatal("first ProposeForgeAction succeeded, want transient error")
	}
	req = connect.NewRequest(req.Msg)
	req.Header().Set(FencingTokenHeader, "8")
	second, err := svc.ProposeForgeAction(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("executor calls = %d, want retry call", calls)
	}
	if !second.Msg.GetCommitted() || !second.Msg.GetDeduped() || second.Msg.GetResponseJson() != `{"retry":true}` {
		t.Fatalf("retry ack = %+v, want committed deduped cached response", second.Msg)
	}
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM action_attempts WHERE outbox_id = ?`, second.Msg.GetOutboxId()).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want error attempt plus retry commit", attempts)
	}
}

func openMigrated(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertWorkItem(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO runs (id, session_id) VALUES ('run-1', 'session-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO work_items (id, kind, source_id, title, scope_generation) VALUES ('work-1', 'reply_to_issue', 'forge:botfam/botfam:issue:498', 'Issue 498', 1)`); err != nil {
		t.Fatal(err)
	}
}
