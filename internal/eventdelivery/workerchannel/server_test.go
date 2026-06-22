package workerchannel

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestProposeForgeActionDoesNotDoubleExecuteConcurrentPendingAction(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t, ctx)
	defer db.Close()
	insertWorkItem(t, ctx, db)

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var calls atomic.Int32
	svc := Service{
		DB: db,
		Executor: ForgeExecutorFunc(func(context.Context, string, string) (string, error) {
			if calls.Add(1) > 1 {
				return "", errors.New("duplicate execution")
			}
			startedOnce.Do(func() { close(started) })
			<-release
			return `{"committed":true}`, nil
		}),
	}
	msg := &pb.ForgeAction{
		WorkItemId:    "work-1",
		ActionKey:     "close-issue",
		ToolName:      "forge_issue_write",
		ArgumentsJson: `{"state":"closed"}`,
	}

	firstDone := make(chan *pb.ActionAck, 1)
	firstErr := make(chan error, 1)
	go func() {
		req := connect.NewRequest(msg)
		req.Header().Set(FencingTokenHeader, "7")
		resp, err := svc.ProposeForgeAction(ctx, req)
		if err != nil {
			firstErr <- err
			return
		}
		firstDone <- resp.Msg
	}()

	select {
	case <-started:
	case err := <-firstErr:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first forge action to start")
	}
	req := connect.NewRequest(msg)
	req.Header().Set(FencingTokenHeader, "8")
	if _, err := svc.ProposeForgeAction(ctx, req); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent ProposeForgeAction error = %v, want already in progress", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("executor calls after concurrent duplicate = %d, want 1", got)
	}

	close(release)
	select {
	case err := <-firstErr:
		t.Fatal(err)
	case ack := <-firstDone:
		if !ack.GetCommitted() || ack.GetDeduped() || ack.GetResponseJson() != `{"committed":true}` {
			t.Fatalf("first ack = %+v, want committed non-deduped response", ack)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first forge action to finish")
	}

	req = connect.NewRequest(msg)
	req.Header().Set(FencingTokenHeader, "9")
	resp, err := svc.ProposeForgeAction(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Msg.GetCommitted() || !resp.Msg.GetDeduped() || resp.Msg.GetResponseJson() != `{"committed":true}` {
		t.Fatalf("deduped ack = %+v, want committed cached response", resp.Msg)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("executor calls after committed dedupe = %d, want 1", got)
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
