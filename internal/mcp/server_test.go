package mcp

import (
	"bufio"
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

	"github.com/robertolupi/botfam/internal/fam"
)

func newTestServer(t *testing.T) (*server, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("COLLAB_ROOT", root)
	t.Setenv("COLLAB_ACTOR", "")
	t.Setenv("BOTFAM_LOCK_ACTOR", "")
	t.Setenv("BOTFAM_TESTING", "1")

	return &server{
		envActor: "",
		lockMode: false,
	}, root
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

	// A later call whose work_dir resolves to a different actor must conflict.
	_, err := s.callTool(context.Background(), "irc_read", map[string]any{"work_dir": bobDir})
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
	runCmd(mainDir, "git", "init")
	runCmd(mainDir, "git", "config", "user.name", "test")
	runCmd(mainDir, "git", "config", "user.email", "test@example.com")
	runCmd(mainDir, "git", "commit", "--allow-empty", "-m", "initial commit")

	wtDir := filepath.Join(baseDir, name)
	runCmd(mainDir, "git", "worktree", "add", wtDir)

	runCmd(wtDir, "git", "config", "extensions.worktreeConfig", "true")
	runCmd(wtDir, "git", "config", "--worktree", "user.name", actor)
	runCmd(wtDir, "git", "config", "--worktree", "user.email", actor+"@example.com")
	return wtDir
}

func TestIdentityOptionalToolsWithoutIdentity(t *testing.T) {
	s, _ := newTestServer(t)
	// A directory with no wt-/botfam- prefix yields no directory actor.
	base := t.TempDir()
	plainDir := setupTestWorktree(t, base, "myrepo", "someactor")

	// Create a mock fam.toml so worktree commands can resolve it
	tomlContent := `name = "mockfam"
roster = ["alice", "bob"]
repo_paths = []
`
	if err := os.WriteFile(filepath.Join(plainDir, "fam.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	// worktree_sync does not use the calling actor.
	if _, err := s.callTool(context.Background(), "worktree_sync", map[string]any{"work_dir": plainDir}); err != nil {
		t.Fatalf("worktree_sync without identity failed: %v", err)
	}
	if s.actor != "" {
		t.Errorf("identity-optional call must not bind an actor, got %q", s.actor)
	}

	// Identity-requiring tools must still fail without an identity.
	_, err := s.callTool(context.Background(), "irc_read", map[string]any{"work_dir": plainDir})
	if err == nil {
		t.Fatal("expected identity error for irc_read without identity")
	}
	if !strings.Contains(err.Error(), "identity required") {
		t.Errorf("expected identity-required error, got %q", err.Error())
	}
}

func TestIdentityOptionalToolsStillEnforceConflictsAndBinding(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := setupTestWorktree(t, base, "wt-alice", "alice")

	// Create mock fam.toml in aliceDir
	tomlContent := `name = "mockfam"
roster = ["alice", "bob"]
repo_paths = []
`
	if err := os.WriteFile(filepath.Join(aliceDir, "fam.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Conflicting explicit actor vs directory actor must still be rejected,
	// even for an identity-optional tool.
	_, err := s.callTool(context.Background(), "worktree_sync", map[string]any{
		"work_dir": aliceDir,
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
	aliceDir := mkdir(t, filepath.Join(base, "wt-alice"))

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
	aliceDir := mkdir(t, filepath.Join(base, "wt-alice"))

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
	aliceDir := mkdir(t, filepath.Join(base, "wt-alice"))

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

func TestIrcWaitToolTimeout(t *testing.T) {
	s, _ := newTestServer(t)
	base := t.TempDir()
	aliceDir := mkdir(t, filepath.Join(base, "wt-alice"))

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

	// Make root a mock git repository so fam.RepoPath resolves to it
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

	// Create a dummy PROTOCOL.md under the temp root's doc/collab/
	collabDir := filepath.Join(root, "doc", "collab")
	if err := os.MkdirAll(collabDir, 0755); err != nil {
		t.Fatal(err)
	}
	dummyContent := "dummy protocol content"
	if err := os.WriteFile(filepath.Join(collabDir, "PROTOCOL.md"), []byte(dummyContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Test empty authority (local family docs)
	req := mcplib.ReadResourceRequest{}
	req.Params.URI = "botfam:///docs/protocol"
	res, err := s.handleReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to read empty authority resource: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("no resource contents returned")
	}
	tr, ok := res[0].(mcplib.TextResourceContents)
	if !ok {
		t.Fatalf("expected text resource contents, got %T", res[0])
	}
	if tr.Text != dummyContent {
		t.Errorf("expected text %q, got %q", dummyContent, tr.Text)
	}

	// 2. Test named authority matching local family
	resolved, err := (fam.Resolver{WorkDir: root}).Resolve()
	if err == nil && resolved.Name != "" {
		req.Params.URI = fmt.Sprintf("botfam://%s/docs/protocol", resolved.Name)
		res, err = s.handleReadResource(context.Background(), req)
		if err != nil {
			t.Fatalf("failed to read local named authority resource: %v", err)
		}
		tr = res[0].(mcplib.TextResourceContents)
		if tr.Text != dummyContent {
			t.Errorf("expected text %q, got %q", dummyContent, tr.Text)
		}
	}

	// 3. Negative cases: unknown path, unsupported scheme, and an unknown
	// named authority must all error rather than read an unintended file.
	negatives := []struct {
		name string
		uri  string
	}{
		{"unknown path", "botfam:///docs/nonexistent"},
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
