package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewfamCmdCreatesWorktreesAndHarnessConfig(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	temp := t.TempDir()
	bin := filepath.Join(temp, "botfam")
	runCmd(t, repoRoot, "go", "build", "-o", bin, "./cmd/botfam")

	target := filepath.Join(temp, "repo")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, target)

	// Create and commit a mock PROTOCOL.md so git worktrees check it out
	if err := os.MkdirAll(filepath.Join(target, "doc/collab"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "doc/collab/PROTOCOL.md"), []byte("botfam Coordination Protocol"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, target, "git", "add", "doc/collab/PROTOCOL.md")
	runCmd(t, target, "git", "commit", "-m", "add protocol doc")

	home := filepath.Join(temp, "home")
	runNewfam(t, home, bin, target, "myproject", "--agents", "agy,codex,claude")
	// Run again to verify idempotency/already existing checkouts
	runNewfam(t, home, bin, target, "myproject", "--agents", "agy,codex,claude")

	for _, agent := range []string{"agy", "codex", "claude"} {
		wt := filepath.Join(temp, "wt-"+agent)
		if got := strings.TrimSpace(string(runCmdOutput(t, wt, "git", "branch", "--show-current"))); got != "agent/"+agent {
			t.Fatalf("%s branch = %q, want %q", wt, got, "agent/"+agent)
		}
		if _, err := os.Stat(filepath.Join(wt, ".mcp.json")); !os.IsNotExist(err) {
			t.Fatalf("expected wt/.mcp.json to be deleted/not exist, got: %v", err)
		}
		if _, err := os.Stat(filepath.Join(wt, ".codex")); !os.IsNotExist(err) {
			t.Fatalf("expected wt/.codex directory to be deleted/not exist, got: %v", err)
		}
		if _, err := os.Stat(filepath.Join(wt, ".agents")); !os.IsNotExist(err) {
			t.Fatalf("expected wt/.agents directory to be deleted/not exist, got: %v", err)
		}
		assertFileContains(t, filepath.Join(wt, ".claude", "settings.json"), `"Bash(botfam:*)"`)
		assertFileContains(t, filepath.Join(wt, "AGENTS.md"), "doc/collab/PROTOCOL.md")
		assertFileContains(t, filepath.Join(wt, "CLAUDE.md"), "doc/collab/PROTOCOL.md")
		assertFileContains(t, filepath.Join(wt, "GEMINI.md"), "doc/collab/PROTOCOL.md")
		assertFileContains(t, filepath.Join(wt, "doc/collab/PROTOCOL.md"), "botfam Coordination Protocol")
	}

	initRes := callBotfamTool(t, home, filepath.Join(temp, "wt-codex"), bin, "worktree_init", map[string]any{
		"target_actor": "codex",
	})
	if ok, _ := initRes["ok"].(bool); !ok {
		t.Fatalf("expected worktree_init to succeed, got: %#v", initRes)
	}

	res := callBotfamTool(t, home, filepath.Join(temp, "wt-codex"), bin, "worktree_sync", map[string]any{})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("expected worktree_sync to succeed, got: %#v", res)
	}
}

func TestNewfamCmdRejectsUnsafeInputs(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	temp := t.TempDir()
	bin := filepath.Join(temp, "botfam")
	runCmd(t, repoRoot, "go", "build", "-o", bin, "./cmd/botfam")

	target := filepath.Join(temp, "repo")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, target)

	// Create and commit a mock PROTOCOL.md so git worktrees check it out
	if err := os.MkdirAll(filepath.Join(target, "doc/collab"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "doc/collab/PROTOCOL.md"), []byte("botfam Coordination Protocol"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, target, "git", "add", "doc/collab/PROTOCOL.md")
	runCmd(t, target, "git", "commit", "-m", "add protocol doc")

	home := filepath.Join(temp, "home")
	if out := runNewfamError(t, home, bin, target, "myproj", "--agents", "../agy"); !strings.Contains(out, "invalid agent") {
		t.Fatalf("unsafe agent output = %q, want invalid agent name", out)
	}

	if err := os.Mkdir(filepath.Join(target, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".codex", "config.toml"), []byte("[mcp_servers.other]\ncommand = \"other\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, target, "git", "add", ".codex/config.toml")
	runCmd(t, target, "git", "commit", "-m", "track codex config")

	if err := os.WriteFile(filepath.Join(target, "AGENTS.md"), []byte("# botfam fam member — read this first\n\nold identity text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runNewfam(t, home, bin, target, "myproject", "--agents", "agy")
	if _, err := os.Stat(filepath.Join(target, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("expected target/.codex to be deleted/not exist, got: %v", err)
	}
	assertFileContains(t, filepath.Join(target, "AGENTS.md"), "doc/collab/PROTOCOL.md")
	assertFileContains(t, filepath.Join(target, "CLAUDE.md"), "doc/collab/PROTOCOL.md")
	assertFileContains(t, filepath.Join(target, "GEMINI.md"), "doc/collab/PROTOCOL.md")
	assertFileContains(t, filepath.Join(target, "doc/collab/PROTOCOL.md"), "botfam Coordination Protocol")
	assertFileNotContains(t, filepath.Join(target, "AGENTS.md"), "old identity text")
}

func runNewfam(t *testing.T, home, bin, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"newfam"}, args...)...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "HOME="+home, "USER=testoperator")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s failed: %v\n%s", strings.Join(cmd.Args, " "), err, out.String())
	}
}

func runNewfamError(t *testing.T, home, bin, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"newfam"}, args...)...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "HOME="+home, "USER=testoperator")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("%s succeeded unexpectedly\n%s", strings.Join(cmd.Args, " "), out.String())
	}
	return out.String()
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out.String())
	}
}

func runCmdOutput(t *testing.T, dir, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return out
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), want) {
		t.Fatalf("%s does not contain %q:\n%s", path, want, string(b))
	}
}

func assertFileNotContains(t *testing.T, path, unwanted string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), unwanted) {
		t.Fatalf("%s contains unwanted %q:\n%s", path, unwanted, string(b))
	}
}

func callBotfamTool(t *testing.T, home, workDir, bin, name string, args map[string]any) map[string]any {
	t.Helper()
	cmd := exec.Command(bin, "serve")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "HOME="+home)
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

	writeJSONRPC(t, stdin, 1, "initialize", map[string]any{})
	writeJSONRPC(t, stdin, 2, "tools/call", map[string]any{"name": name, "arguments": args})
	_ = stdin.Close()

	sc := bufio.NewScanner(stdout)
	responses := []map[string]any{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		responses = append(responses, resp)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("botfam serve failed: %v\n%s", err, stderr.String())
	}
	if len(responses) < 2 {
		t.Fatalf("responses = %#v, stderr:\n%s", responses, stderr.String())
	}
	resp := responses[1]
	if errObj, ok := resp["error"]; ok {
		t.Fatalf("tool %s error: %#v\n%s", name, errObj, stderr.String())
	}
	content := resp["result"].(map[string]any)["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if text == "null" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool payload %q: %v", text, err)
	}
	return payload
}

func writeJSONRPC(t *testing.T, w io.Writer, id int, method string, params map[string]any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
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
