package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
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
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
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
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
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
	tp := textproto.NewReader(c.stdout)
	header, err := tp.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("read response header: %v; stderr:\n%s", err, c.stderr.String())
	}
	n, err := strconv.Atoi(header.Get("Content-Length"))
	if err != nil {
		t.Fatalf("bad Content-Length %q: %v", header.Get("Content-Length"), err)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		t.Fatalf("read response body: %v; stderr:\n%s", err, c.stderr.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response %q: %v", string(body), err)
	}
	return resp
}
