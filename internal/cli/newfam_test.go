package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewfam(t *testing.T) {
	tempDir := t.TempDir()
	mainDir := filepath.Join(tempDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	initGitRepo(t, mainDir)

	// Set environment variables for the test
	collabRoot := filepath.Join(tempDir, "collab")
	t.Setenv("COLLAB_ROOT", collabRoot)
	t.Setenv("USER", "testoperator")
	t.Setenv("HOME", tempDir)

	// Create mock home .botfam directory to hold symlinks
	if err := os.MkdirAll(filepath.Join(tempDir, ".botfam"), 0755); err != nil {
		t.Fatal(err)
	}

	// Change directory to main repo root
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(mainDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	// Run NewfamCmd
	var out bytes.Buffer
	args := []string{"myproject", "--agents", "agy,claude"}
	if err := NewfamCmd(args, &out); err != nil {
		t.Fatalf("NewfamCmd failed: %v\nOutput:\n%s", err, out.String())
	}

	// Check if the registry fam.toml was written correctly
	regPath := filepath.Join(collabRoot, "fam.toml")
	reg, err := ReadRegistry(regPath)
	if err != nil {
		t.Fatalf("failed to read registry at %s: %v", regPath, err)
	}
	if reg.Name != "myproject" {
		t.Errorf("expected registry name 'myproject', got %q", reg.Name)
	}
	if len(reg.WikiProjections) == 0 || reg.WikiProjections[0] != "memory:memory-*" {
		t.Errorf("expected WikiProjections to contain 'memory:memory-*', got %v", reg.WikiProjections)
	}

	// Verify that roster contains all agents and the operator
	expectedRoster := []string{"agy", "claude", "testoperator"}
	rosterMap := make(map[string]bool)
	for _, member := range reg.Roster {
		rosterMap[member] = true
	}
	for _, member := range expectedRoster {
		if !rosterMap[member] {
			t.Errorf("missing %q from roster: %v", member, reg.Roster)
		}
	}

	// Verify that the worktree directories exist
	for _, actor := range expectedRoster {
		wtDir := filepath.Join(tempDir, "wt-"+actor)
		if _, err := os.Stat(wtDir); os.IsNotExist(err) {
			t.Errorf("worktree directory %s does not exist", wtDir)
		}

		// Verify Claude settings file
		claudeSettings := filepath.Join(wtDir, ".claude", "settings.json")
		if _, err := os.Stat(claudeSettings); os.IsNotExist(err) {
			t.Errorf("claude settings %s does not exist", claudeSettings)
		}

		// Verify Agent docs files
		for _, docName := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
			docPath := filepath.Join(wtDir, docName)
			if _, err := os.Stat(docPath); os.IsNotExist(err) {
				t.Errorf("agent doc %s does not exist", docPath)
			}
		}
	}
}

func TestWikiRemoteURL(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	addRemote := func(name, url string) {
		t.Helper()
		cmd := exec.Command("git", "remote", "add", name, url)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git remote add %s: %v", name, err)
		}
	}

	// No remote → error.
	if _, err := wikiRemoteURL(dir); err == nil {
		t.Errorf("expected error with no remote, got nil")
	}

	// origin fallback when gitea is absent.
	addRemote("origin", "https://github.com/botfam/botfam.git")
	got, err := wikiRemoteURL(dir)
	if err != nil {
		t.Fatalf("wikiRemoteURL with origin: %v", err)
	}
	if want := "https://github.com/botfam/botfam.wiki.git"; got != want {
		t.Errorf("origin: got %q, want %q", got, want)
	}

	// gitea takes precedence over origin.
	addRemote("gitea", "http://gitea:3000/botfam/botfam.git")
	got, err = wikiRemoteURL(dir)
	if err != nil {
		t.Fatalf("wikiRemoteURL with gitea: %v", err)
	}
	if want := "http://gitea:3000/botfam/botfam.wiki.git"; got != want {
		t.Errorf("gitea: got %q, want %q", got, want)
	}
}

func TestWriteClaudeSettingsPreservesFields(t *testing.T) {
	tempDir := t.TempDir()
	claudeDir := filepath.Join(tempDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	settingsFile := filepath.Join(claudeDir, "settings.json")
	initialContent := `{
  "attribution": "test-model",
  "env": {
    "FOO": "bar"
  },
  "enabledMcpjsonServers": [
    "other-server",
    "collab"
  ],
  "permissions": {
    "allow": [
      "mcp__collab__*",
      "Bash(git status:*)"
    ],
    "deny": [
      "Bash(rm:*)"
    ]
  }
}`
	if err := os.WriteFile(settingsFile, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run writeClaudeSettings
	if err := writeClaudeSettings(tempDir); err != nil {
		t.Fatalf("writeClaudeSettings failed: %v", err)
	}

	// Read and verify the result
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal settings.json: %v", err)
	}

	// Verify preserved top-level fields
	if parsed["attribution"] != "test-model" {
		t.Errorf("expected attribution to be 'test-model', got %v", parsed["attribution"])
	}

	env, ok := parsed["env"].(map[string]interface{})
	if !ok || env["FOO"] != "bar" {
		t.Errorf("expected env.FOO to be 'bar', got %v", parsed["env"])
	}

	// Verify mutated enabledMcpjsonServers (should filter out "collab", keep "other-server")
	servers, ok := parsed["enabledMcpjsonServers"].([]interface{})
	if !ok {
		t.Fatalf("expected enabledMcpjsonServers to be slice, got %v", parsed["enabledMcpjsonServers"])
	}
	var foundOther, foundCollab bool
	for _, s := range servers {
		if s == "other-server" {
			foundOther = true
		}
		if s == "collab" {
			foundCollab = true
		}
	}
	if !foundOther {
		t.Errorf("expected other-server to remain in enabledMcpjsonServers")
	}
	if foundCollab {
		t.Errorf("expected collab to be removed from enabledMcpjsonServers")
	}

	// Verify permissions
	permissions, ok := parsed["permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected permissions to be object, got %v", parsed["permissions"])
	}

	// Verify permissions.deny is preserved
	deny, ok := permissions["deny"].([]interface{})
	if !ok || len(deny) != 1 || deny[0] != "Bash(rm:*)" {
		t.Errorf("expected permissions.deny to be preserved as ['Bash(rm:*)'], got %v", permissions["deny"])
	}

	// Verify permissions.allow contains all allowed commands, is sorted, and does not contain mcp__collab__*
	allow, ok := permissions["allow"].([]interface{})
	if !ok {
		t.Fatalf("expected permissions.allow to be slice, got %v", permissions["allow"])
	}

	var foundCollabAllow bool
	var foundGitStatus bool
	for _, a := range allow {
		if a == "mcp__collab__*" {
			foundCollabAllow = true
		}
		if a == "Bash(git status:*)" {
			foundGitStatus = true
		}
	}
	if foundCollabAllow {
		t.Errorf("expected mcp__collab__* to be removed from permissions.allow")
	}
	if !foundGitStatus {
		t.Errorf("expected Bash(git status:*) to be in permissions.allow")
	}

	// Total length should be our 14 allowed commands
	if len(allow) != 14 {
		t.Errorf("expected permissions.allow length to be 14, got %d", len(allow))
	}
}

func TestNewfamMCPSelfDiscoverability(t *testing.T) {
	tempDir := t.TempDir()
	mainDir := filepath.Join(tempDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	initGitRepo(t, mainDir)

	// Set environment variables for the test
	collabRoot := filepath.Join(tempDir, "collab")
	t.Setenv("COLLAB_ROOT", collabRoot)
	t.Setenv("USER", "testoperator")
	t.Setenv("HOME", tempDir)

	// Create mock home .botfam directory to hold symlinks
	if err := os.MkdirAll(filepath.Join(tempDir, ".botfam"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create parent directory for antigravity config so it is not skipped
	if err := os.MkdirAll(filepath.Join(tempDir, ".gemini", "antigravity"), 0755); err != nil {
		t.Fatal(err)
	}

	// Change directory to main repo root
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(mainDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	// Run NewfamCmd
	var out bytes.Buffer
	args := []string{"testfam", "--agents", "agy,claude"}
	if err := NewfamCmd(args, &out); err != nil {
		t.Fatalf("NewfamCmd failed: %v\nOutput:\n%s", err, out.String())
	}

	// 1. Verify that the harness pointers are slim
	wtDir := filepath.Join(tempDir, "wt-agy")
	pointerPath := filepath.Join(wtDir, "AGENTS.md")
	content, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md pointer: %v", err)
	}
	if bytes.Contains(content, []byte("botfam Coordination Protocol (IRC-First)")) {
		t.Errorf("pointer file should be slimmed down, but contains full protocol text")
	}
	if !bytes.Contains(content, []byte("botfam:///docs/protocol")) {
		t.Errorf("pointer file missing link to botfam:///docs/protocol")
	}

	// 2. Verify global config files are written and contain collab MCP server
	configPaths := []string{
		filepath.Join(tempDir, ".gemini", "antigravity", "mcp_config.json"),
		filepath.Join(tempDir, ".claude.json"),
	}

	execPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	execPath, err = filepath.Abs(execPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range configPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read global config at %s: %v", path, err)
		}

		var config struct {
			McpServers map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("failed to parse global config at %s: %v", path, err)
		}

		collab, ok := config.McpServers["collab"]
		if !ok {
			t.Errorf("collab MCP server not registered in %s", path)
			continue
		}
		if collab.Command != execPath {
			t.Errorf("collab MCP server command in %s is %q, expected %q", path, collab.Command, execPath)
		}
		if len(collab.Args) != 1 || collab.Args[0] != "serve" {
			t.Errorf("collab MCP server args in %s are not ['serve']: %v", path, collab.Args)
		}
		if collab.Env == nil || collab.Env["PATH"] != os.Getenv("PATH") {
			t.Errorf("collab MCP server env.PATH in %s is %q, expected %q", path, collab.Env["PATH"], os.Getenv("PATH"))
		}
	}
}
