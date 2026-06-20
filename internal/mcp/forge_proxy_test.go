package mcp

import (
	"strings"
	"testing"

	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
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
