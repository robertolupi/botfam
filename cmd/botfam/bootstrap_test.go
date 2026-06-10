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

func TestBootstrapScriptCreatesWorktreesAndHarnessConfig(t *testing.T) {
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

	home := filepath.Join(temp, "home")
	worktrees := filepath.Join(temp, "worktrees")
	script := filepath.Join(repoRoot, "bootstrap-botfam.sh")
	runBootstrap(t, home, script, target, "--agents", "agy,codex,claude", "--botfam-bin", bin, "--worktree-dir", worktrees)
	runBootstrap(t, home, script, target, "--agents", "agy,codex,claude", "--botfam-bin", bin, "--worktree-dir", worktrees)

	for _, agent := range []string{"agy", "codex", "claude"} {
		wt := filepath.Join(worktrees, "wt-"+agent)
		if got := strings.TrimSpace(string(runCmdOutput(t, wt, "git", "branch", "--show-current"))); got != "agent/"+agent {
			t.Fatalf("%s branch = %q, want %q", wt, got, "agent/"+agent)
		}
		assertFileContains(t, filepath.Join(wt, ".mcp.json"), `"command": "`+bin+`"`)
		assertFileContains(t, filepath.Join(wt, ".codex", "config.toml"), `command = "`+bin+`"`)
		assertFileContains(t, filepath.Join(wt, ".agents", "mcp_config.json"), `"command": "`+bin+`"`)
		assertFileContains(t, filepath.Join(wt, ".claude", "settings.json"), `"mcp__collab__*"`)
		assertFileContains(t, filepath.Join(wt, "AGENTS.md"), "wt-"+agent)
	}

	send := callBotfamTool(t, home, filepath.Join(worktrees, "wt-codex"), bin, "send", map[string]any{
		"to":      "agy",
		"type":    "smoke",
		"payload": map[string]any{"msg": "hello"},
	})
	if send["from"] != "codex" || send["to"] != "agy" {
		t.Fatalf("send envelope = %#v, want from codex to agy", send)
	}

	got := callBotfamTool(t, home, filepath.Join(worktrees, "wt-agy"), bin, "try_recv", map[string]any{})
	if got["id"] != send["id"] || got["from"] != "codex" || got["to"] != "agy" {
		t.Fatalf("received envelope = %#v, want sent envelope %#v", got, send)
	}
}

func runBootstrap(t *testing.T, home, script, repo string, args ...string) {
	t.Helper()
	allArgs := append([]string{script, repo}, args...)
	cmd := exec.Command("sh", allArgs...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s failed: %v\n%s", strings.Join(cmd.Args, " "), err, out.String())
	}
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
