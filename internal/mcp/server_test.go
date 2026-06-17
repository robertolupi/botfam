package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/irc"
	"github.com/robertolupi/botfam/internal/mailbox"
)

func newTestServer(t *testing.T) (*server, string) {
	t.Helper()
	root := t.TempDir()
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}

	// Create main and wt-agy worktree
	wtDir := setupTestWorktree(t, root, "wt-agy", "agy")
	writeMockRegistry(t, root, wtDir, "mockfam")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wtDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})

	t.Setenv("HOME", root)
	t.Setenv("BOTFAM_TESTING", "1")

	return &server{}, root
}

// writeMockRegistry points BOTFAM_CONFIG at baseDir/config.toml and registers a
// [repo.<name>] stanza (path=baseDir) plus the standard test roster (#404).
func writeMockRegistry(t *testing.T, baseDir, workDir string, name string) {
	t.Helper()
	t.Setenv("BOTFAM_CONFIG", filepath.Join(baseDir, "config.toml"))
	registerFam(t, name, baseDir, map[string]famconfig.AgentConfig{
		"alice":     {Harness: "claude-code"},
		"bob":       {Harness: "claude-code"},
		"agy":       {Harness: "antigravity"},
		"someactor": {Harness: "claude-code"},
		"myrepo":    {Harness: "claude-code"},
	})
}

// registerFam merges a [repo.<name>] stanza (path=baseDir) and the given agents
// into the active BOTFAM_CONFIG, creating the file if needed. opts lets a test
// set per-repo overrides (e.g. wiki projections).
func registerFam(t *testing.T, name, baseDir string, agents map[string]famconfig.AgentConfig, opts ...func(*famconfig.RepoConfig)) {
	t.Helper()
	cfg, err := famconfig.LoadOrInitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]famconfig.AgentConfig{}
	}
	for k, v := range agents {
		cfg.Agents[k] = v
	}
	rc := famconfig.RepoConfig{Path: baseDir}
	for _, o := range opts {
		o(&rc)
	}
	cfg.UpsertRepo(name, rc)
	if err := famconfig.WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

// TestMaybeStartIngestGuards locks the early-return guards on the spool
// ingester (#254): it must NOT spawn its polling goroutine without a
// serving-lifetime context (the direct-callTool unit-test path — a nil ctx here
// previously panicked) or without a resolved actor.
func TestMaybeStartIngestGuards(t *testing.T) {
	t.Run("nil server ctx (direct callTool): no ingester", func(t *testing.T) {
		s, root := newTestServer(t)
		s.maybeStartIngest(root, "alice")
		if s.ingestStarted {
			t.Fatal("ingester started with nil ctx; must be gated to the serving lifetime")
		}
	})
	t.Run("empty actor: no ingester", func(t *testing.T) {
		s, root := newTestServer(t)
		s.ctx = context.Background()
		s.maybeStartIngest(root, "")
		if s.ingestStarted {
			t.Fatal("ingester started without an actor")
		}
	})
}

// TestMaybeStartIngestForWorkDirArmsIngester locks the fix for the "no spool"
// bug: the ingester must be armed when a discovery workDir resolves an actor
// (the onboarding resources/read path), not only on the first qualifying tool
// call. A server whose ingester started only from callTool left a fresh session
// with no spool for `botfam wait` to read.
func TestMaybeStartIngestForWorkDirArmsIngester(t *testing.T) {
	s, root := newTestServer(t) // chdir'd into the wt-agy worktree
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.ctx = ctx

	wtDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s.maybeStartIngestForWorkDir(ctx, wtDir)

	if !s.ingestStarted {
		t.Fatal("ingester was not armed from the resolved workDir")
	}
	// The goroutine creates the spool at $FAMROOT/spool/$actor.
	spoolDir := filepath.Join(root, "spool", "agy")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(spoolDir); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ingester did not create the spool at %s", spoolDir)
}

// TestNudgeCallbackGating: the #337 notification nudge is on by default for a
// resolvable agent and nil when the fam can't be resolved (no notify target).
// The flag-off case rides on the generic famconfig FlagEnabled tests.
func TestNudgeCallbackGating(t *testing.T) {
	s, _ := newTestServer(t)
	wtDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if s.nudgeCallback(wtDir) == nil {
		t.Error("nudge should default on for a resolvable agent worktree")
	}
	if s.nudgeCallback("/definitely/not/a/fam") != nil {
		t.Error("nudge callback should be nil when the fam can't be resolved")
	}
}

// TestNudgeDebounce: a second nudge within the debounce window is suppressed
// (lastNudge timestamp unchanged), so a backlog drain doesn't flood the client.
func TestNudgeDebounce(t *testing.T) {
	// A real server with no client sessions: SendNotificationToAllClients is a
	// safe no-op, so the test exercises the debounce path without a live client.
	s := &server{mcpSrv: mcpserver.NewMCPServer(serverName, serverVersion)}
	m := &mailbox.Message{Source: mailbox.SourceForge, Subject: "x"}

	s.nudge(m)
	first := s.lastNudge
	if first.IsZero() {
		t.Fatal("first nudge did not set lastNudge")
	}
	s.nudge(m) // within window
	if !s.lastNudge.Equal(first) {
		t.Error("second nudge within debounce window should be suppressed (lastNudge advanced)")
	}
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
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	bobDir := setupTestWorktree(t, base, "wt-bob", "bob")
	writeMockRegistry(t, base, aliceDir, "mockfam")

	// Create log files so irc_read doesn't fail
	if err := os.MkdirAll(filepath.Join(aliceDir, "scratch", "irc", "alice"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aliceDir, "scratch", "irc", "alice", "log"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bobDir, "scratch", "irc", "bob"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bobDir, "scratch", "irc", "bob", "log"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	// First call binds the session to alice via the directory-derived actor.
	if _, err := s.callTool(context.Background(), "irc_read", map[string]any{"work_dir": aliceDir}); err != nil {
		t.Fatalf("first call from wt-alice failed: %v", err)
	}
	if s.actor != "alice" {
		t.Fatalf("expected bound session actor %q, got %q", "alice", s.actor)
	}

	// A later call whose work_dir resolves to a different actor (bob) is cross-actor.
	// Since irc_read is read-only, it must succeed.
	if _, err := s.callTool(context.Background(), "irc_read", map[string]any{"work_dir": bobDir}); err != nil {
		t.Fatalf("cross-actor read-only call to irc_read failed: %v", err)
	}

	// A mutating call like irc_write to bob's work_dir must be blocked.
	_, err := s.callTool(context.Background(), "irc_write", map[string]any{
		"work_dir": bobDir,
		"message":  "hello",
	})
	if err == nil {
		t.Fatal("expected mutating call in cross-actor worktree to be blocked, got nil error")
	}
	want := "acting in another agent's worktree (executing: alice, target: bob) is read-only; mutating tool 'irc_write' is blocked"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("expected error containing %q, got %q", want, err.Error())
	}
	if s.actor != "alice" {
		t.Errorf("bound actor changed: got %q", s.actor)
	}
}

func setupTestWorktree(t *testing.T, baseDir, name, actor string) string {
	t.Helper()
	mainDir := filepath.Join(baseDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd := func(dir string, cmdName string, args ...string) {
		cmd := exec.Command(cmdName, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to run %s %v in %s: %v\nOutput: %s", cmdName, args, dir, err, string(out))
		}
	}
	if _, err := os.Stat(filepath.Join(mainDir, ".git")); os.IsNotExist(err) {
		runCmd(mainDir, "git", "init")
		runCmd(mainDir, "git", "config", "user.name", "test")
		runCmd(mainDir, "git", "config", "user.email", "test@example.com")
		runCmd(mainDir, "git", "commit", "--allow-empty", "-m", "initial commit")
	}

	wtDir := filepath.Join(baseDir, name)
	runCmd(mainDir, "git", "worktree", "add", wtDir)

	runCmd(wtDir, "git", "config", "extensions.worktreeConfig", "true")
	runCmd(wtDir, "git", "config", "--worktree", "user.name", actor)
	runCmd(wtDir, "git", "config", "--worktree", "user.email", actor+"@example.com")
	return wtDir
}

// (The former TestIdentityOptionalToolsWithoutIdentity tested an identity-optional
// tool running in a fam *member* worktree that was not itself config-resolvable —
// the object-store-membership-without-fam.toml state. #404 collapses membership
// into config resolvability, so that state no longer exists: a non-agent worktree
// in a configured fam is quarantined (see quarantine_test.go), and an unconfigured
// dir fails membership. Identity-optional tool behaviour in the valid agent path
// stays covered below.)

func TestIdentityOptionalToolsStillEnforceConflictsAndBinding(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	writeMockRegistry(t, base, aliceDir, "mockfam")

	// Conflicting explicit actor vs directory actor must still be rejected,
	// even for an identity-optional tool. Since worktree_sync is mutating, it is blocked.
	_, err := s.callTool(context.Background(), "worktree_sync", map[string]any{
		"work_dir": aliceDir,
		"actor":    "bob",
	})
	if err == nil {
		t.Fatal("expected actor/work_dir conflict for identity-optional tool, got nil error")
	}
	want := "acting in another agent's worktree (executing: bob, target: alice) is read-only; mutating tool 'worktree_sync' is blocked"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("unexpected conflict error: %q", err.Error())
	}

	// When an identity IS resolvable, an identity-optional tool binds it
	// normally so later conflict checks stay active.
	if _, err := s.callTool(context.Background(), "worktree_sync", map[string]any{"work_dir": aliceDir}); err != nil {
		t.Fatalf("worktree_sync from wt-alice failed: %v", err)
	}
	if s.actor != "alice" {
		t.Errorf("expected session bound to %q, got %q", "alice", s.actor)
	}
}

func TestIrcWriteTool(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	writeMockRegistry(t, base, aliceDir, "mockfam")

	// Create scratch/irc/alice directory structure
	fifoDir := filepath.Join(aliceDir, "scratch", "irc", "alice")
	if err := os.MkdirAll(fifoDir, 0755); err != nil {
		t.Fatal(err)
	}

	fifoPath := filepath.Join(fifoDir, "in")
	// Create named pipe
	if err := syscall.Mkfifo(fifoPath, 0666); err != nil {
		t.Fatalf("failed to create test FIFO: %v", err)
	}

	// Open FIFO for reading in a separate goroutine so it doesn't block
	readCh := make(chan string, 1)
	errCh := make(chan error, 1)
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		// Open the FIFO in RDWR mode so it returns immediately and guarantees a reader
		f, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
		if err != nil {
			errCh <- err
			return
		}
		defer f.Close()

		close(ready)

		// Set a read timeout using select/context
		lineCh := make(chan string, 1)
		go func() {
			reader := bufio.NewReader(f)
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			lineCh <- line
		}()

		select {
		case line := <-lineCh:
			readCh <- line
		case <-ctx.Done():
			errCh <- ctx.Err()
		case <-time.After(2 * time.Second):
			errCh <- fmt.Errorf("timeout waiting for FIFO read")
		}
	}()

	// Wait for reader to be ready before calling irc_write (O_WRONLY|O_NONBLOCK)
	select {
	case <-ready:
	case err := <-errCh:
		t.Fatalf("failed to start FIFO reader: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for FIFO reader to start")
	}

	// Call the irc_write tool using the server
	_, err := s.callTool(context.Background(), "irc_write", map[string]any{
		"work_dir": aliceDir,
		"message":  "hello irc\n",
	})
	if err != nil {
		t.Fatalf("irc_write tool call failed: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("FIFO reader error: %v", err)
	case line := <-readCh:
		if line != "hello irc\n" {
			t.Errorf("expected line %q, got %q", "hello irc\n", line)
		}
	}

	// Test writing with target parameter
	readCh2 := make(chan string, 1)
	errCh2 := make(chan error, 1)
	ready2 := make(chan struct{})
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go func() {
		f, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
		if err != nil {
			errCh2 <- err
			return
		}
		defer f.Close()

		close(ready2)

		lineCh := make(chan string, 1)
		go func() {
			reader := bufio.NewReader(f)
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			lineCh <- line
		}()

		select {
		case line := <-lineCh:
			readCh2 <- line
		case <-ctx2.Done():
			errCh2 <- ctx2.Err()
		case <-time.After(2 * time.Second):
			errCh2 <- fmt.Errorf("timeout waiting for FIFO read (target test)")
		}
	}()

	// Wait for reader to be ready before calling irc_write (O_WRONLY|O_NONBLOCK)
	select {
	case <-ready2:
	case err := <-errCh2:
		t.Fatalf("failed to start FIFO reader 2: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for FIFO reader 2 to start")
	}

	_, err = s.callTool(context.Background(), "irc_write", map[string]any{
		"work_dir": aliceDir,
		"message":  "hello target",
		"target":   "#chan",
	})
	if err != nil {
		t.Fatalf("irc_write tool call with target failed: %v", err)
	}

	select {
	case err := <-errCh2:
		t.Fatalf("FIFO reader error on target test: %v", err)
	case line := <-readCh2:
		if line != "/msg #chan hello target\n" {
			t.Errorf("expected line %q, got %q", "/msg #chan hello target\n", line)
		}
	}
}

// decodeToolResult unmarshals the JSON text payload of a tool result.
func decodeToolResult(t *testing.T, res *mcplib.CallToolResult, v any) {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	tc, ok := mcplib.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("tool result content is not text: %T", res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), v); err != nil {
		t.Fatalf("failed to decode tool result %q: %v", tc.Text, err)
	}
}

func TestIrcReadTool(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	writeMockRegistry(t, base, aliceDir, "mockfam")

	logDir := mkdir(t, filepath.Join(aliceDir, "scratch", "irc", "alice"))
	content := "12:00 <bob> one\n12:01 <bob> two\n12:02 <bob> three\n"
	if err := os.WriteFile(filepath.Join(logDir, "log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.callTool(context.Background(), "irc_read", map[string]any{
		"work_dir": aliceDir,
		"lines":    float64(2),
	})
	if err != nil {
		t.Fatalf("irc_read tool call failed: %v", err)
	}

	var out struct {
		Lines      []string `json:"lines"`
		NextOffset int64    `json:"next_offset"`
	}
	decodeToolResult(t, res, &out)
	want := []string{"12:01 <bob> two", "12:02 <bob> three"}
	if len(out.Lines) != 2 || out.Lines[0] != want[0] || out.Lines[1] != want[1] {
		t.Errorf("lines = %v, want %v", out.Lines, want)
	}
	if out.NextOffset != int64(len(content)) {
		t.Errorf("next_offset = %d, want %d", out.NextOffset, len(content))
	}

	// Paging from an explicit offset returns the remainder.
	res, err = s.callTool(context.Background(), "irc_read", map[string]any{
		"work_dir":    aliceDir,
		"from_offset": float64(len("12:00 <bob> one\n")),
	})
	if err != nil {
		t.Fatalf("irc_read with from_offset failed: %v", err)
	}
	decodeToolResult(t, res, &out)
	if len(out.Lines) != 2 || out.Lines[0] != "12:01 <bob> two" {
		t.Errorf("paged lines = %v", out.Lines)
	}
	if out.NextOffset != int64(len(content)) {
		t.Errorf("paged next_offset = %d, want %d", out.NextOffset, len(content))
	}
}

func TestIrcReadToolMissingLog(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	writeMockRegistry(t, base, aliceDir, "mockfam")

	_, err := s.callTool(context.Background(), "irc_read", map[string]any{
		"work_dir": aliceDir,
	})
	if err == nil {
		t.Fatal("expected error for missing IRC log, got nil")
	}
	wantPath := filepath.Join(aliceDir, "scratch", "irc", "alice", "log")
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error %q does not mention log path %q", err.Error(), wantPath)
	}
	if !strings.Contains(err.Error(), "client running") {
		t.Errorf("error %q does not hint that the client may not be running", err.Error())
	}
}

func TestIrcReplayTool(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	writeMockRegistry(t, base, aliceDir, "myfam")

	// Create a history file
	historyDir := filepath.Join(base, "myfam-collab")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		t.Fatal(err)
	}
	historyFile := filepath.Join(historyDir, "history.jsonl")

	writeEntry := func(sender, evType, target, body string) {
		entry := irc.HistoryEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Sender:    sender,
			Type:      evType,
			Target:    target,
			Body:      body,
		}
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(historyFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, _ = f.Write(append(data, '\n'))
	}

	writeEntry("bob", "PRIVMSG", "#botfam", "peer msg")
	writeEntry("alice-myfam", "PRIVMSG", "#botfam", "my own msg")

	// Call the irc_replay tool using the server
	res, err := s.callTool(context.Background(), "irc_replay", map[string]any{
		"work_dir": aliceDir,
		"since":    "lines:10",
		"channels": "#botfam",
	})
	if err != nil {
		t.Fatalf("irc_replay tool call failed: %v", err)
	}

	var out struct {
		Lines      []string `json:"lines"`
		NextOffset int64    `json:"next_offset"`
	}
	decodeToolResult(t, res, &out)

	if len(out.Lines) != 1 {
		t.Errorf("expected 1 line, got %d: %v", len(out.Lines), out.Lines)
	}
	if !strings.Contains(out.Lines[0], "peer msg") {
		t.Errorf("expected line to contain 'peer msg', got %q", out.Lines[0])
	}
	if strings.Contains(out.Lines[0], "alice-myfam") {
		t.Errorf("expected own message to be filtered out, got %q", out.Lines[0])
	}
}

func TestIrcWaitToolTimeout(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	writeMockRegistry(t, base, aliceDir, "mockfam")

	logDir := mkdir(t, filepath.Join(aliceDir, "scratch", "irc", "alice"))
	if err := os.WriteFile(filepath.Join(logDir, "log"), []byte("12:00 <bob> static\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	res, err := s.callTool(context.Background(), "irc_wait", map[string]any{
		"work_dir":  aliceDir,
		"timeout_s": float64(0.05),
	})
	if err != nil {
		t.Fatalf("irc_wait tool call failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("irc_wait took too long: %v", elapsed)
	}

	var out struct {
		Lines      []string `json:"lines"`
		NextOffset int64    `json:"next_offset"`
		TimedOut   bool     `json:"timed_out"`
	}
	decodeToolResult(t, res, &out)
	if !out.TimedOut {
		t.Error("expected timed_out=true")
	}
	if len(out.Lines) != 0 {
		t.Errorf("expected no lines, got %v", out.Lines)
	}
	if out.NextOffset != int64(len("12:00 <bob> static\n")) {
		t.Errorf("next_offset = %d, want snapshot size %d", out.NextOffset, len("12:00 <bob> static\n"))
	}
}

// TestIrcWaitToolFiltersScopedSelf verifies the MCP irc_wait tool filters the
// agent's OWN messages by the fam-scoped nick (claude-botfam), not the bare
// actor — otherwise it wakes on its own traffic once nicks are scoped (#137,
// codex review of #139).
func TestIrcWaitToolFiltersScopedSelf(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")
	writeMockRegistry(t, base, aliceDir, "myfam")
	logDir := mkdir(t, filepath.Join(aliceDir, "scratch", "irc", "alice"))
	content := "12:00 <alice-myfam> my own line\n12:01 <bob> peer line\n"
	if err := os.WriteFile(filepath.Join(logDir, "log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.callTool(context.Background(), "irc_wait", map[string]any{
		"work_dir":    aliceDir,
		"from_offset": float64(0),
		"timeout_s":   float64(2),
	})
	if err != nil {
		t.Fatalf("irc_wait: %v", err)
	}
	var out struct {
		Lines    []string `json:"lines"`
		TimedOut bool     `json:"timed_out"`
	}
	decodeToolResult(t, res, &out)
	joined := strings.Join(out.Lines, "\n")
	if strings.Contains(joined, "alice-myfam") {
		t.Errorf("own fam-scoped message was not filtered: %v", out.Lines)
	}
	if !strings.Contains(joined, "<bob>") {
		t.Errorf("peer message missing from wait result: %v", out.Lines)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run %s %v: %v", name, args, err)
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "test")
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "commit", "--allow-empty", "-m", "initial commit")
}

func TestWorktreeMcpTools(t *testing.T) {
	s, _ := newTestServer(t)
	tempDir := t.TempDir()
	mainDir := filepath.Join(tempDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	initGitRepo(t, mainDir)

	// Create a branch
	cmd := exec.Command("git", "branch", "feature-branch")
	cmd.Dir = mainDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	// Create worktree
	wtDir := filepath.Join(tempDir, "wt-bob")
	cmd = exec.Command("git", "worktree", "add", wtDir, "feature-branch")
	cmd.Dir = mainDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add worktree: %v", err)
	}

	writeMockRegistry(t, tempDir, wtDir, "mockfam")

	// Call worktree_init via MCP
	res, err := s.callTool(context.Background(), "worktree_init", map[string]any{
		"target_actor": "bob",
		"work_dir":     wtDir,
	})
	if err != nil {
		t.Fatalf("worktree_init tool call failed: %v", err)
	}

	var initOut struct {
		Ok     bool   `json:"ok"`
		Output string `json:"output"`
	}
	decodeToolResult(t, res, &initOut)
	if !initOut.Ok {
		t.Error("expected ok=true")
	}
	if !strings.Contains(initOut.Output, "Worktree identity successfully set") {
		t.Errorf("unexpected output: %s", initOut.Output)
	}

	// Call worktree_sync via MCP
	res, err = s.callTool(context.Background(), "worktree_sync", map[string]any{
		"work_dir": wtDir,
	})
	if err != nil {
		t.Fatalf("worktree_sync tool call failed: %v", err)
	}

	var syncOut struct {
		Ok     bool   `json:"ok"`
		Output string `json:"output"`
	}
	decodeToolResult(t, res, &syncOut)
	if !syncOut.Ok {
		t.Error("expected ok=true")
	}
	if !strings.Contains(syncOut.Output, "Merging main into branch") {
		t.Errorf("unexpected output: %s", syncOut.Output)
	}
}

func TestMcpResources(t *testing.T) {
	s, root := newTestServer(t)

	// Make root a mock git repository so famconfig.RepoPath resolves to it
	initGitRepo(t, root)

	// Save cwd and chdir to root
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})

	// NOTE: no local doc/ is created. #117's contract is that docs/* are served
	// from the embedded corpus, so discovery must work in a docless repo.

	// 1. docs/protocol is served from the embedded corpus (not a local file).
	req := mcplib.ReadResourceRequest{}
	req.Params.URI = "botfam:///docs/protocol"
	res, err := s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read embedded protocol: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("no resource contents returned")
	}
	tr, ok := res[0].(mcplib.TextResourceContents)
	if !ok {
		t.Fatalf("expected text resource contents, got %T", res[0])
	}
	if !strings.Contains(tr.Text, "Coordination Protocol") {
		t.Errorf("expected embedded protocol content, got %q", tr.Text)
	}
	// The corpus is a Go template; serving it must execute the template
	// (e.g. {{.IntegrationBranch}}), never leak raw template syntax.
	if strings.Contains(tr.Text, "{{") {
		t.Errorf("served doc contains unrendered template syntax: %q", tr.Text)
	}

	// 2. The discovery root and its JSON index.
	req.Params.URI = "botfam:///"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read discovery root: %v", err)
	}
	if rootText := res[0].(mcplib.TextResourceContents).Text; !strings.Contains(rootText, "Start here") {
		t.Errorf("root resource missing orientation, got %q", rootText)
	}

	req.Params.URI = "botfam:///index.json"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read index.json: %v", err)
	}
	var idx struct {
		Schema    string   `json:"schema"`
		Resources []string `json:"resources"`
	}
	if err := json.Unmarshal([]byte(res[0].(mcplib.TextResourceContents).Text), &idx); err != nil {
		t.Fatalf("index.json is not valid JSON: %v", err)
	}
	if idx.Schema != "botfam.discovery.v1" {
		t.Errorf("index schema = %q, want botfam.discovery.v1", idx.Schema)
	}
	if len(idx.Resources) == 0 {
		t.Error("index.json advertises no resources")
	}

	resolved, err := (famconfig.GitResolver{}).ResolveIdentity(root)
	if err == nil && resolved.Name != "" {
		req.Params.URI = fmt.Sprintf("botfam://%s/docs/protocol", resolved.Name)
		if _, err := s.handleReadResource(context.Background(), req); err != nil {
			t.Fatalf("failed to read local named authority resource: %v", err)
		}
	}

	// 4. Negative cases: unknown slug, traversal, unsupported scheme, and an
	// unknown named authority must all error.
	negatives := []struct {
		name string
		uri  string
	}{
		{"unknown slug", "botfam:///docs/nonexistent"},
		{"traversal attempt", "botfam:///../../etc/passwd"},
		{"unsupported scheme", "file:///docs/protocol"},
		{"unknown authority", "botfam://definitely-not-a-real-fam/docs/protocol"},
	}
	for _, tc := range negatives {
		req.Params.URI = tc.uri
		if _, err := s.handleReadResource(context.Background(), req); err == nil {
			t.Errorf("%s: expected error for URI %q, got nil", tc.name, tc.uri)
		}
	}
}

func TestMcpCatalogsAndCLI(t *testing.T) {
	// Setup a temporary workspace with skills
	root := t.TempDir()

	// Create mock skills in the temp root
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "zeta"), 0755); err != nil {
		t.Fatal(err)
	}
	zetaContent := "---\nname: zeta-skill\ndescription: Handle zeta work.\n---\n# Zeta skill contents\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "zeta", "SKILL.md"), []byte(zetaContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Change cwd to temp root so ReadRepoSkills reads our mock skills
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldCwd)
	}()

	s := &server{}
	mcpSrv := mcpserver.NewMCPServer(serverName, serverVersion, mcpserver.WithToolCapabilities(false))
	s.mcpSrv = mcpSrv
	s.registerTools(mcpSrv)
	s.registerResources(mcpSrv)

	// 1. Verify tools catalog
	req := mcplib.ReadResourceRequest{}
	req.Params.URI = "botfam:///tools"
	res, err := s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read botfam:///tools: %v", err)
	}
	toolsMarkdown := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(toolsMarkdown, "# botfam Tools Catalog") || !strings.Contains(toolsMarkdown, "irc_read") {
		t.Errorf("unexpected tools markdown: %q", toolsMarkdown)
	}

	req.Params.URI = "botfam:///tools.json"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read botfam:///tools.json: %v", err)
	}
	var toolsIdx struct {
		Schema string `json:"schema"`
		Tools  []struct {
			Name            string `json:"name"`
			Description     string `json:"description"`
			Domain          string `json:"domain"`
			ReadOnly        bool   `json:"read_only"`
			InputSchemaHash string `json:"input_schema_hash"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(res[0].(mcplib.TextResourceContents).Text), &toolsIdx); err != nil {
		t.Fatalf("tools.json is not valid JSON: %v", err)
	}
	if toolsIdx.Schema != "botfam.tools.v1" {
		t.Errorf("expected schema botfam.tools.v1, got %q", toolsIdx.Schema)
	}
	foundIrcRead := false
	for _, tool := range toolsIdx.Tools {
		if tool.Name == "irc_read" {
			foundIrcRead = true
			if tool.Domain != "irc" {
				t.Errorf("expected domain 'irc' for irc_read, got %q", tool.Domain)
			}
			if !tool.ReadOnly {
				t.Errorf("expected read_only true for irc_read")
			}
			if len(tool.InputSchemaHash) != 64 {
				t.Errorf("expected 64-char schema hash, got %q", tool.InputSchemaHash)
			}
		}
	}
	if !foundIrcRead {
		t.Errorf("irc_read tool not found in tools.json catalog")
	}

	// 2. Verify skills catalog
	req.Params.URI = "botfam:///skills"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read botfam:///skills: %v", err)
	}
	skillsMarkdown := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(skillsMarkdown, "# botfam Skills Catalog") || !strings.Contains(skillsMarkdown, "zeta-skill") {
		t.Errorf("unexpected skills markdown: %q", skillsMarkdown)
	}

	req.Params.URI = "botfam:///skills.json"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read botfam:///skills.json: %v", err)
	}
	var skillsIdx struct {
		Schema string `json:"schema"`
		Skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(res[0].(mcplib.TextResourceContents).Text), &skillsIdx); err != nil {
		t.Fatalf("skills.json is not valid JSON: %v", err)
	}
	if skillsIdx.Schema != "botfam.skills.v1" {
		t.Errorf("expected schema botfam.skills.v1, got %q", skillsIdx.Schema)
	}
	if len(skillsIdx.Skills) != 1 || skillsIdx.Skills[0].Name != "zeta-skill" {
		t.Errorf("expected skills catalog to contain 'zeta-skill', got %+v", skillsIdx.Skills)
	}

	// 3. Read specific skill
	req.Params.URI = "botfam:///skills/zeta-skill"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read skill zeta-skill: %v", err)
	}
	if res[0].(mcplib.TextResourceContents).Text != zetaContent {
		t.Errorf("expected skill content %q, got %q", zetaContent, res[0].(mcplib.TextResourceContents).Text)
	}

	// 4. Traversal and bad cases
	negatives := []struct {
		name string
		uri  string
	}{
		{"unknown skill", "botfam:///skills/nonexistent"},
		{"traversal under skills", "botfam:///skills/../../etc/passwd"},
	}
	for _, tc := range negatives {
		req.Params.URI = tc.uri
		if _, err := s.handleReadResource(context.Background(), req); err == nil {
			t.Errorf("%s: expected error for URI %q, got nil", tc.name, tc.uri)
		}
	}
}

func TestMcpCLICommands(t *testing.T) {
	// Setup a temporary workspace with skills
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "zeta"), 0755); err != nil {
		t.Fatal(err)
	}
	zetaContent := "---\nname: zeta-skill\ndescription: Handle zeta work.\n---\n# Zeta skill contents\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "zeta", "SKILL.md"), []byte(zetaContent), 0644); err != nil {
		t.Fatal(err)
	}

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldCwd)
	}()

	// Test list-resources output
	var listBuf bytes.Buffer
	if err := listResourcesCmd(&listBuf); err != nil {
		t.Fatalf("listResourcesCmd failed: %v", err)
	}
	listOut := listBuf.String()
	if !strings.Contains(listOut, "botfam:///tools") || !strings.Contains(listOut, "botfam:///skills/{name}") {
		t.Errorf("listResourcesCmd output missing expected elements: %q", listOut)
	}

	// Test read-resource output
	var readBuf bytes.Buffer
	if err := readResourceCmd(&readBuf, "botfam:///skills/zeta-skill"); err != nil {
		t.Fatalf("readResourceCmd failed: %v", err)
	}
	if readBuf.String() != zetaContent {
		t.Errorf("readResourceCmd output expected %q, got %q", zetaContent, readBuf.String())
	}
}

// TestMcpWikiCacheFallback exercises the #119 wiki provider via the local-cache
// tier: with no forge client resolvable (no token), botfam:///wiki/* is served
// from the local wiki/ clone and flagged stale.
func TestMcpWikiCacheFallback(t *testing.T) {
	s, root := newTestServer(t)
	initGitRepo(t, root) // no remote → forge.NewClient won't resolve → cache tier

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	wikiDir := filepath.Join(root, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wikiDir, "Home.md"), []byte("# Home\ncached body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := mcplib.ReadResourceRequest{}

	// A page comes from the cache and is flagged stale.
	req.Params.URI = "botfam:///wiki/Home"
	res, err := s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("wiki/Home: %v", err)
	}
	txt := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(txt, "cached body") || !strings.Contains(txt, "STALE") {
		t.Errorf("expected cached, stale page, got %q", txt)
	}

	// The JSON index carries the wiki schema.
	req.Params.URI = "botfam:///wiki/index.json"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("wiki/index.json: %v", err)
	}
	if !strings.Contains(res[0].(mcplib.TextResourceContents).Text, "botfam.wiki.index.v1") {
		t.Errorf("missing wiki index schema: %q", res[0].(mcplib.TextResourceContents).Text)
	}

	// A traversal page name must error.
	req.Params.URI = "botfam:///wiki/not%2Fa%2Fpage"
	if _, err := s.handleReadResource(context.Background(), req); err == nil {
		t.Error("expected error for invalid wiki page name")
	}
}

// TestMcpProjections covers #120: a fam-declared wiki_projections entry is
// served as botfam:///<name>[.json], filtering the wiki index by glob.
func TestMcpProjections(t *testing.T) {
	s, root := newTestServer(t)

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wtDir := filepath.Join(root, "wt-agy")
	if err := os.Chdir(wtDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	// Declare a projection on the resolved fam's stanza.
	registerFam(t, "mockfam", root, nil, func(rc *famconfig.RepoConfig) {
		rc.WikiProjections = []string{"reviews:review-*"}
	})
	// Local wiki cache: two reviews + one unrelated page.
	wikiDir := filepath.Join(wtDir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review-2026-06-14-agy", "review-2026-06-13-claude", "Home"} {
		if err := os.WriteFile(filepath.Join(wikiDir, name+".md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	req := mcplib.ReadResourceRequest{}

	// Markdown projection lists only review-* pages.
	req.Params.URI = "botfam:///reviews"
	res, err := s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("reviews: %v", err)
	}
	txt := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(txt, "review-2026-06-14-agy") || strings.Contains(txt, "Home") {
		t.Errorf("projection should list reviews only, got %q", txt)
	}

	// JSON projection carries the schema and filtered pages.
	req.Params.URI = "botfam:///reviews.json"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("reviews.json: %v", err)
	}
	jtxt := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(jtxt, "botfam.projection.v1") || !strings.Contains(jtxt, "review-2026-06-13-claude") {
		t.Errorf("unexpected projection json: %q", jtxt)
	}

	// An undeclared projection name still errors.
	req.Params.URI = "botfam:///nonexistent-projection"
	if _, err := s.handleReadResource(context.Background(), req); err == nil {
		t.Error("expected error for undeclared projection")
	}
}

func TestMcpDefaultMemoryProjection(t *testing.T) {
	s, root := newTestServer(t)

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wtDir := filepath.Join(root, "wt-agy")
	if err := os.Chdir(wtDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	// The mock fam declares no projections, so the default memory projection applies.
	// Local wiki cache: memory pages and home.
	wikiDir := filepath.Join(wtDir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory-first-fact", "memory-second-fact", "Home"} {
		if err := os.WriteFile(filepath.Join(wikiDir, name+".md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	req := mcplib.ReadResourceRequest{}

	// 1. Markdown memory projection lists only memory-* pages.
	req.Params.URI = "botfam:///memory"
	res, err := s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	txt := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(txt, "memory-first-fact") || strings.Contains(txt, "Home") {
		t.Errorf("projection should list memory facts only, got %q", txt)
	}

	// 2. Read discovery root to verify projection is advertised.
	req.Params.URI = "botfam:///"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	rootTxt := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(rootTxt, "botfam:///memory") {
		t.Errorf("expected memory projection advertised in root, got %q", rootTxt)
	}

	// 3. Read index.json to verify projection is advertised.
	req.Params.URI = "botfam:///index.json"
	res, err = s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	var idx struct {
		Resources []string `json:"resources"`
	}
	if err := json.Unmarshal([]byte(res[0].(mcplib.TextResourceContents).Text), &idx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, resURI := range idx.Resources {
		if resURI == "botfam:///memory" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("memory projection not advertised in index.json: %v", idx.Resources)
	}
}

func TestCallToolWithDotWorkDirAtRoot(t *testing.T) {
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCWD)

	s := &server{}

	baseDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(baseDir); err == nil {
		baseDir = eval
	}

	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	registerFam(t, "myfam", baseDir, map[string]famconfig.AgentConfig{
		"agy": {Harness: "antigravity"},
	})

	wtDir := setupTestWorktree(t, baseDir, "agy", "agy")

	// Create required log files
	if err := os.MkdirAll(filepath.Join(wtDir, "scratch", "irc", "agy"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "scratch", "irc", "agy", "log"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BOTFAM_FAM", "")
	t.Setenv("PWD", wtDir)

	if err := os.Chdir("/"); err != nil {
		t.Skipf("cannot chdir to /: %v", err)
	}

	_, err = s.callTool(context.Background(), "irc_read", map[string]any{"work_dir": "."})
	if err != nil {
		t.Fatalf("callTool failed when CWD=/ with work_dir=\".\": %v", err)
	}
}
