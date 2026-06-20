package mcp

import (
	"context"
	"strings"
	"testing"

	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"github.com/robertolupi/botfam/internal/famctx"
)

func TestForgeActionToolResultRejectsUncommittedAck(t *testing.T) {
	_, proxied, err := forgeActionToolResult(&pb.ActionAck{OutboxId: "outbox-1", Deduped: true})
	if !proxied {
		t.Fatal("proxied = false, want true")
	}
	if err == nil || !strings.Contains(err.Error(), "did not commit") {
		t.Fatalf("err = %v, want uncommitted ack error", err)
	}
}

func TestForgeActionToolResultReturnsCommittedResponse(t *testing.T) {
	result, proxied, err := forgeActionToolResult(&pb.ActionAck{Committed: true, ResponseJson: `{"ok":true}`})
	if err != nil {
		t.Fatal(err)
	}
	if !proxied {
		t.Fatal("proxied = false, want true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(result.Content))
	}
}

func TestForgeExecutorProxyBypass(t *testing.T) {
	// Set proxy-mode environment variables
	t.Setenv("BOTFAM_WORKER_CHANNEL_SOCKET", "/tmp/non-existent.sock")
	t.Setenv("BOTFAM_FENCING_TOKEN", "123")
	t.Setenv("BOTFAM_SESSION_RESOLVER_SOCKET", "/tmp/non-existent-resolver.sock")

	fctx := famctx.Context{}
	fctx.WorkDir = t.TempDir()
	executor := NewForgeExecutor(fctx)

	// Invoke ExecuteForgeAction. It should bypass the proxy check and proceed to the Gitea handler.
	// Since Gitea registry context is empty/invalid, it should fail with Gitea client resolution error,
	// NOT with the proxy mode error ("worker proxy mode is active...").
	_, err := executor.ExecuteForgeAction(context.Background(), "forge_version_read", "{}")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errStr := err.Error()
	if strings.Contains(errStr, "worker proxy mode is active") {
		t.Errorf("ExecuteForgeAction did not bypass proxying check: %v", err)
	}
	t.Logf("Got expected bypass error: %v", err)
}
