package mcp

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	serverlib "github.com/robertolupi/botfam/internal/server"
)

func newTestServer(t *testing.T) (*server, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("COLLAB_ROOT", root)
	t.Setenv("COLLAB_ACTOR", "")
	t.Setenv("BOTFAM_LOCK_ACTOR", "")
	t.Setenv("BOTFAM_TESTING", "1")

	// Use scratch directory to ensure socket path is short (Darwin 104 char limit)
	absScratch, err := filepath.Abs("../../scratch")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(absScratch, 0755)
	udsPath := filepath.Join(absScratch, fmt.Sprintf("test-%d.sock", time.Now().UnixNano()))
	t.Setenv("BOTFAM_SOCKET", udsPath)
	t.Cleanup(func() {
		_ = os.Remove(udsPath)
	})

	// Start in-process UDS server
	srv := serverlib.NewServer(udsPath, 0)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		_ = srv.Start(ctx)
	}()

	// Wait for the UDS socket to become active
	var dialErr error
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		conn, err := net.Dial("unix", udsPath)
		if err == nil {
			conn.Close()
			dialErr = nil
			break
		}
		dialErr = err
	}
	if dialErr != nil {
		t.Fatalf("failed to start test UDS daemon: %v", dialErr)
	}

	return &server{}, root
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBoundActorConflictsWithWorkDirActor(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := mkdir(t, filepath.Join(base, "wt-alice"))
	bobDir := mkdir(t, filepath.Join(base, "wt-bob"))

	// First call binds the session to alice via the directory-derived actor.
	if _, err := s.callTool(context.Background(), "inbox", map[string]any{"work_dir": aliceDir}); err != nil {
		t.Fatalf("first call from wt-alice failed: %v", err)
	}
	if s.actor != "alice" {
		t.Fatalf("expected bound session actor %q, got %q", "alice", s.actor)
	}

	// A later call whose work_dir resolves to a different actor must conflict.
	_, err := s.callTool(context.Background(), "inbox", map[string]any{"work_dir": bobDir})
	if err == nil {
		t.Fatal("expected bound-actor vs work_dir conflict, got nil error")
	}
	want := `bound session actor "alice" conflicts with resolved directory actor "bob"`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("expected error containing %q, got %q", want, err.Error())
	}
	if s.actor != "alice" {
		t.Errorf("bound actor changed after conflict: got %q", s.actor)
	}
}

func TestIdentityOptionalToolsWithoutIdentity(t *testing.T) {
	s, root := newTestServer(t)
	// A directory with no wt-/botfam- prefix yields no directory actor.
	plainDir := mkdir(t, filepath.Join(t.TempDir(), "myrepo"))

	// sweep does not use the calling actor (store.Sweep takes none).
	if _, err := s.callTool(context.Background(), "sweep", map[string]any{"work_dir": plainDir}); err != nil {
		t.Fatalf("sweep without identity failed: %v", err)
	}

	// session_read filters only by the explicit "from" argument.
	mkdir(t, filepath.Join(root, "sessions", "s1"))
	if err := os.WriteFile(filepath.Join(root, "sessions", "s1", "session.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callTool(context.Background(), "session_read", map[string]any{"work_dir": plainDir, "session": "s1"}); err != nil {
		t.Fatalf("session_read without identity failed: %v", err)
	}
	if s.actor != "" {
		t.Errorf("identity-optional call must not bind an actor, got %q", s.actor)
	}

	// Identity-requiring tools must still fail without an identity.
	_, err := s.callTool(context.Background(), "inbox", map[string]any{"work_dir": plainDir})
	if err == nil {
		t.Fatal("expected identity error for inbox without identity")
	}
	if !strings.Contains(err.Error(), "identity required") {
		t.Errorf("expected identity-required error, got %q", err.Error())
	}
}

func TestIdentityOptionalToolsStillEnforceConflictsAndBinding(t *testing.T) {
	s, root := newTestServer(t)
	base := t.TempDir()
	aliceDir := mkdir(t, filepath.Join(base, "wt-alice"))
	mkdir(t, filepath.Join(root, "sessions", "s1"))
	if err := os.WriteFile(filepath.Join(root, "sessions", "s1", "session.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Conflicting explicit actor vs directory actor must still be rejected,
	// even for an identity-optional tool.
	_, err := s.callTool(context.Background(), "session_read", map[string]any{
		"work_dir": aliceDir,
		"session":  "s1",
		"actor":    "bob",
	})
	if err == nil {
		t.Fatal("expected actor/work_dir conflict for identity-optional tool, got nil error")
	}
	if !strings.Contains(err.Error(), `actor "bob" conflicts with resolved directory actor "alice"`) {
		t.Errorf("unexpected conflict error: %q", err.Error())
	}

	// When an identity IS resolvable, an identity-optional tool binds it
	// normally so later conflict checks stay active.
	if _, err := s.callTool(context.Background(), "session_read", map[string]any{"work_dir": aliceDir, "session": "s1"}); err != nil {
		t.Fatalf("session_read from wt-alice failed: %v", err)
	}
	if s.actor != "alice" {
		t.Errorf("expected session bound to %q, got %q", "alice", s.actor)
	}
}
