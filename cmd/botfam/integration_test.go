package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rlupi/botfam/internal/fam"
)

func TestIntegrationTwoActorsOverStdio(t *testing.T) {
	if os.Getenv("BOTFAM_TEST_HELPER") == "serve" {
		os.Args = []string{"botfam", "serve"}
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	root := t.TempDir()
	alice := startBotfam(t, root, "alice")
	bob := startBotfam(t, root, "bob")
	defer alice.Close(t)
	defer bob.Close(t)

	alice.Call(t, "initialize", map[string]any{})
	bob.Call(t, "initialize", map[string]any{})

	recvDone := make(chan map[string]any, 1)
	go func() {
		recvDone <- bob.Tool(t, "recv", map[string]any{"timeout_s": 5})
	}()

	time.Sleep(100 * time.Millisecond)
	sent := alice.Tool(t, "send", map[string]any{
		"to":      "bob",
		"type":    "handoff",
		"payload": map[string]any{"note": "hello from alice"},
	})
	sentID := sent["id"].(string)

	var got map[string]any
	select {
	case got = <-recvDone:
	case <-time.After(3 * time.Second):
		t.Fatal("bob recv did not unblock after alice send")
	}
	if got["id"] != sentID {
		t.Fatalf("recv id = %v, want %s", got["id"], sentID)
	}
	if got["from"] != "alice" || got["to"] != "bob" || got["type"] != "handoff" {
		t.Fatalf("unexpected received envelope: %#v", got)
	}

	bob.Tool(t, "ack", map[string]any{"id": sentID, "outcome": map[string]any{"handled": true}})
	seen := bob.Tool(t, "seen", map[string]any{"id": sentID})
	if seen["seen"] != true {
		t.Fatalf("seen = %#v, want true", seen)
	}

	task := alice.Tool(t, "post", map[string]any{"payload": map[string]any{"job": "review"}})
	claimed := bob.Tool(t, "claim", map[string]any{"lease_ttl": 30})
	if claimed["id"] != task["id"] {
		t.Fatalf("claimed task = %#v, want %#v", claimed, task)
	}
	completed := bob.Tool(t, "complete", map[string]any{
		"task_id": claimed["id"],
		"result":  map[string]any{"ok": true},
	})
	if completed["status"] != "done" {
		t.Fatalf("completed task status = %v, want done", completed["status"])
	}

	inbox := bob.Tool(t, "inbox", map[string]any{})
	tasks := inbox["tasks"].(map[string]any)
	if tasks["done"].(float64) < 1 {
		t.Fatalf("inbox tasks = %#v, want at least one done task", tasks)
	}

	if _, err := os.Stat(filepath.Join(root, "bob", "cur")); err != nil {
		t.Fatalf("bob cur mailbox missing: %v", err)
	}
}

func TestIntegrationIdentityBindingAndActorLock(t *testing.T) {
	if os.Getenv("BOTFAM_TEST_HELPER") == "serve" {
		os.Args = []string{"botfam", "serve"}
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	root := t.TempDir()
	unbound := startBotfam(t, root, "")
	defer unbound.Close(t)
	unbound.Call(t, "initialize", map[string]any{})
	unbound.Tool(t, "send", map[string]any{
		"actor":   "alice",
		"to":      "dave",
		"type":    "note",
		"payload": map[string]any{"n": 1},
	})
	if err := unbound.ToolError(t, "send", map[string]any{
		"actor":   "carol",
		"to":      "dave",
		"type":    "note",
		"payload": map[string]any{"n": 2},
	}); err == "" {
		t.Fatal("conflicting actor did not fail after sticky bind")
	}

	bob1 := startBotfam(t, root, "bob")
	bob2 := startBotfam(t, root, "bob")
	defer bob1.Close(t)
	defer bob2.Close(t)
	bob1.Call(t, "initialize", map[string]any{})
	bob2.Call(t, "initialize", map[string]any{})
	if got := bob1.Tool(t, "try_recv", map[string]any{}); got != nil {
		t.Fatalf("first bob try_recv got %v, want nil", got)
	}
	if err := bob2.ToolError(t, "try_recv", map[string]any{}); err == "" {
		t.Fatal("second bob process acquired receive lock unexpectedly")
	}
}

type botClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	nextID int
}

func startBotfam(t *testing.T, root, actor string) *botClient {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestIntegrationTwoActorsOverStdio")
	cmd.Env = append(os.Environ(),
		"BOTFAM_TEST_HELPER=serve",
		"COLLAB_ROOT="+root,
	)
	if actor != "" {
		cmd.Env = append(cmd.Env, "COLLAB_ACTOR="+actor)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &botClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr, nextID: 1}
}

func (c *botClient) Close(t *testing.T) {
	t.Helper()
	_ = c.stdin.Close()
	if err := c.cmd.Wait(); err != nil {
		t.Fatalf("botfam helper exited with %v; stderr:\n%s", err, c.stderr.String())
	}
}

func (c *botClient) Call(t *testing.T, method string, params map[string]any) map[string]any {
	t.Helper()
	id := c.nextID
	c.nextID++
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.stdin, "%s\n", body); err != nil {
		t.Fatalf("write request: %v; stderr:\n%s", err, c.stderr.String())
	}
	resp := c.readResponse(t)
	if errObj, ok := resp["error"].(map[string]any); ok {
		t.Fatalf("%s error: %#v; stderr:\n%s", method, errObj, c.stderr.String())
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s result = %#v, want object", method, resp["result"])
	}
	return result
}

func (c *botClient) CallError(t *testing.T, method string, params map[string]any) string {
	t.Helper()
	id := c.nextID
	c.nextID++
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.stdin, "%s\n", body); err != nil {
		t.Fatalf("write request: %v; stderr:\n%s", err, c.stderr.String())
	}
	resp := c.readResponse(t)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("%s succeeded unexpectedly: %#v", method, resp["result"])
	}
	msg, _ := errObj["message"].(string)
	return msg
}

func (c *botClient) Tool(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	result := c.Call(t, "tools/call", map[string]any{"name": name, "arguments": args})
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("tool %s content = %#v", name, content)
	}
	item := content[0].(map[string]any)
	text := item["text"].(string)
	if text == "null" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool %s payload %q: %v", name, text, err)
	}
	return payload
}

func (c *botClient) ToolError(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	return c.CallError(t, "tools/call", map[string]any{"name": name, "arguments": args})
}

func (c *botClient) readResponse(t *testing.T) map[string]any {
	t.Helper()
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			t.Fatalf("read response: %v; stderr:\n%s", err, c.stderr.String())
		}
		if len(bytes.TrimSpace([]byte(line))) == 0 {
			continue
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("response %q: %v", line, err)
		}
		return resp
	}
}

func TestIntegrationWorktreeBasedResolution(t *testing.T) {
	if os.Getenv("BOTFAM_TEST_HELPER") == "serve" {
		os.Args = []string{"botfam", "serve"}
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	_ = os.MkdirAll(homeDir, 0o755)

	gitDir := filepath.Join(tempDir, "myrepo")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, gitDir)

	t.Setenv("HOME", homeDir)

	t.Chdir(gitDir)
	var setupOut bytes.Buffer
	if err := fam.Setup([]string{"myproj", "--agents", "alice,bob"}, &setupOut); err != nil {
		t.Fatalf("fam.Setup failed: %v", err)
	}

	aliceWorkspace := filepath.Join(gitDir, "wt-alice")
	_ = os.MkdirAll(aliceWorkspace, 0o755)

	bobWorkspace := filepath.Join(gitDir, "wt-bob")
	_ = os.MkdirAll(bobWorkspace, 0o755)

	startClient := func(workDir string) *botClient {
		cmd := exec.Command(os.Args[0], "-test.run=TestIntegrationWorktreeBasedResolution")
		cmd.Env = append(os.Environ(),
			"BOTFAM_TEST_HELPER=serve",
			"HOME="+homeDir,
		)
		cmd.Dir = workDir
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return &botClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr, nextID: 1}
	}

	alice := startClient(aliceWorkspace)
	bob := startClient(bobWorkspace)
	defer alice.Close(t)
	defer bob.Close(t)

	alice.Call(t, "initialize", map[string]any{})
	bob.Call(t, "initialize", map[string]any{})

	sent := alice.Tool(t, "send", map[string]any{
		"to":   "bob",
		"type": "hello",
		"payload": map[string]any{"msg": "hi"},
	})
	sentID := sent["id"].(string)

	got := bob.Tool(t, "try_recv", map[string]any{})
	if got == nil {
		t.Fatal("bob did not receive the message")
	}
	if got["id"] != sentID {
		t.Fatalf("expected message ID %s, got %v", sentID, got["id"])
	}
	if got["from"] != "alice" || got["to"] != "bob" {
		t.Fatalf("unexpected envelope: %+v", got)
	}

	r := fam.Resolver{WorkDir: gitDir}
	info, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Root); err != nil {
		t.Fatalf("expected botfam root to exist at %s, but got error: %v", info.Root, err)
	}
}

func TestIntegrationSessionsOverStdio(t *testing.T) {
	root := t.TempDir()

	// 1. Compile the real botfam binary to root/botfam
	binPath := filepath.Join(root, "botfam")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	var buildStderr bytes.Buffer
	buildCmd.Stderr = &buildStderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("go build failed: %v; stderr:\n%s", err, buildStderr.String())
	}

	// Helper to start real botfam serve as a subprocess
	startRealBot := func(actor string) *botClient {
		cmd := exec.Command(binPath, "serve")
		cmd.Env = append(os.Environ(),
			"COLLAB_ROOT="+root,
			"COLLAB_ACTOR="+actor,
		)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return &botClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr, nextID: 1}
	}

	alice := startRealBot("alice")
	bob := startRealBot("bob")
	defer alice.Close(t)
	defer bob.Close(t)

	alice.Call(t, "initialize", map[string]any{})
	bob.Call(t, "initialize", map[string]any{})

	// Helper to call a tool that returns a JSON array
	toolList := func(c *botClient, name string, args map[string]any) []map[string]any {
		result := c.Call(t, "tools/call", map[string]any{"name": name, "arguments": args})
		content := result["content"].([]any)
		if len(content) != 1 {
			t.Fatalf("tool %s content = %#v", name, content)
		}
		item := content[0].(map[string]any)
		text := item["text"].(string)
		if text == "null" {
			return nil
		}
		var payload []map[string]any
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			t.Fatalf("tool %s payload %q: %v", name, text, err)
		}
		return payload
	}

	// 2. Kickoff session using CLI
	cmd := exec.Command(binPath, "session", "new", "test-mcp-session", "--participants", "alice,bob")
	cmd.Env = append(os.Environ(), "COLLAB_ROOT="+root)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("session new failed: %v; stderr:\n%s", err, stderr.String())
	}

	// 3. Alice appends to session
	entry1 := alice.Tool(t, "session_append", map[string]any{
		"session": "test-mcp-session",
		"body":    "Hello from Alice",
		"handoff": map[string]any{
			"task":        "Review design",
			"context":     "draft docs",
			"deliverable": "approval",
		},
	})
	if entry1["actor"] != "alice" || entry1["body"] != "Hello from Alice" {
		t.Fatalf("unexpected entry1: %+v", entry1)
	}

	// 4. Bob reads session
	entries := toolList(bob, "session_read", map[string]any{
		"session": "test-mcp-session",
	})
	if len(entries) != 1 || entries[0]["id"] != entry1["id"] {
		t.Fatalf("bob read entries = %+v, expected Alice's entry", entries)
	}

	// 5. Bob appends reply
	entry2 := bob.Tool(t, "session_append", map[string]any{
		"session": "test-mcp-session",
		"body":    "Approved by Bob",
	})

	// 6. Alice reads all
	entries2 := toolList(alice, "session_read", map[string]any{
		"session": "test-mcp-session",
	})
	if len(entries2) != 2 || entries2[1]["id"] != entry2["id"] {
		t.Fatalf("alice read entries = %+v, expected 2 entries with Bob's reply", entries2)
	}

	// 7. Close session using CLI
	cmdClose := exec.Command(binPath, "session", "close", "test-mcp-session")
	cmdClose.Env = append(os.Environ(), "COLLAB_ROOT="+root, "BOTFAM_FORCE_CLOSE=1")
	cmdClose.Dir = root
	var stderrClose bytes.Buffer
	cmdClose.Stderr = &stderrClose
	if err := cmdClose.Run(); err != nil {
		t.Fatalf("session close failed: %v; stderr:\n%s", err, stderrClose.String())
	}

	// Verify rendered session.md
	expectedFile := filepath.Join(root, "doc", "collab", "sessions", "test-mcp-session", "session.md")
	b, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(b)
	if !strings.Contains(rendered, "# Session: test-mcp-session") ||
		!strings.Contains(rendered, "## [alice,") ||
		!strings.Contains(rendered, "Hello from Alice") ||
		!strings.Contains(rendered, "## [bob,") ||
		!strings.Contains(rendered, "Approved by Bob") {
		t.Fatalf("unexpected closed session markdown contents:\n%s", rendered)
	}
}

func TestIntegrationSessionCloseTTYGate(t *testing.T) {
	root := t.TempDir()

	binPath := filepath.Join(root, "botfam")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	var buildStderr bytes.Buffer
	buildCmd.Stderr = &buildStderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("go build failed: %v; stderr:\n%s", err, buildStderr.String())
	}

	cmdNew := exec.Command(binPath, "session", "new", "test-tty-session", "--participants", "alice")
	cmdNew.Env = append(os.Environ(), "COLLAB_ROOT="+root)
	if err := cmdNew.Run(); err != nil {
		t.Fatalf("session new failed: %v", err)
	}

	cmdClose := exec.Command(binPath, "session", "close", "test-tty-session")
	cmdClose.Env = append(os.Environ(), "COLLAB_ROOT="+root)
	cmdClose.Dir = root
	var stderr bytes.Buffer
	cmdClose.Stderr = &stderr
	err := cmdClose.Run()
	if err == nil {
		t.Fatal("session close succeeded unexpectedly without a TTY or BOTFAM_FORCE_CLOSE")
	}

	expectedErr := "session close is the operator's promotion gesture and requires a terminal; agents: write your closeout entry and hand back"
	if !strings.Contains(stderr.String(), expectedErr) {
		t.Errorf("expected error containing %q, got %q", expectedErr, stderr.String())
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


func TestIntegrationB_NarrowSafety(t *testing.T) {
	if os.Getenv("BOTFAM_TEST_HELPER") == "serve" {
		os.Args = []string{"botfam", "serve"}
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	root := t.TempDir()

	// 1. Compile the real botfam binary to root/botfam
	binPath := filepath.Join(root, "botfam")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	var buildStderr bytes.Buffer
	buildCmd.Stderr = &buildStderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("go build failed: %v; stderr:\n%s", err, buildStderr.String())
	}

	// Initialize the git repo in root
	gitDir := filepath.Join(root, "myrepo")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, gitDir)

	// Setup family
	homeDir := filepath.Join(root, "home")
	_ = os.MkdirAll(homeDir, 0o755)
	t.Setenv("HOME", homeDir)

	t.Chdir(gitDir)
	var setupOut bytes.Buffer
	if err := fam.Setup([]string{"myproj", "--agents", "alice,bob"}, &setupOut); err != nil {
		t.Fatalf("fam.Setup failed: %v", err)
	}

	// Create named worktrees
	aliceWT := filepath.Join(gitDir, "wt-alice")
	_ = os.MkdirAll(aliceWT, 0o755)

	// Helper to run MCP server stdio in a specific workdir with env
	runMCPServer := func(workDir string, env []string, method string, params map[string]any) (map[string]any, string) {
		cmd := exec.Command(os.Args[0], "-test.run=TestIntegrationB_NarrowSafety")
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(),
			"BOTFAM_TEST_HELPER=serve",
			"HOME="+homeDir,
		)
		cmd.Env = append(cmd.Env, env...)

		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		// Write initialize request
		initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
		fmt.Fprintln(stdin, initReq)

		// Write tools/call request
		callReq := map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      method,
				"arguments": params,
			},
		}
		reqBody, err := json.Marshal(callReq)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(stdin, string(reqBody))
		_ = stdin.Close()

		// Read responses
		scanner := bufio.NewScanner(stdout)
		var lastResult map[string]any
		var lastError string

		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			var resp map[string]any
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				continue
			}
			if idFloat, ok := resp["id"].(float64); ok && idFloat == 2 {
				if errObj, ok := resp["error"].(map[string]any); ok {
					lastError, _ = errObj["message"].(string)
				} else if resObj, ok := resp["result"].(map[string]any); ok {
					lastResult = resObj
				}
			}
		}

		_ = cmd.Wait()
		return lastResult, lastError
	}

	// 1. Conflict check: try to masquerade as bob from wt-alice directory using COLLAB_ACTOR
	_, errStr := runMCPServer(aliceWT, []string{"COLLAB_ACTOR=bob"}, "session_append", map[string]any{
		"session": "test-session",
		"body":    "hi",
	})
	if errStr == "" || !strings.Contains(errStr, "conflicts with resolved directory actor") {
		t.Fatalf("expected conflict error for mismatched COLLAB_ACTOR, got: %s", errStr)
	}

	// 2. Conflict check: try to masquerade as bob from wt-alice directory using explicit actor argument
	_, errStr = runMCPServer(aliceWT, nil, "session_append", map[string]any{
		"actor":   "bob",
		"session": "test-session",
		"body":    "hi",
	})
	if errStr == "" || !strings.Contains(errStr, "conflicts with resolved directory actor") {
		t.Fatalf("expected conflict error for mismatched explicit actor, got: %s", errStr)
	}

	// 3. Test Session Handoff validation
	// Let's create a session first via CLI
	cmdNew := exec.Command(binPath, "session", "new", "test-safety-session", "--participants", "alice,bob")
	cmdNew.Env = append(os.Environ(), "HOME="+homeDir)
	cmdNew.Dir = aliceWT
	if err := cmdNew.Run(); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Try appending with invalid handoffs from aliceWT (acting as alice)
	_, errStr = runMCPServer(aliceWT, nil, "session_append", map[string]any{
		"session": "test-safety-session",
		"body":    "my body",
		"handoff": map[string]any{
			"task":        "",
			"context":     "ctx",
			"deliverable": "deliv",
		},
	})
	if errStr == "" || !strings.Contains(errStr, "invalid handoff: task cannot be empty or whitespace only") {
		t.Fatalf("expected task empty error, got: %s", errStr)
	}

	_, errStr = runMCPServer(aliceWT, nil, "session_append", map[string]any{
		"session": "test-safety-session",
		"body":    "my body",
		"handoff": map[string]any{
			"task":        "task",
			"context":     "  ",
			"deliverable": "deliv",
		},
	})
	if errStr == "" || !strings.Contains(errStr, "invalid handoff: context cannot be empty or whitespace only") {
		t.Fatalf("expected context empty error, got: %s", errStr)
	}
}
